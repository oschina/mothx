// 首页视图:hero、场景预设(办公/代码/创作)、快捷操作卡与 composer。
// 极光渐变与用户背景图延续旧 renderer 的材质系统(index.css 组件层)。

import { AlertCircle, Sparkles } from 'lucide-react';

import { Composer } from '@/components/Composer';
import { HomeLogo } from '@/components/HomeLogo';
import { Button } from '@/components/ui/button';
import { acp } from '@/core/api';
import { requestComposerReplace } from '@/core/bus';
import { getLocale, PRESETS, t } from '@/core/i18n';
import { emit, state } from '@/core/state';
import { useAppState } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { useHomeLogo } from '@/hooks/useHomeLogo';
import { switchView } from '@/core/views';
import { cn } from '@/lib/utils';

function ConnBanner() {
  const appState = useAppState();
  const conn = appState.connection;
  if (conn.state !== 'error' || !conn.error) return null;
  return (
    <div className="mb-3.5 flex items-center gap-2.5 rounded-[10px] border border-danger bg-danger-soft px-3.5 py-2.5 text-[12.5px]">
      <AlertCircle className="size-4 shrink-0 text-danger" />
      <div className="flex-1 leading-normal">
        <div>{t('conn.errorBanner', { m: conn.error.message })}</div>
        {conn.error.fix ? <div className="text-[11.5px] text-muted-foreground">{conn.error.fix}</div> : null}
      </div>
      <Button variant="outline" size="sm" onClick={() => void acp.restart()}>
        {t('conn.retry')}
      </Button>
      <Button variant="outline" size="sm" onClick={() => switchView('settings')}>
        {t('conn.openSettings')}
      </Button>
    </div>
  );
}

export function HomeView() {
  const appState = useAppState();
  const background = useAppBackground();
  const logo = useHomeLogo();
  const preset = PRESETS[appState.preset] || PRESETS.coding;
  const isZh = getLocale() === 'zh';
  const items = isZh ? preset.quickZh : preset.quickEn;
  const tags = isZh ? preset.tagZh : preset.tagEn;
  const prompts = isZh ? preset.promptsZh : preset.promptsEn;

  const conn = appState.connection;
  const heroSlogan =
    conn.state === 'ready'
      ? `${t('conn.ready')} · mothx ${conn.agentInfo?.version || ''} · ACP v1`
      : conn.error
        ? `${conn.error.message}${conn.error.fix ? ` — ${conn.error.fix}` : ''}`
        : t(`conn.${conn.state}`);

  return (
    <section className="absolute inset-0 isolate flex flex-col overflow-hidden">
      {background.home ? <div className="home-background-layer" /> : null}
      <div className="home-aurora relative z-1 flex min-h-0 flex-1 flex-col items-center overflow-y-auto">
        <div className="box-border flex min-h-full w-[min(820px,92%)] flex-col justify-center pt-8 pb-12">
          <ConnBanner />

          <div className="mb-[26px] text-center">
            {!logo.hidden && (
              <span className="mx-auto mb-3.5 block size-[58px] rounded-[14px] [filter:drop-shadow(0_8px_18px_var(--home-accent-shadow))]">
                <HomeLogo logo={logo} />
              </span>
            )}
            <h1 className="text-[26px] font-bold tracking-[-.35px] text-home-text">
              MothX
              <span className="font-medium text-[color-mix(in_srgb,var(--home-accent)_55%,var(--home-muted))]">
                {t('home.heroAccent')}
              </span>
            </h1>
            <div className="mt-2 text-[13px] text-home-text opacity-78">{t('home.heroSubtitle')}</div>
            <div className="mt-[5px] text-[11.5px] text-home-muted">{heroSlogan}</div>
          </div>

          <div className="mb-3.5 flex justify-center gap-2.5">
            {(['working', 'coding', 'design'] as const).map((presetId) => {
              const active = appState.preset === presetId;
              const labels = { working: t('home.presetWorking'), coding: t('home.presetCoding'), design: t('home.presetDesign') };
              return (
                <button
                  key={presetId}
                  type="button"
                  className={cn(
                    'rounded-2xl border border-home-border bg-home-surface px-[18px] py-[7px] text-[13px] font-medium text-home-text shadow-[0_1px_1px_rgba(0,0,0,0.03)] backdrop-blur-[18px] backdrop-saturate-150 transition-all hover:-translate-y-px hover:border-home-accent hover:text-home-accent',
                    active && 'border-home-accent bg-home-solid font-semibold text-home-accent shadow-[0_5px_16px_var(--home-accent-softer)]'
                  )}
                  aria-pressed={active}
                  onClick={() => {
                    state.preset = presetId;
                    emit();
                  }}
                >
                  {labels[presetId]}
                </button>
              );
            })}
          </div>

          <div className="mb-3.5 min-h-[34px] text-center">
            <div className="text-[12px] font-semibold text-home-text opacity-90">{t(`preset.${appState.preset}.sub`)}</div>
            <div className="mt-[3px] text-[12px] leading-normal text-home-muted">{t(`preset.${appState.preset}.desc`)}</div>
          </div>

          <div className="mx-auto mb-5 grid w-full max-w-[640px] grid-cols-3 gap-2.5 max-[560px]:grid-cols-2">
            {items.map((label, index) => (
              <button
                key={label}
                type="button"
                className="flex flex-col items-stretch rounded-[10px] border border-home-border bg-home-surface px-3 py-2.5 text-left text-[color-mix(in_srgb,var(--home-text)_90%,transparent)] backdrop-blur-[14px] backdrop-saturate-[1.4] transition-all hover:-translate-y-px hover:border-home-accent hover:bg-home-accent-soft hover:text-home-accent"
                onClick={() => requestComposerReplace(prompts[index] ?? label)}
              >
                <span className="flex min-w-0 flex-col gap-1">
                  <span className="flex min-h-4 items-center justify-between gap-1.5">
                    <span className="text-[10px] font-semibold uppercase leading-none tracking-[.4px] text-home-muted">
                      {tags[index] ?? ''}
                    </span>
                    <Sparkles className="size-3.5 shrink-0 opacity-65" />
                  </span>
                  <span className="truncate text-[12.5px] font-medium leading-snug">{label}</span>
                </span>
              </button>
            ))}
          </div>

          <div className="mt-9 w-full">
            <Composer source="home" />
          </div>
        </div>
      </div>
    </section>
  );
}
