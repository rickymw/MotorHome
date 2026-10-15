// Package joyid reads and changes the joystick IDs Windows hands out to game
// controllers, and keeps chosen devices pinned to chosen IDs.
//
// A game that uses the legacy joystick API sees controllers as numbered slots,
// 0–15. Windows assigns those numbers itself and reshuffles them when devices
// come and go, which moves a game's bindings onto the wrong device — or, when
// the list is damaged, leaves a connected device with no usable slot at all.
// That second case is what this exists for: on 2026-10-14 the Heusinkveld
// pedals worked in their own software but were invisible to iRacing and to
// joy.cpl, and nothing on the machine said so.
//
// The ID lives in the per-user DirectInput cache, one value per device:
//
//	HKCU\System\CurrentControlSet\Control\MediaProperties\PrivateProperties\
//	    DirectInput\VID_xxxx&PID_xxxx\Calibration\<n>   "Joystick Id" (REG_BINARY, 4 bytes LE)
//
// Changing that value is the whole mechanism — it is what JoyIDs does. Verified
// on this rig: swapping two devices' values changed the order a freshly started
// process saw, the change survived the device being unplugged and replugged,
// and Windows rewrote its own slot table (MediaResources\Joystick\DINPUT.DLL\
// CurrentJoystickSettings) to match without being touched. So that table is
// left alone.
//
// Everything here except the Store implementation is pure, so the planning —
// which device moves where when one is pinned onto an occupied ID — is tested
// without a registry.
package joyid

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// MaxID is the highest joystick ID. The legacy joystick API addresses 16
// slots, so a pin outside 0–15 could never be honoured.
const MaxID = 15

// HIDLimit is roughly how many HID interfaces DirectInput will list. Past it,
// devices later in the system's HID interface order are silently left out —
// EnumDevices still succeeds, they are simply not in the result.
//
// Measured, not documented: on 2026-10-14 this rig had 75 HID interfaces;
// DirectInput listed the wheelbase at position 59 and dropped the pedals at 73,
// and taking the count to 62 (unplugging a 13-interface Steam Controller
// dongle) brought the pedals back. 64 is the value that fits, so it is an
// estimate to warn against, not a boundary to compute with.
const HIDLimit = 64

// hidMargin is how close to HIDLimit the count may get before the table warns.
const hidMargin = 4

// None marks a device Windows knows about but has not given a joystick ID —
// either it is not a game controller, or it has not been enumerated as one
// since its cache entry was created.
const None = -1

// HWID identifies a device type by USB vendor and product ID, which is how the
// DirectInput cache keys its entries.
type HWID struct {
	VID uint16
	PID uint16
}

// Key renders the ID the way the registry names the device's cache entry.
func (h HWID) Key() string { return fmt.Sprintf("VID_%04X&PID_%04X", h.VID, h.PID) }

// ParseKey reads a DirectInput cache entry name such as `VID_30B7&PID_1001`.
// Entries that are not VID/PID pairs — `VIRTUALDEVICE&10` is one on this rig —
// are reported as not ok and skipped by the caller: there is no hardware ID to
// pin them by.
func ParseKey(name string) (HWID, bool) {
	var h HWID
	var haveV, haveP bool
	for _, f := range strings.Split(strings.ToUpper(name), "&") {
		switch {
		case strings.HasPrefix(f, "VID_") && len(f) == 8:
			if v, err := strconv.ParseUint(f[4:], 16, 16); err == nil {
				h.VID, haveV = uint16(v), true
			}
		case strings.HasPrefix(f, "PID_") && len(f) == 8:
			if v, err := strconv.ParseUint(f[4:], 16, 16); err == nil {
				h.PID, haveP = uint16(v), true
			}
		default:
			return HWID{}, false
		}
	}
	if !haveV || !haveP {
		return HWID{}, false
	}
	return h, true
}

// Store is the machine's joystick state. It is an interface so the planning
// and the command layer can be tested without Windows, and so the registry and
// WinMM code stays in one Windows-only file.
type Store interface {
	// Assigned reports the ID the DirectInput cache holds for every device
	// it has an entry for, None where the entry carries no ID. A device absent
	// from the map has never been seen by Windows on this account, and cannot
	// be given an ID until it has.
	Assigned() (map[HWID]int, error)

	// Visible reports which ID each controller answers to for a program
	// starting now — what a game launched at this moment would see. This is
	// the check that matters: a device can hold an ID in the cache and still
	// be missing from this list, which is exactly the failure this package
	// was written to make visible.
	//
	// It is only meaningful in a freshly started process. A long-running one
	// keeps the controller list it built at startup, which is why the GUI asks
	// a re-exec of the CLI rather than calling this itself.
	Visible() (map[HWID]int, error)

	// SetID writes one device's ID.
	SetID(hw HWID, id int) error

	// HIDInterfaces counts the HID device interfaces present on the machine —
	// every collection of every HID device, not one per physical device.
	// DirectInput lists only roughly the first HIDLimit of them; see HIDLimit.
	HIDInterfaces() (int, error)

	// Notify tells Windows the joystick configuration changed.
	Notify()
}

