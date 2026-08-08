package main

import (
	"fmt"
	"os"
	"syscall"
)

func main() {
	mustMount("proc", "/proc", "proc")
	mustMount("sysfs", "/sys", "sysfs")

	fmt.Println()
	fmt.Println("================================")
	fmt.Println("       KERNLET GUEST READY")
	fmt.Println("================================")
	fmt.Println()

	// PID 1 must stay alive.
	select {}
}

func mustMount(source, target, fs string) {
	if err := os.MkdirAll(target, 0755); err != nil {
		panic(err)
	}

	if err := syscall.Mount(source, target, fs, 0, ""); err != nil {
		panic(err)
	}
}
