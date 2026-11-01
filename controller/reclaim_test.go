package main

import "testing"

// The figures in this file are the ones the controller actually logged while
// rebuilding the same 8.54 GB model three times over the same three machines.
// They are not illustrative: gen 3 was scheduled from gen 2's leftovers, and
// these tests exist so that cannot silently return.

// Device totals for the real hardware under test.
const (
	rtx4050Total = 6141 // both Windows workers
	rtx3050Total = 4096 // the controller
)

func TestHeldFromPlacementReadsLlamaCppDeviceNames(t *testing.T) {
	// Exactly the placement the dashboard showed for build 2.
	held := heldFromPlacement([]devicePlacement{
		{Device: "CPU_Mapped", MiB: 532},
		{Device: "CUDA0", MiB: 2080},
		{Device: "RPC0[192.168.1.31:50052]", MiB: 2652},
		{Device: "RPC0[192.168.1.32:50052]", MiB: 2873},
	})

	// CPU_Mapped is system RAM. Counting it as video memory would inflate
	// capacity and push layers onto a device that cannot hold them.
	if held.LocalMiB != 2080 {
		t.Fatalf("local held = %d, want 2080 (CUDA0 only, not CPU_Mapped)", held.LocalMiB)
	}
	if got := held.heldOn("192.168.1.31:50052"); got != 2652 {
		t.Fatalf("held on .31 = %d, want 2652", got)
	}
	if got := held.heldOn("192.168.1.32:50052"); got != 2873 {
		t.Fatalf("held on .32 = %d, want 2873", got)
	}
	// An endpoint that held nothing must report zero, not a missing-map panic.
	if got := held.heldOn("192.168.1.99:50052"); got != 0 {
		t.Fatalf("held on an unused endpoint = %d, want 0", got)
	}
}

func TestHeldFromPlacementSkipsUnknownDevices(t *testing.T) {
	held := heldFromPlacement([]devicePlacement{
		{Device: "CUDA0", MiB: 100},
		{Device: "SomeFutureBackend7", MiB: 9999},
		{Device: "RPC0[host:1]", MiB: 50},
		{Device: "CUDA1", MiB: 25},
		{Device: "CPU", MiB: 400},
	})
	// 100 + 25 from the two CUDA buffers; the unknown backend is not guessed at.
	if held.LocalMiB != 125 {
		t.Fatalf("local held = %d, want 125", held.LocalMiB)
	}
	if got := held.heldOn("host:1"); got != 50 {
		t.Fatalf("held on host:1 = %d, want 50", got)
	}
}

// The core regression: two consecutive rebuilds of the same model over the same
// machines must produce the same proportions, even though the second one sees
// memory the first is still holding.
func TestPlanSplitIsStableAcrossRebuilds(t *testing.T) {
	const epA, epB = "192.168.1.31:50052", "192.168.1.32:50052"

	// Generation 1: everything idle. These are the readings that produced
	// weights 5409,5409,3204 in the log.
	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return 3716, true }
	localVRAMTotal = func() (uint64, bool) { return rtx3050Total, true }
	defer func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{ID: "a", RPCAddr: epA, Telemetry: telemetryWithVRAM(5921, rtx4050Total)})
	s.reg.register(&worker{ID: "b", RPCAddr: epB, Telemetry: telemetryWithVRAM(5921, rtx4050Total)})

	first := s.planSplit([]string{epA, epB}, heldVRAM{})
	if first.TensorSplit != "5409,5409,3204" {
		t.Fatalf("first launch weights = %q, want 5409,5409,3204", first.TensorSplit)
	}

	// Generation 2: the model from gen 1 is still resident. Free has dropped on
	// every device by what that model holds there. Without adding it back, this
	// is the situation that produced a different split for no physical reason.
	placement := []devicePlacement{
		{Device: "CPU_Mapped", MiB: 532},
		{Device: "CUDA0", MiB: 2080},
		{Device: "RPC0[" + epA + "]", MiB: 2652},
		{Device: "RPC0[" + epB + "]", MiB: 2873},
	}
	localVRAMFree = func() (uint64, bool) { return 3716 - 2080, true }
	s.reg.register(&worker{ID: "a", RPCAddr: epA, Telemetry: telemetryWithVRAM(5921-2652, rtx4050Total)})
	s.reg.register(&worker{ID: "b", RPCAddr: epB, Telemetry: telemetryWithVRAM(5921-2873, rtx4050Total)})

	second := s.planSplit([]string{epA, epB}, heldFromPlacement(placement))
	if second.TensorSplit != first.TensorSplit {
		t.Fatalf("rebuild changed the split: %q then %q -- the same model on the same machines must weigh the same",
			first.TensorSplit, second.TensorSplit)
	}
}

// Without the correction the split does move, which is what makes the test
// above meaningful rather than vacuous.
func TestPlanSplitWouldDriftWithoutReclaim(t *testing.T) {
	const epA, epB = "192.168.1.31:50052", "192.168.1.32:50052"

	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return 3716 - 2080, true }
	localVRAMTotal = func() (uint64, bool) { return rtx3050Total, true }
	defer func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{ID: "a", RPCAddr: epA, Telemetry: telemetryWithVRAM(5921-2652, rtx4050Total)})
	s.reg.register(&worker{ID: "b", RPCAddr: epB, Telemetry: telemetryWithVRAM(5921-2873, rtx4050Total)})

	// heldVRAM{} means "assume nothing is held", i.e. the old behaviour.
	drifted := s.planSplit([]string{epA, epB}, heldVRAM{})
	if drifted.TensorSplit == "5409,5409,3204" {
		t.Fatal("expected the uncorrected path to drift; if it no longer does, this test is obsolete")
	}
}

func TestReclaimableCapsAtDeviceTotal(t *testing.T) {
	// Telemetry has already caught up with the release: free is back to 5921
	// and the held figure would double-count. The cap bounds that error.
	if got := reclaimable(5921, 2652, rtx4050Total, true); got != rtx4050Total {
		t.Fatalf("capped value = %d, want %d", got, rtx4050Total)
	}
	// A stale reading plus what we hold stays under the total and is used as is.
	if got := reclaimable(3269, 2652, rtx4050Total, true); got != 5921 {
		t.Fatalf("uncapped value = %d, want 5921", got)
	}
	// With no total known, no ceiling is invented.
	if got := reclaimable(5921, 2652, 0, false); got != 8573 {
		t.Fatalf("uncapped-without-total = %d, want 8573", got)
	}
	// Nothing held is the first-launch case and must change nothing.
	if got := reclaimable(3716, 0, rtx3050Total, true); got != 3716 {
		t.Fatalf("no-held value = %d, want 3716", got)
	}
}

// A worker that reports no total must still be scheduled, just without a cap.
func TestPlanSplitWorksWhenTotalIsUnavailable(t *testing.T) {
	const ep = "10.0.0.1:50052"
	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return 3716, true }
	localVRAMTotal = func() (uint64, bool) { return 0, false }
	defer func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal }()

	s := &server{reg: newRegistry()}
	// telemetryWithFreeVRAM carries no total.
	s.reg.register(&worker{ID: "a", RPCAddr: ep, Telemetry: telemetryWithFreeVRAM(4096)})

	plan := s.planSplit([]string{ep}, heldVRAM{})
	if plan.Strategy != "measured-free-vram" {
		t.Fatalf("strategy = %q, want measured-free-vram", plan.Strategy)
	}
	if plan.TensorSplit != "3584,3204" {
		t.Fatalf("weights = %q, want 3584,3204", plan.TensorSplit)
	}
}
