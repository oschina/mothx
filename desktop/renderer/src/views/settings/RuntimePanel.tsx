// 运行时(ACP)面板:连接状态与重启、mothx/doctor 诊断、本地诊断日志。
// 诊断日志是主进程状态,renderer 只保存当前过滤词与视图内快照,绝不落盘。

import { useEffect, useMemo, useState } from 'react';
import { Cpu, RefreshCw, Shield, Zap } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { acp, desktop, type DiagnosticLogEntry, type RuntimeBinaryStatus } from '@/core/api';
import { t } from '@/core/i18n';
import { mergeDiagnosticLogs } from '@/core/diagnostic-logs';
import { runDoctor, type DoctorCheck, type DoctorResult } from '@/core/manage-api';
import { emit, state } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

function connLabel(connectionState: string): string {
  switch (connectionState) {
    case 'ready': return t('conn.ready');
    case 'starting': return t('conn.starting');
    case 'restarting': return t('conn.restarting');
    case 'stopped': return t('conn.stopped');
    case 'error': return t('conn.error');
    default: return connectionState;
  }
}

function doctorStatusClass(status: string): string {
  switch (status.toLowerCase()) {
    case 'ok':
    case 'pass':
    case 'passed':
      return 'bg-success';
    case 'warn':
    case 'warning':
      return 'bg-warning';
    case 'error':
    case 'fail':
    case 'failed':
      return 'bg-danger';
    default:
      return 'bg-faint';
  }
}

