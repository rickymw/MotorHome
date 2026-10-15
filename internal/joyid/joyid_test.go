package joyid

import (
	"bytes"
	"errors"
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

func asMap(changes []Change) map[HWID][2]int {
	out := map[HWID][2]int{}
	for _, c := range changes {
		out[c.HW] = [2]int{c.From, c.To}
	}
	return out
}

func TestPlanNothingToDoWhenPinsHold(t *testing.T) {
	changes, unseen := Plan(map[HWID]int{pedals: 0, handbrake: 1}, []Pin{{pedals, 0}})
	if len(changes) != 0 || len(unseen) != 0 {
		t.Errorf("got %v %v, want no changes", changes, unseen)
	}
}

// The case this was built for: the pedals onto a slot someone else holds. The
// occupant takes the slot the pedals left, and nothing else moves.
func TestPlanSwapsWithTheOccupant(t *testing.T) {
	assigned := map[HWID]int{handbrake: 0, wheelbase: 3, pedals: 4}
	changes, _ := Plan(assigned, []Pin{{pedals, 0}})
	got := asMap(changes)
	if got[pedals] != [2]int{4, 0} || got[handbrake] != [2]int{0, 4} {
		t.Errorf("changes = %v, want pedals 4→0 and handbrake 0→4", changes)
	}
	if _, moved := got[wheelbase]; moved {
		t.Error("a device not involved in the swap was moved")
	}
}

func TestPlanMovingOntoAFreeIDDisturbsNobody(t *testing.T) {
	changes, _ := Plan(map[HWID]int{handbrake: 0, pedals: 4}, []Pin{{pedals, 9}})
	if len(changes) != 1 || changes[0] != (Change{pedals, 4, 9}) {
		t.Errorf("changes = %v", changes)
	}
}

// A device with no ID has nothing to trade, so the occupant goes to the lowest
// slot nobody holds — and not one a later pin is about to claim.
func TestPlanDisplacedDeviceAvoidsClaimedSlots(t *testing.T) {
	assigned := map[HWID]int{handbrake: 0, rim: 2, pedals: None}
	changes, _ := Plan(assigned, []Pin{{pedals, 0}, {wheelbase, 1}})
	got := asMap(changes)
	if got[pedals] != [2]int{None, 0} {
		t.Errorf("pedals = %v", got[pedals])
	}
	// 1 is free but claimed by the wheelbase pin; 2 is taken; 3 is next.
	if got[handbrake] != [2]int{0, 3} {
		t.Errorf("handbrake = %v, want 0→3", got[handbrake])
	}
}

// Two pins whose devices hold each other's slots must converge, not undo each
// other.
func TestPlanCrossedPinsConverge(t *testing.T) {
	assigned := map[HWID]int{pedals: 1, handbrake: 0}
	changes, _ := Plan(assigned, []Pin{{pedals, 0}, {handbrake, 1}})
	got := asMap(changes)
	if got[pedals] != [2]int{1, 0} || got[handbrake] != [2]int{0, 1} {
		t.Errorf("changes = %v", changes)
	}
}

func TestPlanReportsDevicesWindowsHasNeverSeen(t *testing.T) {
	changes, unseen := Plan(map[HWID]int{handbrake: 0}, []Pin{{pedals, 0}})
	if len(changes) != 0 {
		t.Errorf("an unseen device moved others: %v", changes)
	}
	if len(unseen) != 1 || unseen[0] != pedals {
		t.Errorf("unseen = %v", unseen)
	}
}

// A damaged cache can have two devices on one ID; both have to make room.
func TestPlanMovesEveryHolderOfTheTarget(t *testing.T) {
	assigned := map[HWID]int{pedals: 4, handbrake: 0, rim: 0}
	changes, _ := Plan(assigned, []Pin{{pedals, 0}})
	got := asMap(changes)
	if got[pedals][1] != 0 {
		t.Fatalf("pedals not moved: %v", changes)
	}
	if got[handbrake][1] == 0 || got[rim][1] == 0 {
		t.Errorf("a second holder was left on the pinned ID: %v", changes)
	}
}

type fakeStore struct {
	assigned map[HWID]int
	visible  map[HWID]int
	sets     []Change
	notified int
	hid      int
	setErr   error
}

func (f *fakeStore) Assigned() (map[HWID]int, error) { return f.assigned, nil }
func (f *fakeStore) Visible() (map[HWID]int, error)  { return f.visible, nil }
func (f *fakeStore) SetID(hw HWID, id int) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.sets = append(f.sets, Change{HW: hw, From: f.assigned[hw], To: id})
	return nil
}
func (f *fakeStore) Notify()                     { f.notified++ }
func (f *fakeStore) HIDInterfaces() (int, error) { return f.hid, nil }

