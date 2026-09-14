import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { classifyMessage, desktopInitializeParams, parseStartupErrorLine, shouldRetryStartupError } from './acp-client.ts';
import { DesktopStore } from './store.ts';

test('classifyMessage separates responses, notifications, and reverse requests', () => {
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 1, result: {} }), 'response');
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 2, error: { code: -1, message: 'x' } }), 'response');
  assert.equal(classifyMessage({ jsonrpc: '2.0', method: 'session/update', params: {} }), 'notification');
  assert.equal(classifyMessage({ jsonrpc: '2.0', id: 3, method: 'session/request_permission', params: {} }), 'reverse-request');
  assert.equal(classifyMessage({ jsonrpc: '2.0' }), 'invalid');
  assert.equal(classifyMessage({ jsonrpc: '1.0', id: 4, result: {} } as never), 'invalid');
});

test('parseStartupErrorLine extracts the MOTHX_ACP_ERROR payload', () => {
  const parsed = parseStartupErrorLine('MOTHX_ACP_ERROR {"code":"provider_unusable","message":"default provider x has no API key","fix":"configure a key"}');
  assert.deepEqual(parsed, { code: 'provider_unusable', message: 'default provider x has no API key', fix: 'configure a key' });
  assert.equal(parseStartupErrorLine('plain stderr line'), null);
  assert.equal(parseStartupErrorLine('MOTHX_ACP_ERROR not-json'), null);
});

test('deterministic configuration startup errors do not enter the restart loop', () => {
  assert.equal(shouldRetryStartupError({ code: 'config_invalid', message: 'bad settings' }), false);
  assert.equal(shouldRetryStartupError({ code: 'provider_unusable', message: 'missing key' }), false);
  assert.equal(shouldRetryStartupError({ code: 'exited', message: 'process crashed' }), true);
  assert.equal(shouldRetryStartupError(null), true);
});

test('desktop ACP initialization leaves session work directories unrestricted by default', () => {
  const params = desktopInitializeParams({ clientInfo: { name: 'mothx-desktop', version: 'test' } });
  assert.deepEqual(params._meta, { mothx: { surface: 'desktop' } });
  assert.equal('workspace' in (params._meta?.mothx || {}), false);
});

test('desktop store persists UI state and clamps workspace history', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-desktop-store-'));
  try {
    const store = new DesktopStore(dir);
    assert.equal(store.get().theme, 'light');
    store.set({ theme: 'dark', locale: 'en', homeBackgroundImage: '/tmp/wallpaper.webp', homeBackgroundOpacity: 68, homeBackgroundBlur: 7, homeBackgroundScope: 'home', homeBackgroundFit: 'tile', homeBackgroundPosition: 'right', homeLogoVisible: false, homeLogoImage: '/tmp/logo.png', lastWorkspace: '/tmp/project-a' });
    store.set({ lastWorkspace: '/tmp/project-b' });
    store.set({ pinnedSessions: ['s1', 's1', 42 as never], sessionStatus: { s1: 'completed' } });
    const reloaded = new DesktopStore(dir);
    const data = reloaded.get();
    assert.equal(data.theme, 'dark');
    assert.equal(data.locale, 'en');
    assert.equal(data.homeBackgroundImage, '/tmp/wallpaper.webp');
    assert.equal(data.homeBackgroundOpacity, 68);
    assert.equal(data.homeBackgroundBlur, 7);
    assert.equal(data.homeBackgroundScope, 'home');
    assert.equal(data.homeBackgroundFit, 'tile');
    assert.equal(data.homeBackgroundPosition, 'right');
    assert.equal(data.homeLogoVisible, false);
    assert.equal(data.homeLogoImage, '/tmp/logo.png');
    assert.equal(data.lastWorkspace, '/tmp/project-b');
    assert.deepEqual(data.recentWorkspaces, ['/tmp/project-b', '/tmp/project-a']);
    assert.deepEqual(data.pinnedSessions, ['s1', 's1']);
    assert.deepEqual(data.sessionStatus, { s1: 'completed' });
    store.set({ homeBackgroundOpacity: 999, homeBackgroundBlur: -5, homeBackgroundScope: 'invalid' as never, homeBackgroundFit: 'invalid' as never, homeBackgroundPosition: 'invalid' as never });
    assert.equal(store.get().homeBackgroundOpacity, 100);
    assert.equal(store.get().homeBackgroundBlur, 0);
    assert.equal(store.get().homeBackgroundScope, 'app');
    assert.equal(store.get().homeBackgroundFit, 'cover');
    assert.equal(store.get().homeBackgroundPosition, 'center');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('desktop store ignores malformed persisted files', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mothx-desktop-store-'));
  try {
    const store = new DesktopStore(dir);
    store.set({ theme: 'dark' });
    const file = join(dir, 'desktop-store.json');
    writeFileSync(file, '{ broken json', 'utf8');
    const reloaded = new DesktopStore(dir);
    assert.equal(reloaded.get().theme, 'light');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
