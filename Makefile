BINARY := log-listener
PKG    := ./...

.PHONY: build build-static test vet race cover clean demo help

help:
	@echo "Targets:"
	@echo "  build         — local binary"
	@echo "  build-static  — CGO_ENABLED=0 static binary (Linux: fully static via -extldflags)"
	@echo "  test          — go test"
	@echo "  vet           — go vet"
	@echo "  race          — go test -race"
	@echo "  cover         — coverage summary"
	@echo "  clean         — remove built binary"
	@echo "  demo          — build and tail a tempdir of synthetic log files"

build:
	go build -o $(BINARY) ./cmd/log-listener

build-static:
	CGO_ENABLED=0 go build \
	    -trimpath \
	    -ldflags '-s -w -extldflags "-static"' \
	    -o $(BINARY) ./cmd/log-listener

test:
	go test $(PKG)

vet:
	go vet $(PKG)

race:
	go test -race $(PKG)

cover:
	go test -cover $(PKG)

clean:
	rm -f $(BINARY)

demo: build
	@DIR=$$(mktemp -d); \
	    echo "Demo dir: $$DIR (drop log lines into *.log there)"; \
	    printf '2026-05-28 INFO {"hello":"world"}\n' > $$DIR/seed.log; \
	    ./$(BINARY) -d $$DIR -r 'name:\.log$$' --no-tui
