// HyperPot Controller — Supervisor (Phase 2, Path A)
//
// Supervises a real llama.cpp RPC pipeline: one model split across the
// registered healthy workers via `llama-server --rpc ... --tensor-split ...`.
// On node death the controller recomputes the split over survivors, relaunches
// llama-server, and re-runs the in-flight request.
//
// Config (env):
//
//	HYPERPOT_LLAMA_SERVER   path to llama-server binary (required for supervisor)
//	HYPERPOT_MODEL          path to .gguf model (required for supervisor)
//	HYPERPOT_SERVER_HOST    host llama-server binds to (default 127.0.0.1)
//	HYPERPOT_SERVER_PORT    port llama-server binds to (default 9931)
//	HYPERPOT_NGL            gpu layers (default 99)
//	HYPERPOT_RPC_WORKERS    comma list host:port of ggml-rpc-server endpoints.
//	                        If set, these are used directly as the split targets.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// rpcWorkers is the ordered list of ggml-rpc-server endpoints in the current split.
	rpcWorkers []string
	planner    func([]string) splitPlan

	// Scheduler decision used for the current generation.
	schedulerStrategy string
	tensorSplit       string
	splitWeightsMiB   []uint64
	vramReserveMiB    uint64

	cmd     *exec.Cmd
	running bool
	// ready is true once the current generation answered /health with 200.
	ready bool
	// generation increments every (re)launch; used to detect restarts.
	generation int

	// inferWait is how long one /infer request will wait for a loading
	// pipeline before answering "still loading". It never kills the load.
	inferWait time.Duration

	// startedAt/readyAfter time the current generation's load.
	startedAt  time.Time
	readyAfter time.Duration

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
	Generation        int               `json:"generation"`
	State             string            `json:"state"` // down | loading | ready
	RPCWorkers        []string          `json:"rpc_workers"`
	Devices           []devicePlacement `json:"devices"`
	Offloaded         string            `json:"offloaded"`
	Split             bool              `json:"split"` // true only if a remote RPC device holds weights
	SchedulerStrategy string            `json:"scheduler_strategy"`
	TensorSplit       string            `json:"tensor_split"`
	SplitWeightsMiB   []uint64          `json:"split_weights_mib"`
	VRAMReserveMiB    uint64            `json:"vram_reserve_mib"`
	// LoadingSecs is how long the current generation has been loading (0 once ready).
	LoadingSecs float64 `json:"loading_secs"`
	// ReadySecs is how long the current generation took to become ready.
	ReadySecs float64 `json:"ready_secs"`
}

