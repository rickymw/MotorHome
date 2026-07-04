package analysis

import "github.com/rickymw/MotorHome/internal/trackmap"

// ExitImpact links a corner/chicane's exit speed to the peak speed reached on
// the straight that immediately follows it — the direct measure of whether a
// slow corner exit cost speed (and therefore time) down the next straight.
type ExitImpact struct {
	CornerName           string
	CornerExitSpeedKPH   float32
	StraightName         string
	StraightPeakSpeedKPH float32
}

// ComputeExitImpact pairs each corner/chicane segment with the straight
// segment immediately following it (including wraparound from the last
// segment to the first) and reports the corner's exit speed alongside the
// peak speed reached on that straight. Segments with no computed phases
// (e.g. a truncated final lap) are skipped.
func ComputeExitImpact(segs []trackmap.Segment, phases []Phase) []ExitImpact {
	n := len(segs)
	if n == 0 || len(phases) == 0 {
		return nil
	}

	// Group phases by segment index, preserving entry/mid/exit order.
	bySeg := make(map[int][]Phase)
	for _, p := range phases {
		bySeg[p.SegIndex] = append(bySeg[p.SegIndex], p)
	}

	var out []ExitImpact
	for i := 0; i < n; i++ {
		if segs[i].Kind == trackmap.KindStraight {
			continue
		}
		next := (i + 1) % n
		if segs[next].Kind != trackmap.KindStraight {
			continue
		}

		cornerPhases := bySeg[i]
		straightPhases := bySeg[next]
		if len(cornerPhases) == 0 || len(straightPhases) == 0 {
			continue
		}

		out = append(out, ExitImpact{
			CornerName:           segs[i].Name,
			CornerExitSpeedKPH:   cornerPhases[len(cornerPhases)-1].SpeedExitKPH,
			StraightName:         segs[next].Name,
			StraightPeakSpeedKPH: straightPhases[0].PeakSpeedKPH,
		})
	}

	return out
}
