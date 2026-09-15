package textenc

import (
	"encoding/json"
	"testing"
)

func TestDecode_Latin1Umlaut(t *testing.T) {
	got := Decode("Hockenheimring Baden-W\xfcrttemberg")
	if want := "Hockenheimring Baden-Württemberg"; got != want {
		t.Errorf("Decode = %q, want %q", got, want)
	}
}

func TestDecode_Windows1252HighRange(t *testing.T) {
	// 0x80 € and 0x92 ’ differ between Windows-1252 and Latin-1; 0x81 is
	// undefined in Windows-1252 and falls back to its Latin-1 C1 control.
	got := Decode("\x80 \x92 \x81")
	if want := "€ ’ "; got != want {
		t.Errorf("Decode = %q, want %q", got, want)
	}
}

func TestDecode_ValidUTF8Unchanged(t *testing.T) {
	for _, s := range []string{"", "Spa-Francorchamps", "Baden-Württemberg", "Autódromo José Carlos Pace"} {
		if got := Decode(s); got != s {
			t.Errorf("Decode(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestDecode_Idempotent(t *testing.T) {
	once := Decode("N\xfcrburgring \x80")
	if twice := Decode(once); twice != once {
		t.Errorf("Decode not idempotent: %q then %q", once, twice)
	}
}

func TestDecode_WholeDocumentNotPerRun(t *testing.T) {
	// "\xc3\xbc" is valid UTF-8 (ü) in isolation, but in a document that is
	// otherwise Windows-1252 it means "Ã¼".
	got := Decode("\xc3\xbc \xfc")
	if want := "Ã¼ ü"; got != want {
		t.Errorf("Decode = %q, want %q", got, want)
	}
}

// TestLegacyJSONName_MatchesEncodingJSON is the property that matters: the
// legacy key must be byte-for-byte what encoding/json actually wrote for the
// undecoded name, or the old entry is never found.
func TestLegacyJSONName_MatchesEncodingJSON(t *testing.T) {
	for _, raw := range []string{
		"Hockenheimring Baden-W\xfcrttemberg",
		"\xfc\xfc double",
		"\x80uro",
		"\xc3\xbc accidental \xfc", // valid-looking pair inside an invalid name
		"plain ascii",
	} {
		b, err := json.Marshal(map[string]int{raw: 1})
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]int
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		var stored string
		for k := range back {
			stored = k
		}
		if got := LegacyJSONName(Decode(raw)); got != stored {
			t.Errorf("LegacyJSONName(Decode(%q)) = %q, json stored %q", raw, got, stored)
		}
	}
}

func TestLegacyJSONName_NoLegacyForm(t *testing.T) {
	for _, s := range []string{"Watkins Glen", "東京"} {
		if got := LegacyJSONName(s); got != s {
			t.Errorf("LegacyJSONName(%q) = %q, want unchanged", s, got)
		}
	}
}
