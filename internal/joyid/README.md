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
2026-10-14, and nothing on the machine said so. That case is why `Visible`, the
`Hidden` flag and the HID interface count exist; pinning is the part JoyIDs used
to be needed for.

## Where the ID lives

One value per device, in the per-user DirectInput cache:

```
HKCU\System\CurrentControlSet\Control\MediaProperties\PrivateProperties\
    DirectInput\VID_xxxx&PID_xxxx\Calibration\<n>
        "Joystick Id"  REG_BINARY  4 bytes, little-endian
        "GUID"         REG_BINARY  the device's instance GUID
```

Writing `Joystick Id` is the whole mechanism. Verified on the real rig:
`joy pin pedals 1` swapped the pedals and the haptic unit, and the haptic unit
then answered at ID 4 in a freshly started DirectInput enumeration. Windows keeps
its own slot table (`MediaResources\Joystick\DINPUT.DLL\CurrentJoystickSettings`)
and that table is left alone.

An earlier round of "verification" does not count, and is recorded so nobody
relies on it: it ran from an agent shell whose registry writes landed in a
private sandbox overlay (`\REGISTRY\WC\Silo…`), so its own probes saw changes no
real program could. The claim that a pin survives the device being replugged
came from that round and has not been re-checked.

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
`Device missing [80046140-…]`, which was the wheelbase's old GUID. The same reset
also deletes `Joystick\OEM\VID_xxxx&PID_xxxx\OEMForceFeedback`, the per-user
registration of the wheelbase's force-feedback driver (Simagic's `{148CC31A-…}`),
and Windows does not recreate it — opening the device for force feedback then
fails with `DIERR_DEVICENOTREG` / "Class not registered". Importing the old GUIDs
and that key from a registry export (run by the user, in their own terminal)
brought MAIRA back with force feedback.

The reset was a mistake made while chasing the pedals, whose real cause was the
HID interface limit below. It fixed nothing and cost an evening.

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

It started out asking only the legacy API. DirectInput 8 is the one that has to
be right, since it is what the sims use; MAIRA's own logs, which list every device
its DirectInput 8 enumeration returns, are a good independent check of it. (The
two APIs were also seen to disagree, but only from the sandboxed shell described
above, so that observation is not evidence.)

The answer is only meaningful in a **freshly started process**. A program builds
its controller list when it starts and keeps it, so a long-running process can
go on seeing a device newer processes have lost. That is why the
GUI re-execs `motorhome joy -json` instead of calling this in-process.

The DirectInput call is raw COM through the vtable — `DirectInput8Create`, then
`EnumDevices` with a `syscall.NewCallback`. The callback is created once and
reused, because callback slots are a fixed, never-freed resource. For HID
devices the first field of `guidProduct` packs `PID<<16 | VID`.

## The HID interface limit — what actually hid the pedals

DirectInput lists only roughly the first 64 HID *interfaces* on the machine.
Interfaces, not devices: every top-level collection of every HID device counts,
so one wireless dongle can contribute a dozen. Past the limit, devices later in
the system's HID interface order are simply missing — `EnumDevices` returns
`S_OK` and the list is short. Nothing logs it.

On 2026-10-14 the rig had **75** interfaces. DirectInput listed the wheelbase at
position 59 and dropped the pedals at 73. Unplugging an unused Steam Controller
dongle (13 interfaces) took the count to 62 and the pedals came back, with
Corsair iCUE and the Xeneon Edge both still connected.

That explains a day of misleading clues. iCUE's virtual keyboard/mouse adds 5
interfaces and the Xeneon Edge 4, so uninstalling iCUE or unplugging the Edge
"fixed" it; replugging the Edge moved its interfaces to the end of the order,
behind the pedals, which also "fixed" it. HidHide did not help — hidden devices
still occupy positions in the list. The device lost is whichever rig controller
sits latest in the order, which on this rig is the pedals.

`HIDInterfaces` counts them via `CM_Get_Device_Interface_List` on
`GUID_DEVINTERFACE_HID`. The table prints the count when a device is hidden, and
warns when it is within `hidMargin` of the limit even with nothing hidden yet —
one more gadget is all it takes.

## Files

| File | Contents |
|---|---|
| `joyid.go` | Types, `Store` interface, `Plan`, `Apply`, `BuildReport`, `FormatReport` — all pure |
| `joyid_windows.go` | Registry reads/writes, WinMM slot read, DirectInput 8 enumeration, HID interface count |
| `joyid_other.go` | Stub `Store` returning `ErrUnsupported` |
| `joyid_test.go` | Planning, apply, report and formatting tests against a fake store |

## Known limitations

- Pinned devices are identified by VID/PID, so two identical devices (same
  hardware ID) share one cache entry and cannot be pinned separately.
- A pin onto an ID only takes effect for programs started afterwards; a running
  game keeps the order it saw at launch.
- Windows can still reshuffle IDs between `start`s (a device replugged
  mid-session). `motorhome joy apply` puts pins back on demand.
- `HIDLimit` (64) is an estimate fitted to one rig, not a documented constant,
  so the "close to the limit" warning is a hint rather than a boundary.
- `Hidden` says a device is missing from DirectInput; the HID count says whether
  the limit is the likely reason. Any other cause would need other tools.
