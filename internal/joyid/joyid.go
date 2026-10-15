// Package joyid reports which game controllers a game launched now would see,
// and why one might be missing.
//
// On 2026-10-14 the Heusinkveld pedals worked in their own software but were
// invisible to iRacing, MAIRA and joy.cpl, and nothing on the machine said so.
// The cause was DirectInput's HID interface limit (see HIDLimit). This package
// exists so that failure is one command to see: it lists each rig controller,
// whether DirectInput 8 enumerates it, the joystick ID Windows holds for it, and
// how many HID interfaces the machine has.
//
// It reads only. It used to pin devices to chosen joystick IDs by writing the
// DirectInput cache, but iRacing and MAIRA bind by device GUID rather than by
// slot number, so pinning solved nothing this rig had, and was removed.
//
// The joystick ID shown comes from the per-user DirectInput cache:
//
//	HKCU\System\CurrentControlSet\Control\MediaProperties\PrivateProperties\
//	    DirectInput\VID_xxxx&PID_xxxx\Calibration\<n>   "Joystick Id" (REG_BINARY, 4 bytes LE)
//
// Everything here except the Store implementation is pure, so the report is
// tested without a registry or any hardware.
package joyid

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

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
// are reported as not ok and skipped by the caller.
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

// Store is the machine's joystick state. It is an interface so the command
// layer can be tested without Windows, and so the registry, DirectInput and
// config-manager code stays in one Windows-only file.
type Store interface {
	// Assigned reports the ID the DirectInput cache holds for every device
	// it has an entry for, None where the entry carries no ID.
	Assigned() (map[HWID]int, error)

	// Visible reports the controllers DirectInput 8 lists for a program
	// starting now — what a game launched at this moment would see.
	//
	// It is only meaningful in a freshly started process. A long-running one
	// keeps the controller list it built at startup, which is why the GUI asks
	// a re-exec of the CLI rather than calling this itself.
	Visible() (map[HWID]int, error)

	// HIDInterfaces counts the HID device interfaces present on the machine —
	// every collection of every HID device, not one per physical device.
	// DirectInput lists only roughly the first HIDLimit of them.
	HIDInterfaces() (int, error)
}

// Device is a controller the caller has a name for — in practice, one of the
// rig devices from the usb list.
type Device struct {
	Alias     string
	Name      string
	HW        HWID
	Connected bool
}

// Row is one line of the table. It is also the JSON the GUI renders, which is
// why the ID is a pointer: ID 0 is a real slot, so "no ID" has to be
// distinguishable from it on the wire.
type Row struct {
	Alias      string `json:"alias,omitempty"`
	Name       string `json:"name"`
	HardwareID string `json:"hardwareId"`
	// ID is what the DirectInput cache holds; nil when Windows has given the
	// device no ID.
	ID *int `json:"id"`
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

// Report is the whole table.
type Report struct {
	Devices []Row `json:"devices"`
	// HIDInterfaces is how many HID interfaces the machine has, 0 when it
	// could not be counted. Reported because past HIDLimit DirectInput stops
	// listing devices, which is what hid the pedals on 2026-10-14.
	HIDInterfaces int `json:"hidInterfaces,omitempty"`
	HIDLimit      int `json:"hidLimit"`
}

// BuildReport assembles the table: every named device, plus any other
// controller a game would currently see.
func BuildReport(devs []Device, assigned, visible map[HWID]int) Report {
	r := Report{HIDLimit: HIDLimit}
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
		_, row.Visible = visible[d.HW]
		row.Hidden = row.Connected && !row.Visible
		r.Devices = append(r.Devices, row)
	}

	for hw, id := range visible {
		if named[hw] {
			continue
		}
		row := Row{Name: "Other controller", HardwareID: hw.Key(), Connected: true, Visible: true}
		if id != None {
			row.ID = intPtr(id)
		}
		r.Devices = append(r.Devices, row)
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

// Hidden counts connected devices games cannot see.
func (r Report) Hidden() int {
	n := 0
	for _, row := range r.Devices {
		if row.Hidden {
			n++
		}
	}
	return n
}

// NearLimit reports whether the HID interface count is close enough to
// HIDLimit that one more gadget could push a controller off the list.
func (r Report) NearLimit() bool {
	return r.HIDInterfaces > 0 && r.HIDInterfaces >= r.HIDLimit-hidMargin
}

// FormatReport writes the table for the terminal.
func FormatReport(w io.Writer, r Report) {
	fmt.Fprintln(w, "Game controllers")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-3s %-6s %-11s %s\n", "ID", "Games", "Alias", "Device")
	for _, row := range r.Devices {
		id := "-"
		if row.ID != nil {
			id = strconv.Itoa(*row.ID)
		}
		games := "-"
		switch {
		case row.Hidden:
			games = "NO"
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
		fmt.Fprintf(w, "  %3s %-6s %-11s %s\n", id, games, alias, name)
	}
	fmt.Fprintln(w)
	if r.HIDInterfaces > 0 {
		fmt.Fprintf(w, "  HID interfaces: %d (DirectInput lists only about the first %d)\n", r.HIDInterfaces, r.HIDLimit)
	}
	if hidden := r.Hidden(); hidden > 0 {
		fmt.Fprintf(w, "  %d connected controller(s) are invisible to games.\n", hidden)
		if r.HIDInterfaces > 0 {
			fmt.Fprintln(w, "  Unplug or disable an input gadget you do not use (wireless dongles add the most).")
		}
	} else if r.NearLimit() {
		// Everything is visible, but one more gadget could push the last
		// device in the list off the end. Worth saying before it happens.
		fmt.Fprintln(w, "  Close to the limit: one more input gadget could hide a controller.")
	}
}
