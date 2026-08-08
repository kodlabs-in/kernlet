#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

OUT="$ROOT/.local/applevm"
WORK="$OUT/work"
DOWNLOADS="$OUT/downloads"

KATA_VERSION="3.17.0"
ALPINE_VERSION="3.24.1"

KATA_URL="https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-arm64.tar.xz"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/aarch64/alpine-minirootfs-${ALPINE_VERSION}-aarch64.tar.gz"

if [[ "$(uname -m)" != "arm64" ]]; then
    echo "applevm V1 currently supports arm64 Macs only"
    exit 1
fi

MKE2FS="$(brew --prefix e2fsprogs 2>/dev/null)/sbin/mke2fs"

if [[ ! -x "$MKE2FS" ]]; then
    echo "e2fsprogs is required to build the development rootfs."
    echo
    echo "Install it with:"
    echo "  brew install e2fsprogs"
    exit 1
fi

mkdir -p "$OUT" "$WORK" "$DOWNLOADS"

KATA_ARCHIVE="$DOWNLOADS/kata-${KATA_VERSION}.tar.xz"
ALPINE_ARCHIVE="$DOWNLOADS/alpine-${ALPINE_VERSION}.tar.gz"

echo "==> Downloading Kata kernel package"

if [[ ! -f "$KATA_ARCHIVE" ]]; then
    curl -L --fail \
        -o "$KATA_ARCHIVE" \
        "$KATA_URL"
fi

echo "==> Extracting kernel"

rm -rf "$WORK/kata"
mkdir -p "$WORK/kata"

tar -xJf "$KATA_ARCHIVE" \
    -C "$WORK/kata"

cp -L \
    "$WORK/kata/opt/kata/share/kata-containers/vmlinux.container" \
    "$OUT/vmlinux"

echo "==> Downloading Alpine"

if [[ ! -f "$ALPINE_ARCHIVE" ]]; then
    curl -L --fail \
        -o "$ALPINE_ARCHIVE" \
        "$ALPINE_URL"
fi

ROOTFS_SRC="$WORK/rootfs"

echo "==> Building Alpine root filesystem"

sudo rm -rf "$ROOTFS_SRC"
sudo mkdir -p "$ROOTFS_SRC"

sudo env COPYFILE_DISABLE=1 \
    tar -xzf "$ALPINE_ARCHIVE" \
    -C "$ROOTFS_SRC"

sudo tee "$ROOTFS_SRC/sbin/kernlet-init" >/dev/null <<'EOF'
#!/bin/sh

mount -t proc proc /proc
mount -t sysfs sysfs /sys

echo
echo "================================"
echo "      KERNLET LINUX IS ALIVE"
echo "================================"
echo

exec /bin/sh -i
EOF

sudo chmod +x \
    "$ROOTFS_SRC/sbin/kernlet-init"

rm -f "$OUT/rootfs.img"

mkfile -n 256m "$OUT/rootfs.img"

sudo "$MKE2FS" \
    -t ext4 \
    -F \
    -L kernlet-root \
    -d "$ROOTFS_SRC" \
    "$OUT/rootfs.img"

sudo chown \
    "$(id -u):$(id -g)" \
    "$OUT/rootfs.img"

echo
echo "Kernlet VM assets are ready:"
echo
echo "  $OUT/vmlinux"
echo "  $OUT/rootfs.img"
