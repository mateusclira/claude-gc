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

# The version an existing copy reports, such as v0.3.0, or nothing.
version_of() { "$1" version 2>/dev/null | awk '{print $2}'; }
# A path for messages, with the home directory shown as ~.
pretty() {
  case "$1" in
    "$HOME"/*) printf '~%s' "${1#"$HOME"}" ;;
    *) printf '%s' "$1" ;;
  esac
}

target="$INSTALL_DIR/claude-gc"
previous=""
[ -x "$target" ] && previous=$(version_of "$target")

mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmp/claude-gc" "$target"
installed=$(version_of "$target")
[ -n "$installed" ] || installed="$VERSION"

if [ -z "$previous" ]; then
  say "Installed claude-gc $installed at $(pretty "$target")"
elif [ "$previous" = "$installed" ]; then
  say "claude-gc $installed at $(pretty "$target") is already up to date"
else
  say "Updated claude-gc from $previous to $installed at $(pretty "$target")"
fi

# Another copy earlier on PATH (from go install, say) would keep running
# instead of this one, which makes an upgrade look like it did nothing.
found=$(command -v claude-gc 2>/dev/null || true)
cmd=claude-gc
case ":$PATH:" in
  *":$INSTALL_DIR:"*)
    if [ -n "$found" ] && [ "$found" != "$target" ]; then
      cmd="\"$target\""
      say ""
      say "Warning: \`claude-gc\` still runs $(pretty "$found") ($(version_of "$found")),"
      say "which comes first on your PATH. Remove it so $installed runs:"
      say "  rm \"$found\""
    fi
    ;;
  *)
    cmd="\"$target\""
    say "Note: $(pretty "$INSTALL_DIR") is not on your PATH."
    ;;
esac

# A running daemon keeps the old version until the service is reinstalled.
unit_mac="$HOME/Library/LaunchAgents/io.github.mateusclira.claude-gc.plist"
unit_linux="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/claude-gc.service"
if [ "$previous" != "$installed" ] && { [ -f "$unit_mac" ] || [ -f "$unit_linux" ]; }; then
  say ""
  say "The background service is still running the old version. Restart it on"
  say "$installed, adding any flags you installed it with (such as -life 7d):"
  say "  $cmd service install"
fi

if [ -z "$previous" ]; then
  say ""
  say "Next steps:"
  say "  claude-gc status            # see what is running"
  say "  claude-gc run -dry-run      # preview a pass"
  say "  claude-gc service install   # keep it tidy automatically"
fi
