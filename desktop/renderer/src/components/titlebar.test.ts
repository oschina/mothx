import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const titlebar = await readFile(new URL('./TitleBar.tsx', import.meta.url), 'utf8');
const styles = await readFile(new URL('../index.css', import.meta.url), 'utf8');

test('titlebar keeps its normal inactive styling and drag semantics', () => {
  assert.match(titlebar, /titlebar-drag/, 'titlebar must remain draggable');
  assert.match(titlebar, /bg-titlebar/, 'inactive titlebar must use the theme titlebar background');
  assert.match(titlebar, /border-b border-border/, 'inactive titlebar must keep its bottom border');
  assert.match(titlebar, /titlebar-no-drag/, 'window controls must stay non-draggable');
  assert.match(styles, /\.titlebar-drag \{[\s\S]*?-webkit-app-region: drag/, 'drag region must be declared in the component layer');
  assert.match(styles, /\.titlebar-drag button,\s*\.titlebar-no-drag \{[\s\S]*?-webkit-app-region: no-drag/, 'buttons must opt out of dragging');
  assert.match(styles, /@layer utilities \{[\s\S]*?body\.platform-darwin \.titlebar-drag \{[\s\S]*?padding-left: 78px/, 'macOS traffic-light offset must live in the utilities layer so it beats the `pl-3` utility');
});

test('native macOS traffic lights are used only in the macOS projection on darwin', () => {
  assert.match(titlebar, /desktop\.platform\(\) === 'darwin'/, 'platform detection must gate native traffic lights');
  assert.match(titlebar, /windowStyle === 'mac' && !isDarwin/, 'only the macOS projection on non-mac hosts draws traffic lights');
  assert.match(titlebar, /windowStyle === 'win' \? <WindowsWindowControls/, 'the Windows projection must render the custom controls');
  assert.match(titlebar, /windowControl\('minimize'\)/);
  assert.match(titlebar, /windowControl\('maximize'\)/);
  assert.match(titlebar, /windowControl\('close'\)/);
});

test('window-style switch stays implemented but hidden from the titlebar', () => {
  assert.match(titlebar, /function WindowStyleSwitch/, 'the switch component must remain available for re-enabling');
  assert.match(titlebar, /WINDOW_STYLE_SWITCH_VISIBLE = false/, 'the switch must be hidden without deleting it');
  assert.match(titlebar, /WINDOW_STYLE_SWITCH_VISIBLE \? <WindowStyleSwitch windowStyle=\{windowStyle\} \/> : null/, 'the titlebar must render the switch only when it is re-enabled');
  assert.match(titlebar, /windowStyle === 'mac' && 'active'/, 'macOS segment must keep its active state');
  assert.match(titlebar, /windowStyle === 'win' && 'active'/, 'Windows segment must keep its active state');
  assert.match(titlebar, /applyWindowStyle\(style\)/, 'switching must go through the shared theme action');
  assert.match(titlebar, /windowStyle\.macToast/, 'macOS switch must announce the change');
  assert.match(titlebar, /windowStyle\.winToast/, 'Windows switch must announce the change');
  assert.match(styles, /html\[data-window-style="mac"\] \{[\s\S]*?--titlebar-h: 42px/, 'macOS projection must use a 42px titlebar');
  assert.match(styles, /html\[data-window-style="win"\] \{[\s\S]*?--titlebar-h: 40px/, 'Windows projection must use a 40px titlebar');
  assert.match(styles, /html\[data-window-style="win"\] body\.platform-darwin \.titlebar-drag \{[\s\S]*?padding-left: 12px/, 'Windows projection must reclaim the native traffic-light inset');
});

test('global-background titlebar uses readable contrast surfaces for text and window controls', () => {
  assert.match(titlebar, /background\.app && 'app-surface-veil border-b-transparent'/, 'titlebar must blur the app background');
  assert.match(titlebar, /app-titlebar-surface/, 'titlebar text area and window controls must share a subtler control surface');
  assert.match(styles, /\.app-titlebar-surface \{[\s\S]*?background: var\(--app-titlebar-control-bg\)/, 'control surface must use the lighter titlebar token');
  assert.match(styles, /\.app-titlebar-surface \{[\s\S]*?border: 1px solid var\(--app-titlebar-control-border\)/, 'control surface must have a restrained border');
  assert.match(styles, /\.app-titlebar-surface \{[\s\S]*?box-shadow: var\(--app-titlebar-control-shadow\)/, 'control surface must cast a subtler shadow');
  assert.match(styles, /\.app-titlebar-surface \{[\s\S]*?backdrop-filter: blur\(10px\) saturate\(140%\)/, 'control surface must blur and saturate the background underneath');
  assert.match(styles, /--app-titlebar-control-bg: rgb\(var\(--home-image-overlay-rgb\) \/ 0\.46\)/, 'titlebar control surface must be more transparent than regular controls');
  assert.match(styles, /--app-titlebar-control-border: rgb\(var\(--app-outline-rgb\) \/ 0\.12\)/, 'titlebar control border must be more restrained');
  assert.match(styles, /--app-titlebar-control-shadow: 0 1px 4px rgb\(var\(--app-outline-rgb\) \/ 0\.06\)/, 'titlebar control shadow must be more subtle');
});

test('global-background titlebar preserves hover and close danger state', () => {
  assert.match(titlebar, /hover:bg-\[var\(--app-titlebar-control-hover\)\]/, 'window controls must show a hover surface over a background image');
  assert.match(titlebar, /hover:border-\[var\(--app-control-border-strong\)\]/, 'window controls must darken their border on hover');
  assert.match(titlebar, /hover:bg-danger hover:text-destructive-foreground/, 'close control must retain its red danger hover state');
});

test('titlebar projects app identity and version', () => {
  assert.match(titlebar, /MothxLogo/, 'titlebar must carry the shared brand asset');
  assert.match(titlebar, /v\{version\}/, 'version badge must reflect the packaged app version');
});
