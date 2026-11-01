// HyperPod Controller — model catalogue and fit assessment.
//
// Lists the GGUF files available to the controller and says, for each one,
// whether the cluster's MEASURED free GPU memory can hold it.
//
// The honesty rule that governs this file: capacity is counted only for a
// device that actually reports its free video memory. Apple Silicon reports no
// separate GPU pool, so a Metal worker contributes nothing to the total and is
// listed by name as unmeasured instead. Every verdict is therefore a lower
// bound, and the interface says so rather than implying the figure is complete.
//
// The reserve and the free-VRAM readings come from the same helpers the
// scheduler uses, so a model's verdict and its eventual tensor split cannot
// disagree about how much memory a device has.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// modelFit is the verdict for one model against the cluster's measured memory.
type modelFit string

const (
	fitYes     modelFit = "fits"
	fitTight   modelFit = "tight"
	fitNo      modelFit = "too large"
	fitUnknown modelFit = "unknown"
)

// fitHeadroom is the margin required above the weight size before a model is
// called a comfortable fit. Loading needs more than the weights alone: compute
// buffers and the KV cache also occupy the devices.
const fitHeadroom = 1.15

type modelEntry struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Bytes    int64    `json:"bytes"`
	MiB      uint64   `json:"mib"`
	Fit      modelFit `json:"fit"`
	Reason   string   `json:"reason"`
	Selected bool     `json:"selected"`
}

type modelsResp struct {
	OK     bool         `json:"ok"`
	Models []modelEntry `json:"models"`
	// MeasuredCapacityMiB sums usable free VRAM over devices that reported it.
	MeasuredCapacityMiB uint64 `json:"measured_capacity_mib"`
	// Contributors and Unmeasured make the figure auditable: what was counted,
	// and which healthy devices could not be.
	Contributors []string `json:"contributors"`
	Unmeasured   []string `json:"unmeasured"`
	ReserveMiB   uint64   `json:"reserve_mib"`
	// ReclaimedMiB is video memory counted because the loaded model is about to
	// release it. Reported so the capacity figure can be audited rather than
	// taken on trust.
	ReclaimedMiB uint64 `json:"reclaimed_mib,omitempty"`
	Error        string `json:"error,omitempty"`
}

// measuredCapacity sums the VRAM the cluster can devote to a model's weights.
//
// The question each row of the catalogue answers is "if I select this, will it
// fit", and selecting a model unloads the current one first. So the memory the
// loaded model occupies is capacity, not consumption: it is about to be
// returned. Leaving it out produced the contradiction of a running model
// labelled "too large", because its own weights were being counted against it.
//
// held is what the loaded generation occupies per device. This uses the same
// reclaimable() and reserve as the scheduler, so a verdict here and the tensor
// split that follows cannot disagree about how much memory a device has.
func (s *server) measuredCapacity(held heldVRAM) (total uint64, counted, unmeasured []string) {
	if free, ok := localVRAMFree(); ok {
		localTotal, haveTotal := localVRAMTotal()
		effective := reclaimable(free, held.LocalMiB, localTotal, haveTotal)
		if u := usableVRAM(effective, vramReserveMiB); u > 0 {
			total += u
			counted = append(counted, describeDeviceCapacity("this laptop", free, held.LocalMiB))
		}
	} else {
		unmeasured = append(unmeasured, "this laptop (no nvidia-smi reading)")
	}

	for _, ep := range s.reg.healthyRPCEndpoints() {
		label, gpu := s.reg.describeEndpoint(ep)
		free, ok := s.reg.measuredVRAMFree(ep)
		if !ok {
			unmeasured = append(unmeasured, fmt.Sprintf("%s (%s reports no separate GPU memory)", label, gpu))
			continue
		}
		epTotal, haveTotal := s.reg.measuredVRAMTotal(ep)
		heldHere := held.heldOn(ep)
		effective := reclaimable(free, heldHere, epTotal, haveTotal)
		if u := usableVRAM(effective, vramReserveMiB); u > 0 {
			total += u
			counted = append(counted, describeDeviceCapacity(label, free, heldHere))
		}
	}
	return total, counted, unmeasured
}

