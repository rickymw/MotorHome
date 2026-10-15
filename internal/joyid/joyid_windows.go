//go:build windows

package joyid

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"unsafe"
)

var (
	winmm               = syscall.NewLazyDLL("winmm.dll")
	procJoyGetDevCapsW  = winmm.NewProc("joyGetDevCapsW")
	procJoyConfigChange = winmm.NewProc("joyConfigChanged")
)

const (
	// diCachePath is the per-user DirectInput device cache, under HKCU.
	diCachePath = `System\CurrentControlSet\Control\MediaProperties\PrivateProperties\DirectInput`

	joystickIDValue = "Joystick Id"

	regBinary = 3

	errNoMoreItems = syscall.Errno(259)

	// winmmSlots is how many joystick IDs the legacy WinMM API addresses.
	winmmSlots = 16
)

// joyCaps is JOYCAPSW. Only the IDs are read, but the struct has to be the full
// size or joyGetDevCapsW rejects the call.
type joyCaps struct {
	mid, pid   uint16
	pname      [32]uint16
	xmin, xmax uint32
	ymin, ymax uint32
	zmin, zmax uint32
	numButtons uint32
	periodMin  uint32
	periodMax  uint32
	rmin, rmax uint32
	umin, umax uint32
	vmin, vmax uint32
	caps       uint32
	maxAxes    uint32
	numAxes    uint32
	maxButtons uint32
	regKey     [32]uint16
	oemVxD     [260]uint16
}

type winStore struct{}

// NewStore returns the Windows implementation. It only reads, and needs no
// elevation: the
// cache lives under HKCU.
func NewStore() Store { return winStore{} }

func openKey(path string, access uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, p, 0, access, &h); err != nil {
		return 0, err
	}
	return h, nil
}

func subkeys(h syscall.Handle) ([]string, error) {
	var out []string
	for i := uint32(0); ; i++ {
		buf := make([]uint16, 256)
		n := uint32(len(buf))
		err := syscall.RegEnumKeyEx(h, i, &buf[0], &n, nil, nil, nil, nil)
		if err == errNoMoreItems {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, syscall.UTF16ToString(buf[:n]))
	}
}

// readID returns the joystick ID stored on one Calibration\<n> key.
func readID(path string) (int, bool) {
	h, err := openKey(path, syscall.KEY_READ)
	if err != nil {
		return None, false
	}
	defer syscall.RegCloseKey(h)

	name, _ := syscall.UTF16PtrFromString(joystickIDValue)
	var typ uint32
	buf := make([]byte, 16)
	n := uint32(len(buf))
	if err := syscall.RegQueryValueEx(h, name, nil, &typ, &buf[0], &n); err != nil || typ != regBinary || n < 4 {
		return None, false
	}
	return int(int32(binary.LittleEndian.Uint32(buf[:4]))), true
}

