package platform

import (
	"fmt"

	kernruntime "github.com/kodlabs-in/kernlet/internal/runtime"
)

func Run(image string) error {
	fmt.Println("Linux runtime requested for:", image)

	return kernruntime.Run([]string{
		"/bin/sh",
		"-c",
		"echo 'hello from Kernlet runtime'",
	})
}
