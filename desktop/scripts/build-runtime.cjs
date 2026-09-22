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

function target() {
  const platform = option('platform') || process.platform;
  const arch = option('arch') || process.arch;
  const goos = platform === 'win32' || platform === 'win' ? 'windows' : platform === 'mac' ? 'darwin' : platform;
  const goarch = arch === 'x64' ? 'amd64' : arch;
  if (!['darwin', 'linux', 'windows'].includes(goos) || !['amd64', 'arm64'].includes(goarch)) {
    throw new Error(`Unsupported runtime target: ${goos}/${goarch}`);
  }
  return { goos, goarch, binaryName: goos === 'windows' ? 'mothx.exe' : 'mothx' };
}

const outputRoot = option('output') || path.join(desktopRoot, 'vendor', 'mothx', 'bin');
const { goos, goarch, binaryName } = target();

ensureUI();
fs.mkdirSync(outputRoot, { recursive: true });
const output = path.join(outputRoot, binaryName);
const version = resolveVersion({ repoRoot });
console.log(`Building MothX runtime from current source for ${goos}/${goarch}...`);
run('go', [
  'build', '-trimpath',
  '-ldflags', `-s -w -X main.version=${version} -X github.com/oschina/mothx/internal/version.Version=${version} -X github.com/oschina/mothx/internal/ua.Version=${version}`,
  '-o', output,
  './cmd/mothx',
], repoRoot, { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch });
if (goos !== 'windows') fs.chmodSync(output, 0o755);
console.log(`Built MothX runtime at ${output}`);
