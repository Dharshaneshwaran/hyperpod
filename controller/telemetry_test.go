package main

import "testing"

func TestTelemetryVersionValidation(t *testing.T) {
	var absent *Telemetry
	if !absent.validVersion() {
		t.Fatal("absent telemetry must remain backward compatible")
	}
	if !(&Telemetry{SchemaVersion: TelemetrySchemaVersion}).validVersion() {
		t.Fatal("current schema rejected")
	}
	if (&Telemetry{SchemaVersion: TelemetrySchemaVersion + 1}).validVersion() {
		t.Fatal("unsupported future schema accepted")
	}
}

func TestHeartbeatUpdatesAndPreservesTelemetry(t *testing.T) {
	r := newRegistry()
	initial := telemetryWithFreeVRAM(1000)
	r.register(&worker{ID: "w", Telemetry: initial, GPU: "legacy"})

	if !r.heartbeat("w", nil) {
		t.Fatal("known worker heartbeat rejected")
	}
	if got := r.snapshot()[0].Telemetry.GPU.VRAMFreeMiB; got == nil || *got != 1000 {
		t.Fatal("heartbeat without telemetry erased previous measurement")
	}

	latest := telemetryWithFreeVRAM(2000)
	if !r.heartbeat("w", latest) {
		t.Fatal("telemetry heartbeat rejected")
	}
	got := r.snapshot()[0]
	if got.Telemetry.GPU.VRAMFreeMiB == nil || *got.Telemetry.GPU.VRAMFreeMiB != 2000 {
		t.Fatal("latest measurement was not stored")
	}
	if got.GPU != "measured-test-gpu" {
		t.Fatalf("legacy GPU summary not updated from measured telemetry: %q", got.GPU)
	}
}

func TestPlacementLogParser(t *testing.T) {
	line := "0.1 I load_tensors: RPC0[192.168.1.5:50052] model buffer size =  1366.17 MiB"
	match := reBuffer.FindStringSubmatch(line)
	if match == nil || match[1] != "RPC0[192.168.1.5:50052]" || match[2] != "1366.17" {
		t.Fatalf("placement line not parsed: %#v", match)
	}
}
