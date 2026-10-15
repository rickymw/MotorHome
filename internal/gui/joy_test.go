package gui

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/joyid"
)

const joyDoc = `{"devices":[{"alias":"pedals","name":"Heusinkveld Sim Pedals Sprint","hardwareId":"VID_30B7&PID_1001","id":4,"connected":true,"visible":false,"hidden":true}],"hidInterfaces":75,"hidLimit":64}`

func joyServer(t *testing.T, run func(args ...string) ([]byte, error)) *Server {
	t.Helper()
	s, _, _ := testServer(t, func(d *Deps) {
		d.RunSubcommand = func(_ time.Duration, args ...string) ([]byte, error) { return run(args...) }
	})
	return s
}

// The table comes from a re-exec, not an in-process read: a long-running server
// keeps the controller list it started with.
func TestJoyListReExecs(t *testing.T) {
	var got []string
	s := joyServer(t, func(args ...string) ([]byte, error) {
		got = args
		return []byte("warning: something on stderr\n" + joyDoc), nil
	})
	w := do(t, s, "GET", "/api/joy", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if strings.Join(got, " ") != "joy -json" {
		t.Errorf("argv = %v", got)
	}
	r := decode[joyid.Report](t, w)
	if len(r.Devices) != 1 || !r.Devices[0].Hidden || r.HIDInterfaces != 75 || r.HIDLimit != 64 {
		t.Errorf("report = %+v", r)
	}
}

// Pinning was removed; the route no longer accepts writes.
func TestJoyHasNoWriteRoute(t *testing.T) {
	s := joyServer(t, func(args ...string) ([]byte, error) {
		t.Errorf("a POST reached the subcommand: %v", args)
		return nil, nil
	})
	if w := do(t, s, "POST", "/api/joy", `{"alias":"pedals","id":0}`); w.Code == http.StatusOK {
		t.Errorf("POST /api/joy succeeded; pinning should be gone")
	}
}

func TestJoyReportsTheCommandsFailure(t *testing.T) {
	s := joyServer(t, func(...string) ([]byte, error) {
		return []byte("joy: DirectInput8Create failed: 0x80004005\n"), errors.New("exit status 1")
	})
	w := do(t, s, "GET", "/api/joy", "")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "DirectInput8Create") {
		t.Errorf("status = %d body = %s", w.Code, w.Body)
	}
}

func TestJoyUnavailableWithoutReExec(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "GET", "/api/joy", ""); w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}
