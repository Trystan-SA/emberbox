#!/usr/bin/env bash
# setup-firecracker.sh — one-shot setup for the examples/firecrackerbackend demo.
#
# Downloads the firecracker binary and a CI kernel, builds the example agent
# binary, packages it into an ext4 rootfs (via a throwaway docker build), and
# writes an envrc with the three EMBERBOX_FIRECRACKER_* variables.
#
# Idempotent: re-running skips steps whose outputs already exist. Pass --force
# to redo everything.
#
# Usage:
#   ./scripts/setup-firecracker.sh [--out=<dir>] [--force] [--rebuild-rootfs]
#
# Defaults:
#   --out=$HOME/.emberbox/firecracker
#
# --force          redo every step
# --rebuild-rootfs only re-pack the rootfs (use after editing the example agent)
#
# Overrides (env vars):
#   FIRECRACKER_VERSION  default: latest GitHub release
#   KERNEL_CI_VERSION    default: v1.12
#   KERNEL_VERSION       default: 5.10.233
#
# Run from anywhere — the script cds to the repo root to invoke `go build`.

set -euo pipefail

# --- arg parsing -------------------------------------------------------------

OUT_DIR="$HOME/.emberbox/firecracker"
FORCE=0
REBUILD_ROOTFS=0
for arg in "$@"; do
  case "$arg" in
    --out=*) OUT_DIR="${arg#--out=}" ;;
    --force) FORCE=1 ;;
    --rebuild-rootfs) REBUILD_ROOTFS=1 ;;
    -h|--help)
      sed -n '2,22p' "$0"
      exit 0
      ;;
    *)
      echo "unknown flag: $arg" >&2
      exit 2
      ;;
  esac
done

# --- utilities ---------------------------------------------------------------

die() { echo "error: $*" >&2; exit 1; }
log() { echo "[setup-firecracker] $*"; }

# Resolve repo root via this script's location, then cd there so go build works.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

[[ -d "examples/firecrackerbackend/agent" ]] || die "must run from inside the emberbox repo (couldn't find examples/firecrackerbackend/agent)"

# --- preflight ---------------------------------------------------------------

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|aarch64) ;;
  *) die "unsupported arch $ARCH (firecracker supports x86_64 and aarch64)" ;;
esac

missing=()
for tool in curl tar go docker mke2fs; do
  command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
