.PHONY: help build build-all install test test-all test-ui test-desktop test-npm test-pypi fuzz lint fmt clean run serve docs-llms docs-models
.PHONY: ui-install ui-build ui-dev ui-preview
.PHONY: desktop-runtime desktop-vendor desktop-build desktop-dev desktop-dist desktop-version-check desktop-dist-dev-mac desktop-dist-dev-win desktop-dist-dev-linux
.PHONY: build-linux build-linux-loong64 build-linux-musl build-darwin build-windows
.PHONY: build-freebsd build-openbsd build-netbsd
.PHONY: dist dist-linux dist-darwin dist-windows
.PHONY: dist-freebsd dist-openbsd dist-netbsd
.PHONY: dist-linux-loong64
.PHONY: dist-deb dist-tarball dist-zip dist-installers
.PHONY: clean-all checksums
.PHONY: npm-version npm-binaries npm-packages npm-pack npm-verify-platforms npm-publish-all npm-publish-pre npm-publish
.PHONY: pypi-version pypi-packages pypi-pack pypi-publish pypi-publish-pre

# Variables
BINARY_NAME=mothx
VERSION=$(or $(GITEE_BRANCH),$(shell git describe --tags --always --dirty 2>/dev/null),unknown)
FUZZTIME ?= 10s
PRE_VERSION=$(if $(filter %-pre,$(VERSION)),$(VERSION),$(VERSION)-pre)
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X github.com/oschina/mothx/internal/version.Version=$(VERSION) -X github.com/oschina/mothx/internal/ua.Version=$(VERSION)"
GOBUILD_FLAGS=-trimpath
DIST_DIR=dist
CHECKSUM_FILE=$(DIST_DIR)/checksums.txt
PYTHON ?= python3
NPM ?= npm
NPM_PUBLISH_IF_NEEDED = NPM="$(NPM)" node scripts/npm-publish-if-needed.js

# Python venv for PyPI builds (isolated from system Python)
PYPI_VENV := $(CURDIR)/pypi/.venv-build
PYPI_PYTHON := $(PYPI_VENV)/bin/python

# Create venv and install build deps (idempotent — only runs if venv is missing)
$(PYPI_VENV)/bin/python:
	@echo "Creating PyPI build venv..."
	python3 -m venv $(PYPI_VENV)
	$(PYPI_VENV)/bin/pip install -q --upgrade "setuptools>=77.0.0" build twine
	@echo "PyPI build deps ready: $(PYPI_VENV)"

# UPX compression (only for Linux x86_64 binaries)
USE_UPX ?= true
ifeq ($(shell which upx 2>/dev/null),)
USE_UPX = false
endif
ifeq ($(USE_UPX),true)
UPX_CMD = upx -9
else
UPX_CMD = @true
endif

# Platforms and architectures (verified to compile; 32-bit blocked by larksuite SDK)
# linux:    amd64 arm64 loong64 ppc64le s390x riscv64
# darwin:   amd64 arm64
# windows:  amd64 arm64
# freebsd:  amd64 arm64
# openbsd:  amd64 arm64
# netbsd:   amd64

