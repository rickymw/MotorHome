---
name: session-review
description: Review an iRacing session from MotorHome telemetry as a race engineer — validate the data for accuracy first, then deliver the top 3 driving issues with impact and driver-executable fixes, up to 3 setup changes the car actually allows, and up to 3 questions about how corners felt. Use when the user asks to review, coach, analyse, or debrief a session, lap, or stint.
---

# Session review

A debrief, not a data dump. Four things, in order: **validate**, **diagnose**, **prescribe**, **ask**.

## 1. Get the data

```powershell
.\motorhome.exe coach
```

One self-contained brief: orientation, the coaching framework, and the analysis JSON. Do **not** separately read `coach.md` or run `analyze` for the same session — `coach` already contains both.

Pull these alongside it, because the validation step needs them:

```powershell
.\motorhome.exe analyze                    # human tables + the Map/Turns lines
.\motorhome.exe analyze -json              # per-segment entryPct/exitPct geometry (coach trims this)
.\motorhome.exe analyze -lap <N> -json     # any lap you need to cross-check
```

The setup lives in `pb.json` under the `"<Car>|<Track>"` key's `setup` field, as the raw `CarSetup:` YAML block. Read it — it is the authoritative list of what this car can adjust.

## 2. Validate before you coach

Telemetry lies in specific, repeatable ways. Run every check. Report what fails; say plainly which findings it weakens.

**Lap times reconcile with sectors.** Sum each lap's sector times and compare to its reported lap time. A clean lap agrees within ~20 ms (boundaries are interpolated). A lap off by seconds has a bad `LapLastLapTime` — its displayed time is wrong, and any "you were N seconds slower" built on it is wrong too. Confirm against the sample count: `sum(sampleCount over all phases) / 60` is the lap's true duration.

**Out/in lap times are not real lap times.** If an out lap looks faster than the best flying lap, the recording started mid-lap. Check fuel: a lap that burned half what a flying lap burned covered half the track. Never quote a partial lap's time.

**Brake-entry points can cascade.** `ComputePhases` moves each corner's start back to its stored brake-entry `pct` (`pb.json` → `brakeEntries`) and clamps the *previous* segment's exit to it. Compare every `brakeEntries[T].pct` against that corner's `entryPct` **and** the previous corner's span from `analyze -json`. Two symptoms of corruption:

- A brake entry that lands *inside the previous corner* — linked corners with one continuous brake application, where the detector caught the tail of the first corner's braking. The earlier corner's samples get charged to the later one.
- **A corner with no `exit` phase at all.** That means the next corner's brake entry swallowed it. Its exit data is in the next corner's rows.

Where this happens, name the pair as a complex ("the T6/T7 complex") rather than attributing to one corner. The *events* — lockups, wheelspin, coast — are real sample counts regardless of which segment they land in; only the attribution is soft.

**Sanity-check segment spans.** `sampleCount / 60` should roughly equal `(exitM - entryM) / meanSpeed`. A big overshoot means the span was extended by the check above. A phase whose `peakSpeedKph` is well above both its entry and exit speed is spanning more than one corner.

**Small-N phases are noise.** Under ~20 samples (0.33 s) the percentage columns are meaningless. Say so rather than reporting "100% on brake" from 9 samples.

**Map maturity gates everything.** `confidence: low` with `lapsUsed: 1` means the segmentation came from a single lap. Compare the detected corner count against iRacing's `Turns:` line. Recommend more clean laps then `analyze -update-map` before trusting corner-level attribution.

**Consistency needs laps.** Two laps is a difference, not a spread. Check whether the comparison lap had an off — a 100% `PkBrk` on a **straight** row is a brake stab, a spin, or traffic, and disqualifies that lap as a reference.

**Tyres need heat before they mean anything.** Judge camber or pressure only once surface temps are in the working range. A short run shows cold tyres, and cold tyres always read as "wrong camber". Say the data isn't there yet rather than prescribing from it.

**Cross-check the physical numbers.** Corner weights should sum to the car's known mass; `(LF+RR)/total` should match the reported `CrossWeight`; the front/rear split should match the car's known distribution; the loaded side should run hotter for the track's direction. When these line up, say so — it is evidence the pipeline is sound, and it earns trust for the findings that follow.

## 3. Diagnose — top 3 issues

Three, ranked by time cost. For each: **what the data shows**, **what it costs**, **what to do**.

Quantify from the totals, not from one row: sum coast seconds, lockup samples and wheelspin samples across the lap, and give them as seconds (`samples / 60`) and as a share of the lap. Prefer an internal anchor over an invented benchmark — "T5 shows you can do this; T6 is where you don't" beats a made-up target lap time. When you estimate lap time cost, give a range and say it is an estimate.

Note that `Lock` fires at 5% slip, which is near the optimal braking slip ratio — a high count alone does not prove over-braking. It becomes a finding when paired with 100% peak brake, ABS activity, and coast immediately afterwards.

## 4. Prescribe — fixes the driver can execute

**A fix is a pedal or a marker, not an outcome.** The driver cannot execute "slide less", "carry more speed", "be smoother", or "use the grip". They can execute:

- brake earlier / later, by a stated amount or marker
- brake with less / more peak pressure
- release the brake sooner / hold it longer into the corner
- get to throttle earlier / later, at a stated point
- take one gear higher / lower
- wait for a stated amount of unwind before going past a stated throttle percentage

Name the corner, the phase, and the pedal. One sentence each.

### Setup changes

Up to three. **Only fields that appear in this car's `CarSetup:` YAML** — that block is the ground truth for what iRacing exposes on this car, and it differs enormously between cars (an MX-5 Cup has no brake bias; a GT car has ARB blades and a wing). Never suggest an adjustment that is not in the block.

For each: the field, the current value, the direction, why the data points there, and the risk. Where a change trades one measured problem against another (helping rotation while the car is already spinning the rears), say so and make it conditional on the driver's answer.

## 5. Ask — up to 3 questions about feel

Ask only where the answer would change the prescription — usually to separate two hypotheses the telemetry cannot: understeer versus oversteer, over-slowing versus instability, a car problem versus a reference-point problem.

Ask about **feel in a named corner**, never about data the tool already has. Use `AskUserQuestion` with concrete options phrased the way a driver would describe it ("it wouldn't turn and I was waiting for the nose" / "it was loose and I was catching it").

## Going deeper

Once the aggregates have named the corner, get the samples:

```powershell
.\motorhome.exe coach -segment T3            # or T3,T4 — brief narrowed to those corners
.\motorhome.exe coach -segment T3 -hz 20     # if the trace is too large
.\motorhome.exe analyze -dump T8 -dump-all   # same corner, every comparable lap, as CSV
```

Read the trace for the moments the aggregates hide: where brake release meets steering input, the time offset where throttle first opens, a second brake application mid-corner, steering that goes back up after starting to unwind. A focused brief carries a `Focus:` line — do not characterise the lap as a whole from it.

Below 60 Hz, `ABS` and `Coast` are 1 if the event occurred anywhere in the window the row covers, not at that instant.

## Corner names

Detected labels are **positional** (`T1`, `T2`, …), not iRacing's official turn numbers, and detection merges complexes so the counts differ. Do not assert official names or numbers you are not sure of — describe corners by what the data shows (distance from S/F, entry speed, minimum speed, direction). If the user wants real names, add `cornerNames` to the track's `trackref.json` entry: one entry per **detected** corner, in track order, or nothing is renamed.
