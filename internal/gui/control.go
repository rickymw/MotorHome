package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/launcher"
)

// controlResponse is what the rig control panel gets back from status, start
// and stop alike. One shape for all three because the panel redraws the same
// table from each of them — a start that reports what is now running saves the
// page a follow-up status poll.
type controlResponse struct {
	Apps    []launcher.AppResult `json:"apps"`
	Running int                  `json:"running"`
	Total   int                  `json:"total"`
}

func controlResponseOf(results []launcher.AppResult) controlResponse {
	return controlResponse{
		Apps:    results,
		Running: launcher.CountRunning(results),
		Total:   len(results),
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, controlResponseOf(launcher.Status(cfg, s.deps.NewProcessManager())))
}

// controlRequest optionally narrows a start or stop to one app, named by its
// display name (the name in the table, not the process image name).
//
// App is a pointer so "no body" and "an empty name" are different requests. No
// body is start-all/stop-all, which is what the buttons above the table send.
// An empty name is a page bug, and reading it as "all" would let that bug stop
// the whole rig mid-session when one app was meant.
type controlRequest struct {
	App *string `json:"app"`
}

// controlMaxBody caps the request. The body is one app name.
const controlMaxBody = 4096

func readControlRequest(r *http.Request) (controlRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, controlMaxBody))
	if err != nil {
		return controlRequest{}, err
	}
	var req controlRequest
	if len(bytes.TrimSpace(body)) == 0 {
		return req, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return controlRequest{}, err
	}
	if req.App != nil && strings.TrimSpace(*req.App) == "" {
		return controlRequest{}, errors.New("app name is empty")
	}
	return req, nil
}

// scopeControl resolves a request against the config. With no app named it
// returns cfg unchanged; otherwise a copy holding only that app.
//
// It refuses to guess, like `pb show` and `usb`: an unknown name is a 404 and a
// name shared by two entries is a 409, because starting or stopping the wrong
// program mid-session is cheap to undo and expensive to notice. Nothing stops
// two apps sharing a display name in the config, so the ambiguous case is real.
func scopeControl(cfg config.Config, req controlRequest) (config.Config, int, error) {
	if req.App == nil {
		return cfg, 0, nil
	}
	name := strings.TrimSpace(*req.App)
	var matches []config.App
	for _, app := range cfg.Apps {
		if app.Name == name {
			matches = append(matches, app)
		}
	}
	switch len(matches) {
	case 0:
		return config.Config{}, http.StatusNotFound, fmt.Errorf("no app named %q in the config", name)
	case 1:
	default:
		return config.Config{}, http.StatusConflict,
			fmt.Errorf("%d apps are named %q — rename one in Settings", len(matches), name)
	}

	// The delay exists to space out a sequence: an app launched before the one
	// that attaches to it. A single app has no sequence, and honouring its
	// delay would only hold the request open after the launch already happened.
	one := matches[0]
	one.DelayMs = 0
	scoped := cfg
	scoped.Apps = []config.App{one}
	return scoped, 0, nil
}

// controlScope is the shared front half of start and stop: read the config,
// read the request, narrow to the named app. ok is false once a response has
// been written.
func (s *Server) controlScope(w http.ResponseWriter, r *http.Request) (cfg, scoped config.Config, ok bool) {
	cfg, ok = s.loadConfigOr500(w)
	if !ok {
		return cfg, scoped, false
	}
	req, err := readControlRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad request: "+err.Error())
		return cfg, scoped, false
	}
	scoped, code, err := scopeControl(cfg, req)
	if err != nil {
		writeErr(w, code, err.Error())
		return cfg, scoped, false
	}
	return cfg, scoped, true
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	cfg, scoped, ok := s.controlScope(w, r)
	if !ok {
		return
	}
	pm := s.deps.NewProcessManager()

	// launcher.Start honours each app's delayMs, so a start-all takes as long
	// as the configured delays add up to. That is the same wait the CLI has and
	// the same wait the rig actually needs; the page shows a spinner rather
	// than the server cutting the sequence short.
	started := launcher.Start(scoped, pm)
	if len(scoped.Apps) == len(cfg.Apps) {
		writeJSON(w, http.StatusOK, controlResponseOf(started))
		return
	}

	// One app: the table still redraws every row, so report the whole rig with
	// the started app's own result laid over it. Its row takes Start's result
	// wholesale rather than the status re-check, because a process spawned a
	// moment ago may not be in tasklist yet and would read as stopped.
	results := launcher.Status(cfg, pm)
	for i := range results {
		for _, st := range started {
			if results[i].Name == st.Name {
				results[i] = st
			}
		}
	}
	writeJSON(w, http.StatusOK, controlResponseOf(results))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	cfg, scoped, ok := s.controlScope(w, r)
	if !ok {
		return
	}
	pm := s.deps.NewProcessManager()

	// A kill that returns without error says taskkill was accepted, not that
	// the process is gone — SimHub restarts itself, and an app with several
	// instances only loses the ones matching the image name. Re-checking is
	// what makes the panel show the rig's actual state rather than the state
	// the stop attempt implied, so any failure is merged into a fresh status
	// pass instead of being reported on its own.
	//
	// The re-check covers every app even when one was stopped. Kill works by
	// image name, so stopping one entry also stops any other entry sharing its
	// processName, and the table should show that rather than hide it.
	failures := make(map[string]string)
	for _, r := range launcher.Stop(scoped, pm) {
		if r.Outcome == launcher.OutcomeFailed {
			failures[r.Name] = r.Err
		}
	}

	results := launcher.Status(cfg, pm)
	for i := range results {
		if msg, bad := failures[results[i].Name]; bad {
			results[i].Outcome = launcher.OutcomeFailed
			results[i].Err = msg
		}
	}
	writeJSON(w, http.StatusOK, controlResponseOf(results))
}

// loadConfigOr500 reads the config, reporting a failure to the client and
// returning false if it cannot. Every handler that needs the app list calls it
// rather than caching a config at boot, because the settings panel can rewrite
// the file while the server is running.
func (s *Server) loadConfigOr500(w http.ResponseWriter) (config.Config, bool) {
	cfg, err := s.deps.LoadConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot read config: "+err.Error())
		return config.Config{}, false
	}
	return cfg, true
}
