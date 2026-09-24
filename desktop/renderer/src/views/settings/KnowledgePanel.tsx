// 知识库面板:目录知识库的创建/编辑/扫描/删除与 MCP 服务器配置。所有
// 数据经 ACP 投影;知识源目录只读,删除只移除配置与索引。

import { useCallback, useEffect, useState } from 'react';
import { Folder, Plus } from 'lucide-react';

import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { t } from '@/core/i18n';
import {
  applyKnowledgeBaseMcp,
  clearKnowledgeBase,
  createKnowledgeBase,
  deleteKnowledgeBase,
  knowledgeBaseDefaults,
  knowledgeBaseModels,
  loadKnowledgeBaseRuns,
  loadKnowledgeBaseSchedule,
  loadKnowledgeBaseSources,
  loadKnowledgeBases,
  loadMcp,
  scanKnowledgeBase,
  setKnowledgeBaseScheduleEnabled,
  updateKnowledgeBase,
  type KnowledgeBaseDefaults,
  type KnowledgeBaseScheduleView,
  type KnowledgeBaseSpec,
  type KnowledgeBaseView,
  type KnowledgeIndexRunView,
  type KnowledgeSourceView,
  type McpServerView,
  knowledgeBaseMcpName,
} from '@/core/manage-api';
import { desktop } from '@/core/api';
import { hasFeature } from '@/core/state';
import { confirmDanger, toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';

function knowledgeBaseStatus(view: KnowledgeBaseView): string {
  const base = view.knowledgeBase;
  if (!base.enabled) return t('settings.knowledgeDisabled');
  if (!view.snapshot) return t('settings.knowledgeUnindexed');
  const status = view.status || view.snapshot.status;
  return t('settings.knowledgeStatus', { s: status || t('settings.knowledgeUnindexed') });
}

function formatKnowledgeDate(value?: string): string {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function formatKnowledgeSize(bytes: number): string {
  const size = Number(bytes) || 0;
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

// KnowledgeBaseInsights projects the additive run-history and source-browsing
// ACP methods for one knowledge base. It never reads the source directory and
// gates each tab on its own capability so an older runtime degrades cleanly.
function KnowledgeBaseInsights({ baseId }: { baseId: string }) {
  const runsSupported = hasFeature('knowledgeRuns');
  const sourcesSupported = hasFeature('knowledgeSources');
  const [runs, setRuns] = useState<KnowledgeIndexRunView[]>([]);
  const [sources, setSources] = useState<KnowledgeSourceView[]>([]);
  const [tab, setTab] = useState('runs');
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const [nextRuns, nextSources] = await Promise.all([
      runsSupported ? loadKnowledgeBaseRuns(baseId) : Promise.resolve([]),
      sourcesSupported ? loadKnowledgeBaseSources(baseId) : Promise.resolve([]),
    ]);
    setRuns(nextRuns);
    setSources(nextSources);
  }, [baseId, runsSupported, sourcesSupported]);

  useEffect(() => {
    void load();
  }, [load]);

  const clear = async () => {
    if (!await confirmDanger(t('settings.knowledgeClearConfirm'))) return;
    setBusy(true);
    try {
      await clearKnowledgeBase(baseId);
      toast(t('settings.knowledgeCleared'));
      await load();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  if (!runsSupported && !sourcesSupported) return null;

  return (
    <div className="mt-3 rounded-[14px] border border-border bg-background p-3">
      <Tabs value={tab} onValueChange={setTab} className="gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <TabsList>
            {runsSupported ? <TabsTrigger value="runs">{t('settings.knowledgeRuns')}</TabsTrigger> : null}
            {sourcesSupported ? <TabsTrigger value="sources">{t('settings.knowledgeSources')}</TabsTrigger> : null}
          </TabsList>
          <Button variant="outline" size="sm" disabled={busy} onClick={() => void clear()}>
            {t('settings.knowledgeClear')}
          </Button>
        </div>
        {runsSupported ? (
          <TabsContent value="runs" className="flex flex-col gap-2">
            {runs.length === 0 ? (
              <div className="text-[11.5px] text-muted-foreground">{t('settings.knowledgeRunsEmpty')}</div>
            ) : runs.map((run) => (
              <div key={run.runId} className="flex flex-col gap-0.5 border-b border-border pb-2 last:border-0 last:pb-0">
                <div className="flex items-center gap-2 text-[12.5px] font-semibold text-strong">
                  <span>{run.status}</span>
                  {run.active ? (
                    <span className="rounded-full border border-border px-1.5 text-[10.5px] font-medium text-muted-foreground">
                      {t('settings.knowledgeRunActive')}
                    </span>
                  ) : null}
                </div>
                <div className="text-[11.5px] text-muted-foreground">
                  {formatKnowledgeDate(run.startedAt)}{run.finishedAt ? ` → ${formatKnowledgeDate(run.finishedAt)}` : ''}
                </div>
                <div className="text-[11.5px] text-muted-foreground">
                  {t('settings.knowledgeRunStats', { f: run.fileCount, c: run.chunkCount, n: run.nodeCount, e: run.edgeCount })}
                </div>
                {run.errorSummary ? (
                  <div className="text-[11.5px] text-[color:var(--destructive)]">
                    {t('settings.knowledgeRunError', { s: run.errorSummary })}
                  </div>
                ) : null}
              </div>
            ))}
          </TabsContent>
        ) : null}
        {sourcesSupported ? (
          <TabsContent value="sources" className="flex flex-col gap-2">
            {sources.length === 0 ? (
              <div className="text-[11.5px] text-muted-foreground">{t('settings.knowledgeSourcesEmpty')}</div>
            ) : sources.map((source) => (
              <div key={source.relativePath} className="flex flex-col gap-0.5 border-b border-border pb-2 last:border-0 last:pb-0">
                <div className="text-[12.5px] font-semibold text-strong">{source.title || source.relativePath}</div>
                <div className="break-all text-[11.5px] text-muted-foreground">{source.relativePath}</div>
                <div className="text-[11.5px] text-muted-foreground">
                  {t('settings.knowledgeSourceMeta', { status: source.status, size: formatKnowledgeSize(source.byteSize), chunks: source.chunkCount })}
                </div>
              </div>
            ))}
          </TabsContent>
        ) : null}
      </Tabs>
    </div>
  );
}

// 后台扫描进行中时显示周期轮询拿到的进度,而不是阻塞等待扫描结束。
function knowledgeStatusText(view: KnowledgeBaseView): string {
  const indexing = view.indexing;
  if (indexing?.running) {
    const label = t('library.knowledgeIndexing', { p: indexing.phase || '' });
    return indexing.filesTotal > 0 ? `${label} · ${indexing.filesDone}/${indexing.filesTotal}` : label;
  }
  return knowledgeBaseStatus(view);
}

// KnowledgeBaseScheduleStatus projects the Runtime-owned schedule state: the
// persisted cadence, next run, last result, and a pause/resume action that
// persists through ACP. Desktop owns no scheduler state.
function KnowledgeBaseScheduleStatus({ baseId, onChanged }: { baseId: string; onChanged: () => void }) {
  const [schedule, setSchedule] = useState<KnowledgeBaseScheduleView | undefined>(undefined);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const loaded = await loadKnowledgeBaseSchedule(baseId);
      if (!cancelled) setSchedule(loaded);
    })();
    return () => {
      cancelled = true;
    };
  }, [baseId]);

  if (!schedule) return null;

  const toggle = async () => {
    setBusy(true);
    try {
      const updated = await setKnowledgeBaseScheduleEnabled(baseId, !schedule.enabled);
      setSchedule(updated);
      toast(t('settings.knowledgeScheduleSaved'));
      onChanged();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 text-[11.5px] text-muted-foreground">
      <span>
        {schedule.schedule
          ? schedule.configured && schedule.nextRun
            ? t('settings.knowledgeScheduleNext', { s: formatKnowledgeDate(schedule.nextRun) })
            : t('settings.knowledgeScheduleManual')
          : t('settings.knowledgeScheduleManual')}
      </span>
      {schedule.lastStatus ? (
        <span>{t('settings.knowledgeScheduleLast', { s: schedule.lastStatus })}</span>
      ) : null}
      {!schedule.rootAvailable ? (
        <span className="text-[color:var(--destructive)]">{t('settings.knowledgeScheduleRootUnavailable')}</span>
      ) : null}
      <Button variant="outline" size="sm" disabled={busy} onClick={() => void toggle()}>
        {schedule.enabled ? t('settings.knowledgeSchedulePause') : t('settings.knowledgeScheduleResume')}
      </Button>
    </div>
  );
}

