package joyid

import (
	"bytes"
	"strings"
	"testing"
)

var (
	pedals    = HWID{0x30B7, 0x1001}
	handbrake = HWID{0x346E, 0x001F}
	wheelbase = HWID{0x3670, 0x0501}
	rim       = HWID{0x3670, 0x0808}
)

func TestParseKey(t *testing.T) {
	cases := []struct {
		in   string
		want HWID
		ok   bool
	}{
		{"VID_30B7&PID_1001", pedals, true},
		{"vid_30b7&pid_1001", pedals, true},
		{"VIRTUALDEVICE&10", HWID{}, false}, // the Corsair entry on this rig
		{"VID_30B7", HWID{}, false},
		{"VID_30B7&PID_1001&MI_00", HWID{}, false},
		{"VID_ZZZZ&PID_1001", HWID{}, false},
	}
	for _, c := range cases {
		got, ok := ParseKey(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseKey(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	if pedals.Key() != "VID_30B7&PID_1001" {
		t.Errorf("Key() = %q", pedals.Key())
	}
}

func rigDevices() []Device {
	return []Device{
		{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", HW: pedals, Connected: true},
		{Alias: "handbrake", Name: "MOZA HBP Handbrake", HW: handbrake, Connected: true},
		{Alias: "wheelbase", Name: "SIMAGIC Alpha EVO Pro", HW: wheelbase, Connected: false},
	}
}

func TestBuildReport(t *testing.T) {
	other := HWID{0x044F, 0xB68D}
	assigned := map[HWID]int{pedals: 4, handbrake: 0, wheelbase: 3}
	visible := map[HWID]int{handbrake: 0, wheelbase: 3, other: 1} // pedals hidden
	r := BuildReport(rigDevices(), assigned, visible)

	if r.HIDLimit != HIDLimit {
		t.Errorf("HIDLimit = %d", r.HIDLimit)
	}
	byKey := map[string]Row{}
	var order []string
	for _, row := range r.Devices {
		byKey[row.HardwareID] = row
		order = append(order, row.HardwareID)
	}

	if p := byKey[pedals.Key()]; !p.Hidden || p.Visible || p.ID == nil || *p.ID != 4 {
		t.Errorf("pedals row = %+v, want hidden with ID 4", p)
	}
	// Unplugged is not hidden: a device cannot be missing from games if it is
	// not there to see.
	if w := byKey[wheelbase.Key()]; w.Hidden {
		t.Errorf("an unplugged device was flagged hidden: %+v", w)
	}
	if o, ok := byKey[other.Key()]; !ok || o.Alias != "" || o.ID == nil || *o.ID != 1 {
		t.Errorf("an unnamed visible controller was not listed: %+v", o)
	}
	want := []string{handbrake.Key(), other.Key(), wheelbase.Key(), pedals.Key()}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want by ID", order)
	}
	if r.Hidden() != 1 {
		t.Errorf("Hidden() = %d", r.Hidden())
	}
}

func TestBuildReportDeviceWithNoIDSortsLast(t *testing.T) {
	r := BuildReport(rigDevices(), map[HWID]int{pedals: None, handbrake: 2}, map[HWID]int{})
	if r.Devices[len(r.Devices)-1].ID != nil {
		t.Errorf("a device with no ID did not sort last: %+v", r.Devices)
	}
}

// A visible controller with no slot in the legacy API is listed without an ID,
// not as ID -1.
func TestBuildReportOtherControllerWithoutSlot(t *testing.T) {
	r := BuildReport(nil, nil, map[HWID]int{rim: None})
	if len(r.Devices) != 1 || r.Devices[0].ID != nil {
		t.Errorf("devices = %+v", r.Devices)
	}
}

// A hidden device is explained by the HID count, because on this rig that was
// what DirectInput was dropping devices over.
func TestFormatReportNamesTheHIDLimitForHiddenDevices(t *testing.T) {
	r := BuildReport(rigDevices(), map[HWID]int{pedals: 4, handbrake: 0}, map[HWID]int{handbrake: 0})
	r.HIDInterfaces = 75
	var buf bytes.Buffer
	FormatReport(&buf, r)
	for _, want := range []string{"NO", "HID interfaces: 75", "about the first 64", "invisible to games", "wireless dongles", "(not connected)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
}

// Everything visible but one gadget from the edge: worth a line before it bites.
func TestFormatReportWarnsNearTheHIDLimit(t *testing.T) {
	all := map[HWID]int{pedals: 4, handbrake: 0}
	for _, c := range []struct {
		n    int
		warn bool
	}{{62, true}, {59, false}, {0, false}} {
		r := BuildReport(rigDevices()[:2], all, all)
		r.HIDInterfaces = c.n
		var buf bytes.Buffer
		FormatReport(&buf, r)
		if got := strings.Contains(buf.String(), "Close to the limit"); got != c.warn {
			t.Errorf("%d interfaces: warned=%v, want %v\n%s", c.n, got, c.warn, buf.String())
		}
		if got := r.NearLimit(); got != c.warn {
			t.Errorf("%d interfaces: NearLimit=%v", c.n, got)
		}
	}
}

func TestFormatReportIsQuietWhenAllVisible(t *testing.T) {
	all := map[HWID]int{pedals: 4, handbrake: 0}
	r := BuildReport(rigDevices()[:2], all, all)
	r.HIDInterfaces = 40
	var buf bytes.Buffer
	FormatReport(&buf, r)
	if strings.Contains(buf.String(), "invisible") || strings.Contains(buf.String(), "Close to") {
		t.Errorf("warned with every device visible and plenty of room:\n%s", buf.String())
	}
}

func TestInterfacePathToInstanceID(t *testing.T) {
	in := `\\?\HID#VID_046D&PID_C52B&MI_01&Col01#a&29ceee04&0&0000#{4d1e55b2-f16f-11cf-88cb-001111000030}`
	want := `HID\VID_046D&PID_C52B&MI_01&Col01\a&29ceee04&0&0000`
	if got := InterfacePathToInstanceID(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Every interface and collection of one USB device must group together, or the
// breakdown would show a 13-interface dongle as thirteen 1-interface rows.
func TestDeviceKey(t *testing.T) {
	cases := map[string]string{
		`HID\VID_046D&PID_C52B&MI_01&Col01\a&29ceee04&0&0000`:          "VID_046D&PID_C52B",
		`HID\VID_046D&PID_C52B&MI_02&Col03\a&5f7b042&0&0002`:           "VID_046D&PID_C52B",
		`HID\VID_0FD9&PID_0098&REV_0360&MI_03&Col01\a&1279fb64&1&0000`: "VID_0FD9&PID_0098",
		`HID\VID_30B7&PID_1001\c&1b1d2ea3&0&0000`:                      "VID_30B7&PID_1001",
		`HID\virtualdevice&10&Col02\2&1f4f21a5&0&0001`:                 "VIRTUALDEVICE&10",
		`HID\virtualdevice&10&Col05\2&1f4f21a5&0&0004`:                 "VIRTUALDEVICE&10",
	}
	for in, want := range cases {
		if got := DeviceKey(in); got != want {
			t.Errorf("DeviceKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupHID(t *testing.T) {
	list := []HIDInterface{
		{Device: "VID_046D&PID_C547", Name: "USB Receiver"},
		{Device: "VID_28DE&PID_1304", Name: "Steam Controller Puck"},
		{Device: "VID_28DE&PID_1304"},
		{Device: "VID_28DE&PID_1304"},
		{Device: "VID_046D&PID_C547"},
		{Device: "VID_30B7&PID_1001", Name: "Heusinkveld Sim Pedals Sprint"},
	}
	got := GroupHID(list)
	want := []HIDUser{
		{Device: "VID_28DE&PID_1304", Name: "Steam Controller Puck", Interfaces: 3},
		{Device: "VID_046D&PID_C547", Name: "USB Receiver", Interfaces: 2},
		{Device: "VID_30B7&PID_1001", Name: "Heusinkveld Sim Pedals Sprint", Interfaces: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if GroupHID(nil) != nil {
		t.Error("empty input should give nil")
	}
}
