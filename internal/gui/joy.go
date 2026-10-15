package gui

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/joyid"
)

// joyTimeout bounds one `joy` run. It reads a few registry keys and asks WinMM
// for sixteen slots; anything near this long means something is hung.
const joyTimeout = 30 * time.Second

// joyResponse is the ID table plus what the command printed when it did not
// produce one, so a refused pin explains itself in the panel.
type joyResponse struct {
	joyid.Report
	Output string `json:"output,omitempty"`
}

// handleJoyList reports every rig device's joystick ID.
//
// It re-execs `joy -json` rather than reading the registry in-process, even
// though nothing here needs elevation. Which controllers a program can see is
// fixed when that program starts — this server's view is from whenever it was
// launched, and on 2026-10-14 a process started before a virtual input device
// appeared went on seeing the pedals that every newer process had lost. Only a
// fresh process answers the question the panel asks: would a game started now
// see this device?
func (s *Server) handleJoyList(w http.ResponseWriter, r *http.Request) {
	s.runJoy(w, "joy", "list", "-json")
}

type joySetRequest struct {
	Alias string `json:"alias"`
	// ID is the ID to pin to; null unpins.
	ID *int `json:"id"`
}

// handleJoySet pins or unpins one device.
func (s *Server) handleJoySet(w http.ResponseWriter, r *http.Request) {
	var req joySetRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	alias := strings.TrimSpace(req.Alias)
	if alias == "" {
		writeErr(w, http.StatusBadRequest, "no device named")
		return
	}
	// The alias reaches a command line; a leading dash would be read as a flag.
	if strings.HasPrefix(alias, "-") {
		writeErr(w, http.StatusBadRequest, "device name must not start with a dash")
		return
	}
	// "all" is a valid unpin target on the CLI, but the panel only ever acts
	// on one row — accepting it here would let a page bug clear every pin.
	if strings.EqualFold(alias, "all") {
		writeErr(w, http.StatusBadRequest, "name one device")
		return
	}

	if req.ID == nil {
		s.runJoy(w, "joy", "unpin", "-json", alias)
		return
	}
	if *req.ID < 0 || *req.ID > joyid.MaxID {
		writeErr(w, http.StatusBadRequest, "ID must be from 0 to "+strconv.Itoa(joyid.MaxID))
		return
	}
	s.runJoy(w, "joy", "pin", "-json", alias, strconv.Itoa(*req.ID))
}

func (s *Server) runJoy(w http.ResponseWriter, args ...string) {
	if s.deps.RunSubcommand == nil {
		writeErr(w, http.StatusNotImplemented, "joystick IDs are not available in this build")
		return
	}
	out, runErr := s.deps.RunSubcommand(joyTimeout, args...)

	doc, ok := extractJSONDocument(out)
	if !ok {
		msg := strings.TrimSpace(string(out))
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		status := http.StatusInternalServerError
		if runErr != nil {
			// The command ran and refused: a bad alias, a pin it could not
			// save. That is the request's problem, not the server's.
			status = http.StatusConflict
		}
		writeErr(w, status, msg)
		return
	}

	var resp joyResponse
	if err := json.Unmarshal(doc, &resp.Report); err != nil {
		writeErr(w, http.StatusInternalServerError, "unreadable joy output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
