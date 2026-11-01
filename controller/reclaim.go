// HyperPod Controller — accounting for memory the outgoing generation still holds.
//
// Why this exists
//
// A rebuild kills llama-server and immediately schedules its replacement. The
// driver does not return that memory instantly, and a remote worker's
// ggml-rpc-server releases its tensors on its own schedule, after the client
// disconnects. Worker telemetry is also sampled every five seconds and arrives
// on a three-second heartbeat, so a reading can be several seconds old.
//
// The result was that each rebuild scheduled against whatever the dying
// generation happened to still occupy. Observed across three consecutive
// generations of the same model on the same three machines:
//
//	gen 1  weights = 5409,5409,3204   (workers idle)
//	gen 2  weights = 4385,4509,3204   (workers partly loaded from gen 1)
//	gen 3  weights = 2665,5409,3204   (one worker still holding gen 2)
//
// gen 3's inputs were exactly gen 2's post-load readings. Since --tensor-split
// takes proportions, those shifting numbers moved real layers between machines:
// one worker's share went from 2652 MiB to 1768 MiB for no physical reason.
//
// The correction
//
// The controller already knows the outgoing placement, because it parses
// llama.cpp's load output per device. Capacity for the next generation on a
// device is therefore:
//
//	free_now + what our own outgoing model holds there
//
// capped at the device's total, since a reading that has already caught up with
// the release would otherwise be counted twice. The cap bounds that error at
// the driver's own overhead rather than letting it compound.

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// rpcDeviceRe matches llama.cpp's name for a remote device, e.g.
// "RPC0[192.168.1.31:50052]", capturing the endpoint.
var rpcDeviceRe = regexp.MustCompile(`^RPC\d+\[(.+)\]$`)

// localGPUDeviceRe matches a local accelerator buffer: CUDA0, ROCm1, Metal0,
// Vulkan0, SYCL0. CPU_Mapped is deliberately excluded: it is system RAM, not
// video memory, and must not be added back to a VRAM figure.
var localGPUDeviceRe = regexp.MustCompile(`^(CUDA|ROCm|Metal|Vulkan|SYCL)\d*$`)

// heldVRAM is the video memory a generation occupies, per device, as reported by
// llama.cpp itself. The zero value holds nothing, which is correct for a first
// launch when no previous generation existed.
type heldVRAM struct {
	// LocalMiB is the total across the controller's own accelerator buffers.
	LocalMiB uint64
	// RemoteMiB is keyed by RPC endpoint, matching the scheduler's endpoints.
	RemoteMiB map[string]uint64
}

// heldOn returns what the generation holds on one RPC endpoint.
func (h heldVRAM) heldOn(endpoint string) uint64 {
	if h.RemoteMiB == nil {
		return 0
	}
	return h.RemoteMiB[endpoint]
}

// heldFromPlacement reads a generation's per-device placement into a heldVRAM.
//
// Placement figures come from llama.cpp's own log, so an unrecognised device
// name is skipped rather than guessed at: counting an unknown buffer as video
// memory would inflate capacity and push layers onto a device that cannot hold
// them.
func heldFromPlacement(devices []devicePlacement) heldVRAM {
	held := heldVRAM{RemoteMiB: map[string]uint64{}}
	for _, d := range devices {
		if d.MiB <= 0 {
			continue
		}
		mib := uint64(d.MiB + 0.5)
		name := strings.TrimSpace(d.Device)
		if m := rpcDeviceRe.FindStringSubmatch(name); m != nil {
			held.RemoteMiB[m[1]] += mib
			continue
		}
		if localGPUDeviceRe.MatchString(name) {
			held.LocalMiB += mib
		}
	}
	return held
}

// reclaimable adds back memory our own outgoing generation holds on a device,
// then caps the result at the device's total.
//
// The cap matters because telemetry may already reflect the release. Without it,
// a stale-free reading plus a held figure would exceed the hardware and distort
// the proportions in the opposite direction.
func reclaimable(freeNow, held uint64, total uint64, haveTotal bool) uint64 {
	effective := freeNow + held
	if haveTotal && effective > total {
		return total
	}
	return effective
}

// String summarises what a generation holds, for the launch log. Reporting it
// keeps the scheduler's inputs auditable: a reader can see whether a weight
// came from free memory or from memory about to be released.
func (h heldVRAM) String() string {
	if h.LocalMiB == 0 && len(h.RemoteMiB) == 0 {
		return "nothing held (first launch)"
	}
	parts := make([]string, 0, len(h.RemoteMiB)+1)
	if h.LocalMiB > 0 {
		parts = append(parts, fmt.Sprintf("local %d MiB", h.LocalMiB))
	}
	eps := make([]string, 0, len(h.RemoteMiB))
	for ep := range h.RemoteMiB {
		eps = append(eps, ep)
	}
	sort.Strings(eps)
	for _, ep := range eps {
		parts = append(parts, fmt.Sprintf("%s %d MiB", ep, h.RemoteMiB[ep]))
	}
	return strings.Join(parts, " + ")
}

// totalMiB is all the video memory this generation holds, across every device.
func (h heldVRAM) totalMiB() uint64 {
	total := h.LocalMiB
	for _, mib := range h.RemoteMiB {
		total += mib
	}
	return total
}
