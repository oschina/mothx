// 外观面板:主题(IDE Light / IDE Night)、语言、应用背景图片(范围/透明度/
// 适配/位置/模糊)。这些都是 Desktop 本地视觉偏好,只进入 desktop-store。

import { Frame, Globe, Image as ImageIcon, SlidersHorizontal, Sun, AlignLeft } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Slider } from '@/components/ui/slider';
import { Switch } from '@/components/ui/switch';
import { desktop } from '@/core/api';
import { setLocale, t } from '@/core/i18n';
import { emit } from '@/core/state';
import { applyTheme, updateHomeBackground } from '@/core/theme';
import { toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';
import { cn, basename } from '@/lib/utils';

function SegToggle({
  options,
  value,
  onChange,
  ariaLabel,
}: {
  options: { value: string; label: string }[];
  value: string;
  onChange: (value: string) => void;
  ariaLabel: string;
}) {
  return (
    <div className="flex shrink-0 gap-0.5 rounded-lg bg-placeholder p-0.5" role="group" aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          className={cn(
            'rounded-md px-3 py-1 text-[12px] text-muted-foreground',
            option.value === value && 'bg-card font-semibold text-strong shadow-panel'
          )}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

export function AppearancePanel() {
  const appState = useAppState();
  const store = appState.store;

  const chooseBackground = async () => {
    const picked = await desktop.chooseHomeBackground(store.homeBackgroundImage);
    if (!picked) return;
    updateHomeBackground({ homeBackgroundImage: picked }, true);
    toast(t('settings.homeBackgroundSet', { w: basename(picked) }));
  };

  const chooseLogo = async () => {
    const picked = await desktop.chooseHomeLogo(store.homeLogoImage);
    if (!picked) return;
    appState.store.homeLogoImage = picked;
    void desktop.storeSet({ homeLogoImage: picked });
    emit();
    toast(t('settings.homeLogoSet', { w: basename(picked) }));
  };

  const clearLogo = () => {
    appState.store.homeLogoImage = '';
    void desktop.storeSet({ homeLogoImage: '' });
    emit();
    toast(t('settings.homeLogoCleared'));
  };

  const setLogoVisible = (visible: boolean) => {
    appState.store.homeLogoVisible = visible;
    void desktop.storeSet({ homeLogoVisible: visible });
    emit();
  };

  return (
    <section>
      <RowList>
        <RowItem
          icon={<Sun />}
          title={t('settings.theme')}
          desc={t('settings.themeDesc')}
        >
          <SegToggle
            ariaLabel={t('settings.theme')}
            value={store.theme}
            options={[
              { value: 'light', label: t('settings.light') },
              { value: 'dark', label: t('settings.dark') },
            ]}
            onChange={(value) => applyTheme(value === 'dark' ? 'dark' : 'light')}
          />
        </RowItem>

        <RowItem
          icon={<ImageIcon />}
          title={t('settings.homeLogo')}
          desc={t('settings.homeLogoDesc')}
        >
          <Switch
            checked={store.homeLogoVisible}
            onCheckedChange={(checked) => setLogoVisible(checked)}
            aria-label={t('settings.homeLogo')}
          />
        </RowItem>

        <RowItem
          icon={<ImageIcon />}
          title={t('settings.homeLogoImage')}
          desc={store.homeLogoImage || t('settings.homeLogoNone')}
        >
          <div className="flex shrink-0 gap-1.5">
            <Button variant="outline" size="sm" onClick={() => void chooseLogo()}>
              <ImageIcon />
              {t('settings.homeLogoChoose')}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!store.homeLogoImage}
              onClick={() => clearLogo()}
            >
              {t('settings.homeLogoClear')}
            </Button>
          </div>
        </RowItem>

        <RowItem icon={<Globe />} title={t('settings.language')} desc={t('settings.languageDesc')}>
          <SegToggle
            ariaLabel={t('settings.language')}
            value={store.locale}
            options={[
              { value: 'zh', label: '中文' },
              { value: 'en', label: 'English' },
            ]}
            onChange={(value) => {
              const locale = value === 'en' ? 'en' : 'zh';
              setLocale(locale);
              appState.store.locale = locale;
              void desktop.storeSet({ locale });
              // 触发全量重渲染(等价旧 location.reload,但保留运行状态)。
              emit();
            }}
          />
        </RowItem>

        <RowItem
          icon={<ImageIcon />}
          title={t('settings.homeBackground')}
          desc={store.homeBackgroundImage || t('settings.homeBackgroundNone')}
        >
          <div className="flex shrink-0 gap-1.5">
            <Button variant="outline" size="sm" onClick={() => void chooseBackground()}>
              <ImageIcon />
              {t('settings.homeBackgroundChoose')}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!store.homeBackgroundImage}
              onClick={() => {
                updateHomeBackground({ homeBackgroundImage: '' }, true);
                toast(t('settings.homeBackgroundCleared'));
              }}
            >
              {t('settings.homeBackgroundClear')}
            </Button>
          </div>
        </RowItem>

        <RowItem icon={<Frame />} title={t('settings.homeBackgroundScope')} desc={t('settings.homeBackgroundScopeDesc')}>
          <SegToggle
            ariaLabel={t('settings.homeBackgroundScope')}
            value={store.homeBackgroundScope}
            options={[
              { value: 'app', label: t('settings.homeBackgroundScopeApp') },
              { value: 'home', label: t('settings.homeBackgroundScopeHome') },
            ]}
            onChange={(value) => updateHomeBackground({ homeBackgroundScope: value === 'home' ? 'home' : 'app' }, true)}
          />
        </RowItem>

        <RowItem icon={<SlidersHorizontal />} title={t('settings.homeBackgroundOpacity')} desc={t('settings.homeBackgroundOpacityDesc')}>
          <div className="flex w-[min(270px,34%)] min-w-[180px] shrink-0 items-center gap-2.5">
            <Slider
              className="flex-1"
              min={0}
              max={100}
              step={1}
              value={[store.homeBackgroundOpacity]}
              onValueChange={([value]) => updateHomeBackground({ homeBackgroundOpacity: value }, false)}
              onValueCommit={([value]) => updateHomeBackground({ homeBackgroundOpacity: value }, true)}
              aria-label="Home background image opacity"
            />
            <output className="w-10 text-right font-mono text-[11.5px] text-muted-foreground">
              {Math.min(100, Math.max(0, Math.round(store.homeBackgroundOpacity)))}%
            </output>
          </div>
        </RowItem>

        <RowItem icon={<Frame />} title={t('settings.homeBackgroundFit')} desc={t('settings.homeBackgroundFitDesc')}>
          <Select
            value={store.homeBackgroundFit}
            onValueChange={(value) =>
              updateHomeBackground({ homeBackgroundFit: value as typeof store.homeBackgroundFit }, true)
            }
          >
            <SelectTrigger className="w-[min(270px,34%)] min-w-[180px]" aria-label="Background image fit">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="cover">{t('settings.homeBackgroundFitCover')}</SelectItem>
              <SelectItem value="contain">{t('settings.homeBackgroundFitContain')}</SelectItem>
              <SelectItem value="stretch">{t('settings.homeBackgroundFitStretch')}</SelectItem>
              <SelectItem value="tile">{t('settings.homeBackgroundFitTile')}</SelectItem>
            </SelectContent>
          </Select>
        </RowItem>

        <RowItem icon={<AlignLeft />} title={t('settings.homeBackgroundPosition')} desc={t('settings.homeBackgroundPositionDesc')}>
          <Select
            value={store.homeBackgroundPosition}
            onValueChange={(value) =>
              updateHomeBackground({ homeBackgroundPosition: value as typeof store.homeBackgroundPosition }, true)
            }
          >
            <SelectTrigger className="w-[min(270px,34%)] min-w-[180px]" aria-label="Background image position">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="center">{t('settings.homeBackgroundPositionCenter')}</SelectItem>
              <SelectItem value="left">{t('settings.homeBackgroundPositionLeft')}</SelectItem>
              <SelectItem value="right">{t('settings.homeBackgroundPositionRight')}</SelectItem>
              <SelectItem value="top">{t('settings.homeBackgroundPositionTop')}</SelectItem>
              <SelectItem value="bottom">{t('settings.homeBackgroundPositionBottom')}</SelectItem>
            </SelectContent>
          </Select>
        </RowItem>

        <RowItem icon={<Frame />} title={t('settings.homeBackgroundBlur')} desc={t('settings.homeBackgroundBlurDesc')}>
          <div className="flex w-[min(270px,34%)] min-w-[180px] shrink-0 items-center gap-2.5">
            <Slider
              className="flex-1"
              min={0}
              max={24}
              step={1}
              value={[store.homeBackgroundBlur]}
              onValueChange={([value]) => updateHomeBackground({ homeBackgroundBlur: value }, false)}
              onValueCommit={([value]) => updateHomeBackground({ homeBackgroundBlur: value }, true)}
              aria-label="Home background image blur"
            />
            <output className="w-10 text-right font-mono text-[11.5px] text-muted-foreground">
              {Math.min(24, Math.max(0, Math.round(store.homeBackgroundBlur)))}px
            </output>
          </div>
        </RowItem>
      </RowList>
    </section>
  );
}
