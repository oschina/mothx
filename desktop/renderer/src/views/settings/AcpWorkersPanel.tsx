// Router ACP worker panel. It edits only the behavior-metadata fields the
// Runtime exposes (model, driver, MCP bridge intent, capability tightening);
// argv, URLs, environment names, and trust flags stay operator-owned settings.json.

import { useEffect, useState } from 'react';
import { Cpu, Save } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { t } from '@/core/i18n';
import {
  loadACPWorkerProfileCatalog,
  patchACPWorkerProfile,
  type ACPWorkerProfileCatalog,
  type ACPWorkerProfileView,
} from '@/core/manage-api';
import { shallowEqual, useAppSelector } from '@/hooks/useAppState';
import { toast } from '@/core/ui-host';

interface ProfileDraft {
  model: string;
  driver: string;
  mcpBridge: string;
  overrides: Record<string, boolean>;
}

function draftFor(profile: ACPWorkerProfileView): ProfileDraft {
  const overrides: Record<string, boolean> = {};
  for (const name of profile.capabilityOverrides || []) overrides[name] = true;
  return {
    model: profile.model || '',
    driver: profile.driver || 'generic-acp',
    mcpBridge: profile.mcpBridge || 'auto',
    overrides,
  };
}

export function AcpWorkersPanel() {
  const available = useAppSelector((state) => {
    const caps = state.connection.agentCapabilities as { _meta?: Record<string, { features?: string[] }> } | undefined;
    const dev = caps?._meta?.['mothx.dev'] || caps?._meta?.mothx;
    return Array.isArray(dev?.features) && dev.features.includes('manageACPProfiles');
  }, shallowEqual);
  const [catalog, setCatalog] = useState<ACPWorkerProfileCatalog>({ profiles: [], capabilityNames: [] });
  const [drafts, setDrafts] = useState<Record<string, ProfileDraft>>({});
  const [busy, setBusy] = useState('');
  const [loading, setLoading] = useState(true);

  const applyCatalog = (next: ACPWorkerProfileCatalog) => {
    setCatalog(next);
    const nextDrafts: Record<string, ProfileDraft> = {};
    for (const profile of next.profiles) nextDrafts[profile.id] = draftFor(profile);
    setDrafts(nextDrafts);
  };

  useEffect(() => {
    if (!available) {
      setLoading(false);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const next = await loadACPWorkerProfileCatalog();
        if (!cancelled) applyCatalog(next);
      } catch (error) {
        if (!cancelled) toast(error instanceof Error ? error.message : String(error));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [available]);

  const updateDraft = (id: string, patch: Partial<ProfileDraft>) => {
    setDrafts((current) => ({ ...current, [id]: { ...current[id], ...patch } }));
  };

  const toggleOverride = (id: string, name: string) => {
    setDrafts((current) => {
      const draft = current[id];
      if (!draft) return current;
      const overrides = { ...draft.overrides };
      if (overrides[name]) delete overrides[name];
      else overrides[name] = true;
      return { ...current, [id]: { ...draft, overrides } };
    });
  };

  const save = async (profile: ACPWorkerProfileView) => {
    const draft = drafts[profile.id];
    if (!draft) return;
    const overrides: Record<string, boolean> = {};
    for (const name of Object.keys(draft.overrides)) overrides[name] = false;
    setBusy(profile.id);
    try {
      const profiles = await patchACPWorkerProfile(profile.id, {
        model: draft.model,
        driver: draft.driver,
        mcpBridge: draft.mcpBridge,
        capabilityOverrides: overrides,
      });
      applyCatalog({ profiles, capabilityNames: catalog.capabilityNames });
      toast(t('settings.acpWorkers.saved'));
    } catch (error) {
      toast(t('settings.acpWorkers.saveFailed', { e: error instanceof Error ? error.message : String(error) }));
    } finally {
      setBusy('');
    }
  };

  if (!available) return <p className="text-[12px] text-faint">{t('settings.acpWorkers.unavailable')}</p>;
  if (loading) return <p className="text-[12px] text-faint">{t('settings.acpWorkers.loading')}</p>;
  if (catalog.profiles.length === 0) return <p className="text-[12px] text-faint">{t('settings.acpWorkers.empty')}</p>;

  return (
    <div className="space-y-3">
      <p className="text-[11.5px] leading-relaxed text-muted-foreground">{t('settings.acpWorkers.hint')}</p>
      <RowList>
        {catalog.profiles.map((profile) => {
          const draft = drafts[profile.id] || draftFor(profile);
          return (
            <RowItem
              key={profile.id}
              icon={<Cpu />}
              align="start"
              title={
                <span className="flex flex-wrap items-center gap-2">
                  <span>{profile.title || profile.id}</span>
                  <span className="text-[10.5px] font-normal uppercase tracking-[.03em] text-faint">{profile.id}</span>
                  {profile.invalidReason ? (
                    <span className="rounded-[4px] border border-danger/40 px-1 text-[10px] text-destructive">{profile.invalidReason}</span>
                  ) : null}
                </span>
              }
              desc={profile.transport}
            >
              <div className="flex min-w-0 flex-1 flex-col gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <label className="text-[11px] text-muted-foreground">{t('settings.acpWorkers.model')}</label>
                  <Input
                    value={draft.model}
                    onChange={(event) => updateDraft(profile.id, { model: event.target.value })}
                    className="h-7 w-40 text-[11.5px]"
                    placeholder={t('settings.acpWorkers.modelPlaceholder')}
                  />
                  <label className="ml-2 text-[11px] text-muted-foreground">{t('settings.acpWorkers.driver')}</label>
                  <Input
                    value={draft.driver}
                    onChange={(event) => updateDraft(profile.id, { driver: event.target.value })}
                    className="h-7 w-40 text-[11.5px]"
                    placeholder="generic-acp"
                  />
                  <label className="ml-2 text-[11px] text-muted-foreground">{t('settings.acpWorkers.bridge')}</label>
                  <Select value={draft.mcpBridge} onValueChange={(value) => updateDraft(profile.id, { mcpBridge: value })}>
                    <SelectTrigger className="h-7 w-24 text-[11.5px]">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="auto">auto</SelectItem>
                      <SelectItem value="off">off</SelectItem>
                    </SelectContent>
                  </Select>
                  <Button size="sm" variant="secondary" disabled={busy === profile.id} onClick={() => void save(profile)}>
                    <Save className="mr-1 size-3.5" />
                    {t('settings.acpWorkers.save')}
                  </Button>
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-1">
                  <span className="w-full text-[10.5px] uppercase tracking-[.03em] text-faint">
                    {t('settings.acpWorkers.overrides')}
                  </span>
                  {catalog.capabilityNames.map((name) => (
                    <label key={name} className="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
                      <input
                        type="checkbox"
                        checked={Boolean(draft.overrides[name])}
                        onChange={() => toggleOverride(profile.id, name)}
                      />
                      <span>{name}</span>
                    </label>
                  ))}
                </div>
              </div>
            </RowItem>
          );
        })}
      </RowList>
    </div>
  );
}
