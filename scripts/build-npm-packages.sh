#!/bin/bash

# Build platform-specific npm packages with optionalDependencies architecture.
# Each platform gets its own package containing only its binary.
# Main packages (mothx-installer and the transitional vibecoding-installer)
# declare all platforms as optionalDependencies.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
NPM_DIR="$PROJECT_ROOT/npm"
MOTHX_NPM_DIR="$NPM_DIR/mothx"
BUILD_DIR="$PROJECT_ROOT/bin"
PACKAGES_DIR="$NPM_DIR/packages"

ensure_wrapper() {
  mkdir -p "$NPM_DIR/bin"
  mkdir -p "$MOTHX_NPM_DIR/bin"
  find "$NPM_DIR/bin" -mindepth 1 -maxdepth 1 -type f ! -name vibecoding -delete
  find "$MOTHX_NPM_DIR/bin" -mindepth 1 -maxdepth 1 -type f ! -name mothx -delete
  if ! cmp -s "$SCRIPT_DIR/npm-installer-wrapper.js" "$NPM_DIR/bin/vibecoding"; then
    cp "$SCRIPT_DIR/npm-installer-wrapper.js" "$NPM_DIR/bin/vibecoding"
  fi
  if ! cmp -s "$SCRIPT_DIR/npm-installer-wrapper.js" "$MOTHX_NPM_DIR/bin/mothx"; then
    cp "$SCRIPT_DIR/npm-installer-wrapper.js" "$MOTHX_NPM_DIR/bin/mothx"
  fi
  chmod +x "$NPM_DIR/bin/vibecoding"
  chmod +x "$MOTHX_NPM_DIR/bin/mothx"
}

# Clean packages directory
rm -rf "$PACKAGES_DIR"

# Check if binaries exist
ensure_wrapper

if [ ! -d "$BUILD_DIR" ]; then
  echo "Error: Build directory not found. Run 'make build-all' first."
  exit 1
fi

# Read version from main package.json
VERSION=$(node -e "console.log(require('$NPM_DIR/package.json').version)")

# Platform definitions. Values are current Go build artifact names; npm package
# names are generated with the mothx-installer-* prefix below.
declare -A PLATFORMS=(
  ["linux-x64"]="mothx-linux-amd64"
  ["linux-arm64"]="mothx-linux-arm64"
  ["linux-loong64"]="mothx-linux-loong64"
  ["linux-ppc64le"]="mothx-linux-ppc64le"
  ["linux-s390x"]="mothx-linux-s390x"
  ["linux-riscv64"]="mothx-linux-riscv64"
  ["linux-musl-x64"]="mothx-linux-musl-amd64"
  ["linux-musl-arm64"]="mothx-linux-musl-arm64"
  ["darwin-x64"]="mothx-darwin-amd64"
  ["darwin-arm64"]="mothx-darwin-arm64"
  ["win32-x64"]="mothx-windows-amd64.exe"
  ["win32-arm64"]="mothx-windows-arm64.exe"
  ["freebsd-x64"]="mothx-freebsd-amd64"
  ["freebsd-arm64"]="mothx-freebsd-arm64"
  ["openbsd-x64"]="mothx-openbsd-amd64"
  ["openbsd-arm64"]="mothx-openbsd-arm64"
  ["netbsd-x64"]="mothx-netbsd-amd64"
)

declare -A OS_MAP=(
  ["linux-x64"]="linux"
  ["linux-arm64"]="linux"
  ["linux-loong64"]="linux"
  ["linux-ppc64le"]="linux"
  ["linux-s390x"]="linux"
  ["linux-riscv64"]="linux"
  ["linux-musl-x64"]="linux"
  ["linux-musl-arm64"]="linux"
  ["darwin-x64"]="darwin"
  ["darwin-arm64"]="darwin"
  ["win32-x64"]="win32"
  ["win32-arm64"]="win32"
  ["freebsd-x64"]="freebsd"
  ["freebsd-arm64"]="freebsd"
  ["openbsd-x64"]="openbsd"
  ["openbsd-arm64"]="openbsd"
  ["netbsd-x64"]="netbsd"
)

declare -A CPU_MAP=(
  ["linux-x64"]="x64"
  ["linux-arm64"]="arm64"
  ["linux-loong64"]="loong64"
  ["linux-ppc64le"]="ppc64"
  ["linux-s390x"]="s390x"
  ["linux-riscv64"]="riscv64"
  ["linux-musl-x64"]="x64"
  ["linux-musl-arm64"]="arm64"
  ["darwin-x64"]="x64"
  ["darwin-arm64"]="arm64"
  ["win32-x64"]="x64"
  ["win32-arm64"]="arm64"
  ["freebsd-x64"]="x64"
  ["freebsd-arm64"]="arm64"
  ["openbsd-x64"]="x64"
  ["openbsd-arm64"]="arm64"
  ["netbsd-x64"]="x64"
)

