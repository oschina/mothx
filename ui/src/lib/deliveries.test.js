import test from 'node:test';
import assert from 'node:assert/strict';

import {
  DELIVERY_FAILURES_PATH,
  DELIVERY_RETRY_PATH,
  canRetryDelivery,
  deliveryFailureLabel,
  isDeliveryFailureTransient,
  listDeliveryFailures,
  retryDelivery
} from './deliveries.js';

function mockFetch(t, implementation) {
  const original = globalThis.fetch;
  globalThis.fetch = implementation;
  t.after(() => { globalThis.fetch = original; });
}

test('listDeliveryFailures scopes the query to one session and parses the projection', async (t) => {
  let seen = '';
  mockFetch(t, async (url) => {
    seen = String(url);
    return new Response(JSON.stringify({
      deliveries: [{ operationId: 'op-1', platform: 'wechat', status: 'failed', failureCode: 'transport_error', retryable: true }],
      count: 1
    }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });

  const failures = await listDeliveryFailures('session-1', 25);
  assert.equal(seen, `${DELIVERY_FAILURES_PATH}?session_id=session-1&limit=25`);
  assert.equal(failures.length, 1);
  assert.equal(failures[0].operationId, 'op-1');
  assert.equal(DELIVERY_FAILURES_PATH, '/api/deliveries/failures');
});

test('listDeliveryFailures omits empty filters and tolerates a missing list', async (t) => {
  let seen = '';
  mockFetch(t, async (url) => {
    seen = String(url);
    return new Response(JSON.stringify({}), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  assert.deepEqual(await listDeliveryFailures(), []);
  assert.equal(seen, DELIVERY_FAILURES_PATH);
});

test('retryDelivery posts the operation id and reports the Runtime verdict', async (t) => {
  let body = '';
  let method = '';
  mockFetch(t, async (_url, options) => {
    method = options.method;
    body = String(options.body);
    return new Response(JSON.stringify({ operationId: 'op-1', retried: true }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  assert.equal(await retryDelivery('op-1'), true);
  assert.equal(method, 'POST');
  assert.equal(body, JSON.stringify({ operationId: 'op-1' }));
  assert.equal(DELIVERY_RETRY_PATH, '/api/deliveries/retry');
});

test('only a failed transport-level operation is offered a retry', () => {
  assert.equal(canRetryDelivery({ status: 'failed', failureCode: 'transport_error', retryable: true }), true);
  assert.equal(isDeliveryFailureTransient({ status: 'failed', failureCode: 'transport_error', retryable: true }), true);
  assert.equal(canRetryDelivery({ status: 'failed', failureCode: 'unsupported_media_kind', retryable: false }), false);
  assert.equal(canRetryDelivery({ status: 'failed', failureCode: 'delivery_retries_exhausted', retryable: true }), true);
  assert.equal(canRetryDelivery({ status: 'sending', retryable: true }), false);
  assert.equal(canRetryDelivery({ status: 'uncertain', retryable: false }), false);
  assert.equal(canRetryDelivery(undefined), false);
  assert.equal(deliveryFailureLabel({ platform: 'wechat', operationKind: 'send_text', status: 'failed' }), 'wechat · send_text · failed');
  assert.equal(deliveryFailureLabel(null), '');
});