// Pin is a device held at a chosen ID.
type Pin struct {
	HW HWID
	ID int
}

// Change is one ID that Plan decided to rewrite.
type Change struct {
	HW   HWID
	From int
	To   int
}

// Plan works out the writes that put every pinned device on its pinned ID.
//
// Moving a device onto an occupied ID swaps the occupant onto the ID the
// pinned device is leaving — the same thing JoyIDs does, and the least
// disruptive move, since only two devices change. When the pinned device had no
// ID to give up, the occupant takes the lowest ID nobody holds and no pin
// claims, so it is not parked on a slot that a later pin is about to take.
//
// Pins are applied in ID order so the result does not depend on map iteration.
// Distinct pins cannot undo one another: a later swap only moves a device off
// the later pin's target, and an earlier pinned device is never on that target
// because pins are distinct (config validation guarantees it).
//
// unseen lists pinned devices with no cache entry at all. Those cannot be
// written — there is nothing to write to until Windows has enumerated the device
// once — so they are reported rather than failing the rest.
func Plan(assigned map[HWID]int, pins []Pin) (changes []Change, unseen []HWID) {
	cur := make(map[HWID]int, len(assigned))
	for hw, id := range assigned {
		cur[hw] = id
	}

	claimed := map[int]bool{}
	for _, p := range pins {
		claimed[p.ID] = true
	}

	ordered := append([]Pin(nil), pins...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	for _, p := range ordered {
		from, seen := cur[p.HW]
		if !seen {
			unseen = append(unseen, p.HW)
			continue
		}
		if from == p.ID {
			continue
		}
		for _, occ := range holdersOf(cur, p.ID) {
			if occ == p.HW {
				continue
			}
			if from != None {
				cur[occ] = from
			} else {
				cur[occ] = lowestFree(cur, claimed, p.ID)
			}
		}
		cur[p.HW] = p.ID
	}

	for hw, id := range cur {
		if id != assigned[hw] {
			changes = append(changes, Change{HW: hw, From: assigned[hw], To: id})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].To < changes[j].To })
	return changes, unseen
}

