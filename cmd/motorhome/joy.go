package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/joyid"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

// joyOut and joyErrOut are the joy subcommand's sinks; tests point them at
// buffers. newJoyStore is swappable for the same reason newUSBController is:
// tests have no registry to write to.
var (
	joyOut      io.Writer = os.Stdout
	joyErrOut   io.Writer = os.Stderr
	newJoyStore           = joyid.NewStore
)

func joyPrintf(format string, a ...any) { fmt.Fprintf(joyOut, format, a...) }
func joyErrf(format string, a ...any)   { fmt.Fprintf(joyErrOut, format+"\n", a...) }

func isJoyAction(s string) bool {
	switch s {
	case "list", "pin", "unpin", "apply":
		return true
	}
	return false
}

// RunJoy lists the rig's joystick IDs and pins devices to chosen ones,
// returning the process exit code.
//
// Like usb, it is dispatched ahead of config.Load and reads the config itself:
// listing has to work from a bare exe, where the built-in device list stands
// in. Pinning writes the config, so that half needs a real one.
//
// Nothing here needs elevation — the IDs live under HKCU.
//
// The GUI calls this through a re-exec (`joy -json`) rather than in-process.
// That is not only the usual "a server must not die on os.Exit" reason: which
// controllers a program can see is fixed when it starts, so only a fresh
// process answers "would a game launched now see the pedals?".
func RunJoy(args []string, cfgPath string) int {
	action := "list"
	rest := args
	if len(args) > 0 && isJoyAction(args[0]) {
		action, rest = args[0], args[1:]
	}

	fs := flag.NewFlagSet("joy "+action, flag.ContinueOnError)
	fs.SetOutput(joyErrOut)
	asJSON := fs.Bool("json", false, "print the ID table as JSON")
	fs.Usage = func() {
		fmt.Fprintln(joyErrOut, "Usage: motorhome joy [list] [-json]")
		fmt.Fprintln(joyErrOut, "       motorhome joy pin <alias> <id 0-15> [-json]")
		fmt.Fprintln(joyErrOut, "       motorhome joy unpin <alias|all> [-json]")
		fmt.Fprintln(joyErrOut, "       motorhome joy apply [-json]")
		fs.PrintDefaults()
	}
	// Go's flag parsing stops at the first positional argument, so
	// `joy pin pedals 0 -json` would leave -json as a third operand. Pulled out
	// by hand so the flag works wherever it is typed — the usage line puts it
	// last, and that is where people put it.
	var operands []string
	for _, a := range rest {
		if a == "-json" || a == "--json" {
			*asJSON = true
			continue
		}
		operands = append(operands, a)
	}
	if err := fs.Parse(operands); err != nil {
		return 2
	}

	cfg, cfgErr := config.Load(cfgPath)
	if cfgErr != nil && !os.IsNotExist(cfgErr) {
		joyErrf("warning: using the built-in device list (%v)", cfgErr)
	}
	known := cfg.KnownUSBDevices()
	devs := joyRigDevices(known)
	store := newJoyStore()

	var notes []string
	switch action {
	case "pin":
		if fs.NArg() != 2 {
			fs.Usage()
			return 2
		}
		msgs, err := joyPin(&cfg, cfgErr, cfgPath, devs, store, fs.Arg(0), fs.Arg(1))
		if err != nil {
			joyErrf("joy pin: %v", err)
			return 1
		}
		notes = append(notes, msgs...)
	case "unpin":
		if fs.NArg() != 1 {
			fs.Usage()
			return 2
		}
		msgs, err := joyUnpin(&cfg, cfgErr, cfgPath, fs.Arg(0))
		if err != nil {
			joyErrf("joy unpin: %v", err)
			return 1
		}
		notes = append(notes, msgs...)
	}

	// pin re-applies every pin, not just the new one: a pin onto an occupied
	// ID swaps the occupant, and if that occupant was itself pinned the config
	// has just moved it too.
	if action == "pin" || action == "apply" {
		msgs, changed, err := joyApply(cfg, devs, store)
		if err != nil {
			joyErrf("joy %s: %v", action, err)
			return 1
		}
		notes = append(notes, msgs...)
		if action == "apply" && !changed {
			notes = append(notes, "Every pin already holds — nothing changed.")
		}
	}

	report, err := joyReport(cfg, devs, store)
	if err != nil {
		joyErrf("joy: %v", err)
		return 1
	}
	report.Notes = notes

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

// joyRigDevices pairs the usb device list with whether each is plugged in.
//
// Enumeration failing is not fatal: the IDs can still be read and changed, the
// table just cannot say which devices are connected, so they are reported as
// not connected rather than as hidden from games — the safe way to be wrong.
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

// joyFind resolves a command-line target to one rig device: an exact alias, or
// a substring matching exactly one alias or name. Like usb and pb show, it
// refuses to guess — pinning the wrong device moves a game's bindings onto it.
func joyFind(devs []joyid.Device, target string) (joyid.Device, error) {
	t := strings.ToLower(strings.TrimSpace(target))
	if t == "" {
		return joyid.Device{}, errors.New("no device named")
	}
	for _, d := range devs {
		if strings.ToLower(d.Alias) == t {
			return d, nil
		}
	}
	var hits []joyid.Device
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Alias), t) || strings.Contains(strings.ToLower(d.Name), t) {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		var names []string
		for _, d := range devs {
			names = append(names, d.Alias)
		}
		return joyid.Device{}, fmt.Errorf("no device matches %q\nknown devices: %s", target, strings.Join(names, ", "))
	default:
		var names []string
		for _, d := range hits {
			names = append(names, d.Alias)
		}
		return joyid.Device{}, fmt.Errorf("%q matches %s — be more specific", target, strings.Join(names, ", "))
	}
}