# Default target
help:
	@echo "VibeCoding Build System"
	@echo ""
	@echo "Build targets:"
	@echo "  build            Build for current platform"
	@echo "  build-linux      Build for Linux (amd64, arm64, loong64, ppc64le, s390x, riscv64)"
	@echo "  build-linux-loong64 Build for Linux LoongArch64"
	@echo "  build-linux-musl Build for Linux musl (amd64, arm64)"
	@echo "  build-darwin     Build for macOS (amd64, arm64)"
	@echo "  build-windows    Build for Windows (amd64, arm64)"
	@echo "  build-freebsd    Build for FreeBSD (amd64, arm64)"
	@echo "  build-openbsd    Build for OpenBSD (amd64, arm64)"
	@echo "  build-netbsd     Build for NetBSD (amd64)"
	@echo "  build-all        Build for all platforms and architectures"
	@echo ""
	@echo "Distribution targets:"
	@echo "  dist           Build all distribution packages"
	@echo "  dist-linux     Build Linux packages (tar.gz + deb)"
	@echo "  dist-darwin    Build macOS packages (tar.gz)"
	@echo "  dist-windows   Build Windows packages (zip)"
	@echo "  dist-freebsd   Build FreeBSD packages (tar.gz)"
	@echo "  dist-openbsd   Build OpenBSD packages (tar.gz)"
	@echo "  dist-netbsd    Build NetBSD packages (tar.gz)"
	@echo "  dist-linux-loong64 Build Linux LoongArch64 packages"
	@echo "  dist-deb       Build Debian packages only"
	@echo "  dist-tarball   Build tarball packages only"
	@echo "  dist-zip       Build zip packages only"
	@echo "  dist-installers Build self-extracting Windows/macOS installers"
	@echo ""
	@echo "NPM targets:"
	@echo "  npm-version       Sync version to npm package"
	@echo "  npm-packages      Build platform-specific npm packages"
	@echo "  npm-verify-platforms Verify published platform packages exist"
	@echo "  npm-pack          Pack mothx-installer, compatibility package, and all platform packages"
	@echo "  npm-publish-all   Publish mothx-installer, compatibility package, and all platform packages"
	@echo "  npm-publish-pre   Publish pre-release packages"
	@echo "  npm-binaries      [Legacy] Build all binaries into single package"
	@echo "  npm-publish       [Legacy] Publish main package only"
	@echo ""
	@echo "Desktop targets:"
	@echo "  desktop-vendor     Vendor the platform MothX runtime for the desktop app"
	@echo "  desktop-build      Build the desktop Electron shell"
	@echo "  desktop-dev        Run Desktop with renderer watch/reload, DevTools, and local CDP debugging"
	@echo "  desktop-dist       Package the desktop app for the current platform"
	@echo "  desktop-version-check Check desktop/runtime version alignment"
	@echo "  desktop-dist-dev-mac Build unsigned macOS development package"
	@echo "  desktop-dist-dev-win Build Windows portable development package"
	@echo "  desktop-dist-dev-linux Build Linux AppImage development package"
	@echo ""
	@echo "Serve UI targets:"
	@echo "  ui-install        Install Serve Web UI dependencies"
	@echo "  ui-build          Build Serve Web UI into ui/dist"
	@echo "  ui-dev            Run Serve Web UI dev server"
	@echo "  ui-preview        Preview built Serve Web UI"
	@echo ""
	@echo "PyPI targets:"
	@echo "  pypi-version      Sync version to PyPI package"
	@echo "  pypi-packages     Build platform-specific PyPI wheels"
	@echo "  pypi-pack         Pack all PyPI wheels"
	@echo "  pypi-publish      Publish PyPI wheels"
	@echo "  pypi-publish-pre  Publish pre-release PyPI wheels"
	@echo ""
	@echo "Other targets:"
	@echo "  install        Install via go install"
	@echo "  test           Run tests"
	@echo "  test-all       Run Go, Web UI, desktop, npm, and PyPI tests"
	@echo "  test-ui        Run Web UI unit tests"
	@echo "  test-desktop   Run Electron desktop tests"
	@echo "  test-npm       Run npm installer tests"
	@echo "  test-pypi      Run PyPI installer tests"
	@echo "  fuzz           Run fuzz targets (set FUZZTIME to change duration)"
	@echo "  lint           Run linter"
	@echo "  fmt            Format code"
	@echo "  clean          Remove build artifacts"
	@echo "  clean-all      Remove everything including dist"
	@echo "  checksums      Generate checksums for all dist files"
	@echo "  run            Build and run"
	@echo "  serve          Build and start serve mode"
	@echo "  docs-llms      Generate llms.txt indexes and full documentation files"
	@echo "  docs-models    Generate docs/provider-model-list.md from the built-in provider presets"
	@echo "  help           Show this help"

