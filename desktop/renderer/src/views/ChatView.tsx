// 聊天/任务视图:头部(状态 chip + 标题 + 分叉/重命名/删除)、转录流与
// 底部 composer。

import { useEffect, useMemo, useState } from 'react';
import { Check, ChevronDown, ChevronUp, List, Pencil, ServerCog, Share2, Trash2 } from 'lucide-react';

import { ChatStream } from '@/components/ChatStream';
import { Composer } from '@/components/Composer';
import { StatusChip } from '@/components/StatusChip';
import { WorktreeMenu } from '@/components/WorktreeMenu';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { t } from '@/core/i18n';
import { deleteSession, forkSession, renameSession } from '@/core/sessions';
import { activeRunStatus, hasFeature } from '@/core/state';
import { STATUS_VARIANT, type PlanItem } from '@/core/transcript';
import { worktreeSupported } from '@/core/worktrees';
import { useAppBackground } from '@/hooks/useAppBackground';
import { useAppState } from '@/hooks/useAppState';
import { McpPanel } from '@/views/settings/McpPanel';
import { cn } from '@/lib/utils';

export function ChatView() {
  const appState = useAppState();
  const background = useAppBackground();
  const [projectMcpOpen, setProjectMcpOpen] = useState(false);
  const [planCollapsed, setPlanCollapsed] = useState(false);
  const projectMcpEnabled = Boolean(appState.activeSessionId) && hasFeature('manageMcp');
  // 隔离工作区入口:已有对话后放在头部右上角,不再占用 composer 工具栏。
  const worktreeEnabled = Boolean(appState.activeSessionId) && worktreeSupported();
  const activePlan = useMemo(
    () => [...appState.transcript].reverse().find((item): item is PlanItem => item.kind === 'plan' && item.entries.length > 0),
    [appState.transcript],
  );

  useEffect(() => {
    setPlanCollapsed(false);
  }, [appState.activeSessionId, activePlan?.key]);

  return (
    <section className="absolute inset-0 flex flex-col">
      <header
        className={cn(
          'flex h-[46px] shrink-0 items-center gap-2.5 border-b border-border px-4',
          background.app && 'border-b-transparent bg-transparent'
        )}
      >
        <StatusChip variant={STATUS_VARIANT[activeRunStatus()] || 'idle'} label={t(`status.${activeRunStatus()}`)} />
        <div className="min-w-0 flex-1 truncate text-[13.5px] font-semibold text-strong">
          {appState.activeTitle || t('chat.newTask')}
        </div>
        <div className="flex gap-0.5">
          {worktreeEnabled ? <WorktreeMenu variant="header" /> : null}
          {projectMcpEnabled ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t('chat.projectMcp')}
                  onClick={() => setProjectMcpOpen(true)}
                >
                  <ServerCog />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t('chat.projectMcp')}</TooltipContent>
            </Tooltip>
          ) : null}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t('chat.fork')}
                onClick={() => {
                  if (appState.activeSessionId) void forkSession(appState.activeSessionId);
                }}
              >
                <Share2 />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('chat.fork')}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t('chat.rename')}
                onClick={() => {
                  if (appState.activeSessionId) void renameSession(appState.activeSessionId);
                }}
              >
                <Pencil />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('chat.rename')}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t('chat.delete')}
                onClick={() => {
                  if (appState.activeSessionId) void deleteSession(appState.activeSessionId);
                }}
              >
                <Trash2 />
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t('chat.delete')}</TooltipContent>
          </Tooltip>
        </div>
      </header>

      <ChatStream />

      {activePlan ? (
        <PlanProgress plan={activePlan} collapsed={planCollapsed} onToggle={() => setPlanCollapsed((value) => !value)} />
      ) : null}

      <footer
        className={cn(
          'shrink-0 border-t border-border bg-background pt-2.5 pb-3.5',
          background.app && 'border-t-transparent bg-transparent'
        )}
      >
        <div className="mx-auto w-[min(820px,92%)]">
          <Composer source="chat" />
        </div>
      </footer>

      <Dialog open={projectMcpOpen} onOpenChange={setProjectMcpOpen}>
        <DialogContent className="w-[min(680px,92vw)] max-h-[88vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t('chat.projectMcpTitle')}</DialogTitle>
            <DialogDescription>{t('chat.projectMcpDesc')}</DialogDescription>
          </DialogHeader>
          <McpPanel scope="project" sessionId={appState.activeSessionId || undefined} />
        </DialogContent>
      </Dialog>
    </section>
  );
}

function PlanProgress({ plan, collapsed, onToggle }: { plan: PlanItem; collapsed: boolean; onToggle: () => void }) {
  const completed = plan.entries.filter((entry) => entry.status === 'completed').length;
  const total = plan.entries.length;
  const percent = total ? Math.round((completed / total) * 100) : 0;
  const label = plan.title || t('chat.planProgress');
  const ToggleIcon = collapsed ? ChevronDown : ChevronUp;

  return (
    <aside
      className={cn(
        'absolute top-[58px] right-4 z-20 hidden max-h-[calc(100%-170px)] w-72 flex-col overflow-hidden rounded-xl border border-border bg-card shadow-overlay xl:flex',
        collapsed && 'w-auto'
      )}
      aria-label={t('chat.planProgress')}
    >
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-hoverbg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        aria-expanded={!collapsed}
        aria-label={collapsed ? t('chat.planExpand') : t('chat.planCollapse')}
        onClick={onToggle}
      >
        <List className="size-4 shrink-0 text-primary" aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-[12px] font-semibold text-strong">{label}</span>
        <span className="shrink-0 text-[11px] text-muted-foreground">{t('chat.planProgressCount', { done: completed, total })}</span>
        <ToggleIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      </button>
      {!collapsed ? (
        <div className="flex min-h-0 flex-col border-t border-border px-3 pt-2.5 pb-3">
          {plan.note ? <p className="mb-2 text-[11px] leading-snug text-muted-foreground">{plan.note}</p> : null}
          <div
            className="h-2 overflow-hidden rounded-full bg-muted"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={percent}
            aria-label={t('chat.planProgressCount', { done: completed, total })}
          >
            <div className="h-full rounded-full bg-primary transition-[width] duration-300" style={{ width: `${percent}%` }} />
          </div>
          <ol className="mt-2.5 flex min-h-0 flex-col gap-2 overflow-y-auto pr-1" aria-label={t('chat.plan', { n: total })}>
            {plan.entries.map((entry, index) => {
              const done = entry.status === 'completed';
              const working = entry.status === 'in_progress';
              const failed = entry.status === 'failed';
              return (
                <li
                  key={index}
                  className={cn(
                    'flex items-start gap-2 text-[11.5px] leading-snug',
                    done && 'text-foreground',
                    working && 'font-semibold text-primary',
                    failed && 'text-danger',
                    !done && !working && !failed && 'text-muted-foreground'
                  )}
                  aria-label={entry.content}
                >
                  <span
                    className={cn(
                      'mt-px flex size-4 shrink-0 items-center justify-center rounded-full border transition-all',
                      done && 'border-primary bg-primary text-primary-foreground',
                      working && 'border-primary',
                      failed && 'border-danger',
                      !done && !working && !failed && 'border-borderstrong'
                    )}
                    aria-hidden="true"
                  >
                    {done ? <Check className="size-2.5" /> : null}
                    {working ? <span className="size-1.5 animate-pulse rounded-full bg-primary" /> : null}
                  </span>
                  <span className="min-w-0 break-words">{entry.content}</span>
                </li>
              );
            })}
          </ol>
        </div>
      ) : null}
    </aside>
  );
}
