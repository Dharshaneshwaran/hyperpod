// HyperPot Controller (Phase 1)
//
// The coordinator. It:
//   - lets workers register        (POST /register)
//   - tracks worker liveness        (POST /heartbeat)
//   - accepts user prompts          (POST /infer)  and dispatches to a worker
//   - exposes a debug view          (GET  /status)
//
// Phase 1 scope: a single worker on the same machine. The design already
// supports many workers so later phases can add pipeline split + failover.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// heartbeatTimeout: a worker is considered dead if we have not heard from it
// within this window. This is the hook that Phase 3 failover will build on.
const heartbeatTimeout = 10 * time.Second

// worker is one registered worker agent.
type worker struct {
	ID            string    `json:"worker_id"`
	Address       string    `json:"address"`
	RPCAddr       string    `json:"rpc_addr"` // host:port of this worker's ggml-rpc-server
	GPU           string    `json:"gpu"`
	OS            string    `json:"os"`
	LastHeartbeat time.Time `json:"-"`
}

func (w *worker) healthy() bool {
	return time.Since(w.LastHeartbeat) < heartbeatTimeout
}

// registry holds all known workers, guarded by a mutex.
type registry struct {
	mu      sync.Mutex
	workers map[string]*worker
}

func newRegistry() *registry {
	return &registry{workers: make(map[string]*worker)}
}

func (r *registry) register(w *worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.LastHeartbeat = time.Now()
	r.workers[w.ID] = w
	log.Printf("registered worker %q (%s, %s) at %s", w.ID, w.GPU, w.OS, w.Address)
}

func (r *registry) heartbeat(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return false
	}
	w.LastHeartbeat = time.Now()
	return true
}

// pickHealthy returns any currently-healthy worker (Phase 1: just the first).
func (r *registry) pickHealthy() *worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range r.workers {
		if w.healthy() {
			return w
		}
	}
	return nil
}

func (r *registry) snapshot() []worker {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]worker, 0, len(r.workers))
	for _, w := range r.workers {
		out = append(out, *w)
	}
	return out
}

// healthyRPCEndpoints returns the host:port RPC endpoints of all currently
// healthy workers that advertised one. This is the set the supervisor splits over.
func (r *registry) healthyRPCEndpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var eps []string
	for _, w := range r.workers {
		if w.healthy() && w.RPCAddr != "" {
			eps = append(eps, w.RPCAddr)
		}
	}
	return eps
}

// ---- wire types -------------------------------------------------------

type registerReq struct {
	WorkerID string `json:"worker_id"`
	Address  string `json:"address"`
	RPCAddr  string `json:"rpc_addr"`
	GPU      string `json:"gpu"`
	OS       string `json:"os"`
}

type heartbeatReq struct {
	WorkerID string `json:"worker_id"`
}

type inferReq struct {
	Prompt   string `json:"prompt"`
	NPredict int    `json:"n_predict"`
}

type inferResp struct {
	OK       bool   `json:"ok"`
	WorkerID string `json:"worker_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Error    string `json:"error,omitempty"`
}

// workerRunReq/Resp mirror the worker's POST /run contract.
type workerRunReq struct {
	Prompt   string `json:"prompt"`
	NPredict int    `json:"n_predict"`
}

type workerRunResp struct {
	OK    bool   `json:"ok"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// ---- server -----------------------------------------------------------

type server struct {
	reg    *registry
	client *http.Client
	sup    *supervisor // non-nil when supervisor (Path A) mode is active
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorkerID == "" || req.Address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad register payload"})
		return
	}
	s.reg.register(&worker{ID: req.WorkerID, Address: req.Address, RPCAddr: req.RPCAddr, GPU: req.GPU, OS: req.OS})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorkerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad heartbeat payload"})
		return
	}
	if !s.reg.heartbeat(req.WorkerID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown worker"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleInfer dispatches the user's prompt. In supervisor (Path A) mode it goes
// through the supervised llama-server with automatic detect+recover+rerun.
// Otherwise it falls back to Phase 1 single-worker dispatch.
func (s *server) handleInfer(w http.ResponseWriter, r *http.Request) {
	var req inferReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, inferResp{OK: false, Error: "bad infer payload"})
		return
	}
	if req.NPredict <= 0 {
		req.NPredict = 64
	}

	if s.sup != nil {
		s.handleInferSupervised(w, req)
		return
	}
	s.handleInferDispatch(w, req)
}

