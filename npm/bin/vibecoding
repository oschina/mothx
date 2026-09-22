#!/usr/bin/env node

// Wrapper script that resolves and executes the platform-specific binary.
// When installed via `npm i -g mothx-installer`, this script finds the
// correct binary from the platform-specific optional dependency package.

const { execFileSync } = require('child_process');
const path = require('path');
const fs = require('fs');

// Map npm os/cpu to package name
const PLATFORM_MAP = {
  'linux-x64-glibc': 'mothx-installer-linux-x64',
  'linux-arm64-glibc': 'mothx-installer-linux-arm64',
  'linux-loong64-glibc': 'mothx-installer-linux-loong64',
  'linux-ppc64-glibc': 'mothx-installer-linux-ppc64le',
  'linux-s390x-glibc': 'mothx-installer-linux-s390x',
  'linux-riscv64-glibc': 'mothx-installer-linux-riscv64',
  'linux-x64-musl': 'mothx-installer-linux-musl-x64',
  'linux-arm64-musl': 'mothx-installer-linux-musl-arm64',
  'darwin-x64': 'mothx-installer-darwin-x64',
  'darwin-arm64': 'mothx-installer-darwin-arm64',
  'win32-x64': 'mothx-installer-win32-x64',
  'win32-arm64': 'mothx-installer-win32-arm64',
  'freebsd-x64': 'mothx-installer-freebsd-x64',
  'freebsd-arm64': 'mothx-installer-freebsd-arm64',
  'openbsd-x64': 'mothx-installer-openbsd-x64',
  'openbsd-arm64': 'mothx-installer-openbsd-arm64',
  'netbsd-x64': 'mothx-installer-netbsd-x64',
};

function mainPackageName() {
  try {
    const pkgPath = path.join(__dirname, '..', 'package.json');
    return JSON.parse(fs.readFileSync(pkgPath, 'utf8')).name || '';
  } catch {
    return '';
  }
}

function maybeWarnLegacyPackage() {
  const packageName = mainPackageName();
  const invoked = path.basename(process.argv[1] || '');
  if (packageName !== 'vibecoding-installer' && invoked !== 'vibecoding') {
    return;
  }
  if (process.env.MOTHX_HIDE_LEGACY_NPM_WARNING || process.env.VIBECODING_HIDE_LEGACY_NPM_WARNING) {
    return;
  }
  console.error('[mothx] vibecoding-installer is a compatibility package for this release.');
  console.error('[mothx] Please update with: npm install -g mothx-installer@latest');
  console.error('');
}

function detectPlatform() {
  const os = process.platform;   // 'linux', 'darwin', 'win32'
  const arch = process.arch;     // 'x64', 'arm64'

  if (os === 'linux') {
    // Detect libc: musl or glibc
    const isMusl = (() => {
      try {
        // Check for Alpine's musl
        if (fs.existsSync('/etc/alpine-release')) return true;
        // Check ldd output for musl
        const { execSync } = require('child_process');
        const output = execSync('ldd --version 2>&1 || true', { encoding: 'utf8' });
        return output.includes('musl');
      } catch {
        return false;
      }
    })();

    return `${os}-${arch}-${isMusl ? 'musl' : 'glibc'}`;
  }

  return `${os}-${arch}`;
}

function findBinary() {
  const platform = detectPlatform();
  const packageName = PLATFORM_MAP[platform];

  if (!packageName) {
    console.error(`Unsupported platform: ${platform}`);
    console.error(`Supported platforms: ${Object.keys(PLATFORM_MAP).join(', ')}`);
    process.exit(1);
  }

  const searchDirs = [];
  const addSearchDir = (dir) => {
    if (dir && !searchDirs.includes(dir)) {
      searchDirs.push(dir);
    }
  };

  try {
    addSearchDir(path.dirname(require.resolve(`${packageName}/package.json`)));
  } catch {
    // Keep explicit fallbacks below for unusual npm layouts.
  }

  // npm usually installs dependencies under this package. Some global installs
  // or package managers may hoist them as siblings, so check both layouts.
  addSearchDir(path.join(__dirname, '..', 'node_modules', packageName));
  addSearchDir(path.join(__dirname, '..', '..', packageName));

  for (const pkgDir of searchDirs) {
    const binName = process.platform === 'win32' ? 'mothx.exe' : 'mothx';
    const binPath = path.join(pkgDir, 'bin', binName);

    if (fs.existsSync(binPath)) {
      return binPath;
    }
  }

  // Fallback: check if there's a binary directly in the main package's bin/
  // (old single-package layout, or development mode)
  const fallbackBinName = (() => {
    const suffix = process.platform === 'win32' ? '.exe' : '';
    const osMap = {
      linux: 'linux',
      darwin: 'darwin',
      win32: 'windows',
      freebsd: 'freebsd',
      openbsd: 'openbsd',
      netbsd: 'netbsd',
    };
    const archMap = {
      x64: 'amd64',
      arm64: 'arm64',
      loong64: 'loong64',
      ppc64: 'ppc64le',
      s390x: 's390x',
      riscv64: 'riscv64',
    };
    if (!osMap[process.platform] || !archMap[process.arch]) {
      return '';
    }
    return `mothx-${osMap[process.platform]}-${archMap[process.arch]}${suffix}`;
  })();

  const fallbackPath = fallbackBinName ? path.join(__dirname, fallbackBinName) : '';
  if (fallbackPath && fs.existsSync(fallbackPath)) {
    return fallbackPath;
  }

  console.error(`Could not find mothx binary for platform: ${detectPlatform()}`);
  console.error(`Searched for package: ${packageName}`);
  console.error(`Searched in: ${searchDirs.join(', ')}`);
  console.error('');
  console.error('If you installed globally, try reinstalling:');
  console.error('  npm install -g mothx-installer');
  console.error('');
  console.error('If the problem persists, install via one-line script instead:');
  console.error('  curl -fsSL https://raw.githubusercontent.com/oschina/mothx/main/install.sh | bash');
  process.exit(1);
}

// Main
maybeWarnLegacyPackage();
const binaryPath = findBinary();
const args = process.argv.slice(2);

try {
  execFileSync(binaryPath, args, { stdio: 'inherit' });
} catch (err) {
  // Forward the exit code
  if (err.status !== undefined) {
    process.exit(err.status);
  }
  if (err.code) {
    process.exit(1);
  }
  process.exit(1);
}
