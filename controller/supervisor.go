// HyperPot Controller — Supervisor (Phase 2, Path A)
//
// Supervises a real llama.cpp RPC pipeline: one model split across the
// registered healthy workers via `llama-server --rpc ... --tensor-split ...`.
// On node death the controller recomputes the split over survivors, relaunches
// llama-server, and re-runs the in-flight request.
//
// Config (env):
//   HYPERPOT_LLAMA_SERVER   path to llama-server binary (required for supervisor)
//   HYPERPOT_MODEL          path to .gguf model (required for supervisor)
//   HYPERPOT_SERVER_HOST    host llama-server binds to (default 127.0.0.1)
//   HYPERPOT_SERVER_PORT    port llama-server binds to (default 9931)
//   HYPERPOT_NGL            gpu layers (default 99)
//   HYPERPOT_RPC_WORKERS    comma list host:port of ggml-rpc-server endpoints.
//                           If set, these are used directly as the split targets.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type supervisor struct {
	mu sync.Mutex
	// rebuildMu serializes launch/wait so only one (re)build runs at a time.
	rebuildMu sync.Mutex

	llamaServer string
	model       string
	host        string
	port        string
	ngl         string

	// small-tier model: a separate always-local llama-server (no split) for fast
	// responses. Optional; enabled when HYPERPOT_SMALL_MODEL is set.
	smallModel string
	smallPort  string
	smallCmd   *exec.Cmd
	smallUp    bool

	// rpcWorkers is the list of ggml-rpc-server endpoints (host:port) to split across.
	rpcWorkers []string

	cmd     *exec.Cmd
	running bool
	// ready is true once the current generation answered /health with 200.
	ready bool
	// generation increments every (re)launch; used to detect restarts.
	generation int

	// loadTimeout bounds how long a (re)launch may take to load the model.
	// Loading a split model streams weights over Wi-Fi, so this must be generous.
	loadTimeout time.Duration

	// placement is parsed from llama-server's own load log: how many MiB of
	// model weights each device (local GPU, CPU, or RPC node) actually holds.
	placement []devicePlacement
	offloaded string // e.g. "29/29" layers offloaded to non-CPU devices
}

// devicePlacement is one line of llama.cpp's "model buffer size" report.
type devicePlacement struct {
	Device string  `json:"device"` // e.g. "CUDA0", "RPC0[192.168.1.38:50052]", "CPU_Mapped"
	MiB    float64 `json:"mib"`
}

// pipelineInfo is the observable state of the big-tier pipeline.
type pipelineInfo struct {
	Generation int               `json:"generation"`
	State      string            `json:"state"` // down | loading | ready
	RPCWorkers []string          `json:"rpc_workers"`
	Devices    []devicePlacement `json:"devices"`
	Offloaded  string            `json:"offloaded"`
	Split      bool              `json:"split"` // true only if a remote RPC device holds weights
}

var (
	reBuffer    = regexp.MustCompile(`load_tensors:\s+(\S+) model buffer size\s*=\s*([\d.]+) MiB`)
	reOffloaded = regexp.MustCompile(`offloaded (\d+/\d+) layers`)
)