// handleInferSupervised runs the prompt through the supervised split pipeline,
// and on failure (node death / llama-server crash) recovers once and re-runs.
func (s *server) handleInferSupervised(w http.ResponseWriter, req inferReq) {
	const maxAttempts = 2
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !s.sup.isRunning() {
			log.Printf("infer: pipeline down, (re)building split over survivors (attempt %d)", attempt)
			if err := s.recoverPipeline(); err != nil {
				lastErr = fmt.Errorf("recovery failed: %w", err)
				continue
			}
		}
		text, err := s.sup.completion(context.Background(), req.Prompt, req.NPredict)
		if err == nil {
			writeJSON(w, http.StatusOK, inferResp{OK: true, WorkerID: "supervised-split", Text: text})
			return
		}
		lastErr = err
		log.Printf("infer attempt %d failed: %v -- the pipeline likely lost a node; will recover and re-run", attempt, err)
		// Mark pipeline down so the next loop recovers.
		s.sup.mu.Lock()
		s.sup.stopLocked()
		s.sup.mu.Unlock()
	}
	writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("inference failed after recovery: %v", lastErr)})
}

// recoverPipeline relaunches llama-server over the currently-healthy RPC workers.
func (s *server) recoverPipeline() error {
	workers := s.reg.healthyRPCEndpoints()
	s.sup.mu.Lock()
	err := s.sup.launchLocked(workers)
	s.sup.mu.Unlock()
	if err != nil {
		return err
	}
	return s.sup.waitReady(90 * time.Second)
}

// handleInferDispatch is the Phase 1 path: send the whole prompt to one worker.
func (s *server) handleInferDispatch(w http.ResponseWriter, req inferReq) {
	wrk := s.reg.pickHealthy()
	if wrk == nil {
		writeJSON(w, http.StatusServiceUnavailable, inferResp{OK: false, Error: "no healthy worker"})
		return
	}

	body, _ := json.Marshal(workerRunReq{Prompt: req.Prompt, NPredict: req.NPredict})
	log.Printf("dispatching infer to worker %q at %s", wrk.ID, wrk.Address)
	resp, err := s.client.Post(wrk.Address+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("worker call failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	var wr workerRunResp
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: "bad worker response"})
		return
	}
	if !wr.OK {
		writeJSON(w, http.StatusBadGateway, inferResp{OK: false, WorkerID: wrk.ID, Error: wr.Error})
		return
	}
	writeJSON(w, http.StatusOK, inferResp{OK: true, WorkerID: wrk.ID, Text: wr.Text})
}

func (s *server) handleStatus(w http.ResponseWriter, r *http.Request) {
	type row struct {
		worker
		Healthy      bool    `json:"healthy"`
		LastSeenSecs float64 `json:"last_seen_secs"`
	}
	snap := s.reg.snapshot()
	rows := make([]row, 0, len(snap))
	for _, wk := range snap {
		rows = append(rows, row{
			worker:       wk,
			Healthy:      wk.healthy(),
			LastSeenSecs: time.Since(wk.LastHeartbeat).Seconds(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workers": rows})
}

func main() {
	addr := os.Getenv("HYPERPOT_CONTROLLER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	s := &server{
		reg:    newRegistry(),
		client: &http.Client{Timeout: 120 * time.Second},
	}

	// Supervisor (Path A) mode: active only if llama-server + model are configured.
	if sup, ok := newSupervisor(); ok {
		s.sup = sup
		log.Printf("supervisor mode ENABLED (llama-server=%s model=%s)", sup.llamaServer, sup.model)
		// If explicit RPC workers were provided, launch the split pipeline now.
		// Otherwise it launches lazily on the first /infer once workers register.
		if len(sup.rpcWorkers) > 0 {
			go func() {
				sup.mu.Lock()
				err := sup.launchLocked(sup.rpcWorkers)
				sup.mu.Unlock()
				if err != nil {
					log.Printf("supervisor: initial launch failed: %v", err)
					return
				}
				if err := sup.waitReady(90 * time.Second); err != nil {
					log.Printf("supervisor: initial pipeline not ready: %v", err)
				} else {
					log.Printf("supervisor: pipeline ready at %s", sup.baseURL())
				}
			}()
		}
	} else {
		log.Printf("supervisor mode disabled (Phase 1 dispatch mode)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /infer", s.handleInfer)
	mux.HandleFunc("GET /status", s.handleStatus)

	log.Printf("HyperPot controller listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("controller failed: %v", err)
	}
}
