import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const appSettings = await readFile(new URL('../views/settings/AppSettings.svelte', import.meta.url), 'utf8');
const editorDetail = await readFile(new URL('../views/settings/ProviderEditorDetail.svelte', import.meta.url), 'utf8');
const preferences = await readFile(new URL('./preferences.js', import.meta.url), 'utf8');

const toolControlKeys = [
  'settings.app.toolChoice',
  'settings.app.toolChoiceHint',
  'settings.app.toolParallel',
  'settings.app.toolMaxCalls',
  'settings.app.toolMaxCallsHint',
  'settings.app.modelSupportsToolChoice',
  'settings.app.modelSupportsParallelToolCalls'
];

test('provider tool choice settings carry zh and en translations', () => {
  for (const key of toolControlKeys) {
    const occurrences = preferences.split(`'${key}'`).length - 1;
    assert.equal(occurrences, 2, `${key} must be defined in both the zh and en dictionaries`);
  }
});

test('provider settings load and persist responses.toolControl', () => {
  assert.match(appSettings, /responses\?\.toolControl\?\.choice/, 'tool choice must be read back from settings');
  assert.match(appSettings, /responses\?\.toolControl\?\.parallel/, 'parallel flag must be read back from settings');
  assert.match(appSettings, /responses\?\.toolControl\?\.maxCalls/, 'max calls must be read back from settings');
  assert.match(appSettings, /ensureObject\(raw\.responses, 'toolControl'\)/, 'toolControl must be written as a nested object');
  assert.match(appSettings, /writeString\(toolControl, 'choice'/, 'tool choice must be written back');
  assert.match(appSettings, /writeTriBool\(toolControl, 'parallel'/, 'parallel flag must be written back as a tri-state');
  assert.match(appSettings, /writeOptionalNumber\(toolControl, 'maxCalls'/, 'max calls must be written back as an optional number');
  assert.match(appSettings, /delete raw\.responses\.toolControl/, 'empty toolControl must not be persisted');
});

test('model compat tool choice flags round trip without mutating the loaded row', () => {
  assert.match(appSettings, /compat\?\.supportsToolChoice/, 'supportsToolChoice must be read from the model compat block');
  assert.match(appSettings, /compat\?\.supportsParallelToolCalls/, 'supportsParallelToolCalls must be read from the model compat block');
  assert.match(appSettings, /writeTriBool\(compat, 'supportsToolChoice'/, 'supportsToolChoice must be written back');
  assert.match(appSettings, /writeTriBool\(compat, 'supportsParallelToolCalls'/, 'supportsParallelToolCalls must be written back');
  assert.match(appSettings, /\{ \.\.\.raw\.compat \}/, 'compat edits must copy the loaded object instead of mutating it');
});

test('provider editor renders the tool choice and compat controls', () => {
  for (const key of ['toolChoice', 'toolParallel', 'toolMaxCalls', 'modelSupportsToolChoice', 'modelSupportsParallelToolCalls']) {
    assert.match(editorDetail, new RegExp(`\\$t\\('settings\\.app\\.${key}'\\)`), `${key} must have a settings control`);
  }
  assert.match(editorDetail, /bind:value=\{provider\.responses\.toolChoice\}/);
  assert.match(editorDetail, /bind:value=\{provider\.responses\.toolParallel\}/);
  assert.match(editorDetail, /bind:value=\{provider\.responses\.toolMaxCalls\}/);
  assert.match(editorDetail, /bind:value=\{model\.supportsToolChoice\}/);
  assert.match(editorDetail, /bind:value=\{model\.supportsParallelToolCalls\}/);
});

test('new provider models use catalog presets with 256K reasoning fallback', () => {
  assert.match(appSettings, /function applyModelIDPreset\(/, 'model IDs must seed a new draft from the shared catalog');
  assert.match(appSettings, /catalog\.modelDefaults/, 'generic model defaults must come from the server catalog');
  assert.match(appSettings, /256000/, 'older servers must retain the 256K compatibility fallback');
  assert.match(editorDetail, /onblur=\{\(\) => onModelIDCommit\(provider, model\)\}/, 'the model ID editor must apply its preset');
});
