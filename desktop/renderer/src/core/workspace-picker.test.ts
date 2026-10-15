import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

import { applyWorkspacePickerTarget, workspacePickerTarget } from './workspace-picker.ts';

const composerSrc = await readFile(new URL('../components/Composer.tsx', import.meta.url), 'utf8');
const bootstrapSrc = await readFile(new URL('./bootstrap.ts', import.meta.url), 'utf8');
const stateSrc = await readFile(new URL('./state.ts', import.meta.url), 'utf8');
const pickerSrc = await readFile(new URL('./workspace-picker.ts', import.meta.url), 'utf8');

test('working-directory picker follows the model picker in the shared composer toolbar', () => {
  const modelIndex = composerSrc.indexOf("label={t('composer.model')}");
  const workspaceIndex = composerSrc.indexOf('aria-label={workspaceTitle}');
  assert.ok(modelIndex >= 0, 'model picker must exist');
  assert.ok(workspaceIndex > modelIndex, 'workspace picker must follow the model picker');
  assert.doesNotMatch(composerSrc, /workspace-chip/, 'the title bar must not own a working-directory selector');
});

test('picker changes only the active session cwd and never starts a replacement task', () => {
  assert.match(composerSrc, /applyWorkspacePickerTarget\(appState\.activeSessionId/);
  assert.match(composerSrc, /changeSessionWorkingDirectory,/);
  assert.match(composerSrc, /chooseNextSessionWorkingDirectory: chooseWorkingDirectory/);
  assert.doesNotMatch(composerSrc, /startNewTaskWithDirectory/);
});

test('picker routing cannot mistake a next-session default for a session cwd', () => {
  const calls: string[] = [];
  const actions = {
    changeSessionWorkingDirectory: (sessionId: string) => calls.push(`session:${sessionId}`),
    chooseNextSessionWorkingDirectory: () => calls.push('next-session'),
  };

  assert.deepEqual(workspacePickerTarget(null), { kind: 'next-session' });
  assert.deepEqual(workspacePickerTarget('session-123'), { kind: 'session', sessionId: 'session-123' });

  assert.deepEqual(applyWorkspacePickerTarget('session-123', actions), { kind: 'session', sessionId: 'session-123' });
  assert.deepEqual(calls, ['session:session-123']);

  calls.length = 0;
  assert.deepEqual(applyWorkspacePickerTarget(null, actions), { kind: 'next-session' });
  assert.deepEqual(calls, ['next-session']);
  assert.match(pickerSrc, /owns no directory value or persistence/);
});

test('quick start uses a transient scratch directory while New Task has no implicit cwd', () => {
  assert.match(bootstrapSrc, /state\.draftWorkspaceSource !== 'quick-start'/);
  assert.match(bootstrapSrc, /desktop\.defaultNewSessionDirectory\(\)\.then\(\(cwd\)/);
  assert.match(bootstrapSrc, /isCurrentDraftWorkspace\('quick-start', '', generation\)/);
  const newSessionWorkspace = stateSrc.match(/export function newSessionWorkspace\(\): string \{[\s\S]*?\n\}/)?.[0] || '';
  assert.match(newSessionWorkspace, /return state\.newSessionCwd;/);
  assert.doesNotMatch(newSessionWorkspace, /lastWorkspace/);
  assert.doesNotMatch(newSessionWorkspace, /state\.connection\.workspace/);
});
