// 历史任务视图:全局会话搜索与分页。项目仅作为查询/组织维度,不再拥有
// 独立管理页;每行操作复用任务树的会话菜单。

import { useEffect, useRef, useState } from 'react';
import { List, Search } from 'lucide-react';

import { SessionMenu } from '@/components/Sidebar';
import { EmptyState, PageHead, PageInner, PageScroll, RowItem, RowList } from '@/components/layout';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { t } from '@/core/i18n';
import { historySessions, loadMoreHistory, openSession, refreshHistory, setHistoryQuery, setHistoryScope } from '@/core/sessions';
import { type ListedSessionShape, type ProjectShape } from '@/core/state';
import { shallowEqual, useAppSelector } from '@/hooks/useAppState';

function sessionTitle(session: ListedSessionShape): string {
  return session.title || session.sessionId.slice(0, 10);
}

function sessionProjectName(session: ListedSessionShape, projects: ProjectShape[]): string {
  const project = projects.find((entry) => entry.id === session._meta?.projectId);
  return project?.name || t('tree.ungrouped');
}

type HistoryStatus = 'idle' | 'working' | 'pending' | 'completed' | 'failed' | 'incomplete' | 'cancelled';

function historyStatus(session: ListedSessionShape, rememberedStatus?: string): HistoryStatus {
  const status = rememberedStatus || (session._meta?.lastRun?.active ? 'running' : session._meta?.lastRun?.status);
  switch (status) {
    case 'created':
    case 'queued':
    case 'running':
    case 'cancelling':
    case 'terminalizing':
    case 'working':
      return 'working';
    case 'waiting_for_approval':
    case 'waiting_for_question':
    case 'pending':
      return 'pending';
    case 'completed':
      return 'completed';
    case 'incomplete':
      return 'incomplete';
    case 'failed':
    case 'expired':
    case 'timed_out':
      return 'failed';
    case 'cancelled':
    case 'canceled':
      return 'cancelled';
    default:
      return 'idle';
  }
}

function sessionStatusLabel(status: HistoryStatus): string {
  return t(`status.${status}`);
}

function historyStatusVariant(status: HistoryStatus): 'secondary' | 'info' | 'success' | 'warning' | 'danger' {
  switch (status) {
    case 'working':
      return 'info';
    case 'completed':
      return 'success';
    case 'failed':
    case 'incomplete':
      return 'danger';
    case 'pending':
      return 'warning';
    default:
      return 'secondary';
  }
}

export function HistoryView() {
  const viewState = useAppSelector(
    (state) => ({
      sessionPages: state.sessionPages,
      projects: state.projects,
      sessionStatus: state.store.sessionStatus,
      historyScope: state.historyScope,
      historyProjectId: state.historyProjectId,
      historyQuery: state.historyQuery,
      capabilities: state.connection.agentCapabilities,
    }),
    shallowEqual,
  );
  const [search, setSearch] = useState(viewState.historyQuery);
  const debounceRef = useRef<number | undefined>(undefined);
  const page = historySessions();

  useEffect(() => {
    void refreshHistory();
  }, []);

  useEffect(() => {
    return () => window.clearTimeout(debounceRef.current);
  }, []);

  return (
    <PageScroll>
      <PageInner>
        <PageHead title={t('history.title')} subtitle={t('history.subtitle')} />

        <div className="-mt-1.5 mb-3.5 flex items-center gap-2">
          <label className="flex h-[34px] min-w-0 flex-1 items-center gap-[7px] rounded-lg border border-borderstrong bg-inputbg px-2.5 text-muted-foreground focus-within:border-primary focus-within:ring-[3px] focus-within:ring-primary/12">
            <Search className="size-3.5 shrink-0" />
            <input
              className="w-full min-w-0 border-0 bg-transparent text-foreground outline-none"
              placeholder={t('history.searchPlaceholder')}
              aria-label={t('history.searchPlaceholder')}
              value={search}
              onChange={(event) => {
                const next = event.target.value;
                setSearch(next);
                window.clearTimeout(debounceRef.current);
                debounceRef.current = window.setTimeout(() => void setHistoryQuery(next), 220);
              }}
            />
          </label>
          <Select
            value={viewState.historyScope}
            onValueChange={(value) => {
              if (value === 'project') {
                const id = viewState.historyProjectId || viewState.projects[0]?.id || '';
                if (!id) {
                  void setHistoryScope('all');
                  return;
                }
                void setHistoryScope('project', id);
                return;
              }
              void setHistoryScope(value === 'ungrouped' ? 'ungrouped' : 'all');
            }}
          >
            <SelectTrigger className="max-w-[150px]" aria-label="History scope">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t('history.scopeAll')}</SelectItem>
              <SelectItem value="ungrouped">{t('history.scopeUngrouped')}</SelectItem>
              <SelectItem value="project">{t('history.scopeProject')}</SelectItem>
            </SelectContent>
          </Select>
          {viewState.historyScope === 'project' ? (
            <Select value={viewState.historyProjectId} onValueChange={(value) => void setHistoryScope('project', value)}>
              <SelectTrigger className="max-w-[150px]" aria-label="Project">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {viewState.projects.map((project) => (
                  <SelectItem key={project.id} value={project.id}>
                    {project.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : null}
        </div>

        <RowList>
          {page.sessions.map((session) => {
            // ACP run_status events update this presentation projection before
            // the next history page reload. Durable lastRun remains the fallback.
            const status = historyStatus(session, viewState.sessionStatus[session.sessionId]);
            return (
              <RowItem
                key={session.sessionId}
                className="min-h-[60px]"
                icon={<List />}
                title={
                  <>
                    <span className="min-w-0 truncate">{sessionTitle(session)}</span>
                    <Badge variant={historyStatusVariant(status)}>{sessionStatusLabel(status)}</Badge>
                  </>
                }
                desc={[
                  sessionProjectName(session, viewState.projects),
                  session.cwd,
                  [session.provider, session.model].filter(Boolean).join(' / '),
                  session.updatedAt ? new Date(session.updatedAt).toLocaleString() : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
                onClick={() => void openSession(session.sessionId)}
              >
                <SessionMenu session={session} projects={viewState.projects} />
              </RowItem>
            );
          })}
          {page.loading && page.sessions.length === 0 ? (
            <div className="px-4 py-3 text-[11.5px] text-muted-foreground">…</div>
          ) : null}
        </RowList>

        {page.nextCursor ? (
          <div className="flex justify-center py-3.5">
            <button
              type="button"
              className="rounded-lg border border-borderstrong bg-card px-3 py-1.5 text-[12.5px] hover:border-primary hover:text-primary disabled:opacity-55"
              disabled={page.loading}
              onClick={() => void loadMoreHistory()}
            >
              {page.loading ? '…' : t('history.loadMore')}
            </button>
          </div>
        ) : null}

        {!page.loading && page.sessions.length === 0 ? <EmptyState icon={<Search />} title={t('history.noResults')} /> : null}
      </PageInner>
    </PageScroll>
  );
}
