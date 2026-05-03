//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/iracing"
)

// RunLive implements the "live" subcommand. It reads a snapshot from iRacing
// shared memory and prints the player's current track position along with the
// car directly ahead and behind on track (with time gaps in seconds). Default
// mode is one-shot; `-watch` streams at the configured rate until Ctrl-C.
//
// `-raw` is a diagnostic mode that dumps every field of the LiveData struct
// instead of the formatted gap display — used for troubleshooting if the
// formatted view is empty or obviously wrong.
func RunLive(args []string, _ config.Config) {
	fs := flag.NewFlagSet("live", flag.ExitOnError)
	watch := fs.Bool("watch", false, "poll continuously and print one line per sample (Ctrl-C to stop)")
	hz := fs.Int("hz", 5, "poll rate in Hz when -watch is set (1–60)")
	raw := fs.Bool("raw", false, "print raw LiveData fields (diagnostic) instead of the formatted gap view")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] live [-watch] [-hz N] [-raw]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Reads iRacing shared memory and prints your track position with gaps")
		fmt.Fprintln(os.Stderr, "to the car directly ahead and behind on track.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *raw {
		runLiveRaw(*watch, *hz)
		return
	}

	if !*watch {
		printGapView(iracing.ReadLiveData())
		return
	}

	if *hz < 1 {
		*hz = 1
	}
	if *hz > 60 {
		*hz = 60
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(time.Second / time.Duration(*hz))
	defer tick.Stop()

	fmt.Printf("polling iRacing at %d Hz — Ctrl-C to stop\n", *hz)
	for {
		select {
		case <-sig:
			return
		case <-tick.C:
			printGapLine(iracing.ReadLiveData())
		}
	}
}

