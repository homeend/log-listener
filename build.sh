#!/usr/bin/env sh
# Makeless build helper for log-listener (Linux + macOS).
# Mirrors the Makefile targets so `make` is not required.
#
# Usage: ./build.sh [target]
#   build         local binary (default)
#   build-static  CGO_ENABLED=0 static binary
#   build-nomcp   binary without the MCP server (drops the go-sdk dependency)
#   build-nosse   binary without the SSE server
#   build-minimal binary without MCP and SSE
#   test          go test ./...
#   test-nomcp    go test -tags nomcp ./...
#   test-nosse    go test -tags nosse ./...
#   test-minimal  go test -tags "nomcp nosse" ./...
#   vet           go vet ./...
#   race          go test -race ./...
#   cover         coverage summary
#   keybindings-docs  regenerate KEYBINDINGS.md from the keymap
#   clean         remove built binary
#   help          show this list
set -eu

BINARY="log-listener"
PKG="./..."
CMD="."

# Resolve to the script's own directory so it works from anywhere.
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

target="${1:-build}"

case "$target" in
  build)
    go build -o "$BINARY" "$CMD"
    echo "built ./$BINARY"
    ;;
  build-static)
    # Linux supports a fully static binary via the external linker flag; macOS
    # does not (no static libc), so there we settle for a CGO-free binary.
    os="$(uname -s)"
    if [ "$os" = "Linux" ]; then
      CGO_ENABLED=0 go build -trimpath \
        -ldflags '-s -w -extldflags "-static"' \
        -o "$BINARY" "$CMD"
    else
      CGO_ENABLED=0 go build -trimpath \
        -ldflags '-s -w' \
        -o "$BINARY" "$CMD"
    fi
    echo "built static ./$BINARY"
    ;;
  build-nomcp)
    go build -tags nomcp -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no MCP)"
    ;;
  build-nosse)
    go build -tags nosse -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no SSE)"
    ;;
  build-minimal)
    go build -tags "nomcp nosse" -o "$BINARY" "$CMD"
    echo "built ./$BINARY (no MCP, no SSE)"
    ;;
  test)  go test "$PKG" ;;
  test-nomcp)   go test -tags nomcp "$PKG" ;;
  test-nosse)   go test -tags nosse "$PKG" ;;
  test-minimal) go test -tags "nomcp nosse" "$PKG" ;;
  vet)   go vet "$PKG" ;;
  race)  go test -race "$PKG" ;;
  cover) go test -cover "$PKG" ;;
  keybindings-docs)
    go run "$CMD" --keybindings-doc > KEYBINDINGS.md
    echo "wrote ./KEYBINDINGS.md"
    ;;
  clean)
    rm -f "$BINARY"
    echo "removed ./$BINARY"
    ;;
  help|-h|--help)
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    ;;
  *)
    echo "unknown target: $target (try ./build.sh help)" >&2
    exit 2
    ;;
esac
