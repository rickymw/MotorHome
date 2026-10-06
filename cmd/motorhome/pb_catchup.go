package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/ibt"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/textenc"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// pbScanFile is the ledger of sessions already checked for personal bests. It
// lives next to pb.json.
const pbScanFile = "pbscan.json"

// catchUpProgressThreshold is how many unchecked sessions it takes before the
// catch-up says what it is doing. Below it the check is quick enough not to
// look like a hang; the first run over a whole telemetry folder is not.
const catchUpProgressThreshold = 3

// sessionBest is the fastest valid lap in one session, plus everything a PB
// record stores alongside the time.
type sessionBest struct {
	Car, Track string
	LapTime    float32
	Formatted  string
	Date       string // "YYYY-MM-DD", local
	Weather    string
	Setup      string       // raw CarSetup: block
	Phases     []pb.PBPhase // nil when there is no track map to compute them against
}

// sessionBestFunc reads one session. pbf is the current store, so an
// implementation can skip the expensive parts for a lap that cannot be a PB.
// A nil result with a nil error means the session has no lap worth recording.
type sessionBestFunc func(path string, pbf pb.File) (*sessionBest, error)

// pbPhasesFunc recomputes the phases of a stored PB lap from the session it
// came from. Nil phases with a nil error means they still cannot be computed
// (most often: the track has no map yet).
type pbPhasesFunc func(path string, entry *pb.PersonalBest) ([]pb.PBPhase, error)

// catchUpReaders is everything runCatchUp reads telemetry through, injected so
// the bookkeeping can be tested without real .ibt files.
type catchUpReaders struct {
	best   sessionBestFunc
	phases pbPhasesFunc
}

// catchUpFind reports one car/track whose PB was replaced by the catch-up.
type catchUpFind struct {
	Car, Track string
	Was        string // previous PB, "" if there was none
	Now        string
	Date       string
	File       string
	NoPhases   bool // stored without corner data: no track map yet
}

// catchUpPBs checks every session in ibtDir that has not been checked before,
// and stores any lap that beats the PB for its car/track.
//
// analyze only records a PB from the one session it is run on, so a session
// nobody analysed was invisible: a lap faster than the stored PB could sit in
// the telemetry folder indefinitely. Running this ahead of every analyze (and
// therefore every coach) means a skipped session is picked up by the next one.
//
// skipPath is the session about to be analysed. It is left to the main
// pipeline, which records its PB itself — and must, because the vs-PB table
// captures the previous PB's phases before replacing them; recording the lap
// here first would leave that table comparing the lap to itself.
func catchUpPBs(ibtDir, skipPath, trackmapPath, pbPath, driver string) {
	if ibtDir == "" || pbPath == "" {
		return
	}
	var tmf trackmap.TrackMapFile
	var trf trackmap.TrackRefFile
	if trackmapPath != "" {
		// Read-only, and failures are already reported by the main pipeline;
		// without a map the catch-up still records the time, just no phases.
		tmf, _ = trackmap.Load(trackmapPath)
		trf, _ = trackmap.LoadTrackRef(filepath.Join(filepath.Dir(trackmapPath), "trackref.json"))
	}
	ledgerPath := filepath.Join(filepath.Dir(pbPath), pbScanFile)
	runCatchUp(ibtDir, skipPath, ledgerPath, pbPath, catchUpReaders{
		best:   sessionBestFromIbt(driver, tmf, trf),
		phases: pbPhasesFromIbt(driver, tmf, trf),
	}, os.Stderr)
}

