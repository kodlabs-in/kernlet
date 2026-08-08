package applevm

import "errors"

var (
	ErrUnsupported = errors.New("applevm: requires macOS with cgo enabled")
	ErrClosed      = errors.New("applevm: virtual machine is closed")
)