done
if (( ${#missing[@]} > 0 )); then
  echo "missing tools: ${missing[*]}" >&2
  echo "on Debian/Ubuntu/Mint: sudo apt install -y curl tar e2fsprogs golang-go" >&2
  echo "docker: https://docs.docker.com/engine/install/" >&2
  exit 1
fi

[[ -r /dev/kvm && -w /dev/kvm ]] || die "/dev/kvm not r/w by $USER. Run: sudo setfacl -m u:\$USER:rw /dev/kvm"

mkdir -p "$OUT_DIR"
if (( FORCE )); then
  log "--force: removing all existing artifacts"
  rm -f "$OUT_DIR/firecracker" "$OUT_DIR/vmlinux" "$OUT_DIR/rootfs.ext4" "$OUT_DIR/.envrc"
elif (( REBUILD_ROOTFS )); then
  log "--rebuild-rootfs: removing rootfs only"
  rm -f "$OUT_DIR/rootfs.ext4"
fi

# --- 1. firecracker binary ---------------------------------------------------

FC_BIN="$OUT_DIR/firecracker"
if [[ ! -x "$FC_BIN" ]]; then
  if [[ -z "${FIRECRACKER_VERSION:-}" ]]; then
    log "resolving latest firecracker release"
    FIRECRACKER_VERSION="$(curl -fsSL https://api.github.com/repos/firecracker-microvm/firecracker/releases/latest \
      | grep -oE '"tag_name": "[^"]+"' | head -1 | cut -d\" -f4)"
    [[ -n "$FIRECRACKER_VERSION" ]] || die "couldn't resolve latest firecracker release"
  fi
  log "downloading firecracker $FIRECRACKER_VERSION ($ARCH)"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  url="https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/firecracker-${FIRECRACKER_VERSION}-${ARCH}.tgz"
  curl -fSL --progress-bar "$url" -o "$TMP/fc.tgz"
  tar -xzf "$TMP/fc.tgz" -C "$TMP"
  # Archive layout: release-${VERSION}-${ARCH}/firecracker-${VERSION}-${ARCH}
  cp "$TMP/release-${FIRECRACKER_VERSION}-${ARCH}/firecracker-${FIRECRACKER_VERSION}-${ARCH}" "$FC_BIN"
  chmod +x "$FC_BIN"
  trap - EXIT
  rm -rf "$TMP"
else
  log "firecracker binary already present ($FC_BIN)"
fi

# --- 2. kernel ---------------------------------------------------------------

KERNEL="$OUT_DIR/vmlinux"
KERNEL_CI_VERSION="${KERNEL_CI_VERSION:-v1.12}"
KERNEL_VERSION="${KERNEL_VERSION:-5.10.233}"
if [[ ! -f "$KERNEL" ]]; then
  url="https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/${KERNEL_CI_VERSION}/${ARCH}/vmlinux-${KERNEL_VERSION}"
  log "downloading kernel $KERNEL_VERSION from firecracker-ci ${KERNEL_CI_VERSION}"
  curl -fSL --progress-bar "$url" -o "$KERNEL"
else
  log "kernel already present ($KERNEL)"
fi

# --- 3. rootfs ---------------------------------------------------------------
#
# Build the example agent binary (statically linked, linux/amd64 or arm64),
# bake it into an Alpine image alongside bash, then export the container fs to
# a tree, then mke2fs -d that tree into rootfs.ext4. No mount/loop needed.

ROOTFS="$OUT_DIR/rootfs.ext4"
if [[ ! -f "$ROOTFS" ]]; then
  BUILD="$(mktemp -d)"
  trap 'rm -rf "$BUILD"' EXIT

  case "$ARCH" in
    x86_64)  GOARCH=amd64 ;;
    aarch64) GOARCH=arm64 ;;
  esac

  log "building example agent (linux/$GOARCH)"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" go build -ldflags="-s -w" \
    -o "$BUILD/emberbox-agent" ./examples/firecrackerbackend/agent

  log "building rootfs docker image"
  cat > "$BUILD/Dockerfile" <<'DOCKERFILE'
FROM alpine:3.20
RUN apk add --no-cache bash ca-certificates
COPY emberbox-agent /usr/local/bin/emberbox-agent
RUN chmod +x /usr/local/bin/emberbox-agent && \
    ln -sf /usr/local/bin/emberbox-agent /sbin/init
DOCKERFILE
  docker build -q -t emberbox-fc-rootfs:setup "$BUILD" >/dev/null

  log "exporting rootfs to ext4"
  CID="$(docker create emberbox-fc-rootfs:setup)"
  mkdir -p "$BUILD/rootfs"
  docker export "$CID" | tar -xf - -C "$BUILD/rootfs" \
    --exclude='./dev/*' --exclude='./proc/*' --exclude='./sys/*' \
    --no-same-owner 2>/dev/null || true
  docker rm "$CID" >/dev/null

  # /sbin/init is now a symlink to /usr/local/bin/emberbox-agent — running as
  # PID 1, defaults to --vsock-port=10000 which matches FirecrackerConfig.
  mke2fs -t ext4 -F -q -d "$BUILD/rootfs" -L emberbox "$ROOTFS" 256M

  trap - EXIT
  rm -rf "$BUILD"
else
  log "rootfs already present ($ROOTFS)"
fi

# --- 4. envrc ----------------------------------------------------------------

ENVRC="$OUT_DIR/.envrc"
cat > "$ENVRC" <<EOF
# Source me to run examples/firecrackerbackend:
#   source $ENVRC && go run ./examples/firecrackerbackend
export EMBERBOX_FIRECRACKER_BIN=$FC_BIN
export EMBERBOX_FIRECRACKER_KERNEL=$KERNEL
export EMBERBOX_FIRECRACKER_ROOTFS=$ROOTFS
EOF

cat <<EOF

[setup-firecracker] done.

  binary: $FC_BIN
  kernel: $KERNEL
  rootfs: $ROOTFS

Run the example:
  source $ENVRC
  go run ./examples/firecrackerbackend
EOF
