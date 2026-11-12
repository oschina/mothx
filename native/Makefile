# MothX Native — pure Go desktop app built with MyGo.
#
# The app is a nested Go module. Use these targets from native/, or the
# matching native-* targets in the repository's root Makefile.

GO ?= go
MYGO ?= $(GO) tool mygo
OUT ?= build
BIN ?= $(OUT)/mothx-native
CGO_ENABLED ?= 0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
NATIVE_LDFLAGS ?= -s -w -X main.nativeBuildVersion=$(VERSION)
export CGO_ENABLED

.DEFAULT_GOAL := help

.PHONY: help dev run build test test-race vet fmt tidy dist dist-linux dist-darwin dist-windows clean

help:
	@echo "MothX Native"
	@echo ""
	@echo "  make dev           Run the app with the development inspector"
	@echo "  make run           Same as dev"
	@echo "  make build         Build a stripped binary into $(BIN)"
	@echo "  make test          Run transcript and headless UI tests"
	@echo "  make test-race     Run tests with the race detector"
	@echo "  make vet           Run go vet and MyGo lifetime/goroutine checks"
	@echo "  make fmt           Format Go sources"
	@echo "  make tidy          Tidy the module"
	@echo "  make dist          Package the app for the current platform"
	@echo "  make dist-linux    Package linux/amd64 (deb, tar.gz, PKGBUILD)"
	@echo "  make dist-darwin   Package darwin/universal"
	@echo "  make dist-windows  Package windows/amd64"
	@echo "  make clean         Remove $(OUT)/"
	@echo ""
	@echo "Runtime selection: set MOTHX_BIN=/path/to/mothx, or rely on PATH"
	@echo "or the repository's ../bin/mothx fallback."

dev:
	$(GO) run -ldflags "$(NATIVE_LDFLAGS)" .

run: dev

build:
	@mkdir -p $(OUT)
	$(GO) build -trimpath -tags mygo_noinspector \
		-ldflags "$(NATIVE_LDFLAGS)" -o $(BIN) .

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...
	$(MYGO) vet .

fmt:
	gofmt -w .

tidy:
	$(GO) mod tidy

dist:
	MYGO_LDFLAGS="-X main.nativeBuildVersion=$(VERSION)" $(MYGO) build

dist-linux:
	MYGO_LDFLAGS="-X main.nativeBuildVersion=$(VERSION)" $(MYGO) build -platform linux/amd64

dist-darwin:
	MYGO_LDFLAGS="-X main.nativeBuildVersion=$(VERSION)" $(MYGO) build -platform darwin/universal

dist-windows:
	MYGO_LDFLAGS="-X main.nativeBuildVersion=$(VERSION)" $(MYGO) build -platform windows/amd64

clean:
	rm -rf $(OUT)