docs-llms:
	$(PYTHON) docs/scripts/generate-llms.py

docs-models:
	$(PYTHON) docs/scripts/generate-models.py

# Build for current platform
build:
	go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/mothx

# Platform builds
build-linux:
	@echo "Building for Linux..."
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-amd64 ./cmd/mothx
	GOOS=linux GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-arm64 ./cmd/mothx
	GOOS=linux GOARCH=loong64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-loong64 ./cmd/mothx
	GOOS=linux GOARCH=ppc64le go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-ppc64le ./cmd/mothx
	GOOS=linux GOARCH=s390x go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-s390x ./cmd/mothx
	GOOS=linux GOARCH=riscv64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-riscv64 ./cmd/mothx
	@echo "Compressing Linux x86_64 binary with UPX..."
	$(UPX_CMD) bin/$(BINARY_NAME)-linux-amd64

build-linux-loong64:
	@echo "Building for Linux LoongArch64..."
	@mkdir -p bin
	GOOS=linux GOARCH=loong64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-loong64 ./cmd/mothx

# musl: static build with CGO_ENABLED=0
build-linux-musl:
	@echo "Building for Linux musl..."
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-musl-amd64 ./cmd/mothx
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-linux-musl-arm64 ./cmd/mothx
	@echo "Compressing Linux x86_64 musl binary with UPX..."
	$(UPX_CMD) bin/$(BINARY_NAME)-linux-musl-amd64

build-darwin:
	@echo "Building for macOS..."
	@mkdir -p bin
	GOOS=darwin GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-amd64 ./cmd/mothx
	GOOS=darwin GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-darwin-arm64 ./cmd/mothx

build-windows:
	@echo "Building for Windows..."
	@mkdir -p bin
	GOOS=windows GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-windows-amd64.exe ./cmd/mothx
	GOOS=windows GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-windows-arm64.exe ./cmd/mothx

build-freebsd:
	@echo "Building for FreeBSD..."
	@mkdir -p bin
	GOOS=freebsd GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-freebsd-amd64 ./cmd/mothx
	GOOS=freebsd GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-freebsd-arm64 ./cmd/mothx

build-openbsd:
	@echo "Building for OpenBSD..."
	@mkdir -p bin
	GOOS=openbsd GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-openbsd-amd64 ./cmd/mothx
	GOOS=openbsd GOARCH=arm64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-openbsd-arm64 ./cmd/mothx

build-netbsd:
	@echo "Building for NetBSD..."
	@mkdir -p bin
	GOOS=netbsd GOARCH=amd64 go build $(GOBUILD_FLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME)-netbsd-amd64 ./cmd/mothx

# Build all platforms
build-all: build-linux build-linux-musl build-darwin build-windows build-freebsd build-openbsd build-netbsd
	@echo ""
	@echo "Build complete! Binaries in bin/"
	@ls -lh bin/

# Install
install:
	go install $(GOBUILD_FLAGS) $(LDFLAGS) ./cmd/mothx

# Go tests (the historical default, including race detection)
test:
	go test -v -race ./...

# Cross-surface test suite. Keep the Go target above stable for contributors
# who only have the Go toolchain; this target verifies every maintained client
# and installer entry point as well.
test-all: test test-ui test-desktop test-npm test-pypi

test-ui:
	cd ui && $(NPM) test

test-desktop:
	cd desktop && $(NPM) test

