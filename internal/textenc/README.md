# internal/textenc

Converts iRacing's session-info YAML from Windows-1252 to UTF-8, and recovers
names stored before that conversion existed.

## What it does

iRacing writes the session YAML — in `.ibt` files and in shared memory — in
Windows-1252, not UTF-8. "Hockenheimring Baden-Württemberg" arrives with a raw
`0xFC` byte. A Go string holds that byte without complaint, so nothing looked
wrong until the name was used as a JSON map key: `encoding/json` replaces every
invalid byte with U+FFFD on write, so `trackmap.json` and `pb.json` stored
`Baden-W�rttemberg` and the next lookup, still holding `0xFC`, missed. Every
run at that track re-detected the map as a "first detection", set a "new PB",
and never showed a vs-PB table.

## How it works

`Decode(s)` returns `s` unchanged if it is already valid UTF-8, and otherwise
maps each byte through Windows-1252. The validity check is over the **whole
document**, not per run of bytes: a Windows-1252 document can contain pairs
that happen to form valid UTF-8 (`C3 BC`, "Ã¼"), and decoding those as UTF-8
would corrupt exactly the names that look fine. Returning valid input unchanged
makes `Decode` idempotent — safe at more than one layer — and means a future
iRacing build that switches to UTF-8 is not double-encoded.

The five bytes Windows-1252 leaves undefined (`0x81 0x8D 0x8F 0x90 0x9D`) decode
to the C1 control of the same value, as Latin-1 would.

`LegacyJSONName(name)` computes what a decoded name was stored as **before**
the fix: re-encode to Windows-1252, then replace each invalid byte with U+FFFD
one byte at a time, which is what `encoding/json` does (not
`strings.ToValidUTF8`, which collapses runs). The mangled key cannot be decoded
back — U+FFFD has lost the byte — so migration has to start from the correct
name and compute the old key from it. A test checks the result against what
`encoding/json` actually writes.

It returns `name` unchanged when there is no legacy form (ASCII, or a rune
Windows-1252 cannot represent); callers compare and skip the fallback.

## Architecture

| Symbol | Description |
|---|---|
| `Decode(s)` | Windows-1252 → UTF-8; valid UTF-8 passes through unchanged |
| `LegacyJSONName(name)` | The U+FFFD-mangled key a pre-fix JSON store used for `name` |

Callers: `ibt.parse` and `iracing.ReadLiveData` decode at the source;
`analysis.ParseSessionMeta` decodes again defensively; `trackmap.AdoptLegacyName`,
`TrackRefFile` lookups and `pb.AdoptLegacyKey` use `LegacyJSONName`.
