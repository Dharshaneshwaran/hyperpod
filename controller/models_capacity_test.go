package main

import (
	"strings"
	"testing"
)

// The numbers here are from the dashboard at the moment it labelled a running
// model "too large": the Q8_0 was loaded and serving across three machines
// while its own row said its weights exceeded the free memory.
//
//	Counted: this laptop 1336 MiB free · LAPTOP-J2ULDUCA 2293 MiB · KAMALESH 4061 MiB
//	Placement: CPU_Mapped 532 + CUDA0 2301 + RPC[.31] 1768 + RPC[.32] 3537
//	Verdict shown: too large -- 8145 MiB of weights exceeds 6154 MiB measured free
const (
	q8WeightsMiB = 8145 // Meta-Llama-3.1-8B-Instruct-Q8_0.gguf
	q4WeightsMiB = 4692 // Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf
)

// loadedQ8Placement is what llama.cpp reported for the running Q8_0.
func loadedQ8Placement() []devicePlacement {
	return []devicePlacement{
		{Device: "CPU_Mapped", MiB: 532},
		{Device: "CUDA0", MiB: 2301},
		{Device: "RPC0[192.168.1.31:50052]", MiB: 1768},
		{Device: "RPC0[192.168.1.32:50052]", MiB: 3537},
	}
}

// capacityFixture rebuilds the cluster as it was in that screenshot.
func capacityFixture(t *testing.T) *server {
	t.Helper()
	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return 1336, true }
	localVRAMTotal = func() (uint64, bool) { return rtx3050Total, true }
	t.Cleanup(func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal })

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{
		ID: "worker-LAPTOP-J2ULDUCA", RPCAddr: "192.168.1.31:50052",
		Telemetry: telemetryWithVRAM(2293, rtx4050Total),
	})
	s.reg.register(&worker{
		ID: "worker-KAMALESH", RPCAddr: "192.168.1.32:50052",
		Telemetry: telemetryWithVRAM(4061, rtx4050Total),
	})
	return s
}

// The bug: with the loaded model's memory counted against it, capacity was
// 6154 MiB and the running Q8_0 was called too large.
func TestCapacityWithoutReclaimReproducesTheWrongVerdict(t *testing.T) {
	s := capacityFixture(t)

	capMiB, _, _ := s.measuredCapacity(heldVRAM{})
	if capMiB != 6154 {
		t.Fatalf("capacity = %d, want 6154 (the figure the dashboard showed)", capMiB)
	}
	fit, _ := assessFit(q8WeightsMiB, capMiB, 0)
	if fit != fitNo {
		t.Fatalf("got %q; this test documents the old wrong verdict and must stay %q", fit, fitNo)
	}
}

// The fix: the loaded model is unloaded before a new one loads, so its memory
// is capacity. The model that is demonstrably running must not be called too
// large.
func TestLoadedModelIsNotReportedTooLarge(t *testing.T) {
	s := capacityFixture(t)
	held := heldFromPlacement(loadedQ8Placement())

	capMiB, counted, _ := s.measuredCapacity(held)
	if capMiB <= 6154 {
		t.Fatalf("capacity = %d, expected more than the uncorrected 6154", capMiB)
	}
	if capMiB < q8WeightsMiB {
		t.Fatalf("capacity = %d is below the %d MiB model that is actually running",
			capMiB, q8WeightsMiB)
	}
	if fit, reason := assessFit(q8WeightsMiB, capMiB, 0); fit == fitNo {
		t.Fatalf("a running model was still called %q: %s", fit, reason)
	}

	// The contribution must be explained, not silently folded into a total.
	var explained bool
	for _, c := range counted {
		if strings.Contains(c, "the loaded model would release") {
			explained = true
		}
	}
	if !explained {
		t.Fatal("reclaimed memory must be attributed in the counted list")
	}
}

// Switching away to a smaller model must also see the larger one's memory.
func TestSwitchingToASmallerModelSeesReclaimedMemory(t *testing.T) {
	s := capacityFixture(t)
	held := heldFromPlacement(loadedQ8Placement())
	capMiB, _, _ := s.measuredCapacity(held)

	if fit, reason := assessFit(q4WeightsMiB, capMiB, 0); fit != fitYes {
		t.Fatalf("Q4 at %d MiB against %d MiB capacity = %q (%s), want %q",
			q4WeightsMiB, capMiB, fit, reason, fitYes)
	}
}

// With nothing loaded, capacity must be exactly the free readings. This guards
// against the correction being applied when there is nothing to reclaim.
func TestCapacityUnchangedWhenNothingIsLoaded(t *testing.T) {
	s := capacityFixture(t)

	withEmpty, _, _ := s.measuredCapacity(heldVRAM{})
	withNilMap, _, _ := s.measuredCapacity(heldVRAM{RemoteMiB: nil})
	if withEmpty != withNilMap {
		t.Fatalf("empty and nil-map held disagree: %d vs %d", withEmpty, withNilMap)
	}
	if withEmpty != 6154 {
		t.Fatalf("capacity with nothing loaded = %d, want 6154", withEmpty)
	}
}

// A device's contribution is capped at its total, so an already-refreshed
// reading plus held memory cannot claim more than the hardware has.
func TestCapacityIsCappedByDeviceTotals(t *testing.T) {
	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return rtx3050Total, true } // fully free
	localVRAMTotal = func() (uint64, bool) { return rtx3050Total, true }
	defer func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{
		ID: "w", RPCAddr: "10.0.0.1:50052",
		Telemetry: telemetryWithVRAM(rtx4050Total, rtx4050Total),
	})

	held := heldVRAM{LocalMiB: 2000, RemoteMiB: map[string]uint64{"10.0.0.1:50052": 3000}}
	capMiB, _, _ := s.measuredCapacity(held)

	ceiling := (rtx3050Total - vramReserveMiB) + (rtx4050Total - vramReserveMiB)
	if capMiB > ceiling {
		t.Fatalf("capacity %d exceeds the hardware ceiling %d", capMiB, ceiling)
	}
}
