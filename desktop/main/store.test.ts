import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { DesktopStore } from './store.ts';

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
