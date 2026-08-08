package main

import (
	"fmt"
	"os"

	"github.com/kodlabs-in/kernlet/internal/platform"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: kernlet run <image>")
		return
	}

	command := os.Args[1]
	image := os.Args[2]

	if command != "run" {
		fmt.Println("unknown command:", command)
		return
	}

	if err := platform.Run(image); err != nil {
		fmt.Println("error:", err)
	}
}
