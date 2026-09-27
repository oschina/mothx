import { accessSync, constants, existsSync, statSync } from 'node:fs';
import { join } from 'node:path';

// One place decides which `mothx` executable the desktop shell spawns as its
// ACP runtime. The bundled binary (injected by scripts/after-pack.cjs at
// <resources>/app/vendor/mothx/bin/) is the default; a user-configured custom
// binary and the MOTHX_BINARY development override are the only alternatives.

export type RuntimeBinarySource = 'bundled' | 'custom';
export type RuntimeBinaryOrigin = 'env' | 'custom' | 'bundled' | 'none';

export interface RuntimeBinarySettings {
  source: RuntimeBinarySource;
  path: string;
}

export interface RuntimeBinaryLookup {
  platform: NodeJS.Platform;
  arch: string;
  env: Record<string, string | undefined>;
  /** Electron `process.resourcesPath` of the running app. */
  resourcesPath: string;
  /** Directory of the bundled main process code (`dist/`). */
  distDir: string;
  settings: RuntimeBinarySettings;
}

export interface ResolvedRuntimeBinary {
  path: string;
  origin: 'env' | 'custom' | 'bundled';
}

export interface RuntimeBinaryStatus {
  source: RuntimeBinarySource;
  configuredPath: string;
  bundledPath: string;
  effectivePath: string;
  effectiveOrigin: RuntimeBinaryOrigin;
  /** Which configured source could not be used and why ('' when none). */
  fallback: '' | 'env' | 'custom';
}

export function runtimeBinaryName(platform: NodeJS.Platform): string {
  return platform === 'win32' ? 'mothx.exe' : 'mothx';
}

function goarchFor(arch: string): string {
  return arch === 'x64' ? 'amd64' : arch;
}

function goosFor(platform: NodeJS.Platform): string {
  return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'darwin' : platform;
}

export function isRunnableBinary(candidate: string, platform: NodeJS.Platform): boolean {
  if (!candidate) return false;
  try {
    if (!statSync(candidate).isFile()) return false;
    if (platform !== 'win32') accessSync(candidate, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

export function bundledCandidates(lookup: RuntimeBinaryLookup): string[] {
  const name = runtimeBinaryName(lookup.platform);
  const goarch = goarchFor(lookup.arch);
  const target = `${goosFor(lookup.platform)}-${goarch}`;
  return [
    // Packaged app: injected by scripts/after-pack.cjs.
    join(lookup.resourcesPath, 'app', 'vendor', 'mothx', 'bin', name),
    // Older packaged layouts (binary shipped beside resources/).
    join(lookup.resourcesPath, '..', 'vendor', 'mothx', 'bin', target, name),
    join(lookup.resourcesPath, '..', 'vendor', 'mothx', 'bin', goarch, name),
    join(lookup.resourcesPath, '..', 'vendor', 'mothx', 'bin', name),
    join(lookup.resourcesPath, '..', 'vendor', 'mothx', name),
    // Development: desktop/vendor/mothx/bin/<goos>-<goarch>.
    join(lookup.distDir, '..', 'vendor', 'mothx', 'bin', target, name),
    join(lookup.distDir, '..', 'vendor', 'mothx', 'bin', goarch, name),
    join(lookup.distDir, '..', 'vendor', 'mothx', 'bin', name),
    join(lookup.distDir, '..', 'vendor', 'mothx', name),
    join(lookup.distDir, '..', '..', 'vendor', 'mothx', 'bin', name),
    // Development fallback: the repository build output.
    join(lookup.distDir, '..', '..', '..', 'bin', name),
  ];
}

function firstBundled(lookup: RuntimeBinaryLookup): string {
  return bundledCandidates(lookup).find((candidate) => isRunnableBinary(candidate, lookup.platform)) || '';
}

export function resolveRuntimeBinary(lookup: RuntimeBinaryLookup): ResolvedRuntimeBinary {
  const { env, settings } = lookup;
  const fromEnv = (env.MOTHX_BINARY || '').trim();
  if (fromEnv && isRunnableBinary(fromEnv, lookup.platform)) return { path: fromEnv, origin: 'env' };

  if (settings.source === 'custom') {
    const custom = settings.path.trim();
    if (isRunnableBinary(custom, lookup.platform)) return { path: custom, origin: 'custom' };
  }

  const bundled = firstBundled(lookup);
  if (bundled) return { path: bundled, origin: 'bundled' };

  throw new Error(`MothX runtime not found. Checked:\n${bundledCandidates(lookup).join('\n')}`);
}

export function describeRuntimeBinary(lookup: RuntimeBinaryLookup): RuntimeBinaryStatus {
  const configuredPath = (lookup.settings.path || '').trim();
  const fromEnv = (lookup.env.MOTHX_BINARY || '').trim();
  const bundledPath = firstBundled(lookup);
  let effectivePath = '';
  let effectiveOrigin: RuntimeBinaryOrigin = 'none';
  try {
    const resolved = resolveRuntimeBinary(lookup);
    effectivePath = resolved.path;
    effectiveOrigin = resolved.origin;
  } catch {
    // Leave effectiveOrigin 'none'; the UI surfaces the missing runtime.
  }
  // The MOTHX_BINARY development override intentionally wins over user
  // settings. Otherwise report which configured source could not be used
  // (a broken custom path falls back to the bundled runtime so the client
  // still starts).
  let fallback: RuntimeBinaryStatus['fallback'] = '';
  if (effectiveOrigin !== 'env') {
    if (lookup.settings.source === 'custom' && effectiveOrigin !== 'custom') fallback = 'custom';
    else if (fromEnv) fallback = 'env';
  }
  return {
    source: lookup.settings.source,
    configuredPath,
    bundledPath,
    effectivePath,
    effectiveOrigin,
    fallback,
  };
}