test-npm:
	node --test scripts/*.test.js

test-pypi:
	PYTHONPATH=$(CURDIR)/pypi/src $(PYTHON) -m unittest discover -s pypi/tests -v

# Fuzz tests must run one package at a time because Go fuzzing does not accept
# multiple packages in a single invocation.
fuzz:
	go test ./internal/esm -run='^$$' -fuzz=. -fuzztime=$(FUZZTIME)
	go test ./internal/mcp -run='^$$' -fuzz=. -fuzztime=$(FUZZTIME)
	go test ./internal/util -run='^$$' -fuzz=. -fuzztime=$(FUZZTIME)

# Serve Web UI
ui-install:
	cd ui && $(NPM) ci

ui-build:
	cd ui && $(NPM) run build

ui-dev: build
	@echo "Starting serve backend on 127.0.0.1:7872..."
	@trap 'kill $${BACKEND_PID} 2>/dev/null; exit' INT TERM; \
	./bin/$(BINARY_NAME) serve --unsafe --verbose & \
	BACKEND_PID=$$!; \
	for i in 1 2 3 4 5; do \
		if curl -s http://127.0.0.1:7872/health >/dev/null 2>&1; then break; fi; \
		sleep 1; \
	done; \
	cd ui && $(NPM) run dev; \
	EXIT_CODE=$$?; \
	kill $${BACKEND_PID} 2>/dev/null; \
	wait $${BACKEND_PID} 2>/dev/null; \
	exit $${EXIT_CODE}

desktop-runtime:
	cd desktop && npm ci --no-audit --no-fund && npm run version:set && npm run build:runtime

desktop-version-check:
	cd desktop && npm run version:set && npm run version:check

desktop-vendor: desktop-runtime

desktop-build:
	cd desktop && npm run build

desktop-dev: desktop-runtime
	cd desktop && npm run dev

desktop-dist: desktop-runtime desktop-build
	cd desktop && npm run version:set && npx electron-builder --config electron-builder.yml

desktop-dist-dev-mac:
	cd desktop && npm run dist:dev:mac

desktop-dist-dev-win:
	cd desktop && npm run dist:dev:win

desktop-dist-dev-linux:
	cd desktop && npm run dist:dev:linux

ui-preview:
	cd ui && $(NPM) run preview

# Lint
lint:
	golangci-lint run ./...

# Format
fmt:
	gofmt -w .
	goimports -w .

# Clean
clean:
	rm -rf bin/

# Clean all
clean-all: clean
	rm -rf $(DIST_DIR)
	rm -f npm/*.tgz
	rm -rf pypi/dist pypi/build pypi/*.egg-info $(PYPI_VENV)

# Run
run: build
	./bin/$(BINARY_NAME)

# Start serve mode
serve: ui-build build
	./bin/$(BINARY_NAME) serve

# Distribution: tar.gz for Unix-like platforms
dist-tarball: build-linux build-linux-musl build-darwin build-freebsd build-openbsd build-netbsd
	@echo ""
	@echo "Creating tarball packages..."
	@for arch in amd64 arm64 loong64 ppc64le s390x riscv64; do \
		echo "  Packaging $(BINARY_NAME)-linux-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh linux $${arch} $(VERSION); \
	done
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-darwin-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh darwin $${arch} $(VERSION); \
	done
	@echo "  Packaging $(BINARY_NAME)-linux-musl-amd64.tar.gz..."; \
	./scripts/build-tarball.sh linux-musl amd64 $(VERSION)
	@echo "  Packaging $(BINARY_NAME)-linux-musl-arm64.tar.gz..."; \
	./scripts/build-tarball.sh linux-musl arm64 $(VERSION)
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-freebsd-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh freebsd $${arch} $(VERSION); \
	done
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-openbsd-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh openbsd $${arch} $(VERSION); \
	done
	@echo "  Packaging $(BINARY_NAME)-netbsd-amd64.tar.gz..."; \
	./scripts/build-tarball.sh netbsd amd64 $(VERSION)

# Distribution: deb for Linux
dist-deb: build-linux build-linux-musl
	@echo ""
	@echo "Creating Debian packages..."
	@for arch in amd64 arm64 loong64; do \
		echo "  Packaging $(BINARY_NAME)_$(VERSION)_$${arch}.deb..."; \
		./scripts/build-deb.sh $${arch} $(VERSION); \
	done
	@echo "  Packaging $(BINARY_NAME)_$(VERSION)_amd64-musl.deb..."; \
	./scripts/build-deb.sh amd64-musl $(VERSION)
	@echo "  Packaging $(BINARY_NAME)_$(VERSION)_arm64-musl.deb..."; \
	./scripts/build-deb.sh arm64-musl $(VERSION)

# Distribution: zip for Windows
dist-zip: build-windows
	@echo ""
	@echo "Creating Windows zip packages..."
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-windows-$${arch}.zip..."; \
		./scripts/build-zip.sh $${arch} $(VERSION); \
	done

# Distribution: self-extracting installers for non-developer desktop users.
# These are intentionally Linux-buildable and do not require Windows/macOS signing tools.
dist-installers: build-windows build-darwin
	@echo ""
	@echo "Creating self-extracting desktop installers..."
	./scripts/build-self-extract-installers.sh $(VERSION)

# Platform distributions
dist-linux: dist-deb dist-tarball
	@echo "Linux packages complete!"

dist-linux-loong64: build-linux-loong64
	@echo ""
	@echo "Creating Linux LoongArch64 packages..."
	./scripts/build-tarball.sh linux loong64 $(VERSION)
	./scripts/build-deb.sh loong64 $(VERSION)
	@echo "Linux LoongArch64 packages complete!"

dist-darwin: dist-tarball
	@echo "macOS packages complete!"

dist-windows: dist-zip
	@echo "Windows packages complete!"

dist-freebsd: build-freebsd
	@echo ""
	@echo "Creating FreeBSD packages..."
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-freebsd-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh freebsd $${arch} $(VERSION); \
	done
	@echo "FreeBSD packages complete!"

dist-openbsd: build-openbsd
	@echo ""
	@echo "Creating OpenBSD packages..."
	@for arch in amd64 arm64; do \
		echo "  Packaging $(BINARY_NAME)-openbsd-$${arch}.tar.gz..."; \
		./scripts/build-tarball.sh openbsd $${arch} $(VERSION); \
	done
	@echo "OpenBSD packages complete!"

dist-netbsd: build-netbsd
	@echo ""
	@echo "Creating NetBSD packages..."
	./scripts/build-tarball.sh netbsd amd64 $(VERSION)
	@echo "NetBSD packages complete!"

# Generate checksums
checksums:
	@echo "Generating checksums..."
	@cd $(DIST_DIR) && \
	find . -type f \( -name "*.tar.gz" -o -name "*.deb" -o -name "*.zip" \) | sort | \
	while read f; do \
		sha256sum "$$f"; \
	done > checksums.txt
	@echo "Checksums written to $(CHECKSUM_FILE)"
	@cat $(CHECKSUM_FILE)

# Build all distribution packages
dist: dist-linux dist-darwin dist-windows dist-freebsd dist-openbsd dist-netbsd checksums
	@echo ""
	@echo "=========================================="
	@echo "All distribution packages built!"
	@echo ""
	@echo "Location: $(DIST_DIR)/"
	@echo ""
	@ls -lh $(DIST_DIR)/*/* 2>/dev/null || true
	@echo ""
	@echo "Checksums: $(CHECKSUM_FILE)"
	@echo "=========================================="

