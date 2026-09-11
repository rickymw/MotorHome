//go:build windows

package shaker

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	modWinMM = syscall.NewLazyDLL("winmm.dll")

	procWaveOutGetNumDevs   = modWinMM.NewProc("waveOutGetNumDevs")
	procWaveOutGetDevCapsW  = modWinMM.NewProc("waveOutGetDevCapsW")
	procWaveOutOpen         = modWinMM.NewProc("waveOutOpen")
	procWaveOutPrepHeader   = modWinMM.NewProc("waveOutPrepareHeader")
	procWaveOutWrite        = modWinMM.NewProc("waveOutWrite")
	procWaveOutUnprepHeader = modWinMM.NewProc("waveOutUnprepareHeader")
	procWaveOutReset        = modWinMM.NewProc("waveOutReset")
	procWaveOutClose        = modWinMM.NewProc("waveOutClose")
)

const (
	waveFmtPCM    = 1
	callbackNull  = 0
	mmSysErrNoErr = 0

	// whdrDone is set by the driver once the buffer has finished playing. It
	// is also what waveOutReset sets on every queued buffer, so the same poll
	// loop covers both finishing and being stopped.
	whdrDone = 0x00000001

	// maxPNameLen is MAXPNAMELEN: device names are truncated to this many
	// wide characters by the API, not by us.
	maxPNameLen = 32

	pollInterval = 10 * time.Millisecond
)

// waveOutCaps mirrors WAVEOUTCAPSW.
type waveOutCaps struct {
	WMid           uint16
	WPid           uint16
	VDriverVersion uint32
	SzPname        [maxPNameLen]uint16
	DwFormats      uint32
	WChannels      uint16
	WReserved1     uint16
	DwSupport      uint32
}

// waveFormatEx mirrors WAVEFORMATEX.
type waveFormatEx struct {
	WFormatTag      uint16
	NChannels       uint16
	NSamplesPerSec  uint32
	NAvgBytesPerSec uint32
	NBlockAlign     uint16
	WBitsPerSample  uint16
	CbSize          uint16
}

// waveHdr mirrors WAVEHDR.
type waveHdr struct {
	LpData          uintptr
	DwBufferLength  uint32
	DwBytesRecorded uint32
	DwUser          uintptr
	DwFlags         uint32
	DwLoops         uint32
	LpNext          uintptr
	Reserved        uintptr
}

type winPlayer struct {
	mu     sync.Mutex
	handle uintptr
	open   bool
}

// NewPlayer returns the Windows implementation of Player.
func NewPlayer() Player { return &winPlayer{} }

func (p *winPlayer) Devices() ([]Device, error) {
	n, _, _ := procWaveOutGetNumDevs.Call()
	count := int(n)
	if count == 0 {
		return nil, nil
	}

	devs := make([]Device, 0, count)
	for i := 0; i < count; i++ {
		var caps waveOutCaps
		ret, _, _ := procWaveOutGetDevCapsW.Call(
			uintptr(i),
			uintptr(unsafe.Pointer(&caps)),
			unsafe.Sizeof(caps),
		)
		if ret != mmSysErrNoErr {
			// One unreadable device should not hide the rest — the one being
			// looked for is probably still in the list.
			continue
		}
		devs = append(devs, Device{
			ID:       i,
			Name:     syscall.UTF16ToString(caps.SzPname[:]),
			Channels: int(caps.WChannels),
		})
	}
	return devs, nil
}

func (p *winPlayer) Play(deviceID int, pcm []byte, f Format) error {
	if len(pcm) == 0 {
		return nil
	}

	wfx := waveFormatEx{
		WFormatTag:      waveFmtPCM,
		NChannels:       uint16(f.Channels),
		NSamplesPerSec:  uint32(f.SampleRate),
		NAvgBytesPerSec: uint32(f.AvgBytesPerSec()),
		NBlockAlign:     uint16(f.BlockAlign()),
		WBitsPerSample:  uint16(f.BitsPer),
	}

	var handle uintptr
	ret, _, _ := procWaveOutOpen.Call(
		uintptr(unsafe.Pointer(&handle)),
		uintptr(deviceID),
		uintptr(unsafe.Pointer(&wfx)),
		0, 0, callbackNull,
	)
	if ret != mmSysErrNoErr {
		return mmErr("waveOutOpen", ret)
	}

	p.mu.Lock()
	p.handle, p.open = handle, true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.open = false
		p.handle = 0
		p.mu.Unlock()
		procWaveOutClose.Call(handle)
	}()

	hdr := waveHdr{
		LpData:         uintptr(unsafe.Pointer(&pcm[0])),
		DwBufferLength: uint32(len(pcm)),
	}

	ret, _, _ = procWaveOutPrepHeader.Call(handle, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	if ret != mmSysErrNoErr {
		return mmErr("waveOutPrepareHeader", ret)
	}
	defer procWaveOutUnprepHeader.Call(handle, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))

	ret, _, _ = procWaveOutWrite.Call(handle, uintptr(unsafe.Pointer(&hdr)), unsafe.Sizeof(hdr))
	if ret != mmSysErrNoErr {
		return mmErr("waveOutWrite", ret)
	}

	// The driver reads pcm directly for the length of playback, so it has to
	// outlive this loop rather than however long the compiler can prove the
	// slice is referenced.
	for hdr.DwFlags&whdrDone == 0 {
		time.Sleep(pollInterval)
	}
	runtime.KeepAlive(pcm)

	return nil
}

func (p *winPlayer) Stop() {
	p.mu.Lock()
	handle, open := p.handle, p.open
	p.mu.Unlock()
	if !open {
		return
	}
	// Marks every queued buffer done, which both silences the device and
	// releases the Play poll loop.
	procWaveOutReset.Call(handle)
}

func mmErr(fn string, code uintptr) error {
	return fmt.Errorf("shaker: %s returned error code %d", fn, code)
}
