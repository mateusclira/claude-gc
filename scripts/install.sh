#!/bin/sh
# Installs the latest claude-gc release binary for this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/mateusclira/claude-gc/main/scripts/install.sh | sh
#
# Environment:
#   VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   INSTALL_DIR  where to put the binary (default: ~/.local/bin)
set -eu

REPO="mateusclira/claude-gc"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-}"

say() { printf '%s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

need curl
need tar
need mktemp

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin | linux) ;;
  *) die "unsupported operating system: $os (claude-gc supports macOS and Linux)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$VERSION" ] || die "could not determine the latest release"
fi
version="${VERSION#v}"
asset="claude-gc_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Downloading claude-gc $VERSION for $os/$arch"
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

expected=$(grep "[[:space:]]$asset\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)
[ -n "$expected" ] || die "no checksum found for $asset"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | cut -d ' ' -f 1)
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$asset" | cut -d ' ' -f 1)
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for $asset"

tar -xzf "$tmp/$asset" -C "$tmp" claude-gc
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/claude-gc" "$INSTALL_DIR/claude-gc"
say "Installed $INSTALL_DIR/claude-gc"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) say "Note: $INSTALL_DIR is not on your PATH." ;;
esac
say ""
say "Next steps:"
say "  claude-gc status            # see what is running"
say "  claude-gc run --dry-run     # preview a pass"
say "  claude-gc service install   # keep it tidy automatically"
