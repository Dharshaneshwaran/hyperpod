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
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	_, existed := r.workers[w.ID]
	w.LastHeartbeat = time.Now()
	r.workers[w.ID] = w
	if !existed {
		log.Printf("registered worker %q (%s, %s) at %s", w.ID, w.GPU, w.OS, w.Address)
	}
}

func (r *registry) isKnown(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.workers[id]
	return ok
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
		if w.healthy() && w.RPCAddr != "" && !isLocalEndpoint(w.RPCAddr) {
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
	Tier     string `json:"tier"` // "small" (1B, fast single) or "big" (3B split, default)
}

type inferResp struct {
	OK       bool   `json:"ok"`
	WorkerID string `json:"worker_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Error    string `json:"error,omitempty"`
	// Placement is where the model weights really live, from llama.cpp's log.
	Placement string `json:"placement,omitempty"`
	Split     bool   `json:"split"`
	// Loading is true when the big-tier model is still loading.
	Loading bool `json:"loading,omitempty"`
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
	// pool returns the RPC endpoints the big-tier pipeline may split across.
	pool func() []string
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
		// Tier routing: "small" -> fast single 1B model; "big"/default -> split 3B.
		if req.Tier == "small" && s.sup.hasSmall() {
			s.handleInferSmall(w, req)
			return
		}
		s.handleInferSupervised(w, req)
		return
	}
	s.handleInferDispatch(w, req)
}

// handleInferSmall serves the request from the fast, always-local small-tier model.
func (s *server) handleInferSmall(w http.ResponseWriter, req inferReq) {
	// Ensure the small server is up (launch lazily once).
	s.sup.mu.Lock()
	up := s.sup.smallUp
	s.sup.mu.Unlock()
	if !up {
		if err := s.sup.launchSmall(); err != nil {
			writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("small tier launch failed: %v", err)})
			return
		}
		if err := s.sup.waitReadyURL(s.sup.smallBaseURL(), 90*time.Second); err != nil {
			writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("small tier not ready: %v", err)})
			return
		}
	}
	text, err := s.sup.completionAt(context.Background(), s.sup.smallBaseURL(), req.Prompt, req.NPredict)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("small tier inference failed: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, inferResp{OK: true, WorkerID: "small tier · 1B", Text: text, Placement: "this laptop only (no split)"})
}

// handleInferSupervised runs the prompt through the supervised split pipeline.
//
// Rules (each one fixes a bug seen in testing):
//   - If the pipeline is still LOADING, wait for it. Loading is not failure.
//   - A 503 "Loading model" reply is also treated as "wait", never as death.
//   - Only rebuild when the llama-server process has actually EXITED (the
//     verified signal that a remote node died). A live process is never killed.
//   - The response reports where the weights really are, parsed from
//     llama.cpp's own load log, instead of a fixed label.
func (s *server) handleInferSupervised(w http.ResponseWriter, req inferReq) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := s.sup.ensure(s.pool, s.sup.inferWait); err != nil {
			if errors.Is(err, errLoading) {
				// Still loading: answer now instead of hanging. The load continues.
				info := s.sup.info()
				writeJSON(w, http.StatusServiceUnavailable, inferResp{
					OK:      false,
					Loading: true,
					Error: fmt.Sprintf("big-tier model is still loading (%.0fs so far); try again shortly",
						info.LoadingSecs),
				})
				return
			}
			lastErr = fmt.Errorf("pipeline not available: %w", err)
			log.Printf("infer attempt %d: %v", attempt, lastErr)
			continue
		}
		text, err := s.sup.completion(context.Background(), req.Prompt, req.NPredict)
		if err == nil {
			info := s.sup.info()
			label := "big tier · local only"
			if info.Split {
				label = "big tier · split"
			}
			writeJSON(w, http.StatusOK, inferResp{
				OK:        true,
				WorkerID:  label,
				Text:      text,
				Placement: info.describe(),
				Split:     info.Split,
			})
			return
		}
		lastErr = err
		if strings.Contains(err.Error(), "status 503") {
			log.Printf("infer attempt %d: model still loading, waiting", attempt)
			continue // ensure() will wait for readiness on the next loop
		}
		if s.sup.isRunning() {
			// Process is alive: this is a request error, not a dead node.
			log.Printf("infer attempt %d failed but llama-server is still running: %v", attempt, err)
			break
		}
		log.Printf("infer attempt %d failed and llama-server exited (a node died): %v -- rebuilding and re-running", attempt, err)
	}
	writeJSON(w, http.StatusBadGateway, inferResp{OK: false, Error: fmt.Sprintf("inference failed: %v", lastErr)})
}

// handlePipeline reports the big-tier pipeline's real state and placement.
func (s *server) handlePipeline(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": false})
		return
	}
	info := s.sup.info()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"enabled":     true,
		"pipeline":    info,
		"description": info.describe(),
	})
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
		// Local is true when this worker runs on the controller's own laptop.
		// It is shown in the dashboard but never used as a remote split node.
		Local bool `json:"local"`
	}
	snap := s.reg.snapshot()
	rows := make([]row, 0, len(snap))
	for _, wk := range snap {
		rows = append(rows, row{
			worker:       wk,
			Healthy:      wk.healthy(),
			LastSeenSecs: time.Since(wk.LastHeartbeat).Seconds(),
			Local:        wk.RPCAddr != "" && isLocalEndpoint(wk.RPCAddr),
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

	// LAN auto-discovery: listen for worker UDP broadcasts and auto-register them.
	go s.startDiscovery()

	// Supervisor (Path A) mode: active only if llama-server + model are configured.
	if sup, ok := newSupervisor(); ok {
		s.sup = sup
		log.Printf("supervisor mode ENABLED (llama-server=%s model=%s)", sup.llamaServer, sup.model)

		// The pool used for every (re)build: auto-discovered workers plus any
		// explicitly configured ones that are reachable. The controller's own
		// laptop is never included (its GPU is already the local device).
		// Probing is safe here: it only runs when no llama-server holds the
		// single-client RPC connections.
		configured := append([]string(nil), sup.rpcWorkers...)
		s.pool = func() []string {
			seen := map[string]bool{}
			var out []string
			for _, ep := range s.reg.healthyRPCEndpoints() {
				if !seen[ep] {
					seen[ep] = true
					out = append(out, ep)
				}
			}
			for _, ep := range configured {
				if !seen[ep] && probeAlive(ep, 2*time.Second) {
					seen[ep] = true
					out = append(out, ep)
				}
			}
			return out
		}
		go sup.monitorWorkers(s.pool)
		// If explicit RPC workers were provided, load the split now so the first
		// request does not pay the load time. Otherwise it loads on first /infer.
		if len(configured) > 0 {
			go func() {
				// Launch only; the readiness watcher logs "pipeline ready" when done.
				if err := sup.ensure(s.pool, 0); err != nil {
					log.Printf("supervisor: initial launch failed: %v", err)
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
	mux.HandleFunc("GET /pipeline", s.handlePipeline)

	log.Printf("HyperPot controller listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatalf("controller failed: %v", err)
	}
}

// withCORS allows the React dashboard (served from a different origin/port) to
// call the controller from the browser. Also answers CORS preflight (OPTIONS).
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
