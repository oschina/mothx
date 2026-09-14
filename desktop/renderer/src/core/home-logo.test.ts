import assert from 'node:assert/strict';
import test from 'node:test';

import { getHomeLogoDisplay } from './home-logo.ts';

test('getHomeLogoDisplay hides the logo when visibility is off', () => {
  const result = getHomeLogoDisplay(false, '/tmp/logo.png', 'data:image/png;base64,abc');
  assert.equal(result.hidden, true);
  assert.equal(result.custom, false);
  assert.equal(result.src, null);
});

test('getHomeLogoDisplay uses the built-in logo by default', () => {
  const result = getHomeLogoDisplay(true, '', null);
  assert.equal(result.hidden, false);
  assert.equal(result.custom, false);
  assert.equal(result.src, null);
});

test('getHomeLogoDisplay uses a custom logo when its data URL is available', () => {
  const dataUrl = 'data:image/png;base64,abc';
  const result = getHomeLogoDisplay(true, '/tmp/logo.png', dataUrl);
  assert.equal(result.hidden, false);
  assert.equal(result.custom, true);
  assert.equal(result.src, dataUrl);
});

test('getHomeLogoDisplay falls back to the built-in logo when the custom file is unavailable', () => {
  const result = getHomeLogoDisplay(true, '/tmp/logo.png', null);
  assert.equal(result.hidden, false);
  assert.equal(result.custom, false);
  assert.equal(result.src, null);
});
