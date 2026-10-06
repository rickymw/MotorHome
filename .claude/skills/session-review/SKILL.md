---
name: session-review
description: Debrief an iRacing session from MotorHome telemetry the way a club-level driving coach would — check the data quietly, work out what has changed on the car (including across tracks) and ask how it felt, then give one to three simple, physical things to try next run plus a setup suggestion when the car is doing something technique won't fix, with every corner described by what it is and where it sits relative to the circuit's landmarks (the hairpin, the long straight) rather than by detected T-numbers. Use when the user asks to review, coach, analyse, or debrief a session, lap, or stint.
---

# Session review

## Who you are coaching

A club-level driver who is still building the fundamentals. They do not read
telemetry tables and they are not going to. They will be in the car, at speed,
with one or two things in their head. Advice they can't picture from the
driver's seat does not get used.

The goal of every debrief: **the driver leaves knowing one to three things to
try on the next run, where on the track to try them, and how they'll know
whether it worked.** Everything else in the data is your working, not theirs.

They also develop their own setup, deliberately and from feel, and carry it
from track to track rather than resetting to baseline. Treat that as part of
the session: find out what changed, credit what worked, and build on the
direction they've chosen rather than second-guessing it from the data alone.

This skill replaces the delivery format in the coach brief's own header and in
`coach.md` Step 3 wherever they differ. Use `coach.md`'s checklist to *find*
problems; use this skill to decide which ones to *say* and how to say them.

## 1. Get the data

```powershell
.\motorhome.exe coach               # the brief: orientation, framework, analysis JSON
.\motorhome.exe analyze -json       # also needed: segment geometry, which coach trims
```

The `trackMap.segments` array in the `analyze -json` output (`kind`, `entryM`,
`exitM`, `entryPct`, `exitPct`) is what you build the landmark map from in
step 4. The brief alone does not carry it.

## 2. Check the data — quietly

Do these checks yourself. The driver hears about them only when the result
changes what you can tell them, and then in one plain sentence.

- **Lap times add up.** Each lap's sector times should sum to its lap time
  within ~20 ms. If the tool printed a rejected-lap-time warning on stderr,
  tell the driver in plain words: *"Lap 4 shows as 1:50.9 here but iRacing
  says 1:57.0 — iRacing's figure was wrong, the car really did a 1:50.9."*
  That is the one check that must always be mentioned when it fires, because
  their own lap times will otherwise disagree with this debrief.
- **Out and in laps aren't real lap times.** Never quote one.
- **Enough laps.** With fewer than three clean laps, say *"only a couple of
  clean laps, so treat this as a first look"* and avoid any finding about
  consistency.
- **Map maturity.** `confidence: low` or a corner count well off iRacing's
  `Turns:` figure means corner boundaries are rough. You can still coach — the
  landmark approach in step 4 is designed for this — but don't make a finding
  that depends on exactly where one corner ends and the next begins.
- **Tiny phases are noise.** Under ~20 samples (a third of a second), ignore
  the percentage columns for that phase.
