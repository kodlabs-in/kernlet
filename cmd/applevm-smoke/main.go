package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/kodlabs-in/kernlet/internal/guestproto"
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

		KernelCommandLine: "console=hvc0 root=/dev/vda rootfstype=ext4 rootwait rw init=/sbin/kernlet-agent",
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
	// Temporary V1 readiness delay.
	//
	// Later the guest protocol itself will provide
	// proper readiness handling.
	time.Sleep(500 * time.Millisecond)

	fmt.Println("connecting to kernlet-agent...")

	conn, err := vm.DialVsock(guestproto.Port)
	if err != nil {
		log.Fatal(err)
	}

	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	fmt.Println("requesting guest process...")

	request := guestproto.Request{
		ID:       1,
		Method:   "run",
		Hostname: "kernlet-workload",
		Rootfs:   "/var/lib/kernlet/rootfs",
		Args: []string{
			"/sbin/kernlet-agent",
			"--identity",
		},
	}

	if err := encoder.Encode(request); err != nil {
		log.Fatal(err)
	}

	var response guestproto.Response

	if err := decoder.Decode(&response); err != nil {
		log.Fatal(err)
	}

	if !response.OK {
		log.Fatalf("guest process failed: %s\n%s", response.Error, response.Message)
	}

	fmt.Printf("guest process output: %s", response.Message)

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
