// 转录条目组件:气泡/思考块/工具卡/计划卡/制品卡/审批卡/提问卡/状态行/
// 子代理卡/错误卡。展开(open)状态保存在条目对象上,流式更新不覆盖用户
// 的展开选择;审批/提问倒计时由 useNow 驱动。

import { useLayoutEffect, useRef, useState } from 'react';
import {
  AlertCircle,
  Check,
  ChevronRight,
  Copy,
  Cpu,
  FileText,
  Globe,
  HelpCircle,
  Image as ImageIcon,
  List,
  Loader2,
  Pencil,
  Search,
  Share2,
  Shield,
  Sparkles,
  Terminal,
  Trash2,
  Users,
} from 'lucide-react';

import { t } from '@/core/i18n';
import { emit, type TranscriptItem } from '@/core/state';
import {
  buildDiffLines,
  cancelPermission,
  cancelQuestion,
  deadlineText,
  formatBytes,
  openArtifact,
  planStepClass,
  prettyInput,
  resolvePermission,
  resolveQuestion,
  statusLabel,
  toolSummary,
  type ArtifactItem,
  type DecisionItem,
  type PlanItem,
  type QuestionItem,
  type PermissionItem,
  type ToolItem,
} from '@/core/transcript';
import { toast } from '@/core/ui-host';
import { useNow } from '@/hooks/useNow';
import { cn } from '@/lib/utils';
import { MothxLogo } from '@/components/MothxLogo';
import { Markdown } from '@/components/Markdown';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

const TOOL_ICONS: Record<string, typeof Cpu> = {
  read: FileText,
  edit: Pencil,
  search: Search,
  execute: Terminal,
  delete: Trash2,
  move: Share2,
  think: Sparkles,
  fetch: Globe,
  image: ImageIcon,
  plan: List,
  other: Cpu,
};

const TOOL_BADGE: Record<string, string> = {
  pending: 'bg-placeholder text-muted-foreground',
  in_progress: 'bg-info-soft text-info',
  completed: 'bg-success-soft text-success',
  failed: 'bg-danger-soft text-danger',
};

export function UserBubble({ item }: { item: Extract<TranscriptItem, { kind: 'user' }> }) {
  return (
    <div className="flex animate-in fade-in slide-in-from-bottom-1.5 justify-end duration-300">
      <div className="max-w-[78%] rounded-[12px_12px_3px_12px] bg-primary/8 px-3.5 py-2 text-[13px] leading-[1.65] break-words whitespace-pre-wrap text-strong">
        {item.text}
      </div>
    </div>
  );
}

export function AgentMessage({ item }: { item: Extract<TranscriptItem, { kind: 'agent' }> }) {
  return (
    <div className="flex animate-in fade-in slide-in-from-bottom-1.5 gap-2.5 duration-300">
      <span className="mt-0.5 size-7 shrink-0 overflow-hidden rounded-lg">
        <MothxLogo />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-2.5">
        <Markdown text={item.text} />
        <div className="flex gap-0.5">
          <Button
            variant="ghost"
            size="icon"
            title={t('chat.copied')}
            aria-label={t('chat.copied')}
            onClick={() => {
              void navigator.clipboard.writeText(item.text).then(() => toast(t('chat.copied')));
            }}
          >
            <Copy />
          </Button>
        </div>
      </div>
    </div>
  );
}

function thoughtPreview(text: string): string {
  const tail = text.length > 600 ? text.slice(-600) : text;
  const compact = tail.replace(/\s+/g, ' ').trim();
  return text.length > 600 ? `…${compact}` : compact;
}