// holdersOf returns every device holding id, in a stable order. Normally there
// is at most one; a damaged cache can hold two, and both have to move.
func holdersOf(cur map[HWID]int, id int) []HWID {
	var out []HWID
	for hw, v := range cur {
		if v == id {
			out = append(out, hw)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// lowestFree picks an ID for a device displaced by a pin. It prefers one that
// no device holds and no pin claims; failing that, one no device holds. The
// pin's own target is never returned — that is the ID being vacated for it.
func lowestFree(cur map[HWID]int, claimed map[int]bool, target int) int {
	used := map[int]bool{target: true}
	for _, v := range cur {
		used[v] = true
	}
	for id := 0; id <= MaxID; id++ {
		if !used[id] && !claimed[id] {
			return id
		}
	}
	for id := 0; id <= MaxID; id++ {
		if !used[id] {
			return id
		}
	}
	return None
}

// Apply plans and writes the pins, then tells Windows. It writes nothing when
// every pin already holds, so it is cheap enough to run before every session.
func Apply(s Store, pins []Pin) (changes []Change, unseen []HWID, err error) {
	if len(pins) == 0 {
		return nil, nil, nil
	}
	assigned, err := s.Assigned()
	if err != nil {
		return nil, nil, err
	}
	changes, unseen = Plan(assigned, pins)
	for _, c := range changes {
		if c.To == None {
			continue
		}
		if err := s.SetID(c.HW, c.To); err != nil {
			return nil, unseen, fmt.Errorf("setting %s to ID %d: %w", c.HW.Key(), c.To, err)
		}
	}
	if len(changes) > 0 {
		s.Notify()
	}
	return changes, unseen, nil
}

// Device is a controller the caller has a name for — in practice, one of the
// rig devices from the usb list.
type Device struct {
	Alias     string
	Name      string
	HW        HWID
	Connected bool
}

// Row is one line of the ID table. It is also the JSON the GUI renders, which
// is why the optional numbers are pointers: an ID of 0 is a real slot, so "no
// ID" has to be distinguishable from it on the wire.
type Row struct {
	Alias      string `json:"alias,omitempty"`
	Name       string `json:"name"`
	HardwareID string `json:"hardwareId"`
	// ID is what the DirectInput cache holds; nil when Windows has given the
	// device no ID.
	ID *int `json:"id"`
	// Pinned is the ID the config holds the device at, nil when unpinned.
	Pinned *int `json:"pinned"`
	// Connected is whether the device is plugged in. Unknown (other) devices
	// are only listed because they are visible, so they are connected by
	// construction.
	Connected bool `json:"connected"`
	// Visible is whether a game starting now would see the device.
	Visible bool `json:"visible"`
	// Hidden flags the failure this package exists for: connected, and
	// invisible to games anyway.
	Hidden bool `json:"hidden"`
}

// Report is the whole table plus anything the command did to produce it.
type Report struct {
	Devices []Row `json:"devices"`
	MaxID   int   `json:"maxId"`
	// HIDInterfaces is how many HID interfaces the machine has, 0 when it
	// could not be counted. Reported because past HIDLimit DirectInput stops
	// listing devices, and that — not anything an ID change touches — is what
	// hid the pedals on 2026-10-14.
	HIDInterfaces int      `json:"hidInterfaces,omitempty"`
	HIDLimit      int      `json:"hidLimit"`
	Notes         []string `json:"notes,omitempty"`
}

// BuildReport assembles the table: every named device, plus any other
// controller a game would currently see. The extras matter because they hold
// IDs too — a pin onto an ID held by something unnamed has to show what it is
// displacing.
func BuildReport(devs []Device, assigned, visible map[HWID]int, pins map[string]int) Report {
	r := Report{MaxID: MaxID, HIDLimit: HIDLimit}
	named := map[HWID]bool{}

	for _, d := range devs {
		named[d.HW] = true
		row := Row{
			Alias:      d.Alias,
			Name:       d.Name,
			HardwareID: d.HW.Key(),
			Connected:  d.Connected,
		}
		if id, ok := assigned[d.HW]; ok && id != None {
			row.ID = intPtr(id)
		}
		if p, ok := pins[d.Alias]; ok {
			row.Pinned = intPtr(p)
		}
		_, row.Visible = visible[d.HW]
		row.Hidden = row.Connected && !row.Visible
		r.Devices = append(r.Devices, row)
	}

	for hw, id := range visible {
		if named[hw] {
			continue
		}
		r.Devices = append(r.Devices, Row{
			Name:       "Other controller",
			HardwareID: hw.Key(),
			ID:         intPtr(id),
			Connected:  true,
			Visible:    true,
		})
	}

	sort.SliceStable(r.Devices, func(i, j int) bool {
		a, b := r.Devices[i], r.Devices[j]
		if (a.ID == nil) != (b.ID == nil) {
			return a.ID != nil
		}
		if a.ID != nil && *a.ID != *b.ID {
			return *a.ID < *b.ID
		}
		return a.Alias+a.HardwareID < b.Alias+b.HardwareID
	})
	return r
}

func intPtr(v int) *int { return &v }

// FormatReport writes the ID table for the terminal.
func FormatReport(w io.Writer, r Report) {
	fmt.Fprintln(w, "Joystick IDs")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-3s %-4s %-6s %-11s %s\n", "ID", "Pin", "Games", "Alias", "Device")
	hidden := 0
	for _, row := range r.Devices {
		id, pin := "-", "-"
		if row.ID != nil {
			id = strconv.Itoa(*row.ID)
		}
		if row.Pinned != nil {
			pin = strconv.Itoa(*row.Pinned)
		}
		games := "-"
		switch {
		case row.Hidden:
			games = "NO"
			hidden++
		case row.Visible:
			games = "yes"
		}
		alias := row.Alias
		if alias == "" {
			alias = "-"
		}
		name := row.Name
		if !row.Connected {
			name += "  (not connected)"
		}
		if row.Alias == "" {
			name += "  " + row.HardwareID
		}
		fmt.Fprintf(w, "  %3s %4s %-6s %-11s %s\n", id, pin, games, alias, name)
	}
	fmt.Fprintln(w)
	if hidden > 0 {
		// The table alone would show the NO and leave the reader to guess at
		// it. On this rig the cause was the machine's HID interface count
		// passing DirectInput's limit, which no ID change fixes, so say so.
		fmt.Fprintf(w, "  %d connected controller(s) are invisible to games. A changed ID will not fix that.\n", hidden)
		if r.HIDInterfaces > 0 {
			fmt.Fprintf(w, "  This PC has %d HID interfaces; DirectInput lists only about the first %d.\n", r.HIDInterfaces, r.HIDLimit)
			fmt.Fprintln(w, "  Unplug or disable an input gadget you do not use (wireless dongles add the most).")
		}
	} else if r.HIDInterfaces >= r.HIDLimit-hidMargin {
		// Everything is visible, but one more gadget could push the last
		// device in the list off the end. Worth saying before it happens.
		fmt.Fprintf(w, "  Close to the limit: %d HID interfaces, and DirectInput lists only about the first %d.\n", r.HIDInterfaces, r.HIDLimit)
	}
	fmt.Fprintln(w, "  Pin a device with `motorhome joy pin <alias> <id>`; `motorhome start` re-applies pins.")
	for _, n := range r.Notes {
		fmt.Fprintln(w, "  "+n)
	}
}

// DescribeChange renders a change for output, naming the device by alias when
// it has one.
func DescribeChange(c Change, names map[HWID]string) string {
	who := names[c.HW]
	if who == "" {
		who = c.HW.Key()
	}
	from := "none"
	if c.From != None {
		from = strconv.Itoa(c.From)
	}
	return fmt.Sprintf("%s: ID %s → %d", who, from, c.To)
}
