package trackmap

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

// ovalLap returns n samples round an ellipse near the Nürburgring, in track
// order from LapDistPct 0.
func ovalLap(n int) []Sample {
	out := make([]Sample, n)
	for i := range out {
		a := 2 * math.Pi * float64(i) / float64(n)
		out[i] = Sample{
			LapDistPct: float32(i) / float32(n),
			Lat:        50.33 + 0.004*math.Sin(a),
			Lon:        6.94 + 0.008*math.Cos(a),
		}
	}
	return out
}

func ovalSegs() []Segment {
	return []Segment{
		{Name: "S1", Kind: KindStraight, EntryPct: 0, ExitPct: 0.2},
		{Name: "T1", Kind: KindCorner, EntryPct: 0.2, ExitPct: 0.3},
		{Name: "S2", Kind: KindStraight, EntryPct: 0.3, ExitPct: 0.6},
		{Name: "T2", Kind: KindCorner, EntryPct: 0.6, ExitPct: 0.7},
		{Name: "S3", Kind: KindStraight, EntryPct: 0.7, ExitPct: 1},
	}
}

func render(t *testing.T, samples []Sample, segs []Segment, opt MapOptions) string {
	t.Helper()
	var b bytes.Buffer
	if err := RenderSVG(&b, samples, segs, opt); err != nil {
		t.Fatalf("RenderSVG: %v", err)
	}
	return b.String()
}

// assertWellFormed decodes the whole document, which is what a browser will do
// before it draws anything.
func assertWellFormed(t *testing.T, svg string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(svg))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("SVG is not well-formed XML: %v\n%s", err, svg)
		}
	}
}