function KnowledgeBaseEditor({
  view,
  defaults,
  creating,
  mcpServers,
  onDone,
  onCancel,
}: {
  view?: KnowledgeBaseView;
  defaults: KnowledgeBaseDefaults;
  creating: boolean;
  mcpServers: McpServerView[];
  onDone: () => void;
  onCancel?: () => void;
}) {
  const base = view?.knowledgeBase;
  const source: KnowledgeBaseSpec = base ? { ...base } : { ...defaults.spec };
  const [spec, setSpec] = useState<KnowledgeBaseSpec>(source);
  const [busy, setBusy] = useState<string | null>(null);

  const set = <K extends keyof KnowledgeBaseSpec>(key: K, value: KnowledgeBaseSpec[K]) =>
    setSpec((current) => ({ ...current, [key]: value }));

  const modelChoices = knowledgeBaseModels(defaults.models, spec.provider);
  const effectiveModel = modelChoices.includes(spec.model) ? spec.model : modelChoices[0] || '';

  const mcpName = base ? knowledgeBaseMcpName(base.id) : '';
  const mcpEntry = base ? mcpServers.find((server) => server.name === mcpName) : undefined;
  const mcpWillEnable = !mcpEntry || mcpEntry.enabled === false;

  const save = async () => {
    setBusy('save');
    try {
      const payload: KnowledgeBaseSpec = { ...spec, model: effectiveModel };
      if (creating) {
        await createKnowledgeBase(payload);
        toast(t('settings.knowledgeCreated'));
      } else if (base) {
        await updateKnowledgeBase(base.id, payload);
        toast(t('settings.knowledgeSaved'));
      }
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(null);
    }
  };

  const scan = async () => {
    if (!base) return;
    setBusy('scan');
    try {
      // 扫描 RPC 现在立即返回(后台作业已启动),进度由面板周期轮询。
      await scanKnowledgeBase(base.id);
      toast(t('settings.knowledgeScanStarted'));
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(null);
    }
  };

  const remove = async () => {
    if (!base) return;
    if (!await confirmDanger(t('settings.knowledgeDeleteConfirm', { n: base.name }))) return;
    setBusy('delete');
    try {
      await deleteKnowledgeBase(base.id);
      toast(t('settings.knowledgeDeleted'));
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
      setBusy(null);
    }
  };

  const applyMcp = async () => {
    if (!base) return;
    setBusy('mcp');
    try {
      await applyKnowledgeBaseMcp(base.id, mcpWillEnable);
      toast(t('settings.knowledgeMcpSaved'));
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
      setBusy(null);
    }
  };

  return (
    <ManageCard title={base ? base.name : t('settings.knowledgeNew')} desc={base ? knowledgeStatusText(view!) : t('settings.knowledgeNewDesc')}>
      <FieldGrid>
        <Field label={t('settings.knowledgeName')}>
          <Input value={spec.name} onChange={(event) => set('name', event.target.value)} />
        </Field>
        <div className="col-span-full flex min-w-0 items-end gap-2 max-[760px]:col-auto">
          <label className="flex min-w-0 flex-1 flex-col gap-1.5 text-[11px] font-semibold text-muted-foreground">
            <span>{t('settings.knowledgeRootDir')}</span>
            <Input value={spec.rootDir} onChange={(event) => set('rootDir', event.target.value)} />
          </label>
          <Button
            variant="outline"
            className="shrink-0"
            onClick={async () => {
              const selected = await desktop.chooseDirectory(spec.rootDir.trim() || undefined);
              if (selected) set('rootDir', selected);
            }}
          >
            <Folder />
            {t('settings.knowledgeChooseRoot')}
          </Button>
        </div>
        <Field label={t('settings.knowledgeProfile')}>
          <OptionSelect value={spec.preprocessProfile} options={['documents', 'code', 'notes', 'mixed']} onChange={(value) => set('preprocessProfile', value)} />
        </Field>
        <Field label={t('settings.knowledgeProvider')}>
          <OptionSelect value={spec.provider} options={defaults.providers} onChange={(value) => set('provider', value)} />
        </Field>
        <Field label={t('settings.knowledgeModel')}>
          <OptionSelect value={effectiveModel} options={modelChoices} onChange={(value) => set('model', value)} />
        </Field>
        <Field label={t('settings.knowledgeMode')}>
          <OptionSelect value={spec.mode} options={['yolo', 'agent', 'plan', 'os']} onChange={(value) => set('mode', value)} />
        </Field>
        <Field label={t('settings.knowledgeThinking')}>
          <Input value={spec.thinkingLevel || ''} onChange={(event) => set('thinkingLevel', event.target.value)} />
        </Field>
        <Field label={t('settings.knowledgeSchedule')}>
          <Input placeholder="manual / hourly / daily / @every 6h / 5-field cron" value={spec.schedule} onChange={(event) => set('schedule', event.target.value)} />
        </Field>
        <ToggleField label={t('settings.knowledgeEnabled')} checked={spec.enabled !== false} onChange={(value) => set('enabled', value)} />
      </FieldGrid>

      {view?.snapshot ? (
        <div className="mt-2 text-[11.5px] text-muted-foreground">
          {t('settings.knowledgeStats', {
            f: view.snapshot.fileCount ?? 0,
            c: view.snapshot.chunkCount ?? 0,
            n: view.snapshot.nodeCount ?? 0,
            e: view.snapshot.edgeCount ?? 0,
          })}
        </div>
      ) : null}

      {base ? <KnowledgeBaseScheduleStatus baseId={base.id} onChanged={onDone} /> : null}

      {base && hasFeature('manageMcp') ? (
        <div className="mt-3 rounded-[14px] border border-border bg-background p-3">
          <div className="text-[14px] font-bold leading-snug text-strong">{t('settings.knowledgeMcpTitle')}</div>
          <div className="mt-1 text-[11.5px] leading-snug text-muted-foreground">{t('settings.knowledgeMcpDesc')}</div>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <span className="text-[11.5px] text-muted-foreground">
              {mcpEntry
                ? mcpEntry.enabled === false
                  ? t('settings.knowledgeMcpDisabled')
                  : t('settings.knowledgeMcpEnabled')
                : t('settings.knowledgeMcpNotConfigured')}
            </span>
            <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => void applyMcp()}>
              {mcpWillEnable
                ? mcpEntry
                  ? t('settings.knowledgeMcpEnable')
                  : t('settings.knowledgeMcpConfigure')
                : t('settings.knowledgeMcpDisable')}
            </Button>
          </div>
          <div className="mt-2 text-[11.5px] text-muted-foreground">{t('settings.knowledgeMcpHint')}</div>
        </div>
      ) : null}

      {base && (hasFeature('knowledgeRuns') || hasFeature('knowledgeSources')) ? (
        <KnowledgeBaseInsights baseId={base.id} />
      ) : null}

      <div className="mt-3 flex flex-wrap items-center justify-end gap-[7px]">
        <Button disabled={busy !== null} onClick={() => void save()}>
          {busy === 'save' ? '…' : creating ? t('settings.knowledgeCreate') : t('settings.knowledgeSave')}
        </Button>
        {onCancel ? (
          <Button variant="outline" onClick={onCancel}>
            {t('modal.cancel')}
          </Button>
        ) : null}
        {base ? (
          <>
            <Button variant="outline" disabled={busy !== null || spec.enabled === false} onClick={() => void scan()}>
              {busy === 'scan' ? '…' : t('settings.knowledgeScan')}
            </Button>
            <Button variant="deny" disabled={busy !== null} onClick={() => void remove()}>
              {t('settings.knowledgeDelete')}
            </Button>
          </>
        ) : null}
      </div>
    </ManageCard>
  );
}

