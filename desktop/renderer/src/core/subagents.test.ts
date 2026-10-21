import assert from 'node:assert/strict';
import test from 'node:test';

import { state } from './state.ts';
import {
  applySubagentEvent,
  isSubagentRunning,
  projectSubagentUpdates,
  sessionSubagents,
  subagentLabel,
  subagentStatusKind,
  subagentSummary,
} from './subagents.ts';

test('subagent lifecycle events maintain a per-session list without touching the transcript', () => {
  const previous = state.subagents;
  try {
    state.subagents = {};
    applySubagentEvent({
      sessionId: 'session-a',
      event: 'subagent',
      agentId: 'child-1',
      status: 'started',
      memberDisplayName: '工程师',
      memberEmoji: '🛠️',
      memberRole: 'member',
      expertId: 'software-company',
    });
    applySubagentEvent({
      sessionId: 'session-b',
      event: 'subagent',
      agentId: 'child-2',
      status: 'started',
    });

    let list = sessionSubagents('session-a');
    assert.equal(list.length, 1);
    assert.equal(list[0].status, 'running');
    assert.equal(list[0].memberDisplayName, '工程师');
    assert.equal(subagentLabel(list[0]), '🛠️ 工程师');
    assert.equal(isSubagentRunning(list[0]), true);
    assert.deepEqual(subagentSummary(list), { total: 1, running: 1, completed: 0, failed: 0 });

    applySubagentEvent({ sessionId: 'session-a', event: 'subagent', agentId: 'child-1', status: 'completed' });
    list = sessionSubagents('session-a');
    assert.equal(list[0].status, 'completed');
    assert.equal(list[0].memberDisplayName, '工程师');
    assert.equal(isSubagentRunning(list[0]), false);
    assert.deepEqual(subagentSummary(list), { total: 1, running: 0, completed: 1, failed: 0 });

    // Another session's child never leaks into this session's projection.
    assert.equal(sessionSubagents('session-a').some((info) => info.agentId === 'child-2'), false);
  } finally {
    state.subagents = previous;
  }
});

test('subagent status vocabulary is shared with the ACP projection', () => {
  assert.equal(subagentStatusKind('started'), 'running');
  assert.equal(subagentStatusKind('ready'), 'running');
  assert.equal(subagentStatusKind('done'), 'completed');
  assert.equal(subagentStatusKind('error'), 'failed');
  assert.equal(subagentStatusKind('canceled'), 'cancelled');
  assert.equal(subagentStatusKind('incomplete'), 'incomplete');
});

test('child transcript updates reuse the chat transcript item shape', () => {
  const items = projectSubagentUpdates([
    { sessionUpdate: 'user_message_chunk', messageId: 'u1', content: { type: 'text', text: 'inspect' } },
    { sessionUpdate: 'agent_thought_chunk', messageId: 't1', content: { type: 'text', text: 'thinking' } },
    { sessionUpdate: 'agent_message_chunk', messageId: 'a1', content: { type: 'text', text: 'reading' } },
    { sessionUpdate: 'tool_call', toolCallId: 'call-1', title: 'read: path=main.go', kind: 'read', status: 'pending', rawInput: { path: 'main.go' } },
    { sessionUpdate: 'tool_call_update', toolCallId: 'call-1', status: 'completed', content: [{ type: 'content', content: { type: 'text', text: 'package main' } }] },
  ]);

  assert.deepEqual(items.map((item) => item.kind), ['user', 'thought', 'agent', 'tool']);
  const tool = items.find((item) => item.kind === 'tool');
  assert.ok(tool && tool.kind === 'tool');
  assert.equal(tool.status, 'completed');
  assert.equal(tool.contents.length, 1);
  assert.equal(items.find((item) => item.kind === 'agent')?.text, 'reading');
});