func TestRenderSVG_WellFormedAndLabelsCornersOnly(t *testing.T) {
	svg := render(t, ovalLap(400), ovalSegs(), MapOptions{Title: "Test Ring", Subtitle: "Lap 3 — 1:27.912"})
	assertWellFormed(t, svg)

	for _, want := range []string{">T1</text>", ">T2</text>", ">Test Ring</text>", "Start/finish"} {
		if !strings.Contains(svg, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Straights are drawn but not labelled — a label on every straight would
	// crowd the corners.
	for _, unwanted := range []string{">S1</text>", ">S2</text>"} {
		if strings.Contains(svg, unwanted) {
			t.Errorf("straight label %q should not be drawn", unwanted)
		}
	}
	if strings.Contains(svg, `class="hl"`) {
		t.Error("no segment was highlighted, but a highlight polyline was drawn")
	}
}

func TestRenderSVG_HighlightWithCaption(t *testing.T) {
	svg := render(t, ovalLap(400), ovalSegs(), MapOptions{
		Highlights: []Highlight{{Segment: 3, Caption: "Brake & turn in later"}},
	})
	assertWellFormed(t, svg)

	if n := strings.Count(svg, `class="hl"`); n != 1 {
		t.Errorf("want 1 highlighted run, got %d", n)
	}
	if !strings.Contains(svg, `class="lbl-hl"`) || !strings.Contains(svg, ">T2</text>") {
		t.Error("highlighted segment label missing or not styled")
	}
	// Wrapped to two lines; the first must carry the escaped ampersand.
	if !strings.Contains(svg, ">Brake &amp; turn in</text>") || !strings.Contains(svg, ">later</text>") {
		t.Error("caption missing, unwrapped, or not XML-escaped")
	}
	// A highlighted corner is drawn once, on top — not also as a plain corner.
	if n := strings.Count(svg, `class="corner"`); n != 1 {
		t.Errorf("want 1 plain corner (T1), got %d", n)
	}
}

func TestRenderSVG_HighlightedStraightIsLabelled(t *testing.T) {
	svg := render(t, ovalLap(400), ovalSegs(), MapOptions{Highlights: []Highlight{{Segment: 2}}})
	if !strings.Contains(svg, ">S2</text>") {
		t.Error("a straight that was asked for should be labelled")
	}
}

func TestRenderSVG_OutOfRangeHighlightIgnored(t *testing.T) {
	svg := render(t, ovalLap(400), ovalSegs(), MapOptions{Highlights: []Highlight{{Segment: 99}}})
	if strings.Contains(svg, `class="hl"`) {
		t.Error("out-of-range highlight should draw nothing")
	}
}

func TestRenderSVG_NoGPS(t *testing.T) {
	samples := make([]Sample, 200)
	for i := range samples {
		samples[i].LapDistPct = float32(i) / 200
	}
	err := RenderSVG(io.Discard, samples, ovalSegs(), MapOptions{})
	if !errors.Is(err, ErrNoGPS) {
		t.Fatalf("want ErrNoGPS, got %v", err)
	}
}

func TestRenderSVG_SkipsSamplesWithoutFix(t *testing.T) {
	samples := ovalLap(400)
	for i := 0; i < 400; i += 7 {
		samples[i].Lat, samples[i].Lon = 0, 0
	}
	svg := render(t, samples, ovalSegs(), MapOptions{})
	// A dropped fix projected as (0,0) would land hundreds of km away and
	// shrink the whole outline to a dot; check the outline still spans the box.
	pts := projectSamples(samples)
	fitToBox(pts, 0)
	minX, maxX := pts[0].x, pts[0].x
	for _, p := range pts {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
	}
	if maxX-minX < 400 {
		t.Errorf("outline only %.0fpx wide — a missing fix was probably projected", maxX-minX)
	}
	assertWellFormed(t, svg)
}

func TestSegmentRuns_WrapAcrossStartFinish(t *testing.T) {
	pts := projectSamples(ovalLap(100))
	seg := Segment{Name: "T9", Kind: KindCorner, EntryPct: 0.9, ExitPct: 0.1}
	runs := segmentRuns(pts, seg)
	// Two runs, either side of the line: drawn as one polyline the join would
	// cut straight across the infield.
	if len(runs) != 2 {
		t.Fatalf("want 2 runs for a wrapping segment, got %d", len(runs))
	}
	for _, r := range runs {
		for _, p := range r {
			if p.pct > 0.1 && p.pct < 0.9 {
				t.Errorf("run contains pct %.2f outside the segment", p.pct)
			}
		}
	}
}

func TestFitToBox_StaysInsideViewBox(t *testing.T) {
	// A tall, narrow outline must be limited by height, not width.
	samples := ovalLap(300)
	for i := range samples {
		samples[i].Lon = 6.94 + (samples[i].Lon-6.94)*0.1
		samples[i].Lat = 50.33 + (samples[i].Lat-50.33)*5
	}
	pts := projectSamples(samples)
	h := fitToBox(pts, 0)
	for _, p := range pts {
		if p.x < 0 || p.x > svgWidth || p.y < 0 || p.y > h {
			t.Fatalf("point (%.0f,%.0f) outside 0..%.0f x 0..%.0f", p.x, p.y, svgWidth, h)
		}
	}
	if h > svgTopPad+svgMaxDrawH+svgBottomPad+1 {
		t.Errorf("height %.0f exceeds the cap", h)
	}
}

func TestThinPoints(t *testing.T) {
	pts := []mapPoint{{pct: 0, x: 0}, {pct: 0.1, x: 0.5}, {pct: 0.2, x: 1.5}, {pct: 0.3, x: 2.1}, {pct: 0.4, x: 5}}
	got := thinPoints(pts, 2)
	if len(got) != 3 || got[0].pct != 0 || got[1].pct != 0.3 || got[2].pct != 0.4 {
		t.Errorf("thinPoints = %+v", got)
	}
}

func TestRenderSVG_SizeIsBounded(t *testing.T) {
	// A full 60Hz lap: the output should be tens of kilobytes, not hundreds.
	svg := render(t, ovalLap(5400), ovalSegs(), MapOptions{})
	if len(svg) > 40_000 {
		t.Errorf("SVG is %d bytes — point thinning not applied?", len(svg))
	}
}

func TestLabeler_ClearOfTrackAndEachOther(t *testing.T) {
	pts := projectSamples(ovalLap(400))
	h := fitToBox(pts, 0)
	pts = thinPoints(pts, minPointSpacing)
	lb := &labeler{pts: pts, top: 0, bottom: h}
	var b strings.Builder
	segs := ovalSegs()
	lb.place(&b, segmentRuns(pts, segs[1]), "T1", "A caption long enough to need room", true)
	lb.place(&b, segmentRuns(pts, segs[3]), "T2", "", false)
	lb.place(&b, [][]mapPoint{pts[:12]}, "Start/finish", "", false)

	if len(lb.placed) != 3 {
		t.Fatalf("placed %d labels, want 3", len(lb.placed))
	}
	for i, r := range lb.placed {
		if r.x0 < 0 || r.x1 > svgWidth || r.y0 < 0 || r.y1 > h {
			t.Errorf("label %d box %+v outside the viewBox", i, r)
		}
		// placed boxes carry a few px of padding; the label itself must not
		// sit on the line.
		if c := clearance(pts, rect{r.x0 + 4, r.y0 + 2, r.x1 - 4, r.y1 - 2}); c < 6 {
			t.Errorf("label %d is %.1fpx from the track", i, c)
		}
		for j := i + 1; j < len(lb.placed); j++ {
			if r.overlaps(lb.placed[j]) {
				t.Errorf("labels %d and %d overlap", i, j)
			}
		}
	}
}

func TestWrapCaption(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"Turn in later", []string{"Turn in later"}},
		{"Turn in later - no waiting", []string{"Turn in later - no", "waiting"}},
		{"Supercalifragilistic word", []string{"Supercalifragilistic", "word"}},
	}
	for _, c := range cases {
		got := wrapCaption(c.in, 18)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("wrapCaption(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClearance(t *testing.T) {
	pts := []mapPoint{{x: 0, y: 0}, {x: 100, y: 100}}
	if c := clearance(pts, rect{10, 0, 20, 10}); c != 10 {
		t.Errorf("clearance = %.1f, want 10", c)
	}
	if c := clearance(pts, rect{-5, -5, 5, 5}); c != 0 {
		t.Errorf("point inside box should give 0, got %.1f", c)
	}
}