BUILT=0
for PLATFORM_KEY in "${!PLATFORMS[@]}"; do
  BINARY_NAME="${PLATFORMS[$PLATFORM_KEY]}"
  OS="${OS_MAP[$PLATFORM_KEY]}"
  CPU="${CPU_MAP[$PLATFORM_KEY]}"
  PKG_NAME="mothx-installer-${PLATFORM_KEY}"
  PKG_DIR="$PACKAGES_DIR/$PKG_NAME"

  # Check binary exists
  if [ ! -f "$BUILD_DIR/$BINARY_NAME" ]; then
    echo "Warning: Binary not found: $BUILD_DIR/$BINARY_NAME, skipping $PKG_NAME"
    continue
  fi

  # Create package directory
  mkdir -p "$PKG_DIR/bin"

  # Determine binary name inside package
  if [ "$OS" = "win32" ]; then
    INNER_BINARY="mothx.exe"
  else
    INNER_BINARY="mothx"
  fi

  # Copy binary
  cp "$BUILD_DIR/$BINARY_NAME" "$PKG_DIR/bin/$INNER_BINARY"
  chmod +x "$PKG_DIR/bin/$INNER_BINARY" 2>/dev/null || true

  # Create package.json
  # For musl packages, set libc="musl" so npm can distinguish from glibc
  # npm >=9.4 supports libc field in package.json for optional dependency resolution
  if echo "$PLATFORM_KEY" | grep -q "musl"; then
    cat > "$PKG_DIR/package.json" <<EOF
{
  "name": "$PKG_NAME",
  "version": "$VERSION",
  "description": "MothX native binary for ${OS}-${CPU} (musl static)",
  "os": ["$OS"],
  "cpu": ["$CPU"],
  "libc": ["musl"],
  "files": ["bin/"],
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "https://github.com/oschina/mothx.git",
    "directory": "npm"
  }
}
EOF
  elif echo "$PLATFORM_KEY" | grep -q "^linux-"; then
    cat > "$PKG_DIR/package.json" <<EOF
{
  "name": "$PKG_NAME",
  "version": "$VERSION",
  "description": "MothX native binary for ${OS}-${CPU}",
  "os": ["$OS"],
  "cpu": ["$CPU"],
  "libc": ["glibc"],
  "files": ["bin/"],
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "https://github.com/oschina/mothx.git",
    "directory": "npm"
  }
}
EOF
  else
    cat > "$PKG_DIR/package.json" <<EOF
{
  "name": "$PKG_NAME",
  "version": "$VERSION",
  "description": "MothX native binary for ${OS}-${CPU}",
  "os": ["$OS"],
  "cpu": ["$CPU"],
  "files": ["bin/"],
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "https://github.com/oschina/mothx.git",
    "directory": "npm"
  }
}
EOF
  fi

  # Calculate size
  SIZE=$(du -sh "$PKG_DIR/bin/$INNER_BINARY" | cut -f1)
  echo "  Built: $PKG_NAME ($OS/$CPU) - $SIZE"
  BUILT=$((BUILT + 1))
done

# Update optionalDependencies versions in main package.json files
echo ""
echo "Updating optionalDependencies versions to $VERSION..."
node -e "
const fs = require('fs');
const path = require('path');
const packageRoot = '$PACKAGES_DIR';
const optionalDependencies = {};
for (const entry of fs.readdirSync(packageRoot)) {
  const pkgPath = path.join(packageRoot, entry, 'package.json');
  if (!fs.existsSync(pkgPath)) continue;
  const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
  optionalDependencies[pkg.name] = '$VERSION';
}
for (const pkgPath of ['$NPM_DIR/package.json', '$MOTHX_NPM_DIR/package.json']) {
  if (!fs.existsSync(pkgPath)) continue;
  const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
  pkg.version = '$VERSION';
  pkg.optionalDependencies = optionalDependencies;
  fs.writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
  console.log('Updated optionalDependencies: ' + pkgPath);
}
"

echo ""
echo "Built $BUILT platform packages in $PACKAGES_DIR"
echo ""
echo "Package sizes:"
for d in "$PACKAGES_DIR"/*/; do
  if [ -d "$d" ]; then
    name=$(basename "$d")
    size=$(du -sh "$d" | cut -f1)
    echo "  $name: $size"
  fi
done

# Compare with old single-package approach
echo ""
OLD_SIZE=$(du -sh "$NPM_DIR/bin" 2>/dev/null | cut -f1 || echo "N/A")
echo "Old single-package binary size: $OLD_SIZE"
echo "New per-platform package size:  ~20MB each"
echo "User download: ~20MB (was ~120MB)"
