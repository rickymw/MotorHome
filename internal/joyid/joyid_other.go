//go:build !windows

package joyid

import "errors"

// ErrUnsupported is what every Store method returns off Windows, where there is
// no DirectInput to ask.
var ErrUnsupported = errors.New("game controller checks are only available on Windows")

type otherStore struct{}

// NewStore returns a Store that reports ErrUnsupported, so the command layer
// compiles and its pure halves test on any OS.
func NewStore() Store { return otherStore{} }

func (otherStore) Assigned() (map[HWID]int, error)        { return nil, ErrUnsupported }
func (otherStore) Visible() (map[HWID]int, error)         { return nil, ErrUnsupported }
func (otherStore) HIDInterfaces() ([]HIDInterface, error) { return nil, ErrUnsupported }
