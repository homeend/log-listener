#!/usr/bin/env sh
# run-tests.sh — run the full log-listener test suite.
#
# Usage:
#   ./run-tests.sh           # go test ./...  (every package)
#   ./run-tests.sh --race    # also enable the race detector
#   ./run-tests.sh --cover   # also print a per-package coverage summary
#   ./run-tests.sh --vet     # also run `go vet ./...` first
#   ./run-tests.sh --full    # vet + race + cover (the full quality gate)
#   ./run-tests.sh --force   # bypass Go's test cache (force a real re-run)
#
# How Go testing works (general notes):
#   * Tests are per-PACKAGE, not per-file. `go test` compiles every *_test.go
#     in a package into a single test binary and runs them together — you
#     cannot run one file in isolation; you scope by package or by test name.
#   * Run everything ........ go test ./...
#   * One package ........... go test ./internal/keymap/
#   * One test (name regex) . go test -run TestDocsUpToDate ./internal/keymap/
#   * Verbose, per-test ..... add -v
#   * Bypass the test cache . add -count=1   (Go caches passing test results)
#   * Race detector ......... add -race      (slower; flags data races)
#   * Coverage summary ...... add -cover     (or -coverprofile=cover.out)
#   Test files end in _test.go and sit next to the code they cover. A package
#   named `foo` is white-box tested by `package foo`; `package foo_test`
#   exercises only the exported API. This repo has no Makefile — this script
#   and build.sh/build.cmd wrap the `go` commands above.
set -eu

# Run from the script's own directory so it works from anywhere.
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

PKG="./..."
race=0
cover=0
vet=0
force=0

for a in "$@"; do
  case "$a" in
    --race)  race=1 ;;
    --cover) cover=1 ;;
    --vet)   vet=1 ;;
    --full)  vet=1; race=1; cover=1 ;;
    --force|--no-cache) force=1 ;;
    -h|--help)
      sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown option: $a (try ./run-tests.sh --help)" >&2
      exit 2
      ;;
  esac
done

flags=""
[ "$race" -eq 1 ]  && flags="$flags -race"
[ "$cover" -eq 1 ] && flags="$flags -cover"
[ "$force" -eq 1 ] && flags="$flags -count=1"

if [ "$vet" -eq 1 ]; then
  echo "==> go vet $PKG"
  go vet "$PKG"
fi

echo "==> go test$flags $PKG"
# Intentional word-splitting of $flags.
# shellcheck disable=SC2086
go test $flags "$PKG"

echo "OK — all tests passed."
