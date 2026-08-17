package runtime

import (
	"bytes"
	"errors"
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

const (
	maxHostnameLength = 64
	cgroupMount       = "/sys/fs/cgroup"
	cgroupRoot        = "/sys/fs/cgroup/kernlet"
)

var requiredControllers = []string{"cpu", "memory", "pids"}

type Limits struct {
	MemoryMax uint64
	PidsMax   uint64
	CPUQuota  uint64
	CPUPeriod uint64
}

type Config struct {
	Command     []string
	Environment []string
	WorkingDir  string
	Hostname    string
	Rootfs      string
	UID         uint32
	GID         uint32
	Limits      Limits
}

func SetupCgroups() error {
	if err := enableControllers(cgroupMount); err != nil {
		return fmt.Errorf("enable root cgroup controllers: %w", err)
	}

	if err := os.MkdirAll(cgroupRoot, 0755); err != nil {
		return fmt.Errorf("create kernlet cgroup: %w", err)
	}

	if err := enableControllers(cgroupRoot); err != nil {
		return fmt.Errorf("enable kernlet cgroup controllers: %w", err)
	}

	return nil
}

func Run(config Config) (output string, resultErr error) {
	if len(config.Command) == 0 {
		return "", fmt.Errorf("no command provided")
	}

	if err := validateHostname(config.Hostname); err != nil {
		return "", fmt.Errorf("invalid hostname: %w", err)
	}

	config.Rootfs = filepath.Clean(config.Rootfs)

	if err := validateRootfs(config.Rootfs); err != nil {
		return "", fmt.Errorf("invalid rootfs: %w", err)
	}

	if err := validateIdentity(config.UID, config.GID); err != nil {
		return "", fmt.Errorf("invalid identity: %w", err)
	}

	workingDirectory := config.WorkingDir

	if workingDirectory == "" {
		workingDirectory = "/"
	}

	workingDirectory = filepath.Clean(workingDirectory)

	if !filepath.IsAbs(workingDirectory) {
		return "", fmt.Errorf("working directory must be absolute")
	}

	if err := validateLimits(config.Limits); err != nil {
		return "", fmt.Errorf("invalid resource limits: %w", err)
	}

	cgroupPath, cgroupFD, err := createCgroup(config.Limits)
	if err != nil {
		return "", err
	}

	defer func() {
		if err := closeAndRemoveCgroup(cgroupPath, cgroupFD); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean up workload cgroup: %w", err))
		}
	}()

	network, err := allocateWorkloadNetwork()
	if err != nil {
		return "", err
	}

	if err := createGuestNetwork(network); err != nil {
		return "", err
	}

	defer func() {
		if err := removeGuestNetwork(network); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean up workload network: %w", err))
		}
	}()

	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create network readiness pipe: %w", err)
	}

	defer readyReader.Close()
	defer readyWriter.Close()

	args := make([]string, 0, len(config.Command)+9)

	args = append(
		args,
		InitCommand,
		config.Hostname,
		config.Rootfs,
		strconv.FormatUint(uint64(config.UID), 10),
		strconv.FormatUint(uint64(config.GID), 10),
		workingDirectory,
		network.PeerInterface,
		network.WorkloadAddress,
		network.Gateway,
	)

	args = append(args, config.Command...)

	cmd := exec.Command("/proc/self/exe", args...)

	cmd.Env = workloadEnvironment(config.Environment, network.Gateway)

	cmd.ExtraFiles = []*os.File{readyReader}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		UseCgroupFD: true,
		CgroupFD:    int(cgroupFD.Fd()),
	}

	var outputBuffer bytes.Buffer

	cmd.Stdout = &outputBuffer
	cmd.Stderr = &outputBuffer

	if err := cmd.Start(); err != nil {
		return outputBuffer.String(), fmt.Errorf("start %q: %w", config.Command[0], err)
	}

	// The child owns its inherited descriptor now. The parent must
	// close its copy so EOF works correctly if setup fails.
	_ = readyReader.Close()

	if err := moveNetworkToProcess(network, cmd.Process.Pid); err != nil {
		_ = readyWriter.Close()
		_ = cmd.Wait()

		return outputBuffer.String(), err
	}

	if _, err := readyWriter.Write([]byte{1}); err != nil {
		_ = readyWriter.Close()
		_ = cmd.Wait()

		return outputBuffer.String(), fmt.Errorf("signal network readiness: %w", err)
	}

	if err := readyWriter.Close(); err != nil {
		_ = cmd.Wait()

		return outputBuffer.String(), fmt.Errorf("close network readiness pipe: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return outputBuffer.String(), fmt.Errorf("run %q: %w", config.Command[0], err)
	}

	return outputBuffer.String(), nil
}

