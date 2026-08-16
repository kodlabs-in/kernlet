# Kernlet

Kernlet is KodLabs' lightweight container-runtime foundation built around native Linux isolation and platform-native virtualization.

It provides a controlled runtime layer for internal services that need to execute isolated Linux workloads without directly managing virtual machines, guest communication, namespaces, root filesystems, security controls, cgroups, or OCI runtime preparation.

Kernlet is actively developed and used within KodLabs systems.

> Public APIs and runtime internals may evolve as internal requirements grow.

## Why Kernlet?

KodLabs services need a consistent execution boundary for isolated Linux workloads across different host platforms.

On macOS, Kernlet creates a lightweight Linux virtual machine using Apple's native `Virtualization.framework`. Linux workloads then run inside that VM using standard Linux isolation primitives.

```text
Internal service
      │
      ▼
    Kernlet
      │
      ▼
Apple Virtualization.framework
      │
      ▼
   Linux VM
      │
      ▼
 kernlet-agent
      │
      ├── OCI image preparation
      ├── namespaces
      ├── isolated root filesystem
      ├── process security
      └── cgroups v2
              │
              ▼
       Isolated workload
```

The host integration is platform-specific, while the workload execution model remains Linux-native.

## Current milestone

AppleVM V1 and the first eight Linux runtime milestones are complete.

Kernlet can currently:

- create a Linux VM on Apple Silicon;
- boot Linux directly from a kernel and ext4 root disk;
- start `kernlet-agent` as Linux PID 1;
- communicate between macOS and Linux over VirtIO vsock;
- exchange newline-delimited JSON requests and responses;
- prepare a workload from a local OCI image layout;
- verify OCI descriptor sizes and SHA-256 digests;
- apply the image command, environment, working directory, UID, and GID;
- create private UTS, PID, and mount namespaces;
- assign a private workload hostname;
- run the workload as PID 1 inside its PID namespace;
- switch to an isolated root filesystem using `pivot_root`;
- mount a private `/proc`;
- detach and hide the guest root filesystem;
- remove supplementary groups;
- drop the capability bounding set;
- clear inherited, permitted, effective, and ambient capabilities;
- enable `no_new_privs`;
- enforce memory, process, and CPU limits using cgroups v2;
- capture workload stdout and stderr;
- return workload output and execution errors to the host;
- remove the temporary workload root filesystem and cgroup after exit.

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
kernlet-agent (guest PID 1)
    │
    ├── read OCI image layout
    ├── verify manifests and blobs
    ├── extract workload root filesystem
    └── create workload cgroup
            │
            ▼
     internal/runtime
            │
            ├── UTS namespace
            ├── PID namespace
            ├── mount namespace
            ├── private hostname
            ├── private /proc
            ├── pivot_root
            ├── UID and GID
            ├── capability removal
            └── no_new_privs
                    │
                    ▼
             Workload PID 1
                    │
                    │ stdout and stderr
                    ▼
             kernlet-agent
                    │
                    │ JSON response
                    ▼
               macOS host
