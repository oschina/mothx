// 环境变量面板:mothx/manage/env 投影。值只可写入不回显;删除/新增在本地
// 草稿中累积,保存时一次性提交 patch(set/unset)。

import { useEffect, useState } from 'react';

import { ManageCard, ManageHeader, ManageWorkspace, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { t } from '@/core/i18n';
import { loadEnv, saveEnv, type EnvView } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

type EnvDraft = { value: string; deleted: boolean; isNew: boolean; touched: boolean };

export function EnvPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageEnv');
  const [view, setView] = useState<EnvView | undefined>();
  const [drafts, setDrafts] = useState<Record<string, EnvDraft>>({});
  const [newName, setNewName] = useState('');
  const [newValue, setNewValue] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadEnv().then((loaded) => {
      if (cancelled || !loaded) return;
      setView(loaded);
      const next: Record<string, EnvDraft> = {};
      for (const variable of loaded.variables || []) {
        next[variable.name] = { value: '', deleted: false, isNew: false, touched: false };
      }
      setDrafts(next);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!view) return <UnsupportedRow text="…" />;

  const names = Object.keys(drafts)
    .filter((name) => !drafts[name].deleted)
    .sort();

  const addVariable = () => {
    const name = newName.trim();
    if (!name) {
      toast(t('settings.envNameRequired'));
      return;
    }
    if (drafts[name] && !drafts[name].deleted) {
      toast(t('settings.envDuplicate'));
      return;
    }
    setDrafts((current) => ({ ...current, [name]: { value: newValue, deleted: false, isNew: true, touched: true } }));
    setNewName('');
    setNewValue('');
  };

  const save = async () => {
    const set: { name: string; value: string }[] = [];
    const unset: string[] = [];
    for (const [name, draft] of Object.entries(drafts)) {
      if (draft.deleted) unset.push(name);
      else if (draft.isNew || draft.touched) set.push({ name, value: draft.value });
    }
    if (set.length === 0 && unset.length === 0) return;
    setSaving(true);
    try {
      const updated = await saveEnv({ set, unset });
      setView(updated);
      const next: Record<string, EnvDraft> = {};
      for (const variable of updated.variables || []) {
        next[variable.name] = { value: '', deleted: false, isNew: false, touched: false };
      }
      setDrafts(next);
      toast(t('settings.envSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.envGroup')}
        title={t('settings.envTitle')}
        desc={t('settings.envDesc')}
        actions={
          <Button disabled={saving} onClick={() => void save()}>
            {saving ? t('settings.envSaving') : t('settings.envSave')}
          </Button>
        }
      />
      <div className="text-[11.5px] text-muted-foreground">{t('settings.envHint')}</div>

      <ManageCard title={t('settings.envVariables')} desc={t('settings.envVariablesDesc')}>
        {names.length === 0 ? (
          <div className="flex flex-col items-center gap-1.5 px-4 py-7 text-center text-faint">
            <strong className="text-[13px] font-semibold text-muted-foreground">{t('settings.envEmpty')}</strong>
            <span>{t('settings.envEmptyHint')}</span>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {names.map((name) => (
              <div key={name} className="grid grid-cols-[minmax(140px,.6fr)_1fr_auto] items-center gap-2.5 rounded-lg border border-border bg-background px-3 py-2.5">
                <code className="overflow-wrap-anywhere font-mono text-[12px] font-semibold text-primary [overflow-wrap:anywhere]">{name}</code>
                <Input
                  type="password"
                  className="font-mono text-[12px]"
                  placeholder={t('settings.envValueConfigured')}
                  aria-label={`${t('settings.envValue')}: ${name}`}
                  value={drafts[name].value}
                  onChange={(event) =>
                    setDrafts((current) => ({
                      ...current,
                      [name]: { ...current[name], value: event.target.value, touched: true },
                    }))
                  }
                />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    setDrafts((current) => ({ ...current, [name]: { ...current[name], deleted: true } }))
                  }
                >
                  {t('settings.envRemoveButton')}
                </Button>
              </div>
            ))}
          </div>
        )}
      </ManageCard>

      <ManageCard title={t('settings.envAdd')} desc={t('settings.envAddDesc')}>
        <div className="grid grid-cols-3 items-start gap-2.5 max-[760px]:grid-cols-1">
          <label className="flex min-w-0 flex-col gap-1.5 text-[11px] font-semibold text-muted-foreground">
            <span>{t('settings.envName')}</span>
            <Input autoComplete="off" placeholder="MY_VARIABLE" value={newName} onChange={(event) => setNewName(event.target.value)} />
          </label>
          <label className="flex min-w-0 flex-col gap-1.5 text-[11px] font-semibold text-muted-foreground">
            <span>{t('settings.envValue')}</span>
            <Input type="password" placeholder={t('settings.envValue')} value={newValue} onChange={(event) => setNewValue(event.target.value)} />
          </label>
          <div className="flex min-w-0 flex-col gap-1.5">
            <span className="invisible select-none text-[11px] font-semibold text-muted-foreground" aria-hidden="true">
              {t('settings.envAddButton')}
            </span>
            <Button className="w-full" onClick={addVariable}>
              {t('settings.envAddButton')}
            </Button>
          </div>
        </div>
      </ManageCard>
    </ManageWorkspace>
  );
}
