import assert from 'node:assert/strict';
import { readdir, readFile } from 'node:fs/promises';
import test from 'node:test';

const settingsDir = new URL('.', import.meta.url);

// Settings panels are often open while a background run streams transcript
// chunks. Subscribing to the whole app state re-rendered every open panel on
// each coalesced frame; each panel must select only the state it displays.
test('settings panels subscribe by selector instead of the whole app state', async () => {
  const entries = await readdir(settingsDir);
  const panels = entries.filter((name) => name.endsWith('Panel.tsx')).sort();
  assert.ok(panels.length >= 16, `expected every settings panel, found ${panels.length}`);

  for (const name of panels) {
    const source = await readFile(new URL(name, settingsDir), 'utf8');
    assert.ok(
      !/\buseAppState\s*\(\s*\)/.test(source),
      `${name} must not subscribe to the whole app state`,
    );
    assert.match(source, /useAppSelector/, `${name} must use a narrow selector`);
  }
});
