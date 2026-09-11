package shaker

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func testFormat() Format { return Format{SampleRate: 48000, Channels: 2, BitsPer: 16} }

func TestFormatArithmetic(t *testing.T) {
	f := testFormat()
	if got := f.BlockAlign(); got != 4 {
		t.Errorf("BlockAlign = %d, want 4", got)
	}
	if got := f.AvgBytesPerSec(); got != 192000 {
		t.Errorf("AvgBytesPerSec = %d, want 192000", got)
	}
}

// samples decodes interleaved PCM back to per-frame values, asserting both
// channels carry the same signal.
func samples(t *testing.T, pcm []byte, f Format) []int16 {
	t.Helper()
	if len(pcm)%f.BlockAlign() != 0 {
		t.Fatalf("PCM length %d is not a whole number of frames", len(pcm))
	}
	out := make([]int16, 0, len(pcm)/f.BlockAlign())
	for i := 0; i < len(pcm); i += f.BlockAlign() {
		l := int16(binary.LittleEndian.Uint16(pcm[i:]))
		r := int16(binary.LittleEndian.Uint16(pcm[i+2:]))
		if l != r {
			t.Fatalf("frame %d: channels differ (%d vs %d)", i/f.BlockAlign(), l, r)
		}
		out = append(out, l)
	}
	return out
}

func TestPCMLengthAndFormat(t *testing.T) {
	f := testFormat()
	tone := Tone{Freq: 40, Seconds: 0.5, Level: 0.5, FadeMS: DefaultFadeMS}

	got := samples(t, tone.PCM(f), f)
	if want := f.SampleRate / 2; len(got) != want {
		t.Errorf("got %d frames, want %d", len(got), want)
	}
}

// TestPCMIsEnveloped is the safety-relevant property: a sine that begins at
// full amplitude is a DC step, which a transducer reproduces as a mechanical
// slam rather than a click.
func TestPCMIsEnveloped(t *testing.T) {
	f := testFormat()
	tone := Tone{Freq: 40, Seconds: 1, Level: 1, FadeMS: DefaultFadeMS}
	got := samples(t, tone.PCM(f), f)

	if got[0] != 0 {
		t.Errorf("first sample = %d, want 0", got[0])
	}
	if last := got[len(got)-1]; last != 0 {
		t.Errorf("last sample = %d, want 0", last)
	}

	// Peak must still be reached in the sustained middle.
	var peak int16
	for _, s := range got[len(got)/3 : 2*len(got)/3] {
		if s > peak {
			peak = s
		}
	}
	if peak < math.MaxInt16*9/10 {
		t.Errorf("mid-tone peak %d never approaches full scale — envelope is eating the tone", peak)
	}
}

// TestPCMShortToneStillRamps covers the case the envelope maths has to degrade
// gracefully for: a tone shorter than two fades must not clip back to a step,
// because the shortest tones are the ones used on suspect hardware.
func TestPCMShortToneStillRamps(t *testing.T) {
	f := testFormat()
	tone := Tone{Freq: 40, Seconds: 0.01, Level: 1, FadeMS: DefaultFadeMS} // 10ms vs 150ms fades
	got := samples(t, tone.PCM(f), f)

	if len(got) == 0 {
		t.Fatal("no samples produced")
	}
	if got[0] != 0 {
		t.Errorf("first sample = %d, want 0 even when the tone is shorter than the fade", got[0])
	}
	if last := got[len(got)-1]; last != 0 {
		t.Errorf("last sample = %d, want 0", last)
	}
}

func TestPCMLevelScalesAmplitude(t *testing.T) {
	f := testFormat()
	peakAt := func(level float64) int16 {
		var peak int16
		for _, s := range samples(t, Tone{Freq: 40, Seconds: 1, Level: level, FadeMS: 0}.PCM(f), f) {
			if s > peak {
				peak = s
			}
		}
		return peak
	}

	quiet, loud := peakAt(0.1), peakAt(0.5)
	if quiet >= loud {
		t.Errorf("peak at 10%% (%d) should be below peak at 50%% (%d)", quiet, loud)
	}
	// Roughly linear: a fifth of the amplitude, within rounding.
	if ratio := float64(loud) / float64(quiet); ratio < 4.5 || ratio > 5.5 {
		t.Errorf("amplitude ratio %.2f, want about 5", ratio)
	}
}

func TestPCMZeroDuration(t *testing.T) {
	if got := (Tone{Freq: 40, Seconds: 0, Level: 1}).PCM(testFormat()); got != nil {
		t.Errorf("expected nil PCM for a zero-length tone, got %d bytes", len(got))
	}
}

