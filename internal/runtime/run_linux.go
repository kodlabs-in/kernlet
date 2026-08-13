package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const InitCommand = "runtime-init"

const maxHostnameLength = 64

func Run(command []string, hostname string, rootfs string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("no command provided")
	}

	if err := validateHostname(hostname); err != nil {
		return "", fmt.Errorf("invalid hostname: %w", err)
	}

	rootfs = filepath.Clean(rootfs)

	if err := validateRootfs(rootfs); err != nil {
		return "", fmt.Errorf("invalid rootfs: %w", err)
	}

	args := make([]string, 0, len(command)+3)

	args = append(args, InitCommand, hostname, rootfs)

	args = append(args, command...)

	cmd := exec.Command("/proc/self/exe", args...)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("run %q: %w", command[0], err)
	}

	return string(output), nil
}

func InitProcess(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("runtime init requires hostname, rootfs and command")
	}

	hostname := args[0]
	rootfs := filepath.Clean(args[1])
	command := args[2:]

	if err := validateHostname(hostname); err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}

	if err := validateRootfs(rootfs); err != nil {
		return fmt.Errorf("invalid rootfs: %w", err)
	}

	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("set hostname %q: %w", hostname, err)
	}

	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}

	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind rootfs: %w", err)
	}

	oldRoot := filepath.Join(rootfs, ".oldroot")
	if err := os.Mkdir(oldRoot, 0700); err != nil {
		return fmt.Errorf("create old root directory: %w", err)
	}

	if err := syscall.PivotRoot(rootfs, oldRoot); err != nil {
		return fmt.Errorf("pivot root: %w", err)
	}

	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("change directory to new root: %w", err)
	}

	if err := syscall.Unmount("/.oldroot", syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("detach old root: %w", err)
	}

	if err := os.Remove("/.oldroot"); err != nil {
		return fmt.Errorf("remove old root directory: %w", err)
	}

	if err := syscall.Mount("proc", "/proc", "proc", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount private proc filesystem: %w", err)
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

func validateRootfs(rootfs string) error {
	if !filepath.IsAbs(rootfs) {
		return fmt.Errorf("rootfs must be an absolute path")
	}

	if rootfs == "/" {
		return fmt.Errorf("rootfs cannot be the guest root")
	}

	info, err := os.Stat(rootfs)
	if err != nil {
		return fmt.Errorf("stat rootfs: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("rootfs is not a directory")
	}

	return nil
}
