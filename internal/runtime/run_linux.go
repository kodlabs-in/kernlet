package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const InitCommand = "runtime-init"

const maxHostnameLength = 64

func Run(command []string, hostname string, rootfs string, uid uint32, gid uint32) (string, error) {
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

	args := make([]string, 0, len(command)+5)

	args = append(args, InitCommand, hostname, rootfs, strconv.FormatUint(uint64(uid), 10), strconv.FormatUint(uint64(gid), 10))

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
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	if len(args) < 5 {
		return fmt.Errorf("runtime init requires hostname, rootfs, UID, GID and command")
	}

	hostname := args[0]
	rootfs := filepath.Clean(args[1])

	uid, err := parseID("UID", args[2])
	if err != nil {
		return err
	}

	gid, err := parseID("GID", args[3])
	if err != nil {
		return err
	}

	command := args[4:]

	if err := validateHostname(hostname); err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}

	if err := validateRootfs(rootfs); err != nil {
		return fmt.Errorf("invalid rootfs: %w", err)
	}

	if err := validateIdentity(uid, gid); err != nil {
		return fmt.Errorf("invalid identity: %w", err)
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

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no new privileges: %w", err)
	}

	if err := dropCapabilityBoundingSet(); err != nil {
		return err
	}

	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}

	if err := syscall.Setresgid(int(gid), int(gid), int(gid)); err != nil {
		return fmt.Errorf("set workload GID %d: %w", gid, err)
	}

	if err := syscall.Setresuid(int(uid), int(uid), int(uid)); err != nil {
		return fmt.Errorf("set workload UID %d: %w", uid, err)
	}

	if err := clearCapabilities(); err != nil {
		return err
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

func validateIdentity(uid uint32, gid uint32) error {
	if uid == 0 {
		return fmt.Errorf("workload UID must be nonzero")
	}

	if gid == 0 {
		return fmt.Errorf("workload GID must be nonzero")
	}

	return nil
}

func parseID(name string, value string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}

	return uint32(id), nil
}

func dropCapabilityBoundingSet() error {
	value, err := os.ReadFile("/proc/sys/kernel/cap_last_cap")
	if err != nil {
		return fmt.Errorf("read last capability: %w", err)
	}

	lastCapability, err := strconv.Atoi(strings.TrimSpace(string(value)))
	if err != nil {
		return fmt.Errorf("parse last capability: %w", err)
	}

	for capability := 0; capability <= lastCapability; capability++ {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(capability), 0, 0, 0); err != nil {
			return fmt.Errorf("drop capability %d from bounding set: %w", capability, err)
		}
	}

	return nil
}

func clearCapabilities() error {
	header := unix.CapUserHeader{
		Version: unix.LINUX_CAPABILITY_VERSION_3,
	}

	data := [2]unix.CapUserData{}

	if err := unix.Capset(&header, &data[0]); err != nil {
		return fmt.Errorf("clear capability sets: %w", err)
	}

	return nil
}
