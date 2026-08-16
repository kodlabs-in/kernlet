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
# Minimal Kernlet root filesystem and OCI image
# ---------------------------------------------------------

echo "==> Building Kernlet root filesystem"

ROOTFS_SRC="$WORK/rootfs"
IMAGE_ROOTFS="$WORK/identity-rootfs"
OCI_WORK="$WORK/identity-oci"
OCI_LAYOUT="$ROOTFS_SRC/var/lib/kernlet/images/identity"

rm -rf \
    "$ROOTFS_SRC" \
    "$IMAGE_ROOTFS" \
    "$OCI_WORK"

mkdir -p \
    "$ROOTFS_SRC/dev" \
    "$ROOTFS_SRC/proc" \
    "$ROOTFS_SRC/sys" \
    "$ROOTFS_SRC/run" \
    "$ROOTFS_SRC/tmp" \
    "$ROOTFS_SRC/sbin" \
    "$IMAGE_ROOTFS/dev" \
    "$IMAGE_ROOTFS/etc" \
    "$IMAGE_ROOTFS/proc" \
    "$IMAGE_ROOTFS/run" \
    "$IMAGE_ROOTFS/sbin" \
    "$IMAGE_ROOTFS/sys" \
    "$IMAGE_ROOTFS/tmp" \
    "$OCI_WORK" \
    "$OCI_LAYOUT/blobs/sha256"

chmod 1777 \
    "$ROOTFS_SRC/tmp" \
    "$IMAGE_ROOTFS/tmp"

install \
    -m 0755 \
    "$WORK/kernlet-agent" \
    "$ROOTFS_SRC/sbin/kernlet-agent"

install \
    -m 0755 \
    "$WORK/kernlet-agent" \
    "$IMAGE_ROOTFS/sbin/kernlet-agent"

printf "kernlet-workload" \
    > "$IMAGE_ROOTFS/etc/kernlet-rootfs"

COPYFILE_DISABLE=1 tar \
    -c \
    --format pax \
    --uid 0 \
    --gid 0 \
    --uname root \
    --gname root \
    -f "$OCI_WORK/layer.tar" \
    -C "$IMAGE_ROOTFS" \
    .

gzip \
    -n \
    -c \
    "$OCI_WORK/layer.tar" \
    > "$OCI_WORK/layer.tar.gz"

DIFF_ID="$(
    shasum -a 256 "$OCI_WORK/layer.tar" |
        awk '{print $1}'
)"

LAYER_DIGEST="$(
    shasum -a 256 "$OCI_WORK/layer.tar.gz" |
        awk '{print $1}'
)"

LAYER_SIZE="$(
    wc -c < "$OCI_WORK/layer.tar.gz" |
        tr -d ' '
)"

mv \
    "$OCI_WORK/layer.tar.gz" \
    "$OCI_LAYOUT/blobs/sha256/$LAYER_DIGEST"

cat > "$OCI_WORK/config.json" <<EOF
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
  },
  "rootfs": {
    "type": "layers",
    "diff_ids": [
      "sha256:$DIFF_ID"
    ]
  }
}
EOF

CONFIG_DIGEST="$(
    shasum -a 256 "$OCI_WORK/config.json" |
        awk '{print $1}'
)"

CONFIG_SIZE="$(
    wc -c < "$OCI_WORK/config.json" |
        tr -d ' '
)"

mv \
    "$OCI_WORK/config.json" \
    "$OCI_LAYOUT/blobs/sha256/$CONFIG_DIGEST"

cat > "$OCI_WORK/manifest.json" <<EOF
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "digest": "sha256:$CONFIG_DIGEST",
    "size": $CONFIG_SIZE
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
      "digest": "sha256:$LAYER_DIGEST",
      "size": $LAYER_SIZE
    }
  ]
}
EOF

MANIFEST_DIGEST="$(
    shasum -a 256 "$OCI_WORK/manifest.json" |
        awk '{print $1}'
)"

MANIFEST_SIZE="$(
    wc -c < "$OCI_WORK/manifest.json" |
        tr -d ' '
)"

mv \
    "$OCI_WORK/manifest.json" \
    "$OCI_LAYOUT/blobs/sha256/$MANIFEST_DIGEST"

cat > "$OCI_LAYOUT/index.json" <<EOF
{
  "schemaVersion": 2,
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:$MANIFEST_DIGEST",
      "size": $MANIFEST_SIZE,
      "platform": {
        "architecture": "arm64",
        "os": "linux"
      },
      "annotations": {
        "org.opencontainers.image.ref.name": "identity"
      }
    }
  ]
}
EOF

cat > "$OCI_LAYOUT/oci-layout" <<EOF
{
  "imageLayoutVersion": "1.0.0"
}
EOF

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
