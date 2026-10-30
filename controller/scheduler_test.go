package main

import "testing"

func u64ptr(v uint64) *uint64 { return &v }
func strptr(v string) *string { return &v }

func telemetryWithFreeVRAM(free uint64) *Telemetry {
	return &Telemetry{
		SchemaVersion: 1,
		GPU: &GPUTelemetry{
			Source:      "test",
			Name:        strptr("measured-test-gpu"),
			VRAMFreeMiB: u64ptr(free),
		},
	}
}

func TestUsableVRAM(t *testing.T) {
	for _, tc := range []struct {
		free, reserve, want uint64
	}{
		{4096, 512, 3584},
		{512, 512, 0},
		{128, 512, 0},
	} {
		if got := usableVRAM(tc.free, tc.reserve); got != tc.want {
			t.Fatalf("usableVRAM(%d,%d)=%d, want %d", tc.free, tc.reserve, got, tc.want)
		}
	}
}

func TestPlanSplitUsesMeasuredFreeVRAMInDeterministicOrder(t *testing.T) {
	old := localVRAMFree
	localVRAMFree = func() (uint64, bool) { return 3716, true }
	defer func() { localVRAMFree = old }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{ID: "b", RPCAddr: "10.0.0.2:50052", Telemetry: telemetryWithFreeVRAM(4096)})
	s.reg.register(&worker{ID: "a", RPCAddr: "10.0.0.1:50052", Telemetry: telemetryWithFreeVRAM(6144)})

	plan := s.planSplit([]string{"10.0.0.2:50052", "10.0.0.1:50052"})
	if plan.Strategy != "measured-free-vram" {
		t.Fatalf("strategy=%q", plan.Strategy)
	}
	if got, want := plan.TensorSplit, "5632,3584,3204"; got != want {
		t.Fatalf("tensor split=%q, want %q", got, want)
	}
	if got, want := plan.Workers[0], "10.0.0.1:50052"; got != want {
		t.Fatalf("first worker=%q, want %q", got, want)
	}
}

func TestPlanSplitDoesNotInventMissingCapacity(t *testing.T) {
	old := localVRAMFree
	localVRAMFree = func() (uint64, bool) { return 3716, true }
	defer func() { localVRAMFree = old }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{ID: "unknown", RPCAddr: "10.0.0.1:50052", Telemetry: nil})
	plan := s.planSplit([]string{"10.0.0.1:50052"})
	if plan.TensorSplit != "" || len(plan.WeightsMiB) != 0 {
		t.Fatalf("missing capacity produced invented weights: %+v", plan)
	}
	if plan.Strategy != "llama.cpp-auto (remote free VRAM unavailable)" {
		t.Fatalf("unexpected fallback: %q", plan.Strategy)
	}
}

func TestParseSingleMiB(t *testing.T) {
	if got, ok := parseSingleMiB("3716\n"); !ok || got != 3716 {
		t.Fatalf("got %d,%v", got, ok)
	}
	for _, invalid := range []string{"N/A", "", "3716\n4096\n", "-1"} {
		if _, ok := parseSingleMiB(invalid); ok {
			t.Fatalf("accepted invalid measurement %q", invalid)
		}
	}
}
