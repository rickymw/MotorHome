//go:build !windows

package joyid

import "errors"

// ErrUnsupported is what every Store method returns off Windows, where there is
// no DirectInput cache to read.
var ErrUnsupported = errors.New("joystick IDs are only available on Windows")

type otherStore struct{}

// NewStore returns a Store that reports ErrUnsupported, so the command layer
// compiles and its pure halves test on any OS.
func NewStore() Store { return otherStore{} }

func (otherStore) Assigned() (map[HWID]int, error) { return nil, ErrUnsupported }
func (otherStore) Visible() (map[HWID]int, error)  { return nil, ErrUnsupported }
func (otherStore) SetID(HWID, int) error           { return ErrUnsupported }
func (otherStore) Notify()                         {}
func (otherStore) HIDInterfaces() (int, error)     { return 0, ErrUnsupported }
