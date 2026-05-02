package pb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKey(t *testing.T) {
	got := Key("Porsche 911 GT3 R", "Sebring")
	want := "Porsche 911 GT3 R|Sebring"
	if got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

func TestKey_EmptyFields(t *testing.T) {
	got := Key("", "")
	if got != "|" {
		t.Errorf("Key(\"\",\"\") = %q, want \"|\"", got)
	}
}

// ---- Load ----

func TestLoad_FileNotFound(t *testing.T) {
	f, err := Load("nonexistent_pb_xyzzy.json")
	if err != nil {
		t.Fatalf("Load missing file: got error %v, want nil", err)
	}
	if len(f) != 0 {
		t.Errorf("Load missing file: got non-empty map")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")
	os.WriteFile(p, []byte("not json {{"), 0644)

	_, err := Load(p)
	if err == nil {
		t.Error("Load invalid JSON: expected error, got nil")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	pbf := File{
		"GT3|Sebring": {
			LapTime:          131.5,
			LapTimeFormatted: "2:11.500",
			Date:             "2026-03-01",
			Weather:          "Air 22°C, Track 35°C",
			Car:              "GT3",
			Track:            "Sebring",
		},
	}
	if err := Save(p, pbf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := loaded["GT3|Sebring"]
	if entry == nil {
		t.Fatal("entry not found after load")
	}
	if entry.LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5", entry.LapTime)
	}
	if entry.LapTimeFormatted != "2:11.500" {
		t.Errorf("LapTimeFormatted = %q, want 2:11.500", entry.LapTimeFormatted)
	}
	if entry.Weather != "Air 22°C, Track 35°C" {
		t.Errorf("Weather = %q, want Air 22°C, Track 35°C", entry.Weather)
	}
}

// ---- Save ----

func TestSave_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	if err := Save(p, File{}); err != nil {
		t.Fatalf("Save empty file: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSave_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	orig := File{
		"Car A|Track X": {LapTime: 90.0, LapTimeFormatted: "1:30.000", Date: "2026-01-01", Car: "Car A", Track: "Track X"},
		"Car B|Track Y": {LapTime: 75.5, LapTimeFormatted: "1:15.500", Date: "2026-02-01", Car: "Car B", Track: "Track Y"},
	}
	if err := Save(p, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	if got["Car B|Track Y"].LapTime != 75.5 {
		t.Errorf("LapTime = %v, want 75.5", got["Car B|Track Y"].LapTime)
	}
}

// ---- Update ----

func TestUpdate_NewEntry(t *testing.T) {
	pbf := File{}
	isNew := Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "Air 22°C")
	if !isNew {
		t.Error("Update new entry: expected true, got false")
	}
	entry := pbf[Key("GT3", "Sebring")]
	if entry == nil {
		t.Fatal("entry not stored after Update")
	}
	if entry.LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5", entry.LapTime)
	}
	if entry.Car != "GT3" {
		t.Errorf("Car = %q, want GT3", entry.Car)
	}
	if entry.Track != "Sebring" {
		t.Errorf("Track = %q, want Sebring", entry.Track)
	}
}

func TestUpdate_FasterLap_ReplacesPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")

	isNew := Update(pbf, "GT3", "Sebring", 130.0, "2:10.000", "2026-03-02", "")
	if !isNew {
		t.Error("Update faster lap: expected true, got false")
	}
	if pbf[Key("GT3", "Sebring")].LapTime != 130.0 {
		t.Errorf("LapTime = %v, want 130.0 after PB improvement", pbf[Key("GT3", "Sebring")].LapTime)
	}
}

func TestUpdate_SlowerLap_KeepsPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")

	isNew := Update(pbf, "GT3", "Sebring", 135.0, "2:15.000", "2026-03-02", "")
	if isNew {
		t.Error("Update slower lap: expected false, got true")
	}
	if pbf[Key("GT3", "Sebring")].LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5 (PB should not change)", pbf[Key("GT3", "Sebring")].LapTime)
	}
}

