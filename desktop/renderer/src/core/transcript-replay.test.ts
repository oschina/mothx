import assert from 'node:assert/strict';
import test from 'node:test';

import { state } from './state.ts';
import { applySessionEvent, applySessionUpdate, applyTranscriptPage } from './transcript.ts';

// 历史回放(session/load、mothx/session/history)会把 tool_call 与其后续
// tool_call_update 放在同一页里重放。回归点:同页 update 必须落到同一条
// tool 条目上,否则状态修改丢失,历史 tool 卡永远显示"排队中"。
test('replayed history pages keep the final tool status instead of stuck pending', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [];
  state.transcriptSessionId = 'sess-replay';
  try {
    applyTranscriptPage(
      'sess-replay',
      [
        { sessionUpdate: 'tool_call', toolCallId: 'tc-1', title: 'bash ls', kind: 'execute', status: 'pending' },
        { sessionUpdate: 'tool_call_update', toolCallId: 'tc-1', status: 'in_progress' },
        {
          sessionUpdate: 'tool_call_update',
          toolCallId: 'tc-1',
          status: 'completed',
          content: [{ type: 'content', content: { type: 'text', text: 'ok' } }],
        },
      ],
      false,
    );
    const items = state.transcript.filter((entry) => entry.key === 'tool:tc-1');
    assert.equal(items.length, 1, 'replay must not duplicate the tool entry');
    const item = items[0];
    assert.ok(item.kind === 'tool');
    assert.equal(item.status, 'completed', 'final replayed status must win over the initial pending');
    assert.equal(item.contents.length, 1, 'replayed tool output must attach to the same entry');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});

test('prepended older pages merge updates into their own items without touching the visible transcript', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [
    { kind: 'tool', key: 'tool:live', toolCallId: 'live', title: 'live', toolKind: 'other', status: 'in_progress', open: false, contents: [] },
  ];
  state.transcriptSessionId = 'sess-replay';
  try {
    applyTranscriptPage(
      'sess-replay',
      [
        { sessionUpdate: 'tool_call', toolCallId: 'old-1', title: 'read', kind: 'read', status: 'pending' },
        { sessionUpdate: 'tool_call_update', toolCallId: 'old-1', status: 'failed' },
      ],
      true,
    );
    const oldItem = state.transcript.find((entry) => entry.key === 'tool:old-1');
    const liveItem = state.transcript.find((entry) => entry.key === 'tool:live');
    assert.ok(oldItem && oldItem.kind === 'tool');
    assert.equal(oldItem.status, 'failed');
    assert.equal(state.transcript[0].key, 'tool:old-1', 'older page must be prepended');
    assert.ok(liveItem && liveItem.kind === 'tool' && liveItem.status === 'in_progress', 'visible entries must stay untouched');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});

test('replayed publish_artifact tool completion still projects the fallback artifact card', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [];
  state.transcriptSessionId = 'sess-replay';
  try {
    applyTranscriptPage(
      'sess-replay',
      [
        {
          sessionUpdate: 'tool_call',
          toolCallId: 'tc-pub',
          title: 'publish_artifact report.md',
          kind: 'other',
          status: 'pending',
          rawInput: { path: '/work/report.md' },
        },
        { sessionUpdate: 'tool_call_update', toolCallId: 'tc-pub', status: 'completed' },
      ],
      false,
    );
    const artifact = state.transcript.find((entry) => entry.kind === 'artifact');
    assert.ok(artifact, 'completed publish_artifact in history must project an artifact card');
    assert.equal(artifact.kind === 'artifact' && artifact.filename, 'report.md');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});

test('status, retry, and compaction notices replace instead of accumulating', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [];
  state.transcriptSessionId = 'sess-status';
  try {
    applySessionEvent({ sessionId: 'sess-status', event: 'status', message: 'first' });
    applySessionEvent({ sessionId: 'sess-status', event: 'status', message: 'second' });
    applySessionEvent({ sessionId: 'sess-status', event: 'retry', message: 'one' });
    applySessionEvent({ sessionId: 'sess-status', event: 'retry', message: 'two' });
    applySessionEvent({ sessionId: 'sess-status', event: 'compaction_start' });
    applySessionEvent({ sessionId: 'sess-status', event: 'compaction_start' });
    const statuses = state.transcript.filter((entry) => entry.kind === 'status');
    assert.equal(statuses.length, 3, 'one line per status/retry/compaction kind');
    const status = statuses.find((entry) => entry.kind === 'status' && entry.key === 'status:sess-status');
    assert.ok(status && status.kind === 'status');
    assert.equal(status.text, 'second');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});

test('oversized tool output is bounded in the renderer display copy', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [];
  state.transcriptSessionId = 'sess-big-tool';
  try {
    applySessionUpdate('sess-big-tool', {
      sessionUpdate: 'tool_call',
      toolCallId: 'tc-big',
      title: 'bash yes',
      kind: 'execute',
      status: 'pending',
    });
    applySessionUpdate('sess-big-tool', {
      sessionUpdate: 'tool_call_update',
      toolCallId: 'tc-big',
      status: 'completed',
      content: [{ type: 'content', content: { type: 'text', text: 'x'.repeat(100000) } }],
    });
    const item = state.transcript.find((entry) => entry.kind === 'tool');
    assert.ok(item && item.kind === 'tool');
    const text = item.contents[0]?.content?.text || '';
    assert.ok(text.length <= 20002, 'renderer tool text must stay bounded');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});

// React.memo on TranscriptEntry depends on this invariant: one streamed chunk
// must replace only the changed item and leave every other card's identity alone.
test('streaming chunks replace the transcript item instead of mutating it', () => {
  const previous = { transcript: state.transcript, transcriptSessionId: state.transcriptSessionId };
  state.transcript = [];
  state.transcriptSessionId = 'sess-stream';
  try {
    applySessionUpdate('sess-stream', {
      sessionUpdate: 'agent_message_chunk',
      messageId: 'm1',
      content: { type: 'text', text: 'a' },
    });
    const first = state.transcript[0];
    assert.ok(first && first.kind === 'agent');
    assert.equal(first.text, 'a');

    applySessionUpdate('sess-stream', {
      sessionUpdate: 'agent_message_chunk',
      messageId: 'm1',
      content: { type: 'text', text: 'b' },
    });
    const second = state.transcript[0];
    assert.ok(second && second.kind === 'agent');
    assert.equal(second.text, 'ab');
    assert.notEqual(second, first, 'the changed item must be a new object');
    assert.equal(first.text, 'a', 'the previous item snapshot must be unchanged');
  } finally {
    state.transcript = previous.transcript;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
});
