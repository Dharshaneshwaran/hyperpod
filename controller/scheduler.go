package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// vramReserveMiB is retained on every GPU for runtime buffers and other
// processes. It is a policy margin, not an invented hardware measurement.
const vramReserveMiB uint64 = 512

// splitPlan is the complete, ordered input to llama.cpp placement.
type splitPlan struct {
	Workers     []string
	TensorSplit string
	Strategy    string
	WeightsMiB  []uint64 // RPC workers first, local GPU last (llama.cpp order)
	ReserveMiB  uint64
}

// planSplit assigns weights in proportion to measured usable VRAM.
//
// It never derives capacity from a GPU name. If any selected worker or the
// controller's local GPU lacks a measured free-VRAM value, TensorSplit is left
// empty and llama.cpp performs its own device probing (`llama.cpp-auto`).
func (s *server) planSplit(endpoints []string) splitPlan {
	workers := append([]string(nil), endpoints...)
	sort.Strings(workers)
	if len(workers) == 0 {
		return splitPlan{Workers: workers, Strategy: "local-only", ReserveMiB: vramReserveMiB}
	}

	weights := make([]uint64, 0, len(workers)+1)
	for _, endpoint := range workers {
		free, ok := s.reg.measuredVRAMFree(endpoint)
		if !ok {
			return splitPlan{
				Workers: workers, Strategy: "llama.cpp-auto (remote free VRAM unavailable)", ReserveMiB: vramReserveMiB,
			}
		}
		usable := usableVRAM(free, vramReserveMiB)
		if usable == 0 {
			return splitPlan{
				Workers: workers, Strategy: "llama.cpp-auto (remote usable VRAM is zero)", ReserveMiB: vramReserveMiB,
			}
		}
		weights = append(weights, usable)
	}

	localFree, ok := localVRAMFree()
	if !ok {
		return splitPlan{
			Workers: workers, Strategy: "llama.cpp-auto (local free VRAM unavailable)", ReserveMiB: vramReserveMiB,
		}
	}
	localUsable := usableVRAM(localFree, vramReserveMiB)
	if localUsable == 0 {
		return splitPlan{
			Workers: workers, Strategy: "llama.cpp-auto (local usable VRAM is zero)", ReserveMiB: vramReserveMiB,
		}
	}
	weights = append(weights, localUsable)

	parts := make([]string, len(weights))
	for i, weight := range weights {
		parts[i] = strconv.FormatUint(weight, 10)
	}
	return splitPlan{
		Workers: workers, TensorSplit: strings.Join(parts, ","),
		Strategy: "measured-free-vram", WeightsMiB: weights, ReserveMiB: vramReserveMiB,
	}
}

func usableVRAM(free, reserve uint64) uint64 {
	if free <= reserve {
		return 0
	}
	return free - reserve
}

func (r *registry) measuredVRAMFree(endpoint string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, worker := range r.workers {
		if worker.RPCAddr != endpoint || worker.Telemetry == nil || worker.Telemetry.GPU == nil || worker.Telemetry.GPU.VRAMFreeMiB == nil {
			continue
		}
		return *worker.Telemetry.GPU.VRAMFreeMiB, true
	}
	return 0, false
}

// localVRAMFree is a variable so scheduler tests can supply a measured fixture
// without depending on the test machine's GPU.
var localVRAMFree = collectLocalVRAMFreeMiB

// collectLocalVRAMFreeMiB asks the local NVIDIA driver for current free VRAM.
// A missing command, N/A field, parse failure, or multiple-value ambiguity is
// reported as unavailable.
func collectLocalVRAMFreeMiB() (uint64, bool) {
	output, err := exec.Command(
		"nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return 0, false
	}
	return parseSingleMiB(string(output))
}

func parseSingleMiB(output string) (uint64, bool) {
	lines := strings.Fields(strings.TrimSpace(output))
	if len(lines) != 1 {
		return 0, false
	}
	value, err := strconv.ParseFloat(lines[0], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return uint64(value + 0.5), true
}

func (p splitPlan) String() string {
	if p.TensorSplit == "" {
		return p.Strategy
	}
	return fmt.Sprintf("%s weights=%s reserve=%dMiB", p.Strategy, p.TensorSplit, p.ReserveMiB)
}
