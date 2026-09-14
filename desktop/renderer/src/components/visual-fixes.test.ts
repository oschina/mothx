import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const sidebar = await readFile(new URL('./Sidebar.tsx', import.meta.url), 'utf8');
const home = await readFile(new URL('../views/HomeView.tsx', import.meta.url), 'utf8');
const composer = await readFile(new URL('./Composer.tsx', import.meta.url), 'utf8');
const appearance = await readFile(new URL('../views/settings/AppearancePanel.tsx', import.meta.url), 'utf8');
const styles = await readFile(new URL('../index.css', import.meta.url), 'utf8');

test('appearance settings rows render meaningful icons', () => {
  for (const icon of ['<Sun />', '<Globe />', '<ImageIcon />', '<SlidersHorizontal />', '<Frame />', '<AlignLeft />']) {
    assert.ok(appearance.includes(icon), `appearance rows must include ${icon}`);
  }
});

test('sidebar New Task button is lighter than a solid accent block while staying primary', () => {
  assert.match(sidebar, /bg-primary\/8 font-semibold text-primary/, 'base new-task button must use a subtle accent tint');
  assert.match(sidebar, /border-primary\/55/, 'base new-task button must keep a restrained accent border');
  assert.match(sidebar, /hover:bg-primary hover:text-primary-foreground hover:border-primary/, 'new-task hover must fill with the accent color');
});

test('global-background New Task button uses a readable control surface instead of an opaque accent slab', () => {
  assert.match(sidebar, /background\.app &&\s*'app-control-surface border-\[var\(--app-control-border-strong\)\] bg-\[var\(--app-control-bg\)\] text-primary/, 'global-background new-task must sit on the shared control surface');
  assert.match(styles, /\.app-control-surface \{[\s\S]*?background: var\(--app-control-bg\)/, 'control surface token must exist');
});

test('Home hero headline is less saturated and secondary text is more readable', () => {
  assert.match(
    home,
    /font-medium text-\[color-mix\(in_srgb,var\(--home-accent\)_55%,var\(--home-muted\)\)\]/,
    'hero accent text must be desaturated with the muted tone at a lighter weight',
  );
  assert.match(home, /text-\[13px\] text-home-text opacity-78/, 'hero subtitle must stay slightly subdued but readable');
});

test('Home mode description, quick cards, and composer are readable', () => {
  assert.match(home, /font-semibold text-home-text opacity-90/, 'mode-desc sub must use strong text color');
  assert.match(home, /text-\[12px\] leading-normal text-home-muted/, 'mode-desc desc must use readable muted color');
  assert.match(home, /text-\[color-mix\(in_srgb,var\(--home-text\)_90%,transparent\)\]/, 'quick cards must use stronger text color');
  assert.match(home, /tracking-\[\.4px\] text-home-muted/, 'quick card tags must use readable muted color');
});

test('Home vertical spacing centers the content without affecting the chat composer', () => {
  assert.match(home, /min-h-full/, 'home inner must fill the scroll height so justify-content can balance content');
  assert.match(home, /justify-center/, 'home inner must center its content vertically');
  assert.match(home, /box-border/, 'home padding must remain within the available height');
  assert.match(home, /mt-9 w-full/, 'home composer must keep a clean 36px gap from the content above');
  assert.match(composer, /rounded-xl border/, 'chat composer layout must not inherit Home spacing');
});

test('Home composer area is modestly wider while keeping a wrap fallback for narrow viewports', () => {
  assert.match(home, /w-\[min\(820px,92%\)\]/, 'home inner must use a responsive min(...) width cap');
  assert.match(composer, /flex min-w-0 flex-wrap items-center/, 'composer controls must still wrap when the Home width is constrained');
});

test('sidebar hierarchy labels and statuses are readable without breaking density or truncation', () => {
  const muted = [
    /nav-sub[^\n]*text-muted-foreground|text-\[10\.5px\] text-muted-foreground/,
    /text-\[10\.5px\] font-bold tracking-\[\.3px\] text-muted-foreground/,
    /text-\[10\.5px\] tabular-nums text-muted-foreground/,
    /text-\[11\.5px\] text-muted-foreground/,
    /text-\[11px\] text-muted-foreground/,
  ];
  for (const pattern of muted) {
    assert.match(sidebar, pattern, 'hierarchy labels must use the readable muted text color');
  }
  assert.match(sidebar, /flex-1 truncate text-\[12\.5px\]/, 'session title must keep single-line truncation');
});

test('app background system keeps structural panels quiet and controls readable', () => {
  assert.match(styles, /\.app-shell\.has-app-background::before \{[\s\S]*?background-image: var\(--app-user-image\)/, 'app background must render through the injected image variable');
  assert.match(styles, /\.app-shell\.has-app-background::after \{[\s\S]*?--app-background-veil/, 'app background must keep the derived veil layer');
  assert.match(
    styles,
    /\.has-home-background \.home-aurora,\s*\.has-app-background \.home-aurora \{[\s\S]*?rgb\(var\(--home-image-overlay-rgb\) \/ var\(--app-background-veil, 0\.72\)\)/,
    'Home and app-wide backgrounds must veil the aurora so the image stays visible',
  );
  assert.match(styles, /--app-surface-veil/, 'surface opacity must be configurable instead of a fixed white veil');
  assert.match(styles, /@layer utilities \{[\s\S]*?\.app-shell\.has-app-background \.app-surface-veil \{[\s\S]*?background: rgb\(var\(--home-image-overlay-rgb\) \/ var\(--app-surface-veil, 0\.52\)\)/, 'global background veil must override opaque structural background utilities');
});
