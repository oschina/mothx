import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

const runtimePanel = await readFile(new URL('./RuntimePanel.tsx', import.meta.url), 'utf8');

test('runtime binary selection is applied through the privileged desktop bridge', () => {
  assert.match(
    runtimePanel,
    /desktop\.setRuntimeBinary\(/,
    'the panel must switch runtimes through desktop:set-runtime-binary so main validates, persists, and restarts',
  );
  assert.match(
    runtimePanel,
    /desktop\.chooseRuntimeBinary\(/,
    'the custom path must come from the main-process file dialog, never from renderer-side path input',
  );
  assert.doesNotMatch(
    runtimePanel,
    /\bexec\(|spawn\(|child_process/,
    'the renderer must never launch or inspect executables itself',
  );
});

test('the bundled runtime stays the default and fallbacks stay visible', () => {
  assert.match(runtimePanel, /value=\{runtimeSource\}/, 'the selector must reflect the persisted runtime source');
  assert.match(runtimePanel, /runtimeSource = runtime\?\.source \|\| 'bundled'/, 'the default runtime source is bundled');
  assert.match(
    runtimePanel,
    /settings\.runtimeFallbackCustom/,
    'a custom binary that cannot be used must surface the bundled fallback to the user',
  );
});
