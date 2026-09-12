# internal/iracing

Reads a live telemetry snapshot from iRacing's shared memory interface.

## What it does

Returns a `LiveData` struct with a snapshot of iRacing shared-memory state: session time, lap distance fraction, track/car names, and per-CarIdx arrays for every other car on track (lap distance, lap completed, estimated lap time, overall position, class position) plus a `Drivers` map (`CarIdx → DriverInfo`) parsed from the session info YAML. It also carries the player car's lap clock and delta channels (`Lap`), tank level (`Fuel`) and start/finish-line weather (`Conditions`). Consumed by the `live` subcommand (`cmd/motorhome/live.go`) to display current position and live gap to the car directly ahead/behind on track, and by the GUI's live panel for timing, fuel and conditions.

## How it works

iRacing exposes a named shared memory segment (`Local\IRSDKMemMapFileName` — exact name from the published SDK header `irsdk_defines.h`) that mirrors its in-memory data in real time. `ReadLiveData`:

1. Opens the mapping with `OpenFileMappingW` / `MapViewOfFile`
2. Reads `irsdk_header` status field — returns `Connected=false` if iRacing isn't in a live session
3. Builds a `map[string]varInfo` from the variable header array (type + data offset + array count per channel)
4. Finds the most-recent data buffer (highest `tickCount` among the four rolling buffers)
5. Reads `SessionTime` (float64) and `LapDistPct` (float32) from that buffer
6. Reads the `CarIdx*` arrays (64-wide) for every other car's position/lap/estimated time
   and the player-car scalars through `scalarReader`, whose accessors check the variable's type and report absence rather than returning zero
7. Parses `TrackDisplayName`, `CarScreenName`, `DriverCarIdx`, and the full `Drivers` list from the session info YAML embedded in shared memory

Array data is copied into Go slices before the memory view is unmapped so callers can use it after `ReadLiveData` returns.

All memory access is via `unsafe.Pointer` casts to avoid an extra copy — the mapped region is read-only.

## Architecture

| Symbol | Description |
|---|---|
| `LiveData` | Snapshot: `Connected`, `SessionTime`, `LapDistPct`, `Track`, `Car`, `SessionNum`, `SessionUniqueID`, `LapCompleted`, `IsOnTrack`, `OnPitRoad`, `Lap`, `Fuel`, `Conditions`, `MyCarIdx`, `CarIdxLapDistPct/LapCompleted/EstTime/Position/ClassPosition`, `Drivers`, `ErrMsg`. |
| `ReadLiveData()` | Single entry point; returns zero `LiveData` if iRacing is not running. Windows-only. |
| `LapTiming`, `LapDelta` | `LapCurrentLapTime`/`LapLastLapTime`/`LapBestLapTime` plus five `LapDeltaTo*` channels, each with its `_DD` rate and `_OK` validity flag. Cross-platform. |
| `FuelState` | `FuelLevel` (l), `FuelLevelPct`, `FuelUsePerHour` (kg/h, instantaneous). `Available` is false when the level is not published. Cross-platform. |
| `Conditions`, `SkiesName`, `TrackWetnessName` | Weather at the S/F line. Every field is a pointer: nil means that variable is not published by this build. Cross-platform. |
| `FuelTracker`, `FuelSample`, `FuelEstimate`, `FuelWindowLaps` | Stateful per-lap burn from a stream of snapshots — see below. Cross-platform. |
| `DriverInfo` | One driver block from the session YAML: `CarIdx`, `UserName`, `CarScreenName`, `CarNumber`, `CarClassID`. |
| `ParseDrivers(yaml)` | Returns `map[int32]DriverInfo` keyed by CarIdx. Skips sentinel empty slots (e.g. iRacing's `CarIdx: 255` with blank fields). Cross-platform. |
| `DriverCarIdxFromYAML(yaml)` | Extracts the player's CarIdx. Returns `-1` if absent. Cross-platform. |
| `CarPos`, `GapTo`, `ComputeGaps`, `NoGap` | Gap-to-car-ahead/behind computation. Given the player's `CarPos` and a slice of other cars, returns the two closest on-track with a signed time gap. Falls back to `distPct × lapEstimate` when per-car `EstTime` isn't usable. When no candidate exists in a direction the returned `GapTo` equals the `NoGap` sentinel (`CarIdx == -1`) — callers must check for that, not zero-value, because CarIdx 0 is a real slot (typically the pace car). Cross-platform. |

`ErrMsg` is set (but `Connected` remains false) when the call fails for a diagnosable reason (e.g. `OpenFileMappingW` error) so callers can distinguish "not running" from "unexpected failure".

Gap math, drivers parsing, fuel tracking, and core types live in platform-neutral files (`gap.go`, `drivers.go`, `player.go`, `fuel.go`) so they compile and unit-test on any platform. The memory-mapping read path (`live_windows.go`) is Windows-only (`//go:build windows`).

## Player scalars

Names, types and units were checked against the variable table a running
iRacing build publishes (2026-09-12, 328 variables), not only against the SDK
header — `Precipitation`, `TrackWetness` and `WeatherDeclaredWet` postdate it.
Worth knowing when reading them:

- **Deltas need their `_OK` flag.** On an out lap, or with no session best yet,
  iRacing keeps publishing a value (observed as `0` or stale) with `_OK=false`.
- **Lap times of `0` mean "none yet"**; `LapLastLapTime` of `-1` marks an
  invalidated lap.
- **`FuelUsePerHour` is kg/h**, and iRacing does not publish fuel density, so it
  cannot be turned into litres here.
- **`TrackTemp` is deprecated** and mirrors `TrackTempCrew`, which is what is read.
- **`WindDir` is radians**, and iRacing does not document whether it is the
  direction the wind blows from or towards.
- `LapDeltaToSessionLastlLap` really is spelled with the extra `l`.

## Fuel tracking

`FuelTracker` measures consumption as the **difference in `FuelLevel` between
consecutive lap boundaries** — never by integrating `FuelUsePerHour`, the same
decision `analysis.ComputeFuel` makes for recorded sessions. It averages the
last `FuelWindowLaps` (5) measured laps rather than the whole session, because
burn drifts with fuel load, tyre wear and driving style, and the next stop is
planned on the recent figure. It reports the worst lap alongside the average,
because a stint planned on the average runs dry half the time.

A lap is only measured if the tracker watched all of it. It is **discarded**
when:

- it did not start at an observed boundary (the tracker joined mid-lap);
- the level rose during it (a refuel, including a garage reset);
- the car touched pit road (in and out laps are not racing laps);
- the lap counter jumped by more than one;
- samples stopped for more than 3 s of `SessionTime`, or the clock went
  backwards — whatever happened in between was not seen;
- a sample was invalid (fuel unpublished, or `IsOnTrack` false).

A change of `SessionUniqueID` or `SessionNum` clears the history. Discarding is
always the safe direction: it costs a slower first estimate, whereas a
refuelled lap counted as negative burn, or a pit-lane lap as a light one,
would overstate how far the tank goes.

The consequence is that the estimate only exists for laps something was
streaming through. In the GUI that means the live panel was open.
