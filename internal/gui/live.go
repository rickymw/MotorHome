package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// LiveSnapshot is one frame of the live panel: where the player is, their lap
// clock, the tank, and the weather.
//
// It is this package's own shape rather than iracing.LiveData because that type
// is Windows-only and carries raw per-CarIdx arrays and SDK-shaped structs; this
// one is a wire format the page depends on. The Windows shim does the
// conversion.
//
// The panel deliberately carries no car-ahead/car-behind gaps (they were
// removed 2026-09-12): it is being used to explore which shared-memory values
// are worth building dashboard elements on, and the `live` subcommand still
// prints the gaps for anyone who wants them.
type LiveSnapshot struct {
	Connected bool `json:"connected"`
	// Message is the plain-language reason there is nothing to show. Detail is
	// the underlying diagnostic behind it, kept separate because they are read
	// by different people at different moments: a panel glanced at mid-session
	// wants "iRacing is not running", and only someone debugging wants
	// "OpenFileMappingW: The system cannot find the file specified".
	//
	// The `live` subcommand prints the diagnostic as its whole message, which
	// is right for a command whose -raw mode exists to troubleshoot this. A
	// dashboard is not that.
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`

	Track string `json:"track,omitempty"`
	Car   string `json:"car,omitempty"`

	Position      int     `json:"position,omitempty"`
	FieldSize     int     `json:"fieldSize,omitempty"`
	ClassPosition int     `json:"classPosition,omitempty"`
	ClassSize     int     `json:"classSize,omitempty"`
	Lap           int     `json:"lap,omitempty"`
	LapDistPct    float32 `json:"lapDistPct"`
	OnPitRoad     bool    `json:"onPitRoad"`

	// Fuel and Conditions are nil when the sim published none of their
	// variables, so the page can say "not available" instead of drawing a row
	// of zeros. Timing is always present: every build publishes the lap clock.
	Timing     *LiveTiming     `json:"timing,omitempty"`
	Fuel       *LiveFuel       `json:"fuel,omitempty"`
	Conditions *LiveConditions `json:"conditions,omitempty"`
}

// LiveTiming is the player's lap clock. Lap times of zero mean "no lap yet";
// a negative LastLap is iRacing's marker for an invalidated lap.
type LiveTiming struct {
	CurrentLap float32     `json:"currentLap"`
	LastLap    float32     `json:"lastLap"`
	BestLap    float32     `json:"bestLap"`
	BestLapNum int         `json:"bestLapNum,omitempty"`
	Deltas     []LiveDelta `json:"deltas"`
}

// LiveDelta is one rolling delta channel. Ref names which reference lap:
// best, optimal, sessionBest, sessionOptimal, last.
//
// Valid must gate the display. iRacing publishes a number even when there is
// no reference lap to compare against, and without the flag an out lap would
// show a confident delta to nothing.
type LiveDelta struct {
	Ref     string  `json:"ref"`
	Seconds float32 `json:"seconds"`
	Rate    float32 `json:"rate"` // s/s; positive = losing time right now
	Valid   bool    `json:"valid"`
}

// LiveFuel is the tank reading plus the server's running consumption estimate.
//
// The estimate is stateful — the server watches lap boundaries go by — so it
// only exists for laps someone was streaming through. MeasuredLaps == 0 means
// no estimate yet, and every per-lap field is then zero.
type LiveFuel struct {
	Litres       float32 `json:"litres"`
	Pct          float32 `json:"pct"`
	UsePerHourKg float32 `json:"usePerHourKg"`

	MeasuredLaps  int     `json:"measuredLaps"`
	WindowLaps    int     `json:"windowLaps"`
	LastLap       float32 `json:"lastLap"`
	PerLapAverage float32 `json:"perLapAverage"`
	PerLapWorst   float32 `json:"perLapWorst"`
	LapsLeftAvg   float32 `json:"lapsLeftAverage"`
	LapsLeftWorst float32 `json:"lapsLeftWorst"`
}

// LiveConditions is the weather at the start/finish line. Pointer fields are
// omitted when the running build does not publish that variable — zero
// degrees and "no rain" are real readings and must not stand in for absent.
type LiveConditions struct {
	AirTempC       *float32 `json:"airTempC,omitempty"`
	TrackTempC     *float32 `json:"trackTempC,omitempty"`
	Humidity       *float32 `json:"humidity,omitempty"` // 0–1
	WindMS         *float32 `json:"windMS,omitempty"`
	WindDirDeg     *float32 `json:"windDirDeg,omitempty"`
	AirPressureHPa *float32 `json:"airPressureHPa,omitempty"`
	AirDensity     *float32 `json:"airDensity,omitempty"`    // kg/m³
	FogLevel       *float32 `json:"fogLevel,omitempty"`      // 0–1
	Precipitation  *float32 `json:"precipitation,omitempty"` // 0–1
	Skies          string   `json:"skies,omitempty"`
	TrackWetness   string   `json:"trackWetness,omitempty"`
	DeclaredWet    *bool    `json:"declaredWet,omitempty"`
}

// liveStreamMaxHz mirrors the `live` subcommand's clamp. Above 60 Hz there is
// nothing new to read — iRacing publishes at 60 — and each frame is a browser
// repaint.
const (
	liveStreamMinHz     = 1
	liveStreamMaxHz     = 60
	liveStreamDefaultHz = 5
)

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if s.deps.Live == nil {
		unsupported(w, "live telemetry")
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Live.Snapshot())
}

// handleLiveStream pushes a snapshot per tick as server-sent events.
//
// SSE rather than a WebSocket because net/http speaks it with no framing code
// and no dependency: the data only ever flows one way, and the browser's
// EventSource reconnects on its own. A poll loop from the page would work too,
// but at 5–60 Hz it would mean a request per frame.
func (s *Server) handleLiveStream(w http.ResponseWriter, r *http.Request) {
	if s.deps.Live == nil {
		unsupported(w, "live telemetry")
		return
	}

	hz := liveStreamDefaultHz
	if v := r.URL.Query().Get("hz"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "hz must be a number, got "+strconv.Quote(v))
			return
		}
		hz = min(max(n, liveStreamMinHz), liveStreamMaxHz)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "this server cannot stream")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Nothing here is proxied in normal use, but a buffering proxy would hold
	// the whole stream until it ended, which for this endpoint is never.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	tick := time.NewTicker(time.Second / time.Duration(hz))
	defer tick.Stop()

	// The request context is the only stop signal: the page closes the
	// EventSource when the user leaves the panel, and the handler must return
	// then rather than reading shared memory forever.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			payload, err := json.Marshal(s.deps.Live.Snapshot())
			if err != nil {
				// A snapshot that cannot encode is a bug in the shim, not a
				// transient condition; ending the stream surfaces it rather
				// than silently dropping frames forever.
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
