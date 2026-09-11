package gui

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/rickymw/MotorHome/internal/shaker"
)

// guiMaxLevel caps what the panel is allowed to ask for, independently of what
// the page sends.
//
// It matches the CLI's own default ceiling. A browser control is easier to hit
// by accident than a typed command, and the request body is the only thing
// between a stray click and full-scale output into hardware bolted to a seat,
// so the ceiling is enforced here rather than trusted to the page.
const guiMaxLevel = 0.5

// shakerStepSeconds is how long each step of the ramp sounds. Shorter than a
// CLI run would be: the page holds the request for the whole ramp, and this is
// long enough to feel a step and decide about it without being long enough that
// the panel looks hung.
const shakerStepSeconds = 1.2

type shakerDevice struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Target marks the device a test would play to, so the page does not have
	// to re-implement the name matching to highlight it.
	Target bool `json:"target"`
}

type shakerStatusResponse struct {
	Devices []shakerDevice `json:"devices"`
	// TargetName is empty when nothing matched, which the page shows as a
	// disabled card rather than an error — an absent transducer is a normal
	// state, not a fault.
	TargetName string  `json:"targetName,omitempty"`
	Match      string  `json:"match"`
	MaxLevel   float64 `json:"maxLevel"`
	// Problem explains an unmatched or ambiguous device in the words the
	// shaker package uses, so the browser and the terminal say the same thing.
	Problem string `json:"problem,omitempty"`
}

type shakerRunRequest struct {
	MaxLevel float64 `json:"maxLevel"`
}

type shakerStep struct {
	Level float64 `json:"level"`
	Freq  float64 `json:"freq"`
	Secs  float64 `json:"secs"`
}

type shakerRunResponse struct {
	Device  string       `json:"device"`
	Steps   []shakerStep `json:"steps"`
	Played  int          `json:"played"`
	Aborted bool         `json:"aborted"`
}

func (s *Server) handleShakerStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Shaker == nil {
		unsupported(w, "transducer test")
		return
	}

	devs, err := s.deps.Shaker.Devices()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot enumerate audio output devices: "+err.Error())
		return
	}

	resp := shakerStatusResponse{
		Match:    shaker.DefaultDeviceMatch,
		MaxLevel: guiMaxLevel,
	}

	target, findErr := shaker.FindDevice(devs, shaker.DefaultDeviceMatch)
	if findErr != nil {
		resp.Problem = findErr.Error()
	} else {
		resp.TargetName = target.Name
	}

	for _, d := range devs {
		resp.Devices = append(resp.Devices, shakerDevice{
			ID:     d.ID,
			Name:   d.Name,
			Target: findErr == nil && d.ID == target.ID,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleShakerRun plays the ramp.
//
// Unlike `usb on|off`, this runs in-process rather than through RunSubcommand.
// Playback needs no elevated token, and more to the point a browser cannot send
// Ctrl-C to a child process — a page that starts the seat moving must have a
// Stop that works while it is moving, and that means holding the player here
// where the stop handler can reach it.
//
// The request is held for the length of the ramp (~13s at the defaults). That
// is deliberate: the response is the record of what was played, and the page
// keeps its Stop button live throughout because the run is a separate request
// from the stop.
func (s *Server) handleShakerRun(w http.ResponseWriter, r *http.Request) {
	if s.deps.Shaker == nil {
		unsupported(w, "transducer test")
		return
	}

	var req shakerRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	if req.MaxLevel <= 0 || req.MaxLevel > guiMaxLevel {
		writeErr(w, http.StatusBadRequest, "maxLevel must be above 0 and no more than 0.5")
		return
	}

	// Serialised: two overlapping ramps would interleave buffers on one device,
	// and the second would silently inherit the first's Stop.
	if !s.shakerMu.TryLock() {
		writeErr(w, http.StatusConflict, "a transducer test is already running")
		return
	}
	defer s.shakerMu.Unlock()

	devs, err := s.deps.Shaker.Devices()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot enumerate audio output devices: "+err.Error())
		return
	}
	target, err := shaker.FindDevice(devs, shaker.DefaultDeviceMatch)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	plan := shaker.StepPlan(shaker.DefaultSteps, shaker.DefaultFreq, shakerStepSeconds, req.MaxLevel)
	if len(plan) == 0 {
		writeErr(w, http.StatusBadRequest, "no step is gentle enough for that maximum")
		return
	}
	for _, t := range plan {
		if err := t.Validate(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	s.shakerAbort.Store(false)

	resp := shakerRunResponse{Device: target.Name}
	for _, t := range plan {
		resp.Steps = append(resp.Steps, shakerStep{Level: t.Level, Freq: t.Freq, Secs: t.Seconds})
	}

	for _, t := range plan {
		// Checked between steps as well as inside playback: Stop silences the
		// buffer that is sounding, and this is what keeps the next one from
		// starting straight after it.
		if s.shakerAbort.Load() {
			break
		}
		if err := s.deps.Shaker.Play(target.ID, t.PCM(shaker.DefaultFormat), shaker.DefaultFormat); err != nil {
			writeErr(w, http.StatusInternalServerError, "playback failed: "+err.Error())
			return
		}
		resp.Played++
	}

	resp.Aborted = s.shakerAbort.Load()
	writeJSON(w, http.StatusOK, resp)
}

// handleShakerStop cuts the output immediately.
//
// It deliberately does not take the run lock — it has to be serviceable while a
// run is holding it, which is the entire point of a stop control.
func (s *Server) handleShakerStop(w http.ResponseWriter, r *http.Request) {
	if s.deps.Shaker == nil {
		unsupported(w, "transducer test")
		return
	}
	s.shakerAbort.Store(true)
	s.deps.Shaker.Stop()
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}
