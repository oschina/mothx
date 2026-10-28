// 技能视图:本地技能/指令卡片网格(available_commands_update 投影)+
// SkillHub / 源盾 / ClawHub 在线市场(ACP-only 投影,数据与安装全部经过 ACP)。

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ChevronRight,
  Download,
  Loader2,
  Package,
  Search,
  Slash,
  Sparkles,
  Star,
  Store,
  Zap,
} from 'lucide-react';

import { EmptyState, PageHead, PageInner, PageScroll } from '@/components/layout';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { injectToComposer } from '@/core/composer';
import { t } from '@/core/i18n';
import {
  activateSkill,
  commandKind,
  installSkill,
  loadCatalogBootstrap,
  loadCategories,
  loadSkillDetail,
  searchSkills,
  skillTitle,
  uninstallSkill,
  type CategoryEntry,
  type Market,
  type Skill,
  type Target,
} from '@/core/skillhub';
import { hasFeature, state } from '@/core/state';
import { switchView } from '@/core/views';
import { useAppState } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

const SORT_OPTIONS = [
  { value: 'downloads', label: 'skills.sortDownloads' },
  { value: 'stars', label: 'skills.sortStars' },
  { value: 'updated_at', label: 'skills.sortLatest' },
] as const;

function formatCompact(value?: number): string {
  const count = Number(value || 0);
  return count >= 1000 ? `${(count / 1000).toFixed(1)}k` : String(count);
}

function skillInitials(skill: Skill): string {
  const text = skillTitle(skill).trim();
  return text ? text.slice(0, 1).toUpperCase() : '?';
}

function securityBadge(skill: Skill): { label: string; variant: 'success' | 'danger' | 'warning' | 'secondary' } | null {
  const report = skill.securityReports?.yuandun as { status?: string } | undefined;
  switch (skill.securityStatus || report?.status) {
    case 'SAFE':
      return { label: t('skills.securitySafe'), variant: 'success' };
    case 'RISKY':
      return { label: t('skills.securityRisky'), variant: 'danger' };
    case 'SUSPECTED':
      return { label: t('skills.securitySuspected'), variant: 'danger' };
    case 'AUDITING':
      return { label: t('skills.securityAuditing'), variant: 'warning' };
    case 'NOT_AUDITED':
      return { label: t('skills.securityNotAudited'), variant: 'secondary' };
    default:
      break;
  }
  if (skill.suspicious) return { label: t('skills.securityRisky'), variant: 'danger' };
  return null;
}

function formatJSON(value: unknown): string {
  if (!value || typeof value !== 'object') return t('skills.notAvailable');
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return t('skills.notAvailable');
  }
}

function SkillIcon({ skill, className }: { skill: Skill; className?: string }) {
  const [imageFailed, setImageFailed] = useState(false);
  return (
    <span
      className={cn(
        'flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-[14px] border border-border bg-placeholder text-primary',
        className,
      )}
      aria-hidden="true"
    >
      {skill.iconUrl && !imageFailed ? (
        <img src={skill.iconUrl} alt="" className="size-full object-cover" onError={() => setImageFailed(true)} />
      ) : (
        <span className="text-[15px] font-bold">{skillInitials(skill)}</span>
      )}
    </span>
  );
}

function SkillCards() {
  const appState = useAppState();
  if (appState.availableCommands.length === 0) {
    return <EmptyState icon={<Zap />} title={t('skills.empty')} />;
  }
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-3">
      {appState.availableCommands.map((command) => {
        const kind = commandKind(command);
        return (
          <div
            key={command.name}
            className="relative cursor-pointer rounded-xl border border-border bg-card p-3.5 transition-all hover:-translate-y-px hover:border-primary hover:shadow-float"
            role="button"
            tabIndex={0}
            onClick={() => {
              injectToComposer(command.name.startsWith('/') ? `${command.name} ` : `/${command.name} `);
              switchView(state.activeSessionId ? 'chat' : 'home');
            }}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                injectToComposer(command.name.startsWith('/') ? `${command.name} ` : `/${command.name} `);
                switchView(state.activeSessionId ? 'chat' : 'home');
              }
            }}
          >
            <div className="mb-2.5 flex size-[38px] items-center justify-center rounded-[10px] bg-primary/8 text-primary">
              {kind === 'skill' ? <Zap className="size-5" /> : <Slash className="size-5" />}
            </div>
            <div className="mb-1 flex items-center gap-1.5 text-[13.5px] font-semibold text-strong">
              <span className="truncate">{command.name}</span>
              <Badge variant={kind === 'skill' ? 'accent' : 'info'}>
                {kind === 'skill' ? t('skills.kindSkill') : t('skills.kindCommand')}
              </Badge>
            </div>
            <div className="line-clamp-3 text-[12px] leading-normal text-muted-foreground">{command.description || ''}</div>
            <div className="mt-2.5 flex items-center gap-2 text-[11px] text-primary">{t('skills.use')}</div>
          </div>
        );
      })}
    </div>
  );
}

