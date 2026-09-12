package iracing

import (
	"math"
	"sync"
	"testing"
)

// fuelRun drives a tracker at 1 Hz of SessionTime.
type fuelRun struct {
	t      *FuelTracker
	time   float64
	lc     int32
	litres float32
	pit    bool
	uid    int32
	num    int32
}

func newFuelRun(litres float32) *fuelRun {
	return &fuelRun{t: &FuelTracker{}, litres: litres, uid: 1}
}

func (r *fuelRun) sample() FuelEstimate {
	return r.t.Observe(FuelSample{
		SessionUniqueID: r.uid, SessionNum: r.num, SessionTime: r.time,
		LapCompleted: r.lc, Litres: r.litres, OnPitRoad: r.pit, Valid: true,
	})
}

// lap burns `used` litres over ten 1-second samples and then crosses the line.
func (r *fuelRun) lap(used float32) FuelEstimate {
	for i := 0; i < 10; i++ {
		r.time++
		r.litres -= used / 10
		r.sample()
	}
	r.time++
	r.lc++
	return r.sample()
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

func TestFuelTracker_FirstLapIsNeverMeasured(t *testing.T) {
	// The tracker joins mid-lap: that lap has no observed starting level,
	// so crossing the line only establishes the baseline.
	r := newFuelRun(40)
	r.sample()
	if e := r.lap(2.5); e.Laps != 0 {
		t.Fatalf("partial first lap was measured: %+v", e)
	}
	if e := r.lap(2.5); e.Laps != 1 || !near(e.LastLap, 2.5) {
		t.Fatalf("first full lap = %+v, want one lap of 2.5 L", e)
	}
}

func TestFuelTracker_AverageWorstAndLapsLeft(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(9) // baseline lap
	r.lap(2.0)
	r.lap(3.0)
	e := r.lap(2.5)

	if e.Laps != 3 {
		t.Fatalf("laps = %d, want 3", e.Laps)
	}
	if !near(e.Average, 2.5) || !near(e.Worst, 3.0) || !near(e.LastLap, 2.5) {
		t.Errorf("estimate = %+v, want avg 2.5, worst 3.0, last 2.5", e)
	}
	// 40 − 9 − 2 − 3 − 2.5 = 23.5 L left.
	if !near(e.LapsLeftAverage, 23.5/2.5) || !near(e.LapsLeftWorst, 23.5/3.0) {
		t.Errorf("laps left = %v avg / %v worst", e.LapsLeftAverage, e.LapsLeftWorst)
	}
}

func TestFuelTracker_WindowKeepsRecentLaps(t *testing.T) {
	r := newFuelRun(100)
	r.sample()
	r.lap(1)
	r.lap(9) // heavy early lap that must fall out of the window
	var e FuelEstimate
	for i := 0; i < FuelWindowLaps; i++ {
		e = r.lap(2)
	}
	if e.Laps != FuelWindowLaps || !near(e.Worst, 2) {
		t.Errorf("estimate = %+v, want %d laps all at 2 L", e, FuelWindowLaps)
	}
}

func TestFuelTracker_RefuelledLapIsDiscarded(t *testing.T) {
	r := newFuelRun(20)
	r.sample()
	r.lap(2)
	r.lap(2)
	// Refuel mid-lap without pit road (a garage reset does this).
	r.time++
	r.litres += 30
	r.sample()
	e := r.lap(2)
	if e.Laps != 1 {
		t.Fatalf("refuelled lap was measured: %+v", e)
	}
	if e = r.lap(2.2); e.Laps != 2 || !near(e.LastLap, 2.2) {
		t.Errorf("lap after refuel = %+v, want measured normally", e)
	}
}

func TestFuelTracker_PitRoadTaintsInAndOutLaps(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2) // one measured lap

	// In lap: pit road before the line.
	r.time++
	r.pit = true
	r.sample()
	r.lap(1.5)
	// Out lap: still on pit road at the crossing, leaves it after.
	r.time++
	r.pit = false
	e := r.lap(1.8)
	if e.Laps != 1 {
		t.Fatalf("pit laps were measured: %+v", e)
	}
	if e = r.lap(2.1); e.Laps != 2 || !near(e.LastLap, 2.1) {
		t.Errorf("first racing lap after the stop = %+v", e)
	}
}

func TestFuelTracker_GapInObservationDropsLap(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2)

	// The panel is closed for a minute and the line is crossed unseen.
	r.time += 60
	r.lc++
	r.litres -= 5
	r.sample()
	if e := r.lap(2); e.Laps != 1 {
		t.Fatalf("lap whose baseline was taken after a gap was measured: %+v", e)
	}
	if e := r.lap(2); e.Laps != 2 {
		t.Errorf("tracker did not resume after the gap: %+v", e)
	}
}

func TestFuelTracker_LapCounterJumpDropsLap(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2)
	for i := 0; i < 10; i++ {
		r.time++
		r.litres -= 0.2
		r.sample()
	}
	r.time++
	r.lc += 2
	if e := r.sample(); e.Laps != 1 {
		t.Errorf("jumped lap counter was measured: %+v", e)
	}
}

func TestFuelTracker_NewSessionClearsHistory(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2)
	r.num = 1 // practice → qualify
	r.time = 0
	r.lc = 0
	if e := r.sample(); e.Laps != 0 || e.LastLap != 0 {
		t.Errorf("history survived a session change: %+v", e)
	}
}

func TestFuelTracker_InvalidSampleKeepsHistoryButBreaksLap(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2)

	// Out of the car briefly; the laps already measured still stand.
	if e := r.t.Observe(FuelSample{SessionUniqueID: 1}); e.Laps != 1 {
		t.Fatalf("invalid sample cleared history: %+v", e)
	}
	r.time++
	r.sample()
	if e := r.lap(3); e.Laps != 1 {
		t.Errorf("lap interrupted by an invalid sample was measured: %+v", e)
	}
}

func TestFuelTracker_SessionChangeWhileOutOfCar(t *testing.T) {
	r := newFuelRun(40)
	r.sample()
	r.lap(2)
	r.lap(2)
	r.t.Observe(FuelSample{}) // leave the car
	r.num = 2
	if e := r.sample(); e.Laps != 0 {
		t.Errorf("history survived a session change made out of the car: %+v", e)
	}
}

func TestFuelTracker_ConcurrentObserve(t *testing.T) {
	var tr FuelTracker
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tr.Observe(FuelSample{SessionTime: float64(i) / 10, Litres: 30, Valid: true})
			}
		}()
	}
	wg.Wait()
}

func TestEnumNames(t *testing.T) {
	if SkiesName(0) != "Clear" || SkiesName(3) != "Overcast" || SkiesName(9) != "Unknown" {
		t.Error("SkiesName mapping wrong")
	}
	if TrackWetnessName(1) != "Dry" || TrackWetnessName(7) != "Extremely wet" ||
		TrackWetnessName(0) != "Unknown" {
		t.Error("TrackWetnessName mapping wrong")
	}
}
