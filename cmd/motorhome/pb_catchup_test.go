package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/pb"
)

// catchUpFixture is an ibtDir of placeholder .ibt files plus pb.json and
// ledger paths. The files' contents are never read â€” the injected check
// decides what each one "contains" â€” but their names, sizes and modification
// times are real, because the ledger keys on them.
type catchUpFixture struct {
	dir, pbPath, ledgerPath string
}

func newCatchUpFixture(t *testing.T, names ...string) catchUpFixture {
	t.Helper()
	root := t.TempDir()
	ibtDir := filepath.Join(root, "telemetry")
	if err := os.Mkdir(ibtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, n := range names {
		p := filepath.Join(ibtDir, n)
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
		// Ascending modification times in argument order.
		mt := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	return catchUpFixture{dir: ibtDir,
		pbPath:     filepath.Join(root, "pb.json"),
		ledgerPath: filepath.Join(root, pbScanFile)}
}

// fakeSessions maps a file's basename to the best lap it holds, and counts
// how often each was read.
type fakeSessions struct {
	best  map[string]*sessionBest
	errs  map[string]error
	reads map[string]int

	withMap    map[string]bool
	phaseReads map[string]int
}

func (fs *fakeSessions) check(path string, _ pb.File) (*sessionBest, error) {
	name := filepath.Base(path)
	if fs.reads == nil {
		fs.reads = map[string]int{}
	}
	fs.reads[name]++
	if err := fs.errs[name]; err != nil {
		return nil, err
	}
	return fs.best[name], nil
}

// phases is the fake pbPhasesFunc: a session listed in withMap yields one
// phase, standing in for "this track has a map now".
func (fs *fakeSessions) phases(path string, _ *pb.PersonalBest) ([]pb.PBPhase, error) {
	name := filepath.Base(path)
	if fs.phaseReads == nil {
		fs.phaseReads = map[string]int{}
	}
	fs.phaseReads[name]++
	if fs.withMap[name] {
		return []pb.PBPhase{{SegName: "FILLED"}}, nil
	}
	return nil, nil
}

func (fs *fakeSessions) readers() catchUpReaders {
	return catchUpReaders{best: fs.check, phases: fs.phases}
}

func lapOf(car, track string, secs float32, date string) *sessionBest {
	return &sessionBest{Car: car, Track: track, LapTime: secs,
		Formatted: fmt.Sprintf("%.3f", secs), Date: date,
		Phases: []pb.PBPhase{{SegName: "T1", Kind: "entry"}}, Setup: "CarSetup:\n"}
}

func seedPB(t *testing.T, path, car, track string, secs float32, formatted string) {
	t.Helper()
	pbf := pb.File{}
	pb.Update(pbf, car, track, secs, formatted, "2026-10-06", "")
	pb.SetPhases(pbf, car, track, []pb.PBPhase{{SegName: "OLD"}})
	if err := pb.Save(path, pbf); err != nil {
		t.Fatal(err)
	}
}

func TestRunCatchUp_RecordsFasterLapFromUnanalysedSession(t *testing.T) {
	// The motivating case: Monday's 1:39.954 sat unanalysed while Tuesday's
	// slower 1:40.040 was recorded as the PB.
	fx := newCatchUpFixture(t, "monday.ibt", "tuesday.ibt")
	seedPB(t, fx.pbPath, "MX-5", "Oschersleben", 100.040, "1:40.040")
	fs := &fakeSessions{best: map[string]*sessionBest{
		"monday.ibt":  lapOf("MX-5", "Oschersleben", 99.954, "2026-10-05"),
		"tuesday.ibt": lapOf("MX-5", "Oschersleben", 100.040, "2026-10-06"),
	}}
	var out bytes.Buffer

	finds := runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &out)

	if len(finds) != 1 || finds[0].Was != "1:40.040" || finds[0].Date != "2026-10-05" {
		t.Fatalf("finds = %+v", finds)
	}
	pbf, _ := pb.Load(fx.pbPath)
	got := pbf[pb.Key("MX-5", "Oschersleben")]
	if got.LapTime != 99.954 || got.Date != "2026-10-05" {
		t.Errorf("stored PB = %v on %s, want 99.954 on 2026-10-05", got.LapTime, got.Date)
	}
	// The new record must describe the new lap, not carry the old one's phases.
	if len(got.Phases) != 1 || got.Phases[0].SegName != "T1" || got.Setup == "" {
		t.Errorf("phases/setup not replaced: %+v / %q", got.Phases, got.Setup)
	}
	if !strings.Contains(out.String(), "New PB found in an earlier session") ||
		!strings.Contains(out.String(), "was 1:40.040") {
		t.Errorf("output did not report the find:\n%s", out.String())
	}
}

func TestRunCatchUp_SkipsSessionsAlreadyChecked(t *testing.T) {
	fx := newCatchUpFixture(t, "a.ibt", "b.ibt")
	fs := &fakeSessions{}

	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if fs.reads["a.ibt"] != 1 || fs.reads["b.ibt"] != 1 {
		t.Errorf("each session should be read once across two runs, got %v", fs.reads)
	}
}

func TestRunCatchUp_RechecksAChangedFile(t *testing.T) {
	// A session still being recorded when first checked keeps growing; the
	// finished file must be checked again or its later laps are never seen.
	fx := newCatchUpFixture(t, "live.ibt")
	fs := &fakeSessions{}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	p := filepath.Join(fx.dir, "live.ibt")
	if err := os.WriteFile(p, []byte("now with more laps"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if fs.reads["live.ibt"] != 2 {
		t.Errorf("changed file read %d times, want 2", fs.reads["live.ibt"])
	}
}

func TestRunCatchUp_LeavesTheAnalysedSessionToThePipeline(t *testing.T) {
	// Recording the target's PB here would make the vs-PB table compare the
	// lap to itself â€” the main pipeline captures the previous PB first.
	fx := newCatchUpFixture(t, "older.ibt", "target.ibt")
	fs := &fakeSessions{}

	runCatchUp(fx.dir, filepath.Join(fx.dir, "TARGET.IBT"), fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if fs.reads["target.ibt"] != 0 {
		t.Error("the session being analysed must not be read by the catch-up")
	}
	if fs.reads["older.ibt"] != 1 {
		t.Error("other sessions should still be read")
	}
}

func TestRunCatchUp_SlowerLapDoesNotReplacePB(t *testing.T) {
	fx := newCatchUpFixture(t, "slow.ibt")
	seedPB(t, fx.pbPath, "MX-5", "Oschersleben", 100.040, "1:40.040")
	fs := &fakeSessions{best: map[string]*sessionBest{
		"slow.ibt": lapOf("MX-5", "Oschersleben", 101.5, "2026-10-04"),
	}}

	if finds := runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{}); len(finds) != 0 {
		t.Errorf("finds = %+v, want none", finds)
	}
	pbf, _ := pb.Load(fx.pbPath)
	if got := pbf[pb.Key("MX-5", "Oschersleben")]; got.LapTime != 100.040 || got.Phases[0].SegName != "OLD" {
		t.Errorf("stored PB changed: %+v", got)
	}
}

func TestRunCatchUp_ReportsOnePerCarTrackWithOriginalPB(t *testing.T) {
	// Two successive improvements in one pass: the report should say what the
	// PB was before the catch-up, and the record should hold the faster lap.
	fx := newCatchUpFixture(t, "first.ibt", "second.ibt")
	seedPB(t, fx.pbPath, "MX-5", "Okayama", 110.0, "1:50.000")
	fs := &fakeSessions{best: map[string]*sessionBest{
		"first.ibt":  lapOf("MX-5", "Okayama", 109.5, "2026-09-01"),
		"second.ibt": lapOf("MX-5", "Okayama", 109.0, "2026-09-02"),
	}}

	finds := runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if len(finds) != 1 {
		t.Fatalf("want one find, got %+v", finds)
	}
	if finds[0].Was != "1:50.000" || finds[0].Date != "2026-09-02" || finds[0].File != "second.ibt" {
		t.Errorf("find = %+v", finds[0])
	}
}

func TestRunCatchUp_FirstPBForACombo(t *testing.T) {
	fx := newCatchUpFixture(t, "new.ibt")
	fs := &fakeSessions{best: map[string]*sessionBest{
		"new.ibt": lapOf("GR86", "Lime Rock", 60.0, "2026-08-01"),
	}}
	finds := runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	if len(finds) != 1 || finds[0].Was != "" {
		t.Fatalf("finds = %+v", finds)
	}
	if s := formatCatchUpFind(finds[0]); !strings.Contains(s, "no PB stored before") {
		t.Errorf("message = %q", s)
	}
}

func TestRunCatchUp_UnreadableSessionWarnsAndIsNotRetried(t *testing.T) {
	fx := newCatchUpFixture(t, "broken.ibt")
	fs := &fakeSessions{errs: map[string]error{"broken.ibt": errors.New("bad header")}}
	var out bytes.Buffer

	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &out)
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if !strings.Contains(out.String(), "broken.ibt") {
		t.Errorf("want a warning naming the file, got:\n%s", out.String())
	}
	if fs.reads["broken.ibt"] != 1 {
		t.Errorf("an unchanged unreadable file should not be retried, read %d times", fs.reads["broken.ibt"])
	}
}

func TestRunCatchUp_NeverOverwritesUnreadablePBFile(t *testing.T) {
	fx := newCatchUpFixture(t, "a.ibt")
	if err := os.WriteFile(fx.pbPath, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSessions{best: map[string]*sessionBest{"a.ibt": lapOf("MX-5", "X", 90, "2026-01-01")}}

	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	if b, _ := os.ReadFile(fx.pbPath); string(b) != "{corrupt" {
		t.Errorf("pb.json was rewritten: %q", b)
	}
	if fs.reads["a.ibt"] != 0 {
		t.Error("sessions should not be marked checked when pb.json could not be read")
	}
}

func TestRunCatchUp_ProgressLineOnlyForLargeBacklog(t *testing.T) {
	small := newCatchUpFixture(t, "1.ibt", "2.ibt")
	var out bytes.Buffer
	runCatchUp(small.dir, "", small.ledgerPath, small.pbPath, (&fakeSessions{}).readers(), &out)
	if strings.Contains(out.String(), "Checking") {
		t.Errorf("no progress line expected for 2 sessions:\n%s", out.String())
	}

	big := newCatchUpFixture(t, "1.ibt", "2.ibt", "3.ibt", "4.ibt")
	out.Reset()
	runCatchUp(big.dir, "", big.ledgerPath, big.pbPath, (&fakeSessions{}).readers(), &out)
	if !strings.Contains(out.String(), "Checking 4 sessions") {
		t.Errorf("want a progress line for 4 sessions, got:\n%s", out.String())
	}
}

func TestMarkPBScanned_SkipsTheFileNextRun(t *testing.T) {
	fx := newCatchUpFixture(t, "done.ibt")
	markPBScanned(fx.pbPath, filepath.Join(fx.dir, "done.ibt"))

	fs := &fakeSessions{}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	if fs.reads["done.ibt"] != 0 {
		t.Error("a session marked by the main pipeline should not be re-read")
	}
}

func TestSessionBestFromIbt_UnreadableFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junk.ibt")
	if err := os.WriteFile(p, []byte("not telemetry"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionBestFromIbt("", nil, nil)(p, pb.File{}); err == nil {
		t.Error("want an error for a non-.ibt file")
	}
}

func TestRunCatchUp_RecordsSourceFile(t *testing.T) {
	fx := newCatchUpFixture(t, "monday.ibt")
	fs := &fakeSessions{best: map[string]*sessionBest{
		"monday.ibt": lapOf("MX-5", "Oschersleben", 99.954, "2026-10-05"),
	}}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})

	pbf, _ := pb.Load(fx.pbPath)
	if got := pbf[pb.Key("MX-5", "Oschersleben")].SourceFile; got != "monday.ibt" {
		t.Errorf("SourceFile = %q, want monday.ibt", got)
	}
}

func TestRunCatchUp_PBWithoutMapSaysSo(t *testing.T) {
	// A PB caught up from a track never analysed has no map, so no corner
	// data — and the missing vs-PB table must not be a mystery.
	fx := newCatchUpFixture(t, "monza.ibt")
	lap := lapOf("MX-5", "Monza", 96.817, "2026-09-21")
	lap.Phases = nil
	fs := &fakeSessions{best: map[string]*sessionBest{"monza.ibt": lap}}
	var out bytes.Buffer

	finds := runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &out)

	if len(finds) != 1 || !finds[0].NoPhases {
		t.Fatalf("finds = %+v", finds)
	}
	if !strings.Contains(out.String(), "No track map for it yet") {
		t.Errorf("output did not explain the missing corner data:\n%s", out.String())
	}
	// Found in this pass: the backfill must not re-read it straight away.
	if fs.phaseReads["monza.ibt"] != 0 {
		t.Error("a PB found in this pass should not be backfilled in the same pass")
	}
}

func TestRunCatchUp_BackfillsPhasesOnceTrackHasMap(t *testing.T) {
	fx := newCatchUpFixture(t, "monza.ibt")
	pbf := pb.File{}
	pb.Update(pbf, "MX-5", "Monza", 96.817, "1:36.817", "2026-09-21", "")
	pb.SetSource(pbf, "MX-5", "Monza", "monza.ibt")
	if err := pb.Save(fx.pbPath, pbf); err != nil {
		t.Fatal(err)
	}
	// The session itself is already checked; only the backfill has work to do.
	markPBScanned(fx.pbPath, filepath.Join(fx.dir, "monza.ibt"))

	// No map yet: nothing filled, nothing saved.
	fs := &fakeSessions{}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	got, _ := pb.Load(fx.pbPath)
	if len(got[pb.Key("MX-5", "Monza")].Phases) != 0 {
		t.Fatal("phases filled with no map")
	}

	// The track has been analysed since, so it has a map.
	fs.withMap = map[string]bool{"monza.ibt": true}
	var out bytes.Buffer
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &out)

	got, _ = pb.Load(fx.pbPath)
	e := got[pb.Key("MX-5", "Monza")]
	if len(e.Phases) != 1 || e.Phases[0].SegName != "FILLED" {
		t.Errorf("phases = %+v, want backfilled", e.Phases)
	}
	if e.LapTime != 96.817 {
		t.Errorf("backfill changed the lap time to %v", e.LapTime)
	}
	if !strings.Contains(out.String(), "Corner data added to your MX-5 PB at Monza") {
		t.Errorf("output did not report the backfill:\n%s", out.String())
	}

	// Done: the next run does not read the session again.
	before := fs.phaseReads["monza.ibt"]
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	if fs.phaseReads["monza.ibt"] != before {
		t.Error("an entry that already has phases should not be re-read")
	}
}

func TestRunCatchUp_BackfillSkipsMissingSourceFile(t *testing.T) {
	fx := newCatchUpFixture(t)
	pbf := pb.File{}
	pb.Update(pbf, "MX-5", "Monza", 96.817, "1:36.817", "2026-09-21", "")
	pb.SetSource(pbf, "MX-5", "Monza", "deleted.ibt")
	if err := pb.Save(fx.pbPath, pbf); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSessions{withMap: map[string]bool{"deleted.ibt": true}}
	runCatchUp(fx.dir, "", fx.ledgerPath, fx.pbPath, fs.readers(), &bytes.Buffer{})
	if fs.phaseReads["deleted.ibt"] != 0 {
		t.Error("a deleted source session should not be read")
	}
}
