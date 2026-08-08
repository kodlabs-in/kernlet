# Kernlet

**Kernlet is a lightweight container runtime foundation built around native Linux isolation and platform-native virtualization.**

Kernlet provides the low-level pieces needed to run Linux containers while keeping the runtime small, understandable, and under our control.

On Linux, Kernlet works close to kernel primitives such as namespaces, mounts, processes, and cgroups.

On macOS, Kernlet uses Apple's `Virtualization.framework` to provide the Linux environment required by the container runtime.

Kernlet is actively developed and used within KodLabs systems.

> Public APIs and runtime internals may evolve as the project grows.

---

## Why Kernlet?

Containers look simple from the outside:

```bash
docker run alpine
```

But underneath that command are several different systems working together:

```text
OCI Image
    │
    ▼
Container Runtime
    │
    ├── namespaces
    ├── cgroups
    ├── mounts
    ├── filesystem
    └── processes
         │
         ▼
      Linux Kernel
```

Kernlet exposes and controls these pieces directly rather than hiding them behind a large runtime stack.

The goal is to provide a focused container runtime that can be embedded into higher-level KodLabs infrastructure while remaining useful as a standalone open-source project.

---

## Architecture

Kernlet separates platform-specific machine management from Linux container execution.

```text
                         Kernlet
                            │
               ┌────────────┴────────────┐
               │                         │
             macOS                     Linux
               │                         │
               ▼                         ▼
          pkg/applevm             Container Runtime
               │                         │
               ▼                         ├── Namespaces
      Virtualization.framework          ├── Mounts
               │                         ├── Cgroups
               ▼                         ├── Processes
           Linux VM                     └── Filesystems
               │
               └──────────────► Linux container environment
```

### macOS

macOS does not provide Linux namespaces or cgroups.

Kernlet therefore creates a lightweight Linux virtual machine using Apple's native `Virtualization.framework`.

The current VM stack looks like:

```text
Go
 │
 ▼
pkg/applevm
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
Linux
```

`pkg/applevm` provides a small Go API while keeping Objective-C and Apple-specific implementation details behind the package boundary.

Example:

```go
config := applevm.Config{
    KernelPath:    "./vmlinux",
    InitramfsPath: "./initramfs.cpio.gz",
    RootDiskPath:  "./rootfs.img",

    CPUCount:   2,
    MemorySize: 512 * 1024 * 1024,
}

vm, err := applevm.New(config)
if err != nil {
    return err
}
defer vm.Close()

if err := vm.Start(); err != nil {
    return err
}
```

---

## AppleVM

`pkg/applevm` is Kernlet's macOS virtualization layer.

It currently supports:

* Linux kernel direct boot
* initramfs
* VirtIO block storage
* VirtIO console
* VirtIO entropy
* configurable CPU count
* configurable guest memory
* Linux kernel command-line arguments
* VM start and stop lifecycle
* macOS / Linux build isolation

The VM itself remains intentionally simple.

Kernlet does not ask macOS to understand Linux filesystems or containers. Apple provides virtual hardware; Linux handles the rest.

```text
rootfs.img
    │
    ▼
Virtualization.framework
    │
    ▼
VirtIO block device
    │
    ▼
/dev/vda
    │
    ▼
Linux filesystem
```

---

## Runtime Assets

Linux VM assets are generated locally and are **not stored in the Git repository**.

This includes:

```text
vmlinux
initramfs.cpio.gz
rootfs.img
```

Kernlet currently uses:

* a container-oriented Linux kernel
* Alpine Linux as the minimal guest userspace
* an ext4 root filesystem

Development assets can be prepared with:

```bash
make applevm-assets
```

The generated files are stored under:

```text
.local/applevm/
```

These assets are implementation details of the runtime and should not be committed to Git.

---

## Running the AppleVM smoke test

### Requirements

Currently:

