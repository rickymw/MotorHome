// Package textenc converts iRacing's session-info YAML to UTF-8.
//
// iRacing writes that YAML in Windows-1252, not UTF-8: a track called
// "Hockenheimring Baden-Württemberg" arrives with a raw 0xFC byte. Go strings
// carry that byte happily, but encoding/json replaces every invalid byte with
// U+FFFD when it writes, so a name used as a JSON map key came back from
// trackmap.json and pb.json as a different string from the one it was saved
// under — the lookup missed on every run, re-detecting the map and re-setting
// the PB each time.
package textenc

import (
	"strings"
	"unicode/utf8"
)

// cp1252High maps bytes 0x80–0x9F to their Windows-1252 code points. The five
// bytes Windows-1252 leaves undefined (0x81, 0x8D, 0x8F, 0x90, 0x9D) map to the
// C1 control of the same value, as Latin-1 would; every other byte ≥ 0xA0 is
// identical in both encodings and needs no table.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// Decode returns s as UTF-8. A string that is already valid UTF-8 is returned
// unchanged — so Decode is idempotent and safe to apply at more than one layer,
// and a future iRacing build that switches to UTF-8 is not double-encoded.
// Anything else is decoded byte by byte as Windows-1252.
//
// The check is all-or-nothing over the whole input, not per run of bytes: a
// Windows-1252 document can contain byte pairs that happen to form valid UTF-8
// ("Ã¼" is C3 BC), and decoding those as UTF-8 while decoding the rest as
// Windows-1252 would corrupt exactly the names that look fine.
func Decode(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c < 0x80:
			b.WriteByte(c)
		case c < 0xA0:
			b.WriteRune(cp1252High[c-0x80])
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

// LegacyJSONName returns the string that name was stored under in a JSON file
// written before session YAML was decoded: the raw Windows-1252 bytes, with each
// byte that is not valid UTF-8 replaced by U+FFFD the way encoding/json does
// it (one replacement per byte, not per run).
//
// It returns name itself when there is nothing to recover — an ASCII name, or
// one containing a rune Windows-1252 cannot represent, which therefore never
// came from the YAML as raw bytes. Callers compare the result against name and
// only look up the legacy key when they differ.
func LegacyJSONName(name string) string {
	raw, ok := encodeCP1252(name)
	if !ok || raw == name {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(raw[i : i+size])
		}
		i += size
	}
	return b.String()
}

// encodeCP1252 is the inverse of Decode's byte mapping. ok is false when name
// contains a rune with no Windows-1252 byte.
func encodeCP1252(name string) (string, bool) {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x80 || (r >= 0xA0 && r <= 0xFF):
			b.WriteByte(byte(r))
		default:
			c, found := cp1252Byte(r)
			if !found {
				return "", false
			}
			b.WriteByte(c)
		}
	}
	return b.String(), true
}

func cp1252Byte(r rune) (byte, bool) {
	for i, v := range cp1252High {
		if v == r {
			return byte(0x80 + i), true
		}
	}
	return 0, false
}