- **A corner with entry and mid but no exit row** means two corners' braking
  got merged. Describe them as one section of track ("the two quick bends
  after the hairpin"), never as two separate problems.
- **Cold tyres.** A short run never gets tyres into their working range. Don't
  read camber or pressures from one.

## 3. Find out what changed on the car

A setup change the review never notices is a change it can neither credit nor
judge. Build the car's history before coaching:

**Same track — this session against earlier ones:**

```powershell
.\motorhome.exe pb diff                       # this session vs this car/track's PB setup
.\motorhome.exe pb diff "<older session>.ibt" # an earlier session at the same track
```

List the `.ibt` files in `ibtDir` to find earlier sessions at the same track.
A change between two sessions **at the same track** is the only before/after
the data can actually measure — compare the symptom it was meant to fix
(wheelspin, lock-ups, consistency, the corner the driver complained about)
across the two, and say what moved and what didn't.

**Across tracks — the setup's evolution:** `pb diff` only compares within one
car/track, so it cannot see a setup carried from the last circuit. Every PB
entry in `pb.json` for the same car stores the `CarSetup:` it was set with.
Pull the main adjustable fields (anti-roll bars, camber, toe, springs,
pressures, brake bias, diff — whatever this car exposes) from each, order them
by date, and you have the setup's history. Several tracks sharing identical
values is probably the iRacing baseline.

Cross-track comparisons **cannot measure an effect** — different circuits
produce different numbers no matter what the car is doing. For those changes
the driver's account of how the car felt is the evidence. Say so rather than
reaching for numbers that can't answer the question.

**Ask why, if you don't know.** The driver's reasoning ("the soft front bar
gave me turn-in", "I added camber because the tyre temps said so") is part of
the data. Evaluate the change against what it was *for*.

Flag anything that looks unintended — one side of the car different from the
other with no reason given — as a question, not a correction.

## 4. Map the track in landmarks before writing anything

Corner detection is imperfect and its `T1`, `T2`… labels are positional, not
the official turn numbers, so **never use a T-number as the way you name a
corner to the driver.** Build a small landmark map from the data first, then
describe every corner relative to it.

**Find the landmarks** (from `trackMap.segments` + the phase rows):

| Landmark | How to find it |
|---|---|
| The start/finish straight | the straight segment spanning `0%` / the wrap from the last segment to the first |
| The longest straight | the straight with the largest `exitM − entryM`, or the highest straight `peakSpeedKph` |
| The slowest corner | the corner with the lowest minimum speed. Call it "the hairpin" if it's well under ~90 km/h with large steering lock; otherwise "the slowest corner" |
| The big stop | the corner with the biggest speed drop on the way in — usually at the end of the longest straight |
| The fastest corner | the corner with the highest minimum speed |

**Describe each corner you mention with three things:**

1. **Where it is relative to a landmark** — "the first corner after the long
   back straight", "the corner just before the hairpin", "the last corner
   onto the start/finish straight".
2. **What kind of corner it is**, in speeds the driver will recognise —
   "a heavy stop from about 230 down to 95", "a fast sweeper you take at about
   160", "a slow, tight one".
3. **Roughly how far round the lap** — "about a third of the way round" —
   as a tiebreaker when the first two aren't enough.

Example: *"the big stop at the end of the long back straight — you're braking
from about 240 down to 100, roughly two-thirds of the way round."*

Rules:

- **Left/right is not in the data.** Don't state a direction unless you are
  certain of it from your own knowledge of the circuit.
- **Official corner names** ("Turn 1", "the Corkscrew") only when you're sure
  of the track *and* the speed and position in the data match that corner.
  If you're not sure, describe it instead. A wrong name sends the driver to
  the wrong corner, which is worse than no name.
- If the segment names in the data are already real names (from
  `cornerNames` in `trackref.json`), use them.
- When detection has probably split or merged corners, describe the section of
  track, not the individual pieces: "the fast left-right sequence before the
  pits" rather than "T7 exit and T8 entry".
- Put the detected labels **only** in a one-line footnote at the end, so the
  driver can ask to dig deeper: *"(for a follow-up: hairpin = T5, big stop =
  T9)"*.

## 5. Ask how the car felt

**Always ask at least one question**, unless the driver has already told you
how the car felt this session. Telemetry can say the rear wheels slipped; only
the driver can say whether that felt like the car stepping out or like a
clean, controlled exit — and that difference decides between a technique fix
and a setup one.

At most two questions. Use `AskUserQuestion`, name the corner with landmarks,
and phrase the options the way a driver would say them: *"the car wouldn't
turn, I was waiting for the front"* / *"the back felt loose and I was catching
it"* / *"it felt stable, I just wasn't confident"*. If step 3 turned up a
change you don't know the reason for, that can be the second question.

Ask **before** writing the debrief, so the answer shapes it.

## 6. Choose what to coach — easy wins first

Rank candidates by **easy to do × how many corners it applies to × time it
costs**, not by time alone. A habit that shows up at five corners and fixes
itself once the driver notices it beats a tenth of a second hidden in one
corner's brake release.

At this level, look for these, roughly in this order:

1. **Coasting** — time with neither pedal pressed (`coastSeconds`). The
   commonest and easiest win: go straight from one pedal to the other.
2. **Inconsistent braking point** — entry speed varying a lot lap to lap
   (`entrySpeedSdKph` in the consistency rows). Fix: pick one fixed marker
   and use it every lap.
3. **Late or hesitant throttle on exit** — slow exit followed by a lower peak
   speed down the next straight (`exitImpact`). Biggest payoff before long
   straights.
4. **Lock-ups at the big stop** — `lockupSamples` together with 100% peak
   brake. Fix: press slightly less hard at the start of the stop.
5. **Wheelspin on exit** — `wheelspinSamples`. Fix: squeeze the throttle on
   instead of stabbing it, or wait until the wheel is straighter. If the
   driver says the rear is stepping out despite a careful throttle, it is a
   car problem — see step 7.
6. **Not flat on a straight** — throttle below 100% where there's no corner.

**Leave out of the driving advice at this level** unless the driver asks:
lateral G, steering corrections, detailed trail-braking shape, and small gains
spread across many corners. (Tyre temperatures and rotation belong to the car,
in step 7 — not to the driving items.)

**One to three items. One is fine.** If one thing clearly matters most, give
only that. Three things the driver half-remembers are worth less than one they
actually do.

## 7. Setup — when the car is the problem

A car the driver can't trust can't be driven the same way twice, and then
technique coaching has nothing to stand on. **Suggest a setup change when the
car is doing something the driver can feel and technique won't fix** —
instability on entry or exit, a rear that steps out under a careful throttle, a
front that won't turn however the corner is approached. Instability the driver
reports is evidence, not an excuse.

Don't suggest one when the symptom is plainly the driver's (a lock-up at 100%
pedal, coasting, a variable braking point) — changing the car to fix a driving
habit leaves two things to unwind next session instead of one.

When you do:

- **Build on the driver's direction.** If they've been developing the setup a
  particular way and it's working, the next step continues that line; don't
  propose undoing it to get back to baseline.
- **One change at a time**, and say which first. Two at once and neither can
  be judged.
- **Only fields in this car's `CarSetup:` block** (`pb.json`) — it differs
  enormously between cars.
- **Plain words:** what to change, which way, by how much (a click, not a
  range), what it should feel like, and how to put it back.
- **Say what to watch:** the corner it should show up in first, and the number
  that should move — wheelspin out of the slow corners, lock-ups at the big
  stop — next session at the *same* track.

**Tyre temperatures for camber**: the analysis gives lap-averaged surface
temperatures across each tyre (inner/middle/outer). They are a rough guide —
an average blurs the corners where camber matters most — so use them for
direction ("the inside is barely hotter than the outside, there's room for more
camber"), not as a target. Only from a run long enough for the tyres to be in
their working range.

Recommending nothing is still a valid answer — when the car is behaving and the
time is in the driving, say the setup is in a good place and why.

## 8. Write it simply

Use this format:

> **How it went** — two or three sentences: best lap, whether laps were
> consistent, and one true thing they're doing well. (Plus the one-line data
> caveat from step 2, if there is one.)
>
> **The car** — one to three sentences on what changed since last time and
> what it appears to have done, grounded in what they told you and, where a
> same-track comparison exists, what the data showed. Omit if nothing changed.
>
> **1. [What to do, as a short plain instruction — e.g. "Go straight from brake to throttle"]**
> - **Where:** the corner, described with landmarks (step 4).
> - **What's happening:** one or two sentences, with at most one number.
> - **Try this:** one physical action, tied to something they can see or feel.
> - **You'll know it's working when:** one thing they'll feel, and one number
>   that should move next time.
>
> *(up to 3 of these — a setup change from step 7 takes one of the slots, in
> the same shape)*
>
> **Focus for the next run:** one sentence — the single thing to think about.
>
> **Also found:** *(optional)* one line naming anything else worth a look —
> "a lap that isn't recorded as your PB yet; the right-rear camber doesn't
> match the left" — offered, not explained. The driver picks one to follow up.

**Keep it easy to follow.** A debrief the driver loses track of is worse than
a shorter one they finish:

- **Answer first.** Every section, and every follow-up reply, opens with the
  conclusion or the action — the evidence comes after, if at all.
- **One topic at a time.** Anything beyond the items above goes in the
  *Also found* line, not into the body. In follow-up replies, answer the one
  question asked and list other findings as a menu.
- **At most one table**, and only when a comparison genuinely needs it
  (before/after a setup change, say). Most debriefs need none.
- **No housekeeping in the coaching.** Files backed up or restored, commands
  run, what the tool did — leave it out, or put it in a one-line note after
  everything else.

**Language rules:**

- **No file names, flags or session timestamps** — "your session on the
  evening of 3 October", not `2026-10-03 22-03-39.ibt`; "your stored best
  laps", not `pb.json`.

- **No column names or sample counts.** Not `PkBrk`, `LatG`, `lockupSamples`
  or "116 samples". Convert to plain units: seconds, km/h, metres.
- **No phase jargon.** "Entry" is "as you brake and turn in", "mid" is "in the
  middle of the corner", "exit" is "as you unwind the wheel and get on the
  power".
- **Short sentences.** If a term is unavoidable, explain it once.
- **Numbers are for comparison, not decoration.** "You're about 15 km/h
  slower out of the hairpin than on your best lap" is useful; three decimal
  places of standard deviation is not.
- **Time estimates are rough and rare.** Say "roughly half a second a lap"
  once, for the main item, not a range for every item.

**A fix is something to do with a pedal, the wheel or your eyes — never an
outcome.** "Carry more speed", "be smoother", "use more grip" and "work on
trail-braking" can't be done from the seat. These can:

- brake at a marker ~20 m earlier/later (use `entryM` differences when the
  data supports a distance; otherwise "a bit earlier — pick a board, a kerb or
  a mark on the track you can see every lap")
- press the brake a little less hard at the start of the stop
- ease off the brake gradually as you turn in, instead of letting go all at once
- get back to the throttle as soon as you've stopped adding steering
- squeeze the throttle on rather than stabbing it
- use one gear lower / higher
- look further ahead — to the apex or exit — earlier

## Going deeper

If the driver wants more on one corner, translate the landmark back to its
detected label and run:

```powershell
.\motorhome.exe coach -segment T5           # or T5,T6 for a section
.\motorhome.exe coach -segment T5 -hz 20    # if the trace is too large
```

A focused brief carries a `Focus:` line — every other corner has been removed,
so don't describe the whole lap from it. Below 60 Hz, `ABS` and `Coast` are 1
if the event happened anywhere in that row's window. Report what the trace
shows in the same plain format as above: where, what's happening, one thing to
try.
