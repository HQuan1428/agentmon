#!/bin/sh
# Install amo (Agent Monitor) — the latest release binary for Linux.
#   curl -fsSL https://raw.githubusercontent.com/HQuan1428/agentmon/main/install.sh | sh
# Override the install dir with BIN_DIR, e.g.
#   curl -fsSL .../install.sh | BIN_DIR=/usr/local/bin sh
set -eu

REPO="HQuan1428/agentmon"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"

die() { echo "install: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "amo is Linux-only (found $(uname -s))"
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar  >/dev/null 2>&1 || die "tar is required"

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m) (only amd64, arm64)" ;;
esac

url="https://github.com/$REPO/releases/latest/download/amo_linux_${arch}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading amo (linux/$arch) ..."
curl -fsSL "$url" -o "$tmp/amo.tar.gz" \
  || die "download failed — is there a published release yet? ($url)"
tar -xzf "$tmp/amo.tar.gz" -C "$tmp"
[ -f "$tmp/amo" ] || die "archive did not contain an amo binary"

mkdir -p "$BIN_DIR"
mv "$tmp/amo" "$BIN_DIR/amo"
chmod +x "$BIN_DIR/amo"

echo "amo installed to $BIN_DIR/amo"
"$BIN_DIR/amo" --version || true

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "Note: $BIN_DIR is not on your PATH. Add it, e.g.:"
     echo "  echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.bashrc && . ~/.bashrc" ;;
esac
