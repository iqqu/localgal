APP := localgal
PKG := ./
BINDIR := ./bin/
DISTDIR := ./dist/
BIN := $(APP)

# gather metadata
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -X 'main.Version=$(VERSION)' -X 'main.Commit=$(GIT_COMMIT)' -X 'main.BuildDate=$(BUILD_DATE)'

# target platforms for cross compilation
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: all build build-cross run clean test

all: build

build:
	GO111MODULE=on go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(BIN) $(PKG)

build-cross:
	@mkdir -p $(DISTDIR)
	@set -e; \
	for platform in $(PLATFORMS); do \
	    os=$$(echo $$platform | cut -d/ -f1); \
	    arch=$$(echo $$platform | cut -d/ -f2); \
	    ext=""; \
	    [ "$$os" = "windows" ] && ext=".exe"; \
	    out="$(DISTDIR)/$(APP)-$$os-$$arch$$ext"; \
	    echo "Building $$out"; \
	    GOOS=$$os GOARCH=$$arch GO111MODULE=on \
	        go build -ldflags "$(LDFLAGS)" -o "$$out" $(PKG); \
	done

run: build
	./$(BINDIR)/$(BIN)

clean:
	rm -f $(BINDIR)/$(BIN)
	rm -rf $(DISTDIR)/

test:
	go test ./...
