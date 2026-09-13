// Channels 面板:mothx/manage/channels 投影。制品开关、微信与飞书配置;
// 凭据只可写入不回显,微信 workDir 为必填。

import { useEffect, useState } from 'react';

import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, ToggleField, UnsupportedRow } from '@/components/manage-primitives';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { canRetryDelivery, isDeliveryFailureTransient, loadDeliveries, retryDelivery, DELIVERY_FEATURE, type DeliveryFailureView } from '@/core/deliveries';
import { t } from '@/core/i18n';
import { loadChannels, saveChannels, type ChannelsConfigPatch, type ChannelsConfigView } from '@/core/manage-api';
import { hasFeature, state } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';

export function ChannelsPanel() {
  const appState = useAppState();
  const ready = appState.connection.state === 'ready';
  const supported = hasFeature('manageChannels');
  const [view, setView] = useState<ChannelsConfigView | null>(null);
  const [artifact, setArtifact] = useState(false);
  const [wechatEnabled, setWechatEnabled] = useState(false);
  const [wechatWorkDir, setWechatWorkDir] = useState('');
  const [wechatAutoTyping, setWechatAutoTyping] = useState(true);
  const [wechatCred, setWechatCred] = useState('');
  const [wechatCredConfigured, setWechatCredConfigured] = useState(false);
  const [wechatClearCred, setWechatClearCred] = useState(false);
  const [feishuEnabled, setFeishuEnabled] = useState(false);
  const [feishuWorkDir, setFeishuWorkDir] = useState('');
  const [feishuAppId, setFeishuAppId] = useState('');
  const [feishuAppSecret, setFeishuAppSecret] = useState('');
  const [feishuAppIdConfigured, setFeishuAppIdConfigured] = useState(false);
  const [feishuAppSecretConfigured, setFeishuAppSecretConfigured] = useState(false);
  const [feishuClearAppId, setFeishuClearAppId] = useState(false);
  const [feishuClearAppSecret, setFeishuClearAppSecret] = useState(false);
  const [saving, setSaving] = useState<string | null>(null);
  // 投递失败是 Runtime 的持久化事实:面板只投影当前会话的失败列表,并在
  // Runtime 允许时请求重开一次。
  const deliveriesSupported = hasFeature(DELIVERY_FEATURE);
  const [deliveries, setDeliveries] = useState<DeliveryFailureView[] | null>(null);
  const [deliveriesUnavailable, setDeliveriesUnavailable] = useState(false);
  const [retrying, setRetrying] = useState<string | null>(null);

  const hydrate = (loaded: ChannelsConfigView) => {
    setView(loaded);
    const wechat = loaded.wechat || {};
    const feishu = loaded.feishu || {};
    setArtifact(loaded.artifact === true);
    setWechatEnabled(wechat.enabled === true);
    setWechatWorkDir(wechat.workDir || '');
    setWechatAutoTyping(wechat.autoTyping !== false);
    setWechatCred('');
    setWechatCredConfigured(wechat.credentialConfigured === true);
    setWechatClearCred(false);
    setFeishuEnabled(feishu.enabled === true);
    setFeishuWorkDir(feishu.workDir || '');
    setFeishuAppId('');
    setFeishuAppSecret('');
    setFeishuAppIdConfigured(feishu.appIDConfigured === true);
    setFeishuAppSecretConfigured(feishu.appSecretConfigured === true);
    setFeishuClearAppId(false);
    setFeishuClearAppSecret(false);
  };

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadChannels().then((loaded) => {
      if (!cancelled && loaded) hydrate(loaded);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, supported]);

  useEffect(() => {
    if (!ready || !deliveriesSupported) return;
    let cancelled = false;
    void loadDeliveries(state.activeSessionId || undefined).then((loaded) => {
      if (cancelled) return;
      setDeliveries(loaded || []);
      setDeliveriesUnavailable(loaded === undefined);
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, deliveriesSupported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;
  if (!view) return <UnsupportedRow text="…" />;
  const refreshDeliveries = async () => {
    const loaded = await loadDeliveries(state.activeSessionId || undefined);
    setDeliveries(loaded || []);
    setDeliveriesUnavailable(loaded === undefined);
  };

  const retry = async (failure: DeliveryFailureView) => {
    setRetrying(failure.operationId);
    try {
      const reopened = await retryDelivery(failure.operationId);
      toast(reopened ? t('settings.channelsDeliveriesRetried') : t('settings.channelsDeliveriesNotRetried'));
      await refreshDeliveries();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setRetrying(null);
    }
  };

  const save = async (which: string, patch: ChannelsConfigPatch) => {
    setSaving(which);
    try {
      const updated = await saveChannels(patch);
      hydrate(updated);
      toast(t('settings.channelsSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    } finally {
      setSaving(null);
    }
  };

  const saveWechat = () => {
    const workDir = wechatWorkDir.trim();
    if (!workDir) {
      toast(t('settings.channelsWorkDirRequired'));
      return;
    }
    const patch: ChannelsConfigPatch = { wechat: { enabled: wechatEnabled, workDir, autoTyping: wechatAutoTyping } };
    if (wechatClearCred) patch.wechat!.clearCredPath = true;
    else if (wechatCred.trim()) patch.wechat!.credPath = wechatCred.trim();
    void save('wechat', patch);
  };

  const saveFeishu = () => {
    const workDir = feishuWorkDir.trim();
    if (!workDir) {
      toast(t('settings.channelsWorkDirRequired'));
      return;
    }
    const patch: ChannelsConfigPatch = { feishu: { enabled: feishuEnabled, workDir } };
    if (feishuClearAppId) patch.feishu!.clearAppId = true;
    else if (feishuAppId.trim()) patch.feishu!.appId = feishuAppId.trim();
    if (feishuClearAppSecret) patch.feishu!.clearAppSecret = true;
    else if (feishuAppSecret.trim()) patch.feishu!.appSecret = feishuAppSecret.trim();
    void save('feishu', patch);
  };

  return (
    <ManageWorkspace>
      <ManageHeader eyebrow={t('settings.channelsGroup')} title={t('settings.channelsTitle')} desc={t('settings.channelsDesc')} />
      <div className="text-[11.5px] text-muted-foreground">{t('settings.channelsHint')}</div>

      <ManageCard title={t('settings.channelsArtifact')} desc={t('settings.channelsArtifactDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.channelsArtifactEnabled')} checked={artifact} onChange={setArtifact} />
        </FieldGrid>
        <Button className="mt-3" disabled={saving !== null} onClick={() => void save('artifact', { artifact })}>
          {saving === 'artifact' ? t('settings.channelsSaving') : t('settings.channelsSave')}
        </Button>
      </ManageCard>

      <ManageCard title={t('settings.channelsWechat')} desc={t('settings.channelsWechatDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.channelsEnabled')} checked={wechatEnabled} onChange={setWechatEnabled} />
          <Field label={t('settings.channelsWorkDir')}>
            <Input value={wechatWorkDir} onChange={(event) => setWechatWorkDir(event.target.value)} />
          </Field>
          <ToggleField label={t('settings.channelsAutoTyping')} checked={wechatAutoTyping} onChange={setWechatAutoTyping} />
          <Field label={t('settings.channelsCredPath')}>
            <Input
              type="password"
              value={wechatCred}
              placeholder={wechatCredConfigured ? t('settings.channelsCredConfigured') : t('settings.channelsCredUnset')}
              onChange={(event) => setWechatCred(event.target.value)}
            />
          </Field>
          <ToggleField label={t('settings.channelsClearCred')} checked={wechatClearCred} onChange={setWechatClearCred} />
        </FieldGrid>
        <Button className="mt-3" disabled={saving !== null} onClick={saveWechat}>
          {saving === 'wechat' ? t('settings.channelsSaving') : t('settings.channelsSave')}
        </Button>
      </ManageCard>

      <ManageCard title={t('settings.channelsFeishu')} desc={t('settings.channelsFeishuDesc')}>
        <FieldGrid>
          <ToggleField label={t('settings.channelsEnabled')} checked={feishuEnabled} onChange={setFeishuEnabled} />
          <Field label={t('settings.channelsWorkDir')}>
            <Input value={feishuWorkDir} onChange={(event) => setFeishuWorkDir(event.target.value)} />
          </Field>
          <Field label={t('settings.channelsAppID')}>
            <Input
              type="password"
              value={feishuAppId}
              placeholder={feishuAppIdConfigured ? t('settings.channelsAppIDConfigured') : t('settings.channelsAppIDUnset')}
              onChange={(event) => setFeishuAppId(event.target.value)}
            />
          </Field>
          <Field label={t('settings.channelsAppSecret')}>
            <Input
              type="password"
              value={feishuAppSecret}
              placeholder={feishuAppSecretConfigured ? t('settings.channelsAppSecretConfigured') : t('settings.channelsAppSecretUnset')}
              onChange={(event) => setFeishuAppSecret(event.target.value)}
            />
          </Field>
          <ToggleField label={t('settings.channelsClearAppID')} checked={feishuClearAppId} onChange={setFeishuClearAppId} />
          <ToggleField label={t('settings.channelsClearAppSecret')} checked={feishuClearAppSecret} onChange={setFeishuClearAppSecret} />
        </FieldGrid>
        <Button className="mt-3" disabled={saving !== null} onClick={saveFeishu}>
          {saving === 'feishu' ? t('settings.channelsSaving') : t('settings.channelsSave')}
        </Button>
      </ManageCard>

      {deliveriesSupported ? (
        <ManageCard title={t('settings.channelsDeliveries')} desc={t('settings.channelsDeliveriesDesc')}>
          {deliveries === null ? (
            <div className="text-[11.5px] text-muted-foreground">{t('settings.channelsDeliveriesLoading')}</div>
          ) : deliveriesUnavailable ? (
            <div className="text-[11.5px] text-muted-foreground">{t('settings.channelsDeliveriesUnavailable')}</div>
          ) : deliveries.length === 0 ? (
            <div className="text-[11.5px] text-muted-foreground">{t('settings.channelsDeliveriesEmpty')}</div>
          ) : (
            <ul className="space-y-2">
              {deliveries.map((failure) => (
                <li
                  key={failure.operationId}
                  className="flex items-start justify-between gap-3 rounded-lg border border-border/60 p-2.5"
                >
                  <div className="min-w-0 text-[11.5px] leading-5">
                    <div className="truncate text-strong">
                      {[failure.platform, failure.operationKind, failure.status].filter(Boolean).join(' · ')}
                      {isDeliveryFailureTransient(failure) ? null : (
                        <span className="ml-2 text-muted-foreground">{t('settings.channelsDeliveriesPermanent')}</span>
                      )}
                    </div>
                    <div className="truncate text-muted-foreground">
                      {failure.failureCode || '-'} · {t('settings.channelsDeliveriesAttempts')}: {failure.attemptCount ?? 0}
                      {failure.updatedAt ? ` · ${failure.updatedAt}` : ''}
                    </div>
                    <div className="truncate text-muted-foreground">{failure.operationId}</div>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={retrying !== null || !canRetryDelivery(failure)}
                    onClick={() => void retry(failure)}
                  >
                    {retrying === failure.operationId ? t('settings.channelsDeliveriesRetrying') : t('settings.channelsDeliveriesRetry')}
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <Button className="mt-3" variant="outline" disabled={retrying !== null} onClick={() => void refreshDeliveries()}>
            {t('settings.channelsDeliveriesRefresh')}
          </Button>
        </ManageCard>
      ) : null}
    </ManageWorkspace>
  );
}
