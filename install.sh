#!/bin/sh
# Install the latest hostim CLI binary for the current OS/arch.
# Usage: curl -fsSL https://raw.githubusercontent.com/hostimdev/cli/main/install.sh | sh
set -e

REPO="hostimdev/cli"
BIN="hostim"
PREFIX="${PREFIX:-/usr/local}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$tag" ]; then
  echo "could not determine latest release" >&2
  exit 1
fi

url="https://github.com/${REPO}/releases/download/${tag}/${BIN}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $BIN $tag ($os/$arch)..."
curl -fsSL "$url" -o "$tmp/${BIN}.tar.gz"
tar -xzf "$tmp/${BIN}.tar.gz" -C "$tmp"

dest="${PREFIX}/bin"
if [ -w "$dest" ]; then
  install -m 0755 "$tmp/${BIN}" "$dest/${BIN}"
else
  echo "Installing to $dest (needs sudo)..."
  sudo install -m 0755 "$tmp/${BIN}" "$dest/${BIN}"
fi

echo "Installed $(command -v ${BIN}). Run 'hostim login' to get started."