export function KnowledgePanel() {
  const appState = useAppState();
  const ready = appState.connection.state === 'ready';
  const supported = hasFeature('manageKnowledgeBases');
  const [views, setViews] = useState<KnowledgeBaseView[] | null>(null);
  const [defaults, setDefaults] = useState<KnowledgeBaseDefaults | null>(null);
  const [mcpServers, setMcpServers] = useState<McpServerView[]>([]);
  const [creating, setCreating] = useState(false);

  const reload = useCallback(async () => {
    const [loaded, loadedDefaults] = await Promise.all([loadKnowledgeBases(), knowledgeBaseDefaults()]);
    setViews(loaded || []);
    setDefaults(loadedDefaults);
    if (hasFeature('manageMcp')) {
      const servers = await loadMcp();
      setMcpServers(servers || []);
    }
  }, []);

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void (async () => {
      const [loaded, loadedDefaults] = await Promise.all([loadKnowledgeBases(), knowledgeBaseDefaults()]);
      if (cancelled) return;
      setViews(loaded || []);
      setDefaults(loadedDefaults);
      if (hasFeature('manageMcp')) {
        const servers = await loadMcp();
        if (!cancelled) setMcpServers(servers || []);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  // 任一知识库在后台扫描时周期轮询进度,直到作业结束。
  const anyIndexing = (views || []).some((view) => view.indexing?.running);
  useEffect(() => {
    if (!anyIndexing) return;
    const timer = window.setInterval(() => void reload(), 3000);
    return () => window.clearInterval(timer);
  }, [anyIndexing, reload]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!views || !defaults) return <UnsupportedRow text="…" />;

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.knowledge')}
        title={t('settings.knowledgeTitle')}
        desc={t('settings.knowledgeDesc')}
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus />
            {t('settings.knowledgeAdd')}
          </Button>
        }
      />
      {views.map((view) => (
        <KnowledgeBaseEditor
          key={view.knowledgeBase.id}
          view={view}
          defaults={defaults}
          creating={false}
          mcpServers={mcpServers}
          onDone={() => {
            setCreating(false);
            void reload();
          }}
        />
      ))}
      {creating ? (
        <KnowledgeBaseEditor
          defaults={defaults}
          creating
          mcpServers={mcpServers}
          onDone={() => {
            setCreating(false);
            void reload();
          }}
          onCancel={() => setCreating(false)}
        />
      ) : null}
      {!creating && views.length === 0 ? <div className="text-[11.5px] text-muted-foreground">{t('settings.knowledgeEmpty')}</div> : null}
    </ManageWorkspace>
  );
}
