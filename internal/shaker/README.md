# internal/shaker

Drives a tactile transducer (ButtKicker or similar) through its audio output
device, so it can be tested without launching a sim. Backs `motorhome shaker`.

## What a transducer is, for the purposes of this package

A speaker that moves a seat instead of air. Testing one therefore means playing
a low-frequency tone at it, and the two design constraints below both follow
from that rather than from anything about sim racing.

## Playing to the wrong device is the failure worth engineering against

A 40 Hz tone sent to headphones is unpleasant. Sent to a full-range speaker at
level it is a way to damage a woofer. So there is **no default-output-device
fallback anywhere in this package**: the device is matched by name substring,
and not finding it is an error listing what was found. `FindDevice` also refuses
an ambiguous match, the same rule `pb show` and `usb` follow — but here guessing
wrong does not print a wrong table, it makes a noise in hardware.

The ButtKicker amplifier presents as **"KT USB Audio"**, its manufacturer's
name, with nothing in the string resembling "ButtKicker" — hence
`DefaultDeviceMatch`, and hence the no-match error printing the full device list
rather than just saying no.

Note the WinMM name limit: `waveOutGetDevCapsW` truncates to `MAXPNAMELEN` (32
wide chars), so longer names arrive cut off mid-word (`"Odyssey G95C (NVIDIA
High Defin"`). Substring matching absorbs that as long as the distinguishing
part is early in the name. Reading the full name would mean WASAPI's
`IMMDeviceEnumerator`, which is COM vtable interop for a cosmetic gain.

## Amplitude is stepped, never set

`DefaultSteps` ramps 2% → 50% of full scale. The first test after a transducer
has sat unused is also the moment a fault shows up, so the useful behaviour is
to start below anything that could stress the amplifier and climb in steps the
operator can abort between. It stops at 50% because "does this work" is settled
long before full output; higher is opt-in via `-max`.

`StepPlan` **drops** steps above the max rather than clamping them. Clamping
would replay one level several times and announce each as a step up, which is
precisely the wrong thing to show someone watching for where a fault appears.

## Every tone is enveloped

A sine that begins at full amplitude is a DC step. In a speaker that is a click;
in a transducer bolted to a seat it is a mechanical slam, and it is hard on an
amplifier that may already be sick. `Tone.PCM` applies a `DefaultFadeMS` linear
ramp in and out.

The envelope is computed inside the sample loop rather than as a post-pass, so a
tone shorter than two fade lengths degrades to a triangle instead of clipping
back to a step. That case is not hypothetical — the shortest tones are the ones
used to probe hardware that is suspect.

## What a clean run does and does not prove

It proves Windows accepted the format, opened the device and played every
buffer: the path from PC to the audio interface works. It cannot prove the
transducer moved, and it says nothing about whether the amplifier is
electrically sound — a burning smell, a hot case or a failing supply are all
invisible from here. The command's closing lines say so explicitly, and a test
asserts that wording, because reporting a clean run as "working" is the wrong
thing to tell someone testing hardware they already suspect.

**This is not a theoretical caveat — it happened on the rig this was written
for.** The ButtKicker had smelled of burning electronics, and afterwards
produced no output at all. Throughout, the `KT USB Audio` device kept
enumerating as completely healthy (composite device, audio and HID interfaces,
every node `OK`) and `shaker test` kept reporting clean runs. The USB audio
interface and the power amplifier are separate sections of the unit, and only
the first is visible to software — so a green result from this command is
evidence about the PC, not about the hardware bolted to the seat.

Do not be tempted to infer amplifier health from a successful enumeration or a
successful play. Nothing reachable from here distinguishes a working amplifier
from a dead one.

## Format

48 kHz, 16-bit, stereo — what USB audio interfaces accept without negotiation.
Both channels carry the same signal: a transducer amplifier may take one channel
or sum the pair, and which it does is not worth discovering during a test of
whether it works at all.

## Win32 notes (`shaker_windows.go`)

Raw `winmm.dll` `waveOut*` calls via `syscall.NewLazyDLL`, matching
`internal/audio` (which uses the `waveIn*` half of the same API) and the
no-external-dependency style of `internal/camera` and `internal/usbdev`.

`Play` blocks by polling `WAVEHDR.dwFlags` for `WHDR_DONE`. `Stop` calls
`waveOutReset`, which marks every queued buffer done — so it both silences the
device and releases the poll loop, and one mechanism covers finishing normally
and being interrupted. `Stop` is safe to call from another goroutine and safe to
call when nothing is playing, because what calls it is a Ctrl-C handler.

`runtime.KeepAlive` holds the PCM slice across the poll loop: the driver reads
that memory for the length of playback, which is longer than the compiler can
prove the slice is referenced.

## Testing

`Player` is an interface, so the command layer is tested without making a sound.
Tone generation, device matching and the step plan are all in the cross-platform
`shaker.go` and covered directly — including that the first and last sample of
every tone is zero, which is the envelope property that protects the hardware.
