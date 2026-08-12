# Kernlet

Kernlet is KodLabs' lightweight container-runtime foundation built around native Linux isolation and platform-native virtualization.

It provides a controlled runtime layer for internal services that need to execute isolated Linux workloads without directly managing virtual machines, guest communication, namespaces, filesystems, cgroups, or container processes.

Kernlet is actively developed and used within KodLabs systems.

> Public APIs and runtime internals may evolve as internal requirements grow.

## Why Kernlet?

KodLabs services need a consistent way to run Linux workloads across different host platforms.

On Linux, Kernlet can work directly with kernel isolation primitives.

On macOS, Kernlet first creates a lightweight Linux virtual machine using Apple's native `Virtualization.framework`.

```text
Internal service
      │
      ▼
    Kernlet
      │
      ├── macOS
      │     └── Apple Virtualization.framework
      │              └── Linux VM
      │
      └── Linux
            └── Host Linux kernel
                      │
                      ▼
              Container runtime
                      │
                      ├── processes
                      ├── namespaces
                      ├── mounts
                      ├── filesystems
                      ├── cgroups
                      └── networking
```

The host platform changes, but the container execution model remains Linux-based.

## Current milestone

AppleVM V1 and the first Linux runtime milestone are complete.

Kernlet can currently:

- create a Linux VM on Apple Silicon;
- boot Linux directly from a kernel and ext4 root disk;
- start `kernlet-agent` as Linux PID 1;
- communicate between macOS and Linux over VirtIO vsock;
- exchange newline-delimited JSON requests and responses;
- execute an ordinary Linux guest process;
- capture the process output;
- return the output to the macOS host.

The complete working path is:

```text
macOS host
    │
    │ JSON run request
    ▼
pkg/applevm
    │
    │ VirtIO vsock
    ▼
Linux VM
    │
    ▼
kernlet-agent (PID 1)
    │
    ▼
internal/runtime
    │
    ▼
Linux child process
    │
    │ stdout and stderr
    ▼
kernlet-agent
    │
    │ JSON response
    ▼
macOS host
```

The child process is not a container yet. It currently shares the guest agent's hostname, namespaces, root filesystem, and resource limits.

## Architecture boundaries

Kernlet separates machine management from Linux container execution.

### `pkg/applevm`

`pkg/applevm` owns the macOS virtualization layer.

It handles:

- VM configuration;
- virtual CPU configuration;
- guest memory configuration;
- Linux direct kernel boot;
- VirtIO block storage;
- VirtIO console;
- VirtIO entropy;
- VirtIO socket devices;
- VM start and stop operations;
- VM resource cleanup;
- host connections to guest vsock ports.

It does not implement Linux container isolation.

### `kernlet-agent`

`kernlet-agent` runs inside the Linux guest as PID 1.

It currently:

- mounts `/proc`;
- mounts `/sys`;
- listens on VirtIO vsock port `10789`;
- accepts host connections;
- decodes JSON requests;
- dispatches guest operations;
- executes ordinary Linux child processes;
- captures process output;
- returns JSON responses.

Future container operations will be implemented behind this agent.

### `internal/runtime`

`internal/runtime` owns Linux process and container execution.

It currently:

- validates process argument vectors;
- starts an executable without invoking a shell;
- waits for the process to finish;
- captures stdout and stderr;
- reports execution errors.

This layer will progressively add:

- UTS namespaces;
- PID namespaces;
- mount namespaces;
- isolated root filesystems;
- process security controls;
- cgroups;
- OCI image execution;
- network namespaces.

## AppleVM

The macOS VM layer uses the following native integration path:

```text
Go
 │
 ▼
pkg/applevm
 │
 ▼
cgo
 │
 ▼
C ABI
 │
 ▼
Objective-C
 │
 ▼
Apple Virtualization.framework
 │
 ▼
Linux VM
```

The public Go package keeps Apple-specific implementation details hidden from the rest of Kernlet.

AppleVM currently supports:

- configurable virtual CPUs;
- configurable guest memory;
- Linux kernel command-line arguments;
- direct Linux kernel boot;
- optional initramfs configuration;
- writable VirtIO block storage;
- VirtIO console output;
- VirtIO entropy;
- VirtIO vsock;
- VM start;
- VM stop;
- VM resource cleanup;
- macOS and Linux build isolation.

## Guest environment

The current guest environment is intentionally minimal.

```text
/
├── dev/
├── proc/
├── run/
├── sbin/
│   └── kernlet-agent
├── sys/
└── tmp/
```

The guest does not currently include:

- Alpine Linux;
- BusyBox;
- a shell;
- a package manager;
- general-purpose Linux commands.

The primary userspace executable is:

```text
/sbin/kernlet-agent
```

The same executable is currently reused as a small process-execution workload through:

```text
/proc/self/exe --version
```

This avoids adding temporary guest utilities that are not required by the runtime.

## Runtime assets

VM runtime assets are generated locally and are not committed to Git.

```text
.local/applevm/
├── vmlinux
└── rootfs.img
```

Kernlet currently uses a prebuilt Kata Containers kernel as its bootstrap kernel.

The root filesystem is Kernlet-owned and built locally as an ext4 disk image. It contains the statically linked `kernlet-agent`.

The current boot path does not require an initramfs:

```text
vmlinux
   │
   ▼
VirtIO block device
   │
   ▼
/dev/vda
   │
   ▼
ext4 root filesystem
   │
   ▼
/sbin/kernlet-agent
```

