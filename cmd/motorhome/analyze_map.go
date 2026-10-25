package main

// -map and -highlight: write an SVG of the analysed lap's outline, with chosen
// segments painted on it, next to the .ibt.
//
// The debrief describes corners by landmark because the T-labels are
// positional; a picture of the lap with the corner highlighted makes the
// description unnecessary. It is a file rather than terminal output for the
// same reason -dump is: an SVG is something to open, not to read.

import (
	"encoding/xml"
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

// sessionMapOptions is the header for a session's own map: the lap it shows.
func sessionMapOptions(lap *analysis.Lap, track string, highlights []trackmap.Highlight) trackmap.MapOptions {
	return trackmap.MapOptions{
		Title:      track,
		Subtitle:   fmt.Sprintf("Lap %d — %s", lap.Number, analysis.FormatLapTime(lap.LapTime)),
		Highlights: highlights,
	}
}

// writeTrackMap renders lap's outline to path.
func writeTrackMap(path string, lap *analysis.Lap, segs []trackmap.Segment, opt trackmap.MapOptions) error {
	samples := make([]trackmap.Sample, len(lap.Samples))
	for i, s := range lap.Samples {
		samples[i] = trackmap.Sample{LapDistPct: s.LapDistPct, Lat: s.Lat, Lon: s.Lon}
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

// referenceMapPath is the per-track reference map in mapsDir. The track's
// display name is the file name, with the characters Windows forbids replaced
// ("Kurzanbindung w/out Arena" → "Kurzanbindung w_out Arena.svg").
func referenceMapPath(mapsDir, track string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, strings.TrimSpace(track))
	if name == "" {
		name = "unknown track"
	}
	return filepath.Join(mapsDir, name+".svg")
}

// segmentSignature describes the segment list a map was labelled from. It is
// stored in the reference map's <desc>, so a re-detection or a cornerNames
// edit — anything that would make the file's labels disagree with the names
// analyze now prints — is noticed and the file redrawn.
func segmentSignature(segs []trackmap.Segment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = fmt.Sprintf("%s %.3f-%.3f", s.Name, s.EntryPct, s.ExitPct)
	}
	return "Segments: " + strings.Join(parts, ", ")
}

// refreshReferenceMap keeps mapsDir/<track>.svg — an unhighlighted map with
// every corner labelled — in step with the current segment list.
//
// It exists so there is one picture of a track to keep and point at: each
// session's map carries that session's highlights and sits among the
// telemetry, and is the wrong thing to reuse as a key to the T-labels. It is
// redrawn only when the segments change, not every run, so the copy stays
// stable — a reference that shifted slightly each session would not be one.
//
// Without a track map there are no labels to key, so nothing is written.
func refreshReferenceMap(mapsDir string, lap *analysis.Lap, segs []trackmap.Segment, track string) (path string, written bool, err error) {
	if mapsDir == "" || len(segs) == 0 {
		return "", false, nil
	}
	path = referenceMapPath(mapsDir, track)
	sig := segmentSignature(segs)
	if existing, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(existing), "<desc>"+xmlText(sig)+"</desc>") {
		return path, false, nil
	}
	if err := os.MkdirAll(mapsDir, 0o755); err != nil {
		return path, false, err
	}
	opt := trackmap.MapOptions{
		Title:       track,
		Subtitle:    "Reference map — corner labels as analyze prints them",
		Description: sig,
	}
	if err := writeTrackMap(path, lap, segs, opt); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// runTrackMap is the -map step of RunAnalyze. Paths go to stderr, not the
// table sink, so they survive -json — which is how `coach -map` reports them.
//
// mapsDir is where the per-track reference copy lives ("" disables it).
func runTrackMap(ibtPath string, laps []analysis.Lap, lapNum int, segs []trackmap.Segment,
	highlightSpec, track, mapsDir string) {

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
	if err := writeTrackMap(path, lap, segs, sessionMapOptions(lap, track, highlights)); err != nil {
		analyzeDie("writing track map: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Track map → %s\n", path)

	// The reference copy is a convenience: failing to write it must not fail
	// the run that has just produced the map that was asked for.
	ref, written, err := refreshReferenceMap(mapsDir, lap, segs, track)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "Warning: could not update reference map %s: %v\n", ref, err)
	case written:
		fmt.Fprintf(os.Stderr, "Reference map updated → %s\n", ref)
	case ref != "":
		fmt.Fprintf(os.Stderr, "Reference map → %s\n", ref)
	}
}