// Apply runs before every session, so a pin that already holds must cost no
// writes and no notification.
func TestApplyWritesNothingWhenPinsHold(t *testing.T) {
	s := &fakeStore{assigned: map[HWID]int{pedals: 0}}
	if _, _, err := Apply(s, []Pin{{pedals, 0}}); err != nil {
		t.Fatal(err)
	}
	if len(s.sets) != 0 || s.notified != 0 {
		t.Errorf("sets=%v notified=%d", s.sets, s.notified)
	}
}

func TestApplyWritesAndNotifies(t *testing.T) {
	s := &fakeStore{assigned: map[HWID]int{pedals: 4, handbrake: 0}}
	changes, _, err := Apply(s, []Pin{{pedals, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.sets) != 2 || len(changes) != 2 || s.notified != 1 {
		t.Errorf("sets=%v changes=%v notified=%d", s.sets, changes, s.notified)
	}
}

func TestApplyReportsWriteFailure(t *testing.T) {
	s := &fakeStore{assigned: map[HWID]int{pedals: 4}, setErr: errors.New("access denied")}
	if _, _, err := Apply(s, []Pin{{pedals, 0}}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyWithNoPinsReadsNothing(t *testing.T) {
	changes, unseen, err := Apply(nil, nil) // a nil store would panic if touched
	if err != nil || changes != nil || unseen != nil {
		t.Errorf("got %v %v %v", changes, unseen, err)
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
	r := BuildReport(rigDevices(), assigned, visible, map[string]int{"handbrake": 0})

	if r.MaxID != MaxID {
		t.Errorf("MaxID = %d", r.MaxID)
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
	if h := byKey[handbrake.Key()]; h.Pinned == nil || *h.Pinned != 0 || h.Hidden {
		t.Errorf("handbrake row = %+v", h)
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
}

func TestBuildReportDeviceWithNoIDSortsLast(t *testing.T) {
	r := BuildReport(rigDevices(), map[HWID]int{pedals: None, handbrake: 2}, map[HWID]int{}, nil)
	if r.Devices[len(r.Devices)-1].ID != nil {
		t.Errorf("a device with no ID did not sort last: %+v", r.Devices)
	}
}

func TestFormatReportExplainsHiddenDevices(t *testing.T) {
	r := BuildReport(rigDevices(), map[HWID]int{pedals: 4, handbrake: 0}, map[HWID]int{handbrake: 0}, nil)
	r.Notes = []string{"Pinned pedals to ID 0."}
	var buf bytes.Buffer
	FormatReport(&buf, r)
	out := buf.String()
	for _, want := range []string{"NO", "invisible to games", "will not fix", "(not connected)", "Pinned pedals to ID 0."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatReportIsQuietWhenAllVisible(t *testing.T) {
	r := BuildReport(rigDevices()[:2], map[HWID]int{pedals: 4, handbrake: 0}, map[HWID]int{pedals: 4, handbrake: 0}, nil)
	var buf bytes.Buffer
	FormatReport(&buf, r)
	if strings.Contains(buf.String(), "invisible") {
		t.Errorf("warned with every device visible:\n%s", buf.String())
	}
}

func TestDescribeChange(t *testing.T) {
	names := map[HWID]string{pedals: "pedals"}
	if got := DescribeChange(Change{pedals, 4, 0}, names); got != "pedals: ID 4 → 0" {
		t.Errorf("got %q", got)
	}
	if got := DescribeChange(Change{rim, None, 2}, names); got != "VID_3670&PID_0808: ID none → 2" {
		t.Errorf("got %q", got)
	}
}

// A hidden device is explained by the HID count, because on this rig that —
// not the ID — was what DirectInput was dropping devices over.
func TestFormatReportNamesTheHIDLimitForHiddenDevices(t *testing.T) {
	r := BuildReport(rigDevices(), map[HWID]int{pedals: 4, handbrake: 0}, map[HWID]int{handbrake: 0}, nil)
	r.HIDInterfaces = 75
	var buf bytes.Buffer
	FormatReport(&buf, r)
	for _, want := range []string{"75 HID interfaces", "about the first 64", "wireless dongles"} {
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
		r := BuildReport(rigDevices()[:2], all, all, nil)
		r.HIDInterfaces = c.n
		var buf bytes.Buffer
		FormatReport(&buf, r)
		if got := strings.Contains(buf.String(), "Close to the limit"); got != c.warn {
			t.Errorf("%d interfaces: warned=%v, want %v\n%s", c.n, got, c.warn, buf.String())
		}
	}
}

func TestBuildReportCarriesTheLimit(t *testing.T) {
	if r := BuildReport(nil, nil, nil, nil); r.HIDLimit != HIDLimit {
		t.Errorf("HIDLimit = %d", r.HIDLimit)
	}
}
