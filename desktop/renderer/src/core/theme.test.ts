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

test('large background data URLs become blob object URLs before CSS injection', () => {
  // Chromium drops CSS custom-property values above 2MiB, so injecting the raw
  // base64 data URL into --app-user-image silently loses multi-MB wallpapers.
  assert.match(theme, /dataUrlToObjectUrl\(result\.dataUrl\)/, 'authorized image bytes must be converted before CSS injection');
  assert.match(theme, /URL\.createObjectURL\(new Blob\(\[bytes\]/, 'conversion must produce a short blob: URL');
  assert.match(theme, /URL\.revokeObjectURL\(backgroundImageSource\)/, 'replaced or cleared object URLs must be revoked');
  assert.match(theme, /backgroundImagePath = path;\s+setBackgroundSource\(''\);/, 'changing a selected image must revoke its prior blob URL before loading the replacement');
});

test('Home logo settings project visibility and custom image selection through the Desktop bridge', () => {
  assert.match(homeView, /useHomeLogo\(\)/);
  assert.match(homeView, /<HomeLogo logo=\{logo\}/);
  assert.match(homeView, /!logo\.hidden/);
  assert.match(appearance, /settings\.homeLogo/);
  assert.match(appearance, /desktop\.chooseHomeLogo/);
});

test('changing background image revokes the previous blob object URL', async () => {
  const originalCreate = globalThis.URL.createObjectURL;
  const originalRevoke = globalThis.URL.revokeObjectURL;
  const created: string[] = [];
  const revoked: string[] = [];
  let objectUrlCounter = 0;

  globalThis.URL.createObjectURL = (blob: Blob) => {
    objectUrlCounter += 1;
    const url = `blob:test-${objectUrlCounter}`;
    created.push(url);
    return url;
  };
  globalThis.URL.revokeObjectURL = (url: string) => {
    revoked.push(url);
  };

  // Provide a minimal bridge so theme.ts can run outside the renderer.
  (globalThis as unknown as { window: { mothx: unknown } }).window = {
    mothx: {
      isDesktop: true,
      platform: 'linux',
      desktop: {
        homeBackgroundDataURL: async () => ({ ok: true, dataUrl: 'data:image/png;base64,SGVsbG8=' }),
      },
    },
  };

  try {
    const { applyHomeBackground, updateHomeBackground } = await import('./theme.ts');
    const root = {
      isConnected: true,
      classList: { toggle() {}, remove() {} },
      style: { setProperty() {}, removeProperty() {} },
    } as unknown as HTMLElement;

    updateHomeBackground({ homeBackgroundImage: '/tmp/first.png' }, false);
    applyHomeBackground(root);
    await new Promise((resolve) => setTimeout(resolve, 20));

    updateHomeBackground({ homeBackgroundImage: '/tmp/second.png' }, false);
    applyHomeBackground(root);
    await new Promise((resolve) => setTimeout(resolve, 20));

    assert.equal(created.length, 2, 'two blob URLs should be created for two image selections');
    assert.equal(revoked.length, 1, 'the first blob URL should be revoked when the image changes');
    assert.equal(revoked[0], created[0], 'revoked URL must be the previously created blob URL');
  } finally {
    globalThis.URL.createObjectURL = originalCreate;
    globalThis.URL.revokeObjectURL = originalRevoke;
    (globalThis as unknown as { window?: unknown }).window = undefined;
  }
});