# NPM targets
npm-version:
	./scripts/sync-npm-version.sh $(VERSION)

# Legacy: build all binaries into single package (use npm-packages instead)
npm-binaries: build-all
	@echo "WARNING: npm-binaries is deprecated, use npm-packages instead" >&2
	./scripts/build-npm.sh

# Build platform-specific packages
npm-packages: build-all
	./scripts/build-npm-packages.sh

# Pack main packages + platform packages
npm-pack: npm-version npm-packages
	@echo "Packing platform packages..."
	@set -e; for d in npm/packages/*/; do \
		if [ -f "$$d/package.json" ]; then \
			echo "  Packing $$(basename $$d)..."; \
			cd "$$d" && npm pack && cd - > /dev/null; \
			mv "$$d"/*.tgz npm/ 2>/dev/null || true; \
		fi; \
	done
	@echo "Packing mothx-installer package..."
	cd npm/mothx && npm pack
	mv npm/mothx/*.tgz npm/ 2>/dev/null || true
	@echo "Packing compatibility package..."
	cd npm && npm pack
	@echo "Done. Tarballs in npm/"

# Publish platform packages first, then main packages
npm-verify-platforms:
	node scripts/verify-npm-platform-packages.js npm/mothx/package.json

npm-publish-all: npm-version npm-packages
	@echo "Publishing platform packages..."
	@set -e; for d in npm/packages/*/; do \
		if [ -f "$$d/package.json" ]; then \
			echo "  Publishing $$(basename $$d)..."; \
			$(NPM_PUBLISH_IF_NEEDED) --tag latest "$$d"; \
		fi; \
	done
	$(MAKE) npm-verify-platforms
	@echo "Publishing mothx-installer package..."
	$(NPM_PUBLISH_IF_NEEDED) --tag latest npm/mothx
	@echo "Publishing compatibility package..."
	$(NPM_PUBLISH_IF_NEEDED) --tag latest npm
	@echo "Published all packages!"

