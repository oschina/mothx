// 投递失败数据动作:ACP 投影的持久化投递状态（mothx/manage/deliveries/*）。
// Runtime 拥有投递操作行,renderer 只投影失败列表、并允许重开一次传输级失败;
// 任何投递事实都不落 Desktop 本地存储。

import { invoke } from './api';
import { guard } from './manage-api';
import { hasFeature } from './state';

export const DELIVERY_LIST_METHOD = 'mothx/manage/deliveries/list';
export const DELIVERY_RETRY_METHOD = 'mothx/manage/deliveries/retry';
export const DELIVERY_FEATURE = 'manageDeliveries';

// 失败的投递操作投影。字段与 ACP list 的响应一一对应;status 为
// failed/uncertain 之外的值时该操作不需要运维介入。
export interface DeliveryFailureView {
  operationId: string;
  intentId?: string;
  sessionId?: string;
  runId?: string;
  platform?: string;
  targetId?: string;
  operationKind?: string;
  status?: string;
  failureCode?: string;
  attemptCount?: number;
  updatedAt?: string;
  retryable?: boolean;
}

// canRetryDelivery 与 ACP 重试入口的拒绝规则一致:只有 failed 且失败码属于
// 传输级(可重开)的操作才可重试。进行中、已投递、永久失败(平台 4xx、不支持的
// 媒体类型)都必须保持原状,否则只会重复同一次失败或打断进行中的投递。
export function canRetryDelivery(failure: DeliveryFailureView | undefined | null): boolean {
  return !!failure && failure.status === 'failed' && failure.retryable === true;
}

// isDeliveryFailureTransient 投影 failure_code 的类别,供面板区分“可重试的
// 传输失败”与“永久失败”,不参与任何授权判断(授权始终由 Runtime fence 决定)。
export function isDeliveryFailureTransient(failure: DeliveryFailureView | undefined | null): boolean {
  return !!failure && failure.failureCode === 'transport_error';
}

export async function loadDeliveries(sessionId?: string): Promise<DeliveryFailureView[] | undefined> {
  if (!hasFeature(DELIVERY_FEATURE)) return undefined;
  const view = await guard<{ deliveries?: DeliveryFailureView[] } | undefined>(
    DELIVERY_FEATURE,
    () => invoke<{ deliveries?: DeliveryFailureView[] }>(DELIVERY_LIST_METHOD, sessionId ? { sessionId } : {}),
    undefined,
  );
  return view ? view.deliveries || [] : undefined;
}

// retryDelivery 请求 Runtime 重开该操作。永久失败、非 failed 状态或已不存在的
// 操作会被 ACP 入口拒绝,错误信息原样返回给调用方展示。
export async function retryDelivery(operationId: string): Promise<boolean> {
  const result = await invoke<{ retried?: boolean }>(DELIVERY_RETRY_METHOD, { operationId });
  return result?.retried === true;
}
