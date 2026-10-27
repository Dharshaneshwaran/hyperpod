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
	"strings"
	"sync"
	"time"
)

type supervisor struct {
	mu sync.Mutex

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
	// generation increments every (re)launch; used to detect restarts.
	generation int
}

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
				workers = append(workers, p)
			}
		}
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
	}, true
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
	}
	if len(workers) > 0 {
		args = append(args, "--rpc", strings.Join(workers, ","))
		// Weight the split heavily toward the REMOTE workers so that losing a
		// remote node genuinely breaks generation (local keeps a small share).
		// Order of --tensor-split matches: local device first, then each --rpc worker.
		args = append(args, "--tensor-split", remoteHeavySplit(len(workers)))
	}

	cmd := exec.Command(s.llamaServer, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}
	s.cmd = cmd
	s.running = true
	s.rpcWorkers = workers
	s.generation++
	gen := s.generation
	log.Printf("supervisor: launched llama-server (gen %d) split over %v", gen, workers)

	// Reap the process in the background; mark not-running on exit.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.generation == gen {
			s.running = false
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
	s.cmd = nil
}

// waitReady polls llama-server's /health until ready or timeout.
func (s *supervisor) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(s.baseURL() + "/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("llama-server not ready within %s", timeout)
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
		survivors := healthyFn()
		s.mu.Lock()
		// Re-check: another path may already be rebuilding.
		if s.running {
			s.mu.Unlock()
			continue
		}
		if err := s.launchLocked(survivors); err != nil {
			log.Printf("monitor: rebuild launch failed: %v", err)
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		if err := s.waitReady(90 * time.Second); err != nil {
			log.Printf("monitor: rebuilt pipeline not ready: %v", err)
		} else {
			log.Printf("monitor: pipeline REBUILT over survivors %v and ready", survivors)
		}
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
