package trackmap

// SVG rendering of the track outline, with segments labelled and selected ones
// highlighted.
//
// This exists so a debrief can *show* a corner instead of describing it. The
// segment names are positional and the driver does not read telemetry, so
// "T6" means nothing to them and "the last corner onto the start/finish
// straight" only works if they already know the circuit. A picture of the lap
// with the corner painted on it needs neither.
//
// The outline is the GPS trace of one lap, not a surveyed track: it is exactly
// as accurate as iRacing's Lat/Lon channels, which is plenty for "this one".

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// Highlight marks one segment for emphasis on the rendered map.
type Highlight struct {
	Segment int    // index into the segments slice
	Caption string // optional second line under the label; "" for none
}

// MapOptions controls RenderSVG.
type MapOptions struct {
	Title    string // first header line, e.g. the track name; "" for none
	Subtitle string // second header line, e.g. the lap; "" for none
	// Description goes in the SVG's <desc> element: not drawn, but read by
	// screen readers and by callers that need to know what a file was drawn
	// from (the reference map keys its staleness check on it).
	Description string
	Highlights  []Highlight
}

// ErrNoGPS is returned when the samples carry too few Lat/Lon fixes to draw.
var ErrNoGPS = errors.New("no GPS position in this lap's telemetry")

const (
	svgWidth     = 680.0
	svgSidePad   = 80.0 // room for labels either side of the outline
	svgTopPad    = 40.0
	svgBottomPad = 40.0
	svgMaxDrawH  = 560.0
	svgHeaderH   = 44.0
	charWidth    = 7.0 // rough width of one 12px label character, for sizing label boxes
	minGPSPoints = 20
	// minPointSpacing drops samples closer than this (in viewBox px) to the
	// last one kept. A 60Hz lap is ~5,000 points, roughly ten per pixel of
	// outline; thinning cuts the file by an order of magnitude with no visible
	// change.
	minPointSpacing = 2.0
)

// mapPoint is one projected sample, in SVG coordinates.
type mapPoint struct {
	pct  float32
	x, y float64
}

