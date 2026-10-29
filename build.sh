#!/usr/bin/env bash
set -euo pipefail

export CGO_ENABLED=0
mkdir -p build

build_one() {
  local out="${1}" goos="${2}" goarch="${3}" goarm="${4:-}"
  echo "building ${goos}/${goarch}${goarm} -> ${out}"
  GOOS="${goos}" GOARCH="${goarch}" ${goarm:+GOARM="${goarm}"} \
    go build -trimpath -ldflags "-s -w" -o "${out}" .
}

build_one build/pojiagw-linux-amd64       linux   amd64
build_one build/pojiagw-linux-arm64       linux   arm64
build_one build/pojiagw-linux-armv7       linux   arm  7
build_one build/pojiagw.exe               windows amd64
build_one build/pojiagw-windows-arm64.exe windows arm64
build_one build/pojiagw-darwin-amd64      darwin  amd64
build_one build/pojiagw-darwin-arm64      darwin  arm64

echo "done."
