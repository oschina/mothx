// 左侧任务树。项目只是全局 ACP 任务目录的组织维度分支;它们绝不筛选当前
// 工作目录。任务分页/展开状态只存在于渲染进程内存。

import { useRef, useState } from 'react';
import {
  BookOpen,
  Check,
  ChevronRight,
  Clock,
  List,
  Moon,
  Pin,
  Plus,
  Search,
  Settings,
  Sparkles,
  Sun,
  Users,
  Zap,
} from 'lucide-react';

import { t } from '@/core/i18n';
import {
  changeSessionWorkingDirectory,
  createProject,
  createProjectAndAssign,
  deleteProject,
  deleteSession,
  forkSession,
  isPinned,
  loadMoreTaskSessions,
  openSession,
  recentSessions,
  refreshHistory,
  renameProject,
  renameSession,
  setSessionProject,
  sessionPage,
  startNewTask,
  taskProjectPageKey,
  taskUngroupedPageKey,
  togglePin,
  toggleProjectExpanded,
  ungroupedSessions,
} from '@/core/sessions';
import { openSettingsTab } from '@/core/settings-nav';
import { hasFeature, isSessionRunning, sessionRunStatus, sessionTitle, state, type ListedSessionShape, type ProjectShape } from '@/core/state';
import { applyTheme } from '@/core/theme';
import { switchView } from '@/core/views';
import { useAppState } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { useAutoHideScrollbar } from '@/hooks/useAutoHideScrollbar';
import { useFlipList } from '@/hooks/useFlipList';
import { matchesTaskSearch, sortTaskSessions } from '@/core/task-tree';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';

const DOT_CLASS: Record<string, string> = {
  planning: 'bg-info',
  working: 'bg-primary',
  pending: 'bg-warning',
  cancelling: 'bg-warning',
  completed: 'bg-success',
  failed: 'bg-danger',
  cancelled: 'bg-faint',
  idle: 'bg-faint',
};

function sessionStatus(session: ListedSessionShape): string {
  const lastRun = session._meta?.lastRun;
  if (hasFeature('runStatus') && lastRun) {
    if (lastRun.active || lastRun.status === 'running') return 'working';
    return lastRun.status || 'idle';
  }
  // 每个任务各自投影自己的 Run:后台任务运行时,当前任务不会显示为“执行中”。
  // Return the precise live status so pending/cancelling sessions get their own
  // discoverable dot color instead of being collapsed into 'working'.
  const live = sessionRunStatus(session.sessionId);
  if (isSessionRunning(session.sessionId)) return live;
  return 'idle';
}

export function SessionMenu({ session }: { session: ListedSessionShape }) {
  const appState = useAppState();
  const projectId = session._meta?.projectId || null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="flex size-5 shrink-0 items-center justify-center rounded text-[16px] leading-none text-faint hover:bg-activebg hover:text-strong"
          title={t('session.actions')}
          aria-label={t('session.actions')}
          onClick={(event) => event.stopPropagation()}
        >
          ⋯
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-[208px]" onClick={(event) => event.stopPropagation()}>
        <DropdownMenuItem onSelect={() => void togglePin(session.sessionId)}>
          {isPinned(session.sessionId) ? t('session.unpin') : t('session.pin')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void renameSession(session.sessionId)}>{t('chat.rename')}</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t('session.moveToProject')}</DropdownMenuLabel>
        {appState.projects.map((project) => (
          <DropdownMenuItem key={project.id} onSelect={() => void setSessionProject(session.sessionId, project.id)}>
            {project.id === projectId ? <Check className="size-3.5" /> : null}
            <span className="truncate">{project.name}</span>
          </DropdownMenuItem>
        ))}
        <DropdownMenuItem onSelect={() => void createProjectAndAssign(session.sessionId)}>
          {t('session.createProjectAndMove')}
        </DropdownMenuItem>
        {projectId ? (
          <DropdownMenuItem onSelect={() => void setSessionProject(session.sessionId, null)}>
            {t('session.removeProject')}
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t('session.actions')}</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => void changeSessionWorkingDirectory(session.sessionId)}>
          {t('session.setWorkspace')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void forkSession(session.sessionId)}>{t('chat.fork')}</DropdownMenuItem>
        <DropdownMenuItem variant="destructive" onSelect={() => void deleteSession(session.sessionId)}>
          {t('chat.delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SessionItem({ session, registerFlip }: { session: ListedSessionShape; registerFlip: (key: string) => (el: HTMLElement | null) => void }) {
  const appState = useAppState();
  const status = sessionStatus(session);
  const active = appState.activeSessionId === session.sessionId;
  const title = sessionTitle(session.sessionId);
  return (
    <div
      ref={registerFlip(session.sessionId)}
      className={cn(
        'group flex h-[26px] cursor-pointer items-center gap-[7px] rounded-md px-2 hover:bg-hoverbg',
        'animate-in fade-in slide-in-from-bottom-1 duration-200',
        active && 'bg-activebg'
      )}
      title={`${title}${session.model ? ` · ${session.model}` : ''}${session.cwd ? ` · ${session.cwd}` : ''}`}
      role="button"
      tabIndex={0}
      onClick={() => void openSession(session.sessionId)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') void openSession(session.sessionId);
      }}
    >
      <span className={cn('size-1.5 shrink-0 rounded-full', DOT_CLASS[status] || 'bg-faint')} />
      <span className="flex-1 truncate text-[12.5px]">{title}</span>
      {isPinned(session.sessionId) ? <Pin className="size-3 shrink-0 text-faint" /> : null}
      <SessionMenu session={session} />
    </div>
  );
}

