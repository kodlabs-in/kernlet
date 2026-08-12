package runtime

import (
	"fmt"
	"os/exec"
)

func Run(command []string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("no command provided")
	}

	cmd := exec.Command(command[0], command[1:]...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("run %q: %w", command[0], err)
	}

	return string(output), nil
}