* macOS
* Apple Silicon (`arm64`)
* Go with cgo enabled
* Xcode Command Line Tools
* Homebrew
* `e2fsprogs`

Install the filesystem tooling:

```bash
brew install e2fsprogs
```

Prepare the VM:

```bash
make applevm-assets
```

Build and sign the smoke-test executable:

```bash
make applevm-smoke
```

Run it:

```bash
make applevm-run
```

A successful boot should eventually reach the Kernlet guest environment:

```text
================================
      KERNLET LINUX IS ALIVE
================================
```

Inside the VM:

```bash
uname -a
cat /etc/alpine-release
mount
ls -l /dev/vda
```

---

## Repository Layout

```text
.
├── build/
│   └── applevm.entitlements
│
├── cmd/
│   └── applevm-smoke/
│
├── hack/
│   └── applevm/
│       └── prepare-assets.sh
│
├── pkg/
│   └── applevm/
│       ├── config.go
│       ├── errors.go
│       ├── vm_darwin.go
│       ├── vm_unsupported.go
│       ├── bridge_darwin.go
│       ├── applevm_bridge.h
│       └── applevm_bridge_darwin.m
│
└── Makefile
```

---

## Project Status

Kernlet is under active development and is already used within KodLabs systems.

The runtime is being built as independent layers so that virtualization, Linux container execution, image management, networking, and higher-level APIs remain cleanly separated.

| Component                                           | Status      |
| --------------------------------------------------- | ----------- |
| Apple Virtualization integration                    | Working     |
| Direct Linux kernel boot                            | Working     |
| VirtIO block storage                                | Working     |
| Serial console                                      | Working     |
| Linux root filesystem boot                          | Working     |
| Linux container runtime                             | In progress |
| Guest agent                                         | Planned     |
| OCI image execution                                 | Planned     |
| Container networking                                | Planned     |
| Resource isolation with cgroups                     | Planned     |
| Runtime distribution and automatic asset management | Planned     |

### Runtime Roadmap

The current macOS runtime uses a prebuilt Kata Containers Linux kernel and Alpine Linux as a minimal guest userspace. These are bootstrap dependencies while Kernlet's runtime architecture is being developed.

The long-term runtime will move toward:

```text
Kernlet
   │
   ▼
Apple Virtualization.framework
   │
   ▼
Kernlet Linux Kernel
   │
   ▼
Minimal Kernlet Root Filesystem
   │
   ├── kernlet-agent
   ├── container runtime
   ├── networking
   └── required Linux utilities
   │
   ▼
OCI Containers
```

Planned runtime improvements include:

* Build and maintain a Kernlet-specific Linux kernel configuration instead of depending on a prebuilt Kata kernel.
* Replace the Alpine-based guest environment with a minimal Kernlet-owned root filesystem.
* Boot directly from the root filesystem where possible, removing unnecessary early-userspace layers such as the external initramfs.
* Introduce a Kernlet guest agent for communication between the macOS host and Linux runtime.
* Execute OCI images using native Linux namespaces, mounts, processes, and cgroups.
* Add container networking and host-to-guest communication.
* Distribute versioned, verified runtime bundles automatically so users don't need to manually prepare kernels or Linux disk images.
* Keep the macOS virtualization layer independent from the Linux container runtime so both can evolve separately.

The goal is for the Linux VM to eventually become an implementation detail: users interact with Kernlet and containers, not with VM setup or Linux boot artifacts.

---

## Direction

Kernlet is being built toward a runtime where applications can request a container without needing to know whether Linux is running directly on the host or inside a lightweight VM.

Conceptually:

```text
kernlet run <image>
       │
       ▼
Kernlet
       │
       ├── prepare Linux environment
       ├── prepare OCI filesystem
       ├── create isolation
       ├── configure resources
       └── start process
```

On Linux this can happen directly against the host kernel.

On macOS, Kernlet first provides the required Linux environment through Apple Virtualization.

The platform changes.

The container model does not.
