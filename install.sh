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
mkdir -p "$dest" 2>/dev/null || true

# sudo is only usable when it will not stop to ask for a password: piped from
# curl there is no terminal to type one into, and `sudo: a terminal is required`
# would fail the whole install for an agent or a CI job.
can_sudo() {
  command -v sudo >/dev/null 2>&1 || return 1
  sudo -n true 2>/dev/null && return 0
  # No password cached: only worth prompting when someone is watching a terminal.
  [ -t 1 ]
}

if [ -w "$dest" ]; then
  install -m 0755 "$tmp/${BIN}" "$dest/${BIN}"
elif can_sudo && (echo "Installing to $dest (needs sudo)..." && sudo install -m 0755 "$tmp/${BIN}" "$dest/${BIN}"); then
  :
else
  # Not writable and sudo is unusable (a plain container, a CI job, an agent
  # piping this script): fall back to a per-user directory rather than failing.
  dest="${HOME}/.local/bin"
  echo "${PREFIX}/bin is not writable without a password; installing to $dest instead."
  mkdir -p "$dest"
  install -m 0755 "$tmp/${BIN}" "$dest/${BIN}"
fi

echo "Installed ${dest}/${BIN}."
case ":${PATH}:" in
  *":${dest}:"*) ;;
  *) echo "Note: $dest is not on your PATH. Add it with: export PATH=\"$dest:\$PATH\"" ;;
esac
echo "Run '${BIN} login' to get started, or '${BIN} agent' to print the full manual."