// idKey finds the Calibration\<n> subkey that carries the device's ID.
//
// A device can have several: each HID collection DirectInput has seen gets one,
// and only the game-controller collection is given an ID. The wheelbase here
// has a vendor-defined collection at Calibration\0 with no ID and its joystick
// at Calibration\1. The lowest-numbered key that holds an ID is the one used,
// so reading and writing always agree on which it is.
func idKey(hw HWID) (path string, id int, ok bool) {
	calPath := diCachePath + `\` + hw.Key() + `\Calibration`
	h, err := openKey(calPath, syscall.KEY_READ)
	if err != nil {
		return "", None, false
	}
	names, _ := subkeys(h)
	syscall.RegCloseKey(h)

	sort.Slice(names, func(i, j int) bool {
		a, errA := strconv.Atoi(names[i])
		b, errB := strconv.Atoi(names[j])
		if errA != nil || errB != nil {
			return names[i] < names[j]
		}
		return a < b
	})
	for _, n := range names {
		p := calPath + `\` + n
		if v, found := readID(p); found {
			return p, v, true
		}
	}
	return "", None, false
}

func (winStore) Assigned() (map[HWID]int, error) {
	h, err := openKey(diCachePath, syscall.KEY_READ)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return map[HWID]int{}, nil
		}
		return nil, fmt.Errorf("reading the DirectInput cache: %w", err)
	}
	names, err := subkeys(h)
	syscall.RegCloseKey(h)
	if err != nil {
		return nil, fmt.Errorf("reading the DirectInput cache: %w", err)
	}

	out := make(map[HWID]int, len(names))
	for _, n := range names {
		hw, ok := ParseKey(n)
		if !ok {
			continue
		}
		_, id, _ := idKey(hw)
		out[hw] = id
	}
	return out, nil
}

// Visible lists the controllers DirectInput 8 enumerates — the API iRacing and
// MAIRA use — with the slot each answers to in the legacy joystick API, or None.
//
// It asks DirectInput rather than only the legacy API because the two can
// disagree, and DirectInput is the one that matters. That was not assumed: on
// 2026-10-14, after the device GUIDs in the cache were restored from a backup,
// the legacy API still reported all five rig devices in their slots while
// DirectInput listed three — the wheelbase and rim had silently dropped out,
// and MAIRA reported the wheelbase missing. A check built on the legacy API
// alone said everything was fine.
func (winStore) Visible() (map[HWID]int, error) {
	// Ask WinMM to drop anything it cached first. In a fresh process — which
	// is the only place this is meant to be called — there is nothing cached,
	// so it costs nothing; it is here so a caller who forgets that rule gets a
	// best effort rather than a stale list.
	procJoyConfigChange.Call(0)

	slots := map[HWID]int{}
	for id := 0; id < winmmSlots; id++ {
		var c joyCaps
		r, _, _ := procJoyGetDevCapsW.Call(uintptr(id), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c))
		if r != 0 {
			continue
		}
		slots[HWID{VID: c.mid, PID: c.pid}] = id
	}

	listed, err := di8GameControllers()
	if err != nil {
		return nil, err
	}
	out := make(map[HWID]int, len(listed))
	for _, hw := range listed {
		if id, ok := slots[hw]; ok {
			out[hw] = id
		} else {
			out[hw] = None
		}
	}
	return out, nil
}

var (
	dinput8                = syscall.NewLazyDLL("dinput8.dll")
	procDirectInput8Create = dinput8.NewProc("DirectInput8Create")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")

	// iidDirectInput8W is IID_IDirectInput8W.
	iidDirectInput8W = syscall.GUID{Data1: 0xBF798031, Data2: 0x483A, Data3: 0x4DA2,
		Data4: [8]byte{0xAA, 0x99, 0x5D, 0x64, 0xED, 0x36, 0x97, 0x00}}

	// The enumeration callback is created once: syscall.NewCallback slots are a
	// fixed, never-freed resource, and a per-call callback would leak one each
	// time. It appends to di8Found, which di8Mu guards.
	di8Once     sync.Once
	di8Callback uintptr
	di8Mu       sync.Mutex
	di8Found    []HWID
)

const (
	directInputVersion  = 0x0800
	di8DevClassGameCtrl = 4
	diedflAttachedOnly  = 1

	// IDirectInput8W vtable slots.
	vtRelease     = 2
	vtEnumDevices = 4

	// Offset of guidProduct in DIDEVICEINSTANCEW: dwSize (4) + guidInstance (16).
	offGUIDProduct = 20
)

// di8GameControllers enumerates attached game controllers through DirectInput
// 8. For a HID device the first field of guidProduct packs the IDs as
// PID<<16 | VID, which is what the cache and the rig list key on.
func di8GameControllers() ([]HWID, error) {
	di8Once.Do(func() {
		di8Callback = syscall.NewCallback(func(inst, _ uintptr) uintptr {
			// inst is DirectInput's own DIDEVICEINSTANCEW, valid for the
			// duration of the callback.
			data1 := *(*uint32)(unsafe.Pointer(inst + offGUIDProduct)) //nolint:govet // OS-owned memory passed to a callback
			di8Found = append(di8Found, HWID{VID: uint16(data1), PID: uint16(data1 >> 16)})
			return 1 // DIENUM_CONTINUE
		})
	})

	hinst, _, _ := procGetModuleHandleW.Call(0)
	var di uintptr
	hr, _, _ := procDirectInput8Create.Call(hinst, directInputVersion,
		uintptr(unsafe.Pointer(&iidDirectInput8W)), uintptr(unsafe.Pointer(&di)), 0)
	if hr != 0 || di == 0 {
		return nil, fmt.Errorf("DirectInput8Create failed: 0x%08X", uint32(hr))
	}
	vtbl := *(*[8]uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(di)))) //nolint:govet // COM vtable owned by dinput8
	defer syscall.SyscallN(vtbl[vtRelease], di)

	di8Mu.Lock()
	defer di8Mu.Unlock()
	di8Found = nil
	hr, _, _ = syscall.SyscallN(vtbl[vtEnumDevices], di, di8DevClassGameCtrl, di8Callback, 0, diedflAttachedOnly)
	if hr != 0 {
		return nil, fmt.Errorf("IDirectInput8::EnumDevices failed: 0x%08X", uint32(hr))
	}
	return append([]HWID(nil), di8Found...), nil
}

var (
	cfgmgr32                         = syscall.NewLazyDLL("cfgmgr32.dll")
	procCMGetDeviceInterfaceListSize = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	procCMGetDeviceInterfaceList     = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")

	// guidDevInterfaceHID is GUID_DEVINTERFACE_HID.
	guidDevInterfaceHID = syscall.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF,
		Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}
)

var (
	procCMLocateDevNodeW      = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	procCMGetParent           = cfgmgr32.NewProc("CM_Get_Parent")
	procCMGetDevNodePropertyW = cfgmgr32.NewProc("CM_Get_DevNode_PropertyW")
	devpkeyBusReportedDesc    = devPropKey{syscall.GUID{Data1: 0x540B947E, Data2: 0x8B40, Data3: 0x45BC, Data4: [8]byte{0xA8, 0xA2, 0x6A, 0x0B, 0x89, 0x4C, 0xBD, 0xA2}}, 4}
	devpkeyDeviceDesc         = devPropKey{syscall.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 2}
)

// devPropKey is DEVPROPKEY.
type devPropKey struct {
	fmtid syscall.GUID
	pid   uint32
}

// HIDInterfaces lists present HID device interfaces — one per top-level
// collection, which is why a single wireless dongle can contribute a dozen —
// each with the device it belongs to and that device's product name.
func (winStore) HIDInterfaces() ([]HIDInterface, error) {
	var size uint32
	if r, _, _ := procCMGetDeviceInterfaceListSize.Call(uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&guidDevInterfaceHID)), 0, 0); r != 0 {
		return nil, fmt.Errorf("CM_Get_Device_Interface_List_Size failed: %d", r)
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]uint16, size)
	if r, _, _ := procCMGetDeviceInterfaceList.Call(uintptr(unsafe.Pointer(&guidDevInterfaceHID)), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0); r != 0 {
		return nil, fmt.Errorf("CM_Get_Device_Interface_List failed: %d", r)
	}

	names := map[string]string{} // one name lookup per device, not per interface
	var out []HIDInterface
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] != 0 {
			continue
		}
		if i == start {
			break // the double NUL that ends the list
		}
		id := InterfacePathToInstanceID(syscall.UTF16ToString(buf[start:i]))
		start = i + 1
		key := DeviceKey(id)
		name, seen := names[key]
		if !seen {
			name = deviceName(id)
			names[key] = name
		}
		out = append(out, HIDInterface{Device: key, Name: name})
	}
	return out, nil
}

// deviceName finds a readable name for the device a HID collection belongs to.
//
// The collection itself is "HID-compliant mouse" or similar; the product string
// the hardware reports ("Razer Clio 2.4") lives on a USB ancestor as the
// bus-reported description, so walk up a few levels looking for it. Software
// devices have none, so fall back to the parent's description, which for iCUE is
// "Corsair composite virtual input device".
func deviceName(instanceID string) string {
	id, err := syscall.UTF16PtrFromString(instanceID)
	if err != nil {
		return ""
	}
	var node uint32
	if r, _, _ := procCMLocateDevNodeW.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(id)), 0); r != 0 {
		return ""
	}
	fallback := ""
	for level := 0; level < 4; level++ {
		if s := nodeString(node, &devpkeyBusReportedDesc); s != "" {
			return s
		}
		var parent uint32
		if r, _, _ := procCMGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); r != 0 {
			break
		}
		node = parent
		if level == 0 {
			fallback = nodeString(node, &devpkeyDeviceDesc)
		}
	}
	return fallback
}

// nodeString reads a string device property, "" when absent.
func nodeString(node uint32, key *devPropKey) string {
	var typ uint32
	buf := make([]uint16, 256)
	size := uint32(len(buf) * 2)
	if r, _, _ := procCMGetDevNodePropertyW.Call(uintptr(node), uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
