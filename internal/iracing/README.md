# internal/iracing

Reads a live telemetry snapshot from iRacing's shared memory interface.

## What it does

Returns a `LiveData` struct with a snapshot of iRacing shared-memory state: session time, lap distance fraction, track/car names, and per-CarIdx arrays for every other car on track (lap distance, lap completed, estimated lap time, overall position, class position) plus a `Drivers` map (`CarIdx → DriverInfo`) parsed from the session info YAML. Consumed by the `live` subcommand (`cmd/motorhome/live.go`) to display current position and live gap to the car directly ahead/behind on track.

## How it works

iRacing exposes a named shared memory segment (`Local\IRSDKMemMapFileName` — exact name from the published SDK header `irsdk_defines.h`) that mirrors its in-memory data in real time. `ReadLiveData`:

1. Opens the mapping with `OpenFileMappingW` / `MapViewOfFile`
2. Reads `irsdk_header` status field — returns `Connected=false` if iRacing isn't in a live session
3. Builds a `map[string]varInfo` from the variable header array (type + data offset + array count per channel)
4. Finds the most-recent data buffer (highest `tickCount` among the four rolling buffers)
5. Reads `SessionTime` (float64) and `LapDistPct` (float32) from that buffer
6. Reads the `CarIdx*` arrays (64-wide) for every other car's position/lap/estimated time
7. Parses `TrackDisplayName`, `CarScreenName`, `DriverCarIdx`, and the full `Drivers` list from the session info YAML embedded in shared memory

Array data is copied into Go slices before the memory view is unmapped so callers can use it after `ReadLiveData` returns.

All memory access is via `unsafe.Pointer` casts to avoid an extra copy — the mapped region is read-only.

## Architecture

| Symbol | Description |
|---|---|
| `LiveData` | Snapshot: `Connected`, `SessionTime`, `LapDistPct`, `Track`, `Car`, `MyCarIdx`, `CarIdxLapDistPct/LapCompleted/EstTime/Position/ClassPosition`, `Drivers`, `ErrMsg`. |
| `ReadLiveData()` | Single entry point; returns zero `LiveData` if iRacing is not running. Windows-only. |
| `DriverInfo` | One driver block from the session YAML: `CarIdx`, `UserName`, `CarScreenName`, `CarNumber`, `CarClassID`. |
| `ParseDrivers(yaml)` | Returns `map[int32]DriverInfo` keyed by CarIdx. Skips sentinel empty slots (e.g. iRacing's `CarIdx: 255` with blank fields). Cross-platform. |
| `DriverCarIdxFromYAML(yaml)` | Extracts the player's CarIdx. Returns `-1` if absent. Cross-platform. |
| `CarPos`, `GapTo`, `ComputeGaps`, `NoGap` | Gap-to-car-ahead/behind computation. Given the player's `CarPos` and a slice of other cars, returns the two closest on-track with a signed time gap. Falls back to `distPct × lapEstimate` when per-car `EstTime` isn't usable. When no candidate exists in a direction the returned `GapTo` equals the `NoGap` sentinel (`CarIdx == -1`) — callers must check for that, not zero-value, because CarIdx 0 is a real slot (typically the pace car). Cross-platform. |

`ErrMsg` is set (but `Connected` remains false) when the call fails for a diagnosable reason (e.g. `OpenFileMappingW` error) so callers can distinguish "not running" from "unexpected failure".

Gap math, drivers parsing, and core types live in platform-neutral files (`gap.go`, `drivers.go`) so they compile and unit-test on any platform. The memory-mapping read path (`live_windows.go`) is Windows-only (`//go:build windows`).