The Kata kernel remains a bootstrap dependency while Kernlet's container-runtime architecture is developed. A Kernlet-specific kernel configuration can replace it when internal runtime requirements justify maintaining one.

## Host-to-guest protocol

The macOS host and Linux guest communicate using newline-delimited JSON over VirtIO vsock.

The shared protocol is defined in:

```text
internal/guestproto/protocol.go
```

The guest listens on:

```text
vsock port 10789
```

### Health request

```json
{
  "id": 1,
  "method": "ping"
}
```

Response:

```json
{
  "id": 1,
  "ok": true,
  "message": "pong"
}
```

### Process request

```json
{
  "id": 1,
  "method": "run",
  "args": [
    "/proc/self/exe",
    "--version"
  ]
}
```

Successful response:

```json
{
  "id": 1,
  "ok": true,
  "message": "kernlet-agent\n"
}
```

The argument vector maps directly to process execution:

```text
args[0]   executable
args[1:]  executable arguments
```

Kernlet does not invoke a shell when executing the request.

This means the following shell features are not interpreted:

```text
;
|
>
$()
```

They remain ordinary argument data unless the requested executable interprets them itself.

The current protocol assumes a trusted host-to-guest control channel. It is not intended to be exposed directly to untrusted clients.

## Process execution

The current Linux runtime executes one-shot processes synchronously.

```text
run request
    │
    ▼
validate arguments
    │
    ▼
start process
    │
    ▼
capture stdout and stderr
    │
    ▼
wait for exit
    │
    ▼
return response
```

This is sufficient for short internal runtime operations and proves the host-to-guest execution path.

Long-running containers will require a lifecycle protocol for:

- process creation;
- process identifiers;
- output streaming;
- process status;
- signals;
- termination;
- exit events.

That lifecycle will be added after the isolation layers are working.

## Building and running

### Requirements

The current AppleVM implementation requires:

- macOS;
- Apple Silicon;
- Go with cgo enabled;
- Xcode Command Line Tools;
- Homebrew;
- `e2fsprogs`.

Install the filesystem tooling:

```bash
brew install e2fsprogs
```

Prepare the Linux kernel and root filesystem:

```bash
make applevm-assets
```

Build and sign the AppleVM smoke executable:

```bash
make applevm-smoke
```

Boot the VM and run the process-execution smoke test:

```bash
make applevm-run
```

A successful run includes:

```text
Run /sbin/kernlet-agent as init process
kernlet-agent: starting as PID 1

================================
       KERNLET GUEST READY
================================

kernlet-agent: listening on vsock port 10789
connecting to kernlet-agent...
requesting guest process...
guest process output: kernlet-agent
press Ctrl-C to stop
```

Stop the VM using `Ctrl-C`.

Remove generated AppleVM assets:

```bash
make applevm-clean
```

## Repository layout

```text
.
├── build/
│   └── applevm.entitlements
│
├── cmd/
│   ├── applevm-smoke/
│   │   └── main.go
│   ├── kernlet/
│   │   └── main.go
│   └── kernlet-agent/
│       └── main_linux.go
│
├── hack/
│   └── applevm/
│       └── prepare-assets.sh
│
├── internal/
│   ├── guestproto/
│   │   └── protocol.go
│   ├── platform/
│   │   ├── run_darwin.go
│   │   └── run_linux.go
│   └── runtime/
│       └── run_linux.go
│
├── pkg/
│   └── applevm/
│       ├── applevm_bridge.h
│       ├── applevm_bridge_darwin.m
│       ├── bridge_darwin.go
│       ├── config.go
│       ├── conn.go
│       ├── errors.go
│       ├── vm_darwin.go
│       └── vm_unsupported.go
│
├── Makefile
├── README.md
├── go.mod
└── go.sum
```

## Project status

| Component | Status |
| --- | --- |
| Apple Virtualization integration | Working |
| Direct Linux kernel boot | Working |
| Minimal Kernlet root filesystem | Working |
| `kernlet-agent` as PID 1 | Working |
| VirtIO vsock | Working |
| JSON request and response protocol | Working |
| Host-to-guest health check | Working |
| Guest process argument protocol | Working |
| Guest process execution | Working |
| Guest process output capture | Working |
| UTS namespace isolation | Next |
| PID namespace isolation | Planned |
| Mount namespace isolation | Planned |
| Container root filesystem | Planned |
| Process security controls | Planned |
| cgroups v2 resource control | Planned |
| OCI image execution | Planned |
| Container networking | Planned |
| Complete container lifecycle | Planned |

## Runtime roadmap

The Linux runtime will be developed in the following order:

1. Execute an ordinary Linux child process over vsock. **Complete**
2. Add UTS namespace isolation and a private hostname.
3. Add PID namespace isolation and container PID 1 behavior.
4. Add mount namespace isolation and a private `/proc`.
5. Add an isolated container root filesystem.
6. Apply UID, GID, capabilities, and `no_new_privs`.
7. Apply CPU, memory, and process limits with cgroups v2.
8. Read an OCI image layout and prepare its runtime filesystem.
9. Add network namespace isolation and container networking.
10. Add the complete container process lifecycle.

## Next milestone

The next runtime milestone is UTS namespace isolation.

```text
kernlet-agent
      │
      │ create child process
      ▼
new UTS namespace
      │
      ├── private hostname
      └── private domain name
```

The process will remain an ordinary guest process in every other respect.

This isolates one kernel resource at a time and keeps failures attributable to the layer being introduced.
