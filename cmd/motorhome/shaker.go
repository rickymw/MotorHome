package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rickymw/MotorHome/internal/shaker"
)

var (
	shakerOut    io.Writer = os.Stdout
	shakerErrOut io.Writer = os.Stderr

	// Swappable for tests, which must not make a sound.
	newShakerPlayer = shaker.NewPlayer

	// shakerSleep is the between-step pause, indirected so tests do not wait.
	shakerSleep = time.Sleep
)

func shakerPrintf(format string, a ...any) { fmt.Fprintf(shakerOut, format, a...) }
func shakerErrf(format string, a ...any)   { fmt.Fprintf(shakerErrOut, format+"\n", a...) }

func isShakerAction(s string) bool {
	switch s {
	case "devices", "list", "test", "tone":
		return true
	}
	return false
}

// RunShaker tests a tactile transducer through its audio output device,
// returning the process exit code.
//
// The default action is `devices` rather than `test`: this command makes the
// seat move, and a bare `motorhome shaker` typed to see what it does should not
// be what starts that.
func RunShaker(args []string) int {
	action := "devices"
	rest := args
	if len(args) > 0 && isShakerAction(args[0]) {
		action, rest = args[0], args[1:]
	}

	fs := flag.NewFlagSet("shaker "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	device := fs.String("device", shaker.DefaultDeviceMatch, "output device name substring")
	freq := fs.Float64("freq", shaker.DefaultFreq, "tone frequency in Hz")
	secs := fs.Float64("secs", 1.5, "seconds per tone")
	level := fs.Float64("level", 0.05, "amplitude 0-1 (tone action only)")
	max := fs.Float64("max", 0.5, "highest amplitude the ramp reaches (test action only)")
	gap := fs.Float64("gap", 0.8, "seconds of silence between steps")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome shaker [devices]")
		fmt.Fprintln(os.Stderr, "       motorhome shaker test [-device S] [-freq Hz] [-secs N] [-max 0-1]")
		fmt.Fprintln(os.Stderr, "       motorhome shaker tone [-device S] [-freq Hz] [-secs N] [-level 0-1]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	player := newShakerPlayer()
	devs, err := player.Devices()
	if err != nil {
		shakerErrf("cannot enumerate audio output devices: %v", err)
		return 1
	}

	if action == "devices" || action == "list" {
		shakerPrintf("%s", shaker.FormatDevices(devs))
		reportShakerTarget(devs, *device)
		return 0
	}

	target, err := shaker.FindDevice(devs, *device)
	if err != nil {
		shakerErrf("shaker %s: %v", action, err)
		return 1
	}

	var plan []shaker.Tone
	switch action {
	case "tone":
		plan = []shaker.Tone{{
			Freq: *freq, Seconds: *secs, Level: *level, FadeMS: shaker.DefaultFadeMS,
		}}
	case "test":
		plan = shaker.StepPlan(shaker.DefaultSteps, *freq, *secs, *max)
		if len(plan) == 0 {
			shakerErrf("shaker test: -max %.2f is below the gentlest step (%.2f)", *max, shaker.DefaultSteps[0])
			return 1
		}
	}

	// Validate the whole plan before making any sound, so a bad -freq is a
	// message rather than one tone followed by a message.
	for _, t := range plan {
		if err := t.Validate(); err != nil {
			shakerErrf("shaker %s: %v", action, err)
			return 1
		}
	}

	return playShakerPlan(player, target, plan, action, *gap)
}

// reportShakerTarget says whether the device the test would pick is present,
// so `shaker devices` answers the question that precedes running a test.
func reportShakerTarget(devs []shaker.Device, match string) {
	target, err := shaker.FindDevice(devs, match)
	if err != nil {
		shakerPrintf("\n  No device matches %q — `shaker test` would refuse to run.\n", match)
		shakerPrintf("  Pass -device with a substring of the right name above.\n")
		return
	}
	shakerPrintf("\n  `shaker test` would play to: %s (device %d)\n", target.Name, target.ID)
}

// playShakerPlan runs the tones, stopping the moment Ctrl-C arrives.
//
// The signal handler calls Stop rather than letting the default handler kill
// the process: an interrupt during playback would otherwise leave the device
// open with a buffer queued, and the tone would keep going after the command
// had apparently exited.
func playShakerPlan(player shaker.Player, target shaker.Device, plan []shaker.Tone, action string, gap float64) int {
	var aborted atomic.Bool

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			aborted.Store(true)
			player.Stop()
		case <-done:
		}
	}()

	shakerPrintf("Transducer %s — %s\n\n", action, target.Name)
	if action == "test" {
		shakerPrintf("Stepping up from near-silence. Ctrl-C cuts the output immediately —\n")
		shakerPrintf("use it at the first sign of anything wrong, including a smell.\n\n")
	}

	played := 0
	for i, t := range plan {
		if aborted.Load() {
			break
		}
		shakerPrintf("  %6s  %5.1f Hz  %.1fs\n", formatLevel(t.Level), t.Freq, t.Seconds)

		if err := player.Play(target.ID, t.PCM(shaker.DefaultFormat), shaker.DefaultFormat); err != nil {
			shakerErrf("  [!] playback failed: %v", err)
			return 1
		}
		played++

		if i < len(plan)-1 && !aborted.Load() {
			shakerSleep(time.Duration(gap * float64(time.Second)))
		}
	}

	noun := "steps"
	if len(plan) == 1 {
		noun = "tone"
	}

	if aborted.Load() {
		shakerPrintf("\nStopped after %d of %d %s. Output cut.\n", played, len(plan), noun)
		return 1
	}

	// Deliberately narrow about what this proves. Windows accepting a buffer
	// says the PC-to-amplifier path works; it cannot say the transducer moved,
	// and reporting a clean run as "working" would be exactly the wrong thing
	// to tell someone testing hardware they suspect is faulty.
	shakerPrintf("\nDone — %d %s played to %s.\n", played, noun, target.Name)
	shakerPrintf("Windows accepted and played every buffer, so the path from PC to amplifier\n")
	shakerPrintf("is working. Whether the transducer actually moved is the half only you can\n")
	shakerPrintf("confirm, and none of it says the amplifier is electrically sound.\n")
	return 0
}

// formatLevel renders an amplitude as a percentage without trailing zeros, so
// the ramp reads "2%" while a deliberately tiny diagnostic level still reads as
// something other than "0%".
func formatLevel(level float64) string {
	s := strconv.FormatFloat(level*100, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		s = "0"
	}
	return s + "%"
}
