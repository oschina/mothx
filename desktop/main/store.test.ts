import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { DesktopStore, restrictRendererStorePatch } from './store.ts';
import { readHomeImageDataURL } from './home-image.ts';

test('store defaults to visible home logo and no custom image', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const store = new DesktopStore(dir);
    const data = store.get();
    assert.equal(data.homeLogoVisible, true);
    assert.equal(data.homeLogoImage, '');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store persists home logo visibility and custom image across reloads', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const store = new DesktopStore(dir);
    store.set({ homeLogoVisible: false, homeLogoImage: '/tmp/test-logo.png' });

    const reopened = new DesktopStore(dir);
    const data = reopened.get();
    assert.equal(data.homeLogoVisible, false);
    assert.equal(data.homeLogoImage, '/tmp/test-logo.png');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store clamps custom logo path length', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const store = new DesktopStore(dir);
    const longPath = 'a'.repeat(5000);
    store.set({ homeLogoImage: longPath });
    assert.equal(store.get().homeLogoImage.length, 4096);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('generic renderer store patches cannot grant non-empty image paths', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const target = join(dir, 'private.png');
    const store = new DesktopStore(dir);
    store.set(restrictRendererStorePatch({ homeBackgroundImage: target, homeLogoImage: target }));
    assert.equal(store.get().homeBackgroundImage, '');
    assert.equal(store.get().homeLogoImage, '');
    assert.deepEqual(readHomeImageDataURL(target, store.get().homeBackgroundImage), { ok: false, reason: 'unauthorized' });

    // The privileged chooser writes directly to DesktopStore; generic renderer
    // persistence may still clear those user-granted paths.
    store.set({ homeBackgroundImage: target, homeLogoImage: target });
    store.set(restrictRendererStorePatch({ homeBackgroundImage: '', homeLogoImage: '' }));
    assert.equal(store.get().homeBackgroundImage, '');
    assert.equal(store.get().homeLogoImage, '');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store defaults to the bundled runtime binary', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const data = new DesktopStore(dir).get();
    assert.equal(data.runtimeSource, 'bundled');
    assert.equal(data.runtimeBinaryPath, '');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store persists the runtime binary selection across reloads', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const store = new DesktopStore(dir);
    store.set({ runtimeSource: 'custom', runtimeBinaryPath: '/opt/mothx/bin/mothx' });

    const reopened = new DesktopStore(dir).get();
    assert.equal(reopened.runtimeSource, 'custom');
    assert.equal(reopened.runtimeBinaryPath, '/opt/mothx/bin/mothx');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store sanitizes the runtime binary selection', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-store-'));
  try {
    const store = new DesktopStore(dir);
    store.set({ runtimeSource: 'custom', runtimeBinaryPath: 'a'.repeat(5000) });
    assert.equal(store.get().runtimeBinaryPath.length, 4096);

    store.set({ runtimeSource: 'bogus' as unknown as 'bundled' });
    assert.equal(store.get().runtimeSource, 'custom');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
