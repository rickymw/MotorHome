package main

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/shaker"
)

type fakePlayer struct {
	mu      sync.Mutex
	devs    []shaker.Device
	devErr  error
	playErr error
	plays   []fakePlay
	onPlay  func(p *fakePlayer)
	stops   int
}

type fakePlay struct {
	deviceID int
	bytes    int
}

func (f *fakePlayer) Devices() ([]shaker.Device, error) {
	if f.devErr != nil {
		return nil, f.devErr
	}
	return f.devs, nil
}

func (f *fakePlayer) Play(deviceID int, pcm []byte, _ shaker.Format) error {
	f.mu.Lock()
	f.plays = append(f.plays, fakePlay{deviceID, len(pcm)})
	f.mu.Unlock()
	if f.onPlay != nil {
		f.onPlay(f)
	}
	return f.playErr
}

func (f *fakePlayer) Stop() {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
}

func (f *fakePlayer) playCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.plays)
}

func fakeOutputs() []shaker.Device {
	return []shaker.Device{
		{ID: 0, Name: "Speakers (Razer Clio Surround)", Channels: 2},
		{ID: 1, Name: "Headphones (KT USB Audio)", Channels: 2},
		{ID: 2, Name: "Speakers (Sound Blaster X4)", Channels: 2},
	}
}

// runShaker drives RunShaker with a fake player, so no sound is made and no
// real time passes between steps.
func runShaker(t *testing.T, p shaker.Player, args ...string) (string, string, int) {
	t.Helper()

	oldOut, oldErr, oldNew, oldSleep := shakerOut, shakerErrOut, newShakerPlayer, shakerSleep
	t.Cleanup(func() {
		shakerOut, shakerErrOut, newShakerPlayer, shakerSleep = oldOut, oldErr, oldNew, oldSleep
	})

	var out, errBuf bytes.Buffer
	shakerOut, shakerErrOut = &out, &errBuf
	newShakerPlayer = func() shaker.Player { return p }
	shakerSleep = func(time.Duration) {}

	code := RunShaker(args)
	return out.String(), errBuf.String(), code
}

// TestRunShakerDefaultActionMakesNoSound: a bare `motorhome shaker` typed to
// see what the command does must not be what starts the seat moving.
func TestRunShakerDefaultActionMakesNoSound(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	out, _, code := runShaker(t, p)

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if p.playCount() != 0 {
		t.Errorf("the default action played %d buffers, want 0", p.playCount())
	}
	if !strings.Contains(out, "KT USB Audio") {
		t.Errorf("expected the device list, got:\n%s", out)
	}
	if !strings.Contains(out, "would play to") {
		t.Errorf("expected the resolved target disclosed, got:\n%s", out)
	}
}

func TestRunShakerDevicesReportsMissingTarget(t *testing.T) {
	p := &fakePlayer{devs: []shaker.Device{{ID: 0, Name: "Speakers (Sound Blaster X4)"}}}
	out, _, code := runShaker(t, p, "devices")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "would refuse to run") {
		t.Errorf("expected the absent transducer called out, got:\n%s", out)
	}
}

func TestRunShakerTestRampsUp(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	out, _, code := runShaker(t, p, "test")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if p.playCount() != len(shaker.DefaultSteps) {
		t.Fatalf("played %d buffers, want %d", p.playCount(), len(shaker.DefaultSteps))
	}
	for _, play := range p.plays {
		if play.deviceID != 1 {
			t.Errorf("played to device %d, want the KT USB Audio device (1)", play.deviceID)
		}
	}
	// Every step must be longer in bytes than nothing, and louder steps are the
	// same length — so assert the ramp through the rendered levels instead.
	if !strings.Contains(out, "2%") || !strings.Contains(out, "50%") {
		t.Errorf("expected the ramp levels in the output, got:\n%s", out)
	}
	if !strings.Contains(out, "Ctrl-C") {
		t.Errorf("expected the abort instruction before playback, got:\n%s", out)
	}
}

// TestRunShakerNeverFallsBackToDefaultDevice: playing a 40 Hz tone into
// whatever device happened to be first is the failure this package exists to
// avoid, so an unmatched name must stop the command.
func TestRunShakerNeverFallsBackToDefaultDevice(t *testing.T) {
	p := &fakePlayer{devs: []shaker.Device{{ID: 0, Name: "Speakers (Sound Blaster X4)"}}}
	_, errOut, code := runShaker(t, p, "test")

	if code == 0 {
		t.Error("expected a non-zero exit when the transducer is not found")
	}
	if p.playCount() != 0 {
		t.Errorf("played %d buffers with no matching device, want 0", p.playCount())
	}
	if !strings.Contains(errOut, "no audio output device matches") {
		t.Errorf("expected a device-resolution error, got:\n%s", errOut)
	}
}