func closeAndRemoveCgroup(path string, fd *os.File) error {
	closeErr := fd.Close()
	removeErr := os.Remove(path)

	return errors.Join(closeErr, removeErr)
}

func InitProcess(args []string) error {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	if len(args) < 9 {
		return fmt.Errorf(
			"runtime init requires hostname, rootfs, UID, GID, working directory, network configuration and command",
		)
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

	workingDirectory := filepath.Clean(args[4])
	peerInterface := args[5]
	workloadAddress := args[6]
	gatewayAddress := args[7]
	command := args[8:]

	if !filepath.IsAbs(workingDirectory) {
		return fmt.Errorf("working directory must be absolute")
	}

	if err := validateHostname(hostname); err != nil {
		return fmt.Errorf("invalid hostname: %w", err)
	}

	if err := validateRootfs(rootfs); err != nil {
		return fmt.Errorf("invalid rootfs: %w", err)
	}

	if err := validateIdentity(uid, gid); err != nil {
		return fmt.Errorf("invalid identity: %w", err)
	}

	if err := waitForNetwork(); err != nil {
		return err
	}

	if err := configureWorkloadNetwork(peerInterface, workloadAddress, gatewayAddress); err != nil {
		return fmt.Errorf("configure workload network: %w", err)
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

	if err := os.Chdir(workingDirectory); err != nil {
		return fmt.Errorf("change directory to %q: %w", workingDirectory, err)
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

func enableControllers(path string) error {
	available, err := os.ReadFile(filepath.Join(path, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read available controllers: %w", err)
	}

	availableControllers := make(map[string]bool)

	for _, controller := range strings.Fields(string(available)) {
		availableControllers[controller] = true
	}

	for _, controller := range requiredControllers {
		if !availableControllers[controller] {
			return fmt.Errorf("required controller %q is unavailable", controller)
		}
	}

	value := "+cpu +memory +pids"

	if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte(value), 0644); err != nil {
		return fmt.Errorf("write subtree control: %w", err)
	}

	return nil
}

func createCgroup(limits Limits) (string, *os.File, error) {
	path, err := os.MkdirTemp(cgroupRoot, "workload-")
	if err != nil {
		return "", nil, fmt.Errorf("create workload cgroup: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(path)
	}

	values := map[string]string{
		"memory.max": strconv.FormatUint(limits.MemoryMax, 10),
		"pids.max":   strconv.FormatUint(limits.PidsMax, 10),
		"cpu.max":    fmt.Sprintf("%d %d", limits.CPUQuota, limits.CPUPeriod),
	}

	for name, value := range values {
		if err := writeCgroupValue(path, name, value); err != nil {
			cleanup()
			return "", nil, err
		}
	}

	fd, err := os.Open(path)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("open workload cgroup: %w", err)
	}

	return path, fd, nil
}

func writeCgroupValue(path string, name string, value string) error {
	file := filepath.Join(path, name)

	if err := os.WriteFile(file, []byte(value), 0644); err != nil {
		return fmt.Errorf("write %s=%q: %w", name, value, err)
	}

	actual, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}

	expectedFields := strings.Join(strings.Fields(value), " ")
	actualFields := strings.Join(strings.Fields(string(actual)), " ")

	if actualFields != expectedFields {
		return fmt.Errorf("verify %s: expected %q, received %q", name, expectedFields, actualFields)
	}

	return nil
}

func validateLimits(limits Limits) error {
	if limits.MemoryMax == 0 {
		return fmt.Errorf("memory limit must be greater than zero")
	}

	if limits.PidsMax == 0 {
		return fmt.Errorf("process limit must be greater than zero")
	}

	if limits.CPUQuota == 0 {
		return fmt.Errorf("CPU quota must be greater than zero")
	}

	if limits.CPUPeriod < 1000 || limits.CPUPeriod > 1_000_000 {
		return fmt.Errorf("CPU period must be between 1000 and 1000000 microseconds")
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