function SkillMarketCard({ skill, selected, onSelect }: { skill: Skill; selected: boolean; onSelect: () => void }) {
  const security = securityBadge(skill);
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        'group flex h-full min-w-0 flex-col rounded-xl border bg-card p-4 text-left shadow-panel transition-all duration-200',
        selected
          ? 'border-primary/50 shadow-float ring-2 ring-primary/12'
          : 'border-border hover:-translate-y-0.5 hover:border-primary/35 hover:shadow-float',
      )}
    >
      <span className="flex items-start gap-3">
        <SkillIcon skill={skill} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13.5px] font-semibold text-strong">{skillTitle(skill)}</span>
          <span className="mt-1 block truncate text-[10.5px] text-muted-foreground">
            {skill.author || skill.market}
            {skill.version ? <span className="ml-1 font-mono text-faint">v{skill.version}</span> : null}
          </span>
        </span>
        {security ? <Badge variant={security.variant} className="shrink-0">{security.label}</Badge> : null}
      </span>
      <span className="mt-3 line-clamp-2 min-h-[34px] text-[11.5px] leading-[1.5] text-muted-foreground">
        {skill.description || t('skills.noDescription')}
      </span>
      <span className="mt-auto flex flex-wrap items-center gap-2 pt-3 text-[10.5px] text-muted-foreground">
        <span className="inline-flex items-center gap-1">
          <Star className="size-3.5 opacity-70" />
          {formatCompact(skill.stars)}
        </span>
        <span className="inline-flex items-center gap-1">
          <Download className="size-3.5 opacity-70" />
          {formatCompact(skill.downloads)}
        </span>
        {skill.category ? (
          <span className="rounded-full border border-border px-1.5 py-0.5 text-[10px]">{skill.category}</span>
        ) : null}
        {skill.installed?.installed ? (
          <Badge variant="info">
            {skill.installed.updateAvailable ? t('skills.updateAvailable') : t('skills.installed')}
          </Badge>
        ) : null}
        <ChevronRight className="ml-auto size-3.5 opacity-0 transition-opacity group-hover:opacity-100" />
      </span>
    </button>
  );
}

function SkillMarketSkeleton() {
  return <div className="h-[150px] animate-pulse rounded-xl border border-border bg-card" />;
}

