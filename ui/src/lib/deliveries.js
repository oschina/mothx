// Failed durable delivery operations (the Runtime-owned outbox). The WebUI only
// projects the failures and may ask for one transport-level failure to be
// reopened; delivery facts stay in the session store.

import { postJSON, request } from './api.js';

export const DELIVERY_FAILURES_PATH = '/api/deliveries/failures';
export const DELIVERY_RETRY_PATH = '/api/deliveries/retry';

// canRetryDelivery mirrors the retry entry's own refusal rule: only a failed
// transport-level operation may be reopened. In-flight, delivered, or
// permanently failed operations must stay as they are.
export function canRetryDelivery(failure) {
  return Boolean(failure) && failure.status === 'failed' && failure.retryable === true;
}

// isDeliveryFailureTransient classifies the failure code for display only; the
// authorization to reopen always belongs to the Runtime fence.
export function isDeliveryFailureTransient(failure) {
  return Boolean(failure) && failure.failureCode === 'transport_error';
}

// deliveryFailureLabel summarizes one operation for a compact list row.
export function deliveryFailureLabel(failure) {
  if (!failure) return '';
  return [failure.platform, failure.operationKind, failure.status].filter(Boolean).join(' · ');
}

export async function listDeliveryFailures(sessionId = '', limit = 0) {
  const params = new URLSearchParams();
  if (sessionId) params.set('session_id', sessionId);
  if (Number.isFinite(limit) && limit > 0) params.set('limit', String(limit));
  const query = params.toString();
  const data = await request(query ? `${DELIVERY_FAILURES_PATH}?${query}` : DELIVERY_FAILURES_PATH);
  return Array.isArray(data?.deliveries) ? data.deliveries : [];
}

export async function retryDelivery(operationId) {
  const data = await postJSON(DELIVERY_RETRY_PATH, { operationId });
  return data?.retried === true;
}
