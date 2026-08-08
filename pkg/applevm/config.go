package applevm

import (
	"fmt"
	"os"
)

type Config struct {
	// Linux kernel that Apple will boot.
	KernelPath string

	// Optional initial filesystem loaded into RAM during Linux boot.
	//
	// Most Kernlet VMs should boot directly from RootDiskPath.
	// This is kept for guests that require early userspace.
	InitramfsPath string

	// Main Linux disk.
	// Inside Linux this will normally become /dev/vda.
	RootDiskPath string

	// Number of virtual CPUs.
	CPUCount uint

	// Amount of RAM in bytes.
	MemorySize uint64

	// Arguments passed directly to the Linux kernel.
	KernelCommandLine string
}

func (c Config) validate() error {
	if c.KernelPath == "" {
		return fmt.Errorf("kernel path is required")
	}

	if _, err := os.Stat(c.KernelPath); err != nil {
		return fmt.Errorf("kernel: %w", err)
	}

	if c.InitramfsPath != "" {
		if _, err := os.Stat(c.InitramfsPath); err != nil {
			return fmt.Errorf("initramfs: %w", err)
		}
	}

	if c.RootDiskPath == "" {
		return fmt.Errorf("root disk path is required")
	}

	if _, err := os.Stat(c.RootDiskPath); err != nil {
		return fmt.Errorf("root disk: %w", err)
	}

	if c.CPUCount == 0 {
		return fmt.Errorf("CPU count must be greater than 0")
	}

	if c.MemorySize == 0 {
		return fmt.Errorf("memory size must be greater than 0")
	}

	return nil
}
