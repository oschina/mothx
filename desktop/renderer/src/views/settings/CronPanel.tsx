// 自动化 / Cron 设置面板:mothx/manage/cron 投影 + 行内编辑器卡片。

import { useCallback, useEffect, useState } from 'react';
import { Clock, Plus, Zap } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { StatusChip } from '@/components/StatusChip';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { t } from '@/core/i18n';
import { formatCronTime, type CronJobView } from '@/core/manage-api';
import {
  createCronJob,
  loadCronJobs,
  removeCronJobWithConfirm,
  runCronJobNow,
  toggleCronJob,
  updateCronJob,
} from '@/core/automation';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

export function CronPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageCron');
  const [jobs, setJobs] = useState<CronJobView[] | null>(null);
  const [draft, setDraft] = useState<CronJobView | null>(null);
  const [form, setForm] = useState({ name: '', schedule: '', prompt: '', mode: 'yolo', enabled: true });

  const reload = useCallback(async () => {
    const loaded = await loadCronJobs();
    setJobs(loaded || []);
  }, []);

  useEffect(() => {
    if (!ready || !supported) return;
    void reload();
  }, [ready, reload, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;

  const openDraft = (job?: CronJobView) => {
    setDraft(job || { id: '' });
    setForm({
      name: job?.name || '',
      schedule: job?.schedule || '',
      prompt: job?.prompt || '',
      mode: job?.mode || 'yolo',
      enabled: job?.enabled !== false,
    });
  };

  const saveDraft = async () => {
    if (!draft) return;
    if (!form.name.trim() || !form.schedule.trim() || !form.prompt.trim()) {
      toast(t('prompt.empty'));
      return;
    }
    try {
      if (!draft.id) {
        await createCronJob({ name: form.name.trim(), schedule: form.schedule.trim(), prompt: form.prompt.trim(), mode: form.mode, enabled: form.enabled });
        toast(t('automation.created'));
      } else {
        await updateCronJob({ id: draft.id, name: form.name.trim(), schedule: form.schedule.trim(), prompt: form.prompt.trim(), mode: form.mode, enabled: form.enabled });
        toast(t('automation.updated'));
      }
      setDraft(null);
      await reload();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('automation.title')}
        title={t('automation.subtitle')}
        actions={
          <Button onClick={() => openDraft()}>
            <Plus />
            {t('automation.new')}
          </Button>
        }
      />

      <RowList>
        {jobs === null ? (
          <div className="px-4 py-3 text-[11.5px] text-muted-foreground">…</div>
        ) : jobs.length === 0 ? (
          <div className="px-4 py-3 text-[12.5px] text-muted-foreground">{t('automation.empty')}</div>
        ) : (
          jobs.map((job) => {
            const meta = [
              job.schedule,
              job.mode,
              job.workDir,
              job.lastRun ? `${t('automation.lastRun')}: ${formatCronTime(job.lastRun)}${job.lastStatus ? ` · ${job.lastStatus}` : ''}` : '',
              job.lastError ? `${t('automation.lastError')}: ${job.lastError}` : '',
              job.nextRun ? `next: ${formatCronTime(job.nextRun)}` : '',
              job.runCount ? `runs: ${job.runCount}` : '',
              job.provider ? `provider: ${job.provider}` : '',
              job.model ? `model: ${job.model}` : '',
            ]
              .filter(Boolean)
              .join(' · ');
            return (
              <RowItem
                key={job.id}
                icon={<Clock />}
                title={
                  <>
                    <span className="truncate">{job.name || job.id}</span>
                    <StatusChip variant={job.enabled === false ? 'pending' : 'working'} label={job.enabled === false ? t('automation.paused') : t('automation.active')} />
                  </>
                }
                desc={
                  <span className="whitespace-pre-wrap">
                    {[meta, job.prompt].filter(Boolean).join(' — ')}
                  </span>
                }
              >
                <Button variant="outline" size="sm" onClick={async () => { await runCronJobNow(job.id); }}>
                  <Zap />
                  {t('automation.runNow')}
                </Button>
                <Button variant="outline" size="sm" onClick={async () => { if (await toggleCronJob(job)) await reload(); }}>
                  {job.enabled === false ? t('automation.resume') : t('automation.pause')}
                </Button>
                <Button variant="outline" size="sm" onClick={() => openDraft(job)}>
                  {t('automation.edit')}
                </Button>
                <Button
                  variant="deny"
                  size="sm"
                  onClick={async () => {
                    if (await removeCronJobWithConfirm(job.id)) {
                      if (draft?.id === job.id) setDraft(null);
                      await reload();
                    }
                  }}
                >
                  {t('automation.delete')}
                </Button>
              </RowItem>
            );
          })
        )}
      </RowList>

      {draft ? (
        <ManageCard title={draft.id ? t('automation.name') : t('automation.new')} desc={draft.id || undefined}>
          <FieldGrid>
            <Field label={t('automation.name')}>
              <Input value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} />
            </Field>
            <Field label={t('automation.schedule')}>
              <Input value={form.schedule} onChange={(event) => setForm((current) => ({ ...current, schedule: event.target.value }))} />
            </Field>
            <Field label={t('automation.mode')}>
              <OptionSelect value={form.mode} options={['agent', 'yolo']} onChange={(value) => setForm((current) => ({ ...current, mode: value }))} />
            </Field>
            <ToggleField label={t('automation.enabled')} checked={form.enabled} onChange={(value) => setForm((current) => ({ ...current, enabled: value }))} />
            <Field label={t('automation.prompt')} full>
              <Textarea rows={4} value={form.prompt} onChange={(event) => setForm((current) => ({ ...current, prompt: event.target.value }))} />
            </Field>
          </FieldGrid>
          <div className="flex justify-end gap-2 pt-2">
            <Button onClick={() => void saveDraft()}>{draft.id ? t('settings.applicationSave') : t('automation.new')}</Button>
            <Button variant="outline" onClick={() => setDraft(null)}>
              {t('modal.cancel')}
            </Button>
          </div>
        </ManageCard>
      ) : null}
    </ManageWorkspace>
  );
}
