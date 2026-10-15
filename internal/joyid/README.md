# internal/joyid

Reports which game controllers a game launched now would see, and the most
likely reason one is missing. Backs `motorhome joy`, the warning in
`motorhome start`, and the GUI's Game controllers card. It only reads.

## Why

On 2026-10-14 the Heusinkveld pedals worked in their own software but were
invisible to iRacing, MAIRA and `joy.cpl`, and nothing on the machine said so.
This package makes that one command to see: each rig controller, whether
DirectInput 8 lists it, the joystick ID Windows holds for it, and how many HID
interfaces the machine has.

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

`HIDLimit` (64) is fitted to that one observation, not documented anywhere, so
the warnings treat it as an estimate.

`HIDInterfaces` lists every interface via `CM_Get_Device_Interface_List` on
`GUID_DEVINTERFACE_HID`, tagged with the device it belongs to (`DeviceKey`: the
VID/PID pair, so all of one dongle's collections group together) and that
device's product name — the bus-reported description from a USB ancestor, since
the collection itself is only "HID-compliant mouse". `GroupHID` totals them per
device, largest first; the GUI shows the top of that list so the answer to
"what do I unplug?" is the first row.

## Why there is no pinning

The package used to pin devices to chosen joystick IDs by writing the
`Joystick Id` value in the per-user DirectInput cache (what JoyIDs does). It was
removed: iRacing and MAIRA bind by device **GUID**, not by slot number, so the
slot order never mattered to them, and the "device changed, rebind it" problems
that prompted it were devices dropping off the list because of the HID limit.
The one real-rig test of it worked (`joy pin pedals 1` swapped the pedals and the
haptic unit), so the mechanism was sound — it just answered the wrong question.

The joystick ID is still shown, read from:

```
HKCU\System\CurrentControlSet\Control\MediaProperties\PrivateProperties\
    DirectInput\VID_xxxx&PID_xxxx\Calibration\<n>   "Joystick Id"  REG_BINARY, 4 bytes LE
```

A device can have several `Calibration\<n>` keys, one per HID collection
DirectInput has seen, and only the game-controller collection has an ID (the
wheelbase has a vendor collection at `\0` and its joystick at `\1`). The
lowest-numbered key holding an ID is the one read.

### Never clear that cache

The same keys hold `GUID`, the instance GUID games and tools (iRacing, MAIRA)
save bindings against. Deleting the cache — tried on 2026-10-14 while chasing the
pedals — made Windows invent new GUIDs, so MAIRA reported the wheelbase as
`Device missing [80046140-…]`. It also deleted
`Joystick\OEM\VID_xxxx&PID_xxxx\OEMForceFeedback`, the per-user registration of
the wheelbase's force-feedback driver (Simagic's `{148CC31A-…}`), which Windows
does not recreate; force feedback then failed with `DIERR_DEVICENOTREG` /
"Class not registered". Importing the old GUIDs and that key from a registry
export, run by the user in their own terminal, put both back. The reset fixed
nothing.

## What games can see

`Visible` asks **DirectInput 8** — the API iRacing and MAIRA use — which game
controllers are attached, pairing each with its legacy (WinMM) slot. MAIRA's own
logs (`Documents\MAIRA\Logs`) list every device its DirectInput 8 enumeration
returns, which makes them a good independent check.

The answer is only meaningful in a **freshly started process**: a program builds
its controller list when it starts and keeps it. That is why the GUI re-execs
`motorhome joy -json` instead of calling this in-process.

The DirectInput call is raw COM through the vtable — `DirectInput8Create`, then
`EnumDevices` with a `syscall.NewCallback`. The callback is created once and
reused, because callback slots are a fixed, never-freed resource. For HID
devices the first field of `guidProduct` packs `PID<<16 | VID`.

## A warning about testing from an agent shell

On 2026-10-14 the agent shell used to develop this ran inside a sandbox whose
registry writes landed in a private overlay (`\REGISTRY\WC\Silo…`). Its own
probes saw changes no real program could, which produced false "verified on the
rig" claims for an hour. Device enumeration and file access were real; HKCU was
not. Anything that depends on the registry must be confirmed by a command the
user runs, or by a real application's logs.

## Files

| File | Contents |
|---|---|
| `joyid.go` | Types, `Store` interface, `BuildReport`, `FormatReport`, `GroupHID`, `DeviceKey` — all pure |
| `joyid_windows.go` | Registry read, WinMM slot read, DirectInput 8 enumeration, HID interface list with device names |
| `joyid_other.go` | Stub `Store` returning `ErrUnsupported` |
| `joyid_test.go` | Report and formatting tests against fixed inputs |

## Known limitations

- `HIDLimit` (64) is an estimate fitted to one rig, so "close to the limit" is a
  hint rather than a boundary.
- A device is identified by VID/PID, so two identical devices share one entry.
- `Hidden` says a device is missing from DirectInput; the HID count says whether
  the limit is the likely reason. Any other cause would need other tools.
