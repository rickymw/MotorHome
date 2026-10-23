package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// mapTestLap is a flying lap round a GPS ellipse. Like exitCaseTraceLaps it
// is also used from TestMain's child process, so it takes no *testing.T.
func mapTestLap(number int) analysis.Lap {
	const n = 600
	samples := make([]analysis.SampleData, n)
	for i := range samples {
		a := 2 * math.Pi * float64(i) / n
		samples[i] = analysis.SampleData{
			LapDistPct: float32(i) / n,
			Lat:        50.33 + 0.004*math.Sin(a),
			Lon:        6.94 + 0.008*math.Cos(a),
			Speed:      40,
		}
	}
	return analysis.Lap{Number: number, Kind: analysis.KindFlying, LapTime: 87.912, Samples: samples}
}

func mapTestSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0, ExitPct: 0.2},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.2, ExitPct: 0.3},
		{Name: "S2", Kind: trackmap.KindStraight, EntryPct: 0.3, ExitPct: 0.6},
		{Name: "T2", Kind: trackmap.KindCorner, EntryPct: 0.6, ExitPct: 0.7},
		{Name: "S3", Kind: trackmap.KindStraight, EntryPct: 0.7, ExitPct: 1},
	}
}

func TestParseHighlights(t *testing.T) {
	segs := mapTestSegs()
	got, err := parseHighlights(segs, " T2 : Turn in later , 2,T2:second caption ignored")
	if err != nil {
		t.Fatal(err)
	}
	want := []trackmap.Highlight{{Segment: 3, Caption: "Turn in later"}, {Segment: 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("highlight %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseHighlights_LaterCaptionFillsBlank(t *testing.T) {
	got, err := parseHighlights(mapTestSegs(), "T1,T1:Brake earlier")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Caption != "Brake earlier" {
		t.Errorf("got %+v", got)
	}
}

func TestParseHighlights_Errors(t *testing.T) {
	if _, err := parseHighlights(mapTestSegs(), "T1,T9"); err == nil ||
		!strings.Contains(err.Error(), `"T9"`) || !strings.Contains(err.Error(), "available") {
		t.Errorf("unknown segment: want an error naming it and listing the available ones, got %v", err)
	}
	if _, err := parseHighlights(mapTestSegs(), " , "); err == nil {
		t.Error("an empty spec should be an error, not an empty highlight list")
	}
}

func TestTrackMapPath(t *testing.T) {
	got := trackMapPath(filepath.Join("C:", "tel", "mx5 session 22-02-45.ibt"))
	want := filepath.Join("C:", "tel", "mx5 session 22-02-45_map.svg")
	if got != want {
		t.Errorf("trackMapPath = %q, want %q", got, want)
	}
}

func TestMapLap(t *testing.T) {
	laps := []analysis.Lap{mapTestLap(2), mapTestLap(3)}
	if l := mapLap(laps, 3); l == nil || l.Number != 3 {
		t.Errorf("explicit lap: got %+v", l)
	}
	if l := mapLap(laps, 9); l != nil {
		t.Errorf("missing lap should be nil, got lap %d", l.Number)
	}
}

func TestWriteTrackMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s_map.svg")
	hl := []trackmap.Highlight{{Segment: 3, Caption: "Turn in later"}}
	lap := mapTestLap(12)
	if err := writeTrackMap(path, &lap, mapTestSegs(), hl, "Test Ring"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	svg := string(b)
	for _, want := range []string{">Test Ring</text>", "Lap 12 — 1:27.912", `class="hl"`, ">Turn in later</text>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("map missing %q", want)
		}
	}
}

func TestWriteTrackMap_NoGPSLeavesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s_map.svg")
	lap := mapTestLap(1)
	for i := range lap.Samples {
		lap.Samples[i].Lat, lap.Samples[i].Lon = 0, 0
	}
	if err := writeTrackMap(path, &lap, mapTestSegs(), nil, "Test Ring"); err == nil {
		t.Fatal("expected an error for a lap with no GPS")
	}
	// A half-written or empty SVG would open as a blank page and look like a
	// rendering bug rather than missing data.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("failed render left a file behind (stat err %v)", err)
	}
}

func TestHighlightRefusals(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"analyze-highlight-without-map", "analyze: -highlight has no effect without -map"},
		{"coach-highlight-without-map", "coach: -highlight has no effect without -map"},
		{"map-highlight-unknown", `segment "T9" not found`},
		{"map-highlight-no-track-map", "-highlight needs a track map"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stderr := runExitCase(t, c.name)
			if code == 0 {
				t.Errorf("want non-zero exit, got 0")
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr %q does not contain %q", stderr, c.want)
			}
		})
	}
}
