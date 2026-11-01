package main

import "testing"

// The sizes below are the real byte counts of the GGUF files used in testing,
// converted to MiB, so the verdicts here are the ones the interface will show.
const (
	mib1B = 770  //  807,694,464 bytes
	mib3B = 1925 // 2,019,377,696 bytes
	mib8B = 4692 // 4,920,739,232 bytes
)

func TestAssessFitUsesMeasuredCapacity(t *testing.T) {
	// Measured on the two NVIDIA laptops: 3716 and 5843 MiB free, less the
	// 512 MiB reserve each, is 8535 MiB of usable capacity.
	const twoLaptops = (3716 - 512) + (5843 - 512)

	cases := []struct {
		name     string
		mib      uint64
		capacity uint64
		want     modelFit
	}{
		{"3B fits across both NVIDIA laptops", mib3B, twoLaptops, fitYes},
		{"8B fits across both NVIDIA laptops", mib8B, twoLaptops, fitYes},
		// The controller alone holds 3204 MiB usable, which is under the 8B's
		// weights. This is the case that makes the 8B run require the pool.
		{"8B does not fit on the controller alone", mib8B, 3204, fitNo},
		{"3B fits on the controller alone", mib3B, 3204, fitYes},
		{"1B fits anywhere", mib1B, 3204, fitYes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := assessFit(c.mib, c.capacity, 0)
			if got != c.want {
				t.Fatalf("assessFit(%d, %d) = %q, want %q (%s)", c.mib, c.capacity, got, c.want, reason)
			}
			if reason == "" {
				t.Fatal("a verdict must carry a reason the interface can show")
			}
		})
	}
}

// A model that needs more than the measured figure but less than the headroom
// margin is reported as tight, not as a comfortable fit, because the KV cache
// and compute buffers also have to land somewhere.
func TestAssessFitReportsTightSeparately(t *testing.T) {
	got, reason := assessFit(1000, 1050, 0)
	if got != fitTight {
		t.Fatalf("got %q, want %q", got, fitTight)
	}
	if reason == "" {
		t.Fatal("tight verdict needs a reason")
	}
}

// With nothing measured, the verdict must be unknown rather than a guess.
// This is the Metal-only case: a healthy worker that reports no GPU memory.
func TestAssessFitRefusesToGuessWithoutMeasurement(t *testing.T) {
	got, reason := assessFit(mib3B, 0, 1)
	if got != fitUnknown {
		t.Fatalf("got %q, want %q", got, fitUnknown)
	}
	if reason == "" {
		t.Fatal("unknown verdict needs a reason")
	}
}

// When a model does not fit the measured devices but unmeasured ones exist, the
// reason must say so, so a reader is not told a flat no that may be wrong.
func TestAssessFitMentionsUnmeasuredDevices(t *testing.T) {
	_, withUnmeasured := assessFit(mib8B, 1000, 2)
	_, without := assessFit(mib8B, 1000, 0)
	if withUnmeasured == without {
		t.Fatal("the reason should note that unmeasured devices may cover the shortfall")
	}
}
