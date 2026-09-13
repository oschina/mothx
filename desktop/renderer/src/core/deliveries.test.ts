import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

import { DELIVERY_FEATURE, DELIVERY_LIST_METHOD, DELIVERY_RETRY_METHOD, canRetryDelivery, isDeliveryFailureTransient } from './deliveries.ts';
import type { DeliveryFailureView } from './deliveries.ts';

const here = dirname(fileURLToPath(import.meta.url));
const deliveriesSource = readFileSync(join(here, 'deliveries.ts'), 'utf8');
const channelsPanelSource = readFileSync(join(here, '..', 'views', 'settings', 'ChannelsPanel.tsx'), 'utf8');
const translations = readFileSync(join(here, 'i18n.ts'), 'utf8');

test('delivery failures load and retry through the canonical ACP methods', () => {
  assert.equal(DELIVERY_LIST_METHOD, 'mothx/manage/deliveries/list');
  assert.equal(DELIVERY_RETRY_METHOD, 'mothx/manage/deliveries/retry');
  assert.equal(DELIVERY_FEATURE, 'manageDeliveries');
  assert.match(deliveriesSource, /invoke<.*>\(DELIVERY_LIST_METHOD/);
  assert.match(deliveriesSource, /invoke<.*>\(DELIVERY_RETRY_METHOD/);
  // The panel must gate the whole card on the advertised ACP feature key.
  assert.match(channelsPanelSource, /hasFeature\(DELIVERY_FEATURE\)/);
  // ...and must project the retry rule instead of offering a retry on every row.
  assert.match(channelsPanelSource, /disabled=\{retrying !== null \|\| !canRetryDelivery\(failure\)\}/);
});

test('only a failed transport-level operation may be retried', () => {
  const failedTransient: DeliveryFailureView = { operationId: 'op-1', status: 'failed', failureCode: 'transport_error', retryable: true };
  assert.equal(canRetryDelivery(failedTransient), true);
  assert.equal(isDeliveryFailureTransient(failedTransient), true);

  const failedPermanent: DeliveryFailureView = { operationId: 'op-2', status: 'failed', failureCode: 'unsupported_media_kind', retryable: false };
  assert.equal(canRetryDelivery(failedPermanent), false);
  assert.equal(isDeliveryFailureTransient(failedPermanent), false);

  const exhausted: DeliveryFailureView = { operationId: 'op-3', status: 'failed', failureCode: 'delivery_retries_exhausted', retryable: true };
  assert.equal(canRetryDelivery(exhausted), true);
  assert.equal(isDeliveryFailureTransient(exhausted), false);

  // In-flight, delivered, and uncertain operations stay untouched, and a missing
  // projection is never treated as retryable.
  assert.equal(canRetryDelivery({ operationId: 'op-4', status: 'sending', retryable: true }), false);
  assert.equal(canRetryDelivery({ operationId: 'op-5', status: 'delivered', retryable: true }), false);
  assert.equal(canRetryDelivery({ operationId: 'op-6', status: 'uncertain', retryable: false }), false);
  assert.equal(canRetryDelivery(undefined), false);
  assert.equal(canRetryDelivery(null), false);
});

test('the deliveries card is bilingual', () => {
  const keys = [
    'settings.channelsDeliveries',
    'settings.channelsDeliveriesDesc',
    'settings.channelsDeliveriesEmpty',
    'settings.channelsDeliveriesLoading',
    'settings.channelsDeliveriesUnavailable',
    'settings.channelsDeliveriesRefresh',
    'settings.channelsDeliveriesRetry',
    'settings.channelsDeliveriesRetrying',
    'settings.channelsDeliveriesRetried',
    'settings.channelsDeliveriesNotRetried',
    'settings.channelsDeliveriesAttempts',
    'settings.channelsDeliveriesPermanent',
  ];
  for (const key of keys) {
    const occurrences = translations.split(`'${key}'`).length - 1;
    assert.equal(occurrences, 2, `${key} must exist in both zh and en dictionaries`);
    assert.match(channelsPanelSource, new RegExp(key.replaceAll('.', '\\.')), `panel must use ${key}`);
  }
});
