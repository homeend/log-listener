#!/usr/bin/env sh
# Makeless build helper for log-listener (Linux + macOS).
# Mirrors the Makefile targets so `make` is not required.
#
# Usage: ./build.sh [target]
#   build         local binary (default)
#   build-static  CGO_ENABLED=0 static binary
#   test          go test ./...
#   vet           go vet ./...
#   race          go test -race ./...
#   cover         coverage summary
#   clean         remove built binary
#   help          show this list
set -eu

BINARY="log-listener"
PKG="./..."
CMD="./cmd/log-listener"

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
  test)  go test "$PKG" ;;
  vet)   go vet "$PKG" ;;
  race)  go test -race "$PKG" ;;
  cover) go test -cover "$PKG" ;;
  clean)
    rm -f "$BINARY"
    echo "removed ./$BINARY"
    ;;
  help|-h|--help)
    sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
    ;;
  *)
    echo "unknown target: $target (try ./build.sh help)" >&2
    exit 2
    ;;
esac