// joyRequireConfig turns a config load failure into the reason a pin cannot be
// saved. Listing tolerates a missing config; changing a pin cannot, because
// the pin has to outlive this process to mean anything.
func joyRequireConfig(cfgErr error, cfgPath string) error {
	if cfgErr == nil {
		return nil
	}
	if os.IsNotExist(cfgErr) {
		return fmt.Errorf("pins are saved in %s, which does not exist", cfgPath)
	}
	return fmt.Errorf("cannot save pins — the config does not load: %v", cfgErr)
}

// joyPin records a pin and saves the config. Applying it is the caller's job.
//
// If another device is already pinned to the requested ID, its pin moves to the
// ID this device currently holds — the same swap the registry write is about to
// make — so the two stay consistent and the config stays valid (pins must be
// distinct). If this device holds no ID to trade, the other pin is dropped, and
// either way the user is told.
func joyPin(cfg *config.Config, cfgErr error, cfgPath string, devs []joyid.Device, store joyid.Store, target, idArg string) ([]string, error) {
	if err := joyRequireConfig(cfgErr, cfgPath); err != nil {
		return nil, err
	}
	dev, err := joyFind(devs, target)
	if err != nil {
		return nil, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(idArg))
	if err != nil || id < 0 || id > joyid.MaxID {
		return nil, fmt.Errorf("ID must be a whole number from 0 to %d, got %q", joyid.MaxID, idArg)
	}

	if cfg.JoyPins == nil {
		cfg.JoyPins = map[string]int{}
	}
	var msgs []string

	for other, otherID := range cfg.JoyPins {
		if otherID != id || other == dev.Alias {
			continue
		}
		current := currentID(store, dev.HW)
		if current != joyid.None && current != id && !pinnedTo(cfg.JoyPins, current, other) {
			cfg.JoyPins[other] = current
			msgs = append(msgs, fmt.Sprintf("%s was pinned to %d; its pin moves to %d, the ID %s is leaving.", other, id, current, dev.Alias))
		} else {
			delete(cfg.JoyPins, other)
			msgs = append(msgs, fmt.Sprintf("%s was pinned to %d and has been unpinned.", other, id))
		}
	}

	cfg.JoyPins[dev.Alias] = id
	if err := config.Save(cfgPath, *cfg); err != nil {
		return nil, fmt.Errorf("saving the pin: %w", err)
	}
	return append(msgs, fmt.Sprintf("Pinned %s to ID %d.", dev.Alias, id)), nil
}

// currentID reads one device's current ID, None when it has none or the read fails.
func currentID(store joyid.Store, hw joyid.HWID) int {
	assigned, err := store.Assigned()
	if err != nil {
		return joyid.None
	}
	if id, ok := assigned[hw]; ok {
		return id
	}
	return joyid.None
}

func pinnedTo(pins map[string]int, id int, except string) bool {
	for alias, v := range pins {
		if v == id && alias != except {
			return true
		}
	}
	return false
}

