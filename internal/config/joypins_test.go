package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAcceptsDistinctPins(t *testing.T) {
	cfg := Config{JoyPins: map[string]int{"pedals": 0, "wheelbase": 1, "rim": 15}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidateRejectsBadPins(t *testing.T) {
	cases := []struct {
		pins map[string]int
		want string
	}{
		{map[string]int{"pedals": -1}, "outside 0–15"},
		{map[string]int{"pedals": 16}, "outside 0–15"},
		{map[string]int{" ": 0}, "empty device alias"},
		// Two devices on one ID would swap back and forth on every start.
		{map[string]int{"pedals": 2, "rim": 2}, "pedals and rim are both pinned to 2"},
	}
	for _, c := range cases {
		err := Config{JoyPins: c.pins}.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.pins, err, c.want)
		}
	}
}

// A pin naming a device that no longer exists must not stop the config
// loading — every other subcommand would go down with it.
func TestValidateAcceptsPinForUnknownAlias(t *testing.T) {
	if err := (Config{JoyPins: map[string]int{"gone": 3}}).Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestJoyPinsRoundTripAndOmitWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := Save(path, Config{JoyPins: map[string]int{"pedals": 0}}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.JoyPins["pedals"] != 0 || len(got.JoyPins) != 1 {
		t.Errorf("JoyPins = %v", got.JoyPins)
	}

	if err := Save(path, Config{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "joyPins") {
		t.Errorf("an unpinned config grew a joyPins field:\n%s", b)
	}
}
