#!/usr/bin/env bash
set -euo pipefail

# Cross-compile naquadah for common targets.

APP=naquadah
CMD=./cmd/naquadah
DIST_DIR=dist

mkdir -p "$DIST_DIR"

build() {
  local goos=$1
  local goarch=$2
  local ext=""
  if [[ "$goos" == "windows" ]]; then ext=".exe"; fi
  local out="$DIST_DIR/${APP}-${goos}-${goarch}${ext}"
  echo "Building $out"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w" -o "$out" "$CMD"
}

# Windows x64
build windows amd64

# macOS Apple Silicon
build darwin arm64

# macOS Intel
build darwin amd64

# Linux x64
build linux amd64

echo "Build artifacts in $DIST_DIR/"
