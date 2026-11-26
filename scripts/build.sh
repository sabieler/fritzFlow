#!/usr/bin/env sh
# Builds FritzFlow for Windows, macOS and Linux into ./dist
set -eu
cd "$(dirname "$0")/.."

if ! command -v go >/dev/null 2>&1; then
  echo "Fehler: 'go' wurde nicht gefunden." >&2
  echo "  Go installieren (>= 1.21): https://go.dev/dl/" >&2
  echo "  Ist Go schon installiert, den bin-Ordner in den PATH aufnehmen, z. B.:" >&2
  echo "    export PATH=\"\$HOME/go/bin:/usr/local/go/bin:\$PATH\"" >&2
  exit 1
fi
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
rm -rf dist && mkdir -p dist

build() {
  os="$1"; arch="$2"; ext="${3:-}"
  # fixed names without version: the README links to
  # https://github.com/<owner>/<repo>/releases/latest/download/<name>
  out="dist/fritzflow-${os}-${arch}${ext}"
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
echo "$VERSION" > dist/VERSION
ls -lh dist
