const fs = require('node:fs');
const path = require('node:path');
const { Arch } = require('builder-util');

const { binaryArch } = require('./binary-arch.cjs');

const desktopRoot = path.resolve(__dirname, '..');

// electron-builder's Arch enum is resolved by name (not by a hard-coded
// number) so a dependency update cannot silently change the mapping.
function goarchForElectronArch(arch) {
  const name = Arch[arch];
  if (name === 'x64') return 'amd64';
  if (name === 'arm64') return 'arm64';
  throw new Error(`Unsupported desktop architecture for bundled CLI: ${name || arch}`);
}

function binaryNameFor(electronPlatformName) {
  return electronPlatformName === 'win32' ? 'mothx.exe' : 'mothx';
}

// The vendored runtime is injected after packing (instead of being shipped
// through the `files` patterns) so that:
//   - exactly one copy is packaged, at the path `main/runtime-binary.ts`
//     probes first (`<resources>/app/vendor/mothx/bin/...`),
//   - the copy always matches the packed app's CPU architecture even when one
//     `build:runtime` run produces several architectures (macOS arm64 + x64),
//   - on macOS the binary lives inside `MothX.app`, where the code signing
//     pass seals it together with the rest of the bundle.
function resolveSource(goos, goarch, binaryName) {
  const candidates = [
    // Per-target output of `npm run build:runtime`.
    path.join(desktopRoot, 'vendor', 'mothx', 'bin', `${goos}-${goarch}`, binaryName),
    path.join(desktopRoot, 'vendor', 'mothx', 'bin', goarch, binaryName),
    // Legacy single-architecture layout.
    path.join(desktopRoot, 'vendor', 'mothx', 'bin', binaryName),
    path.join(desktopRoot, 'vendor', 'mothx', binaryName),
  ];
  const source = candidates.find((candidate) => fs.existsSync(candidate));
  if (!source) {
    throw new Error(
      `Source-built MothX CLI not found for ${goarch}. Checked:\n${candidates.join('\n')}\n` +
        'Run `npm run build:runtime` (it builds every packaged architecture) before packaging.'
    );
  }
  return source;
}

module.exports = async function afterPack(packContext) {
  const { appOutDir, electronPlatformName, packager } = packContext;
  const goos = electronPlatformName === 'win32' ? 'windows' : electronPlatformName;
  const goarch = goarchForElectronArch(packContext.arch);
  const binaryName = binaryNameFor(electronPlatformName);
  const source = resolveSource(goos, goarch, binaryName);

  const resourcesDir = packager.getResourcesDir(appOutDir);
  if (!fs.existsSync(path.join(resourcesDir, 'app'))) {
    throw new Error(
      `Packaged app directory not found at ${path.join(resourcesDir, 'app')}. ` +
        'Bundled CLI injection requires asar: false (see electron-builder.yml).'
    );
  }
  const binary = path.join(resourcesDir, 'app', 'vendor', 'mothx', 'bin', binaryName);

  const detected = binaryArch(source);
  if (detected !== goarch) {
    throw new Error(
      `Bundled MothX CLI arch mismatch: ${source} is ${detected || 'unrecognized'} but the app is ${goarch}. ` +
        `Rebuild the runtime with the matching --arch (npm run build:runtime -- --platform ${electronPlatformName} --arch ${
          goarch === 'amd64' ? 'x64' : goarch
        }).`
    );
  }

  fs.mkdirSync(path.dirname(binary), { recursive: true });
  fs.copyFileSync(source, binary);
  if (electronPlatformName !== 'win32') fs.chmodSync(binary, 0o755);
  console.log(`Bundled source-built MothX CLI at ${binary} (${detected})`);
};