function SkillHubCatalog() {
  const appState = useAppState();
  const sessionId = appState.activeSessionId;
  const serialRef = useRef(0);
  const [markets, setMarkets] = useState<Market[]>([]);
  const [targets, setTargets] = useState<Target[]>([]);
  const [market, setMarket] = useState('');
  const [targetDir, setTargetDir] = useState('');
  const [scope, setScope] = useState<'project' | 'global'>('project');
  const [activeSkills, setActiveSkills] = useState<string[]>([]);
  const [categories, setCategories] = useState<CategoryEntry[]>([]);
  const [category, setCategory] = useState('');
  const [sort, setSort] = useState('downloads');
  const [query, setQuery] = useState('');
  const [items, setItems] = useState<Skill[] | null>(null);
  const [detail, setDetail] = useState<Skill | null>(null);
  const [catalogError, setCatalogError] = useState('');

  const activeSkillsRef = useRef(activeSkills);
  activeSkillsRef.current = activeSkills;

  const loadResults = useCallback(
    async (chosenMarket: string, text: string, chosenCategory: string, chosenSort: string, official: boolean) => {
      if (!sessionId) return;
      const serial = ++serialRef.current;
      setItems(null);
      try {
        const found = await searchSkills({
          sessionId,
          market: chosenMarket,
          query: text,
          category: chosenCategory,
          sort: chosenSort,
          official,
        });
        if (serial !== serialRef.current || state.activeSessionId !== sessionId) return;
        setItems(found);
        if (found[0]) {
          const first = await loadSkillDetail(sessionId, chosenMarket, found[0].id);
          if (serial === serialRef.current && state.activeSessionId === sessionId) setDetail(first);
        } else {
          setDetail(null);
        }
      } catch (error) {
        if (serial !== serialRef.current) return;
        setCatalogError(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
        setItems([]);
      }
    },
    [sessionId],
  );

  useEffect(() => {
    if (!sessionId) {
      setMarkets([]);
      setItems(null);
      setDetail(null);
      return;
    }
    const serial = ++serialRef.current;
    setCatalogError('');
    setItems(null);
    setDetail(null);
    void (async () => {
      try {
        const bootstrap = await loadCatalogBootstrap(sessionId, market, targetDir, scope);
        if (serial !== serialRef.current || state.activeSessionId !== sessionId) return;
        setMarkets(bootstrap.markets);
        setTargets(bootstrap.targets);
        setActiveSkills(bootstrap.activeSkills);
        const nextMarket = bootstrap.market;
        const nextTarget = bootstrap.target;
        setMarket(nextMarket);
        setTargetDir(nextTarget?.path || '');
        setScope(nextTarget?.scope || 'project');
        setSort('downloads');
        const chosen = bootstrap.markets.find((entry) => entry.id === nextMarket);
        setCategories(chosen?.capabilities?.categories ? await loadCategories(sessionId, nextMarket) : []);
        setCategory('');
        await loadResults(nextMarket, query, '', 'downloads', nextMarket === 'skillhub.cn');
      } catch (error) {
        if (serial !== serialRef.current) return;
        setCatalogError(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
        setItems([]);
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId]);

  if (!hasFeature('manageSkillHubCatalog')) return null;

  if (!sessionId) {
    return (
      <section className="mt-[26px] border-t border-border pt-[22px]" aria-live="polite">
        <div className="relative overflow-hidden rounded-2xl border border-border bg-card p-5 shadow-panel">
          <div className="pointer-events-none absolute inset-x-0 -top-24 h-48 bg-[radial-gradient(ellipse_at_top,var(--brand-glow),transparent_70%)] opacity-50" />
          <div className="relative flex items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-primary/15 bg-primary/8 text-primary">
              <Store className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="text-[17px] font-semibold text-strong">{t('skills.heroTitle')}</div>
              <div className="mt-1 text-[12px] leading-relaxed text-muted-foreground">{t('skills.heroSubtitle')}</div>
            </div>
          </div>
          <div className="relative mt-4 rounded-xl border border-dashed border-border bg-background/60 px-5 py-6 text-center">
            <div className="text-[13px] font-semibold text-muted-foreground">{t('skills.marketplaceSessionRequired')}</div>
            <Button className="mt-3" onClick={() => switchView('sessions')}>
              {t('skills.openSession')}
            </Button>
          </div>
        </div>
      </section>
    );
  }

  const currentMarket = markets.find((entry) => entry.id === market);
  const isOfficialMarket = market === 'skillhub.cn';
  const supportsSort = market !== 'clawhub.ai';
  const security = detail ? securityBadge(detail) : null;

  const openDetail = async (skill: Skill) => {
    if (!sessionId) return;
    try {
      const full = await loadSkillDetail(sessionId, skill.market, skill.id);
      if (state.activeSessionId === sessionId) setDetail(full);
    } catch (error) {
      setCatalogError(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  const refreshAfterMutation = async () => {
    if (!sessionId) return;
    const bootstrap = await loadCatalogBootstrap(sessionId, market, targetDir, scope);
    setMarkets(bootstrap.markets);
    setTargets(bootstrap.targets);
    setActiveSkills(bootstrap.activeSkills);
    await loadResults(market, query, category, sort, false);
  };

  return (
    <section className="mt-[26px] border-t border-border pt-[22px]" aria-live="polite">
      <div className="relative overflow-hidden rounded-2xl border border-border bg-card p-5 shadow-panel">
        <div className="pointer-events-none absolute inset-x-0 -top-24 h-48 bg-[radial-gradient(ellipse_at_top,var(--brand-glow),transparent_70%)] opacity-50" />
        <div className="relative">
          <div className="flex items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-primary/15 bg-primary/8 text-primary">
              <Store className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="text-[17px] font-semibold text-strong">{t('skills.heroTitle')}</div>
              <div className="mt-1 text-[12px] leading-relaxed text-muted-foreground">{t('skills.heroSubtitle')}</div>
            </div>
          </div>

          <form
            className="mt-4 flex items-center gap-2 rounded-full border border-border bg-background/80 px-3 py-1.5 transition-colors focus-within:border-primary/40 focus-within:ring-2 focus-within:ring-primary/10"
            onSubmit={(event) => {
              event.preventDefault();
              void loadResults(market, query, category, sort, false);
            }}
          >
            <Search className="size-4 shrink-0 text-faint" />
            <Input
              className="h-7 flex-1 border-0 bg-transparent px-0 text-[12.5px] shadow-none focus-visible:ring-0"
              placeholder={t('skills.marketplaceSearch')}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <Button type="submit" size="sm" className="rounded-full px-3">
              {t('skills.search')}
            </Button>
          </form>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            {markets.map((entry) => (
              <button
                key={entry.id}
                type="button"
                className={cn(
                  'rounded-full border px-3 py-1.5 text-[11.5px] font-medium transition-colors',
                  market === entry.id
                    ? 'border-primary/40 bg-primary/8 text-primary'
                    : 'border-border bg-background text-muted-foreground hover:bg-hoverbg hover:text-strong',
                )}
                onClick={() => {
                  setMarket(entry.id);
                  setCategories([]);
                  setCategory('');
                  setSort('downloads');
                  void (async () => {
                    const chosen = markets.find((item) => item.id === entry.id);
                    if (chosen?.capabilities?.categories) setCategories(await loadCategories(sessionId || '', entry.id));
                    await loadResults(entry.id, query, '', 'downloads', entry.id === 'skillhub.cn');
                  })();
                }}
              >
                {entry.name || entry.id}
              </button>
            ))}
            {isOfficialMarket ? (
              <Button
                variant="outline"
                size="sm"
                className="rounded-full"
                onClick={() => void loadResults(market, query, category, sort, true)}
              >
                <Sparkles className="size-3.5" />
                {t('skills.official')}
              </Button>
            ) : null}
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-2">
            {supportsSort ? (
              <div className="flex items-center gap-1 rounded-full border border-border bg-secondary/50 p-0.5">
                {SORT_OPTIONS.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    className={cn(
                      'rounded-full px-3 py-1 text-[11px] transition-colors',
                      sort === option.value
                        ? 'bg-card text-strong shadow-panel'
                        : 'text-muted-foreground hover:text-strong',
                    )}
                    onClick={() => {
                      setSort(option.value);
                      void loadResults(market, query, category, option.value, false);
                    }}
                  >
                    {t(option.label)}
                  </button>
                ))}
              </div>
            ) : null}
            {currentMarket?.capabilities?.categories ? (
              <Select
                value={category || '__all'}
                onValueChange={(value) => {
                  const next = value === '__all' ? '' : value;
                  setCategory(next);
                  void loadResults(market, query, next, sort, false);
                }}
              >
                <SelectTrigger className="h-8 min-w-[130px] rounded-full bg-background px-3 text-[11.5px]">
                  <SelectValue placeholder={t('skills.allCategories')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__all">{t('skills.allCategories')}</SelectItem>
                  {categories.map((entry) => (
                    <SelectItem key={entry.key} value={entry.key}>
                      {entry.nameEn || entry.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
          </div>
        </div>
      </div>

      {catalogError ? <div className="mt-3 text-[11.5px] text-danger">{catalogError}</div> : null}

      <div className="mt-4 grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 text-[13px] font-semibold text-strong">
              {t('skills.results')}
              <span className="text-[11px] font-normal text-muted-foreground">{items?.length ?? 0}</span>
            </div>
            {items === null ? <Loader2 className="size-4 animate-spin text-faint" /> : null}
          </div>

          {items === null ? (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {Array.from({ length: 4 }).map((_, index) => (
                <SkillMarketSkeleton key={index} />
              ))}
            </div>
          ) : items.length === 0 ? (
            <div className="rounded-xl border border-dashed border-border bg-card/60 py-12 text-center text-[12.5px] text-muted-foreground">
              {t('skills.marketplaceEmpty')}
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {items.map((skill) => (
                <SkillMarketCard
                  key={`${skill.market}:${skill.id}`}
                  skill={skill}
                  selected={selectedId(detail) === `${skill.market}:${skill.id}`}
                  onSelect={() => void openDetail(skill)}
                />
              ))}
            </div>
          )}
        </div>

        <aside className="min-w-0 xl:sticky xl:top-4 xl:self-start">
          {detail === null ? (
            <div className="rounded-2xl border border-dashed border-border bg-card/50 p-6 text-center text-[12px] text-muted-foreground">
              {t('skills.select')}
            </div>
          ) : (
            <div className="rounded-2xl border border-border bg-card p-4 shadow-panel">
              <div className="flex items-start gap-3">
                <SkillIcon skill={detail} className="size-12 rounded-[16px]" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[16px] font-semibold text-strong">{skillTitle(detail)}</div>
                  <div className="mt-1 truncate text-[11px] text-muted-foreground">
                    {detail.author || detail.market}
                    {detail.version ? ` · v${detail.version}` : ''}
                  </div>
                </div>
                {security ? <Badge variant={security.variant}>{security.label}</Badge> : null}
              </div>

              <div className="mt-3 text-[11.5px] leading-relaxed text-muted-foreground">
                {detail.description || t('skills.noDescription')}
              </div>

              {detail.category || detail.tags?.length ? (
                <div className="mt-3 flex flex-wrap gap-1.5">
                  {detail.category ? (
                    <span className="rounded-full border border-border px-2 py-0.5 text-[10px] text-muted-foreground">
                      {detail.category}
                    </span>
                  ) : null}
                  {(detail.tags || []).slice(0, 4).map((tag) => (
                    <span key={tag} className="rounded-full border border-border px-2 py-0.5 text-[10px] text-muted-foreground">
                      {tag}
                    </span>
                  ))}
                </div>
              ) : null}

              <div className="mt-4 rounded-xl border border-border bg-secondary/40 p-3">
                <div className="flex items-center justify-between gap-2">
                  <div className="text-[10.5px] font-semibold uppercase tracking-[.08em] text-muted-foreground">
                    {t('skills.installTarget')}
                  </div>
                  <span className="text-[10px] text-faint">
                    {scope === 'global' ? t('skills.scopeGlobal') : t('skills.scopeProject')}
                  </span>
                </div>
                <Select
                  value={targetDir}
                  onValueChange={(value) => {
                    const found = targets.find((entry) => entry.path === value);
                    setTargetDir(found?.path || '');
                    setScope(found?.scope || 'project');
                  }}
                >
                  <SelectTrigger className="mt-2 h-8 w-full bg-card text-[11.5px]">
                    <SelectValue placeholder={t('skills.targetRequired')} />
                  </SelectTrigger>
                  <SelectContent>
                    {targets.map((entry) => (
                      <SelectItem key={entry.path} value={entry.path}>
                        {`${entry.label} · ${entry.scope}`}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <div className="mt-2 truncate font-mono text-[10px] text-faint">{targetDir || t('skills.targetRequired')}</div>
              </div>

              <div className="mt-3 flex flex-wrap gap-2">
                <Button
                  className="flex-1"
                  onClick={async () => {
                    const installed = Boolean(detail.installed?.installed);
                    const ok = await installSkill({
                      sessionId: sessionId || '',
                      skill: detail,
                      overwrite: installed,
                      scope,
                      targetDir,
                    });
                    if (ok) await refreshAfterMutation();
                  }}
                >
                  {detail.installed?.installed ? t('skills.update') : t('skills.install')}
                </Button>
                {detail.installed?.installed && !activeSkills.includes(detail.installed?.name || detail.slug || detail.name || '') ? (
                  <Button
                    variant="outline"
                    onClick={async () => {
                      const next = await activateSkill(sessionId || '', detail);
                      if (next) setActiveSkills(next);
                    }}
                  >
                    {t('skills.activate')}
                  </Button>
                ) : null}
                {detail.installed?.installed ? (
                  <Button
                    variant="deny"
                    onClick={async () => {
                      const ok = await uninstallSkill(sessionId || '', detail, scope);
                      if (ok) await refreshAfterMutation();
                    }}
                  >
                    {t('skills.uninstall')}
                  </Button>
                ) : null}
              </div>

              <dl className="mt-4 grid grid-cols-2 gap-2 text-[11px]">
                {[
                  [t('skills.market'), detail.market],
                  [t('skills.version'), detail.version || t('skills.notAvailable')],
                  [t('skills.category'), detail.category || t('skills.notAvailable')],
                  [t('skills.author'), detail.author || t('skills.notAvailable')],
                  [t('skills.downloads'), formatCompact(detail.downloads)],
                  [t('skills.stars'), formatCompact(detail.stars)],
                ].map(([label, value]) => (
                  <div key={label} className="min-w-0 rounded-lg border border-border bg-background/60 px-2.5 py-2">
                    <dt className="text-[9.5px] font-semibold uppercase tracking-wide text-faint">{label}</dt>
                    <dd className="mt-0.5 truncate text-strong">{value}</dd>
                  </div>
                ))}
              </dl>

              <div className="mt-4">
                <div className="mb-2 flex items-center justify-between text-[11px] font-semibold text-strong">
                  <span>{t('skills.files')}</span>
                  <span className="text-faint">{detail.files?.length || 0}</span>
                </div>
                <div className="max-h-40 overflow-y-auto rounded-lg border border-border bg-background/60">
                  {detail.files?.length ? (
                    detail.files.slice(0, 8).map((file) => (
                      <div
                        key={file.path}
                        className="flex items-center justify-between gap-3 border-b border-border px-2.5 py-1.5 text-[10.5px] last:border-b-0"
                      >
                        <span className="truncate text-muted-foreground">{file.path}</span>
                        <span className="shrink-0 text-faint">{formatCompact(file.size)} B</span>
                      </div>
                    ))
                  ) : (
                    <div className="px-3 py-2 text-[10.5px] text-muted-foreground">{t('skills.notAvailable')}</div>
                  )}
                </div>
              </div>

              <details className="mt-3 rounded-lg border border-border bg-background/60">
                <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-strong">
                  {t('skills.security')}
                </summary>
                <pre className="max-h-48 overflow-auto border-t border-border p-2.5 text-[10px] leading-relaxed text-muted-foreground">
                  {formatJSON(detail.securityReports)}
                </pre>
              </details>

              <details className="mt-2 rounded-lg border border-border bg-background/60">
                <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-strong">
                  {t('skills.evaluation')}
                </summary>
                <pre className="max-h-48 overflow-auto border-t border-border p-2.5 text-[10px] leading-relaxed text-muted-foreground">
                  {formatJSON(detail.evaluation)}
                </pre>
              </details>

              <details className="mt-2 rounded-lg border border-border bg-background/60">
                <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-strong">
                  {t('skills.downloadSources')}
                </summary>
                <div className="border-t border-border p-2.5">
                  {detail.downloadSources?.length ? (
                    <div className="flex flex-col gap-1.5">
                      {detail.downloadSources.map((source) => (
                        <div key={source.url} className="flex min-w-0 items-center gap-2 text-[10px]">
                          <span className="shrink-0 rounded-full border border-border px-1.5 py-0.5 text-faint">
                            {source.kind}
                          </span>
                          <code className="truncate text-muted-foreground">{source.url}</code>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="text-[10.5px] text-muted-foreground">{t('skills.notAvailable')}</div>
                  )}
                </div>
              </details>
            </div>
          )}
        </aside>
      </div>
    </section>
  );
}

function selectedId(detail: Skill | null): string {
  return detail ? `${detail.market}:${detail.id}` : '';
}

export function SkillsView() {
  return (
    <PageScroll>
      <PageInner className="w-[min(1120px,94%)]">
        <PageHead title={t('skills.title')} subtitle={t('skills.subtitle')} />
        <SkillCards />
        <SkillHubCatalog />
      </PageInner>
    </PageScroll>
  );
}
