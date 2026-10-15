package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/joyid"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

// Hardware IDs of the built-in rig, which is the device list these tests use.
var (
	hwPedals    = joyid.HWID{VID: 0x30B7, PID: 0x1001}
	hwHandbrake = joyid.HWID{VID: 0x346E, PID: 0x001F}
	hwWheelbase = joyid.HWID{VID: 0x3670, PID: 0x0501}
)

type fakeJoyStore struct {
	assigned map[joyid.HWID]int
	visible  map[joyid.HWID]int
	sets     int
}

func (f *fakeJoyStore) Assigned() (map[joyid.HWID]int, error) {
	out := map[joyid.HWID]int{}
	for k, v := range f.assigned {
		out[k] = v
	}
	return out, nil
}
func (f *fakeJoyStore) Visible() (map[joyid.HWID]int, error) { return f.visible, nil }
func (f *fakeJoyStore) SetID(hw joyid.HWID, id int) error {
	f.assigned[hw] = id
	f.sets++
	return nil
}
func (f *fakeJoyStore) Notify() {}

// rigStore is the rig as found on 2026-10-14: everything visible but the pedals.
func rigStore() *fakeJoyStore {
	return &fakeJoyStore{
		assigned: map[joyid.HWID]int{hwHandbrake: 0, hwWheelbase: 3, hwPedals: 4},
		visible:  map[joyid.HWID]int{hwHandbrake: 0, hwWheelbase: 3},
	}
}

func connectedRig() *fakeUSBController {
	return &fakeUSBController{devs: []usbdev.Device{
		{Known: usbdev.Known{Alias: "pedals"}, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "handbrake"}, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "wheelbase"}, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "rim"}, State: usbdev.StateAbsent},
	}}
}

func withJoyFakes(t *testing.T, store joyid.Store, ctrl usbdev.Controller) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	oldOut, oldErr, oldStore, oldCtrl := joyOut, joyErrOut, newJoyStore, newUSBController
	t.Cleanup(func() { joyOut, joyErrOut, newJoyStore, newUSBController = oldOut, oldErr, oldStore, oldCtrl })
	var out, errBuf bytes.Buffer
	joyOut, joyErrOut = &out, &errBuf
	newJoyStore = func() joyid.Store { return store }
	newUSBController = func([]usbdev.Known) usbdev.Controller { return ctrl }
	return &out, &errBuf
}

func joyConfig(t *testing.T, pins map[string]int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher.config.json")
	if err := config.Save(path, config.Config{Driver: "Ricky Maw", JoyPins: pins}); err != nil {
		t.Fatal(err)
	}
	return path
}

func savedPins(t *testing.T, path string) map[string]int {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.JoyPins
}

func TestRunJoyListFlagsTheHiddenDevice(t *testing.T) {
	out, _ := withJoyFakes(t, rigStore(), connectedRig())
	if code := RunJoy(nil, joyConfig(t, nil)); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := out.String()
	for _, want := range []string{"pedals", "NO", "invisible to games", "(not connected)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// Listing must work from a bare exe, the same as usb.
func TestRunJoyListWithoutConfig(t *testing.T) {
	out, errBuf := withJoyFakes(t, rigStore(), connectedRig())
	if code := RunJoy([]string{"list"}, filepath.Join(t.TempDir(), "missing.json")); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	if !strings.Contains(out.String(), "handbrake") {
		t.Errorf("no listing:\n%s", out)
	}
}

func TestRunJoyPinSavesAndSwaps(t *testing.T) {
	store := rigStore()
	out, errBuf := withJoyFakes(t, store, connectedRig())
	path := joyConfig(t, nil)

	if code := RunJoy([]string{"pin", "pedals", "0"}, path); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	if savedPins(t, path)["pedals"] != 0 {
		t.Errorf("pin not saved: %v", savedPins(t, path))
	}
	if store.assigned[hwPedals] != 0 || store.assigned[hwHandbrake] != 4 {
		t.Errorf("registry = %v, want pedals 0 and handbrake 4", store.assigned)
	}
	if !strings.Contains(out.String(), "handbrake: ID 0 → 4") {
		t.Errorf("the displaced device was not reported:\n%s", out)
	}
}

// Pinning onto another pinned device's ID moves that pin to the ID being left,
// so the config stays valid and matches the registry swap.
func TestRunJoyPinMovesTheDisplacedPin(t *testing.T) {
	store := rigStore()
	out, errBuf := withJoyFakes(t, store, connectedRig())
	path := joyConfig(t, map[string]int{"handbrake": 0})

	if code := RunJoy([]string{"pin", "pedals", "0"}, path); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	pins := savedPins(t, path)
	if pins["pedals"] != 0 || pins["handbrake"] != 4 {
		t.Errorf("pins = %v, want pedals 0 and handbrake 4", pins)
	}
	if !strings.Contains(out.String(), "its pin moves to 4") {
		t.Errorf("the moved pin was not reported:\n%s", out)
	}
}

func TestRunJoyPinRefusals(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"pin", "pedals", "16"}, "0 to 15"},
		{[]string{"pin", "pedals", "x"}, "0 to 15"},
		{[]string{"pin", "nothing", "1"}, "no device matches"},
		// Substring matching both SIMAGIC devices must not pick one.
		{[]string{"pin", "simagic", "1"}, "be more specific"},
	}
	for _, c := range cases {
		store := rigStore()
		_, errBuf := withJoyFakes(t, store, connectedRig())
		path := joyConfig(t, nil)
		if code := RunJoy(c.args, path); code == 0 {
			t.Errorf("%v: exit 0, want a refusal", c.args)
		}
		if !strings.Contains(errBuf.String(), c.want) {
			t.Errorf("%v: stderr = %q, want %q", c.args, errBuf, c.want)
		}
		if store.sets != 0 {
			t.Errorf("%v: a refused pin wrote the registry", c.args)
		}
	}
}

