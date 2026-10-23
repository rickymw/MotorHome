# internal/trackmap

Geometry-based corner/straight detection and persistent track segment storage.

## What it does

- Detects corners, straights, and chicanes automatically from telemetry
- Persists results to `trackmap.json` keyed by track name
- Tracks geometry confidence (low/moderate/high) based on laps seen
- Scores how well a new session's telemetry matches stored boundaries

## How it works

### Files

| File | Contents |
|---|---|
| `detect.go` | Constants, types (`Sample`, `rawSeg`), detection entry points (`Detect`, `DetectFromMultiple`, `DetectFromMultipleLatLon`, `searchThresholds`, `countCornerSegs`, `detectRawFromProfiles`, `detectFromProfiles`) |
| `profiles.go` | Signal processing: `buildProfile`, `buildSpeedProfile`, `buildSteerProfile`, `buildPositionProfile`, `project`, `signedCurvature` |
| `postprocess.go` | Post-detection validation pipeline: `trimWraparoundCorner`, `confirmCorners`, `validateCornerSpeed`, `refineBoundaries`, `splitLargeCorners`, `findTroughs`, `findSplitPoints` |
| `util.go` | Bucket math, merge helpers, `MatchScore`, `labelSegments`: `allZero`, `bucketMean`, `bucketMinMax`, `fillGaps`, `boxSmooth`, `hysteresis`, `groupBuckets`, `avgSign`, `mergeShort`, `mergeIdx`, `mergeAt`, `mergeChicanes` |
| `trackmap.go` | `TrackMap`, `TrackMapFile`, `Segment`, `Load`, `Save` (atomic write-to-temp-then-rename), confidence logic |
| `trackref.go` | `TrackRef`, `TrackRefFile`, `LoadTrackRef` |
| `svg.go` | `RenderSVG` — draws one lap's GPS outline with segments labelled and selected ones highlighted (`MapOptions`, `Highlight`) |

### Detection pipeline

Both methods share the same final pipeline (`detectFromProfiles`):

1. **Bucket** samples into 1000 position bins (0.1% resolution)
2. **Forward-fill** empty bins from neighbours
3. **Box-smooth** with a window proportional to ~15 m of track
4. **Hysteresis classify**: enter corner at ≥ threshold, exit at < lower threshold
5. **Group** consecutive same-class buckets into raw segments
6. **Merge short** segments repeatedly until stable (min 1.2% straight, 0.6% corner)
7. **Merge chicanes**: `[corner, short-straight ≤ ~100 m, opposite-direction corner]` → single chicane
8. **Label**: S1/S2… for straights, T1/T2… for corners, T3-T4 for chicanes

**`latlon` method** (`DetectFromMultipleLatLon`): projects GPS lat/lon to local XY metres using equirectangular projection, accumulates bin-averaged positions across all laps, then computes signed curvature using triplets spaced ~20 m apart. Thresholds: κ ≥ 0.004 m⁻¹ (enter), < 0.0015 m⁻¹ (exit). Returns `nil` if lat/lon channels absent — caller falls back to lataccel.

After initial detection, the latlon path applies four post-processing validation steps:

1. **S/F wraparound trim** (`trimWraparoundCorner`): removes tiny GPS-artifact corners (< 50 m) at the start/finish line
2. **Steering/lateral-G confirmation** (`confirmCorners`): reclassifies corners as straights if they have neither meaningful steering (< 10° mean) nor lateral load (< 2.0 m/s²)
3. **Oversized corner splitting** (`splitLargeCorners`): splits corners > 200 m that contain multiple speed troughs separated by re-acceleration > 20 km/h
4. **Speed-profile validation** (`validateCornerSpeed`): reclassifies corners with flat speed (< 10 km/h variation) as straights

Chicane merging also enforces a maximum total length of 400 m to prevent merging genuinely separate corners (e.g. Redgate + Hollywood at Donington).

5. **Boundary refinement** (`refineBoundaries`): adjusts each straight↔corner boundary using steering and lateral-G profiles. If the straight side of a boundary has active cornering (steering ≥ 7° or lat-G ≥ 2.5), the boundary shifts into the straight. If the corner side has no cornering activity, the boundary shifts into the corner. Short straights that get absorbed are merged into adjacent corners.

### Target-guided detection (`trackref.go`)

When `trackref.json` exists alongside the binary, the latlon detector reads the expected corner count for the current track and searches across curvature threshold candidates to find the one that produces the correct number of corner segments. This eliminates the need to manually tune thresholds per track. Tracks not in the reference fall back to default thresholds.

### Corner names (`cornerNames`)

Segments are auto-labelled `T1`, `T2`, … in track order from S/F. Those are **positional labels, not iRacing's official turn numbers** — detection merges complexes, so at Road America 11 corners are detected against `TrackNumTurns: 14`, and every label after the first merge is offset.

`TrackRef.CornerNames` fixes this: one free-text entry per detected corner, in track order. An entry may be a number, a name, or both (`"T11 Kink"`); an empty entry leaves the generated label alone so a track can be annotated incrementally.

```json
"Road America": {
  "corners": 11,
  "cornerNames": ["T1", "", "T5", "", "", "T8 Carousel", "", "T11 Kink", "", "", ""]
}
```

