// 设置视图:分类 / 条目 / 详情三栏。两列导航只拥有展示状态;详情面板在
// 挂载时按需加载各自的 ACP 管理投影(隐藏面板绝不预载)。

import { useEffect, useSyncExternalStore } from 'react';

import { iconFor } from '@/components/icons';
import { t } from '@/core/i18n';
import {
  getSettingsNav,
  getSettingsNavVersion,
  SETTINGS_CATEGORIES,
  setSettingsCategory,
  setSettingsTab,
  subscribeSettingsNav,
  type SettingsTabID,
} from '@/core/settings-nav';
import { hasFeature } from '@/core/state';
import { useAppBackground } from '@/hooks/useAppBackground';
import { cn } from '@/lib/utils';

import { AboutPanel } from './AboutPanel';
import { AcpWorkersPanel } from './AcpWorkersPanel';
import { ApplicationPanel } from './ApplicationPanel';
import { AppearancePanel } from './AppearancePanel';
import { ChannelsPanel } from './ChannelsPanel';
import { CronPanel } from './CronPanel';
import { EnvPanel } from './EnvPanel';
import { ExpertsPanel } from './ExpertsPanel';
import { KnowledgePanel } from './KnowledgePanel';
import { McpPanel } from './McpPanel';
import { MemoryPanel } from './MemoryPanel';
import { ProvidersPanel } from './ProvidersPanel';
import { RuntimePanel } from './RuntimePanel';
import { ServePanel } from './ServePanel';
import { SkillHubPanel } from './SkillHubPanel';
import { SkillsPanel } from './SkillsPanel';
import { StatsPanel } from './StatsPanel';

export function useSettingsNav() {
  useSyncExternalStore(subscribeSettingsNav, getSettingsNavVersion, getSettingsNavVersion);
  return getSettingsNav();
}

function tabVisible(tab: SettingsTabID): boolean {
  if (tab === 'knowledge') return hasFeature('manageKnowledgeBases');
  if (tab === 'experts') return hasFeature('manageExperts');
  if (tab === 'acpWorkers') return hasFeature('manageACPProfiles');
  return true;
}

function NavButton({ icon, label, active, onClick }: { icon: string; label: string; active: boolean; onClick: () => void }) {
  const Icon = iconFor(icon);
  return (
    <button
      type="button"
      aria-pressed={active}
      className={cn(
        'flex min-h-8 w-full min-w-0 items-center gap-2 rounded-[7px] px-2 py-1.5 text-left text-[12px] text-muted-foreground hover:bg-hoverbg hover:text-strong',
        active && 'bg-primary/8 font-semibold text-primary'
      )}
      onClick={onClick}
    >
      <Icon className="size-4 shrink-0" />
      <span className="min-w-0 flex-1 truncate">{label}</span>
    </button>
  );
}

export function SettingsView() {
  const nav = useSettingsNav();
  const background = useAppBackground();

  const category = SETTINGS_CATEGORIES.find((entry) => entry.id === nav.category) || SETTINGS_CATEGORIES[0];
  const visibleTabs = category.tabs.filter((tab) => tabVisible(tab.id));
  const activeTab = visibleTabs.some((tab) => tab.id === nav.tab) ? nav.tab : visibleTabs[0]?.id || 'appearance';

  // 当前条目被能力门隐藏时,回退到该分类第一个可见条目。
  useEffect(() => {
    if (activeTab !== nav.tab) setSettingsTab(activeTab);
  }, [activeTab, nav.tab]);

  const activeEntry = visibleTabs.find((tab) => tab.id === activeTab) || visibleTabs[0];

  return (
    <section
      className={cn(
        'absolute inset-0 grid grid-cols-[158px_202px_minmax(0,1fr)] max-[760px]:grid-cols-[120px_150px_minmax(0,1fr)]',
        background.app ? 'bg-transparent' : 'bg-background'
      )}
    >
      <aside
        aria-label="Settings categories"
        className={cn(
          'min-w-0 overflow-y-auto border-r border-border px-2.5 py-4 max-[760px]:px-1.5',
          background.app ? 'border-transparent bg-transparent' : 'bg-sidebar'
        )}
      >
        <div className="px-2 pb-2 text-[10.5px] font-bold uppercase tracking-[.55px] text-faint">{t('settings.categories')}</div>
        <div className="flex flex-col gap-[3px]">
          {SETTINGS_CATEGORIES.map((entry) => (
            <NavButton
              key={entry.id}
              icon={entry.icon}
              label={t(entry.label)}
              active={entry.id === category.id}
              onClick={() => setSettingsCategory(entry.id)}
            />
          ))}
        </div>
      </aside>

      <aside
        aria-label="Settings sections"
        className={cn(
          'min-w-0 overflow-y-auto px-3 py-4 max-[760px]:px-1.5',
          background.app ? 'border-transparent bg-transparent' : 'border-r border-border bg-background'
        )}
      >
        <div className="px-2 pb-2 text-[10.5px] font-bold uppercase tracking-[.55px] text-faint">{t('settings.sections')}</div>
        <div className="flex flex-col gap-[3px]">
          {visibleTabs.map((tab) => (
            <NavButton
              key={tab.id}
              icon={tab.icon}
              label={t(tab.label)}
              active={tab.id === activeTab}
              onClick={() => setSettingsTab(tab.id)}
            />
          ))}
        </div>
      </aside>

      <div className="flex min-h-0 min-w-0">
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-[min(880px,94%)] py-6 pb-12">
            <div className="mb-2 flex items-start gap-3">
              <div className="min-w-0">
                <div className="text-[19px] font-bold text-strong">
                  {activeEntry ? t(activeEntry.label) : t('settings.title')}
                </div>
                <div className="mt-1 text-[12.5px] leading-relaxed text-muted-foreground">
                  {activeEntry ? t(activeEntry.description) : t('settings.subtitle')}
                </div>
              </div>
            </div>
            {activeTab === 'appearance' ? <AppearancePanel /> : null}
            {activeTab === 'application' ? <ApplicationPanel /> : null}
            {activeTab === 'providers' ? <ProvidersPanel /> : null}
            {activeTab === 'knowledge' ? <KnowledgePanel /> : null}
            {activeTab === 'skills' ? <SkillsPanel /> : null}
            {activeTab === 'skillhub' ? <SkillHubPanel /> : null}
            {activeTab === 'experts' ? <ExpertsPanel /> : null}
            {activeTab === 'acpWorkers' ? <AcpWorkersPanel /> : null}
            {activeTab === 'env' ? <EnvPanel /> : null}
            {activeTab === 'cron' ? <CronPanel /> : null}
            {activeTab === 'channels' ? <ChannelsPanel /> : null}
            {activeTab === 'mcp' ? <McpPanel /> : null}
            {activeTab === 'memory' ? <MemoryPanel /> : null}
            {activeTab === 'serve' ? <ServePanel /> : null}
            {activeTab === 'runtime' ? <RuntimePanel /> : null}
            {activeTab === 'stats' ? <StatsPanel /> : null}
            {activeTab === 'about' ? <AboutPanel /> : null}
          </div>
        </div>
      </div>
    </section>
  );
}
