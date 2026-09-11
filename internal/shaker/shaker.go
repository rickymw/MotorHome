// Package shaker drives a tactile transducer (ButtKicker or similar) through
// its audio output device, so it can be tested without launching a sim.
//
// A transducer is a speaker that moves a seat rather than air, so testing one
// means playing a low-frequency tone at it. Two things follow from that, and
// they shape this whole package:
//
// Playing to the *wrong* device is the failure worth engineering against. A
// 40 Hz tone sent to headphones is unpleasant at best; sent to a full-range
// speaker at level it is a way to damage a woofer. So there is no "default
// output device" fallback anywhere here — the device is matched by name, and
// not finding it is an error that lists what was found.
//
// Amplitude is stepped from near-silence upward rather than jumping to a set
// level, because the first test after a transducer has sat unused is also the
// moment a fault shows up. Every tone is enveloped: a sine that starts at full
// amplitude is a DC step, which in a transducer is a mechanical slam rather
// than a click.
package shaker

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// DefaultDeviceMatch is the substring identifying a ButtKicker's USB audio
// interface. Its amplifier presents as a generic USB audio device under the
// manufacturer's name rather than anything containing "ButtKicker".
const DefaultDeviceMatch = "KT USB Audio"

// Frequency and duration bounds. A tactile transducer works from a few Hz to
// a couple of hundred; outside that a tone either does nothing or is being
// asked of hardware not built to reproduce it. The duration cap exists because
// this is a test, and an unattended transducer humming for minutes is how a
// suspect amplifier gets cooked.
const (
	MinFreq    = 5.0
	MaxFreq    = 200.0
	MaxSeconds = 30.0

	// DefaultFreq sits in the usable band for most transducers without being
	// at the violent end of it.
	DefaultFreq = 40.0

	// DefaultFadeMS envelopes every tone. Long enough to remove the step,
	// short enough not to hide a brief test.
	DefaultFadeMS = 150
)

// DefaultSteps is the gentle ramp: amplitude as a fraction of full scale.
//
// It starts at 2%, which on most amplifier gain settings is barely perceptible,
// and stops at 50% rather than 100% — the question being answered is "does this
// work", and that is settled long before full output. Going louder is opt-in.
var DefaultSteps = []float64{0.02, 0.05, 0.10, 0.20, 0.35, 0.50}

// Format is the PCM format played to the device.
//
// 48 kHz 16-bit stereo is what USB audio interfaces accept without negotiation.
// Both channels carry the same signal: a transducer amplifier may take either
// one or sum the pair, and which it does is not worth discovering during a test
// of whether it works at all.
type Format struct {
	SampleRate int
	Channels   int
	BitsPer    int
}

// DefaultFormat is the format every tone here is generated in.
var DefaultFormat = Format{SampleRate: 48000, Channels: 2, BitsPer: 16}

// BlockAlign is the size in bytes of one sample across all channels.
func (f Format) BlockAlign() int { return f.Channels * (f.BitsPer / 8) }

// AvgBytesPerSec is the byte rate of the format.
func (f Format) AvgBytesPerSec() int { return f.SampleRate * f.BlockAlign() }

// Device is an audio output device.
type Device struct {
	ID       int
	Name     string
	Channels int
}

// Player enumerates output devices and plays PCM to one. It is an interface so
// the command layer can be tested without making a sound.
type Player interface {
	Devices() ([]Device, error)
	// Play blocks until the buffer has finished or Stop is called.
	Play(deviceID int, pcm []byte, f Format) error
	// Stop halts playback immediately. It is safe to call from another
	// goroutine and safe to call when nothing is playing — it is what a
	// Ctrl-C handler calls, which is the whole reason playback is abortable.
	Stop()
}

// Tone is a single enveloped sine burst.
type Tone struct {
	Freq    float64 // Hz
	Seconds float64
	Level   float64 // amplitude as a fraction of full scale, 0..1
	FadeMS  int
}

