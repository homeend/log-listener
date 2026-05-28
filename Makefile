BINARY := log-listener
PKG    := ./...

.PHONY: build build-static test vet race cover clean run help

help:
	@echo "Targets:"
	@echo "  build         — local binary"
	@echo "  build-static  — CGO_ENABLED=0 static binary (Linux first)"
	@echo "  test          — go test"
	@echo "  vet           — go vet"
	@echo "  race          — go test -race"
	@echo "  cover         — coverage summary"
	@echo "  clean         — remove built binary"
	@echo "  run           — build and run with example config"

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

run: build
	./$(BINARY) --config log-listener.example.yml --no-tui
