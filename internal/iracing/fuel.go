package iracing

import "sync"

// Fuel tracking tunables.
const (
	// FuelWindowLaps is how many measured laps the live burn rate averages.
	// A rolling window rather than the whole session because consumption
	// changes within one: a lighter car, worn tyres, a lift-and-coast stint.
	// The figure a driver plans the next stop on is the recent one.
	FuelWindowLaps = 5

	// fuelRefuelEpsilon is how far the level may rise between two samples
	// before the lap is treated as refuelled. Burn only ever lowers the level;
	// the allowance covers float noise, not a real top-up.
	fuelRefuelEpsilon = 0.05 // litres

	// fuelMaxSampleGap is the longest the tracker may go unobserved before it
	// stops trusting the lap in progress. The live stream's slowest rate is
	// 1 Hz; anything longer means the panel was closed, and whatever happened
	// in between (a pit stop, a tow) was not seen.
	fuelMaxSampleGap = 3.0 // seconds of SessionTime
)

// FuelSample is what the tracker needs from one shared-memory read.
type FuelSample struct {
	SessionUniqueID int32   // SessionUniqueID
	SessionNum      int32   // SessionNum — practice → qualify → race within one event
	SessionTime     float64 // SessionTime, s
	LapCompleted    int32   // LapCompleted
	Litres          float32 // FuelLevel
	OnPitRoad       bool    // OnPitRoad
	// Valid is false when the sample says nothing about burn: fuel not
	// published, or the player not in the car (IsOnTrack=false — in the
	// garage the tank can be changed with no pit road involved).
	Valid bool
}

// FuelEstimate is the tracker's current view of consumption. Laps == 0 means
// no lap has been measured yet and every rate is zero.
type FuelEstimate struct {
	Laps    int     // measured laps behind Average/Worst, at most FuelWindowLaps
	LastLap float32 // litres used on the most recently measured lap
	Average float32 // mean litres per lap over the window
	// Worst is the heaviest lap in the window. Planning on the average runs dry
	// half the time; the worst lap is what a stint has to survive — the same
	// reason analyze reports both.
	Worst float32

	LapsLeftAverage float32 // current Litres / Average
	LapsLeftWorst   float32 // current Litres / Worst
}

// FuelTracker turns a stream of snapshots into per-lap consumption.
//
// Consumption is the difference between tank readings at consecutive lap
// boundaries, never an integration of FuelUsePerHour: that channel is an
// instantaneous reading, and integrating it accumulates error around a lap
// (the same decision analysis.ComputeFuel makes for recorded sessions).
//
// A lap is only measured if the tracker watched all of it. It is discarded
// when it did not start at an observed boundary, when the level rose (a
// refuel), when the car touched pit road (in and out laps cover less than a
// racing lap of work), when the lap counter jumped, or when samples stopped
// arriving for longer than fuelMaxSampleGap. Discarding is always safe —
// the cost is a slower first estimate — whereas a refuelled lap counted as a
// negative burn, or a pit-lane lap counted as a light one, would quietly
// overstate how far the tank goes.
//
// Safe for concurrent use: two browser tabs streaming at once both feed it.
type FuelTracker struct {
	mu sync.Mutex

	seen        bool // lastTime/lastLap/lastLitres describe an unbroken run of samples
	haveSession bool
	sessionUID  int32
	sessionNum  int32
	lastTime    float64
	lastLap     int32
	lastLitres  float32
	baseSet     bool // baseLitres was taken at an observed lap boundary
	baseLitres  float32
	lapTainted  bool
	history     []float32
	lastMeasure float32
}

// Observe feeds one sample and returns the estimate after it.
func (t *FuelTracker) Observe(s FuelSample) FuelEstimate {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !s.Valid {
		// Nothing about this lap can be trusted any more, but the laps
		// already measured still describe the car.
		t.baseSet = false
		t.seen = false
		return t.estimate(0)
	}

	if t.haveSession && (s.SessionUniqueID != t.sessionUID || s.SessionNum != t.sessionNum) {
		// A new session is a different car state (and often a different
		// car); carrying burn history across would blend them.
		t.history = nil
		t.lastMeasure = 0
		t.seen = false
	}

	t.haveSession = true
	t.sessionUID, t.sessionNum = s.SessionUniqueID, s.SessionNum

	// A clock that went backwards (a reset) or a gap in observation (the panel
	// was closed) breaks the run exactly like a first sample does. A lap
	// change seen after a gap happened at some unknown point during it, so it
	// must not become the next lap's baseline — that would under-measure it.
	if dt := s.SessionTime - t.lastTime; !t.seen || dt < 0 || dt > fuelMaxSampleGap {
		t.seen = true
		t.lastTime, t.lastLap, t.lastLitres = s.SessionTime, s.LapCompleted, s.Litres
		t.baseSet = false
		t.lapTainted = s.OnPitRoad
		return t.estimate(s.Litres)
	}

	if s.Litres > t.lastLitres+fuelRefuelEpsilon || s.OnPitRoad {
		t.lapTainted = true
	}

	if s.LapCompleted != t.lastLap {
		if t.baseSet && !t.lapTainted && s.LapCompleted == t.lastLap+1 {
			if used := t.baseLitres - s.Litres; used > 0 {
				t.history = append(t.history, used)
				if len(t.history) > FuelWindowLaps {
					t.history = t.history[len(t.history)-FuelWindowLaps:]
				}
				t.lastMeasure = used
			}
		}
		// Whatever happened to the lap just finished, this sample is a
		// boundary the tracker watched, so the next lap starts clean.
		t.baseSet = true
		t.baseLitres = s.Litres
		t.lapTainted = s.OnPitRoad
	}

	t.lastTime, t.lastLap, t.lastLitres = s.SessionTime, s.LapCompleted, s.Litres
	return t.estimate(s.Litres)
}

func (t *FuelTracker) estimate(litres float32) FuelEstimate {
	e := FuelEstimate{Laps: len(t.history), LastLap: t.lastMeasure}
	if e.Laps == 0 {
		return e
	}
	var sum float32
	for _, u := range t.history {
		sum += u
		e.Worst = max(e.Worst, u)
	}
	e.Average = sum / float32(e.Laps)
	if litres > 0 {
		e.LapsLeftAverage = litres / e.Average
		e.LapsLeftWorst = litres / e.Worst
	}
	return e
}
