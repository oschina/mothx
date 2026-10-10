import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { createStoreSetHandler, DesktopStore } from './store.ts';

function tempStore() {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-desktop-store-'));
  const store = new DesktopStore(dir);
  return { store, dir };
}

test('store-set handler rejects non-empty home image paths from arbitrary renderer patches', () => {
  const { store, dir } = tempStore();
  try {
    const handler = createStoreSetHandler(store);

    // Pre-seed a chooser-authorized path.
    store.set({ homeBackgroundImage: '/tmp/chooser-background.png', homeLogoImage: '/tmp/chooser-logo.png' });
    assert.equal(store.get().homeBackgroundImage, '/tmp/chooser-background.png');
    assert.equal(store.get().homeLogoImage, '/tmp/chooser-logo.png');

    // Arbitrary store-set must not be able to point the background at a
    // different file, even one disguised as a data URL.
    handler({ homeBackgroundImage: '/etc/passwd' });
    assert.equal(store.get().homeBackgroundImage, '/tmp/chooser-background.png');

    handler({ homeBackgroundImage: 'data:image/png;base64,SGVsbG8=' });
    assert.equal(store.get().homeBackgroundImage, '/tmp/chooser-background.png');

    handler({ homeLogoImage: '/etc/shadow' });
    assert.equal(store.get().homeLogoImage, '/tmp/chooser-logo.png');

    handler({ homeLogoImage: 'data:image/webp;base64,UklGRg==' });
    assert.equal(store.get().homeLogoImage, '/tmp/chooser-logo.png');

    // Other fields should still apply.
    handler({ theme: 'dark' });
    assert.equal(store.get().theme, 'dark');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store-set handler still allows clearing image paths with an empty string', () => {
  const { store, dir } = tempStore();
  try {
    const handler = createStoreSetHandler(store);
    store.set({ homeBackgroundImage: '/tmp/chooser-background.png', homeLogoImage: '/tmp/chooser-logo.png' });

    handler({ homeBackgroundImage: '' });
    assert.equal(store.get().homeBackgroundImage, '');

    handler({ homeLogoImage: '' });
    assert.equal(store.get().homeLogoImage, '');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('store-set handler ignores invalid patches', () => {
  const { store, dir } = tempStore();
  try {
    const handler = createStoreSetHandler(store);
    const previous = store.get();
    handler(null as never);
    handler('not an object' as never);
    handler([1, 2, 3] as never);
    assert.deepEqual(store.get(), previous);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
