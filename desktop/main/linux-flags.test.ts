import assert from 'node:assert/strict';
import test from 'node:test';

import { shouldDisableDevShm, shouldDisableGpu } from './linux-flags.ts';

const GB = 1024 * 1024 * 1024;

test('packaged Linux keeps GPU compositing when a display server exists', () => {
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: { DISPLAY: ':0' } }), false);
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: { WAYLAND_DISPLAY: 'wayland-0' } }), false);
});

test('headless packaged Linux keeps the GPU fallback', () => {
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: {} }), true);
});

test('GPU policy is limited to packaged Linux and honours an explicit override', () => {
  assert.equal(shouldDisableGpu({ platform: 'darwin', packaged: true, env: {} }), false);
  assert.equal(shouldDisableGpu({ platform: 'win32', packaged: true, env: {} }), false);
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: false, env: {} }), false);

  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: { DISPLAY: ':0', MOTHX_DESKTOP_DISABLE_GPU: '1' } }), true);
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: { MOTHX_DESKTOP_DISABLE_GPU: 'off' } }), false);
  assert.equal(shouldDisableGpu({ platform: 'linux', packaged: true, env: { MOTHX_DESKTOP_DISABLE_GPU: 'garbage', DISPLAY: ':0' } }), false);
});

test('dev-shm fallback only applies to missing, unwritable, or tiny mounts', () => {
  const big = () => ({ bsize: 4096, blocks: 4 * GB / 4096 });
  const tiny = () => ({ bsize: 4096, blocks: 1024 });
  const yes = () => true;
  const no = () => false;

  assert.equal(shouldDisableDevShm(big, yes), false, 'a normal writable /dev/shm keeps the fast path');
  assert.equal(shouldDisableDevShm(tiny, yes), true, 'a small /dev/shm keeps the /tmp fallback');
  assert.equal(shouldDisableDevShm(big, no), true, 'an unwritable /dev/shm keeps the /tmp fallback');
  assert.equal(
    shouldDisableDevShm(() => {
      throw new Error('missing');
    }, yes),
    true,
    'a missing /dev/shm keeps the /tmp fallback',
  );
});
