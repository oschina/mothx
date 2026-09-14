import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { MAX_HOME_IMAGE_BYTES, readHomeImageDataURL } from './home-image.ts';

test('home logo data URL is available only for the configured selected image', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-home-logo-'));
  try {
    const selected = join(dir, 'logo #1.png');
    const other = join(dir, 'other.png');
    writeFileSync(selected, Buffer.from([0x89, 0x50, 0x4e, 0x47]));
    writeFileSync(other, Buffer.from([0x00]));

    assert.deepEqual(readHomeImageDataURL(selected, selected), { ok: true, dataUrl: 'data:image/png;base64,iVBORw==' });
    assert.deepEqual(readHomeImageDataURL(other, selected), { ok: false, reason: 'unauthorized' }, 'renderer may not read an arbitrary image path');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('home logo data URL rejects unsupported, missing, and oversized files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-home-logo-'));
  try {
    const unsupported = join(dir, 'logo.txt');
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
