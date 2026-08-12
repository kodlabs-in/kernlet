package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

const InitCommand = "runtime-init"

const maxHostnameLength = 64

func Run(command []string, hostname string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("no command provided")
	}

	if err := validateHostname(hostname); err != nil {
		return "", fmt.Errorf("invalid hostname: %w", err)
	}

	args := make([]string, 0, len(command)+2)

	args = append(args, InitCommand, hostname)

	args = append(args, command...)

	cmd := exec.Command("/proc/self/exe", args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS,
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("run %q: %w", command[0], err)
	}

	return string(output), nil
}

func InitProcess(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("runtime init requires hostname and command")
	}

	hostname := args[0]
	command := args[1:]

	if err := validateHostname(hostname); err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}

	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("set hostname %q: %w", hostname, err)
	}

	path, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("find executable %q: %w", command[0], err)
	}

	if err := syscall.Exec(path, command, os.Environ()); err != nil {
		return fmt.Errorf("exec %q: %w", command[0], err)
	}

	return nil
}

func validateHostname(hostname string) error {
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}

	if len(hostname) > maxHostnameLength {
		return fmt.Errorf("hostname exceeds %d bytes", maxHostnameLength)
	}

	if strings.IndexByte(hostname, 0) >= 0 {
		return fmt.Errorf("hostname contains a null byte")
	}

	return nil
}