func newSupervisor() (*supervisor, bool) {
	ls := os.Getenv("HYPERPOT_LLAMA_SERVER")
	model := os.Getenv("HYPERPOT_MODEL")
	if ls == "" || model == "" {
		return nil, false // supervisor mode not configured
	}
	host := envDefault("HYPERPOT_SERVER_HOST", "127.0.0.1")
	port := envDefault("HYPERPOT_SERVER_PORT", "9931")
	ngl := envDefault("HYPERPOT_NGL", "99")

	var workers []string
	if rw := os.Getenv("HYPERPOT_RPC_WORKERS"); rw != "" {
		for _, p := range strings.Split(rw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				if isLocalEndpoint(p) {
					log.Printf("supervisor: ignoring RPC worker %s (it is this machine; its GPU is already the local device)", p)
					continue
				}
				workers = append(workers, p)
			}
		}
	}
	loadSecs, err := strconv.Atoi(envDefault("HYPERPOT_LOAD_TIMEOUT_SECS", "300"))
	if err != nil || loadSecs <= 0 {
		loadSecs = 300
	}
	return &supervisor{
		llamaServer: ls,
		model:       model,
		host:        host,
		port:        port,
		ngl:         ngl,
		smallModel:  os.Getenv("HYPERPOT_SMALL_MODEL"), // optional
		smallPort:   envDefault("HYPERPOT_SMALL_PORT", "9932"),
		rpcWorkers:  workers,
		loadTimeout: time.Duration(loadSecs) * time.Second,
	}, true
}

// localIPs returns every IP address bound to this machine.
func localIPs() map[string]bool {
	ips := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			ips[ipn.IP.String()] = true
		}
	}
	return ips
}

// isLocalEndpoint reports whether host:port points at this machine. Splitting
// "over" our own rpc-server would put the same GPU in the pipeline twice, which
// is not a real split, so such endpoints are excluded from the pool.
func isLocalEndpoint(endpoint string) bool {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	return localIPs()[host]
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (s *supervisor) baseURL() string {
	return fmt.Sprintf("http://%s:%s", s.host, s.port)
}

func (s *supervisor) smallBaseURL() string {
	return fmt.Sprintf("http://%s:%s", s.host, s.smallPort)
}

func (s *supervisor) hasSmall() bool {
	return s.smallModel != ""
}

// launchSmall starts the always-local small-tier llama-server (no split).
func (s *supervisor) launchSmall() error {
	if !s.hasSmall() {
		return fmt.Errorf("small model not configured")
	}
	args := []string{
		"-m", s.smallModel,
		"-ngl", s.ngl,
		"--host", s.host,
		"--port", s.smallPort,
		"-c", envDefault("HYPERPOT_CTX", "2048"),
	}
	cmd := exec.Command(s.llamaServer, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start small llama-server: %w", err)
	}
	s.mu.Lock()
	s.smallCmd = cmd
	s.smallUp = true
	s.mu.Unlock()
	log.Printf("supervisor: launched SMALL-tier llama-server on %s (model=%s)", s.smallBaseURL(), s.smallModel)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.smallUp = false
		s.mu.Unlock()
		log.Printf("supervisor: small-tier llama-server exited: %v", err)
	}()
	return nil
}

// waitReadyURL polls a given base URL's /health until ready or timeout.
func (s *supervisor) waitReadyURL(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server at %s not ready within %s", base, timeout)
}

// evenSplit returns a comma tensor-split string giving each worker an equal share.
func evenSplit(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "1"
	}
	return strings.Join(parts, ",")
}

// remoteHeavySplit builds a tensor-split where the LOCAL device gets a small
// share (1) and each of the nWorkers REMOTE nodes gets a large share (4), so
// that the remote nodes genuinely hold most of the model. Losing a remote node
// then actually breaks generation, which is what we want to recover from.
// Layout: "local,remote1,remote2,..."
func remoteHeavySplit(nWorkers int) string {
	parts := make([]string, 0, nWorkers+1)
	parts = append(parts, "1") // local device: small share
	for i := 0; i < nWorkers; i++ {
		parts = append(parts, "4") // each remote: large share
	}
	return strings.Join(parts, ",")
}

