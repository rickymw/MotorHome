package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/shaker"
)

type stubShaker struct {
	mu      sync.Mutex
	devs    []shaker.Device
	devErr  error
	playErr error
	plays   int
	stops   int

	// block, when non-nil, holds each Play until it is closed — used to test
	// what the server does while a run is in flight.
	block chan struct{}
}

func (s *stubShaker) Devices() ([]shaker.Device, error) {
	if s.devErr != nil {
		return nil, s.devErr
	}
	return s.devs, nil
}

func (s *stubShaker) Play(int, []byte, shaker.Format) error {
	s.mu.Lock()
	s.plays++
	s.mu.Unlock()
	if s.block != nil {
		<-s.block
	}
	return s.playErr
}

func (s *stubShaker) Stop() {
	s.mu.Lock()
	s.stops++
	s.mu.Unlock()
}

func (s *stubShaker) counts() (plays, stops int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plays, s.stops
}

func shakerDevs() []shaker.Device {
	return []shaker.Device{
		{ID: 0, Name: "Speakers (Sound Blaster X4)", Channels: 2},
		{ID: 1, Name: "Headphones (KT USB Audio)", Channels: 2},
	}
}

// shakerServer builds a server whose only wired provider is the transducer.
func shakerServer(t *testing.T, p ShakerProvider) *Server {
	t.Helper()
	return New(Deps{Shaker: p})
}

func TestShakerStatusNamesTarget(t *testing.T) {
	h := shakerServer(t, &stubShaker{devs: shakerDevs()})
	w := do(t, h, "GET", "/api/shaker", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp shakerStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if resp.TargetName != "Headphones (KT USB Audio)" {
		t.Errorf("target = %q, want the KT USB Audio device", resp.TargetName)
	}
	if len(resp.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(resp.Devices))
	}
	if !resp.Devices[1].Target || resp.Devices[0].Target {
		t.Error("the wrong device is flagged as the target")
	}
}

// TestShakerStatusMissingTargetIsNotAnError: an unplugged transducer is a
// normal state, so the card greys out rather than showing a failure.
func TestShakerStatusMissingTargetIsNotAnError(t *testing.T) {
	h := shakerServer(t, &stubShaker{devs: []shaker.Device{{ID: 0, Name: "Speakers (Sound Blaster X4)"}}})
	w := do(t, h, "GET", "/api/shaker", "")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp shakerStatusResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TargetName != "" {
		t.Errorf("target = %q, want empty", resp.TargetName)
	}
	if !strings.Contains(resp.Problem, "Sound Blaster") {
		t.Errorf("expected the device list in the problem text, got %q", resp.Problem)
	}
}

func TestShakerRunPlaysRamp(t *testing.T) {
	p := &stubShaker{devs: shakerDevs()}
	h := shakerServer(t, p)
	w := do(t, h, "POST", "/api/shaker", `{"maxLevel":0.1}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	var resp shakerRunResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	// 2%, 5%, 10% qualify under a 0.1 max.
	if resp.Played != 3 {
		t.Errorf("played %d steps, want 3", resp.Played)
	}
	if plays, _ := p.counts(); plays != 3 {
		t.Errorf("provider saw %d plays, want 3", plays)
	}
	if resp.Aborted {
		t.Error("run reported as aborted")
	}
}

// TestShakerRunRejectsExcessiveLevel is the server-side half of the safety
// posture: the page offers a capped menu, but the body is what actually
// reaches the hardware.
func TestShakerRunRejectsExcessiveLevel(t *testing.T) {
	for _, body := range []string{`{"maxLevel":1}`, `{"maxLevel":0.9}`, `{"maxLevel":0}`, `{"maxLevel":-1}`} {
		p := &stubShaker{devs: shakerDevs()}
		h := shakerServer(t, p)
		w := do(t, h, "POST", "/api/shaker", body)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
		if plays, _ := p.counts(); plays != 0 {
			t.Errorf("%s: played %d buffers, want 0", body, plays)
		}
	}
}

func TestShakerRunRefusesWithoutTarget(t *testing.T) {
	p := &stubShaker{devs: []shaker.Device{{ID: 0, Name: "Speakers (Sound Blaster X4)"}}}
	h := shakerServer(t, p)
	w := do(t, h, "POST", "/api/shaker", `{"maxLevel":0.1}`)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if plays, _ := p.counts(); plays != 0 {
		t.Errorf("played %d buffers with no matching device, want 0", plays)
	}
}

// TestShakerStopWorksDuringARun is the property the whole in-process design
// exists for: a stop that queued behind the run it is stopping would be no
// stop at all.
func TestShakerStopWorksDuringARun(t *testing.T) {
	p := &stubShaker{devs: shakerDevs(), block: make(chan struct{})}
	h := shakerServer(t, p)

	runDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { runDone <- do(t, h, "POST", "/api/shaker", `{"maxLevel":0.1}`) }()

	// Wait for the run to be inside Play and holding the lock.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if plays, _ := p.counts(); plays > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run never reached playback")
		}
		time.Sleep(time.Millisecond)
	}

	w := do(t, h, "POST", "/api/shaker/stop", "")
	if w.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200 while a run is in flight", w.Code)
	}
	if _, stops := p.counts(); stops != 1 {
		t.Errorf("provider saw %d stops, want 1", stops)
	}

	close(p.block)
	runResp := <-runDone

	var resp shakerRunResponse
	json.Unmarshal(runResp.Body.Bytes(), &resp)
	if !resp.Aborted {
		t.Error("run should report itself aborted after a stop")
	}
	// The abort must also prevent the remaining steps from starting.
	if plays, _ := p.counts(); plays != 1 {
		t.Errorf("played %d buffers after the stop, want 1", plays)
	}
}

// TestShakerRunRejectsConcurrentRun: a queued ramp would start the seat moving
// some time after the click that asked for it.
func TestShakerRunRejectsConcurrentRun(t *testing.T) {
	p := &stubShaker{devs: shakerDevs(), block: make(chan struct{})}
	h := shakerServer(t, p)

	go do(t, h, "POST", "/api/shaker", `{"maxLevel":0.1}`)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if plays, _ := p.counts(); plays > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first run never reached playback")
		}
		time.Sleep(time.Millisecond)
	}

	w := do(t, h, "POST", "/api/shaker", `{"maxLevel":0.1}`)
	if w.Code != http.StatusConflict {
		t.Errorf("second run status = %d, want 409", w.Code)
	}
	close(p.block)
}

// TestShakerUnavailableIsNotAnError checks the nil-provider contract the rest
// of the package follows: 501 lets the page grey the card out instead of
// showing a failure the user cannot act on.
func TestShakerUnavailableReports501(t *testing.T) {
	h := shakerServer(t, nil)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/shaker", ""},
		{"POST", "/api/shaker", `{"maxLevel":0.1}`},
		{"POST", "/api/shaker/stop", ""},
	} {
		w := do(t, h, tc.method, tc.path, tc.body)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status = %d, want 501", tc.method, tc.path, w.Code)
		}
	}
}

func TestShakerRunBadBody(t *testing.T) {
	p := &stubShaker{devs: shakerDevs()}
	h := shakerServer(t, p)
	w := do(t, h, "POST", "/api/shaker", `{"maxLevel":`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if plays, _ := p.counts(); plays != 0 {
		t.Errorf("played %d buffers on a malformed body, want 0", plays)
	}
}
