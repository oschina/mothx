// SkillHub 市场配置面板:默认市场/安装范围/官方 Handles + 市场源编辑。
// Token 仅可写入,已配置状态不回显。

import { useEffect, useState } from 'react';
import { Plus } from 'lucide-react';

import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { t } from '@/core/i18n';
import { applicationLines, loadSkillHub, saveSkillHub, type SkillHubMarketView, type SkillHubView } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { promptModal, toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

interface MarketDraft extends SkillHubMarketView {
  token: string;
  clearToken: boolean;
}

export function SkillHubPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageSkillHub');
  const [view, setView] = useState<SkillHubView | null>(null);
  const [defaultMarket, setDefaultMarket] = useState('');
  const [defaultScope, setDefaultScope] = useState('project');
  const [officialHandles, setOfficialHandles] = useState('');
  const [markets, setMarkets] = useState<MarketDraft[]>([]);
  const [saving, setSaving] = useState(false);

  const hydrate = (loaded: SkillHubView) => {
    setView(loaded);
    setDefaultMarket(loaded.defaultMarket || '');
    setDefaultScope(loaded.defaultInstallScope || 'project');
    setOfficialHandles((loaded.officialHandles || []).join('\n'));
    setMarkets((loaded.markets || []).map((market) => ({ ...market, token: '', clearToken: false })));
  };

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadSkillHub().then((loaded) => {
      if (!cancelled && loaded) hydrate(loaded);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!view) return <UnsupportedRow text="…" />;

  const addMarket = async () => {
    const id = await promptModal({ title: t('settings.skillhubMarketId'), initialValue: '', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
    const trimmed = id?.trim();
    if (!trimmed) return;
    if (markets.some((entry) => entry.id === trimmed)) {
      toast(t('settings.skillhubMarketIdExists'));
      return;
    }
    setMarkets((current) => [...current, { id: trimmed, name: '', siteURL: '', apiURL: '', enabled: true, apiTokenConfigured: false, token: '', clearToken: false }]);
  };

  const patchMarket = (index: number, patch: Partial<MarketDraft>) => {
    setMarkets((current) => current.map((entry, entryIndex) => (entryIndex === index ? { ...entry, ...patch } : entry)));
  };

  const save = async () => {
    setSaving(true);
    try {
      const updated = await saveSkillHub({
        defaultMarket: defaultMarket.trim(),
        defaultInstallScope: defaultScope,
        officialHandles: applicationLines(officialHandles),
        markets: markets.map((entry) => ({
          id: entry.id.trim(),
          name: (entry.name || '').trim(),
          siteURL: (entry.siteURL || '').trim(),
          apiURL: (entry.apiURL || '').trim(),
          enabled: entry.enabled !== false,
          ...(entry.clearToken ? { clearApiToken: true } : entry.token.trim() ? { apiToken: entry.token.trim() } : {}),
        })),
      });
      hydrate(updated);
      toast(t('settings.skillhubSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.skillhub')}
        title={t('settings.skillhubTitle')}
        desc={t('settings.skillhubDesc')}
        actions={
          <Button disabled={saving} onClick={() => void save()}>
            {saving ? t('settings.skillhubSaving') : t('settings.skillhubSave')}
          </Button>
        }
      />

      <ManageCard title={t('settings.skillhubDefaults')} desc={t('settings.skillhubDefaultsDesc')}>
        <FieldGrid>
          <Field label={t('settings.skillhubDefaultMarket')}>
            <Input value={defaultMarket} onChange={(event) => setDefaultMarket(event.target.value)} />
          </Field>
          <Field label={t('settings.skillhubDefaultScope')}>
            <OptionSelect value={defaultScope} options={['project', 'global']} onChange={setDefaultScope} />
          </Field>
          <Field label={t('settings.skillhubOfficialHandles')} full>
            <Textarea rows={3} className="min-h-[74px] resize-y leading-snug" value={officialHandles} onChange={(event) => setOfficialHandles(event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.skillhubMarkets')} desc={t('settings.skillhubMarketsDesc')}>
        <div className="flex flex-col gap-3">
          {markets.map((market, index) => (
            <div key={market.id} className="flex flex-col gap-2 rounded-lg border border-border bg-background p-3">
              <div className="grid grid-cols-2 items-end gap-2.5 max-[760px]:grid-cols-1">
                <Field label={t('settings.skillhubMarketId')}>
                  <Input readOnly value={market.id} />
                </Field>
                <Field label={t('settings.skillhubMarketName')}>
                  <Input value={market.name || ''} onChange={(event) => patchMarket(index, { name: event.target.value })} />
                </Field>
                <Field label={t('settings.skillhubMarketSiteURL')}>
                  <Input value={market.siteURL || ''} onChange={(event) => patchMarket(index, { siteURL: event.target.value })} />
                </Field>
                <Field label={t('settings.skillhubMarketApiURL')}>
                  <Input value={market.apiURL || ''} onChange={(event) => patchMarket(index, { apiURL: event.target.value })} />
                </Field>
                <Field label={t('settings.skillhubMarketToken')}>
                  <Input
                    type="password"
                    value={market.token}
                    placeholder={market.apiTokenConfigured ? t('settings.skillhubTokenConfigured') : t('settings.skillhubTokenUnset')}
                    onChange={(event) => patchMarket(index, { token: event.target.value })}
                  />
                </Field>
                <div className="flex flex-col gap-2">
                  <ToggleField label={t('settings.skillhubMarketEnabled')} checked={market.enabled !== false} onChange={(checked) => patchMarket(index, { enabled: checked })} />
                  <ToggleField label={t('settings.skillhubClearToken')} checked={market.clearToken} onChange={(checked) => patchMarket(index, { clearToken: checked })} />
                </div>
              </div>
              <Button
                variant="deny"
                size="sm"
                className="self-start"
                onClick={() => setMarkets((current) => current.filter((entry) => entry.id !== market.id))}
              >
                {t('settings.skillhubMarketRemove')}
              </Button>
            </div>
          ))}
          <Button variant="outline" className="self-start" onClick={() => void addMarket()}>
            <Plus />
            {t('settings.skillhubMarketAdd')}
          </Button>
        </div>
      </ManageCard>
    </ManageWorkspace>
  );
}
