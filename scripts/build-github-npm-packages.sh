#!/bin/bash
set -euo pipefail

# Build scoped npm packages for GitHub Packages. This is intentionally separate
# from the npmjs.org package layout under npm/.

SCOPE="${GITHUB_NPM_SCOPE:-}"
if [[ ! "$SCOPE" =~ ^@[a-z0-9][a-z0-9-]*$ ]]; then
  echo "GITHUB_NPM_SCOPE must be a GitHub Packages scope such as @oschina" >&2
  exit 1
fi

VERSION="${VERSION:-$(git describe --tags --always)}"
VERSION="${VERSION#v}"
VERSION="${VERSION%-dirty}"
VERSION="${VERSION%%-[0-9]*-g[0-9a-f]*}"

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="$PROJECT_ROOT/dist/github-npm"
PACKAGES_DIR="$OUTPUT_DIR/packages"
MAIN_PACKAGE="$SCOPE/mothx"

rm -rf "$OUTPUT_DIR"
mkdir -p "$PACKAGES_DIR" "$OUTPUT_DIR/mothx/bin" "$OUTPUT_DIR/mothx/scripts"

node - "$PROJECT_ROOT/scripts/npm-installer-wrapper.js" "$OUTPUT_DIR/mothx/bin/mothx" "$MAIN_PACKAGE" <<'NODE'
const fs = require('fs');
const [source, destination, packageName] = process.argv.slice(2);
const wrapper = fs.readFileSync(source, 'utf8')
  .replaceAll('mothx-installer-', `${packageName}-`)
  .replaceAll('mothx-installer', packageName);
fs.writeFileSync(destination, wrapper);
NODE

cp "$PROJECT_ROOT/npm/mothx/scripts/postinstall.js" "$OUTPUT_DIR/mothx/scripts/postinstall.js"
chmod +x "$OUTPUT_DIR/mothx/bin/mothx"

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
  ["linux-x64"]="linux" ["linux-arm64"]="linux" ["linux-loong64"]="linux"
  ["linux-ppc64le"]="linux" ["linux-s390x"]="linux" ["linux-riscv64"]="linux"
  ["linux-musl-x64"]="linux" ["linux-musl-arm64"]="linux"
  ["darwin-x64"]="darwin" ["darwin-arm64"]="darwin"
  ["win32-x64"]="win32" ["win32-arm64"]="win32"
  ["freebsd-x64"]="freebsd" ["freebsd-arm64"]="freebsd"
  ["openbsd-x64"]="openbsd" ["openbsd-arm64"]="openbsd" ["netbsd-x64"]="netbsd"
)

declare -A CPU_MAP=(
  ["linux-x64"]="x64" ["linux-arm64"]="arm64" ["linux-loong64"]="loong64"
  ["linux-ppc64le"]="ppc64" ["linux-s390x"]="s390x" ["linux-riscv64"]="riscv64"
  ["linux-musl-x64"]="x64" ["linux-musl-arm64"]="arm64"
  ["darwin-x64"]="x64" ["darwin-arm64"]="arm64"
  ["win32-x64"]="x64" ["win32-arm64"]="arm64"
  ["freebsd-x64"]="x64" ["freebsd-arm64"]="arm64"
  ["openbsd-x64"]="x64" ["openbsd-arm64"]="arm64" ["netbsd-x64"]="x64"
)

for platform in "${!PLATFORMS[@]}"; do
  binary="$PROJECT_ROOT/bin/${PLATFORMS[$platform]}"
  if [[ ! -f "$binary" ]]; then
    echo "Missing binary: $binary" >&2
    exit 1
  fi

  package_dir="$PACKAGES_DIR/mothx-$platform"
  mkdir -p "$package_dir/bin"
  binary_name="mothx"
  if [[ "${OS_MAP[$platform]}" == "win32" ]]; then
    binary_name="mothx.exe"
  fi
  cp "$binary" "$package_dir/bin/$binary_name"
  chmod +x "$package_dir/bin/$binary_name" 2>/dev/null || true

  libc=""
  if [[ "$platform" == linux-musl-* ]]; then
    libc=$'\n  ,"libc": ["musl"]'
  elif [[ "$platform" == linux-* ]]; then
    libc=$'\n  ,"libc": ["glibc"]'
  fi

  cat > "$package_dir/package.json" <<EOF
{
  "name": "$MAIN_PACKAGE-$platform",
  "version": "$VERSION",
  "description": "MothX native binary for ${OS_MAP[$platform]}-${CPU_MAP[$platform]}",
  "os": ["${OS_MAP[$platform]}"],
  "cpu": ["${CPU_MAP[$platform]}"]${libc},
  "files": ["bin/"],
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "git+https://github.com/${GITHUB_REPOSITORY:-oschina/mothx}.git"
  }
}
EOF
done

node - "$OUTPUT_DIR/mothx/package.json" "$MAIN_PACKAGE" "$VERSION" "$PACKAGES_DIR" <<'NODE'
const fs = require('fs');
const path = require('path');
const [packagePath, name, version, packagesDir] = process.argv.slice(2);
const optionalDependencies = {};
for (const entry of fs.readdirSync(packagesDir)) {
  const pkg = JSON.parse(fs.readFileSync(path.join(packagesDir, entry, 'package.json'), 'utf8'));
  optionalDependencies[pkg.name] = version;
}
fs.writeFileSync(packagePath, `${JSON.stringify({
  name,
  version,
  description: 'AI coding assistant for the terminal',
  bin: { mothx: 'bin/mothx' },
  scripts: { postinstall: 'node scripts/postinstall.js' },
  files: ['bin/', 'scripts/'],
  optionalDependencies,
  license: 'MIT',
  repository: {
    type: 'git',
    url: `git+https://github.com/${process.env.GITHUB_REPOSITORY || 'oschina/mothx'}.git`,
  },
  engines: { node: '>=14' },
}, null, 2)}\n`);
NODE

echo "Built GitHub Packages npm artifacts in $OUTPUT_DIR"
