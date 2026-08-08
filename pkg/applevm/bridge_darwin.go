//go:build darwin && cgo

package applevm

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Foundation -framework Virtualization

#include <stdlib.h>
#include "applevm_bridge.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"unsafe"
)

type nativeVM struct {
	handle *C.applevm_handle_t
}

func newNativeVM(config Config) (*nativeVM, error) {
	kernelPath := C.CString(config.KernelPath)
	defer C.free(unsafe.Pointer(kernelPath))

	initramfsPath := C.CString(config.InitramfsPath)
	defer C.free(unsafe.Pointer(initramfsPath))

	rootDiskPath := C.CString(config.RootDiskPath)
	defer C.free(unsafe.Pointer(rootDiskPath))

	commandLine := C.CString(config.KernelCommandLine)
	defer C.free(unsafe.Pointer(commandLine))

	cConfig := C.applevm_config_t{
		kernel_path:         kernelPath,
		initramfs_path:      initramfsPath,
		root_disk_path:      rootDiskPath,
		kernel_command_line: commandLine,

		cpu_count:   C.uint32_t(config.CPUCount),
		memory_size: C.uint64_t(config.MemorySize),
	}

	var cErr *C.char

	handle := C.applevm_create(&cConfig, &cErr)

	if handle == nil {
		return nil, nativeError(cErr, "failed to create virtual machine")
	}

	return &nativeVM{
		handle: handle,
	}, nil
}

func (vm *nativeVM) start() error {
	var cErr *C.char

	result := C.applevm_start(vm.handle, &cErr)

	if result != 0 {
		return nativeError(cErr, "failed to start virtual machine")
	}

	return nil
}

func (vm *nativeVM) stop() error {
	var cErr *C.char

	result := C.applevm_stop(vm.handle, &cErr)

	if result != 0 {
		return nativeError(cErr, "failed to stop virtual machine")
	}

	return nil
}

func (vm *nativeVM) close() {
	if vm.handle == nil {
		return
	}

	C.applevm_destroy(vm.handle)
	vm.handle = nil
}

func nativeError(cErr *C.char, fallback string) error {
	if cErr == nil {
		return errors.New(fallback)
	}

	defer C.applevm_error_free(cErr)

	return errors.New(C.GoString(cErr))
}

func (vm *nativeVM) dialVsock(port uint32) (Conn, error) {
	var cErr *C.char
	var fd C.int

	result := C.applevm_vsock_connect(vm.handle, C.uint32_t(port), &fd, &cErr)

	if result != 0 {
		return nil, nativeError(cErr, "failed to connect to guest vsock")
	}

	file := os.NewFile(uintptr(fd), fmt.Sprintf("applevm-vsock-%d", port))

	if file == nil {
		return nil, errors.New("failed to create Go file from vsock")
	}

	return file, nil
}
