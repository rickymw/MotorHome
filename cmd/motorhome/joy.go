package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/joyid"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

// joyOut and joyErrOut are the joy subcommand's sinks; tests point them at
// buffers. newJoyStore is swappable for the same reason newUSBController is:
// tests have no DirectInput to ask.
var (
	joyOut      io.Writer = os.Stdout
	joyErrOut   io.Writer = os.Stderr
	newJoyStore           = joyid.NewStore
)

func joyErrf(format string, a ...any) { fmt.Fprintf(joyErrOut, format+"\n", a...) }

// RunJoy reports which rig controllers a game launched now would see, and the
// machine's HID interface count, returning the process exit code.
//
// It only reads. It used to pin devices to joystick IDs as well, but iRacing
// and MAIRA bind by device GUID rather than by slot, so pinning fixed nothing
// this rig had and was removed.
//
// Like usb, it is dispatched ahead of config.Load and reads the config itself:
// it has to work from a bare exe, where the built-in device list stands in.
//
// The GUI calls this through a re-exec (`joy -json`) rather than in-process,
// because which controllers a program can see is fixed when it starts — only a
// fresh process answers "would a game launched now see the pedals?".
func RunJoy(args []string, cfgPath string) int {
	rest := args
	if len(rest) > 0 && rest[0] == "list" {
		rest = rest[1:]
	}

	fs := flag.NewFlagSet("joy", flag.ContinueOnError)
	fs.SetOutput(joyErrOut)
	asJSON := fs.Bool("json", false, "print the table as JSON")
	fs.Usage = func() {
		fmt.Fprintln(joyErrOut, "Usage: motorhome joy [-json]")
		fmt.Fprintln(joyErrOut, "Lists each rig controller, whether games can see it, and the HID interface count.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	devs := joyRigDevices(joyKnownDevices(cfgPath))
	report, err := joyReport(devs, newJoyStore())
	if err != nil {
		joyErrf("joy: %v", err)
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(joyOut)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(report); err != nil {
			joyErrf("joy: %v", err)
			return 1
		}
		return 0
	}
	joyid.FormatReport(joyOut, report)
	return 0
}

// joyKnownDevices reads the usb device list from the config. A missing config
// means the built-in list; a broken one is reported and does the same.
func joyKnownDevices(cfgPath string) []usbdev.Known {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if !os.IsNotExist(err) {
			joyErrf("warning: using the built-in device list (%v)", err)
		}
		return nil
	}
	return cfg.KnownUSBDevices()
}

// joyRigDevices pairs the usb device list with whether each is plugged in.
//
// Enumeration failing is not fatal: DirectInput can still be asked, the table
// just cannot say which devices are connected, so they are reported as not
// connected rather than as hidden from games — the safe way to be wrong.
func joyRigDevices(known []usbdev.Known) []joyid.Device {
	resolved := usbdev.ResolveKnown(known)
	connected := map[string]bool{}
	if found, err := newUSBController(known).Enumerate(); err != nil {
		joyErrf("warning: cannot tell which devices are connected (%v)", err)
	} else {
		for _, d := range found {
			connected[d.Alias] = d.State == usbdev.StateEnabled
		}
	}

	out := make([]joyid.Device, 0, len(resolved))
	for _, k := range resolved {
		out = append(out, joyid.Device{
			Alias:     k.Alias,
			Name:      k.Name,
			HW:        joyid.HWID{VID: k.VID, PID: k.PID},
			Connected: connected[k.Alias],
		})
	}
	return out
}

func joyReport(devs []joyid.Device, store joyid.Store) (joyid.Report, error) {
	assigned, err := store.Assigned()
	if err != nil {
		return joyid.Report{}, err
	}
	visible, err := store.Visible()
	if err != nil {
		return joyid.Report{}, err
	}
	r := joyid.BuildReport(devs, assigned, visible)
	// A failed count only loses the explanation, not the table.
	if n, err := store.HIDInterfaces(); err == nil {
		r.HIDInterfaces = n
	}
	return r, nil
}

// joyPreflight runs ahead of `motorhome start` and warns about any connected
// rig controller a game would not see.
//
// A hidden controller is otherwise invisible until a sim is loaded and the
// pedals do nothing; the start of a session is the last cheap moment to say so.
// It prints nothing when all is well and never blocks the start — a failure
// here is a warning, not a reason to keep the rig from launching.
func joyPreflight(cfg config.Config) {
	r, err := joyReport(joyRigDevices(cfg.KnownUSBDevices()), newJoyStore())
	if err != nil {
		joyErrf("warning: could not check which controllers games can see: %v", err)
		return
	}
	for _, row := range r.Devices {
		if row.Hidden {
			joyErrf("warning: %s is connected but games cannot see it — run `motorhome joy` for details", row.Alias)
		}
	}
	// Deliberately no "close to the limit" note here, unlike the joy table:
	// this rig sits a couple below the limit, and a line on every start that
	// asks for nothing would soon be read as noise — including the day it
	// matters. Hidden controllers are the actionable case.
	if r.Hidden() > 0 && r.HIDInterfaces > 0 {
		joyErrf("warning: this PC has %d HID interfaces and DirectInput lists only about the first %d — unplug an input gadget you do not use", r.HIDInterfaces, r.HIDLimit)
	}
}
