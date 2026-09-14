import { Cpu, Folder, SlidersHorizontal } from 'lucide-react';

import { t } from '@/core/i18n';
import { currentModelLabel, hasFeature } from '@/core/state';
import { useAppState } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { basename, cn } from '@/lib/utils';

// 状态栏:22px。连接状态点 + 版本 + 用量/模式/模型/工作目录投影。
export function StatusBar() {
  const appState = useAppState();
  const background = useAppBackground();

  const conn = appState.connection;
  const dotClass =
    conn.state === 'ready'
      ? 'bg-success'
      : conn.state === 'starting' || conn.state === 'restarting'
        ? 'bg-warning'
        : conn.state === 'error'
          ? 'bg-danger'
          : 'bg-faint';
  const connLabel = t(
    conn.state === 'ready'
      ? 'conn.ready'
      : conn.state === 'starting'
        ? 'conn.starting'
        : conn.state === 'restarting'
          ? 'conn.restarting'
          : conn.state === 'error'
            ? 'conn.error'
            : 'conn.stopped',
  );

  // 已打开的会话优先显示它自己的工作目录;没有会话时显示下一次新建任务的
  // 候选目录。connection.workspace 只是 ACP 进程目录,不能作为任一会话目录。
  const workspace = appState.activeSessionCwd || appState.newSessionCwd || appState.store.lastWorkspace || '…';
  const usage = appState.usage;
  const showUsage = usage && usage.used > 0;
  const percent = showUsage && usage.size > 0 ? Math.round((usage.used / usage.size) * 100) : 0;
  const cost = showUsage && usage.cost ? ` · $${usage.cost.toFixed(4)}` : '';
  // 提示词缓存只显示 Runtime 已经算好的累计量,且必须等 ACP 广告对应能力;
  // 这里不再拍另一个分母,以免与服务端/TUI 的命中率口径分叉。
  const cache = showUsage && hasFeature('usageCacheProjection') ? usage.cache : null;
  const cachePercent =
    cache && cache.totalInputTokens > 0 ? Math.min(100, Math.round((cache.cacheRead / cache.totalInputTokens) * 100)) : 0;
  const cacheTitle = cache
    ? t('status.cacheTip', { read: cache.cacheRead, write: cache.cacheWrite, total: cache.totalInputTokens })
    : undefined;

  return (
    <footer
      className={cn(
        'app-layer flex h-[var(--statusbar-h)] shrink-0 items-center justify-between border-t border-border bg-statusbar px-3 text-[11px] text-muted-foreground select-none',
        background.app && 'app-surface-veil border-t-transparent'
      )}
    >
      <div className="flex min-w-0 items-center gap-3">
        <span className="flex items-center gap-1 whitespace-nowrap">
          <span className={cn('size-1.5 shrink-0 rounded-full', dotClass)} />
          <span>{connLabel}</span>
        </span>
        <span className="whitespace-nowrap">MothX Desktop v{appState.appInfo.version} · ACP</span>
      </div>
      <div className="flex min-w-0 items-center gap-3">
        {showUsage ? (
          <span className="whitespace-nowrap" title={cacheTitle}>
            ctx {usage.used}
            {usage.size ? `/${usage.size}` : ''}
            {percent ? ` (${percent}%)` : ''}
            {cost}
            {cachePercent ? ` · ${t('status.cache', { p: cachePercent })}` : ''}
          </span>
        ) : null}
        <span className="flex items-center gap-1 whitespace-nowrap">
          <SlidersHorizontal className="size-3.5" />
          <span>{appState.currentMode || '…'}</span>
        </span>
        <span className="flex max-w-[260px] items-center gap-1">
          <Cpu className="size-3.5 shrink-0" />
          <span className="truncate">{currentModelLabel()}</span>
        </span>
        <span className="flex max-w-[220px] items-center gap-1">
          <Folder className="size-3.5 shrink-0" />
          <span className="truncate">{basename(workspace)}</span>
        </span>
      </div>
    </footer>
  );
}