export function ThoughtBlock({ item, live = false }: { item: Extract<TranscriptItem, { kind: 'thought' }>; live?: boolean }) {
  const previewRef = useRef<HTMLSpanElement>(null);
  const preview = !item.open && live ? thoughtPreview(item.text) : '';
  const toggle = () => {
    item.open = !item.open;
    emit();
  };

  // Keep the one-line preview pinned to the newest tokens while reasoning
  // streams. A frame-based catch-up avoids restarting a smooth scroll on every
  // token chunk. Expanded text remains a normal scrollable block.
  useLayoutEffect(() => {
    const node = previewRef.current;
    if (!node || !preview) return;
    const target = node.scrollWidth - node.clientWidth;
    if (target <= 0) return;
    let frame = 0;
    const step = () => {
      const remaining = target - node.scrollLeft;
      if (Math.abs(remaining) < 1) return;
      node.scrollLeft += Math.ceil(remaining * 0.35);
      frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [preview]);

  return (
    <div className={cn('animate-in fade-in border-l-2 border-borderstrong pl-2.5 duration-300', item.open && '[&_.thought-chev]:rotate-90')}>
      <button
        type="button"
        className="thought-toggle flex w-full cursor-pointer items-center gap-1.5 rounded text-left text-[12px] text-muted-foreground select-none hover:text-strong focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        aria-expanded={item.open}
        onClick={toggle}
      >
        <ChevronRight className="thought-chev size-3.5 shrink-0 transition-transform" />
        <span className="shrink-0">{t('chat.thinking')}</span>
        {preview ? (
          <span
            ref={previewRef}
            className="thought-preview block min-w-0 flex-1 overflow-hidden text-[11.5px] whitespace-nowrap text-faint"
            aria-hidden="true"
          >
            {preview}
          </span>
        ) : null}
      </button>
      {item.open ? (
        <div className="mt-1.5 max-h-60 overflow-y-auto text-[12px] leading-[1.7] break-words whitespace-pre-wrap text-muted-foreground">
          {item.text}
        </div>
      ) : null}
    </div>
  );
}

function ToolBody({ item }: { item: ToolItem }) {
  return (
    <div className="flex flex-col gap-2 px-3 pb-2.5">
      {item.rawInput && Object.keys(item.rawInput).length > 0 ? (
        <>
          <div className="text-[11px] font-semibold tracking-[.3px] text-faint">{t('chat.toolInput')}</div>
          <div className="max-h-80 overflow-y-auto rounded-md border border-border bg-code p-2.5 font-mono text-[11.5px] leading-[1.7] break-all whitespace-pre-wrap">
            {prettyInput(item.rawInput)}
          </div>
        </>
      ) : null}
      {item.contents.map((content, index) => {
        if (content.type === 'diff') {
          const lines = buildDiffLines(content.oldText ?? null, content.newText || '');
          return (
            <div key={index}>
              <div className="text-[11px] font-semibold tracking-[.3px] text-faint">diff</div>
              <div className="mt-1 max-h-80 overflow-auto rounded-md border border-border bg-code p-2.5 font-mono text-[11.5px] leading-[1.7]">
                {content.path ? <div className="mb-1 font-sans text-[11px] break-all text-muted-foreground">{content.path}</div> : null}
                {lines.map((line, lineIndex) => (
                  <div
                    key={lineIndex}
                    className={cn(
                      'break-all whitespace-pre-wrap',
                      line.type === 'add' && 'bg-success-soft text-success',
                      line.type === 'del' && 'bg-danger-soft text-danger line-through'
                    )}
                  >
                    {line.text}
                  </div>
                ))}
              </div>
            </div>
          );
        }
        const text = content.content?.text || '';
        if (!text.trim()) return null;
        return (
          <div key={index}>
            <div className="text-[11px] font-semibold tracking-[.3px] text-faint">{t('chat.toolOutput')}</div>
            <div className="mt-1 max-h-80 overflow-y-auto rounded-md border border-border bg-code p-2.5 font-mono text-[11.5px] leading-[1.7] break-all whitespace-pre-wrap">
              {text.length > 12000 ? `${text.slice(0, 12000)}\n…` : text}
            </div>
          </div>
        );
      })}
      {item.locations && item.locations.length > 0 ? (
        <div className="font-sans text-[11px] break-all text-muted-foreground">
          {item.locations.map((location) => location.path).join('\n')}
        </div>
      ) : null}
    </div>
  );
}

export function ToolCard({ item }: { item: ToolItem }) {
  const Icon = TOOL_ICONS[item.toolKind] || Cpu;
  const toggle = () => {
    item.open = !item.open;
    emit();
  };
  return (
    <div className="animate-in fade-in overflow-hidden rounded-[10px] border border-border bg-card duration-300">
      <button
        type="button"
        className="tool-head flex w-full cursor-pointer items-center gap-[7px] px-3 py-[7px] text-left text-[12px] text-muted-foreground select-none hover:bg-hoverbg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        aria-expanded={item.open}
        onClick={toggle}
      >
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="max-w-[70%] truncate font-semibold text-foreground" title={item.title}>
          {toolSummary(item)}
        </span>
        <span className={cn('ml-1 shrink-0 rounded-lg px-[7px] py-px text-[10px] font-semibold', TOOL_BADGE[item.status] || TOOL_BADGE.pending)}>
          {statusLabel(item.status)}
        </span>
        <ChevronRight className={cn('ml-auto size-3.5 shrink-0 transition-transform', item.open && 'rotate-90')} />
      </button>
      {item.open ? <ToolBody item={item} /> : null}
    </div>
  );
}

export function PlanCard({ item }: { item: PlanItem }) {
  return (
    <div className="animate-in fade-in overflow-hidden rounded-[10px] border border-border bg-card shadow-panel duration-300">
      <div className="flex items-center gap-2 border-b border-border bg-primary/4 px-3 py-2 text-[12.5px] font-semibold text-strong">
        <List className="size-4 text-primary" />
        <span>{t('chat.plan', { n: item.entries.length })}</span>
      </div>
      <div className="flex flex-col gap-[7px] px-3 pt-2 pb-2.5">
        {item.entries.map((entry, index) => {
          const stepClass = planStepClass(entry.status);
          return (
            <div
              key={index}
              className={cn(
                'flex items-start gap-2 text-[12.5px] leading-normal',
                stepClass === 'done' && 'text-foreground',
                stepClass === 'doing' && 'font-semibold text-primary',
                stepClass === 'failed' && 'text-danger',
                stepClass === '' && 'text-muted-foreground'
              )}
            >
              <span
                className={cn(
                  'mt-px flex size-[15px] shrink-0 items-center justify-center rounded-full border-[1.5px] transition-all',
                  stepClass === 'done' && 'border-primary bg-primary text-primary-foreground',
                  stepClass === 'doing' && 'border-primary',
                  stepClass === 'failed' && 'border-danger',
                  stepClass === '' && 'border-borderstrong text-transparent'
                )}
              >
                {stepClass === 'done' ? <Check className="size-2.5" /> : null}
                {stepClass === 'doing' ? <span className="size-1.5 animate-pulse rounded-full bg-primary" /> : null}
              </span>
              <span>{entry.content}</span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ArtifactChip({ item }: { item: ArtifactItem }) {
  const Icon = item.artifactKind === 'image' ? ImageIcon : FileText;
  return (
    <div
      className="flex min-w-0 cursor-pointer items-center gap-[7px] rounded-lg border border-borderstrong bg-background px-3 py-[7px] transition-all hover:border-primary hover:bg-primary/4"
      role="button"
      tabIndex={0}
      onClick={() => void openArtifact(item)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') void openArtifact(item);
      }}
    >
      <span className="flex size-[26px] shrink-0 items-center justify-center rounded-md bg-primary/8 text-primary">
        <Icon className="size-4" />
      </span>
      <span className="min-w-0">
        <span className="block max-w-[220px] truncate text-[12px] font-semibold text-strong">{item.filename}</span>
        <span className="block text-[10.5px] text-faint">{[item.artifactKind, formatBytes(item.size)].filter(Boolean).join(' · ')}</span>
      </span>
    </div>
  );
}

export function ArtifactCard({ item }: { item: ArtifactItem }) {
  return (
    <div className="animate-in fade-in rounded-[10px] border border-border bg-card px-3 py-2.5 shadow-panel duration-300">
      <div className="mb-2 text-[12px] text-muted-foreground">
        {item.fromToolCall ? t('chat.artifactFallback') : t('chat.artifacts', { n: 1 })}
      </div>
      <div className="flex flex-wrap gap-2">
        <ArtifactChip item={item} />
      </div>
    </div>
  );
}

function DecisionDeadline({ item }: { item: DecisionItem }) {
  const active = Boolean(item.deadline && !item.resolved);
  useNow(active);
  if (!item.deadline || item.resolved) return null;
  return (
    <span className={cn('ml-auto text-[11px] tabular-nums', item.resolved ? 'text-faint' : 'text-warning')}>
      {deadlineText(item.deadline)}
    </span>
  );
}

export function PermissionCard({ item }: { item: PermissionItem }) {
  return (
    <div
      className={cn(
        'animate-in fade-in overflow-hidden rounded-[10px] border bg-card shadow-panel duration-300',
        item.resolved ? 'border-border opacity-75' : 'border-warning'
      )}
    >
      <div
        className={cn(
          'flex items-center gap-2 px-3 py-2 text-[12.5px] font-semibold',
          item.resolved ? 'bg-placeholder text-muted-foreground' : 'bg-warning-soft text-strong'
        )}
      >
        <Shield className={cn('size-4', item.resolved ? 'text-muted-foreground' : 'text-warning')} />
        <span>{`${t('chat.approvalTitle')} · ${item.title}`}</span>
        <DecisionDeadline item={item} />
      </div>
      <div className="flex flex-col gap-2.5 px-3 py-2.5">
        {item.rawInput && Object.keys(item.rawInput).length > 0 ? (
          <div className="max-h-80 overflow-y-auto rounded-md border border-border bg-code p-2.5 font-mono text-[11.5px] leading-[1.7] break-all whitespace-pre-wrap">
            {prettyInput(item.rawInput)}
          </div>
        ) : null}
        {item.resolved ? (
          <div className="text-[11.5px] text-faint">{t('chat.resolved', { v: item.resolved })}</div>
        ) : (
          <div className="flex flex-wrap gap-2">
            {item.options.map((option) => (
              <Button
                key={option.optionId}
                variant={option.kind === 'reject_once' ? 'deny' : 'allow'}
                onClick={() => resolvePermission(item, option.optionId)}
              >
                {option.name || (option.kind === 'reject_once' ? t('chat.reject') : t('chat.allow'))}
              </Button>
            ))}
            <Button variant="deny" onClick={() => cancelPermission(item)}>
              {t('chat.cancelled')}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

export function QuestionCard({ item }: { item: QuestionItem }) {
  const [answer, setAnswer] = useState('');
  const submit = () => {
    const value = answer.trim();
    if (!value) return;
    resolveQuestion(item, value);
  };
  return (
    <div
      className={cn(
        'animate-in fade-in overflow-hidden rounded-[10px] border bg-card shadow-panel duration-300',
        item.resolved ? 'border-border opacity-75' : 'border-warning'
      )}
    >
      <div
        className={cn(
          'flex items-center gap-2 px-3 py-2 text-[12.5px] font-semibold',
          item.resolved ? 'bg-placeholder text-muted-foreground' : 'bg-warning-soft text-strong'
        )}
      >
        <HelpCircle className={cn('size-4', item.resolved ? 'text-muted-foreground' : 'text-warning')} />
        <span>{t('chat.questionTitle')}</span>
        <DecisionDeadline item={item} />
      </div>
      <div className="flex flex-col gap-2.5 px-3 py-2.5">
        {item.prompt ? <div className="text-[13px] leading-relaxed whitespace-pre-wrap">{item.prompt}</div> : null}
        {item.explanation ? <div className="text-[12px] leading-normal text-muted-foreground">{item.explanation}</div> : null}
        {item.resolved ? (
          <div className="text-[11.5px] text-faint">{t('chat.resolved', { v: item.resolved })}</div>
        ) : (
          <>
            {item.options.length > 0 ? (
              <div className="flex flex-wrap gap-2">
                {item.options.map((option) => (
                  <Button key={option.id} variant="outline" onClick={() => resolveQuestion(item, option.label)}>
                    {option.label}
                  </Button>
                ))}
              </div>
            ) : null}
            <div className="flex gap-2">
              <Input
                className="flex-1"
                type="text"
                placeholder={t('chat.answerPlaceholder')}
                value={answer}
                onChange={(event) => setAnswer(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') submit();
                }}
              />
              <Button variant="allow" onClick={submit}>
                {t('chat.submit')}
              </Button>
              <Button variant="deny" onClick={() => cancelQuestion(item)}>
                {t('chat.skip')}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export function StatusLine({ item }: { item: Extract<TranscriptItem, { kind: 'status' }> }) {
  return (
    <div className="flex animate-in fade-in items-center gap-2 text-[12px] text-muted-foreground duration-300">
      {item.spin ? <Loader2 className="size-3.5 shrink-0 animate-spin text-primary" /> : null}
      <span>{item.text}</span>
    </div>
  );
}

export function SubagentCard({ item }: { item: Extract<TranscriptItem, { kind: 'subagent' }> }) {
  const badgeClass =
    item.status === 'completed'
      ? 'bg-success-soft text-success'
      : item.status === 'failed'
        ? 'bg-danger-soft text-danger'
        : 'bg-info-soft text-info';
  const detail = [item.role, item.expertId].filter(Boolean).join(' · ');
  return (
    <div className="animate-in fade-in overflow-hidden rounded-[10px] border border-dashed border-borderstrong bg-card duration-300">
      <div className="flex items-center gap-2 px-3 py-[7px] text-[12px] text-muted-foreground">
        <Users className="size-4 shrink-0 text-expert" />
        <span className="min-w-0 flex-1 truncate font-semibold text-foreground">{`${t('chat.subagent')} · ${item.title}`}</span>
        <span className={cn('shrink-0 rounded-lg px-[7px] py-px text-[10px] font-semibold', badgeClass)}>{item.status}</span>
      </div>
      {detail ? <div className="px-3 pb-2 text-[11.5px] text-muted-foreground">{detail}</div> : null}
    </div>
  );
}

export function ErrorCard({ item }: { item: Extract<TranscriptItem, { kind: 'error' }> }) {
  return (
    <div className="animate-in fade-in rounded-[10px] border border-danger bg-danger-soft px-3 py-2.5 duration-300">
      <div className="flex items-center gap-[7px] text-[12.5px] font-semibold text-danger">
        <AlertCircle className="size-4" />
        <span>{t('status.failed')}</span>
      </div>
      <div className="mt-1.5 text-[12.5px] leading-relaxed break-words whitespace-pre-wrap text-foreground">{item.message}</div>
      {item.code || item.retryable ? (
        <div className="mt-1.5 flex flex-wrap gap-2.5 text-[11px] text-muted-foreground">
          {item.code ? <span>code: {item.code}</span> : null}
          {item.retryable ? <span>retryable</span> : null}
        </div>
      ) : null}
    </div>
  );
}

export function TranscriptEntry({ item, live = false }: { item: TranscriptItem; live?: boolean }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} />;
    case 'agent':
      return <AgentMessage item={item} />;
    case 'thought':
      return <ThoughtBlock item={item} live={live} />;
    case 'tool':
      return <ToolCard item={item} />;
    case 'plan':
      return <PlanCard item={item} />;
    case 'artifact':
      return <ArtifactCard item={item} />;
    case 'permission':
      return <PermissionCard item={item} />;
    case 'question':
      return <QuestionCard item={item} />;
    case 'status':
      return <StatusLine item={item} />;
    case 'subagent':
      return <SubagentCard item={item} />;
    case 'error':
      return <ErrorCard item={item} />;
    default:
      return null;
  }
}
