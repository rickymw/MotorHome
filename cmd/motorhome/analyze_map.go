package main

// -map and -highlight: write an SVG of the analysed lap's outline, with chosen
// segments painted on it, next to the .ibt.
//
// The debrief describes corners by landmark because the T-labels are
// positional; a picture of the lap with the corner highlighted makes the
// description unnecessary. It is a file rather than terminal output for the
// same reason -dump is: an SVG is something to open, not to read.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// parseHighlights resolves a -highlight spec: comma-separated segment names or
// 1-based indices, each optionally followed by ":caption" ("T6:Turn in later").
//
// Like ResolveSegmentList, an unknown name fails the whole spec rather than
// being dropped — a map missing the corner the debrief is about would send the
// driver looking at the wrong part of the track. A repeated segment keeps its
// first non-empty caption.
func parseHighlights(segs []trackmap.Segment, spec string) ([]trackmap.Highlight, error) {
	var out []trackmap.Highlight
	pos := map[int]int{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, caption, _ := strings.Cut(part, ":")
		name, caption = strings.TrimSpace(name), strings.TrimSpace(caption)
		idx := analysis.ResolveSegmentName(segs, name)
		if idx < 0 {
			return nil, fmt.Errorf("segment %q not found — available: %s", name, segmentNames(segs))
		}
		if j, seen := pos[idx]; seen {
			if out[j].Caption == "" {
				out[j].Caption = caption
			}
			continue
		}
		pos[idx] = len(out)
		out = append(out, trackmap.Highlight{Segment: idx, Caption: caption})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-highlight names no segments")
	}
	return out, nil
}

// trackMapPath is where -map writes: beside the .ibt, named after it, so maps
// from different sessions never overwrite each other.
func trackMapPath(ibtPath string) string {
	stem := strings.TrimSuffix(filepath.Base(ibtPath), filepath.Ext(ibtPath))
	return filepath.Join(filepath.Dir(ibtPath), stem+"_map.svg")
}

// mapLap picks the lap -map draws: the one being analysed.
func mapLap(laps []analysis.Lap, lapNum int) *analysis.Lap {
	if lapNum > 0 {
		return findAnalyzeLap(laps, lapNum)
	}
	return bestAnalyzeLap(laps)
}

// writeTrackMap renders lap's outline to path.
func writeTrackMap(path string, lap *analysis.Lap, segs []trackmap.Segment,
	highlights []trackmap.Highlight, track string) error {

	samples := make([]trackmap.Sample, len(lap.Samples))
	for i, s := range lap.Samples {
		samples[i] = trackmap.Sample{LapDistPct: s.LapDistPct, Lat: s.Lat, Lon: s.Lon}
	}
	opt := trackmap.MapOptions{
		Title:      track,
		Subtitle:   fmt.Sprintf("Lap %d — %s", lap.Number, analysis.FormatLapTime(lap.LapTime)),
		Highlights: highlights,
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := trackmap.RenderSVG(f, samples, segs, opt); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// runTrackMap is the -map step of RunAnalyze. The path goes to stderr, not the
// table sink, so it survives -json — which is how `coach -map` reports it.
func runTrackMap(ibtPath string, laps []analysis.Lap, lapNum int, segs []trackmap.Segment,
	highlightSpec, track string) {

	var highlights []trackmap.Highlight
	if highlightSpec != "" {
		if len(segs) == 0 {
			analyzeDie("-highlight needs a track map to name segments against (run analyze once first)")
		}
		var err error
		if highlights, err = parseHighlights(segs, highlightSpec); err != nil {
			analyzeDie("%v", err)
		}
	}
	lap := mapLap(laps, lapNum)
	if lap == nil {
		analyzeDie("-map: no lap to draw")
	}
	path := trackMapPath(ibtPath)
	if err := writeTrackMap(path, lap, segs, highlights, track); err != nil {
		analyzeDie("writing track map: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Track map → %s\n", path)
}