function SessionRows({
  sessions,
  keyword,
  registerFlip,
}: {
  sessions: ListedSessionShape[];
  keyword: string;
  registerFlip: (key: string) => (el: HTMLElement | null) => void;
}) {
  const visible = sortTaskSessions(sessions).filter((session) => matchesTaskSearch(session, state.projects, keyword));
  return (
    <>
      {visible.map((session) => (
        <SessionItem key={session.sessionId} session={session} registerFlip={registerFlip} />
      ))}
    </>
  );
}

function LoadMore({ label, loading, onClick }: { label: string; loading: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      className="mx-2 mt-0.5 mb-1 rounded-[5px] px-1.5 py-0.5 text-left text-[11px] text-muted-foreground hover:bg-hoverbg hover:text-foreground disabled:opacity-60"
      disabled={loading}
      onClick={onClick}
    >
      {loading ? '…' : label}
    </button>
  );
}

function ProjectMenu({ project }: { project: ProjectShape }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="flex size-5 items-center justify-center rounded text-[16px] leading-none text-faint hover:bg-activebg hover:text-strong"
          title={t('projects.actions')}
          aria-label={t('projects.actions')}
          onClick={(event) => event.stopPropagation()}
        >
          ⋯
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-[180px]">
        <DropdownMenuItem
          onSelect={async () => {
            await createProject();
          }}
        >
          {t('projects.new')}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void renameProject(project.id, project.name)}>{t('projects.rename')}</DropdownMenuItem>
        <DropdownMenuItem variant="destructive" onSelect={() => void deleteProject(project.id)}>
          {t('projects.delete')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ProjectBranch({ project, keyword, registerFlip }: { project: ProjectShape; keyword: string; registerFlip: (key: string) => (el: HTMLElement | null) => void }) {
  const appState = useAppState();
  const expanded = appState.expandedProjectIds.includes(project.id);
  const key = taskProjectPageKey(project.id);
  const page = sessionPage(key);
  return (
    <div className="flex flex-col">
      <div className="group flex min-h-[27px] items-center gap-[3px] rounded-md px-1 hover:bg-hoverbg">
        <button
          type="button"
          className="flex h-5 w-[18px] items-center justify-center text-faint"
          aria-expanded={expanded}
          aria-label={`${project.name}`}
          onClick={() => void toggleProjectExpanded(project.id)}
        >
          <ChevronRight className={cn('size-3.5 transition-transform', expanded && 'rotate-90')} />
        </button>
        <button type="button" className="min-w-0 flex-1 truncate text-left text-[12.5px]" onClick={() => void toggleProjectExpanded(project.id)}>
          {project.name}
        </button>
        <span className="shrink-0 text-[10.5px] tabular-nums text-muted-foreground">{project.sessionCount ?? 0}</span>
        <ProjectMenu project={project} />
      </div>
      {expanded ? (
        <div className="ml-[9px] border-l border-border pl-[7px]">
          <SessionRows sessions={page.sessions} keyword={keyword} registerFlip={registerFlip} />
          {page.loading && page.sessions.length === 0 ? <div className="px-2 py-1.5 text-[11.5px] text-muted-foreground">…</div> : null}
          {page.nextCursor ? (
            <LoadMore
              label={t('history.loadMore')}
              loading={page.loading}
              onClick={() => void loadMoreTaskSessions(key, { scope: 'project', projectId: project.id })}
            />
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

interface NavEntry {
  view: string;
  icon: typeof Sparkles;
  label: string;
  sub: string;
  feature?: string;
  onClick?: () => void;
}

export function Sidebar() {
  const appState = useAppState();
  const background = useAppBackground();
  const scrollRef = useRef<HTMLDivElement>(null);
  useAutoHideScrollbar(scrollRef);
  const [keyword, setKeyword] = useState('');
  const query = keyword.trim().toLowerCase();

  // 列表签名(可见条目顺序)变化时才触发 FLIP 位移动画,避免流式重渲染开销。
  const visibleIds = (list: ListedSessionShape[]) =>
    sortTaskSessions(list)
      .filter((session) => matchesTaskSearch(session, state.projects, query))
      .map((session) => session.sessionId)
      .join(',');
  const flipSignature = [
    ...appState.projects.map((project) => visibleIds(sessionPage(taskProjectPageKey(project.id)).sessions)),
    visibleIds(recentSessions()),
    visibleIds(ungroupedSessions()),
  ].join('|');
  const registerFlip = useFlipList(flipSignature);

  const navEntries: NavEntry[] = [
    { view: 'home', icon: Sparkles, label: t('nav.home'), sub: '' },
    { view: 'skills', icon: Zap, label: t('nav.skills'), sub: t('nav.skillsSub') },
    { view: 'history', icon: List, label: t('nav.history'), sub: t('nav.historySub') },
    { view: 'automation', icon: Clock, label: t('nav.automation'), sub: t('nav.automationSub') },
    {
      view: 'experts', icon: Users, label: t('nav.experts'), sub: t('nav.expertsSub'), feature: 'manageExperts',
      onClick: () => {
        switchView('settings');
        openSettingsTab('experts');
      },
    },
    { view: 'library', icon: BookOpen, label: t('nav.library'), sub: t('nav.librarySub') },
  ];

  const ungroupedPage = sessionPage(taskUngroupedPageKey());
  const hasVisible =
    appState.projects.length > 0 ||
    recentSessions().filter((session) => matchesTaskSearch(session, state.projects, query)).length > 0 ||
    ungroupedSessions().filter((session) => matchesTaskSearch(session, state.projects, query)).length > 0;
  const emptyText = query ? t('history.noResults') : appState.sessionsLoading ? '…' : t('nav.noTasks');

  return (
    <aside
      className={cn(
        'app-layer flex w-[var(--sidebar-w)] shrink-0 flex-col overflow-hidden border-r border-border bg-sidebar',
        background.app && 'app-surface-veil border-r-transparent'
      )}
    >
      <div ref={scrollRef} className="autohide-scrollbar flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2.5 pt-2.5 pb-2">
        <Button
          variant="outline"
          className={cn(
            'mb-2.5 h-9 w-full rounded-lg border-primary/55 bg-primary/8 font-semibold text-primary hover:bg-primary hover:text-primary-foreground hover:border-primary',
            background.app &&
              'app-control-surface border-[var(--app-control-border-strong)] bg-[var(--app-control-bg)] text-primary hover:bg-primary hover:text-primary-foreground'
          )}
          onClick={() => void startNewTask()}
        >
          <Plus className="size-4" />
          <span className="truncate">{t('nav.newTask')}</span>
        </Button>

        <label
          className={cn(
            'mb-2 flex h-7 items-center gap-1.5 rounded-md bg-placeholder px-2 text-muted-foreground focus-within:ring-1 focus-within:ring-ring/40',
            background.app && 'app-control-surface'
          )}
        >
          <Search className="size-3.5 shrink-0" />
          <input
            className="w-full min-w-0 border-none bg-transparent text-[12.5px] text-foreground outline-none placeholder:text-muted-foreground"
            placeholder={t('nav.searchPlaceholder')}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            aria-label={t('nav.searchPlaceholder')}
          />
        </label>

        {navEntries.map((entry) => {
          if (entry.feature && !hasFeature(entry.feature)) return null;
          const Icon = entry.icon;
          const active = entry.view !== 'experts' && appState.view === entry.view;
          return (
            <div
              key={entry.view}
              className={cn(
                'relative mx-0 my-[3px] flex h-[30px] cursor-pointer items-center gap-2 rounded-md px-2 transition-colors hover:bg-hoverbg focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-primary',
                active && 'bg-activebg font-semibold'
              )}
              role="button"
              tabIndex={0}
              onClick={() => {
                if (entry.onClick) entry.onClick();
                else switchView(entry.view);
                if (entry.view === 'history') void refreshHistory();
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  if (entry.onClick) entry.onClick();
                  else switchView(entry.view);
                  if (entry.view === 'history') void refreshHistory();
                }
              }}
            >
              {active ? <span className="absolute top-1/2 -left-2.5 h-4 w-[3px] -translate-y-1/2 rounded-r-[3px] bg-primary" /> : null}
              <Icon className={cn('size-4 shrink-0', active ? 'text-primary' : 'text-muted-foreground')} />
              <span className="min-w-0 flex-1 truncate">{entry.label}</span>
              {entry.sub ? <span className="shrink-0 text-[10.5px] text-muted-foreground">{entry.sub}</span> : null}
            </div>
          );
        })}

        <div className={cn('mx-1 my-2 h-px', background.app ? 'bg-[rgb(var(--app-outline-rgb)/.14)]' : 'bg-border')} />

        <div className="flex items-center justify-between px-2 pt-2.5 pb-1 text-[11px] font-semibold tracking-[.3px] text-muted-foreground">
          <span>{t('nav.tasks')}</span>
          <span className="flex items-center gap-0.5">
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  className="flex rounded p-0.5 text-faint hover:bg-hoverbg hover:text-foreground"
                  aria-label={t('nav.history')}
                  onClick={() => {
                    switchView('history');
                    void refreshHistory();
                  }}
                >
                  <List className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>{t('nav.history')}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  className="flex rounded p-0.5 text-faint hover:bg-hoverbg hover:text-foreground"
                  aria-label={t('nav.newTask')}
                  onClick={() => void startNewTask()}
                >
                  <Plus className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>{t('nav.newTask')}</TooltipContent>
            </Tooltip>
          </span>
        </div>

        <section className="flex flex-col gap-px">
          <div className="flex min-h-[22px] items-center justify-between px-2 pt-[3px] pb-0.5 text-[10.5px] font-bold tracking-[.3px] text-muted-foreground">
            <span>{t('tree.projects')}</span>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  className="flex rounded p-0.5 text-muted-foreground hover:bg-hoverbg hover:text-foreground"
                  aria-label={t('projects.new')}
                  onClick={() => void createProject()}
                >
                  <Plus className="size-3.5" />
                </button>
              </TooltipTrigger>
              <TooltipContent>{t('projects.new')}</TooltipContent>
            </Tooltip>
          </div>
          <div>
            {appState.projects.map((project) => (
              <ProjectBranch key={project.id} project={project} keyword={query} registerFlip={registerFlip} />
            ))}
            {appState.projects.length === 0 && hasFeature('projects') ? (
              <div className="px-2 py-1.5 text-[11.5px] text-muted-foreground">{t('projects.empty')}</div>
            ) : null}
          </div>
        </section>

        <section className="flex flex-col gap-px">
          <div className="flex min-h-[22px] items-center justify-between px-2 pt-[3px] pb-0.5 text-[10.5px] font-bold tracking-[.3px] text-muted-foreground">
            <span>{t('tree.recent')}</span>
          </div>
          <div>
            <SessionRows sessions={recentSessions()} keyword={query} registerFlip={registerFlip} />
          </div>
        </section>

        <section className="flex flex-col gap-px">
          <div className="flex min-h-[22px] items-center justify-between px-2 pt-[3px] pb-0.5 text-[10.5px] font-bold tracking-[.3px] text-muted-foreground">
            <span>{t('tree.ungrouped')}</span>
          </div>
          <div>
            <SessionRows sessions={ungroupedSessions()} keyword={query} registerFlip={registerFlip} />
            {ungroupedPage.nextCursor ? (
              <LoadMore
                label={t('history.loadMore')}
                loading={ungroupedPage.loading}
                onClick={() => void loadMoreTaskSessions(taskUngroupedPageKey(), { scope: 'ungrouped' })}
              />
            ) : null}
          </div>
        </section>

        {hasVisible ? null : <div className="px-2 py-1.5 text-[11.5px] text-muted-foreground">{emptyText}</div>}
      </div>

      <div className="flex shrink-0 items-center gap-1.5 border-t border-border px-2.5 py-2">
        <span className="flex-1 truncate pl-1.5 text-[11px] text-muted-foreground">{t('nav.localMode')}</span>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-hoverbg hover:text-strong"
              aria-label={appState.store.theme === 'dark' ? t('settings.light') : t('settings.dark')}
              onClick={() => applyTheme(appState.store.theme === 'dark' ? 'light' : 'dark')}
            >
              {appState.store.theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
            </button>
          </TooltipTrigger>
          <TooltipContent>{appState.store.theme === 'dark' ? t('settings.light') : t('settings.dark')}</TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-hoverbg hover:text-strong"
              aria-label={t('settings.title')}
              onClick={() => switchView('settings')}
            >
              <Settings className="size-4" />
            </button>
          </TooltipTrigger>
          <TooltipContent>{t('settings.title')}</TooltipContent>
        </Tooltip>
      </div>
    </aside>
  );
}

export { sessionStatus, DOT_CLASS };