// launch (re)starts llama-server with the current set of rpc workers.
// Caller must hold s.mu.
func (s *supervisor) launchLocked(workers []string) error {
	s.stopLocked()

	args := []string{
		"-m", s.model,
		"-ngl", s.ngl,
		"--host", s.host,
		"--port", s.port,
		"-c", envDefault("HYPERPOT_CTX", "2048"),
		// Verbosity 4 makes llama.cpp print where every model buffer lives
		// ("<device> model buffer size = N MiB"). We parse that as ground truth.
		"-lv", "4",
	}
	if len(workers) > 0 {
		args = append(args, "--rpc", strings.Join(workers, ","))
		// Weight the split heavily toward the REMOTE workers so that losing a
		// remote node genuinely breaks generation (local keeps a small share).
		// Order of --tensor-split matches: local device first, then each --rpc worker.
		args = append(args, "--tensor-split", remoteHeavySplit(len(workers)))
	}

	cmd := exec.Command(s.llamaServer, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // llama.cpp logs to stderr; merge into one stream
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}
	s.cmd = cmd
	s.running = true
	s.ready = false
	s.placement = nil
	s.offloaded = ""
	s.rpcWorkers = workers
	s.generation++
	gen := s.generation
	log.Printf("supervisor: launched llama-server (gen %d) split over %v", gen, workers)

	// Stream llama-server's log: keep only the useful lines, and capture the
	// device placement it reports while loading.
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if m := reBuffer.FindStringSubmatch(line); m != nil {
				mib, _ := strconv.ParseFloat(m[2], 64)
				s.mu.Lock()
				if s.generation == gen {
					s.placement = append(s.placement, devicePlacement{Device: m[1], MiB: mib})
				}
				s.mu.Unlock()
				log.Printf("placement (gen %d): %s holds %.1f MiB of model weights", gen, m[1], mib)
			} else if m := reOffloaded.FindStringSubmatch(line); m != nil {
				s.mu.Lock()
				if s.generation == gen {
					s.offloaded = m[1]
				}
				s.mu.Unlock()
				log.Printf("placement (gen %d): offloaded %s layers to non-CPU devices", gen, m[1])
			} else if strings.Contains(line, " E ") || strings.Contains(line, "RPC") ||
				strings.Contains(line, "eval time") || strings.Contains(line, "listening") {
				log.Printf("llama-server[%d] %s", gen, strings.TrimSpace(line))
			}
		}
	}()

	// Reap the process in the background; mark not-running on exit.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.generation == gen {
			s.running = false
			s.ready = false
			log.Printf("supervisor: llama-server (gen %d) exited: %v", gen, err)
		}
		s.mu.Unlock()
	}()
	return nil
}

func (s *supervisor) stopLocked() {
	if s.cmd != nil && s.cmd.Process != nil && s.running {
		_ = s.cmd.Process.Kill()
	}
	s.running = false
	s.ready = false
	s.cmd = nil
}

// waitReady polls llama-server's /health until ready, the process exits, or
// timeout. A 503 from /health means "still loading"; we keep waiting.
func (s *supervisor) waitReady(timeout time.Duration) error {
	s.mu.Lock()
	gen := s.generation
	s.mu.Unlock()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running, sameGen := s.running, s.generation == gen
		s.mu.Unlock()
		if !sameGen {
			return fmt.Errorf("pipeline was relaunched while waiting")
		}
		if !running {
			return fmt.Errorf("llama-server exited while loading")
		}
		resp, err := client.Get(s.baseURL() + "/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				s.mu.Lock()
				if s.generation == gen {
					s.ready = true
				}
				s.mu.Unlock()
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("llama-server not ready within %s", timeout)
}

// ensure makes the big-tier pipeline usable: if no llama-server is running it
// launches one over workersFn(), then waits until it is ready. Calls are
// serialized so the infer path and the monitor can never launch two
// pipelines at once or kill one that is still loading.
func (s *supervisor) ensure(workersFn func() []string) error {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()
	s.mu.Lock()
	if !s.running {
		if err := s.launchLocked(workersFn()); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	ready := s.ready
	s.mu.Unlock()
	if ready {
		return nil
	}
	return s.waitReady(s.loadTimeout)
}

// info returns the observable state of the big-tier pipeline.
func (s *supervisor) info() pipelineInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "down"
	if s.running {
		state = "loading"
		if s.ready {
			state = "ready"
		}
	}
	devs := append([]devicePlacement(nil), s.placement...)
	split := false
	for _, d := range devs {
		if strings.HasPrefix(d.Device, "RPC") && d.MiB > 0 {
			split = true
		}
	}
	return pipelineInfo{
		Generation: s.generation,
		State:      state,
		RPCWorkers: append([]string(nil), s.rpcWorkers...),
		Devices:    devs,
		Offloaded:  s.offloaded,
		Split:      split,
	}
}

// describe renders the real placement, e.g.
// "CUDA0 1262 MiB + RPC0[192.168.1.38:50052] 1002 MiB".
func (p pipelineInfo) describe() string {
	var parts []string
	for _, d := range p.Devices {
		if d.MiB <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %.0f MiB", d.Device, d.MiB))
	}
	if len(parts) == 0 {
		return "placement unknown"
	}
	return strings.Join(parts, " + ")
}

func (s *supervisor) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// activeWorkers returns the RPC endpoints the current pipeline was launched with.
func (s *supervisor) activeWorkers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.rpcWorkers))
	copy(out, s.rpcWorkers)
	return out
}

