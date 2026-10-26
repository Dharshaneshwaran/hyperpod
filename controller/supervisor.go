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
		args = append(args, "--tensor-split", evenSplit(len(workers)+1)) // +1 local device
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

// completion sends a prompt to the supervised llama-server and returns the text.
func (s *supervisor) completion(ctx context.Context, prompt string, nPredict int) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"prompt":    prompt,
		"n_predict": nPredict,
		"stream":    false,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL()+"/completion", bytes.NewReader(body))
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
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("bad llama-server response: %w", err)
	}
	return strings.TrimSpace(out.Content), nil
}