func runLiveRaw(watch bool, hz int) {
	if !watch {
		printSnapshotVerbose(iracing.ReadLiveData())
		return
	}
	if hz < 1 {
		hz = 1
	}
	if hz > 60 {
		hz = 60
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	tick := time.NewTicker(time.Second / time.Duration(hz))
	defer tick.Stop()
	fmt.Printf("polling iRacing at %d Hz (raw) — Ctrl-C to stop\n", hz)
	fmt.Println("  conn  sessionTime  lapDistPct  track / car / errMsg")
	for {
		select {
		case <-sig:
			return
		case <-tick.C:
			printSnapshotCompact(iracing.ReadLiveData())
		}
	}
}

// printGapView prints a multi-line summary: track/car, position, lap %, and
// gaps to the cars directly ahead and behind on track.
func printGapView(ld iracing.LiveData) {
	if !ld.Connected {
		msg := ld.ErrMsg
		if msg == "" {
			msg = "iRacing not connected (not running or not on-track)"
		}
		fmt.Println(msg)
		return
	}
	fmt.Printf("Track : %s | Car: %s\n", ld.Track, ld.Car)
	fmt.Println(formatPositionLine(ld))
	ahead, behind := gapsFromLive(ld)
	fmt.Println("Ahead : " + formatGap(ld, ahead))
	fmt.Println("Behind: " + formatGap(ld, behind))
}

// printGapLine prints a single-line summary used by `-watch` mode.
func printGapLine(ld iracing.LiveData) {
	if !ld.Connected {
		msg := ld.ErrMsg
		if msg == "" {
			msg = "not connected"
		}
		fmt.Printf("[%s]\n", msg)
		return
	}
	ahead, behind := gapsFromLive(ld)
	fmt.Printf("%s  |  Ahead %s  |  Behind %s\n",
		formatPositionLine(ld),
		formatGap(ld, ahead),
		formatGap(ld, behind),
	)
}

func formatPositionLine(ld iracing.LiveData) string {
	pos, classPos, lap := int32(0), int32(0), int32(0)
	if ld.MyCarIdx >= 0 {
		if p := idxValue(ld.CarIdxPosition, ld.MyCarIdx); p > 0 {
			pos = p
		}
		if cp := idxValue(ld.CarIdxClassPosition, ld.MyCarIdx); cp > 0 {
			classPos = cp
		}
		// iRacing publishes -1 before the first S/F crossing; display that as
		// lap 1 (the out-lap), then LapCompleted+1 thereafter.
		lc := idxValue(ld.CarIdxLapCompleted, ld.MyCarIdx)
		if lc < 0 {
			lap = 1
		} else {
			lap = lc + 1
		}
	}
	gridSize := countValidCars(ld.CarIdxLapDistPct)
	// Best-effort "class total" — count cars sharing my class id.
	classTotal := 0
	if ld.MyCarIdx >= 0 {
		if mine, ok := ld.Drivers[ld.MyCarIdx]; ok && mine.CarClassID != 0 {
			for _, d := range ld.Drivers {
				if d.CarClassID == mine.CarClassID {
					classTotal++
				}
			}
		}
	}
	posStr := "?"
	if pos > 0 {
		if gridSize > 0 {
			posStr = fmt.Sprintf("%d/%d", pos, gridSize)
		} else {
			posStr = fmt.Sprintf("%d", pos)
		}
	}
	classStr := ""
	if classPos > 0 && classTotal > 0 {
		classStr = fmt.Sprintf(" (class %d/%d)", classPos, classTotal)
	}
	lapStr := "?"
	if lap > 0 {
		lapStr = fmt.Sprintf("%d", lap)
	}
	return fmt.Sprintf("Pos %s%s  Lap %s @ %5.1f%%", posStr, classStr, lapStr, ld.LapDistPct*100)
}

func formatGap(ld iracing.LiveData, g iracing.GapTo) string {
	if g.CarIdx < 0 {
		return "(none)"
	}
	name := "?"
	number := "?"
	if d, ok := ld.Drivers[g.CarIdx]; ok {
		if d.UserName != "" {
			name = d.UserName
		}
		if d.CarNumber != "" {
			number = d.CarNumber
		}
	}
	sign := "+"
	if g.TimeSeconds < 0 {
		sign = "-"
	}
	lapNote := ""
	if g.LapsDelta > 0 {
		lapNote = fmt.Sprintf(" (+%d lap)", g.LapsDelta)
	} else if g.LapsDelta < 0 {
		lapNote = fmt.Sprintf(" (%d lap)", g.LapsDelta)
	}
	return fmt.Sprintf("#%-4s %-20s %s%.3fs%s", number, truncate(name, 20), sign, absFloat(g.TimeSeconds), lapNote)
}

// gapsFromLive picks out the player's CarPos and every other valid car, then
// delegates to iracing.ComputeGaps. Uses me.EstTime as the lap-time estimate
// when it looks sane (>5s at lap end); falls back to 90s when no EstTime is
// available so the gap stays on the same order of magnitude.
func gapsFromLive(ld iracing.LiveData) (iracing.GapTo, iracing.GapTo) {
	if ld.MyCarIdx < 0 {
		return iracing.GapTo{}, iracing.GapTo{}
	}
	me := iracing.CarPos{
		CarIdx:       ld.MyCarIdx,
		LapDistPct:   idxValueF(ld.CarIdxLapDistPct, ld.MyCarIdx),
		LapCompleted: idxValue(ld.CarIdxLapCompleted, ld.MyCarIdx),
		EstTime:      idxValueF(ld.CarIdxEstTime, ld.MyCarIdx),
	}
	others := make([]iracing.CarPos, 0, len(ld.CarIdxLapDistPct))
	for i := range ld.CarIdxLapDistPct {
		idx := int32(i)
		if idx == ld.MyCarIdx {
			continue
		}
		pct := ld.CarIdxLapDistPct[i]
		if pct < 0 {
			continue
		}
		others = append(others, iracing.CarPos{
			CarIdx:       idx,
			LapDistPct:   pct,
			LapCompleted: idxValue(ld.CarIdxLapCompleted, idx),
			EstTime:      idxValueF(ld.CarIdxEstTime, idx),
		})
	}
	// Total lap time estimate = EstTime / LapDistPct. This holds for any
	// point on the lap (not just near the S/F line) because EstTime is the
	// time from S/F to the current position on a representative lap.
	lapEst := float32(90)
	if me.EstTime > 1 && me.LapDistPct > 0.01 {
		lapEst = me.EstTime / me.LapDistPct
	}
	return iracing.ComputeGaps(me, others, lapEst)
}

func idxValue(arr []int32, i int32) int32 {
	if i < 0 || int(i) >= len(arr) {
		return 0
	}
	return arr[i]
}

func idxValueF(arr []float32, i int32) float32 {
	if i < 0 || int(i) >= len(arr) {
		return 0
	}
	return arr[i]
}

func countValidCars(arr []float32) int {
	n := 0
	for _, v := range arr {
		if v >= 0 {
			n++
		}
	}
	return n
}

func absFloat(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func printSnapshotVerbose(ld iracing.LiveData) {
	fmt.Printf("Connected    : %v\n", ld.Connected)
	fmt.Printf("SessionTime  : %.3f s\n", ld.SessionTime)
	fmt.Printf("LapDistPct   : %.4f\n", ld.LapDistPct)
	fmt.Printf("Track        : %q\n", ld.Track)
	fmt.Printf("Car          : %q\n", ld.Car)
	fmt.Printf("MyCarIdx     : %d\n", ld.MyCarIdx)
	fmt.Printf("Drivers      : %d entries\n", len(ld.Drivers))
	for idx, d := range ld.Drivers {
		fmt.Printf("  [%d] #%s %q — %q class=%d\n", idx, d.CarNumber, d.UserName, d.CarScreenName, d.CarClassID)
	}
	fmt.Printf("CarIdx arrays: LapDistPct=%d LapCompleted=%d EstTime=%d Position=%d ClassPos=%d\n",
		len(ld.CarIdxLapDistPct), len(ld.CarIdxLapCompleted), len(ld.CarIdxEstTime),
		len(ld.CarIdxPosition), len(ld.CarIdxClassPosition))
	if ld.MyCarIdx >= 0 && int(ld.MyCarIdx) < len(ld.CarIdxLapDistPct) {
		i := ld.MyCarIdx
		fmt.Printf("Me (idx %d)  : LapDistPct=%.4f LapCompleted=%d EstTime=%.3f Pos=%d ClassPos=%d\n",
			i, idxValueF(ld.CarIdxLapDistPct, i), idxValue(ld.CarIdxLapCompleted, i),
			idxValueF(ld.CarIdxEstTime, i), idxValue(ld.CarIdxPosition, i), idxValue(ld.CarIdxClassPosition, i))
	}
	// Show every CarIdx with a valid LapDistPct (>= 0) to reveal how many
	// cars iRacing is actually tracking in this session.
	valid := 0
	for i, pct := range ld.CarIdxLapDistPct {
		if pct < 0 {
			continue
		}
		valid++
		fmt.Printf("  car %2d: pct=%.4f lap=%d est=%.3f pos=%d\n",
			i, pct, idxValue(ld.CarIdxLapCompleted, int32(i)),
			idxValueF(ld.CarIdxEstTime, int32(i)), idxValue(ld.CarIdxPosition, int32(i)))
	}
	fmt.Printf("Valid cars   : %d\n", valid)
	if ld.ErrMsg != "" {
		fmt.Printf("ErrMsg       : %s\n", ld.ErrMsg)
	}
}

func printSnapshotCompact(ld iracing.LiveData) {
	conn := "F"
	if ld.Connected {
		conn = "T"
	}
	detail := ld.ErrMsg
	if detail == "" {
		detail = fmt.Sprintf("%s / %s", ld.Track, ld.Car)
	}
	fmt.Printf("  %s     %12.3f  %10.4f  %s\n", conn, ld.SessionTime, ld.LapDistPct, detail)
}
