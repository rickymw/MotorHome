package gui

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const joyDoc = `{"devices":[{"alias":"pedals","name":"Heusinkveld Sim Pedals Sprint","hardwareId":"VID_30B7&PID_1001","id":0,"pinned":0,"connected":true,"visible":true,"hidden":false}],"maxId":15,"notes":["Pinned pedals to ID 0."]}`

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
	if strings.Join(got, " ") != "joy list -json" {
		t.Errorf("argv = %v", got)
	}
	r := decode[joyResponse](t, w)
	if len(r.Devices) != 1 || r.Devices[0].ID == nil || *r.Devices[0].ID != 0 || r.MaxID != 15 {
		t.Errorf("report = %+v", r.Report)
	}
}

func TestJoySetPinAndUnpin(t *testing.T) {
	var got []string
	s := joyServer(t, func(args ...string) ([]byte, error) {
		got = args
		return []byte(joyDoc), nil
	})

	if w := do(t, s, "POST", "/api/joy", `{"alias":"pedals","id":0}`); w.Code != http.StatusOK {
		t.Fatalf("pin status = %d: %s", w.Code, w.Body)
	}
	// -json goes ahead of the operands, where the flag parser will see it.
	if strings.Join(got, " ") != "joy pin -json pedals 0" {
		t.Errorf("pin argv = %v", got)
	}

	if w := do(t, s, "POST", "/api/joy", `{"alias":"pedals","id":null}`); w.Code != http.StatusOK {
		t.Fatalf("unpin status = %d", w.Code)
	}
	if strings.Join(got, " ") != "joy unpin -json pedals" {
		t.Errorf("unpin argv = %v", got)
	}
}

func TestJoySetRejectsBadRequests(t *testing.T) {
	s := joyServer(t, func(args ...string) ([]byte, error) {
		t.Errorf("a bad request reached the subcommand: %v", args)
		return nil, nil
	})
	for _, body := range []string{
		`{"alias":"","id":1}`,
		`{"alias":"-json","id":1}`,
		`{"alias":"all","id":null}`, // the panel acts on one row; never clear every pin
		`{"alias":"pedals","id":16}`,
		`{"alias":"pedals","id":-1}`,
		`not json`,
	} {
		if w := do(t, s, "POST", "/api/joy", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
}

// A refusal from the command comes back as its own message, not a bare
// "exit status 1".
func TestJoySetReportsTheCommandsRefusal(t *testing.T) {
	s := joyServer(t, func(...string) ([]byte, error) {
		return []byte("joy pin: no device matches \"pedls\"\n"), errors.New("exit status 1")
	})
	w := do(t, s, "POST", "/api/joy", `{"alias":"pedls","id":0}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no device matches") {
		t.Errorf("body = %s", w.Body)
	}
}

func TestJoyUnavailableWithoutReExec(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "GET", "/api/joy", ""); w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}
