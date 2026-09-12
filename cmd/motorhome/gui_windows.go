//go:build windows

package main

import (
	"math"

	"github.com/rickymw/MotorHome/internal/camera"
	"github.com/rickymw/MotorHome/internal/gui"
	"github.com/rickymw/MotorHome/internal/iracing"
	"github.com/rickymw/MotorHome/internal/shaker"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

// attachPlatformDeps fills in the providers that only exist on Windows:
// iRacing shared memory, SetupAPI device enumeration, the service control
// manager, and WinMM audio output. internal/gui stays free of all of them so it
// compiles and tests on any OS; this file is the only place they meet.
func attachPlatformDeps(deps *gui.Deps) {
	deps.Live = &liveProvider{}
	deps.USB = usbProvider{}
	deps.Camera = camera.NewRestarter()
	deps.Shaker = shaker.NewPlayer()
}

// usbProvider builds a controller per call rather than holding one.
//
// The device list is now editable through the settings panel, and a controller
// constructed once at startup would keep matching against whatever the server
// booted with — a device added through the picker would not show up until a
// restart, which is exactly the friction the picker exists to remove.
// Construction is a struct literal; there is nothing to reuse.
type usbProvider struct{}

func (usbProvider) Enumerate(known []usbdev.Known) ([]usbdev.Device, error) {
	return usbdev.NewController(known).Enumerate()
}

func (usbProvider) Scan(known []usbdev.Known) ([]usbdev.Scanned, error) {
	return usbdev.NewController(known).Scan()
}

// liveProvider holds the fuel tracker, which is why it is a pointer and lives
// for the whole server: consumption is measured across lap boundaries, and a
// provider rebuilt per request would forget every lap it had watched.
type liveProvider struct {
	fuel iracing.FuelTracker
}

func (p *liveProvider) Snapshot() gui.LiveSnapshot {
	return snapshotFromLive(iracing.ReadLiveData(), &p.fuel)
}

// snapshotFromLive reduces a shared-memory read to the panel's wire shape.
//
// Position and lap use the same helpers the `live` subcommand prints with, so
// the browser and the terminal cannot disagree about them. It is split from
// Snapshot so the conversion is testable without a running sim.
func snapshotFromLive(ld iracing.LiveData, fuel *iracing.FuelTracker) gui.LiveSnapshot {
	if !ld.Connected {
		// The Win32 reason goes in Detail rather than becoming the message.
		// Not connected is overwhelmingly "the sim is not running", and
		// "OpenFileMappingW: The system cannot find the file specified" is a
		// alarming way to say so on a panel someone glances at mid-session.
		return gui.LiveSnapshot{
			Connected: false,
			Message:   "iRacing is not running, or you are not on track",
			Detail:    ld.ErrMsg,
		}
	}

	snap := gui.LiveSnapshot{
		Connected:  true,
		Track:      ld.Track,
		Car:        ld.Car,
		LapDistPct: ld.LapDistPct,
		FieldSize:  countValidCars(ld.CarIdxLapDistPct),
		OnPitRoad:  ld.OnPitRoad,
	}

	if ld.MyCarIdx >= 0 {
		if p := idxValue(ld.CarIdxPosition, ld.MyCarIdx); p > 0 {
			snap.Position = int(p)
		}
		if cp := idxValue(ld.CarIdxClassPosition, ld.MyCarIdx); cp > 0 {
			snap.ClassPosition = int(cp)
		}
		// iRacing publishes -1 before the first S/F crossing; that is the out
		// lap, which the driver counts as lap 1.
		if lc := idxValue(ld.CarIdxLapCompleted, ld.MyCarIdx); lc < 0 {
			snap.Lap = 1
		} else {
			snap.Lap = int(lc + 1)
		}
		if mine, ok := ld.Drivers[ld.MyCarIdx]; ok && mine.CarClassID != 0 {
			for _, d := range ld.Drivers {
				if d.CarClassID == mine.CarClassID {
					snap.ClassSize++
				}
			}
		}
	}

	snap.Timing = liveTiming(ld.Lap)
	snap.Fuel = liveFuel(ld, fuel)
	snap.Conditions = liveConditions(ld.Conditions)
	return snap
}

func liveTiming(l iracing.LapTiming) *gui.LiveTiming {
	return &gui.LiveTiming{
		CurrentLap: l.Current,
		LastLap:    l.Last,
		BestLap:    max(l.Best, 0),
		BestLapNum: int(l.BestLapNum),
		Deltas: []gui.LiveDelta{
			liveDelta("best", l.ToBest),
			liveDelta("optimal", l.ToOptimal),
			liveDelta("sessionBest", l.ToSessionBest),
			liveDelta("sessionOptimal", l.ToSessionOptimal),
			liveDelta("last", l.ToLast),
		},
	}
}

func liveDelta(ref string, d iracing.LapDelta) gui.LiveDelta {
	return gui.LiveDelta{Ref: ref, Seconds: d.Seconds, Rate: d.Rate, Valid: d.Valid}
}

// liveFuel feeds the tracker on every connected frame, including ones where
// the tank is not published, so that a missing reading breaks the lap in
// progress rather than being skipped over.
func liveFuel(ld iracing.LiveData, tracker *iracing.FuelTracker) *gui.LiveFuel {
	est := tracker.Observe(iracing.FuelSample{
		SessionUniqueID: ld.SessionUniqueID,
		SessionNum:      ld.SessionNum,
		SessionTime:     ld.SessionTime,
		LapCompleted:    ld.LapCompleted,
		Litres:          ld.Fuel.Litres,
		OnPitRoad:       ld.OnPitRoad,
		Valid:           ld.Fuel.Available && ld.IsOnTrack,
	})
	if !ld.Fuel.Available {
		return nil
	}
	return &gui.LiveFuel{
		Litres:        ld.Fuel.Litres,
		Pct:           ld.Fuel.Pct,
		UsePerHourKg:  ld.Fuel.UsePerHourKg,
		MeasuredLaps:  est.Laps,
		WindowLaps:    iracing.FuelWindowLaps,
		LastLap:       est.LastLap,
		PerLapAverage: est.Average,
		PerLapWorst:   est.Worst,
		LapsLeftAvg:   est.LapsLeftAverage,
		LapsLeftWorst: est.LapsLeftWorst,
	}
}

// liveConditions converts units at the boundary so the page does no physics:
// wind direction to degrees, pressure to hPa. Enums become names here because
// the decoding tables live in internal/iracing next to the variables.
func liveConditions(c iracing.Conditions) *gui.LiveConditions {
	out := &gui.LiveConditions{
		AirTempC:      c.AirTempC,
		TrackTempC:    c.TrackTempC,
		Humidity:      c.Humidity,
		WindMS:        c.WindMS,
		AirDensity:    c.AirDensity,
		FogLevel:      c.FogLevel,
		Precipitation: c.Precipitation,
		DeclaredWet:   c.DeclaredWet,
	}
	if c.WindDirRad != nil {
		deg := float32(math.Mod(float64(*c.WindDirRad)*180/math.Pi+360, 360))
		out.WindDirDeg = &deg
	}
	if c.AirPressurePa != nil {
		hpa := *c.AirPressurePa / 100
		out.AirPressureHPa = &hpa
	}
	if c.Skies != nil {
		out.Skies = iracing.SkiesName(*c.Skies)
	}
	if c.TrackWetness != nil {
		out.TrackWetness = iracing.TrackWetnessName(*c.TrackWetness)
	}
	if *out == (gui.LiveConditions{}) {
		return nil
	}
	return out
}
