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