// runCatchUp is catchUpPBs with the telemetry readers and output injected.
//
// It does two things. It checks every unchecked session for a lap faster than
// the stored PB; and it fills in the corner data of any PB stored without it,
// once that track has a map. The second exists because of the first: a PB
// caught up from a track that was never analysed has no map to compute phases
// against, and without a repair it would have no vs-PB table until it was
// beaten — possibly never.
func runCatchUp(ibtDir, skipPath, ledgerPath, pbPath string, r catchUpReaders, w io.Writer) []catchUpFind {
	ledger, err := pb.LoadScanned(ledgerPath)
	if err != nil {
		// A lost ledger costs one slow re-check, which is safe; it is rewritten below.
		fmt.Fprintf(w, "Warning: could not read %s (%v) — re-checking every session for personal bests\n", pbScanFile, err)
		ledger = pb.Scanned{}
	}

	type pending struct {
		path    string
		name    string
		size    int64
		modTime time.Time
	}
	entries, err := os.ReadDir(ibtDir)
	if err != nil {
		return nil // the main pipeline reports an unreadable ibtDir
	}
	var todo []pending
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ibt") {
			continue
		}
		path := filepath.Join(ibtDir, e.Name())
		if samePath(path, skipPath) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if ledger.Seen(e.Name(), info.Size(), info.ModTime()) {
			continue
		}
		todo = append(todo, pending{path, e.Name(), info.Size(), info.ModTime()})
	}
	// Oldest first, so when several sessions improve on the same PB the record
	// ends up dated by the session that actually set it.
	sort.Slice(todo, func(i, j int) bool { return todo[i].modTime.Before(todo[j].modTime) })

	pbf, err := pb.Load(pbPath)
	if err != nil {
		// Never write over a pb.json that could not be read — it would replace
		// every stored PB with whatever this pass happened to find.
		if len(todo) > 0 {
			fmt.Fprintf(w, "Warning: could not load pb.json (%v) — skipping the check of older sessions for personal bests\n", err)
		}
		return nil
	}

	if len(todo) > catchUpProgressThreshold {
		fmt.Fprintf(w, "Checking %d sessions not looked at before for personal bests (once only)...\n", len(todo))
	}

	finds := map[string]*catchUpFind{}
	var order []string
	for _, p := range todo {
		best, err := r.best(p.path, pbf)
		// Marked whatever the outcome: a file that cannot be read now will not
		// be readable next run either, unless it changes — and a changed file
		// has a new stamp, so it is checked again then.
		ledger.Mark(p.name, p.size, p.modTime)
		if err != nil {
			fmt.Fprintf(w, "Warning: could not check %s for a personal best: %v\n", p.name, err)
			continue
		}
		if best == nil || best.Car == "" || best.Track == "" {
			continue
		}
		pb.AdoptLegacyKey(pbf, best.Car, best.Track)
		key := pb.Key(best.Car, best.Track)
		was := ""
		if prev := pbf[key]; prev != nil && prev.LapTime > 0 {
			was = prev.LapTimeFormatted
		}
		if !pb.Update(pbf, best.Car, best.Track, best.LapTime, best.Formatted, best.Date, best.Weather) {
			continue
		}
		if best.Phases != nil {
			pb.SetPhases(pbf, best.Car, best.Track, best.Phases)
		}
		if best.Setup != "" {
			pb.SetSetup(pbf, best.Car, best.Track, best.Setup)
		}
		pb.SetSource(pbf, best.Car, best.Track, p.name)
		if f, ok := finds[key]; ok {
			// A second improvement in the same pass: keep the original "was",
			// so the report says what the stored PB was before the catch-up.
			f.Now, f.Date, f.File, f.NoPhases = best.Formatted, best.Date, p.name, best.Phases == nil
			continue
		}
		finds[key] = &catchUpFind{Car: best.Car, Track: best.Track, Was: was,
			Now: best.Formatted, Date: best.Date, File: p.name, NoPhases: best.Phases == nil}
		order = append(order, key)
	}

	filled := backfillPBPhases(ibtDir, pbf, finds, r.phases)

	if len(finds) > 0 || len(filled) > 0 {
		if err := pb.Save(pbPath, pbf); err != nil {
			fmt.Fprintf(w, "Warning: could not save pb.json: %v\n", err)
			return nil // nothing was stored, so nothing to report; ledger left as-is to retry
		}
	}
	if len(todo) > 0 {
		if err := pb.SaveScanned(ledgerPath, ledger); err != nil {
			fmt.Fprintf(w, "Warning: could not save %s: %v\n", pbScanFile, err)
		}
	}

	out := make([]catchUpFind, 0, len(order))
	for _, k := range order {
		f := finds[k]
		out = append(out, *f)
		fmt.Fprintln(w, formatCatchUpFind(*f))
	}
	for _, k := range filled {
		e := pbf[k]
		fmt.Fprintf(w, "Corner data added to your %s PB at %s (%s), now that the track has a map.\n",
			e.Car, e.Track, e.LapTimeFormatted)
	}
	return out
}

// backfillPBPhases fills in phases for PB entries stored without them, from
// the session each one came from, and returns the keys it filled in sorted.
// Entries caught up in this same pass are skipped — they were computed a
// moment ago against the same maps. An entry whose source session is gone, or
// still has no map, is left for a later run; that costs a stat per run, and a
// re-read only for a track that still has no map.
func backfillPBPhases(ibtDir string, pbf pb.File, justFound map[string]*catchUpFind, phases pbPhasesFunc) []string {
	var filled []string
	for key, e := range pbf {
		if e == nil || len(e.Phases) > 0 || e.SourceFile == "" || e.LapTime <= 0 {
			continue
		}
		if _, ok := justFound[key]; ok {
			continue
		}
		path := filepath.Join(ibtDir, e.SourceFile)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ph, err := phases(path, e)
		if err != nil || len(ph) == 0 {
			continue
		}
		e.Phases = ph
		filled = append(filled, key)
	}
	sort.Strings(filled)
	return filled
}

// formatCatchUpFind is the one line a catch-up PB prints. It names the
// session's date because the whole point is that it was not the session the
// user is looking at.
func formatCatchUpFind(f catchUpFind) string {
	was := "no PB stored before"
	if f.Was != "" {
		was = "was " + f.Was
	}
	s := fmt.Sprintf("New PB found in an earlier session: %s — %s at %s, %s (%s). Saved.",
		f.Now, f.Car, f.Track, f.Date, was)
	if f.NoPhases {
		// Without this the missing vs-PB table would be a mystery.
		s += "\n  No track map for it yet, so its corner data is filled in once you have analysed a session there."
	}
	return s
}