// Validate reports whether the tone is within the safe bounds of the package.
func (t Tone) Validate() error {
	if t.Freq < MinFreq || t.Freq > MaxFreq {
		return fmt.Errorf("frequency %.1f Hz is outside the %.0f–%.0f Hz range a transducer can use", t.Freq, MinFreq, MaxFreq)
	}
	if t.Seconds <= 0 || t.Seconds > MaxSeconds {
		return fmt.Errorf("duration %.1fs is outside 0–%.0fs", t.Seconds, MaxSeconds)
	}
	if t.Level <= 0 || t.Level > 1 {
		return fmt.Errorf("level %.3f is outside 0–1", t.Level)
	}
	return nil
}

// PCM renders the tone to signed 16-bit little-endian samples.
//
// The envelope is applied in the sample loop rather than as a post-pass so that
// a tone shorter than two fades still ramps — it degrades to a triangle rather
// than clipping back to a step, which is the case that matters, since the
// shortest tones are the ones used to check a suspect amplifier.
func (t Tone) PCM(f Format) []byte {
	n := int(float64(f.SampleRate) * t.Seconds)
	if n <= 0 {
		return nil
	}

	fade := f.SampleRate * t.FadeMS / 1000
	if fade*2 > n {
		fade = n / 2
	}

	out := make([]byte, n*f.BlockAlign())
	step := 2 * math.Pi * t.Freq / float64(f.SampleRate)

	for i := 0; i < n; i++ {
		env := 1.0
		if fade > 0 {
			if i < fade {
				env = float64(i) / float64(fade)
			} else if i >= n-fade {
				env = float64(n-1-i) / float64(fade)
			}
		}
		if env < 0 {
			env = 0
		}

		v := int16(math.Sin(step*float64(i)) * t.Level * env * math.MaxInt16)
		base := i * f.BlockAlign()
		for c := 0; c < f.Channels; c++ {
			binary.LittleEndian.PutUint16(out[base+c*2:], uint16(v))
		}
	}
	return out
}

// FindDevice returns the one output device whose name contains match.
//
// It refuses to guess, like `pb show` and `usb`: several matches is an error
// listing them. The stakes are higher here than elsewhere in this tool, because
// guessing wrong does not print the wrong table — it plays a low-frequency tone
// into whatever it picked.
func FindDevice(devs []Device, match string) (Device, error) {
	match = strings.TrimSpace(match)
	if match == "" {
		return Device{}, fmt.Errorf("no output device named")
	}

	var hits []Device
	lower := strings.ToLower(match)
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Name), lower) {
			hits = append(hits, d)
		}
	}

	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Device{}, fmt.Errorf("no audio output device matches %q\n%s", match, FormatDevices(devs))
	default:
		var names []string
		for _, d := range hits {
			names = append(names, fmt.Sprintf("%d (%s)", d.ID, d.Name))
		}
		return Device{}, fmt.Errorf("%q matches %d output devices: %s\nbe more specific", match, len(hits), strings.Join(names, ", "))
	}
}

// FormatDevices renders the output device list.
func FormatDevices(devs []Device) string {
	if len(devs) == 0 {
		return "  (no audio output devices found)\n"
	}
	var b strings.Builder
	b.WriteString("  Audio output devices:\n")
	for _, d := range devs {
		b.WriteString(fmt.Sprintf("  %3d  %s\n", d.ID, d.Name))
	}
	return b.String()
}

// StepPlan builds the ramp of tones for a gentle test.
//
// Steps above max are dropped rather than clamped: clamping would play the same
// level several times over and report each as a step up, which is exactly the
// wrong thing to tell someone watching for the point where a fault appears.
func StepPlan(steps []float64, freq, seconds float64, max float64) []Tone {
	var out []Tone
	for _, level := range steps {
		if level > max {
			continue
		}
		out = append(out, Tone{Freq: freq, Seconds: seconds, Level: level, FadeMS: DefaultFadeMS})
	}
	return out
}
