package pb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanned_MissingFileIsEmptyLedger(t *testing.T) {
	s, err := LoadScanned(filepath.Join(t.TempDir(), "pbscan.json"))
	if err != nil {
		t.Fatalf("LoadScanned: %v", err)
	}
	if len(s) != 0 {
		t.Errorf("want empty ledger, got %d entries", len(s))
	}
}

func TestScanned_RoundTripAndStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pbscan.json")
	mod := time.Date(2026, 10, 5, 19, 58, 18, 0, time.UTC)

	s := Scanned{}
	s.Mark("a.ibt", 100, mod)
	if err := SaveScanned(path, s); err != nil {
		t.Fatalf("SaveScanned: %v", err)
	}
	got, err := LoadScanned(path)
	if err != nil {
		t.Fatalf("LoadScanned: %v", err)
	}

	if !got.Seen("a.ibt", 100, mod) {
		t.Error("same name/size/time should be seen")
	}
	// A file still being recorded when it was first checked grows afterwards —
	// it must be checked again, not skipped forever.
	if got.Seen("a.ibt", 200, mod) {
		t.Error("a changed size must not count as seen")
	}
	if got.Seen("a.ibt", 100, mod.Add(time.Second)) {
		t.Error("a changed modification time must not count as seen")
	}
	if got.Seen("b.ibt", 100, mod) {
		t.Error("an unknown file must not count as seen")
	}
}

func TestScanned_CorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pbscan.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScanned(path); err == nil {
		t.Error("want an error for a corrupt ledger")
	}
}
