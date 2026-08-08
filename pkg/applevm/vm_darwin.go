//go:build darwin && cgo

package applevm

import (
	"fmt"
	"sync"
)

type VM struct {
	mu sync.Mutex

	native *nativeVM
}

func New(config Config) (*VM, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("applevm: invalid config: %w", err)
	}

	native, err := newNativeVM(config)
	if err != nil {
		return nil, fmt.Errorf("applevm: create VM: %w", err)
	}

	return &VM{
		native: native,
	}, nil
}

func (vm *VM) Start() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.native == nil {
		return ErrClosed
	}

	if err := vm.native.start(); err != nil {
		return fmt.Errorf("applevm: start VM: %w", err)
	}

	return nil
}

func (vm *VM) Stop() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.native == nil {
		return ErrClosed
	}

	if err := vm.native.stop(); err != nil {
		return fmt.Errorf("applevm: stop VM: %w", err)
	}

	return nil
}

func (vm *VM) Close() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.native == nil {
		return nil
	}

	vm.native.close()
	vm.native = nil

	return nil
}
