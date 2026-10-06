# internal/pb

Personal best lap time tracking per car/track combination.

## What it does

Loads, updates, and saves a flat JSON store of the fastest recorded lap for each car+track pair. Called by `analyze` after every session.

## How it works

The store is a `map[string]*PersonalBest` keyed by `"Car|Track"`. On each `analyze` run the best flying lap is compared against the stored PB; if it's faster (or no PB exists yet), the entry is updated and the file is saved. `Update` returns `true` when a new PB is set so the caller can print a notification.

When a new PB is set, three extras are saved alongside the lap time: the per-segment phase data, the raw `CarSetup:` YAML block from the session, and the rolling brake-entry positions (kept across PB swaps). On subsequent sessions, `analyze` prints a delta comparison table ("vs PB") showing speed, braking, throttle, lateral G, and error count differences per segment/phase, so it's immediately visible where time is being lost or gained relative to the PB. `analyze -lap pb` re-renders the stored PB (header, setup tables, phase table) from this data without needing the original `.ibt`.

Entries with `LapTime == 0` are stub records created by `BrakeEntrySet` to hold accumulated brake data before any PB is set; `Update` treats them as "no PB yet" so the first real PB always replaces them.

`Save` uses atomic write (write-to-temp-then-rename) to prevent file corruption if interrupted mid-write.

## Sessions nobody analysed — the scan ledger

`analyze` only records a PB from the session it is run on, so a session that was never analysed could hold a lap faster than the stored PB and nothing would notice (found the hard way: a 1:39.954 sat unrecorded behind a 1:40.040 "PB"). `cmd/motorhome/pb_catchup.go` closes that gap by checking every session in `ibtDir` that has not been checked before, ahead of each `analyze`/`coach`.

`Scanned` (`scanned.go`) is the ledger of what has been checked, stored in `pbscan.json` next to `pb.json` and keyed by `.ibt` basename. Each entry carries the file's size and modification time, and `Seen` requires both to match — a session still being recorded when it was first checked grows afterwards, and must be checked again once finished. `LoadScanned` treats a missing file as an empty ledger, which is the first-run state: every session gets checked once.

`SourceFile` on `PersonalBest` records which `.ibt` the PB lap came from (`SetSource`). It exists so phase data can be filled in later: a PB caught up from a track that has never been analysed has no track map to compute phases against, and without a source to go back to, it would have no vs-PB table until it was beaten.

## Reading the store back

The store is never pruned by `analyze`, so it grows one entry per car/track combination indefinitely. The `pb` subcommand (`cmd/motorhome/pb.go`) is the way in: `pb list` shows every entry and which optional payloads it carries, `pb show` re-renders one, `pb diff` compares a session's setup against the stored PB setup, and `pb prune` removes entries — previewing by default, writing only with `-apply`.

## Architecture

| Symbol | Description |
|---|---|
| `PersonalBest` | Lap time (seconds + formatted string), date, weather, car, track, brake entries, PB phases, raw `CarSetup:` YAML, source `.ibt` basename. |
| `PBPhase` | Per-phase telemetry snapshot (speed, brake, throttle, lat G, corrections, ABS/lockup/spin/coast). |
| `File` | `map[string]*PersonalBest` — the top-level JSON type. |
| `Scanned` | `map[string]ScanStamp` — `pbscan.json`, the sessions already checked for PBs. `Seen`/`Mark` key on basename + size + modification time. |

### Key functions

```go
pbf, err := pb.Load("pb.json")

isNew := pb.Update(pbf, car, track, lapTime, "2:11.367", "2026-03-31", "Air 22°C, Track 35°C")
if isNew {
    pb.SetPhases(pbf, car, track, pbPhases)                         // phase table for "vs PB" deltas
    pb.SetSetup(pbf, car, track, analysis.ExtractCarSetupBlock(yaml)) // setup snapshot for offline review
}

err = pb.Save("pb.json", pbf)
```

`AdoptLegacyKey(pbf, car, track)` moves an entry stored under the U+FFFD-mangled key written before session YAML was decoded from Windows-1252 (see [internal/textenc](../textenc/README.md)) to `Key(car, track)`, fixing its `Car`/`Track` fields; an entry already under the correct key wins. `analyze` calls it and saves; `pb diff` and `analyze -lap pb` call it in memory only, since a read should not rewrite the store.

`Key(car, track)` returns the map key; `Load` returns an empty `File` (not an error) when the file does not yet exist. `SetPhases` and `SetSetup` are no-ops if no entry exists; both fields are cleared when a new PB replaces an old one (the caller must repopulate them after `Update`).
