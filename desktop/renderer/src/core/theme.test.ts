import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const theme = await readFile(new URL('./theme.ts', import.meta.url), 'utf8');
const app = await readFile(new URL('../App.tsx', import.meta.url), 'utf8');
const homeView = await readFile(new URL('../views/HomeView.tsx', import.meta.url), 'utf8');
const appearance = await readFile(new URL('../views/settings/AppearancePanel.tsx', import.meta.url), 'utf8');

test('background images use the restricted Desktop bridge instead of file URLs', () => {
  assert.match(theme, /desktop\.homeBackgroundDataURL\(path\)/);
  assert.doesNotMatch(theme, /file:\/\//, 'packaged renderers must not load the selected image through file://');
});

test('app background removes the opaque bg-background utility so the image is visible', () => {
  assert.match(app, /background\.app \? 'app-surface-veil' : 'bg-background'/, 'app-scope background must not be covered by an opaque bg-background layer');
  assert.doesNotMatch(app, /bg-background.*app-surface-veil/, 'bg-background and app-surface-veil must not both be applied at the same time');
});

test('background image errors are surfaced as bilingual toast strings instead of failing silently', () => {
  assert.match(theme, /settings\.homeBackgroundUnsupported/);
  assert.match(theme, /settings\.homeBackgroundMissing/);
  assert.match(theme, /settings\.homeBackgroundOversized/);
  assert.match(theme, /settings\.homeBackgroundUnauthorized/);
});

test('Home logo settings project visibility and custom image selection through the Desktop bridge', () => {
  assert.match(homeView, /useHomeLogo\(\)/);
  assert.match(homeView, /<HomeLogo logo=\{logo\}/);
  assert.match(homeView, /!logo\.hidden/);
  assert.match(appearance, /settings\.homeLogo/);
  assert.match(appearance, /desktop\.chooseHomeLogo/);
});
