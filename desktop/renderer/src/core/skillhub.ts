// SkillHub 在线市场数据动作:目录、搜索、详情、安装/启用/卸载都通过 ACP
// 投影;本地技能列表来自 available_commands_update(state.availableCommands)。

import { invoke } from './api';
import { t } from './i18n';
import { confirmDanger, toast } from './ui-host';

export interface Market { id: string; name?: string; capabilities?: { categories?: boolean }; }
export interface Target { path: string; scope: 'project' | 'global'; label: string; }
export interface Installed { installed?: boolean; updateAvailable?: boolean; name?: string; scope?: string; }
export interface Skill { market: string; id: string; name?: string; displayName?: string; slug?: string; description?: string; version?: string; category?: string; author?: string; suspicious?: boolean; installed?: Installed; }
export interface SearchPage { items?: Skill[]; }
export interface SessionActiveSkills { activeSkills?: string[]; }
export interface CategoryEntry { key: string; name: string; nameEn?: string; }

export function commandKind(command: { _meta?: Record<string, unknown> }): string {
  return (command._meta?.['mothx.dev'] as { kind?: string } | undefined)?.kind || 'command';
}

export function skillTitle(skill: Skill): string { return skill.displayName || skill.name || skill.slug || skill.id; }
export function installedName(skill: Skill): string { return skill.installed?.name || skill.slug || skill.name || ''; }

export interface CatalogBootstrap {
  markets: Market[];
  targets: Target[];
  activeSkills: string[];
  market: string;
  target: Target | undefined;
}

export async function loadCatalogBootstrap(sessionId: string, preferredMarket: string, preferredTargetDir: string, preferredScope: 'project' | 'global'): Promise<CatalogBootstrap> {
  const [marketResult, targetResult, installedResult] = await Promise.all([
    invoke<{ markets?: Market[]; defaultMarket?: string }>('mothx/manage/skillhub/markets', { sessionId }),
    invoke<{ targets?: Target[] }>('mothx/manage/skillhub/targets', { sessionId }),
    invoke<{ session?: SessionActiveSkills }>('mothx/manage/skillhub/installed', { sessionId }),
  ]);
  const markets = marketResult.markets || [];
  const targets = targetResult.targets || [];
  const activeSkills = installedResult.session?.activeSkills || [];
  // 默认市场是 ACP 投影的 Runtime/settings 状态(产品默认 skillhub.cn);
  // 只有在运行时未给出默认值时才退回列表首个市场。
  const defaultMarket = marketResult.defaultMarket || '';
  const market = markets.some((item) => item.id === preferredMarket)
    ? preferredMarket
    : markets.some((item) => item.id === defaultMarket)
      ? defaultMarket
      : markets[0]?.id || '';
  const target = targets.find((item) => item.path === preferredTargetDir) || targets.find((item) => item.scope === preferredScope) || targets[0];
  return { markets, targets, activeSkills, market, target };
}

export async function loadCategories(sessionId: string, market: string): Promise<CategoryEntry[]> {
  try {
    const result = await invoke<{ categories?: CategoryEntry[] }>('mothx/manage/skillhub/categories', { sessionId, market });
    return result.categories || [];
  } catch {
    // Market category support is optional.
    return [];
  }
}

export interface SearchParams {
  sessionId: string;
  market: string;
  query: string;
  category?: string;
  official?: boolean;
}

export async function searchSkills(params: SearchParams): Promise<Skill[]> {
  const page = await invoke<SearchPage>(
    params.official ? 'mothx/manage/skillhub/official' : 'mothx/manage/skillhub/search',
    { sessionId: params.sessionId, market: params.market, query: params.query.trim(), category: params.category || '', sort: 'downloads', order: 'desc', limit: 20, page: 1 },
  );
  return page.items || [];
}

export async function loadSkillDetail(sessionId: string, market: string, id: string): Promise<Skill> {
  return invoke<Skill>('mothx/manage/skillhub/detail', { sessionId, market, id });
}

export interface InstallParams {
  sessionId: string;
  skill: Skill;
  overwrite: boolean;
  scope: 'project' | 'global';
  targetDir: string;
}

export async function installSkill(params: InstallParams): Promise<boolean> {
  if (!params.targetDir) {
    toast(t('skills.targetRequired'));
    return false;
  }
  if (params.overwrite && !await confirmDanger(t('skills.confirmUpdate'))) return false;
  try {
    await invoke('mothx/manage/skillhub/install', {
      sessionId: params.sessionId,
      market: params.skill.market,
      id: params.skill.id,
      version: params.skill.version || '',
      scope: params.scope,
      targetDir: params.targetDir,
      overwrite: params.overwrite,
      activate: false,
    });
    toast(t(params.overwrite ? 'skills.updated' : 'skills.installedNotice'));
    return true;
  } catch (error) {
    toast(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
    return false;
  }
}

export async function activateSkill(sessionId: string, skill: Skill): Promise<string[] | null> {
  try {
    const result = await invoke<{ session?: SessionActiveSkills }>('mothx/manage/skillhub/activate', { sessionId, id: installedName(skill) });
    toast(t('skills.activated'));
    return result.session?.activeSkills || [];
  } catch (error) {
    toast(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
    return null;
  }
}

export async function uninstallSkill(sessionId: string, skill: Skill, fallbackScope: 'project' | 'global'): Promise<boolean> {
  if (!await confirmDanger(t('skills.confirmUninstall'))) return false;
  try {
    await invoke('mothx/manage/skillhub/uninstall', { sessionId, market: skill.market, id: skill.id, scope: skill.installed?.scope || fallbackScope });
    toast(t('skills.uninstalled'));
    return true;
  } catch (error) {
    toast(`${t('skills.marketplaceError')}: ${error instanceof Error ? error.message : String(error)}`);
    return false;
  }
}
