import assert from 'node:assert/strict';
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import {
  bundledCandidates,
  describeRuntimeBinary,
  isRunnableBinary,
  resolveRuntimeBinary,
  runtimeBinaryName,
  type RuntimeBinaryLookup,
} from './runtime-binary.ts';

function makeExecutable(file: string): void {
  mkdirSync(join(file, '..'), { recursive: true });
  writeFileSync(file, '#!/bin/sh\n');
  chmodSync(file, 0o755);
}

function lookup(root: string, overrides: Partial<RuntimeBinaryLookup> = {}): RuntimeBinaryLookup {
  return {
    platform: 'linux',
    arch: 'x64',
    env: {},
    resourcesPath: join(root, 'resources'),
    distDir: join(root, 'desktop', 'dist'),
    settings: { source: 'bundled', path: '' },
    ...overrides,
  };
}

test('resolves the packaged bundled runtime first', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const packaged = join(root, 'resources', 'app', 'vendor', 'mothx', 'bin', 'mothx');
    makeExecutable(packaged);
    makeExecutable(join(root, 'desktop', 'vendor', 'mothx', 'bin', 'linux-amd64', 'mothx'));

    const resolved = resolveRuntimeBinary(lookup(root));
    assert.equal(resolved.path, packaged);
    assert.equal(resolved.origin, 'bundled');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('resolves the per-target development layout first', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const dev = join(root, 'desktop', 'vendor', 'mothx', 'bin', 'linux-arm64', 'mothx');
    makeExecutable(dev);
    makeExecutable(join(root, 'desktop', 'vendor', 'mothx', 'bin', 'arm64', 'mothx'));

    const resolved = resolveRuntimeBinary(lookup(root, { arch: 'arm64' }));
    assert.equal(resolved.path, dev);
    assert.equal(resolved.origin, 'bundled');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('falls back to legacy vendored runtime layouts', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const legacy = join(root, 'desktop', 'vendor', 'mothx', 'bin', 'mothx');
    makeExecutable(legacy);

    assert.equal(resolveRuntimeBinary(lookup(root)).path, legacy);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('a custom binary wins over the bundled one', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    makeExecutable(join(root, 'resources', 'app', 'vendor', 'mothx', 'bin', 'mothx'));
    const custom = join(root, 'custom', 'mothx');
    makeExecutable(custom);

    const resolved = resolveRuntimeBinary(lookup(root, { settings: { source: 'custom', path: custom } }));
    assert.equal(resolved.path, custom);
    assert.equal(resolved.origin, 'custom');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('an unavailable custom binary falls back to the bundled runtime', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const packaged = join(root, 'resources', 'app', 'vendor', 'mothx', 'bin', 'mothx');
    makeExecutable(packaged);

    const input = lookup(root, { settings: { source: 'custom', path: join(root, 'missing', 'mothx') } });
    const resolved = resolveRuntimeBinary(input);
    assert.equal(resolved.path, packaged);
    assert.equal(resolved.origin, 'bundled');

    const status = describeRuntimeBinary(input);
    assert.equal(status.source, 'custom');
    assert.equal(status.effectiveOrigin, 'bundled');
    assert.equal(status.fallback, 'custom');
    assert.equal(status.bundledPath, packaged);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('the MOTHX_BINARY development override wins over user settings', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    makeExecutable(join(root, 'resources', 'app', 'vendor', 'mothx', 'bin', 'mothx'));
    const custom = join(root, 'custom', 'mothx');
    makeExecutable(custom);
    const fromEnv = join(root, 'env', 'mothx');
    makeExecutable(fromEnv);

    const input = lookup(root, {
      env: { MOTHX_BINARY: fromEnv },
      settings: { source: 'custom', path: custom },
    });
    const resolved = resolveRuntimeBinary(input);
    assert.equal(resolved.path, fromEnv);
    assert.equal(resolved.origin, 'env');

    const status = describeRuntimeBinary(input);
    assert.equal(status.effectiveOrigin, 'env');
    assert.equal(status.fallback, '');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('a missing runtime reports every checked path', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const input = lookup(root);
    assert.throws(() => resolveRuntimeBinary(input), /MothX runtime not found/);

    const status = describeRuntimeBinary(input);
    assert.equal(status.effectiveOrigin, 'none');
    assert.equal(status.effectivePath, '');
    assert.ok(status.bundledPath === '');
    assert.ok(bundledCandidates(input).length > 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('windows runtimes are named mothx.exe and need no exec bit', () => {
  const root = mkdtempSync(join(tmpdir(), 'mothx-runtime-'));
  try {
    const packaged = join(root, 'resources', 'app', 'vendor', 'mothx', 'bin', 'mothx.exe');
    mkdirSync(join(packaged, '..'), { recursive: true });
    writeFileSync(packaged, 'MZ');

    const input = lookup(root, { platform: 'win32' });
    assert.equal(runtimeBinaryName('win32'), 'mothx.exe');
    assert.equal(isRunnableBinary(packaged, 'win32'), true);
    assert.equal(resolveRuntimeBinary(input).path, packaged);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
