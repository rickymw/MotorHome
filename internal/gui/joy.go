package gui

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/joyid"
)

// joyTimeout bounds one `joy` run. It reads a few registry keys, asks
// DirectInput for its controller list and counts HID interfaces; anything near
// this long means something is hung.
const joyTimeout = 30 * time.Second

// handleJoyList reports whether each rig controller is visible to games, plus
// the machine's HID interface count.
//
// It re-execs `joy -json` rather than asking DirectInput in-process. Which
// controllers a program can see is fixed when that program starts — this
// server's view is from whenever it was launched, and on 2026-10-14 a process
// started earlier went on seeing the pedals that every newer process had lost.
// Only a fresh process answers the question the panel asks: would a game started
// now see this device?
func (s *Server) handleJoyList(w http.ResponseWriter, r *http.Request) {
	if s.deps.RunSubcommand == nil {
		writeErr(w, http.StatusNotImplemented, "game controller checks are not available in this build")
		return
	}
	out, runErr := s.deps.RunSubcommand(joyTimeout, "joy", "-json")

	doc, ok := extractJSONDocument(out)
	if !ok {
		msg := strings.TrimSpace(string(out))
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		writeErr(w, http.StatusInternalServerError, msg)
		return
	}

	var report joyid.Report
	if err := json.Unmarshal(doc, &report); err != nil {
		writeErr(w, http.StatusInternalServerError, "unreadable joy output: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}