// joyUnpin removes pins and saves the config. It changes no ID: unpinning means
// "stop holding it there", not "move it", and the device keeps the ID it has
// until Windows next reshuffles.
//
// The alias is matched against the pins, not the device list, so a pin left
// behind by a renamed or removed device can still be cleared.
func joyUnpin(cfg *config.Config, cfgErr error, cfgPath, target string) ([]string, error) {
	if err := joyRequireConfig(cfgErr, cfgPath); err != nil {
		return nil, err
	}
	t := strings.TrimSpace(target)
	if strings.EqualFold(t, "all") {
		if len(cfg.JoyPins) == 0 {
			return []string{"Nothing is pinned."}, nil
		}
		n := len(cfg.JoyPins)
		cfg.JoyPins = nil
		if err := config.Save(cfgPath, *cfg); err != nil {
			return nil, fmt.Errorf("saving: %w", err)
		}
		return []string{fmt.Sprintf("Unpinned %d device(s). Their IDs are unchanged.", n)}, nil
	}

	for alias := range cfg.JoyPins {
		if strings.EqualFold(alias, t) {
			delete(cfg.JoyPins, alias)
			if len(cfg.JoyPins) == 0 {
				cfg.JoyPins = nil
			}
			if err := config.Save(cfgPath, *cfg); err != nil {
				return nil, fmt.Errorf("saving: %w", err)
			}
			return []string{fmt.Sprintf("Unpinned %s. It keeps its current ID until Windows reassigns it.", alias)}, nil
		}
	}
	return nil, fmt.Errorf("%q is not pinned", target)
}

// joyPinsFor converts the config's pins to registry terms. A pin whose alias no
// longer names a device is skipped with a note rather than failing the rest.
func joyPinsFor(cfg config.Config, devs []joyid.Device) (pins []joyid.Pin, msgs []string) {
	byAlias := map[string]joyid.Device{}
	for _, d := range devs {
		byAlias[strings.ToLower(d.Alias)] = d
	}
	aliases := make([]string, 0, len(cfg.JoyPins))
	for a := range cfg.JoyPins {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		d, ok := byAlias[strings.ToLower(a)]
		if !ok {
			msgs = append(msgs, fmt.Sprintf("Pin for %q ignored — no device has that alias. Clear it with `motorhome joy unpin %s`.", a, a))
			continue
		}
		pins = append(pins, joyid.Pin{HW: d.HW, ID: cfg.JoyPins[a]})
	}
	return pins, msgs
}

// joyApply writes every pin and describes what moved.
func joyApply(cfg config.Config, devs []joyid.Device, store joyid.Store) (msgs []string, changed bool, err error) {
	pins, msgs := joyPinsFor(cfg, devs)
	changes, unseen, err := joyid.Apply(store, pins)
	if err != nil {
		return msgs, false, err
	}

	names := map[joyid.HWID]string{}
	for _, d := range devs {
		names[d.HW] = d.Alias
	}
	for _, c := range changes {
		msgs = append(msgs, joyid.DescribeChange(c, names))
	}
	for _, hw := range unseen {
		who := names[hw]
		if who == "" {
			who = hw.Key()
		}
		msgs = append(msgs, fmt.Sprintf("%s has never been plugged in on this account, so it has no ID to change yet.", who))
	}
	if len(changes) > 0 {
		msgs = append(msgs, "A game that is already running keeps the old order until it is restarted.")
	}
	return msgs, len(changes) > 0, nil
}

func joyReport(cfg config.Config, devs []joyid.Device, store joyid.Store) (joyid.Report, error) {
	assigned, err := store.Assigned()
	if err != nil {
		return joyid.Report{}, err
	}
	visible, err := store.Visible()
	if err != nil {
		return joyid.Report{}, err
	}
	return joyid.BuildReport(devs, assigned, visible, cfg.JoyPins), nil
}

// joyPreflight runs ahead of `motorhome start`: it re-applies the pins and
// warns about any connected controller a game would not see.
//
// Both halves are why it belongs here and not only in the joy subcommand.
// Windows reshuffles IDs when devices come and go, so a pin applied once would
// quietly lapse; and a controller hidden from games is invisible until a sim is
// already loaded and the pedals do nothing — the start of a session is the last
// cheap moment to say so. It prints nothing when there is nothing to say, and
// never blocks the start: a failure here is a warning, not a reason to keep the
// rig from launching.
func joyPreflight(cfg config.Config) {
	devs := joyRigDevices(cfg.KnownUSBDevices())
	store := newJoyStore()

	if len(cfg.JoyPins) > 0 {
		msgs, _, err := joyApply(cfg, devs, store)
		if err != nil {
			joyErrf("warning: could not apply joystick pins: %v", err)
		}
		for _, m := range msgs {
			joyPrintf("[joy] %s\n", m)
		}
	}

	visible, err := store.Visible()
	if err != nil {
		joyErrf("warning: could not check which controllers games can see: %v", err)
		return
	}
	for _, d := range devs {
		if _, ok := visible[d.HW]; d.Connected && !ok {
			joyErrf("warning: %s is connected but games cannot see it — run `motorhome joy` for details", d.Alias)
		}
	}
}