func TestToneValidate(t *testing.T) {
	tests := []struct {
		name string
		tone Tone
		ok   bool
	}{
		{"typical", Tone{Freq: 40, Seconds: 2, Level: 0.5}, true},
		{"at the bounds", Tone{Freq: MinFreq, Seconds: MaxSeconds, Level: 1}, true},
		{"too low", Tone{Freq: MinFreq - 1, Seconds: 2, Level: 0.5}, false},
		{"too high", Tone{Freq: MaxFreq + 1, Seconds: 2, Level: 0.5}, false},
		{"too long", Tone{Freq: 40, Seconds: MaxSeconds + 1, Level: 0.5}, false},
		{"zero duration", Tone{Freq: 40, Seconds: 0, Level: 0.5}, false},
		{"silent", Tone{Freq: 40, Seconds: 2, Level: 0}, false},
		{"over full scale", Tone{Freq: 40, Seconds: 2, Level: 1.5}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tone.Validate()
			if tc.ok && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func testOutputs() []Device {
	return []Device{
		{ID: 0, Name: "Speakers (Razer Clio Surround)", Channels: 2},
		{ID: 1, Name: "Headphones (KT USB Audio)", Channels: 2},
		{ID: 2, Name: "OnBoard (Realtek(R) Audio)", Channels: 2},
		{ID: 3, Name: "Speakers (Sound Blaster X4)", Channels: 2},
	}
}

func TestFindDeviceMatchesButtKicker(t *testing.T) {
	got, err := FindDevice(testOutputs(), DefaultDeviceMatch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("got device %d (%s), want 1", got.ID, got.Name)
	}
}

func TestFindDeviceIsCaseInsensitive(t *testing.T) {
	got, err := FindDevice(testOutputs(), "kt usb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != 1 {
		t.Errorf("got device %d, want 1", got.ID)
	}
}

// TestFindDeviceRefusesAmbiguous is the one that matters most here: guessing
// wrong does not print a wrong table, it plays a low-frequency tone into
// whatever it picked.
func TestFindDeviceRefusesAmbiguous(t *testing.T) {
	_, err := FindDevice(testOutputs(), "Speakers")
	if err == nil {
		t.Fatal("expected an error for a substring matching two devices")
	}
	if !strings.Contains(err.Error(), "Razer") || !strings.Contains(err.Error(), "Sound Blaster") {
		t.Errorf("expected both candidates named, got: %v", err)
	}
}

func TestFindDeviceNoMatchListsDevices(t *testing.T) {
	_, err := FindDevice(testOutputs(), "ButtKicker")
	if err == nil {
		t.Fatal("expected an error when nothing matches")
	}
	// The name people look for is not the name Windows reports, so the error
	// has to show what is actually there.
	if !strings.Contains(err.Error(), "KT USB Audio") {
		t.Errorf("expected the device list in the error, got: %v", err)
	}
}

func TestFindDeviceEmptyMatch(t *testing.T) {
	if _, err := FindDevice(testOutputs(), "  "); err == nil {
		t.Fatal("expected an error for an empty match")
	}
}

func TestFindDeviceNoDevicesAtAll(t *testing.T) {
	if _, err := FindDevice(nil, DefaultDeviceMatch); err == nil {
		t.Fatal("expected an error when there are no output devices")
	}
}

func TestStepPlanRamps(t *testing.T) {
	plan := StepPlan(DefaultSteps, 40, 1.5, 0.5)
	if len(plan) != len(DefaultSteps) {
		t.Fatalf("got %d steps, want %d", len(plan), len(DefaultSteps))
	}
	for i := 1; i < len(plan); i++ {
		if plan[i].Level <= plan[i-1].Level {
			t.Errorf("step %d (%.2f) does not rise above step %d (%.2f)", i, plan[i].Level, i-1, plan[i-1].Level)
		}
	}
	if plan[0].Level != DefaultSteps[0] {
		t.Errorf("ramp starts at %.3f, want the gentlest step %.3f", plan[0].Level, DefaultSteps[0])
	}
	for _, tone := range plan {
		if err := tone.Validate(); err != nil {
			t.Errorf("generated step is invalid: %v", err)
		}
	}
}

// TestStepPlanDropsRatherThanClamps: clamping would replay the same level and
// announce each as a step up, which is the wrong thing to show someone watching
// for where a fault appears.
func TestStepPlanDropsRatherThanClamps(t *testing.T) {
	plan := StepPlan(DefaultSteps, 40, 1.5, 0.1)

	seen := map[float64]bool{}
	for _, tone := range plan {
		if tone.Level > 0.1 {
			t.Errorf("step at %.2f exceeds the max of 0.10", tone.Level)
		}
		if seen[tone.Level] {
			t.Errorf("level %.2f is played twice", tone.Level)
		}
		seen[tone.Level] = true
	}
	if len(plan) != 3 { // 0.02, 0.05, 0.10
		t.Errorf("got %d steps under a 0.10 max, want 3", len(plan))
	}
}

func TestStepPlanEmptyWhenMaxBelowGentlest(t *testing.T) {
	if plan := StepPlan(DefaultSteps, 40, 1.5, 0.001); len(plan) != 0 {
		t.Errorf("got %d steps, want none below the gentlest level", len(plan))
	}
}

func TestFormatDevicesEmpty(t *testing.T) {
	if got := FormatDevices(nil); !strings.Contains(got, "no audio output devices") {
		t.Errorf("expected an explicit empty message, got: %q", got)
	}
}
