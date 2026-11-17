#!/bin/sh
set -eu

# Install the latest ckb-whitehat release into ~/.local/bin.
# Override REPO, VERSION, or BIN_DIR for mirrors, pinned versions, or a
# different user-owned install directory.
REPO=${REPO:-nghiadang391/ckb-whitehat}
VERSION=${VERSION:-latest}
BIN_DIR=${BIN_DIR:-"$HOME/.local/bin"}

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) echo "ckb-whitehat: unsupported operating system: $(uname -s)" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "ckb-whitehat: unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac

case "$VERSION" in
  latest) BASE="https://github.com/$REPO/releases/latest/download" ;;
  v*) BASE="https://github.com/$REPO/releases/download/$VERSION" ;;
  *) BASE="https://github.com/$REPO/releases/download/v$VERSION" ;;
esac

ASSET="ckb-whitehat-$OS-$ARCH"
TMP=$(mktemp -d 2>/dev/null || mktemp -d -t ckb-whitehat)
trap 'rm -rf "$TMP"' EXIT INT TERM

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only --quiet --output-document="$2" "$1"
  else
    echo "ckb-whitehat: curl or wget is required" >&2
    exit 1
  fi
}

fetch "$BASE/$ASSET" "$TMP/$ASSET"
fetch "$BASE/sha256sums.txt" "$TMP/sha256sums.txt"

EXPECTED=$(awk -v name="$ASSET" '$2 == name || $2 == "*" name { print $1; exit }' "$TMP/sha256sums.txt")
if [ -z "$EXPECTED" ]; then
  echo "ckb-whitehat: checksum entry missing for $ASSET" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP/$ASSET" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL=$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')
else
  echo "ckb-whitehat: sha256sum or shasum is required" >&2
  exit 1
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "ckb-whitehat: checksum verification failed" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
chmod 0755 "$TMP/$ASSET"
# mv is atomic when source and destination share a filesystem; install into a
# temporary file in the destination first so an interrupted install never
# leaves a partially written executable at the final path.
INSTALL_TMP="$BIN_DIR/.ckb-whitehat.$$"
cp "$TMP/$ASSET" "$INSTALL_TMP"
chmod 0755 "$INSTALL_TMP"
mv -f "$INSTALL_TMP" "$BIN_DIR/ckb-whitehat"

echo "installed ckb-whitehat to $BIN_DIR/ckb-whitehat"
case ":${PATH:-}:" in
  *:"$BIN_DIR":*) ;;
  *) echo "add $BIN_DIR to PATH if it is not already present" ;;
esac