// RenderSVG writes a standalone SVG of the lap outline described by samples.
//
// Corners and chicanes are labelled with their segment name; straights are
// drawn but not labelled, since a label on every straight would crowd the ones
// that matter. Highlighted segments are drawn thicker in a contrasting colour
// with a bold label and their caption.
//
// samples must be one lap in track order. Samples without a GPS fix (Lat and
// Lon both zero) are skipped.
func RenderSVG(w io.Writer, samples []Sample, segs []Segment, opt MapOptions) error {
	pts := projectSamples(samples)
	if len(pts) < minGPSPoints {
		return ErrNoGPS
	}

	headerH := 0.0
	if opt.Title != "" || opt.Subtitle != "" {
		headerH = svgHeaderH
	}
	height := fitToBox(pts, headerH)
	pts = thinPoints(pts, minPointSpacing)

	highlighted := map[int]string{}
	for _, h := range opt.Highlights {
		if h.Segment >= 0 && h.Segment < len(segs) {
			highlighted[h.Segment] = h.Caption
		}
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="100%%" viewBox="0 0 %.0f %.0f" role="img">`+"\n",
		svgWidth, height)
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(fallbackTitle(opt.Title)))
	if opt.Description != "" {
		fmt.Fprintf(&b, "<desc>%s</desc>\n", esc(opt.Description))
	}
	b.WriteString(svgStyle)
	fmt.Fprintf(&b, `<rect class="bg" width="%.0f" height="%.0f"/>`+"\n", svgWidth, height)

	if opt.Title != "" {
		fmt.Fprintf(&b, `<text class="title" x="%.0f" y="%.0f">%s</text>`+"\n", svgSidePad/2, svgTopPad-8, esc(opt.Title))
	}
	if opt.Subtitle != "" {
		fmt.Fprintf(&b, `<text class="sub" x="%.0f" y="%.0f">%s</text>`+"\n", svgSidePad/2, svgTopPad+12, esc(opt.Subtitle))
	}

	// Whole lap first, so every segment drawn after it sits on top.
	writePolyline(&b, "track", pts, true)
	for i, seg := range segs {
		if seg.Kind == KindStraight {
			continue
		}
		if _, ok := highlighted[i]; ok {
			continue // drawn last, above everything
		}
		for _, run := range segmentRuns(pts, seg) {
			writePolyline(&b, "corner", run, false)
		}
	}
	for i, seg := range segs {
		if _, ok := highlighted[i]; !ok {
			continue
		}
		for _, run := range segmentRuns(pts, seg) {
			writePolyline(&b, "hl", run, false)
		}
	}

	writeStartFinishMarks(&b, pts)

	// Highlighted labels are placed first so they get the clearest spots; the
	// start/finish label next, as the one landmark every reader orients by.
	// The header only covers the top-left, so labels may use the rest of the
	// top margin — the header lines are registered as already-placed boxes.
	lb := &labeler{pts: pts, top: 6, bottom: height - 6}
	if opt.Title != "" {
		lb.placed = append(lb.placed, rect{svgSidePad/2 - 4, svgTopPad - 24, svgSidePad/2 + float64(len([]rune(opt.Title)))*9 + 4, svgTopPad})
	}
	if opt.Subtitle != "" {
		lb.placed = append(lb.placed, rect{svgSidePad/2 - 4, svgTopPad + 2, svgSidePad/2 + float64(len([]rune(opt.Subtitle)))*charWidth + 4, svgTopPad + 20})
	}
	for i, seg := range segs {
		if caption, ok := highlighted[i]; ok {
			lb.place(&b, segmentRuns(pts, seg), seg.Name, caption, true)
		}
	}
	lb.place(&b, [][]mapPoint{pts[:min(len(pts), 12)]}, "Start/finish", "", false)
	for i, seg := range segs {
		if _, ok := highlighted[i]; ok || seg.Kind == KindStraight {
			continue
		}
		lb.place(&b, segmentRuns(pts, seg), seg.Name, "", false)
	}

	b.WriteString("</svg>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// svgStyle keeps the colours in one place and gives the file a dark variant,
// since it is opened in whatever browser or viewer happens to be at hand.
const svgStyle = `<style>
.bg{fill:#FFFFFF}
.track{fill:none;stroke:#D3D1C7;stroke-width:9;stroke-linecap:round;stroke-linejoin:round}
.corner{fill:none;stroke:#888780;stroke-width:9;stroke-linecap:round;stroke-linejoin:round}
.hl{fill:none;stroke:#D85A30;stroke-width:12;stroke-linecap:round;stroke-linejoin:round}
.sf{stroke:#2C2C2A;stroke-width:3}
.dir{fill:#5F5E5A}
text{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;font-size:12px;fill:#5F5E5A}
.title{font-size:16px;font-weight:500;fill:#2C2C2A}
.sub{fill:#5F5E5A}
.lbl-hl{font-size:14px;font-weight:500;fill:#993C1D}
.cap{fill:#993C1D}
@media (prefers-color-scheme: dark){
.bg{fill:#1F1E1D}
.track{stroke:#444441}
.corner{stroke:#888780}
.hl{stroke:#F0997B}
.sf{stroke:#F1EFE8}
.dir{fill:#B4B2A9}
text{fill:#B4B2A9}
.title{fill:#F1EFE8}
.sub{fill:#B4B2A9}
.lbl-hl{fill:#F5C4B3}
.cap{fill:#F5C4B3}
}
</style>
`

func fallbackTitle(t string) string {
	if t == "" {
		return "Track map"
	}
	return t
}

// projectSamples converts GPS fixes to an equirectangular plane (north up),
// dropping samples without a fix and consecutive duplicates. The coordinates
// are unscaled; fitToBox maps them into the viewBox.
func projectSamples(samples []Sample) []mapPoint {
	var lat0 float64
	n := 0
	for _, s := range samples {
		if s.Lat != 0 || s.Lon != 0 {
			lat0 += s.Lat
			n++
		}
	}
	if n == 0 {
		return nil
	}
	cosLat := math.Cos(lat0 / float64(n) * math.Pi / 180)

	pts := make([]mapPoint, 0, n)
	for _, s := range samples {
		if s.Lat == 0 && s.Lon == 0 {
			continue
		}
		p := mapPoint{pct: s.LapDistPct, x: s.Lon * cosLat, y: -s.Lat}
		if k := len(pts); k > 0 && pts[k-1].x == p.x && pts[k-1].y == p.y {
			continue
		}
		pts = append(pts, p)
	}
	return pts
}

// fitToBox scales pts in place to fill the drawing area, preserving aspect
// ratio, and returns the resulting viewBox height.
func fitToBox(pts []mapPoint, headerH float64) float64 {
	minX, maxX, minY, maxY := pts[0].x, pts[0].x, pts[0].y, pts[0].y
	for _, p := range pts {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	spanX, spanY := maxX-minX, maxY-minY
	if spanX == 0 {
		spanX = 1e-9
	}
	if spanY == 0 {
		spanY = 1e-9
	}
	drawW := svgWidth - 2*svgSidePad
	scale := drawW / spanX
	if spanY*scale > svgMaxDrawH {
		scale = svgMaxDrawH / spanY
	}
	offX := svgSidePad + (drawW-spanX*scale)/2
	offY := svgTopPad + headerH
	for i := range pts {
		pts[i].x = offX + (pts[i].x-minX)*scale
		pts[i].y = offY + (pts[i].y-minY)*scale
	}
	return math.Ceil(offY + spanY*scale + svgBottomPad)
}

// thinPoints keeps the first point and then each point at least minDist from
// the last one kept. Every kept point retains its own LapDistPct, so segment
// membership is unaffected.
func thinPoints(pts []mapPoint, minDist float64) []mapPoint {
	if len(pts) == 0 {
		return pts
	}
	out := []mapPoint{pts[0]}
	for _, p := range pts[1:] {
		last := out[len(out)-1]
		if math.Hypot(p.x-last.x, p.y-last.y) >= minDist {
			out = append(out, p)
		}
	}
	return out
}

// inSegment reports whether pct lies in seg, including a segment that wraps
// across the start/finish line (exit before entry).
func inSegment(pct float32, seg Segment) bool {
	if seg.ExitPct >= seg.EntryPct {
		return pct >= seg.EntryPct && pct <= seg.ExitPct
	}
	return pct >= seg.EntryPct || pct <= seg.ExitPct
}

// segmentRuns returns the contiguous stretches of pts inside seg. A wrapping
// segment yields two runs — one either side of the line — which must be drawn
// separately or the join would cut straight across the infield.
func segmentRuns(pts []mapPoint, seg Segment) [][]mapPoint {
	var runs [][]mapPoint
	var cur []mapPoint
	for _, p := range pts {
		if inSegment(p.pct, seg) {
			cur = append(cur, p)
			continue
		}
		if len(cur) > 1 {
			runs = append(runs, cur)
		}
		cur = nil
	}
	if len(cur) > 1 {
		runs = append(runs, cur)
	}
	return runs
}

func writePolyline(b *strings.Builder, class string, pts []mapPoint, closed bool) {
	fmt.Fprintf(b, `<polyline class="%s" points="`, class)
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(b, "%.1f,%.1f", p.x, p.y)
	}
	if closed && len(pts) > 0 {
		fmt.Fprintf(b, " %.1f,%.1f", pts[0].x, pts[0].y)
	}
	b.WriteString(`"/>` + "\n")
}

// tangentAt returns the unit direction of travel at pts[i].
func tangentAt(pts []mapPoint, i int) (float64, float64) {
	a := pts[max(i-3, 0)]
	c := pts[min(i+3, len(pts)-1)]
	dx, dy := c.x-a.x, c.y-a.y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return 1, 0
	}
	return dx / l, dy / l
}

// writeStartFinishMarks draws a bar across the track at the first sample and
// an arrowhead beside the track a little further on, pointing the way the lap
// is driven. The arrow sits off the line so it never hides what it describes.
func writeStartFinishMarks(b *strings.Builder, pts []mapPoint) {
	tx, ty := tangentAt(pts, 0)
	nx, ny := -ty, tx
	p := pts[0]
	fmt.Fprintf(b, `<line class="sf" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`+"\n",
		p.x-nx*11, p.y-ny*11, p.x+nx*11, p.y+ny*11)

	i := min(len(pts)/25+1, len(pts)-1)
	ax, ay := tangentAt(pts, i)
	q := pts[i]
	// Of the two sides, the one further from the rest of the track.
	side := 1.0
	if clearance(pts, rect{q.x - ay*16, q.y + ax*16, q.x - ay*16, q.y + ax*16}) <
		clearance(pts, rect{q.x + ay*16, q.y - ax*16, q.x + ay*16, q.y - ax*16}) {
		side = -1
	}
	bx, by := q.x-ay*side*16, q.y+ax*side*16
	fmt.Fprintf(b, `<polygon class="dir" points="%.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`+"\n",
		bx+ax*7, by+ay*7, bx-ax*5-ay*5, by-ay*5+ax*5, bx-ax*5+ay*5, by-ay*5-ax*5)
}

// rect is an axis-aligned box in viewBox coordinates.
type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) overlaps(o rect) bool {
	return r.x0 < o.x1 && o.x0 < r.x1 && r.y0 < o.y1 && o.y0 < r.y1
}

// clearance is the distance from r to the nearest track point (0 if one lies
// inside it).
func clearance(pts []mapPoint, r rect) float64 {
	best := math.Inf(1)
	for _, p := range pts {
		dx := math.Max(math.Max(r.x0-p.x, 0), p.x-r.x1)
		dy := math.Max(math.Max(r.y0-p.y, 0), p.y-r.y1)
		if d := math.Hypot(dx, dy); d < best {
			best = d
		}
	}
	return best
}

// labeler places labels beside the track without covering it or each other.
//
// Pushing each label "outward, away from the centre" was tried first and fails
// on any real circuit: tracks are not convex, so the outside of one corner is
// often the inside of the next, and a caption long enough to need clamping at
// the edge of the drawing gets pushed straight back over the line. Instead each
// label tries a handful of spots along its segment, either side, at a few
// distances, and takes the first with comfortable clearance — or failing that,
// the one with the most.
type labeler struct {
	pts         []mapPoint
	top, bottom float64
	placed      []rect
}

// labelClearance is the gap from the track that counts as comfortable; the
// track stroke is up to 6px either side of the line, so this leaves ~6px air.
const labelClearance = 12.0

func (lb *labeler) place(b *strings.Builder, runs [][]mapPoint, name, caption string, isHL bool) {
	if len(runs) == 0 {
		return
	}
	run := runs[0]
	for _, r := range runs[1:] {
		if len(r) > len(run) {
			run = r
		}
	}

	lines := wrapCaption(caption, captionWrapChars)
	nameW := float64(len([]rune(name))) * charWidth
	if isHL {
		nameW *= 1.25 // 14px bold
	}
	w := nameW
	for _, l := range lines {
		w = math.Max(w, float64(len([]rune(l)))*charWidth)
	}
	h := 16.0 + 16*float64(len(lines))

	type cand struct {
		box   rect
		score float64
	}
	var best *cand
	for _, frac := range []float64{0.5, 0.35, 0.65, 0.2, 0.8} {
		i := int(frac * float64(len(run)-1))
		a, c := run[max(i-3, 0)], run[min(i+3, len(run)-1)]
		tx, ty := c.x-a.x, c.y-a.y
		if l := math.Hypot(tx, ty); l > 0 {
			tx, ty = tx/l, ty/l
		} else {
			tx, ty = 1, 0
		}
		p := run[i]
		for _, off := range []float64{18, 28, 42, 60, 85} {
			for _, side := range []float64{1, -1} {
				nx, ny := -ty*side, tx*side
				cx, cy := p.x+nx*off, p.y+ny*off // centre of the label box
				// Box sits on the far side of its own centre from the track,
				// so a long caption grows away from the line, not across it.
				bx := cx - w/2 + nx*w/2
				by := cy - h/2 + ny*h/2
				bx = math.Min(math.Max(bx, 6), svgWidth-6-w)
				by = math.Min(math.Max(by, lb.top), lb.bottom-h)
				k := cand{box: rect{bx, by, bx + w, by + h}}
				k.score = clearance(lb.pts, k.box)
				for _, o := range lb.placed {
					if k.box.overlaps(o) {
						k.score = -1
					}
				}
				if best == nil || k.score > best.score {
					kk := k
					best = &kk
				}
				if k.score >= labelClearance {
					goto done
				}
			}
		}
	}
done:
	r := best.box
	lb.placed = append(lb.placed, rect{r.x0 - 4, r.y0 - 2, r.x1 + 4, r.y1 + 2})

	// Text is drawn left-aligned in its box; that keeps the name and caption
	// lined up wherever the box ended up.
	class := ""
	if isHL {
		class = ` class="lbl-hl"`
	}
	fmt.Fprintf(b, `<text%s x="%.1f" y="%.1f" dominant-baseline="central">%s</text>`+"\n",
		class, r.x0, r.y0+8, esc(name))
	for i, l := range lines {
		fmt.Fprintf(b, `<text class="cap" x="%.1f" y="%.1f" dominant-baseline="central">%s</text>`+"\n",
			r.x0, r.y0+25+16*float64(i), esc(l))
	}
}

// captionWrapChars is the widest caption line. A one-line caption of coaching
// advice ("Turn in later, then straight to throttle") is wider than any gap a
// real circuit leaves beside a corner, so it would always land on the track.
const captionWrapChars = 18

// wrapCaption breaks s into lines of at most width characters at spaces. A
// single word longer than width gets a line of its own rather than being cut.
func wrapCaption(s string, width int) []string {
	var lines []string
	var cur string
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