// ReadyAfter formats the load time of a ready pipeline.
func (p pipelineInfo) ReadyAfter() string {
	return fmt.Sprintf("%.0fs", p.ReadySecs)
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
	waitSecs, err := strconv.Atoi(envDefault("HYPERPOT_INFER_WAIT_SECS", "120"))
	if err != nil || waitSecs <= 0 {
		waitSecs = 120
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
		inferWait:   time.Duration(waitSecs) * time.Second,
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

// launch (re)starts llama-server with the current set of rpc workers.
// Caller must hold s.mu.
func (s *supervisor) launchLocked(workers []string) error {
	s.stopLocked()

	plan := splitPlan{Workers: append([]string(nil), workers...), Strategy: "llama.cpp-auto"}
	if s.planner != nil {
		plan = s.planner(workers)
	}
	workers = plan.Workers

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
		if plan.TensorSplit != "" {
			args = append(args, "--tensor-split", plan.TensorSplit)
		}
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
	s.schedulerStrategy = plan.Strategy
	s.tensorSplit = plan.TensorSplit
	s.splitWeightsMiB = append([]uint64(nil), plan.WeightsMiB...)
	s.vramReserveMiB = plan.ReserveMiB
	s.startedAt = time.Now()
	s.readyAfter = 0
	readerDone := make(chan struct{})
	s.rpcWorkers = workers
	s.generation++
	gen := s.generation
	log.Printf("supervisor: launched llama-server (gen %d) over %v; scheduler=%s", gen, workers, plan.String())

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
		// If Scan stopped early (e.g. a line over 1 MiB), keep draining so
		// llama-server never blocks writing to a full pipe.
		_, _ = io.Copy(io.Discard, stdout)
		close(readerDone)
	}()

	// Watch readiness independently of any caller, so "ready" is set the
	// moment /health returns 200, however long loading takes. There is no
	// timeout: a process that is still running is still loading.
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		for {
			s.mu.Lock()
			alive := s.running && s.generation == gen
			s.mu.Unlock()
			if !alive {
				return
			}
			resp, err := client.Get(s.baseURL() + "/health")
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					s.mu.Lock()
					if s.generation == gen && s.running {
						s.ready = true
						s.readyAfter = time.Since(s.startedAt)
					}
					s.mu.Unlock()
					info := s.info()
					log.Printf("supervisor: pipeline ready (gen %d) after %s -> %s",
						gen, info.ReadyAfter(), info.describe())
					return
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Reap the process. Per os/exec, Wait must only be called after all reads
	// from the pipe are done, so wait for the reader first.
	go func() {
		<-readerDone
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

// errLoading means the pipeline is alive but still loading the model.
var errLoading = errors.New("big-tier model is still loading")

// waitReady waits up to timeout for the current generation to become ready.
// It only reads flags set by the readiness watcher; it never kills anything.
func (s *supervisor) waitReady(timeout time.Duration) error {
	s.mu.Lock()
	gen := s.generation
	s.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for {
		s.mu.Lock()
		running, ready, sameGen := s.running, s.ready, s.generation == gen
		s.mu.Unlock()
		switch {
		case !sameGen:
			return fmt.Errorf("pipeline was relaunched while waiting")
		case !running:
			return fmt.Errorf("llama-server exited while loading")
		case ready:
			return nil
		case time.Now().After(deadline):
			return errLoading
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// ensure makes sure a big-tier llama-server is running (launching one over
// workersFn() if none is), then waits up to `wait` for it to be ready.
// A load that outlasts `wait` returns errLoading and keeps loading.
// Launches are serialized so two pipelines never start at once.
func (s *supervisor) ensure(workersFn func() []string, wait time.Duration) error {
	s.rebuildMu.Lock()
	s.mu.Lock()
	if !s.running {
		if err := s.launchLocked(workersFn()); err != nil {
			s.mu.Unlock()
			s.rebuildMu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	s.rebuildMu.Unlock()
	if wait <= 0 {
		return nil
	}
	return s.waitReady(wait)
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
	loading := 0.0
	if s.running && !s.ready {
		loading = time.Since(s.startedAt).Seconds()
	}
	devs := append([]devicePlacement(nil), s.placement...)
	split := false
	for _, d := range devs {
		if strings.HasPrefix(d.Device, "RPC") && d.MiB > 0 {
			split = true
		}
	}
	return pipelineInfo{
		Generation:        s.generation,
		State:             state,
		RPCWorkers:        append([]string(nil), s.rpcWorkers...),
		Devices:           devs,
		Offloaded:         s.offloaded,
		Split:             split,
		SchedulerStrategy: s.schedulerStrategy,
		TensorSplit:       s.tensorSplit,
		SplitWeightsMiB:   append([]uint64(nil), s.splitWeightsMiB...),
		VRAMReserveMiB:    s.vramReserveMiB,
		LoadingSecs:       loading,
		ReadySecs:         s.readyAfter.Seconds(),
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
		if err := s.ensure(healthyFn, 0); err != nil {
			log.Printf("monitor: rebuild launch failed: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		info := s.info()
		log.Printf("monitor: pipeline REBUILT (gen %d) over %v; loading (ready is logged when done)", info.Generation, info.RPCWorkers)
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
