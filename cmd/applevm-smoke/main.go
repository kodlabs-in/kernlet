package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/kodlabs-in/kernlet/pkg/applevm"
)

func main() {
	kernel := flag.String("kernel", "", "path to Linux kernel")

	initramfs := flag.String("initramfs", "", "optional path to initramfs")

	disk := flag.String("disk", "", "path to root disk image")

	cpus := flag.Uint("cpus", 2, "number of virtual CPUs")

	memoryMiB := flag.Uint64("memory", 512, "VM memory in MiB")

	flag.Parse()

	if *kernel == "" || *disk == "" {
		flag.Usage()
		os.Exit(2)
	}

	config := applevm.Config{
		KernelPath:    *kernel,
		InitramfsPath: *initramfs,
		RootDiskPath:  *disk,

		CPUCount: *cpus,

		MemorySize: *memoryMiB * 1024 * 1024,

		KernelCommandLine: "console=hvc0 root=/dev/vda rootfstype=ext4 rootwait rw init=/sbin/kernlet-init",
	}

	vm, err := applevm.New(config)
	if err != nil {
		log.Fatal(err)
	}

	defer vm.Close()

	fmt.Println("starting Linux VM...")

	if err := vm.Start(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("VM started")
	fmt.Println("press Ctrl-C to stop")

	signals := make(chan os.Signal, 1)

	signal.Notify(signals, os.Interrupt)

	<-signals

	fmt.Println()
	fmt.Println("stopping VM...")

	if err := vm.Stop(); err != nil {
		log.Printf("stop VM: %v", err)
	}

	fmt.Println("VM stopped")
}
