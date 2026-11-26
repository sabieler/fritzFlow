#!/usr/bin/env sh
# Builds FritzFlow for Windows, macOS and Linux into ./dist
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
rm -rf dist && mkdir -p dist

build() {
  os="$1"; arch="$2"; ext="${3:-}"
  out="dist/fritzflow-${VERSION}-${os}-${arch}${ext}"
  echo "→ ${out}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" .
}

go vet ./...
go test ./...
build windows amd64 .exe
build windows arm64 .exe
build darwin  arm64
build darwin  amd64
build linux   amd64
build linux   arm64
build linux   arm
(cd dist && sha256sum fritzflow-* > SHA256SUMS 2>/dev/null || shasum -a 256 fritzflow-* > SHA256SUMS)
ls -lh dist
