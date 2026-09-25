// Worktree 领域逻辑:隔离工作区的 ACP 投影、就绪等待与生命周期动作。
// renderer 不执行 git、不直连文件系统:创建/删除/重置都经 mothx/worktree/*，
// 目录授权由 Runtime 完成。就绪状态由 mothx/worktree/status 通知驱动。

import { invoke, type WorktreeStatusPayload } from './api';
import { hasFeature } from './state';

export interface WorktreeShape {
  id?: string;
  name?: string;
  branch?: string;
  directory: string;
  repositoryRoot?: string;
  projectId?: string;
  status?: string;
  error?: string;
  external?: boolean;
}

export type WorktreeReadiness =
  | { status: 'pending' }
  | { status: 'ready' }
  | { status: 'failed'; message: string };

const readiness = new Map<string, WorktreeReadiness>();
const waiters = new Map<string, (value: WorktreeReadiness) => void>();

function key(directory: string): string {
  return directory.replace(/[\\/]+$/, '');
}

function settle(id: string, value: WorktreeReadiness): void {
  readiness.set(id, value);
  const resolve = waiters.get(id);
  if (resolve) {
    waiters.delete(id);
    resolve(value);
  }
}

// applyWorktreeStatus folds one mothx/worktree/status notification into the
// in-memory readiness projection and resolves any pending waiter.
export function applyWorktreeStatus(payload: WorktreeStatusPayload): void {
  if (!payload || typeof payload.directory !== 'string') return;
  const id = key(payload.directory);
  switch (payload.status) {
    case 'ready':
      settle(id, { status: 'ready' });
      return;
    case 'failed':
      settle(id, { status: 'failed', message: payload.error || 'worktree failed' });
      return;
    case 'pending':
      if (!readiness.has(id)) readiness.set(id, { status: 'pending' });
      return;
    case 'removed':
      readiness.delete(id);
      return;
    default:
      return;
  }
}

export function worktreeReadiness(directory: string): WorktreeReadiness | undefined {
  return readiness.get(key(directory));
}

// waitForWorktree resolves once the worktree reaches a terminal readiness state
// (ready or failed). A pending worktree is awaited through a shared waiter.
export function waitForWorktree(directory: string): Promise<WorktreeReadiness> {
  const id = key(directory);
  const current = readiness.get(id);
  if (current && current.status !== 'pending') return Promise.resolve(current);
  return new Promise((resolve) => {
    waiters.set(id, resolve);
  });
}

export function worktreeSupported(): boolean {
  return hasFeature('worktrees');
}

export async function listWorktrees(repositoryRoot?: string): Promise<WorktreeShape[]> {
  if (!worktreeSupported()) return [];
  const params = repositoryRoot ? { repositoryRoot } : {};
  const result = await invoke<{ worktrees?: WorktreeShape[] }>('mothx/worktree/list', params);
  return result.worktrees || [];
}

export async function createWorktree(input: { baseCwd: string; name?: string; detached?: boolean }): Promise<WorktreeShape> {
  if (!worktreeSupported()) throw new Error('worktrees are unavailable');
  const result = await invoke<{ worktree?: WorktreeShape }>('mothx/worktree/create', { ...input });
  const worktree = result.worktree;
  if (!worktree || typeof worktree.directory !== 'string' || worktree.directory === '') {
    throw new Error('worktree create returned no directory');
  }
  readiness.set(key(worktree.directory), { status: 'pending' });
  return worktree;
}

export async function removeWorktree(input: { id?: string; directory?: string; force?: boolean }): Promise<void> {
  if (!worktreeSupported()) throw new Error('worktrees are unavailable');
  await invoke('mothx/worktree/remove', { ...input });
  if (input.directory) readiness.delete(key(input.directory));
}

export async function resetWorktree(input: { id?: string; directory?: string }): Promise<void> {
  if (!worktreeSupported()) throw new Error('worktrees are unavailable');
  await invoke('mothx/worktree/reset', { ...input });
}
