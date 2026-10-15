# internal/joyid

Reads and changes the joystick IDs Windows hands out to game controllers, pins
chosen devices to chosen IDs, and checks which controllers a game would actually
see. Backs `motorhome joy`, the pre-flight in `motorhome start`, and the GUI's
Joystick IDs card.

## Why

Games number controllers 0–15. Windows assigns those numbers and reshuffles them
when devices come and go, which moves bindings onto the wrong device. Worse, a
connected device can drop out of the list games see altogether while working
fine in its vendor's software — the Heusinkveld pedals did exactly that on
2026-10-14, and nothing on the machine said so. That case is why `Visible` and
the `Hidden` flag exist; pinning is the part JoyIDs used to be needed for.

## Where the ID lives

One value per device, in the per-user DirectInput cache:

```
HKCU\System\CurrentControlSet\Control\MediaProperties\PrivateProperties\
    DirectInput\VID_xxxx&PID_xxxx\Calibration\<n>
        "Joystick Id"  REG_BINARY  4 bytes, little-endian
        "GUID"         REG_BINARY  the device's instance GUID
```

Writing `Joystick Id` is the whole mechanism. Verified on this rig before any
code was written: swapping two devices' values changed the order a freshly
started process saw; the change survived the device being unplugged and
replugged; and Windows rewrote its own slot table
(`MediaResources\Joystick\DINPUT.DLL\CurrentJoystickSettings`) to match without
being touched — so that table is left alone.

A device can have several `Calibration\<n>` keys, one per HID collection
DirectInput has seen, and only the game-controller collection has an ID (the
wheelbase has a vendor collection at `\0` with none and its joystick at `\1`).
The lowest-numbered key holding an ID is the one read and written, so the two
always agree.

No elevation is needed: it is HKCU.

### Never touch the GUID — and never clear the cache

The same key holds `GUID`, the instance GUID DirectInput reports. Games and
tools (iRacing, MAIRA) save bindings against it. Deleting the cache to "reset"
joystick IDs makes Windows invent new GUIDs and every binding goes missing —
learned the hard way: after a reset, MAIRA reported the wheelbase as
`Device missing [80046140-…]`, which was the wheelbase's old GUID. Restoring the
old GUIDs from a registry export brought everything back.

This package only ever writes `Joystick Id`. There is deliberately no reset.

## Pinning

`Plan` works out the writes. Moving a device onto an occupied ID swaps the
occupant onto the ID being vacated — the least disruptive move, since only two
devices change. A device with no ID to trade pushes the occupant to the lowest ID
nobody holds and no pin claims. Pins are applied in ID order; distinct pins
(enforced by config validation) cannot undo each other.

A pinned device Windows has never enumerated has no cache entry to write; it is
reported as unseen rather than failing the rest.

`Apply` writes nothing when every pin holds, which is what makes it cheap enough
to run before every session in `motorhome start`.

## What games can see

`Visible` asks **DirectInput 8** — the API iRacing and MAIRA use — which game
controllers are attached, and pairs each with its legacy (WinMM) slot.

It started out asking only the legacy API, and that was wrong in a way that
mattered: during the GUID restore above, the legacy API reported all five rig
devices in their slots while DirectInput 8 listed three. A check built on the
legacy API said everything was fine while MAIRA could not find the wheelbase.

The answer is only meaningful in a **freshly started process**. A program builds
its controller list when it starts and keeps it, so a long-running process can
go on seeing a device every newer process has lost (observed). That is why the
GUI re-execs `motorhome joy -json` instead of calling this in-process.

The DirectInput call is raw COM through the vtable — `DirectInput8Create`, then
`EnumDevices` with a `syscall.NewCallback`. The callback is created once and
reused, because callback slots are a fixed, never-freed resource. For HID
devices the first field of `guidProduct` packs `PID<<16 | VID`.

## Files

| File | Contents |
|---|---|
| `joyid.go` | Types, `Store` interface, `Plan`, `Apply`, `BuildReport`, `FormatReport` — all pure |
| `joyid_windows.go` | Registry reads/writes, WinMM slot read, DirectInput 8 enumeration |
| `joyid_other.go` | Stub `Store` returning `ErrUnsupported` |
| `joyid_test.go` | Planning, apply, report and formatting tests against a fake store |

## Known limitations

- Pinned devices are identified by VID/PID, so two identical devices (same
  hardware ID) share one cache entry and cannot be pinned separately.
- A pin onto an ID only takes effect for programs started afterwards; a running
  game keeps the order it saw at launch.
- Windows can still reshuffle IDs between `start`s (a device replugged
  mid-session). `motorhome joy apply` puts pins back on demand.
- `Hidden` says a device is missing from DirectInput, not why. The one cause seen
  so far was a third-party virtual input device (Corsair iCUE), which no ID change
  fixes.
