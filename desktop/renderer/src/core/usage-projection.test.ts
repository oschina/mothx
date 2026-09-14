import assert from 'node:assert/strict';
import test from 'node:test';

import { state } from './state.ts';
import { applySessionUpdate } from './transcript.ts';

// usage_update 的标准字段(used/size/cost)与 mothx.dev 附加的会话累计缓存量
// 必须分别投影:客户端不自己算分母,也不在扩展缺失时伪造 0。
function withUsageBaseline(run: () => void): void {
  const previous = { usage: state.usage, transcriptSessionId: state.transcriptSessionId };
  state.usage = null;
  state.transcriptSessionId = 'sess-usage';
  try {
    run();
  } finally {
    state.usage = previous.usage;
    state.transcriptSessionId = previous.transcriptSessionId;
  }
}

test('usage_update projects the mothx.dev cache totals carried by ACP', () => {
  withUsageBaseline(() => {
    applySessionUpdate('sess-usage', {
      sessionUpdate: 'usage_update',
      used: 20,
      size: 100,
      cost: { amount: 0.00002, currency: 'USD' },
      _meta: { 'mothx.dev': { cacheRead: 40, cacheWrite: 8, totalInputTokens: 58 } },
    });

    assert.deepEqual(state.usage, {
      used: 20,
      size: 100,
      cost: 0.00002,
      cache: { cacheRead: 40, cacheWrite: 8, totalInputTokens: 58 },
    });
  });
});

test('usage_update keeps standard fields alone when the extension is absent', () => {
  withUsageBaseline(() => {
    applySessionUpdate('sess-usage', {
      sessionUpdate: 'usage_update',
      used: 12,
      size: 100,
      cost: { amount: 0.0001, currency: 'USD' },
    });

    assert.equal(state.usage?.cache, undefined, 'an older runtime must not gain invented cache totals');

    // 后续事件仍然携带累计缓存时,已投影的值继续跟随服务端累计量。
    applySessionUpdate('sess-usage', {
      sessionUpdate: 'usage_update',
      used: 12,
      size: 100,
      _meta: { 'mothx.dev': { cacheRead: 100, cacheWrite: 8, totalInputTokens: 122 } },
    });
    assert.deepEqual(state.usage?.cache, { cacheRead: 100, cacheWrite: 8, totalInputTokens: 122 });
    assert.equal(state.usage?.cost, 0.0001, 'cost is cumulative, so a payload without cost keeps the last value');
  });
});
