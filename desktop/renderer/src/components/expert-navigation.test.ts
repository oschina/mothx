import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const sidebar = await readFile(new URL('./Sidebar.tsx', import.meta.url), 'utf8');
const composer = await readFile(new URL('./Composer.tsx', import.meta.url), 'utf8');
const composerCore = await readFile(new URL('../core/composer.ts', import.meta.url), 'utf8');
const translations = await readFile(new URL('../core/i18n.ts', import.meta.url), 'utf8');

test('expert teams sidebar entry precedes knowledge library and is ACP-gated', () => {
  const expertIndex = sidebar.indexOf("view: 'experts'");
  const libraryIndex = sidebar.indexOf("view: 'library'");
  assert.ok(expertIndex >= 0 && expertIndex < libraryIndex, 'Expert teams must appear directly above the library');
  const between = sidebar.slice(expertIndex + "view: 'experts'".length, libraryIndex);
  assert.ok(!between.includes("view: '"), 'No other nav item may sit between experts and library');
  assert.match(sidebar, /feature: 'manageExperts'/, 'sidebar entry must follow the ACP feature gate');
  assert.match(sidebar, /openSettingsTab\('experts'\)/, 'sidebar entry must open the canonical Expert settings tab');
});

test('composer binds or clears current session expert through its canonical config option', () => {
  assert.match(composerCore, /applyConfigOption\('expert', value\)/, 'fresh binding and unbinding must use the expert config option');
  assert.match(composerCore, /expertOption\.currentValue && value/, 'only non-empty expert replacement may fork');
  assert.match(composerCore, /forkSession\(sessionID, value\)/, 'expert replacement must use the Runtime-owned session fork');
  assert.match(composerCore, /if \(value === expertOption\.currentValue\) return;/, 're-selecting the current expert is a no-op');
});

test('chat composer exposes an independent visible Expert Teams picker', () => {
  assert.match(composer, /expertAvailable \? \(/, 'picker must only render when the runtime projects expert options');
  assert.match(composer, /const expertAvailable = Boolean\(expertOption\?\.options\?\.length\);/, 'availability must derive from the canonical config option');
  assert.match(composer, /aria-label=\{expertTitle\}/, 'picker must have a localized accessible label');
  assert.match(composer, /title=\{expertTitle\}/, 'picker must have a localized accessible title');
  assert.match(composer, /chooseSessionExpert\(expertOption, choice\.value\)/, 'picker must share canonical binding behavior');
  assert.match(composer, /t\('composer\.expertNone'\)/, 'picker menu must include a localized No expert option');
  assert.match(composer, /DropdownMenuLabel>\{t\('composer\.expert'\)\}/, 'expert menu must be independent of the mode menu');
});

test('new-session expert selection is independent from Mode and is carried into the initial session config', () => {
  const configPicker = composer.match(/\{\/\* 模式 \+ 供应商 \+ 模型[\s\S]*?\{\/\* 主角团/)?.[0] || '';
  assert.ok(configPicker.length > 0, 'merged mode/provider/model panel must be locatable');
  assert.doesNotMatch(configPicker, /expertOption|chooseSessionExpert/, 'the merged config panel must not duplicate the dedicated Expert picker');
  assert.match(composerCore, /mothx\/session\/draft-config-options/, 'draft options must come from the shared ACP Runtime projection');
  assert.match(composerCore, /pendingConfig\[configId\] = value/, 'a pre-session expert choice must be retained for session creation');
  assert.match(composerCore, /pendingConfig\.expert = undefined/, 'the pending expert choice must be cleared after it is applied');
});

test('expert sidebar label stays bilingual', () => {
  assert.equal(translations.split("'nav.experts'").length - 1, 2, 'nav.experts must be present in both translation maps');
});
