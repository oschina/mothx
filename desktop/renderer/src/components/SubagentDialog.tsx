// 子代理只读面板:列表/状态来自 Runtime 的 ACP 投影,对话内容复用普通
// TranscriptEntry。这里不提供发送、取消或创建入口。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { AlertCircle, Loader2, RefreshCw, Users } from 'lucide-react';

import { TranscriptEntry } from '@/components/TranscriptItems';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { t } from '@/core/i18n';
import {
  fetchSubagentTranscript,
  isSubagentRunning,
  mergeSubagentTranscript,
  refreshSubagents,
  sessionSubagents,
  subagentLabel,
  subagentStatusKind,
  subagentSummary,
  type SubagentStatusKind,
} from '@/core/subagents';
import type { TranscriptItem } from '@/core/state';
import { useAppState } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

const STATUS_DOT: Record<SubagentStatusKind, string> = {
  running: 'bg-info',
  completed: 'bg-success',
  failed: 'bg-danger',
  cancelled: 'bg-muted-foreground',
  incomplete: 'bg-warning',
};

const STATUS_BADGE: Record<SubagentStatusKind, string> = {
  running: 'bg-info-soft text-info',
  completed: 'bg-success-soft text-success',
  failed: 'bg-danger-soft text-danger',
  cancelled: 'bg-placeholder text-muted-foreground',
  incomplete: 'bg-warning-soft text-warning',
};

export function subagentStatusLabel(status: string): string {
  return t(`chat.subagents.status.${subagentStatusKind(status)}`);
}

export function SubagentSummaryButton({ sessionId, onClick }: { sessionId: string | null; onClick: () => void }) {
  const appState = useAppState();
  const list = sessionSubagents(sessionId);
  if (!appState.activeSessionId || list.length === 0) return null;
  const summary = subagentSummary(list);
  const label = summary.running > 0
    ? t('chat.subagents.running', { running: summary.running, total: summary.total })
    : t('chat.subagents.total', { total: summary.total });
  return (
    <Button
      variant="ghost"
      size="sm"
      className="gap-1.5 px-2 text-[11.5px] text-muted-foreground"
      aria-label={t('chat.subagents.open')}
      title={t('chat.subagents.open')}
      onClick={onClick}
    >
      <Users className="size-3.5 shrink-0 text-expert" aria-hidden="true" />
      <span className="hidden max-w-[150px] truncate sm:inline">{label}</span>
      <span className="sm:hidden">{summary.total}</span>
      {summary.failed > 0 ? <span className="size-1.5 rounded-full bg-danger" aria-hidden="true" /> : null}
    </Button>
  );
}

