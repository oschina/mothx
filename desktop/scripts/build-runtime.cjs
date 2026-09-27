const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const { resolveVersion } = require('./resolve-version.cjs');

const desktopRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(desktopRoot, '..');
const uiRoot = path.join(repoRoot, 'ui');

function option(name) {
  const index = process.argv.indexOf(`--${name}`);
  return index >= 0 ? process.argv[index + 1] : undefined;
}

function run(command, args, cwd, env = process.env) {
  const result = spawnSync(command, args, { cwd, env, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed with status ${result.status}`);
}

function npmCommand() {
  if (process.env.npm_execpath) {
    return { command: process.execPath, prefix: [process.env.npm_execpath] };
  }
  return { command: process.platform === 'win32' ? 'npm.exe' : 'npm', prefix: [] };
}

// ui/dist is embedded into the Go binary (ui/embed.go). The desktop frontend
// is standalone (desktop/renderer) and does not use the serve Web UI, but the
// vendored binary still needs ui/dist to exist at build time. Only build the
// UI when the embed directory is missing.
function ensureUI() {
  if (fs.existsSync(path.join(uiRoot, 'dist', 'index.html'))) return;
  const npm = npmCommand();
  if (!fs.existsSync(path.join(uiRoot, 'node_modules'))) {
    run(npm.command, [...npm.prefix, 'ci', '--no-audit', '--no-fund'], uiRoot);
  }
  run(npm.command, [...npm.prefix, 'run', 'build'], uiRoot);
}

function normalizeGoarch(arch) {
  const value = arch === 'x64' ? 'amd64' : arch;
  if (!['amd64', 'arm64'].includes(value)) {
    throw new Error(`Unsupported runtime architecture: ${arch}`);
  }
  return value;
}

function target() {
  const platform = option('platform') || process.platform;
  const goos = platform === 'win32' || platform === 'win' ? 'windows' : platform === 'mac' ? 'darwin' : platform;
  if (!['darwin', 'linux', 'windows'].includes(goos)) {
    throw new Error(`Unsupported runtime target: ${goos}`);
  }
  return { goos, binaryName: goos === 'windows' ? 'mothx.exe' : 'mothx' };
}

// Keep in sync with the target matrix in electron-builder.yml: macOS packages
// arm64 and x64, Windows and Linux package x64 only. Without an explicit
// --arch this script builds one vendored runtime per packaged architecture so
// every release artifact ships a runnable `mothx` binary (macOS x64 used to
// receive the host's arm64 build). `--arch <x64|arm64|amd64>` builds one.
const RELEASE_ARCHES = { darwin: ['arm64', 'amd64'], windows: ['amd64'], linux: ['amd64'] };

function archesFor(goos) {
  const requested = option('arch');
  if (requested) return [normalizeGoarch(requested)];
  // A flat `--output` destination can only hold one binary.
  if (option('output')) return [normalizeGoarch(process.arch)];
  return RELEASE_ARCHES[goos];
}

// Per-architecture output keeps several vendored runtimes side by side;
// `--output <dir>` keeps the legacy flat single-architecture layout.
const outputRoot = option('output');
const { goos, binaryName } = target();
const goarches = archesFor(goos);

ensureUI();
const version = resolveVersion({ repoRoot });
for (const goarch of goarches) {
  // <goos>-<goarch> keeps cross-built runtimes from overwriting each other.
  const outputDir = outputRoot || path.join(desktopRoot, 'vendor', 'mothx', 'bin', `${goos}-${goarch}`);
  fs.mkdirSync(outputDir, { recursive: true });
  const output = path.join(outputDir, binaryName);
  console.log(`Building MothX runtime from current source for ${goos}/${goarch}...`);
  run('go', [
    'build', '-trimpath',
    '-ldflags', `-s -w -X main.version=${version} -X github.com/oschina/mothx/internal/version.Version=${version} -X github.com/oschina/mothx/internal/ua.Version=${version}`,
    '-o', output,
    './cmd/mothx',
  ], repoRoot, { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch });
  if (goos !== 'windows') fs.chmodSync(output, 0o755);
  console.log(`Built MothX runtime at ${output}`);
}
