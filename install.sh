#!/usr/bin/env bash
# Install the colt CLI into ~/.local/bin (override with COLT_INSTALL_DIR).
#
#   curl -fsSL https://raw.githubusercontent.com/MozeBaltyk/Colt/main/install.sh | bash
#   bash install.sh v0.1.0     # pin a release tag instead of latest
#
# Releases are published by .github/workflows/release.yml as
# colt-<os>-<arch> assets on GitHub Releases.

set -euo pipefail

REPO="MozeBaltyk/Colt"
VERSION="${1:-latest}"
BIN_DIR="${COLT_INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *)
    echo "install.sh: unsupported architecture '$arch'" >&2
    exit 1
    ;;
esac
case "$os" in
  linux | darwin) ;;
  *)
    echo "install.sh: unsupported platform '$os'" >&2
    exit 1
    ;;
esac

name="colt-${os}-${arch}"
if [ "$VERSION" = "latest" ]; then
  url="https://github.com/${REPO}/releases/latest/download/${name}"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${name}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "install.sh: fetching ${url}"
if ! curl -fsSL "$url" -o "$tmp/colt"; then
  echo "install.sh: download failed — is release '${VERSION}' published for ${os}/${arch}?" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/colt" "$BIN_DIR/colt"
echo "install.sh: installed ${BIN_DIR}/colt"