function formatShortTime(iso: string): string {
  try {
    const date = new Date(iso);
    return date.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch {
    return iso;
  }
}

export function RuntimePanel() {
  const appState = useAppState();
  const conn = appState.connection;
  const [doctor, setDoctor] = useState<DoctorResult | null>(null);
  const [doctorRunning, setDoctorRunning] = useState(false);
  const [logs, setLogs] = useState<DiagnosticLogEntry[]>([]);
  const [filter, setFilter] = useState('');
  const [runtime, setRuntime] = useState<RuntimeBinaryStatus | null>(null);

  const refreshRuntime = async () => {
    try {
      setRuntime(await desktop.runtimeBinary());
    } catch (error) {
      desktop.log(`settings: failed to load runtime binary status: ${error instanceof Error ? error.message : String(error)}`);
    }
    // The About panel shows the effective runtime binary; keep it in sync.
    state.appInfo = await desktop.appInfo().catch(() => state.appInfo);
    emit();
  };

  // Only the privileged process validates the picked executable, persists the
  // selection, and restarts the ACP runtime (rolling back on failure).
  const switchRuntime = async (source: 'bundled' | 'custom') => {
    const picked = source === 'custom' ? await desktop.chooseRuntimeBinary(runtime?.configuredPath || runtime?.bundledPath || '') : null;
    if (source === 'custom' && !picked) return;
    const outcome = await desktop.setRuntimeBinary(source, picked || '');
    toast(outcome.ok ? t('settings.runtimeApplied') : t('settings.runtimeApplyFailed', { e: outcome.error || '' }));
    await refreshRuntime();
  };

  useEffect(() => {
    void refreshRuntime();
  }, []);

  useEffect(() => {
    const off = desktop.onDiagnosticLog((entry) => {
      setLogs((current) => mergeDiagnosticLogs(current, [entry]));
    });
    void desktop
      .getDiagnosticLogs()
      .then((snapshot) => setLogs((current) => mergeDiagnosticLogs(current, snapshot)))
      .catch((error: unknown) => {
        desktop.log(`settings: failed to load diagnostic logs: ${error instanceof Error ? error.message : String(error)}`);
      });
    return off;
  }, []);

  const filtered = useMemo(() => {
    const term = filter.trim().toLowerCase();
    return term ? logs.filter((entry) => `${entry.source} ${entry.message}`.toLowerCase().includes(term)) : logs;
  }, [filter, logs]);

  const agentVersion = conn.agentInfo?.version || '';
  const detail = `${connLabel(conn.state)}${conn.pid ? ` · pid ${conn.pid}` : ''}${agentVersion ? ` · mothx ${agentVersion}` : ''}${conn.error ? ` · ${conn.error.message}` : ''}`;

  const runtimeSource = runtime?.source || 'bundled';
  const runtimeDesc =
    `${t('settings.runtimeEffective')}: ${runtime?.effectivePath || t('settings.runtimeMissing')}` +
    (runtime?.fallback === 'custom'
      ? ` · ${t('settings.runtimeFallbackCustom')}`
      : runtime?.fallback === 'env'
        ? ` · ${t('settings.runtimeFallbackEnv')}`
        : '');

  const runDoctorNow = async () => {
    setDoctorRunning(true);
    setDoctor(null);
    try {
      const result = await runDoctor();
      setDoctor(result);
    } catch (error) {
      setDoctor(null);
      toast(t('doctor.failed', { e: error instanceof Error ? error.message : String(error) }));
    } finally {
      setDoctorRunning(false);
    }
  };

  return (
    <section className="flex flex-col">
      <RowList>
        <RowItem icon={<Cpu />} title={t('settings.runtimeBinary')} desc={runtimeDesc}>
          <div className="flex items-center gap-2">
            <Select value={runtimeSource} onValueChange={(value) => void switchRuntime(value === 'custom' ? 'custom' : 'bundled')}>
              <SelectTrigger className="w-[172px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="bundled">{t('settings.runtimeBundled')}</SelectItem>
                <SelectItem value="custom">{t('settings.runtimeCustom')}</SelectItem>
              </SelectContent>
            </Select>
            {runtimeSource === 'custom' ? (
              <>
                <Input
                  readOnly
                  className="w-[260px]"
                  placeholder={t('settings.runtimePathPlaceholder')}
                  value={runtime?.configuredPath || ''}
                />
                <Button variant="outline" onClick={() => void switchRuntime('custom')}>
                  {t('settings.runtimePick')}
                </Button>
              </>
            ) : null}
          </div>
        </RowItem>
        <RowItem icon={<Zap />} title={t('settings.connection')} desc={detail}>
          <Button
            variant="outline"
            onClick={() =>
              void acp.restart().then((outcome) => {
                toast(outcome.ok ? t('settings.restarted') : t('settings.switchFailed', { e: outcome.error.message }));
              })
            }
          >
            <RefreshCw />
            {t('settings.restart')}
          </Button>
        </RowItem>
        <RowItem icon={<Shield />} title={t('settings.doctor')} desc={t('settings.doctorDesc')}>
          <Button variant="outline" disabled={doctorRunning} onClick={() => void runDoctorNow()}>
            <Shield />
            {t('settings.run')}
          </Button>
        </RowItem>
      </RowList>

      {doctorRunning ? <div className="px-1 pt-3 text-[11.5px] text-muted-foreground">…</div> : null}
      {doctor ? (
        <div className="mt-3 overflow-hidden rounded-xl border border-border bg-card">
          {(doctor.checks || []).length === 0 ? (
            <div className="px-3.5 py-2.5 text-[12px] text-muted-foreground">no checks reported</div>
          ) : (
            (doctor.checks || []).map((check: DoctorCheck, index) => (
              <div key={index} className="flex items-start gap-2.5 border-b border-border px-3.5 py-2.5 last:border-b-0">
                <span className={cn('mt-[5px] size-2 shrink-0 rounded-full', doctorStatusClass(check.status || ''))} />
                <div className="min-w-0 flex-1">
                  <div className="text-[12.5px] font-semibold text-strong">{`${check.title || check.id || 'check'} — ${check.status || ''}`}</div>
                  {check.detail ? <div className="mt-0.5 text-[11.5px] leading-normal break-words text-muted-foreground">{check.detail}</div> : null}
                  {check.fix ? <div className="mt-0.5 text-[11px] text-primary">{check.fix}</div> : null}
                </div>
              </div>
            ))
          )}
        </div>
      ) : null}

      <div className="px-1 pt-3.5 pb-1.5 text-[11px] font-semibold tracking-[.3px] text-muted-foreground">{t('settings.diagnosticLogs')}</div>
      <div className="flex flex-col gap-2.5">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            className="max-w-[360px] min-w-[200px] flex-[1_1_240px]"
            placeholder={t('settings.diagnosticLogsFilter')}
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
          <span className="whitespace-nowrap text-[11px] text-muted-foreground">
            {t('settings.diagnosticLogsCount', { n: String(filtered.length), m: String(logs.length) })}
          </span>
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              void desktop
                .getDiagnosticLogs()
                .then((snapshot) => setLogs((current) => mergeDiagnosticLogs(current, snapshot)))
                .catch(() => undefined)
            }
          >
            <RefreshCw />
            {t('common.refresh')}
          </Button>
        </div>
        <div className="overflow-hidden rounded-lg border border-border bg-background">
          {filtered.length === 0 ? (
            <div className="flex h-[280px] min-h-[180px] items-center justify-center text-[13px] text-muted-foreground">
              {t('settings.diagnosticLogsEmpty')}
            </div>
          ) : (
            <div className="h-[280px] min-h-[180px] overflow-auto px-3 py-2.5 font-mono text-[11.5px] leading-snug">
              {filtered.map((entry) => (
                <div key={entry.id} className="grid grid-cols-[62px_62px_1fr] gap-2.5 py-px max-[640px]:grid-cols-[54px_52px_1fr] max-[640px]:gap-1.5">
                  <span className="whitespace-nowrap text-muted-foreground">{formatShortTime(entry.timestamp)}</span>
                  <span
                    className={cn(
                      'text-[10px] font-semibold uppercase whitespace-nowrap',
                      entry.source === 'acp' ? 'text-info' : entry.source === 'renderer' ? 'text-muted-foreground' : 'text-primary'
                    )}
                  >
                    {entry.source}
                  </span>
                  <span className="min-w-0 [overflow-wrap:anywhere]">{entry.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
