package analysis

import (
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

func TestComputeExitImpact_CornerToStraight(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.5, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		s := SampleData{LapDistPct: pct, SessionTime: float64(i) / 60}
		if pct < 0.5 {
			// Corner: steer, slow exit speed of 30 m/s at segment end.
			s.SteeringAngle = 45.0 / rad2deg
			s.Speed = 30
		} else {
			// Straight: speed ramps up to 70 m/s mid-straight then eases off.
			s.Speed = 70
		}
		samples[i] = s
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs, nil)

	impacts := ComputeExitImpact(segs, phases)
	if len(impacts) != 1 {
		t.Fatalf("len(impacts) = %d, want 1", len(impacts))
	}

	imp := impacts[0]
	if imp.CornerName != "T1" {
		t.Errorf("CornerName = %q, want T1", imp.CornerName)
	}
	if imp.StraightName != "S1" {
		t.Errorf("StraightName = %q, want S1", imp.StraightName)
	}
	wantExit := float32(30 * ms2kmh)
	if imp.CornerExitSpeedKPH < wantExit-1 || imp.CornerExitSpeedKPH > wantExit+1 {
		t.Errorf("CornerExitSpeedKPH = %.1f, want ~%.1f", imp.CornerExitSpeedKPH, wantExit)
	}
	wantPeak := float32(70 * ms2kmh)
	if imp.StraightPeakSpeedKPH < wantPeak-1 || imp.StraightPeakSpeedKPH > wantPeak+1 {
		t.Errorf("StraightPeakSpeedKPH = %.1f, want ~%.1f", imp.StraightPeakSpeedKPH, wantPeak)
	}
}

func TestComputeExitImpact_WrapsAroundToFirstSegment(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		s := SampleData{LapDistPct: pct, SessionTime: float64(i) / 60, Speed: 40}
		if pct >= 0.5 {
			s.SteeringAngle = 45.0 / rad2deg
		}
		samples[i] = s
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs, nil)

	impacts := ComputeExitImpact(segs, phases)
	if len(impacts) != 1 {
		t.Fatalf("len(impacts) = %d, want 1 (wraparound T1 -> S1)", len(impacts))
	}
	if impacts[0].CornerName != "T1" || impacts[0].StraightName != "S1" {
		t.Errorf("impact = %+v, want T1 -> S1 via wraparound", impacts[0])
	}
}

func TestComputeExitImpact_NoStraightAfterCorner(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T2", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		samples[i] = SampleData{
			LapDistPct:    pct,
			SessionTime:   float64(i) / 60,
			Speed:         30,
			SteeringAngle: 45.0 / rad2deg,
		}
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs, nil)

	impacts := ComputeExitImpact(segs, phases)
	if len(impacts) != 0 {
		t.Errorf("len(impacts) = %d, want 0 (no straight follows either corner)", len(impacts))
	}
}

func TestComputeExitImpact_EmptyInputs(t *testing.T) {
	if got := ComputeExitImpact(nil, nil); got != nil {
		t.Errorf("ComputeExitImpact(nil, nil) = %v, want nil", got)
	}
	segs := []trackmap.Segment{{Name: "S1", Kind: trackmap.KindStraight}}
	if got := ComputeExitImpact(segs, nil); got != nil {
		t.Errorf("ComputeExitImpact with no phases = %v, want nil", got)
	}
}
