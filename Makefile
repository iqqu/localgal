APP := localgal
PKG := ./
BINDIR := ./bin/
BIN := $(APP)

# gather metadata
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -X 'main.Version=$(VERSION)' -X 'main.Commit=$(GIT_COMMIT)' -X 'main.BuildDate=$(BUILD_DATE)'

.PHONY: all build run clean test

all: build

build:
	GO111MODULE=on go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(BIN) $(PKG)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)

test:
	go test ./...
