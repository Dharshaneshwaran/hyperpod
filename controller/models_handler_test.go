package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// This exercises GET /models through the real handler, with a supervisor that
// has a model loaded. The arithmetic is covered elsewhere; what this checks is
// the wiring: that the loaded placement reaches measuredCapacity at all, and
// that the response carries the reclaimed figure.
//
// It exists because the live path could not be verified end to end: a
// distributed model load outlasts the time available to a single test command,
// so this is the closest in-process equivalent.
func TestHandleModelsReportsReclaimedForLoadedModel(t *testing.T) {
	// A model directory with two files of known size.
	dir := t.TempDir()
	small := filepath.Join(dir, "Llama-3.2-3B-Instruct-Q4_K_M.gguf")
	big := filepath.Join(dir, "Meta-Llama-3.1-8B-Instruct-Q8_0.gguf")
	writeSizedFile(t, small, 2*1024*1024) // 2 MiB stand-in
	writeSizedFile(t, big, 200*1024*1024) // 200 MiB stand-in
	t.Setenv("HYPERPOD_MODEL_DIR", dir)

	oldFree, oldTotal := localVRAMFree, localVRAMTotal
	localVRAMFree = func() (uint64, bool) { return 1336, true }
	localVRAMTotal = func() (uint64, bool) { return rtx3050Total, true }
	defer func() { localVRAMFree, localVRAMTotal = oldFree, oldTotal }()

	s := &server{reg: newRegistry()}
	s.reg.register(&worker{
		ID: "worker-KAMALESH", RPCAddr: "192.168.1.32:50052",
		Telemetry: telemetryWithVRAM(4061, rtx4050Total),
	})
	// A supervisor with the big model loaded and a known placement.
	s.sup = &supervisor{
		model:     big,
		placement: loadedQ8Placement(),
	}

	get := func() modelsResp {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleModels(rec, httptest.NewRequest(http.MethodGet, "/models", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var out modelsResp
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad response json: %v", err)
		}
		return out
	}

	withLoaded := get()
	if !withLoaded.OK {
		t.Fatalf("handler reported not ok: %s", withLoaded.Error)
	}
	if withLoaded.ReclaimedMiB == 0 {
		t.Fatal("a loaded model must contribute a reclaimed figure")
	}
	// CUDA0 2301 + RPC .31 1768 + RPC .32 3537; CPU_Mapped 532 excluded.
	if withLoaded.ReclaimedMiB != 2301+1768+3537 {
		t.Fatalf("reclaimed = %d, want %d (CPU_Mapped must be excluded)",
			withLoaded.ReclaimedMiB, 2301+1768+3537)
	}
	if len(withLoaded.Models) != 2 {
		t.Fatalf("listed %d models, want 2", len(withLoaded.Models))
	}
	// The loaded model must be marked selected.
	var sawSelected bool
	for _, m := range withLoaded.Models {
		if m.Selected && m.Name == filepath.Base(big) {
			sawSelected = true
		}
	}
	if !sawSelected {
		t.Fatal("the loaded model was not marked selected")
	}

	// With nothing loaded, the reclaimed figure must disappear rather than
	// linger from a previous generation.
	s.sup.placement = nil
	withoutLoaded := get()
	if withoutLoaded.ReclaimedMiB != 0 {
		t.Fatalf("reclaimed = %d with nothing loaded, want 0", withoutLoaded.ReclaimedMiB)
	}
	if withoutLoaded.MeasuredCapacityMiB >= withLoaded.MeasuredCapacityMiB {
		t.Fatalf("capacity without a loaded model (%d) should be lower than with one (%d)",
			withoutLoaded.MeasuredCapacityMiB, withLoaded.MeasuredCapacityMiB)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
