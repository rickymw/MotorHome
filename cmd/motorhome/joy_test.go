package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	hid      []joyid.HIDInterface
}

func (f *fakeJoyStore) Assigned() (map[joyid.HWID]int, error)        { return f.assigned, nil }
func (f *fakeJoyStore) Visible() (map[joyid.HWID]int, error)         { return f.visible, nil }
func (f *fakeJoyStore) HIDInterfaces() ([]joyid.HIDInterface, error) { return f.hid, nil }

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

func joyConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher.config.json")
	if err := config.Save(path, config.Config{Driver: "Ricky Maw"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunJoyFlagsTheHiddenDevice(t *testing.T) {
	store := rigStore()
	store.hid = hidList(75, 13)
	out, _ := withJoyFakes(t, store, connectedRig())
	if code := RunJoy(nil, joyConfig(t)); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := out.String()
	for _, want := range []string{"pedals", "NO", "invisible to games", "HID interfaces: 75", "(not connected)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// `joy list` is accepted as a synonym, for anyone with the old habit.
func TestRunJoyListSynonym(t *testing.T) {
	out, _ := withJoyFakes(t, rigStore(), connectedRig())
	bare := RunJoy(nil, joyConfig(t))
	first := out.String()
	out.Reset()
	if code := RunJoy([]string{"list"}, joyConfig(t)); code != 0 || bare != 0 || out.String() != first {
		t.Errorf("`joy list` differs from `joy`")
	}
}

// It must work from a bare exe, the same as usb.
func TestRunJoyWithoutConfig(t *testing.T) {
	out, errBuf := withJoyFakes(t, rigStore(), connectedRig())
	if code := RunJoy(nil, filepath.Join(t.TempDir(), "missing.json")); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	if !strings.Contains(out.String(), "handbrake") {
		t.Errorf("no listing:\n%s", out)
	}
}

// The pinning subcommands were removed; asking for one is a usage error, not a
// silent no-op that reads as "pinned".
func TestRunJoyRejectsRemovedSubcommands(t *testing.T) {
	for _, args := range [][]string{{"pin", "pedals", "0"}, {"unpin", "pedals"}, {"apply"}} {
		_, errBuf := withJoyFakes(t, rigStore(), connectedRig())
		if code := RunJoy(args, joyConfig(t)); code == 0 {
			t.Errorf("%v: exit 0, want a usage error", args)
		}
		if !strings.Contains(errBuf.String(), "Usage: motorhome joy") {
			t.Errorf("%v: stderr = %q", args, errBuf)
		}
	}
}

func TestRunJoyJSON(t *testing.T) {
	store := rigStore()
	store.hid = hidList(62, 0)
	out, errBuf := withJoyFakes(t, store, connectedRig())
	if code := RunJoy([]string{"-json"}, joyConfig(t)); code != 0 {
		t.Fatalf("exit = %d: %s", code, errBuf)
	}
	var r joyid.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if r.HIDInterfaces != 62 || r.HIDLimit != joyid.HIDLimit || r.Hidden() != 1 {
		t.Errorf("report = %+v", r)
	}
	if strings.Contains(out.String(), "\\u0026") || !strings.Contains(out.String(), "VID_30B7&PID_1001") {
		t.Errorf("hardware IDs were HTML-escaped:\n%s", out)
	}
}

func TestJoyPreflightWarnsAndNamesTheLimit(t *testing.T) {
	store := rigStore()
	store.hid = hidList(75, 13)
	out, errBuf := withJoyFakes(t, store, connectedRig())
	joyPreflight(config.Config{})

	if !strings.Contains(errBuf.String(), "pedals is connected but games cannot see it") {
		t.Errorf("hidden pedals not warned about: %q", errBuf)
	}
	if !strings.Contains(errBuf.String(), "75 HID interfaces") {
		t.Errorf("the likely cause was not named: %q", errBuf)
	}
	if strings.Contains(errBuf.String(), "rim") {
		t.Error("warned about an unplugged device")
	}
	if out.Len() != 0 {
		t.Errorf("preflight wrote to stdout: %q", out)
	}
}

// With everything visible, start must print nothing — even close to the limit,
// where a line every session would soon be ignored.
func TestJoyPreflightIsSilentWhenAllIsWell(t *testing.T) {
	store := rigStore()
	store.visible[hwPedals] = 4
	store.hid = hidList(62, 0)
	out, errBuf := withJoyFakes(t, store, connectedRig())
	joyPreflight(config.Config{})
	if out.Len() != 0 || errBuf.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q", out, errBuf)
	}
}

// hidList makes n interfaces, the first `dongle` of them belonging to one
// 13-interface-style dongle and the rest to distinct devices.
func hidList(n, dongle int) []joyid.HIDInterface {
	out := make([]joyid.HIDInterface, 0, n)
	for i := 0; i < n; i++ {
		if i < dongle {
			out = append(out, joyid.HIDInterface{Device: "VID_28DE&PID_1304", Name: "Steam Controller Puck"})
		} else {
			out = append(out, joyid.HIDInterface{Device: fmt.Sprintf("VID_%04X&PID_0001", i)})
		}
	}
	return out
}

func TestRunJoyJSONBreaksDownHIDUse(t *testing.T) {
	store := rigStore()
	store.hid = hidList(75, 13)
	out, _ := withJoyFakes(t, store, connectedRig())
	if code := RunJoy([]string{"-json"}, joyConfig(t)); code != 0 {
		t.Fatal("exit != 0")
	}
	var r joyid.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.HIDInterfaces != 75 || len(r.HIDUsers) == 0 || r.HIDUsers[0].Interfaces != 13 || r.HIDUsers[0].Name != "Steam Controller Puck" {
		t.Errorf("breakdown = %+v", r.HIDUsers)
	}
}
