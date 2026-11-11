// 设置视图导航状态:分类 → 条目 → 详情面板。两列导航只拥有展示状态;
// 所有配置与变更仍由各 ACP-backed 面板负责。旧 settings.ts 的三栏结构在
// React 中保留:分类 pane、条目 pane、详情 pane。

export type SettingsTabID =
  | 'appearance'
  | 'application'
  | 'providers'
  | 'knowledge'
  | 'skills'
  | 'skillhub'
  | 'experts'
  | 'acpWorkers'
  | 'mcp'
  | 'memory'
  | 'env'
  | 'cron'
  | 'channels'
  | 'serve'
  | 'runtime'
  | 'stats'
  | 'about';

export interface SettingsTabEntry {
  id: SettingsTabID;
  label: string;
  description: string;
  icon: string;
}

export interface SettingsCategory {
  id: string;
  label: string;
  icon: string;
  tabs: SettingsTabEntry[];
}

// The same conceptual groups exposed by WebUI settings, narrowed to the
// settings surfaces Desktop can truthfully project through ACP today.
export const SETTINGS_CATEGORIES: SettingsCategory[] = [
  {
    id: 'appearance', label: 'settings.tab.appearance', icon: 'sun',
    tabs: [
      { id: 'appearance', label: 'settings.tab.appearance', description: 'settings.tab.appearanceDesc', icon: 'sun' },
    ],
  },
  {
    id: 'agent', label: 'settings.category.agent', icon: 'cpu',
    tabs: [
      { id: 'application', label: 'settings.tab.application', description: 'settings.tab.applicationDesc', icon: 'settings' },
      { id: 'providers', label: 'settings.tab.providers', description: 'settings.tab.providersDesc', icon: 'cpu' },
      { id: 'knowledge', label: 'settings.tab.knowledge', description: 'settings.tab.knowledgeDesc', icon: 'book' },
      { id: 'skills', label: 'settings.tab.skills', description: 'settings.tab.skillsDesc', icon: 'zap' },
      { id: 'skillhub', label: 'settings.tab.skillhub', description: 'settings.tab.skillhubDesc', icon: 'shop' },
      { id: 'experts', label: 'settings.tab.experts', description: 'settings.tab.expertsDesc', icon: 'users' },
      { id: 'acpWorkers', label: 'settings.tab.acpWorkers', description: 'settings.tab.acpWorkersDesc', icon: 'cpu' },
      { id: 'env', label: 'settings.tab.env', description: 'settings.tab.envDesc', icon: 'terminal' },
    ],
  },
  {
    id: 'integrations', label: 'settings.category.integrations', icon: 'globe',
    tabs: [
      { id: 'channels', label: 'settings.tab.channels', description: 'settings.tab.channelsDesc', icon: 'channels' },
      { id: 'mcp', label: 'settings.tab.mcp', description: 'settings.tab.mcpDesc', icon: 'globe' },
      { id: 'memory', label: 'settings.tab.memory', description: 'settings.tab.memoryDesc', icon: 'book' },
    ],
  },
  {
    id: 'automation', label: 'settings.category.automation', icon: 'clock',
    tabs: [
      { id: 'cron', label: 'settings.tab.cron', description: 'settings.tab.cronDesc', icon: 'clock' },
    ],
  },
  {
    id: 'system', label: 'settings.category.system', icon: 'settings',
    tabs: [
      { id: 'serve', label: 'settings.tab.serve', description: 'settings.tab.serveDesc', icon: 'server' },
      { id: 'runtime', label: 'settings.tab.runtime', description: 'settings.tab.runtimeDesc', icon: 'refresh' },
      { id: 'stats', label: 'settings.tab.stats', description: 'settings.tab.statsDesc', icon: 'list' },
      { id: 'about', label: 'settings.tab.about', description: 'settings.tab.aboutDesc', icon: 'doc' },
    ],
  },
];

export type ManagedSettingsTab =
  | 'application'
  | 'providers'
  | 'knowledge'
  | 'skills'
  | 'skillhub'
  | 'experts'
  | 'acpWorkers'
  | 'mcp'
  | 'memory'
  | 'env'
  | 'cron'
  | 'channels'
  | 'serve'
  | 'stats';

const MANAGED_SETTINGS_TABS = new Set<ManagedSettingsTab>([
  'application', 'providers', 'knowledge', 'skills', 'skillhub', 'experts', 'acpWorkers', 'mcp',
  'memory', 'env', 'cron', 'channels', 'serve', 'stats',
]);

export function isManagedSettingsTab(tab: SettingsTabID): tab is ManagedSettingsTab {
  return MANAGED_SETTINGS_TABS.has(tab as ManagedSettingsTab);
}

export interface SettingsNavState {
  category: string;
  tab: SettingsTabID;
}

let nav: SettingsNavState = { category: 'appearance', tab: 'appearance' };
let navVersion = 0;
const navListeners = new Set<() => void>();

function emitNav(): void {
  navVersion += 1;
  for (const listener of navListeners) listener();
}

export function getSettingsNav(): SettingsNavState {
  return nav;
}

export function getSettingsNavVersion(): number {
  return navVersion;
}

export function subscribeSettingsNav(listener: () => void): () => void {
  navListeners.add(listener);
  return () => navListeners.delete(listener);
}

export function setSettingsCategory(categoryId: string): void {
  const category = SETTINGS_CATEGORIES.find((entry) => entry.id === categoryId);
  if (!category) return;
  nav = { category: categoryId, tab: category.tabs[0]?.id || 'appearance' };
  emitNav();
}

export function setSettingsTab(tab: SettingsTabID): void {
  const category = SETTINGS_CATEGORIES.find((entry) => entry.tabs.some((entryTab) => entryTab.id === tab));
  nav = { category: category?.id || nav.category, tab };
  emitNav();
}

// Library 视图与侧边栏通过这一座桥打开设置的具体条目,而不是复制知识库
// 编辑器;设置视图仍然是导航状态与懒加载 ACP 管理数据的拥有者。
export function openSettingsTab(tab: SettingsTabID): void {
  setSettingsTab(tab);
}