```

The current workload is isolated from the guest agent's hostname, process IDs, mounts, root filesystem, identity, capabilities, and resource accounting.

Network namespace isolation and complete workload lifecycle management remain pending.

## Architecture boundaries

Kernlet separates machine management, guest control, OCI preparation, and Linux workload execution.

### `pkg/applevm`

`pkg/applevm` owns the macOS virtualization layer.

It handles:

- VM configuration;
- virtual CPU configuration;
- guest memory configuration;
- Linux direct-kernel boot;
- optional initramfs configuration;
- VirtIO block storage;
- VirtIO console;
- VirtIO entropy;
- VirtIO socket devices;
- VM start and stop operations;
- VM resource cleanup;
- host connections to guest vsock ports.

It does not implement Linux workload isolation.

### `kernlet-agent`

`kernlet-agent` runs inside the Linux guest as PID 1.

It handles:

- mounting the guest `/proc`;
- mounting the guest `/sys`;
- mounting the cgroup v2 filesystem;
- enabling the required cgroup controllers;
- listening on VirtIO vsock port `10789`;
- accepting host connections;
- decoding and validating guest requests;
- preparing local OCI images;
- dispatching runtime operations;
- capturing workload output;
- returning JSON responses.

### `internal/oci`

`internal/oci` prepares OCI images for runtime execution.

It currently:

- reads an OCI image layout;
- selects the matching `linux/arm64` manifest;
- verifies descriptor sizes;
- verifies SHA-256 content digests;
- reads the OCI image configuration;
- resolves the image entrypoint and command;
- reads the image environment and working directory;
- requires a numeric non-root `UID:GID`;
- supports one uncompressed or gzip-compressed filesystem layer;
- verifies the layer DiffID;
- safely extracts paths beneath the temporary workload root;
- rejects paths that escape the root filesystem;
- rejects unsupported filesystem objects;
- rejects whiteouts until multi-layer image support is added;
- preserves file, directory, and root-directory metadata;
- removes the temporary runtime bundle after workload exit.

The current implementation intentionally supports one local base layer. Registry pulls, multiple layers, whiteouts, and image caching are outside the current milestone.

### `internal/runtime`

`internal/runtime` owns Linux workload isolation and execution.

It currently:

- validates runtime configuration;
- creates workload cgroups;
- applies memory, process, and CPU limits;
- places the workload in its cgroup during process creation;
- creates UTS, PID, and mount namespaces;
- assigns the private workload hostname;
- makes mounts private;
- bind-mounts the workload root filesystem;
- switches roots using `pivot_root`;
- detaches and removes the old guest root;
- mounts a private `/proc`;
- applies the OCI working directory;
- enables `no_new_privs`;
- clears supplementary groups;
- switches to the configured UID and GID;
- drops the capability bounding set;
- clears process capability sets;
- executes the workload without invoking a shell;
- waits for the workload to exit;
- returns stdout, stderr, and execution errors;
- removes the workload cgroup after exit.

## Linux isolation model

Each workload currently receives the following isolation layers:

| Isolation layer | Current behavior |
| --- | --- |
| UTS namespace | Private hostname |
| PID namespace | Workload becomes PID 1 |
| Mount namespace | Private mount table |
| `/proc` | Private proc filesystem matching the PID namespace |
| Root filesystem | OCI layer extracted into a temporary root and activated with `pivot_root` |
| Old guest root | Detached and removed from the workload namespace |
| UID and GID | Numeric non-root identity from OCI configuration |
| Supplementary groups | Cleared |
| Capabilities | Bounding and process capability sets cleared |
| Privilege escalation | Blocked with `no_new_privs` |
| Memory | Limited through `memory.max` |
| Processes | Limited through `pids.max` |
| CPU | Limited through `cpu.max` |
| Networking | Still shared with the guest agent |

The runtime executes the workload directly:

```text
args[0]   executable
args[1:]  executable arguments
```

Kernlet does not invoke a shell.

Therefore, shell syntax such as the following is not interpreted automatically:

```text
;
|
>
$()
```

These remain ordinary argument values unless the selected executable interprets them.

## OCI workload format

The current AppleVM assets contain a local OCI image layout:

```text
/var/lib/kernlet/images/identity/
├── oci-layout
├── index.json
└── blobs/
    └── sha256/
        ├── <manifest>
        ├── <configuration>
        └── <compressed layer>
```

The image configuration defines:

```json
{
  "architecture": "arm64",
  "os": "linux",
  "config": {
    "User": "65532:65532",
    "Env": [
      "PATH=/sbin:/usr/sbin:/bin:/usr/bin"
    ],
    "Entrypoint": [
      "/sbin/kernlet-agent"
    ],
    "Cmd": [
      "--identity"
    ],
    "WorkingDir": "/"
  }
}
```

The host request may optionally override the image command with an explicit argument vector. The image environment, working directory, and workload identity remain runtime-controlled.

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

The guest environment is intentionally minimal.

```text
/
├── dev/
├── proc/
├── run/
├── sbin/
│   └── kernlet-agent
├── sys/
├── tmp/
└── var/
    └── lib/
        └── kernlet/
            └── images/
                └── identity/
```

The guest does not require:

- Alpine Linux;
- BusyBox;
- a shell;
- a package manager;
- general-purpose Linux commands.

The primary guest userspace executable is:

```text
/sbin/kernlet-agent
```

The generated identity OCI image also contains the statically linked agent binary as a small internal workload used to verify the complete runtime boundary.

## Runtime assets

VM runtime assets are generated locally and are not committed to Git.

```text
.local/applevm/
├── vmlinux
├── rootfs.img
├── downloads/
└── work/
```

Kernlet currently uses a prebuilt Kata Containers kernel as its bootstrap kernel.

The guest root filesystem is Kernlet-owned and generated locally as an ext4 disk image. It contains the guest agent and the local OCI image layout.

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
ext4 guest root filesystem
   │
   ▼
/sbin/kernlet-agent
```

The Kata kernel remains a bootstrap dependency while Kernlet's runtime requirements are established. A Kernlet-specific kernel configuration can replace it when internal requirements justify maintaining one.

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

Successful response:

```json
{
  "id": 1,
  "ok": true,
  "message": "pong"
}
```

### Workload request

```json
{
  "id": 1,
  "method": "run",
  "hostname": "kernlet-workload",
  "image": "/var/lib/kernlet/images/identity",
  "memory_max": 67108864,
  "pids_max": 32,
  "cpu_quota": 50000,
  "cpu_period": 100000
}
```

The resource configuration means:

- maximum memory: 64 MiB;
- maximum processes: 32;
- CPU quota: 50,000 microseconds per 100,000-microsecond period.

A successful response contains the workload output:

```json
{
  "id": 1,
  "ok": true,
  "message": "hostname=kernlet-workload pid=1 ..."
}
```

