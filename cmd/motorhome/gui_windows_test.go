//go:build windows

package main

import (
	"math"
	"testing"

	"github.com/rickymw/MotorHome/internal/iracing"
)

func f32(v float32) *float32 { return &v }
func i32(v int32) *int32     { return &v }

// connectedFixture is a player on track with the scalars a current build
// publishes, values taken from a real MX-5 Cup session at Okayama.
func connectedFixture() iracing.LiveData {
	ld := liveFixture()
	ld.SessionUniqueID = 1
	ld.SessionTime = 583.5
	ld.LapCompleted = 3
	ld.IsOnTrack = true
	ld.Lap = iracing.LapTiming{
		Current: 55.004, Last: 107.999, Best: 107.999, BestLapNum: 3,
		ToBest:        iracing.LapDelta{Seconds: -0.254, Rate: 0.0502, Valid: true},
		ToSessionBest: iracing.LapDelta{Seconds: 4.1, Valid: false},
	}
	ld.Fuel = iracing.FuelState{Available: true, Litres: 20.75, Pct: 0.461, UsePerHourKg: 23.55}
	ld.Conditions = iracing.Conditions{
		AirTempC:      f32(27.15),
		TrackTempC:    f32(40.56),
		WindDirRad:    f32(float32(math.Pi)),
		AirPressurePa: f32(98315),
		Skies:         i32(1),
		TrackWetness:  i32(1),
	}
	return ld
}

func TestSnapshotFromLive_NotConnected(t *testing.T) {
	var tr iracing.FuelTracker
	snap := snapshotFromLive(iracing.LiveData{ErrMsg: "OpenFileMappingW: nope"}, &tr)
	if snap.Connected || snap.Detail != "OpenFileMappingW: nope" || snap.Message == "" {
		t.Errorf("snapshot = %+v", snap)
	}
	if snap.Timing != nil || snap.Fuel != nil || snap.Conditions != nil {
		t.Error("disconnected snapshot carried telemetry groups")
	}
}

func TestSnapshotFromLive_PositionAndLap(t *testing.T) {
	var tr iracing.FuelTracker
	snap := snapshotFromLive(connectedFixture(), &tr)
	if !snap.Connected || snap.Track != "Watkins Glen" || snap.Position == 0 || snap.Lap == 0 {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestSnapshotFromLive_Timing(t *testing.T) {
	var tr iracing.FuelTracker
	snap := snapshotFromLive(connectedFixture(), &tr)

	tm := snap.Timing
	if tm == nil || tm.CurrentLap != 55.004 || tm.LastLap != 107.999 || tm.BestLapNum != 3 {
		t.Fatalf("timing = %+v", tm)
	}
	refs := map[string]bool{}
	for _, d := range tm.Deltas {
		refs[d.Ref] = true
		switch d.Ref {
		case "best":
			if !d.Valid || d.Seconds != -0.254 || d.Rate != 0.0502 {
				t.Errorf("best delta = %+v", d)
			}
		case "sessionBest":
			// The _OK flag must travel with the value, or the page has no way to
			// suppress a delta to a lap that does not exist.
			if d.Valid {
				t.Errorf("sessionBest delta lost its invalid flag: %+v", d)
			}
		}
	}
	for _, want := range []string{"best", "optimal", "sessionBest", "sessionOptimal", "last"} {
		if !refs[want] {
			t.Errorf("missing delta ref %q", want)
		}
	}
}

func TestSnapshotFromLive_FuelIsAbsentWhenUnpublished(t *testing.T) {
	var tr iracing.FuelTracker
	ld := connectedFixture()
	ld.Fuel = iracing.FuelState{}
	if snap := snapshotFromLive(ld, &tr); snap.Fuel != nil {
		t.Errorf("fuel = %+v, want nil when FuelLevel is not published", snap.Fuel)
	}
}

// The provider's tracker persists across frames, so a lap watched through the
// snapshot path produces a burn estimate.
func TestSnapshotFromLive_FuelTrackerAcrossLaps(t *testing.T) {
	var tr iracing.FuelTracker
	ld := connectedFixture()
	step := func(dt float64, dl float32, lapDone bool) *float32 {
		ld.SessionTime += dt
		ld.Fuel.Litres -= dl
		if lapDone {
			ld.LapCompleted++
		}
		snap := snapshotFromLive(ld, &tr)
		return &snap.Fuel.PerLapAverage
	}

	snapshotFromLive(ld, &tr)
	step(1, 0.1, true) // boundary: baseline
	for i := 0; i < 10; i++ {
		step(1, 0.25, false)
	}
	avg := step(1, 0, true)
	if math.Abs(float64(*avg-2.5)) > 1e-3 {
		t.Errorf("per-lap average = %v, want 2.5", *avg)
	}

	snap := snapshotFromLive(ld, &tr)
	if snap.Fuel.MeasuredLaps != 1 || snap.Fuel.WindowLaps != iracing.FuelWindowLaps {
		t.Errorf("fuel = %+v", snap.Fuel)
	}
}

func TestSnapshotFromLive_FuelNotMeasuredOutOfCar(t *testing.T) {
	var tr iracing.FuelTracker
	ld := connectedFixture()
	ld.IsOnTrack = false
	for i := 0; i < 30; i++ {
		ld.SessionTime++
		ld.Fuel.Litres -= 0.2
		if i%10 == 0 {
			ld.LapCompleted++
		}
		if snap := snapshotFromLive(ld, &tr); snap.Fuel.MeasuredLaps != 0 {
			t.Fatalf("measured a lap while not in the car: %+v", snap.Fuel)
		}
	}
}

func TestSnapshotFromLive_Conditions(t *testing.T) {
	var tr iracing.FuelTracker
	c := snapshotFromLive(connectedFixture(), &tr).Conditions
	if c == nil {
		t.Fatal("conditions = nil")
	}
	if c.WindDirDeg == nil || math.Abs(float64(*c.WindDirDeg-180)) > 1e-3 {
		t.Errorf("wind dir = %v, want 180°", c.WindDirDeg)
	}
	if c.AirPressureHPa == nil || math.Abs(float64(*c.AirPressureHPa-983.15)) > 1e-2 {
		t.Errorf("pressure = %v, want 983.15 hPa", c.AirPressureHPa)
	}
	if c.Skies != "Partly cloudy" || c.TrackWetness != "Dry" {
		t.Errorf("enums = %q / %q", c.Skies, c.TrackWetness)
	}
	if c.Humidity != nil || c.Precipitation != nil {
		t.Error("unpublished variables became values")
	}
}

func TestSnapshotFromLive_WindDirectionWrapsNegative(t *testing.T) {
	var tr iracing.FuelTracker
	ld := connectedFixture()
	ld.Conditions.WindDirRad = f32(float32(-math.Pi / 2))
	c := snapshotFromLive(ld, &tr).Conditions
	if math.Abs(float64(*c.WindDirDeg-270)) > 1e-3 {
		t.Errorf("wind dir = %v, want 270°", *c.WindDirDeg)
	}
}

func TestSnapshotFromLive_NoConditionsPublished(t *testing.T) {
	var tr iracing.FuelTracker
	ld := connectedFixture()
	ld.Conditions = iracing.Conditions{}
	if c := snapshotFromLive(ld, &tr).Conditions; c != nil {
		t.Errorf("conditions = %+v, want nil", c)
	}
}
