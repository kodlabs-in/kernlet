//go:build !darwin || !cgo

package applevm

type VM struct{}

func New(config Config) (*VM, error) {
	return nil, ErrUnsupported
}

func (vm *VM) Start() error {
	return ErrUnsupported
}

func (vm *VM) Stop() error {
	return ErrUnsupported
}

func (vm *VM) Close() error {
	return nil
}

func (vm *VM) DialVsock(port uint32) (Conn, error) {
	return nil, ErrUnsupported
}
