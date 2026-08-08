package runtime

import (
	"fmt"
	"os"
	"os/exec"
)

func Run(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("no command provided")
	}

	cmd := exec.Command(command[0], command[1:]...)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