func TestUpdate_EqualLap_KeepsPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "old weather")

	isNew := Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-02", "new weather")
	if isNew {
		t.Error("Update equal lap: expected false, got true")
	}
	// Original entry should be unchanged.
	if pbf[Key("GT3", "Sebring")].Weather != "old weather" {
		t.Error("weather changed on equal laptime — original PB entry should be kept")
	}
}

func TestUpdate_FasterLap_PreservesBrakeEntries(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	// Simulate brake entries accumulated before the new PB.
	BrakeEntrySet(pbf, "GT3", "Sebring", "T1", 0.42, 5)

	// New PB — brake entries must survive.
	Update(pbf, "GT3", "Sebring", 130.0, "2:10.000", "2026-03-02", "")

	entry := pbf[Key("GT3", "Sebring")]
	if entry == nil {
		t.Fatal("entry not found after PB update")
	}
	if entry.BrakeEntries == nil {
		t.Fatal("BrakeEntries nil after PB update — should be preserved")
	}
	if entry.BrakeEntries["T1"].Pct != 0.42 {
		t.Errorf("T1 BrakeEntry.Pct = %v, want 0.42 after PB update", entry.BrakeEntries["T1"].Pct)
	}
}

func TestUpdate_FasterLap_ClearsPhases(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	// Simulate stored PB phases.
	SetPhases(pbf, "GT3", "Sebring", []PBPhase{
		{SegName: "T1", Kind: "entry", SpeedEntryKPH: 200},
	})
	if len(pbf[Key("GT3", "Sebring")].Phases) != 1 {
		t.Fatal("phases not stored")
	}

	// New PB — old phases must be cleared (they belong to the old lap).
	Update(pbf, "GT3", "Sebring", 130.0, "2:10.000", "2026-03-02", "")

	entry := pbf[Key("GT3", "Sebring")]
	if len(entry.Phases) != 0 {
		t.Errorf("Phases should be cleared on new PB, got %d phases", len(entry.Phases))
	}
}

func TestSetPhases(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	phases := []PBPhase{
		{SegName: "S1", Kind: "full", SpeedEntryKPH: 180, SpeedExitKPH: 220, ThrottlePct: 95},
		{SegName: "T1", Kind: "entry", SpeedEntryKPH: 220, SpeedExitKPH: 140, BrakePct: 85},
		{SegName: "T1", Kind: "mid", SpeedEntryKPH: 140, SpeedExitKPH: 130, LatGAvg: 1.45},
		{SegName: "T1", Kind: "exit", SpeedEntryKPH: 130, SpeedExitKPH: 160, ThrottlePct: 70},
	}
	SetPhases(pbf, "GT3", "Sebring", phases)

	stored := pbf[Key("GT3", "Sebring")].Phases
	if len(stored) != 4 {
		t.Fatalf("len(Phases) = %d, want 4", len(stored))
	}
	if stored[1].SegName != "T1" || stored[1].Kind != "entry" {
		t.Errorf("Phase[1] = %s/%s, want T1/entry", stored[1].SegName, stored[1].Kind)
	}
	if stored[1].BrakePct != 85 {
		t.Errorf("Phase[1].BrakePct = %v, want 85", stored[1].BrakePct)
	}
}

func TestSetPhases_NoEntry(t *testing.T) {
	pbf := File{}
	// SetPhases on non-existent entry should not panic or create entry.
	SetPhases(pbf, "GT3", "Sebring", []PBPhase{{SegName: "T1", Kind: "full"}})
	if len(pbf) != 0 {
		t.Error("SetPhases should not create a PB entry if none exists")
	}
}

func TestPhases_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	SetPhases(pbf, "GT3", "Sebring", []PBPhase{
		{SegName: "T1", Kind: "entry", SpeedEntryKPH: 200, BrakePct: 80, LatGAvg: 1.2, Corrections: 2, ABSCount: 5},
		{SegName: "T1", Kind: "mid", SpeedEntryKPH: 140, LatGAvg: 1.45},
	})

	if err := Save(p, pbf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := loaded[Key("GT3", "Sebring")]
	if entry == nil {
		t.Fatal("entry not found after roundtrip")
	}
	if len(entry.Phases) != 2 {
		t.Fatalf("len(Phases) = %d, want 2", len(entry.Phases))
	}
	p0 := entry.Phases[0]
	if p0.SegName != "T1" || p0.Kind != "entry" {
		t.Errorf("Phase[0] = %s/%s, want T1/entry", p0.SegName, p0.Kind)
	}
	if p0.SpeedEntryKPH != 200 {
		t.Errorf("SpeedEntryKPH = %v, want 200", p0.SpeedEntryKPH)
	}
	if p0.ABSCount != 5 {
		t.Errorf("ABSCount = %v, want 5", p0.ABSCount)
	}
}

