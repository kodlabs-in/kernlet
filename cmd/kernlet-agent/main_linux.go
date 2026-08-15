//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/kodlabs-in/kernlet/internal/guestproto"
	kernruntime "github.com/kodlabs-in/kernlet/internal/runtime"
	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == kernruntime.InitCommand {
		if err := kernruntime.InitProcess(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: runtime init: %v\n", err)
			os.Exit(1)
		}

		return
	}

	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("kernlet-agent")
		return
	}

	if len(os.Args) == 2 && os.Args[1] == "--identity" {
		hostname, err := os.Hostname()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read hostname: %v\n", err)
			os.Exit(1)
		}

		procSelf, err := os.Readlink("/proc/self")
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read /proc/self: %v\n", err)
			os.Exit(1)
		}

		procRoot, err := os.Readlink("/proc/self/root")
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read /proc/self/root: %v\n", err)
			os.Exit(1)
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: get cw: %v\n", err)
			os.Exit(1)
		}

		rootfsMarker, err := os.ReadFile("/etc/kernlet-rootfs")
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read rootfs marker: %v\n", err)
			os.Exit(1)
		}

		oldRoot := "hidden"
		if _, err := os.Stat("/.oldroot"); err == nil {
			oldRoot = "visible"
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "kernlet-agent: inspect old root: %v\n", err)
			os.Exit(1)
		}

		groups, err := os.Getgroups()
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read groups: %v\n", err)
			os.Exit(1)
		}

		status, err := os.ReadFile("/proc/self/status")
		if err != nil {
			fmt.Fprintf(os.Stderr, "kernlet-agent: read process status: %v\n", err)
			os.Exit(1)
		}

		capInh, err := statusField(status, "CapInh")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		capPrm, err := statusField(status, "CapPrm")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		capEff, err := statusField(status, "CapEff")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		capBnd, err := statusField(status, "CapBnd")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		capAmb, err := statusField(status, "CapAmb")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		noNewPrivs, err := statusField(status, "NoNewPrivs")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		fmt.Printf(
			"hostname=%s pid=%d ppid=%d proc-self=%s cwd=%s root=%s rootfs=%s old-root=%s uid=%d euid=%d gid=%d egid=%d groups=%d cap-inh=%s cap-prm=%s cap-eff=%s cap-bnd=%s cap-amb=%s no-new-privs=%s\n",
			hostname,
			os.Getpid(),
			os.Getppid(),
			procSelf,
			cwd,
			procRoot,
			string(rootfsMarker),
			oldRoot,
			os.Getuid(),
			os.Geteuid(),
			os.Getgid(),
			os.Getegid(),
			len(groups),
			capInh,
			capPrm,
			capEff,
			capBnd,
			capAmb,
			noNewPrivs,
		)

		return
	}

	fmt.Printf("kernlet-agent: starting as PID %d\n", os.Getpid())

	mustMount("proc", "/proc", "proc")
	mustMount("sysfs", "/sys", "sysfs")

	fmt.Println()
	fmt.Println("================================")
	fmt.Println("       KERNLET GUEST READY")
	fmt.Println("================================")
	fmt.Println()

	if err := serveVsock(); err != nil {
		panic(err)
	}
}

func serveVsock() error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("create vsock socket: %w", err)
	}

	defer unix.Close(fd)

	address := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: guestproto.Port,
	}

	if err := unix.Bind(fd, address); err != nil {
		return fmt.Errorf("bind vsock port %d: %w", guestproto.Port, err)
	}

	if err := unix.Listen(fd, 16); err != nil {
		return fmt.Errorf("listen on vsock: %w", err)
	}

	fmt.Printf("kernlet-agent: listening on vsock port %d\n", guestproto.Port)

	for {
		connFD, _, err := unix.Accept4(fd, unix.SOCK_CLOEXEC)
		if err != nil {
			if err == unix.EINTR {
				continue
			}

			return fmt.Errorf("accept vsock connection: %w", err)
		}

		go handleConnection(connFD)
	}
}

func handleConnection(fd int) {
	file := os.NewFile(uintptr(fd), "kernlet-vsock")

	if file == nil {
		_ = unix.Close(fd)
		return
	}

	defer file.Close()

	decoder := json.NewDecoder(file)
	encoder := json.NewEncoder(file)

	for {
		var request guestproto.Request

		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}

			fmt.Printf("kernlet-agent: decode request: %v\n", err)

			return
		}

		response := handleRequest(request)

		if err := encoder.Encode(response); err != nil {
			fmt.Printf("kernlet-agent: encode response: %v\n", err)

			return
		}
	}
}

func handleRequest(request guestproto.Request) guestproto.Response {
	switch request.Method {
	case "ping":
		return guestproto.Response{
			ID:      request.ID,
			OK:      true,
			Message: "pong",
		}

	case "run":
		output, err := kernruntime.Run(request.Args, request.Hostname, request.Rootfs, request.UID, request.GID)
		if err != nil {
			return guestproto.Response{
				ID:      request.ID,
				OK:      false,
				Message: output,
				Error:   err.Error(),
			}
		}

		return guestproto.Response{
			ID:      request.ID,
			OK:      true,
			Message: output,
		}

	default:
		return guestproto.Response{
			ID:    request.ID,
			OK:    false,
			Error: "unknown method",
		}
	}
}

func mustMount(source, target, fsType string) {
	if err := os.MkdirAll(target, 0755); err != nil {
		panic(fmt.Errorf("create mount point %s: %w", target, err))
	}

	if err := syscall.Mount(source, target, fsType, 0, ""); err != nil {
		if err == syscall.EBUSY {
			return
		}

		panic(fmt.Errorf("mount %s on %s: %w", fsType, target, err))
	}
}

func statusField(status []byte, name string) (string, error) {
	prefix := name + ":"

	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)

		if len(fields) >= 2 && fields[0] == prefix {
			return strings.Join(fields[1:], ","), nil
		}
	}

	return "", fmt.Errorf("kernlet-agent: process status field %s is missing", name)
}
