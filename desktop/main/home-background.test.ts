import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { MAX_HOME_IMAGE_BYTES, readHomeImageDataURL } from './home-image.ts';

test('home background data URL is available only for the configured selected image', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-home-background-'));
  try {
    const selected = join(dir, 'wallpaper #1.png');
    const other = join(dir, 'other.png');
    writeFileSync(selected, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
    writeFileSync(other, Buffer.from([0x00]));

    assert.deepEqual(readHomeImageDataURL(selected, selected), { ok: true, dataUrl: 'data:image/png;base64,iVBORw==' });
    assert.deepEqual(readHomeImageDataURL(other, selected), { ok: false, reason: 'unauthorized' }, 'renderer may not read an arbitrary image path');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('the same restricted image resolver supports a separately configured home logo', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-home-logo-'));
  try {
    const logo = join(dir, 'logo.webp');
    const background = join(dir, 'background.webp');
    writeFileSync(logo, Buffer.from([0x52, 0x49, 0x46, 0x46]));
    writeFileSync(background, Buffer.from([0x52, 0x49, 0x46, 0x46]));
    assert.deepEqual(readHomeImageDataURL(logo, logo), { ok: true, dataUrl: 'data:image/webp;base64,UklGRg==' });
    assert.deepEqual(readHomeImageDataURL(background, logo), { ok: false, reason: 'unauthorized' });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('home background data URL rejects unsupported, missing, and oversized files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-home-background-'));
  try {
    const unsupported = join(dir, 'wallpaper.txt');
    const large = join(dir, 'large.webp');
    writeFileSync(unsupported, 'not an image');
    writeFileSync(large, Buffer.alloc(MAX_HOME_IMAGE_BYTES + 1));

    assert.deepEqual(readHomeImageDataURL(unsupported, unsupported), { ok: false, reason: 'unsupported' });
    assert.deepEqual(readHomeImageDataURL(large, large), { ok: false, reason: 'oversized' });
    assert.deepEqual(readHomeImageDataURL(join(dir, 'missing.png'), join(dir, 'missing.png')), { ok: false, reason: 'missing' });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
