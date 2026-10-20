import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const composer = await readFile(new URL('./Composer.tsx', import.meta.url), 'utf8');
const composerCore = await readFile(new URL('../core/composer.ts', import.meta.url), 'utf8');
const translations = await readFile(new URL('../core/i18n.ts', import.meta.url), 'utf8');

test('composer merges mode, provider and model into one toolbar control', () => {
  assert.match(composer, /\{\/\* 模式 \+ 供应商 \+ 模型:合并为一个配置入口 \*\//, 'one merged config control must replace the three separate toolbar buttons');
  assert.match(composer, /const configTitle = `\$\{t\('composer\.model'\)\}.*t\('composer\.provider'\)/s, 'the merged trigger must summarize model and provider');
  assert.match(composer, /t\('composer\.mode'\)/, 'mode must stay available inside the merged panel');
  assert.match(composer, /currentModelLabel\(\)/, 'the trigger label must project the canonical model');
  assert.match(composer, /aria-expanded=\{configOpen\}/, 'the trigger must expose its panel state');
  assert.doesNotMatch(composer, /function SearchablePicker/, 'separate provider and model pickers must be gone');
});

test('merged panel keeps searchable ACP-projected provider and model choices', () => {
  assert.match(composer, /function ConfigChoiceList/, 'provider and model tabs must share one searchable implementation');
  assert.match(composer, /placeholder=\{placeholder\}/, 'picker must render a search input');
  assert.match(composer, /searchText\(choice\)\.toLowerCase\(\)\.includes\(term\)/, 'picker must filter through the projected searchable text');
  assert.match(composer, /event\.key === 'Enter'/, 'Enter must choose the first filtered option');
  assert.match(composer, /onSelect\(first\.value\)/, 'Enter must select the first visible choice');
  assert.match(composer, /onOpenChange=\{\(next\) => \{/, 'Popover open state must close the merged panel (Escape handled by Radix)');
  assert.match(composer, /applyConfigOption\('provider', next\)/, 'provider selection must use session config options');
  assert.match(composer, /applyConfigOption\('model', next\)/, 'model selection must use session config options');
  assert.match(composer, /TabsTrigger value="model"/, 'model choices must stay directly reachable');
  assert.match(composer, /TabsTrigger value="provider"/, 'provider choices must stay directly reachable');
  assert.match(composer, /t\('composer\.noMatches'\)/, 'empty search state must stay localized');
});

test('model choices expose catalog capabilities without local provider state', () => {
  assert.match(composerCore, /input: Array\.isArray\(model\.input\) \? model\.input\.slice\(\) : \[\]/, 'catalog projection must retain input modalities');
  assert.match(composerCore, /catalogCapabilities\.set/, 'capabilities must be held only in the ephemeral catalog projection');
  assert.match(composer, /modelCapability\(provider, choice\.value\)/, 'model rows must read the ephemeral capability projection');
  assert.match(composer, /modalityLabel\(input\)/, 'input modalities must be rendered as capability badges');
  assert.match(composer, /capability\?\.reasoning/, 'reasoning must be rendered as a model capability');
  assert.doesNotMatch(composerCore, /desktop\.storeSet\([^)]*(provider|model)/i, 'picker must not persist provider or model state locally');
  assert.doesNotMatch(composerCore, /localStorage/i, 'picker must not use localStorage');
});

test('draft config options come from the shared ACP Runtime projection', () => {
  assert.match(composerCore, /mothx\/manage\/providers\/list/, 'draft provider/model choices must come from the ACP projection');
  assert.match(composerCore, /mothx\/session\/draft-config-options/, 'draft options must merge the runtime draft projection');
  assert.match(composerCore, /session\/set_config_option/, 'session config changes must go through canonical ACP');
});

test('composer picker copy remains bilingual', () => {
  for (const key of ['composer.provider', 'composer.model', 'composer.mode', 'composer.thinking', 'composer.config', 'composer.searchProvider', 'composer.searchModel', 'composer.noMatches', 'composer.reasoning']) {
    assert.equal(translations.split(`'${key}'`).length - 1, 2, `${key} must exist in both locales`);
  }
});