export function SubagentDialog({
  open,
  onOpenChange,
  sessionId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sessionId: string | null;
}) {
  const list = sessionSubagents(sessionId);
  const [selectedId, setSelectedId] = useState('');
  const [items, setItems] = useState<TranscriptItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const requestRef = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickRef = useRef(true);

  const selected = useMemo(
    () => list.find((info) => info.agentId === selectedId) || list[0] || null,
    [list, selectedId],
  );
  const selectedStatus = selected ? subagentStatusKind(selected.status) : '';
  const running = Boolean(selected && isSubagentRunning(selected));

  const load = useCallback(async (agentId: string) => {
    if (!sessionId || !agentId) return;
    const request = requestRef.current + 1;
    requestRef.current = request;
    setLoading(true);
    setError('');
    try {
      const next = await fetchSubagentTranscript(sessionId, agentId);
      if (requestRef.current !== request) return;
      setItems((previous) => mergeSubagentTranscript(previous, next));
    } catch (cause) {
      if (requestRef.current !== request) return;
      setError(t('chat.subagents.loadFailed', { e: cause instanceof Error ? cause.message : String(cause) }));
    } finally {
      if (requestRef.current === request) setLoading(false);
    }
  }, [sessionId]);

  useEffect(() => {
    if (!open) return;
    void refreshSubagents(sessionId);
  }, [open, sessionId]);

  useEffect(() => {
    if (!open) return;
    if (list.length === 0) {
      if (selectedId) setSelectedId('');
      setItems([]);
      return;
    }
    if (!list.some((info) => info.agentId === selectedId)) setSelectedId(list[0].agentId);
  }, [open, list, selectedId]);

  // A different child owns a different transcript; never show the previous
  // child's messages while the next read is in flight.
  useEffect(() => {
    setItems([]);
  }, [open, selected?.agentId]);

  // Reload when the selected child changes or reaches a new terminal state.
  useEffect(() => {
    if (!open || !selected?.agentId) return;
    void load(selected.agentId);
  }, [open, selected?.agentId, selectedStatus, load]);

  // A running child has no separate stream in Desktop; poll the canonical
  // read-only projection at a gentle cadence while its panel is visible.
  useEffect(() => {
    if (!open || !selected?.agentId || !running) return;
    const timer = window.setInterval(() => {
      void load(selected.agentId);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [open, selected?.agentId, running, load]);

  useEffect(() => {
    const node = scrollRef.current;
    if (node && stickRef.current) node.scrollTop = node.scrollHeight;
  }, [items]);

  useEffect(() => {
    stickRef.current = true;
  }, [open, selected?.agentId]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[min(680px,86vh)] w-[min(920px,94vw)] max-w-none grid-rows-none flex-col gap-0 overflow-hidden p-0">
        <DialogHeader className="shrink-0 gap-1 border-b border-border px-4 py-3 pr-12">
          <div className="flex items-center gap-2">
            <Users className="size-4 text-expert" aria-hidden="true" />
            <DialogTitle>{t('chat.subagents.title')}</DialogTitle>
            <Badge variant="outline" className="text-[10px] font-semibold text-muted-foreground">
              {t('chat.subagents.readonly')}
            </Badge>
            <span className="ml-auto text-[11.5px] text-muted-foreground">
              {t('chat.subagents.subtitle', { n: list.length })}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t('chat.subagents.refresh')}
              title={t('chat.subagents.refresh')}
              onClick={() => {
                if (sessionId) void refreshSubagents(sessionId);
                if (selected?.agentId) void load(selected.agentId);
              }}
            >
              <RefreshCw className={cn(loading && 'animate-spin')} />
            </Button>
          </div>
          <DialogDescription className="truncate">
            {selected ? subagentLabel(selected) : t('chat.subagents.empty')}
            {selected?.parentAgentId ? ` · ${selected.parentAgentId}` : ''}
          </DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1">
          <aside className="flex w-[220px] shrink-0 flex-col overflow-y-auto border-r border-border p-1.5" aria-label={t('chat.subagents.title')}>
            {list.length === 0 ? (
              <p className="px-2 py-3 text-[11.5px] text-muted-foreground">{t('chat.subagents.empty')}</p>
            ) : (
              list.map((info) => {
                const status = subagentStatusKind(info.status);
                const active = selected?.agentId === info.agentId;
                return (
                  <button
                    key={info.agentId}
                    type="button"
                    className={cn(
                      'flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-[12px] transition-colors',
                      active ? 'bg-primary/8 text-strong' : 'text-muted-foreground hover:bg-hoverbg hover:text-strong',
                    )}
                    aria-current={active ? 'true' : undefined}
                    onClick={() => setSelectedId(info.agentId)}
                  >
                    <span className={cn('size-1.5 shrink-0 rounded-full', STATUS_DOT[status], status === 'running' && 'animate-pulse')} aria-hidden="true" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-semibold">{subagentLabel(info)}</span>
                      <span className="block truncate text-[10.5px] text-faint">
                        {[subagentStatusLabel(info.status), info.messageCount ? t('chat.subagents.messages', { n: info.messageCount }) : '']
                          .filter(Boolean)
                          .join(' · ')}
                      </span>
                    </span>
                    <span className={cn('shrink-0 rounded-md px-1.5 py-px text-[9.5px] font-semibold', STATUS_BADGE[status])}>
                      {subagentStatusLabel(info.status)}
                    </span>
                  </button>
                );
              })
            )}
          </aside>

          <section
            ref={scrollRef}
            className="min-w-0 flex-1 overflow-y-auto px-4 py-4"
            onScroll={() => {
              const node = scrollRef.current;
              if (node) stickRef.current = node.scrollHeight - node.scrollTop - node.clientHeight < 120;
            }}
          >
            {selected?.error ? (
              <div className="mb-3 rounded-lg border border-danger bg-danger-soft px-3 py-2 text-[12px] text-danger">
                <div className="flex items-center gap-1.5 font-semibold">
                  <AlertCircle className="size-3.5" />
                  <span>{t('chat.subagents.error')}</span>
                </div>
                <p className="mt-1 break-words whitespace-pre-wrap text-foreground">{selected.error}</p>
              </div>
            ) : null}
            {error ? (
              <div className="flex flex-col items-start gap-2 text-[12px] text-danger">
                <p>{error}</p>
                <Button variant="outline" size="sm" onClick={() => selected && void load(selected.agentId)}>
                  {t('chat.subagents.retry')}
                </Button>
              </div>
            ) : loading && items.length === 0 ? (
              <p className="flex items-center gap-2 text-[12px] text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin text-primary" />
                {t('chat.subagents.loading')}
              </p>
            ) : items.length === 0 ? (
              <p className="text-[12px] text-muted-foreground">{t('chat.subagents.transcriptEmpty')}</p>
            ) : (
              <div className="mx-auto flex w-[min(760px,100%)] flex-col gap-4">
                {items.map((item) => (
                  <TranscriptEntry key={item.key} item={item} />
                ))}
              </div>
            )}
          </section>
        </div>
      </DialogContent>
    </Dialog>
  );
}
