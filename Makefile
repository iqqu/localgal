APP := localgal
PKG := ./
BINDIR := ./bin/
DISTDIR := ./dist/
BIN := $(APP)

HOST_GOOS := $(shell go env GOOS)
HOST_GOARCH := $(shell go env GOARCH)
EXT := $(if $(filter $(HOST_GOOS),windows),.exe,)
LDFLAGS_WIN := $(if $(filter $(HOST_GOOS),windows),-H windowsgui,)

# gather metadata
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -X 'main.Version=$(VERSION)' -X 'main.Commit=$(GIT_COMMIT)' -X 'main.BuildDate=$(BUILD_DATE)'

.PHONY: all build build-dist run clean test

all: build

build:
	CGO_ENABLED=1 GO111MODULE=on go build -trimpath -ldflags "$(LDFLAGS) $(LDFLAGS_WIN)" -o $(BINDIR)/$(BIN)$(EXT) $(PKG)

build-dist:
	@mkdir -p $(DISTDIR)
	CGO_ENABLED=1 GO111MODULE=on go build -trimpath -ldflags "-s -w $(LDFLAGS) $(LDFLAGS_WIN)" -o $(DISTDIR)/$(BIN)-$(HOST_GOOS)-$(HOST_GOARCH)$(EXT) $(PKG)

run: build
	./$(BINDIR)/$(BIN)

clean:
	rm -f $(BINDIR)/$(BIN)
	rm -rf $(DISTDIR)/

test:
	go test ./...
