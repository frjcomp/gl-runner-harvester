#!/bin/sh

set -eu

REPO="frjcomp/gl-runner-harvester"
BINARY="gl-runner-harvester"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-}"

cleanup() {
  if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}

trap cleanup EXIT INT TERM

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 1
  fi
}

download() {
  url="$1"
  out="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$out"
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url"
    return
  fi

  echo "either curl or wget is required" >&2
  exit 1
}

detect_os() {
  os_name=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')
  case "$os_name" in
    linux)
      echo "linux"
      ;;
    darwin)
      echo "darwin"
      ;;
    *)
      echo "unsupported operating system: $os_name" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  arch_name=$(uname -m 2>/dev/null)
  case "$arch_name" in
    x86_64|amd64)
      echo "amd64"
      ;;
    arm64|aarch64)
      echo "arm64"
      ;;
    *)
      echo "unsupported architecture: $arch_name" >&2
      exit 1
      ;;
  esac
}

latest_version() {
  api_url="https://api.github.com/repos/$REPO/releases/latest"
  response_file="$TMP_DIR/latest.json"
  download "$api_url" "$response_file"
  tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$response_file" | head -n 1)
  if [ -z "$tag" ]; then
    echo "failed to determine latest release version" >&2
    exit 1
  fi
  echo "$tag"
}

require_cmd uname
require_cmd mktemp
require_cmd chmod
require_cmd mkdir
require_cmd tar

TMP_DIR=$(mktemp -d)
OS=$(detect_os)
ARCH=$(detect_arch)

if [ -z "$VERSION" ]; then
  VERSION=$(latest_version)
fi

TAG="$VERSION"
case "$TAG" in
  v*)
    VERSION_STRIPPED=${TAG#v}
    ;;
  *)
    VERSION_STRIPPED=$TAG
    TAG="v$TAG"
    ;;
esac

ARCHIVE="${BINARY}_${VERSION_STRIPPED}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ARCHIVE}"

mkdir -p "$INSTALL_DIR"
download "$URL" "$TMP_DIR/$ARCHIVE"
tar -xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"

if [ ! -f "$TMP_DIR/$BINARY" ]; then
  echo "downloaded archive did not contain $BINARY" >&2
  exit 1
fi

chmod +x "$TMP_DIR/$BINARY"
mv "$TMP_DIR/$BINARY" "$INSTALL_DIR/$BINARY"

echo "$BINARY installed to $INSTALL_DIR/$BINARY"