The current protocol assumes a trusted host-to-guest control channel. It must not be exposed directly to untrusted clients.

## Current process model

The current runtime executes one workload synchronously for each `run` request.

```text
run request
    │
    ▼
prepare OCI bundle
    │
    ▼
create cgroup
    │
    ▼
create isolated process
    │
    ▼
capture stdout and stderr
    │
    ▼
wait for exit
    │
    ▼
remove cgroup and bundle
    │
    ▼
return response
```

This model is suitable for one-shot internal workloads.

Long-running workloads require the lifecycle protocol planned for roadmap point 10.

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

Prepare the Linux kernel, guest root filesystem, and local OCI image:

```bash
make applevm-assets
```

Build and sign the AppleVM smoke executable:

```bash
make applevm-smoke
```

Boot the VM and execute the OCI workload:

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
guest process output: hostname=kernlet-workload pid=1 ppid=0 proc-self=1 cwd=/ root=/ rootfs=kernlet-workload old-root=hidden uid=65532 euid=65532 gid=65532 egid=65532 groups=0 cap-inh=0000000000000000 cap-prm=0000000000000000 cap-eff=0000000000000000 cap-bnd=0000000000000000 cap-amb=0000000000000000 no-new-privs=1 cgroup=0::/kernlet/workload-...
press Ctrl-C to stop
```

The workload cgroup suffix is generated independently for every workload.

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
├── cmd/
│   ├── applevm-smoke/
│   │   └── main.go
│   ├── kernlet/
│   │   └── main.go
│   └── kernlet-agent/
│       └── main_linux.go
├── hack/
│   └── applevm/
│       └── prepare-assets.sh
├── internal/
│   ├── guestproto/
│   │   └── protocol.go
│   ├── oci/
│   │   ├── image.go
│   │   └── image_test.go
│   ├── platform/
│   │   ├── run_darwin.go
│   │   └── run_linux.go
│   └── runtime/
│       └── run_linux.go
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
├── Makefile
├── README.md
├── go.mod
└── go.sum
```

## Project status

| Milestone | Status |
| --- | --- |
| Apple Virtualization integration | Complete |
| Direct Linux kernel boot | Complete |
| Minimal Kernlet guest filesystem | Complete |
| `kernlet-agent` as guest PID 1 | Complete |
| VirtIO vsock control channel | Complete |
| JSON request and response protocol | Complete |
| Guest process execution | Complete |
| UTS namespace isolation | Complete |
| PID namespace isolation | Complete |
| Mount namespace and private `/proc` | Complete |
| Isolated workload root filesystem | Complete |
| Non-root identity and security controls | Complete |
| cgroups v2 resource limits | Complete |
| Local OCI image preparation | Complete |
| Workload networking | Remaining |
| Complete workload lifecycle | Remaining |

## Runtime roadmap

The Linux runtime is being developed in the following order:

1. Execute an ordinary Linux child process over vsock. **Complete**
2. Add UTS namespace isolation and a private hostname. **Complete**
3. Add PID namespace isolation and workload PID 1 behavior. **Complete**
4. Add mount namespace isolation and a private `/proc`. **Complete**
5. Add an isolated workload root filesystem. **Complete**
6. Apply UID, GID, capabilities, and `no_new_privs`. **Complete**
7. Apply CPU, memory, and process limits with cgroups v2. **Complete**
8. Read an OCI image layout and prepare its runtime filesystem. **Complete**
9. Add network namespace isolation and workload networking. **Next**
10. Add complete workload process lifecycle management. **Planned**

## Remaining roadmap

### 9. Workload networking

The next milestone will isolate workload networking from the guest agent.

It will introduce:

- a private network namespace;
- workload loopback configuration;
- a virtual Ethernet connection between the guest and workload namespaces;
- workload IP address assignment;
- routing between the workload and guest;
- explicit connectivity and cleanup behavior.

The target boundary is:

```text
guest network namespace
        │
        │ virtual Ethernet pair
        ▼
workload network namespace
        │
        ├── private interfaces
        ├── private routes
        └── private loopback device
```

### 10. Complete workload lifecycle

The final milestone will replace the current synchronous one-shot execution model with an explicit lifecycle.

It will introduce:

- stable workload identifiers;
- create and start operations;
- asynchronous workload state;
- stdout and stderr streaming;
- status inspection;
- signal delivery;
- graceful termination;
- forced termination;
- exit events;
- wait operations;
- deterministic cgroup, bundle, namespace, and process cleanup;
- guest readiness through the control protocol.

The target lifecycle is:

```text
create
   │
   ▼
start
   │
   ▼
running
   │
   ├── status
   ├── stream output
   ├── signal
   └── wait
          │
          ▼
        exited
          │
          ▼
        delete
```

## Next milestone

The next runtime milestone is workload networking.

No lifecycle work will begin until network namespace isolation and workload connectivity are complete.