func TestRunShakerRefusesAmbiguousDevice(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	_, errOut, code := runShaker(t, p, "test", "-device", "Speakers")

	if code == 0 {
		t.Error("expected a non-zero exit for an ambiguous device")
	}
	if p.playCount() != 0 {
		t.Errorf("played %d buffers without resolving a device, want 0", p.playCount())
	}
	if !strings.Contains(errOut, "matches 2 output devices") {
		t.Errorf("expected the ambiguity reported, got:\n%s", errOut)
	}
}

func TestRunShakerTone(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	out, _, code := runShaker(t, p, "tone", "-level", "0.1", "-secs", "1")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if p.playCount() != 1 {
		t.Fatalf("played %d buffers, want 1", p.playCount())
	}
	// 1s of 48kHz 16-bit stereo.
	if want := 48000 * 4; p.plays[0].bytes != want {
		t.Errorf("played %d bytes, want %d", p.plays[0].bytes, want)
	}
	if !strings.Contains(out, "1 tone played") {
		t.Errorf("expected a singular summary, got:\n%s", out)
	}
}

// TestRunShakerValidatesBeforePlaying: a bad flag must be a message, not one
// tone followed by a message.
func TestRunShakerValidatesBeforePlaying(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	_, errOut, code := runShaker(t, p, "tone", "-freq", "5000")

	if code == 0 {
		t.Error("expected a non-zero exit for an out-of-range frequency")
	}
	if p.playCount() != 0 {
		t.Errorf("played %d buffers before validating, want 0", p.playCount())
	}
	if !strings.Contains(errOut, "outside") {
		t.Errorf("expected a range error, got:\n%s", errOut)
	}
}

func TestRunShakerRejectsMaxBelowGentlestStep(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	_, errOut, code := runShaker(t, p, "test", "-max", "0.001")

	if code == 0 {
		t.Error("expected a non-zero exit when no step qualifies")
	}
	if p.playCount() != 0 {
		t.Errorf("played %d buffers, want 0", p.playCount())
	}
	if !strings.Contains(errOut, "gentlest step") {
		t.Errorf("expected an explanation, got:\n%s", errOut)
	}
}

func TestRunShakerPlaybackFailure(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs(), playErr: errors.New("device in use")}
	_, errOut, code := runShaker(t, p, "tone")

	if code == 0 {
		t.Error("expected a non-zero exit when playback fails")
	}
	if !strings.Contains(errOut, "device in use") {
		t.Errorf("expected the underlying error surfaced, got:\n%s", errOut)
	}
}

func TestRunShakerDeviceEnumerationFailure(t *testing.T) {
	p := &fakePlayer{devErr: errors.New("winmm exploded")}
	_, errOut, code := runShaker(t, p, "test")

	if code == 0 {
		t.Error("expected a non-zero exit when enumeration fails")
	}
	if !strings.Contains(errOut, "winmm exploded") {
		t.Errorf("expected the underlying error surfaced, got:\n%s", errOut)
	}
}

// TestRunShakerSummaryDoesNotClaimTheAmpIsHealthy guards the wording. Reporting
// a clean run as "working" is exactly the wrong thing to tell someone testing
// hardware they suspect is faulty.
func TestRunShakerSummaryDoesNotClaimTheAmpIsHealthy(t *testing.T) {
	p := &fakePlayer{devs: fakeOutputs()}
	out, _, _ := runShaker(t, p, "test")

	if !strings.Contains(out, "electrically sound") {
		t.Errorf("expected the summary to disclaim the amplifier's condition, got:\n%s", out)
	}
	if !strings.Contains(out, "only you can") {
		t.Errorf("expected the summary to say the transducer half is unverified, got:\n%s", out)
	}
}

func TestFormatLevel(t *testing.T) {
	cases := map[float64]string{
		0.02:   "2%",
		0.05:   "5%",
		0.5:    "50%",
		1:      "100%",
		0.125:  "12.5%",
		0.0001: "0.01%",
	}
	for level, want := range cases {
		if got := formatLevel(level); got != want {
			t.Errorf("formatLevel(%g) = %q, want %q", level, got, want)
		}
	}
}
