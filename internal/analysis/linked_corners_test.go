package analysis

// Tests for the two ways a lap's own data can be mis-attributed: a brake onset
// that walks back into the corner before it, and a published lap time that does
// not describe the lap it is attached to.

import (
	"testing"

	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// linkedCornerSegs models two corners joined by a short link, with a long
// straight before the first — the Okayama T6/T7 shape, where one continuous
// brake application covers the approach and both corners.
func linkedCornerSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 0.7},
		{Name: "T2", Kind: trackmap.KindCorner, EntryPct: 0.7, ExitPct: 1.0},
	}
}

// linkedCornerLap brakes continuously from 0.40 (on the straight) through both
// corners to 0.85, so a backward scan from T2 finds unbroken braking all the
// way back past T1's entry.
func linkedCornerLap() Lap {
	const n = 1000
	samples := make([]SampleData, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		if pct >= 0.40 && pct < 0.85 {
			samples[i] = brakingSample(pct, float64(i)/60)
		} else {
			samples[i] = straightSample(pct, float64(i)/60)
		}
	}
	return makeFlyingLap(samples)
}

// TestComputeBrakeEntries_StopsAtPrecedingCorner verifies that a corner's
// detected brake onset never lands inside the corner before it, even when the
// braking really is continuous across both.
func TestComputeBrakeEntries_StopsAtPrecedingCorner(t *testing.T) {
	segs := linkedCornerSegs()
	entries := ComputeBrakeEntries([]Lap{linkedCornerLap()}, segs)

	// T1 is preceded by a straight, so its onset may sit back on that straight.
	e1, ok := entries["T1"]
	if !ok {
		t.Fatal("T1: expected a brake entry, got none")
	}
	if e1.Pct < 0.395 || e1.Pct > 0.410 {
		t.Errorf("T1 brake onset: got %.4f, want ~0.400 (start of braking on S1)", e1.Pct)
	}

	// T2 is preceded by a corner. Its onset must not be reported inside T1,
	// which is where an unbounded backward scan would put it.
	if e2, ok := entries["T2"]; ok && e2.Pct < segs[1].ExitPct {
		t.Errorf("T2 brake onset %.4f lies inside T1 [%.2f, %.2f) — the scan crossed a corner boundary",
			e2.Pct, segs[1].EntryPct, segs[1].ExitPct)
	}
}

// TestComputePhases_ClampsOnsetInsidePrecedingCorner verifies that a stored
// onset violating that rule is repaired at read time. pb.json files written
// before the detection guard existed still carry such values and are not
// rewritten until the car/track sets a new personal best.
func TestComputePhases_ClampsOnsetInsidePrecedingCorner(t *testing.T) {
	segs := linkedCornerSegs()
	lap := linkedCornerLap()

	// A corrupt onset for T2, sitting well inside T1.
	corrupt := pb.BrakeEntryMap{
		"T2": {Pct: 0.55, LapsUsed: 1},
	}

	phases := ComputePhases(&lap, segs, corrupt)

	countFor := func(name string) int {
		total := 0
		for _, p := range phases {
			if p.SegName == name {
				total += p.SampleCount
			}
		}
		return total
	}

	// T1 spans [0.5, 0.7) — 200 of the 1000 samples. Left uncorrected, the
	// onset at 0.55 clamps T1's exit and moves 150 of them into T2.
	if got := countFor("T1"); got < 190 || got > 210 {
		t.Errorf("T1 sample count: got %d, want ~200 — samples leaked into T2", got)
	}
	if got := countFor("T2"); got < 290 || got > 310 {
		t.Errorf("T2 sample count: got %d, want ~300 — it absorbed part of T1", got)
	}

	// The clamp exists because losing samples also erases a phase: with T1's
	// exit pulled back to 0.55 the steering trace never unwinds inside it, so
	// no exit phase is emitted at all.
	if len(phasesFor(phases, "T1")) == 0 {
		t.Error("T1: expected phases, got none")
	}
}

// TestComputePhases_KeepsOnsetOnPrecedingStraight verifies the clamp does not
// defeat the feature it guards: an onset on the straight before a corner is the
// whole point of storing brake entries and must survive.
func TestComputePhases_KeepsOnsetOnPrecedingStraight(t *testing.T) {
	segs := linkedCornerSegs()
	lap := linkedCornerLap()

	entries := pb.BrakeEntryMap{
		"T1": {Pct: 0.40, LapsUsed: 1},
	}

	phases := ComputePhases(&lap, segs, entries)

	total := 0
	for _, p := range phases {
		if p.SegName == "T1" {
			total += p.SampleCount
		}
	}
	// T1 now runs [0.40, 0.70) — 300 samples rather than its geometric 200.
	if total < 290 || total > 310 {
		t.Errorf("T1 sample count: got %d, want ~300 — the onset on S1 was not honoured", total)
	}
}

func phasesFor(phases []Phase, name string) []Phase {
	var out []Phase
	for _, p := range phases {
		if p.SegName == name {
			out = append(out, p)
		}
	}
	return out
}

// ---- officialTimePlausible ----

// lapWithSpan builds a lap whose samples cover spanSecs of session time.
func lapWithSpan(spanSecs float64) Lap {
	n := MinSamplesForValidLap + 100
	samples := make([]SampleData, n)
	for i := range samples {
		frac := float64(i) / float64(n-1)
		samples[i] = SampleData{
			LapDistPct:  float32(frac),
			SessionTime: frac * spanSecs,
			Speed:       50,
		}
	}
	return Lap{Number: 1, Kind: KindFlying, Samples: samples}
}

func TestOfficialTimePlausible(t *testing.T) {
	tests := []struct {
		name     string
		span     float64
		official float32
		want     bool
	}{
		{"exact match", 109.88, 109.88, true},
		{"official slightly longer (S/F fractions)", 109.85, 109.88, true},
		{"at the tolerance", 109.88, 110.38, true},
		{"just past the tolerance", 109.88, 110.50, false},
		// The real case: iRacing published 1:57.032 for a lap whose samples
		// and sector times both said 1:50.97.
		{"observed six-second phantom", 110.967, 117.032, false},
		{"phantom short value", 109.88, 95.0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lap := lapWithSpan(tc.span)
			if got := officialTimePlausible(&lap, tc.official); got != tc.want {
				t.Errorf("officialTimePlausible(span=%.3f, official=%.3f) = %v, want %v",
					tc.span, tc.official, got, tc.want)
			}
		})
	}
}

// TestOfficialTimePlausible_ShortLapTrusted verifies that a lap with too few
// samples to have a meaningful span is trusted rather than second-guessed.
func TestOfficialTimePlausible_ShortLapTrusted(t *testing.T) {
	lap := Lap{Samples: make([]SampleData, 10)}
	if !officialTimePlausible(&lap, 500) {
		t.Error("a lap below MinSamplesForValidLap should trust the published time")
	}
}