# Publish pre-release
npm-publish-pre:
	./scripts/sync-npm-version.sh $(PRE_VERSION)
	$(MAKE) npm-packages VERSION=$(PRE_VERSION)
	@echo "Publishing platform packages (pre-release)..."
	@set -e; for d in npm/packages/*/; do \
		if [ -f "$$d/package.json" ]; then \
			echo "  Publishing $$(basename $$d)..."; \
			$(NPM_PUBLISH_IF_NEEDED) --tag next "$$d"; \
		fi; \
	done
	$(MAKE) npm-verify-platforms
	@echo "Publishing mothx-installer package (pre-release)..."
	$(NPM_PUBLISH_IF_NEEDED) --tag next npm/mothx
	@echo "Publishing compatibility package (pre-release)..."
	$(NPM_PUBLISH_IF_NEEDED) --tag next npm
	@echo "Published all packages (pre-release)!"

# Legacy: publish main package only (use npm-publish-all instead)
npm-publish: npm-version npm-binaries
	@echo "WARNING: npm-publish is deprecated, use npm-publish-all instead" >&2
	$(NPM_PUBLISH_IF_NEEDED) --tag latest npm

# PyPI targets

# Target-specific variable: use the isolated venv for all PyPI operations
pypi-version: PYTHON := $(PYPI_PYTHON)
pypi-packages: PYTHON := $(PYPI_PYTHON)
pypi-pack: PYTHON := $(PYPI_PYTHON)
pypi-publish: PYTHON := $(PYPI_PYTHON)
pypi-publish-pre: PYTHON := $(PYPI_PYTHON)

pypi-version: $(PYPI_VENV)/bin/python
	PYTHON="$(PYTHON)" ./scripts/sync-pypi-version.sh $(VERSION)

# Build platform-specific wheels
pypi-packages: build-all $(PYPI_VENV)/bin/python
	PYTHON="$(PYTHON)" ./scripts/build-pypi-packages.sh

# Pack PyPI wheels
pypi-pack: pypi-version pypi-packages
	@echo "Done. Wheels in pypi/dist/"

# Publish PyPI wheels
pypi-publish: pypi-pack $(PYPI_VENV)/bin/python
	$(PYTHON) -m twine upload pypi/dist/*.whl

# Publish PyPI pre-release wheels
pypi-publish-pre:
	PYTHON="$(PYPI_PYTHON)" ./scripts/sync-pypi-version.sh $(PRE_VERSION)
	$(MAKE) pypi-packages VERSION=$(PRE_VERSION) PYTHON=$(PYPI_PYTHON)
	$(PYPI_PYTHON) -m twine upload pypi/dist/*.whl
