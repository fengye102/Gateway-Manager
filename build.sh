#!/usr/bin/env bash
set -euo pipefail

export CGO_ENABLED=0
mkdir -p build

build_one() {
  local out="${1}" goos="${2}" goarch="${3}" goarm="${4:-}"
  echo "building ${goos}/${goarch}${goarm} -> ${out}"
  GOOS="${goos}" GOARCH="${goarch}" ${goarm:+GOARM="${goarm}"} \
    go build -trimpath -ldflags "-s -w -X main.version=2.1.0" -o "${out}" .
}

build_one build/gateway-manager-linux-amd64       linux   amd64
build_one build/gateway-manager-linux-arm64       linux   arm64
build_one build/gateway-manager-linux-armv7       linux   arm  7
build_one build/gateway-manager-windows-amd64.exe               windows amd64
build_one build/gateway-manager-windows-arm64.exe windows arm64
build_one build/gateway-manager-darwin-amd64      darwin  amd64
build_one build/gateway-manager-darwin-arm64      darwin  arm64

echo "done."