// describeDeviceCapacity states a device's contribution so a reader can see
// where the number came from, including memory counted because the loaded
// model is about to release it.
func describeDeviceCapacity(label string, free, held uint64) string {
	if held == 0 {
		return fmt.Sprintf("%s: %d MiB free", label, free)
	}
	return fmt.Sprintf("%s: %d MiB free + %d MiB the loaded model would release", label, free, held)
}

// assessFit turns a weight size and a measured capacity into a verdict.
func assessFit(mib, capacity uint64, unmeasured int) (modelFit, string) {
	if capacity == 0 {
		return fitUnknown, "no device reported its free GPU memory, so this cannot be judged"
	}
	need := uint64(float64(mib) * fitHeadroom)
	switch {
	case capacity >= need:
		return fitYes, fmt.Sprintf("%d MiB of weights against %d MiB measured free", mib, capacity)
	case capacity >= mib:
		return fitTight, fmt.Sprintf("%d MiB of weights against %d MiB measured free: little headroom for the KV cache", mib, capacity)
	default:
		msg := fmt.Sprintf("%d MiB of weights exceeds %d MiB measured free", mib, capacity)
		if unmeasured > 0 {
			msg += "; unmeasured devices may still cover it"
		}
		return fitNo, msg
	}
}

// modelDir is where the catalogue looks for GGUF files.
func (s *server) modelDir() string {
	if d := os.Getenv("HYPERPOD_MODEL_DIR"); d != "" {
		return d
	}
	if s.sup != nil && s.sup.modelPath() != "" {
		return filepath.Dir(s.sup.modelPath())
	}
	return ""
}

// handleModels lists available GGUF files and assesses each against measured memory.
func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		writeJSON(w, http.StatusOK, modelsResp{Error: "supervisor disabled"})
		return
	}
	dir := s.modelDir()
	if dir == "" {
		writeJSON(w, http.StatusOK, modelsResp{Error: "no model directory configured"})
		return
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.gguf"))
	if err != nil {
		writeJSON(w, http.StatusOK, modelsResp{Error: err.Error()})
		return
	}
	sort.Strings(paths)

	// The loaded model's own memory is capacity for any candidate, because
	// selecting one unloads the current first.
	held := heldFromPlacement(s.sup.currentPlacement())
	capMiB, counted, unmeasured := s.measuredCapacity(held)
	current := s.sup.modelPath()

	out := modelsResp{
		OK:                  true,
		MeasuredCapacityMiB: capMiB,
		Contributors:        counted,
		Unmeasured:          unmeasured,
		ReserveMiB:          vramReserveMiB,
		ReclaimedMiB:        held.totalMiB(),
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		mib := uint64(fi.Size() / (1024 * 1024))
		fit, reason := assessFit(mib, capMiB, len(unmeasured))
		out.Models = append(out.Models, modelEntry{
			Name:     filepath.Base(p),
			Path:     p,
			Bytes:    fi.Size(),
			MiB:      mib,
			Fit:      fit,
			Reason:   reason,
			Selected: p == current,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type selectModelReq struct {
	Path string `json:"path"`
}

// handleSelectModel switches the big-tier model. The running pipeline is torn
// down and the next big-tier prompt loads the new file.
//
// The path must be one the catalogue already lists. Accepting an arbitrary path
// here would let any caller on the LAN make the controller open a file of their
// choosing, so the request is matched against the directory listing instead.
func (s *server) handleSelectModel(w http.ResponseWriter, r *http.Request) {
	if s.sup == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "supervisor disabled"})
		return
	}
	var req selectModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad request body"})
		return
	}
	dir := s.modelDir()
	allowed, _ := filepath.Glob(filepath.Join(dir, "*.gguf"))
	permitted := false
	for _, a := range allowed {
		if a == req.Path {
			permitted = true
			break
		}
	}
	if !permitted {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "that model is not in the catalogue",
		})
		return
	}
	if strings.TrimSpace(req.Path) == s.sup.modelPath() {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "model": filepath.Base(req.Path), "note": "already selected",
		})
		return
	}
	s.sup.setModelPath(req.Path)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"model": filepath.Base(req.Path),
		"note":  "pipeline stopped; the next big-tier prompt loads this model",
	})
}