`ApplyCornerNames` is **all-or-nothing on length**: if the list does not have exactly `CountCorners(segs)` entries, nothing is renamed and the caller warns. A short or stale list would otherwise shift every subsequent label, which is the silent mislabelling the feature exists to prevent.

Names are applied in memory on each run and are deliberately *not* written to `trackmap.json`, which detection regenerates.

**`lataccel` method** (`DetectFromMultiple`): averages `abs(LatAccel)` element-wise across laps. Thresholds: ≥ 5.0 m/s² (enter), < 2.5 m/s² (exit).

`MatchScore` re-runs the lataccel pipeline on the current session's best lap and checks whether each stored boundary has a transition within ±2%. Always uses lataccel for consistency. Returns 0.0–1.0.

### Map rendering (`svg.go`)

`RenderSVG(w, samples, segs, MapOptions)` writes a standalone SVG of one lap's outline. It exists so a debrief can *show* a corner: the segment names are positional, and "the last corner onto the start/finish straight" only works for a driver who already knows the circuit. Used by `analyze -map` / `coach -map`.

- **The outline is the lap's own GPS trace**, projected equirectangular, north up, scaled to a 680px-wide viewBox. Samples without a fix (Lat and Lon both 0) are skipped — projected, they would land in the Gulf of Guinea and shrink the track to a dot. Fewer than 20 fixes returns `ErrNoGPS`.
- **Points are thinned** to one per 2px of outline. A 60Hz lap is ~5,000 points, about ten per pixel; thinning takes the file from ~150 KB to ~15 KB with no visible change, which matters when an assistant has to read the file to show it.
- **Corners and chicanes are labelled; straights are not**, unless highlighted. Highlighted segments are drawn thicker in a contrasting colour, on top, with a bold label and a caption wrapped to 18-character lines.
- **A segment that wraps across S/F is drawn as two runs.** As one polyline the join would cut across the infield.
- **Label placement is a search, not a rule.** "Push the label outward, away from the centre" was tried first and fails on a real circuit: tracks are not convex, so the outside of one corner is often the inside of the next, and a long caption clamped at the drawing's edge got pushed straight back over the line. `labeler.place` tries five spots along the segment, either side, at five distances, and takes the first box with 12px clearance from every track point and every earlier label — otherwise the one with the most. Highlighted labels are placed first so they get the clearest spots; the header lines are registered as occupied so the rest of the top margin is usable.
- **Colours live in one `<style>` block with a `prefers-color-scheme: dark` variant**, since the file is opened in whatever viewer is at hand.

Label widths are estimated (7px per character), not measured, so an unusually wide font can still clip a label at the edge.

### Persistent storage (`trackmap.go`)

`trackmap.json` is a flat JSON map keyed by `TrackDisplayName`. Each entry (`TrackMap`) stores:
- `segments` — detected segment list
- `lapsUsed` / `sessionsUsed` — cumulative confidence counters
- `seenSessions` — RFC3339 session start dates (deduplication, capped at 50)
- `geoMethod` — `"latlon"` or `"lataccel"`

Each `Segment` stores geometric boundaries (`entryPct`/`exitPct`) and an optional `brakeEntryPct` — the average distance where drivers begin braking, blended with weighted averaging across sessions.

**Mis-encoded keys.** Before session YAML was decoded from Windows-1252, a non-ASCII track name was stored with U+FFFD in place of each accented byte (`Baden-W�rttemberg`) and never found again. `TrackMapFile.AdoptLegacyName(track)` moves such an entry to the correct name (an entry already under the correct name wins and the legacy one is dropped); `analyze` calls it before reading the store and saves when it changed anything. `trackref.json` is hand-edited and never written, so `Corners`/`CornerNames` instead fall back to the legacy key on a miss. See [internal/textenc](../textenc/README.md).

## Architecture

| Symbol | Description |
|---|---|
| `Segment` | One straight/corner/chicane: name, kind, entry/exit pct+metres, brakeEntryPct. |
| `TrackMap` | Full segment map for one track with confidence metadata. |
| `TrackMapFile` | Top-level JSON type: `map[string]*TrackMap`. |
| `GeometryConfidence` | `"low"` / `"moderate"` / `"high"` based on laps used. |
| `SegmentKind` | `"straight"`, `"corner"`, `"chicane"`. |
| `TrackRef` | Expected corner count for a known track. |
| `TrackRefFile` | Top-level JSON type for `trackref.json`: `map[string]*TrackRef`. |
| `ApplyCornerNames` | Renames corner/chicane segments in track order from a `cornerNames` list. All-or-nothing on length. |
| `CountCorners` | Number of corner + chicane segments — the count `cornerNames` must match. |
| `RenderSVG` | Writes one lap's outline as SVG, with `MapOptions.Highlights` painted and captioned. `ErrNoGPS` without position data. |

### Key functions

```go
tmf, err := trackmap.Load("trackmap.json")
tm := tmf["Donington Park"]

segs := trackmap.DetectFromMultipleLatLon(allLapSamples, trackLengthM)
// falls back to:
segs = trackmap.DetectFromMultiple(allLapSamples, trackLengthM)

score := trackmap.MatchScore(bestLapSamples, segs, trackLengthM)
conf := tm.EffectiveConfidence(score)

trackmap.Save("trackmap.json", tmf)
```