// probeAlive reports whether a host:port accepts a TCP connection.
func probeAlive(endpoint string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", endpoint, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// monitorWorkers runs in the background. It TCP-probes every active RPC worker
// of the current pipeline; if any is unreachable it proactively tears the
// pipeline down and rebuilds it over the survivors (reported by healthyFn).
// This makes recovery DETERMINISTIC: it fires on a real node death even if
// llama-server itself would have limped along on cached weights.
// monitorWorkers runs in the background and detects a dead RPC node using the
// ONE unambiguous signal verified in Phase 0: when a remote RPC node truly
// dies, llama.cpp ABORTS and the llama-server PROCESS EXITS.
//
// We deliberately do NOT probe the RPC port (ggml-rpc-server is single-client
// and refuses extra connections -> false positives) nor poll /health (a busy
// server mid-generation is slow to answer -> false positives). Process exit is
// unambiguous: a busy/slow server is still running; a dead-node server exits.
//
// So: if a pipeline that was launched WITH remote workers stops running (the
// supervised process exited) and we are not already mid-rebuild, rebuild over
// the survivors reported by healthyFn.
func (s *supervisor) monitorWorkers(healthyFn func() []string) {
	for {
		time.Sleep(1 * time.Second)

		s.mu.Lock()
		running := s.running
		hadWorkers := len(s.rpcWorkers) > 0
		s.mu.Unlock()

		// Only act when a REMOTE-split pipeline has exited on its own.
		if running || !hadWorkers {
			continue
		}

		log.Printf("monitor: supervised llama-server EXITED (remote node died) -> rebuilding over survivors")
		if err := s.ensure(healthyFn); err != nil {
			log.Printf("monitor: rebuild failed: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		info := s.info()
		log.Printf("monitor: pipeline REBUILT (gen %d) over %v -> %s", info.Generation, info.RPCWorkers, info.describe())
	}
}

// completion sends a prompt to the supervised llama-server and returns the text.
func (s *supervisor) completion(ctx context.Context, prompt string, nPredict int) (string, error) {
	return s.completionAt(ctx, s.baseURL(), prompt, nPredict)
}

// completionAt sends a prompt to a specific llama-server base URL using the
// OpenAI-compatible chat endpoint (/v1/chat/completions). This makes llama-server
// apply the model's CHAT TEMPLATE, so the model answers cleanly instead of
// drifting into raw instruction tokens like [INST]/<<SYS>> (which happens when
// posting an un-templated prompt to the raw /completion endpoint).
func (s *supervisor) completionAt(ctx context.Context, base, prompt string, nPredict int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": nPredict,
		"stream":     false,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llama-server status %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("bad llama-server response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llama-server returned no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
