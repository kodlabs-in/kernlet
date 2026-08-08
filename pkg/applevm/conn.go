package applevm

import "io"

type Conn interface {
	io.Reader
	io.Writer
	io.Closer
}
