package iracing

import "testing"

const sampleYAML = `
WeekendInfo:
 TrackName: nurburgring grand prix
DriverInfo:
 DriverCarIdx: 2
 DriverHeadPosX: 0.0
 Drivers:
  - CarIdx: 0
    UserName: Pace Car
    CarNumber: "0"
    CarScreenName: Pace Car
    CarClassID: 0
  - CarIdx: 1
    UserName: John Doe
    CarNumber: "22"
    CarScreenName: Porsche 718 Cayman GT4
    CarClassID: 4001
  - CarIdx: 2
    UserName: Ricky Maw
    CarNumber: "77"
    CarScreenName: Porsche 718 Cayman GT4
    CarClassID: 4001
  - CarIdx: 5
    UserName: Jane Smith
    CarNumber: "7"
    CarScreenName: BMW M4 GT4
    CarClassID: 4002
SplitTimeInfo:
 Sectors:
`

func TestParseDrivers_ExtractsAllBlocks(t *testing.T) {
	drivers := ParseDrivers(sampleYAML)
	if len(drivers) != 4 {
		t.Fatalf("want 4 drivers, got %d", len(drivers))
	}
	want := map[int32]DriverInfo{
		0: {CarIdx: 0, UserName: "Pace Car", CarNumber: "0", CarScreenName: "Pace Car", CarClassID: 0},
		1: {CarIdx: 1, UserName: "John Doe", CarNumber: "22", CarScreenName: "Porsche 718 Cayman GT4", CarClassID: 4001},
		2: {CarIdx: 2, UserName: "Ricky Maw", CarNumber: "77", CarScreenName: "Porsche 718 Cayman GT4", CarClassID: 4001},
		5: {CarIdx: 5, UserName: "Jane Smith", CarNumber: "7", CarScreenName: "BMW M4 GT4", CarClassID: 4002},
	}
	for idx, w := range want {
		got, ok := drivers[idx]
		if !ok {
			t.Errorf("CarIdx %d missing", idx)
			continue
		}
		if got != w {
			t.Errorf("CarIdx %d: got %+v want %+v", idx, got, w)
		}
	}
}

func TestParseDrivers_StopsAtTopLevelKey(t *testing.T) {
	// After the Drivers block, SplitTimeInfo is a sibling top-level key — the
	// parser must not treat its children as continuing the last driver block.
	drivers := ParseDrivers(sampleYAML)
	if d, ok := drivers[5]; ok && d.UserName != "Jane Smith" {
		t.Errorf("last driver parsed as %+v — looks like parser ran past end of block", d)
	}
}

func TestDriverCarIdxFromYAML(t *testing.T) {
	got := DriverCarIdxFromYAML(sampleYAML)
	if got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

func TestDriverCarIdxFromYAML_Missing(t *testing.T) {
	got := DriverCarIdxFromYAML("WeekendInfo:\n TrackName: foo\n")
	if got != -1 {
		t.Fatalf("want -1 (missing), got %d", got)
	}
}