// markPBScanned records that the session at path has had its PB checked, so
// the catch-up does not read it again. Called by the main pipeline for the
// session it analysed.
func markPBScanned(pbPath, path string) {
	if pbPath == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	ledgerPath := filepath.Join(filepath.Dir(pbPath), pbScanFile)
	ledger, err := pb.LoadScanned(ledgerPath)
	if err != nil {
		ledger = pb.Scanned{}
	}
	ledger.Mark(filepath.Base(path), info.Size(), info.ModTime())
	if err := pb.SaveScanned(ledgerPath, ledger); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save %s: %v\n", pbScanFile, err)
	}
}

// samePath compares two paths the way Windows does: case-insensitively, after
// making both absolute.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return strings.EqualFold(aa, bb)
}

// sessionBestFromIbt is the real sessionBestFunc: the same best-lap selection
// as analyze (bestAnalyzeLap — flying, uncut, plausible), so the catch-up can
// never record a lap the full analysis would have rejected.
func sessionBestFromIbt(driver string, tmf trackmap.TrackMapFile, trf trackmap.TrackRefFile) sessionBestFunc {
	return func(path string, pbf pb.File) (*sessionBest, error) {
		f, err := ibt.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		meta := analysis.ParseSessionMeta(f.SessionInfo(), driver)
		if meta.CarScreenName == "" || meta.TrackDisplayName == "" {
			return nil, nil
		}
		laps, err := analysis.ExtractLaps(f)
		if err != nil {
			return nil, err
		}
		best := bestAnalyzeLap(laps)
		if best == nil {
			return nil, nil
		}
		res := &sessionBest{
			Car:       meta.CarScreenName,
			Track:     meta.TrackDisplayName,
			LapTime:   best.LapTime,
			Formatted: analysis.FormatLapTime(best.LapTime),
			Date:      f.DiskHeader().SessionStartDate.Local().Format("2006-01-02"),
			Weather:   analysis.ParseWeather(f.SessionInfo()),
		}
		// The rest is only worth computing for a lap that will be stored.
		prev := pbf[pb.Key(res.Car, res.Track)]
		if prev != nil && prev.LapTime > 0 && prev.LapTime <= res.LapTime {
			return res, nil
		}
		res.Setup = analysis.ExtractCarSetupBlock(f.SessionInfo())
		res.Phases = catchUpPhases(best, res.Track, tmf, trf, prev)
		return res, nil
	}
}

// pbPhasesFromIbt is the real pbPhasesFunc. It finds the stored lap in its
// session by lap time — the stored time came from that file, so it matches to
// the float32 bit — rather than re-running best-lap selection, which could
// legitimately pick differently if the selection rules have changed since.
func pbPhasesFromIbt(driver string, tmf trackmap.TrackMapFile, trf trackmap.TrackRefFile) pbPhasesFunc {
	return func(path string, entry *pb.PersonalBest) ([]pb.PBPhase, error) {
		if mapFor(tmf, entry.Track) == nil {
			return nil, nil // nothing to compute against; skip the read
		}
		f, err := ibt.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		laps, err := analysis.ExtractLaps(f)
		if err != nil {
			return nil, err
		}
		for i := range laps {
			if math.Abs(float64(laps[i].LapTime-entry.LapTime)) < 0.0005 {
				return catchUpPhases(&laps[i], entry.Track, tmf, trf, entry), nil
			}
		}
		return nil, nil
	}
}

// mapFor returns the stored map for track, falling back to the key it was
// saved under before session YAML was decoded from Windows-1252. The catch-up
// reads the map but never saves it, so it cannot migrate the key the way
// analyze does — it can only look in both places.
func mapFor(tmf trackmap.TrackMapFile, track string) *trackmap.TrackMap {
	if tm := tmf[track]; tm != nil && len(tm.Segments) > 0 {
		return tm
	}
	if tm := tmf[textenc.LegacyJSONName(track)]; tm != nil && len(tm.Segments) > 0 {
		return tm
	}
	return nil
}

// catchUpPhases computes the phases stored with a caught-up PB, against the
// same segments the main pipeline would use: the stored track map with
// trackref.json's corner names applied. Names matter — vs-PB rows are matched
// by segment name, so phases stored under the generated labels would never
// line up with a renamed map. No map means no phases; the catch-up does not
// detect maps, which is the main pipeline's job.
func catchUpPhases(lap *analysis.Lap, track string, tmf trackmap.TrackMapFile, trf trackmap.TrackRefFile, prev *pb.PersonalBest) []pb.PBPhase {
	tm := mapFor(tmf, track)
	if tm == nil {
		return nil
	}
	// Copy: ApplyCornerNames writes in place, and tmf is shared across sessions.
	segs := append([]trackmap.Segment(nil), tm.Segments...)
	if names := trf.CornerNames(track); len(names) > 0 {
		trackmap.ApplyCornerNames(segs, names)
	}
	var brakeEntries pb.BrakeEntryMap
	if prev != nil {
		brakeEntries = prev.BrakeEntries
	}
	return phasesToPB(analysis.ComputePhases(lap, segs, brakeEntries))
}
