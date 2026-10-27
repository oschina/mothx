import assert from 'node:assert/strict';
import test from 'node:test';

import { createDiagnosticLogger, DESKTOP_LOG_ROTATE_BYTES, DiagnosticLogBuffer, DiagnosticStreamGate, maskSecrets, shouldRotateDesktopLog } from './diagnostic-logs.ts';

test('maskSecrets obscures Bearer tokens and Authorization headers', () => {
  assert.equal(maskSecrets('Authorization: Bearer sk-1234567890abcdef'), 'Authorization: ***');
  assert.equal(maskSecrets('Authorization: bearer abc.def.ghi'), 'Authorization: ***');
  assert.equal(maskSecrets('GET /api?Authorization=Bearer%20secret'), 'GET /api?Authorization=***');
});

test('maskSecrets obscures api key / token / secret / password values', () => {
  assert.equal(maskSecrets('api_key=supersecret'), 'api_key=***');
  assert.equal(maskSecrets('apikey: supersecret'), 'apikey: ***');
  assert.equal(maskSecrets('token=supersecret'), 'token=***');
  assert.equal(maskSecrets('secret=supersecret'), 'secret=***');
  assert.equal(maskSecrets('password=supersecret'), 'password=***');
  assert.equal(maskSecrets('Authorization: supersecret'), 'Authorization: ***');
});

test('maskSecrets leaves unrelated text intact', () => {
  assert.equal(maskSecrets('starting ACP runtime /tmp/mothx'), 'starting ACP runtime /tmp/mothx');
  assert.equal(maskSecrets('error: unable to spawn child'), 'error: unable to spawn child');
});

test('DiagnosticStreamGate only projects live logs to the renderer on demand', () => {
  const gate = new DiagnosticStreamGate();
  assert.equal(gate.enabledForRenderer, false, 'live log projection must default off');
  gate.setEnabled(true);
  assert.equal(gate.enabledForRenderer, true);
  gate.setEnabled(false);
  assert.equal(gate.enabledForRenderer, false);
  gate.setEnabled('yes' as never);
  assert.equal(gate.enabledForRenderer, false, 'only a real true enables projection');
});

test('desktop.log rotates only after the bounded size', () => {
  assert.equal(shouldRotateDesktopLog(0), false);
  assert.equal(shouldRotateDesktopLog(DESKTOP_LOG_ROTATE_BYTES - 1), false);
  assert.equal(shouldRotateDesktopLog(DESKTOP_LOG_ROTATE_BYTES), false);
  assert.equal(shouldRotateDesktopLog(DESKTOP_LOG_ROTATE_BYTES + 1), true);
  assert.equal(shouldRotateDesktopLog(Number.NaN), false);
});

test('DiagnosticLogBuffer bounds entries and emits subscribers', () => {
  const buffer = new DiagnosticLogBuffer({ maxEntries: 3 });
  const seen: string[] = [];
  buffer.subscribe((entry) => seen.push(entry.message));

  buffer.append('desktop', 'line 1');
  buffer.append('acp', 'line 2');
  buffer.append('renderer', 'line 3');

  assert.deepEqual(buffer.snapshot().map((e) => e.message), ['line 1', 'line 2', 'line 3']);
  assert.deepEqual(seen, ['line 1', 'line 2', 'line 3']);

  buffer.append('desktop', 'line 4');
  assert.deepEqual(buffer.snapshot().map((e) => e.message), ['line 2', 'line 3', 'line 4']);
  assert.equal(buffer.snapshot().length, 3);
});

test('DiagnosticLogBuffer stores appended messages without masking', () => {
  const buffer = new DiagnosticLogBuffer({ maxEntries: 10 });
  buffer.append('acp', 'Authorization: Bearer abc123');
  const entry = buffer.snapshot()[0];
  assert.equal(entry?.message, 'Authorization: Bearer abc123');
});

test('createDiagnosticLogger masks once before console, file, and buffer', () => {
  const buffer = new DiagnosticLogBuffer({ maxEntries: 10 });
  const consoleLines: string[] = [];
  const fileLines: string[] = [];
  const logger = createDiagnosticLogger({
    buffer,
    getLogStream: () => ({ write: (line: string) => fileLines.push(line) }),
    consoleLog: (line: string) => consoleLines.push(line),
  });

  logger.log('Authorization: Bearer secret-token', 'acp');

  assert.equal(buffer.snapshot().length, 1);
  assert.equal(buffer.snapshot()[0]?.message, 'Authorization: ***');
  assert.ok(consoleLines[0]?.includes('Authorization: ***'));
  assert.ok(fileLines[0]?.includes('Authorization: ***'));
  assert.equal(consoleLines[0]?.includes('secret-token'), false);
  assert.equal(fileLines[0]?.includes('secret-token'), false);
  assert.equal(buffer.snapshot()[0]?.message.includes('secret-token'), false);
});

test('DiagnosticLogBuffer subscribe unsubscribe stops delivery', () => {
  const buffer = new DiagnosticLogBuffer({ maxEntries: 10 });
  const seen: string[] = [];
  const unsubscribe = buffer.subscribe((entry) => seen.push(entry.message));
  buffer.append('desktop', 'before');
  assert.deepEqual(seen, ['before']);
  unsubscribe();
  buffer.append('desktop', 'after');
  assert.deepEqual(seen, ['before']);
});

test('DiagnosticLogBuffer clear removes all entries', () => {
  const buffer = new DiagnosticLogBuffer({ maxEntries: 10 });
  buffer.append('desktop', 'line');
  assert.equal(buffer.snapshot().length, 1);
  buffer.clear();
  assert.equal(buffer.snapshot().length, 0);
});
