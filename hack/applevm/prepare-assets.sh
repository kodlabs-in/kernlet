#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

OUT="$ROOT/.local/applevm"
WORK="$OUT/work"
DOWNLOADS="$OUT/downloads"

KATA_VERSION="3.17.0"

KATA_URL="https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-arm64.tar.xz"

if [[ "$(uname -m)" != "arm64" ]]; then
    echo "applevm V1 currently supports arm64 Macs only"
    exit 1
fi

MKE2FS="$(brew --prefix e2fsprogs 2>/dev/null)/sbin/mke2fs"

if [[ ! -x "$MKE2FS" ]]; then
    echo "e2fsprogs is required."
    echo
    echo "Install it with:"
    echo "  brew install e2fsprogs"
    exit 1
fi

mkdir -p "$OUT" "$WORK" "$DOWNLOADS"

KATA_ARCHIVE="$DOWNLOADS/kata-${KATA_VERSION}.tar.xz"

# ---------------------------------------------------------
# Kernel
# ---------------------------------------------------------

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

# ---------------------------------------------------------
# Kernlet guest agent
# ---------------------------------------------------------

echo "==> Building Kernlet guest agent"

GOOS=linux \
GOARCH=arm64 \
CGO_ENABLED=0 \
go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$WORK/kernlet-agent" \
    "$ROOT/cmd/kernlet-agent"

echo "==> Guest agent binary"

file "$WORK/kernlet-agent"

# ---------------------------------------------------------
# Minimal Kernlet root filesystem
# ---------------------------------------------------------

echo "==> Building Kernlet root filesystem"

ROOTFS_SRC="$WORK/rootfs"

rm -rf "$ROOTFS_SRC"

mkdir -p \
    "$ROOTFS_SRC/dev" \
    "$ROOTFS_SRC/proc" \
    "$ROOTFS_SRC/sys" \
    "$ROOTFS_SRC/run" \
    "$ROOTFS_SRC/tmp" \
    "$ROOTFS_SRC/sbin"

chmod 1777 "$ROOTFS_SRC/tmp"

install \
    -m 0755 \
    "$WORK/kernlet-agent" \
    "$ROOTFS_SRC/sbin/kernlet-agent"

# ---------------------------------------------------------
# ext4 disk
# ---------------------------------------------------------

rm -f "$OUT/rootfs.img"

# 64 MiB is plenty for our tiny V1 guest.
mkfile -n 64m "$OUT/rootfs.img"

"$MKE2FS" \
    -t ext4 \
    -F \
    -L kernlet-root \
    -d "$ROOTFS_SRC" \
    "$OUT/rootfs.img"

echo
echo "Kernlet VM assets are ready:"
echo
echo "  Kernel:"
echo "    $OUT/vmlinux"
echo
echo "  Root filesystem:"
echo "    $OUT/rootfs.img"