func TestPhaseLookup(t *testing.T) {
	phases := []PBPhase{
		{SegName: "T1", Kind: "entry"},
		{SegName: "T1", Kind: "mid"},
		{SegName: "S2", Kind: "full"},
	}
	m := PhaseLookup(phases)
	if len(m) != 3 {
		t.Errorf("len = %d, want 3", len(m))
	}
	if m[PhaseKey("T1", "mid")] == nil {
		t.Error("T1|mid not found")
	}
	if m[PhaseKey("S2", "full")] == nil {
		t.Error("S2|full not found")
	}
	if m[PhaseKey("T1", "exit")] != nil {
		t.Error("T1|exit should not exist")
	}
}

func TestUpdate_StubEntryFromBrakeEntries(t *testing.T) {
	// BrakeEntrySet creates a stub entry with LapTime=0 to hold accumulated
	// brake data. The first real PB must succeed against that stub even though
	// 0 < newLapTime would make a naive comparison return "stub is faster".
	pbf := File{}
	BrakeEntrySet(pbf, "GT3", "Sebring", "T1", 0.42, 5)
	if pbf[Key("GT3", "Sebring")].LapTime != 0 {
		t.Fatalf("setup error: stub entry should have LapTime=0")
	}

	isNew := Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	if !isNew {
		t.Fatal("Update against LapTime=0 stub: expected true (new PB), got false")
	}
	entry := pbf[Key("GT3", "Sebring")]
	if entry.LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5", entry.LapTime)
	}
	if entry.BrakeEntries["T1"].Pct != 0.42 {
		t.Error("BrakeEntries lost when promoting stub to real PB")
	}
}

func TestSetSetup(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")

	yaml := "CarSetup:\n Tires:\n  LeftFront:\n   ColdPressure: 138 kPa\n"
	SetSetup(pbf, "GT3", "Sebring", yaml)

	stored := pbf[Key("GT3", "Sebring")].Setup
	if stored != yaml {
		t.Errorf("Setup = %q, want %q", stored, yaml)
	}
}

func TestSetSetup_NoEntry(t *testing.T) {
	pbf := File{}
	SetSetup(pbf, "GT3", "Sebring", "CarSetup:\n")
	if len(pbf) != 0 {
		t.Error("SetSetup should not create a PB entry if none exists")
	}
}

func TestSetup_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")
	yaml := "CarSetup:\n Tires:\n  LeftFront:\n   ColdPressure: 138 kPa\n"
	SetSetup(pbf, "GT3", "Sebring", yaml)

	if err := Save(p, pbf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded[Key("GT3", "Sebring")].Setup != yaml {
		t.Errorf("Setup not preserved across save/load")
	}
}

func TestUpdate_IndependentCarTrackCombos(t *testing.T) {
	pbf := File{}
	Update(pbf, "Car A", "Track X", 100.0, "1:40.000", "2026-01-01", "")
	Update(pbf, "Car A", "Track Y", 90.0, "1:30.000", "2026-01-01", "")
	Update(pbf, "Car B", "Track X", 80.0, "1:20.000", "2026-01-01", "")

	if len(pbf) != 3 {
		t.Errorf("len = %d, want 3", len(pbf))
	}
	if pbf[Key("Car A", "Track X")].LapTime != 100.0 {
		t.Errorf("Car A / Track X laptime wrong")
	}
	if pbf[Key("Car B", "Track X")].LapTime != 80.0 {
		t.Errorf("Car B / Track X laptime wrong")
	}
}