// A pin has to outlive the process, so it needs a config to live in.
func TestRunJoyPinNeedsAConfig(t *testing.T) {
	store := rigStore()
	_, errBuf := withJoyFakes(t, store, connectedRig())
	if code := RunJoy([]string{"pin", "pedals", "0"}, filepath.Join(t.TempDir(), "missing.json")); code == 0 {
		t.Fatal("pinned with no config to save it in")
	}
	if !strings.Contains(errBuf.String(), "does not exist") || store.sets != 0 {
		t.Errorf("stderr = %q, sets = %d", errBuf, store.sets)
	}
}

func TestRunJoyUnpinKeepsTheID(t *testing.T) {
	store := rigStore()
	_, errBuf := withJoyFakes(t, store, connectedRig())
	path := joyConfig(t, map[string]int{"pedals": 4, "handbrake": 0})

	if code := RunJoy([]string{"unpin", "pedals"}, path); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	if _, still := savedPins(t, path)["pedals"]; still || savedPins(t, path)["handbrake"] != 0 {
		t.Errorf("pins = %v", savedPins(t, path))
	}
	if store.sets != 0 {
		t.Error("unpinning moved a device")
	}
}

// A pin for a device that was renamed away must still be clearable.
func TestRunJoyUnpinStalePinAndAll(t *testing.T) {
	_, _ = withJoyFakes(t, rigStore(), connectedRig())
	path := joyConfig(t, map[string]int{"oldname": 5, "pedals": 4})
	if code := RunJoy([]string{"unpin", "oldname"}, path); code != 0 {
		t.Fatal("could not clear a stale pin")
	}
	if code := RunJoy([]string{"unpin", "all"}, path); code != 0 || len(savedPins(t, path)) != 0 {
		t.Errorf("unpin all left %v", savedPins(t, path))
	}
	if code := RunJoy([]string{"unpin", "pedals"}, path); code == 0 {
		t.Error("unpinning something not pinned should fail")
	}
}

// The usage line puts -json last, and Go's flag parsing would otherwise stop
// at the alias and leave it as an operand — which is what the GUI tripped on.
func TestRunJoyJSONFlagAnywhere(t *testing.T) {
	out, errBuf := withJoyFakes(t, rigStore(), connectedRig())
	path := joyConfig(t, nil)
	if code := RunJoy([]string{"pin", "pedals", "0", "-json"}, path); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	var r joyid.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(r.Notes) == 0 || !strings.Contains(strings.Join(r.Notes, "\n"), "Pinned pedals to ID 0.") {
		t.Errorf("notes = %v", r.Notes)
	}
	if strings.Contains(out.String(), "\\u0026") || !strings.Contains(out.String(), "VID_30B7&PID_1001") {
		t.Errorf("hardware IDs were HTML-escaped:\n%s", out)
	}
}

func TestRunJoyApply(t *testing.T) {
	store := rigStore()
	out, _ := withJoyFakes(t, store, connectedRig())
	path := joyConfig(t, map[string]int{"pedals": 0, "gone": 7})

	if code := RunJoy([]string{"apply"}, path); code != 0 {
		t.Fatal("apply failed")
	}
	if store.assigned[hwPedals] != 0 {
		t.Errorf("pin not applied: %v", store.assigned)
	}
	if !strings.Contains(out.String(), `Pin for "gone" ignored`) {
		t.Errorf("a stale pin was skipped silently:\n%s", out)
	}

	out.Reset()
	RunJoy([]string{"apply"}, path)
	if !strings.Contains(out.String(), "already holds") {
		t.Errorf("second apply should report nothing to do:\n%s", out)
	}
}

func TestJoyPreflightAppliesPinsAndWarns(t *testing.T) {
	store := rigStore()
	out, errBuf := withJoyFakes(t, store, connectedRig())

	joyPreflight(config.Config{JoyPins: map[string]int{"wheelbase": 0}})

	if store.assigned[hwWheelbase] != 0 {
		t.Errorf("pin not applied before start: %v", store.assigned)
	}
	if !strings.Contains(out.String(), "[joy] wheelbase: ID 3 → 0") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errBuf.String(), "pedals is connected but games cannot see it") {
		t.Errorf("hidden pedals not warned about: %q", errBuf)
	}
	if strings.Contains(errBuf.String(), "rim") {
		t.Error("warned about an unplugged device")
	}
}

// With no pins and everything visible, start must print nothing.
func TestJoyPreflightIsSilentWhenAllIsWell(t *testing.T) {
	store := rigStore()
	store.visible[hwPedals] = 4
	out, errBuf := withJoyFakes(t, store, connectedRig())
	joyPreflight(config.Config{})
	if out.Len() != 0 || errBuf.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q", out, errBuf)
	}
}
