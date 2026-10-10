import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Desktop-local persistence for UI state that ACP intentionally does not own
// (theme, locale, default-new-session directory history,
// observed run statuses, and which local `mothx` executable this client
// spawns as its ACP runtime). `lastWorkspace` remains the on-disk
// compatibility field name; it is not a process-wide workspace or session
// filter. The runtime binary selection is client-shell launch state only —
// ACP cannot prescribe which local executable the Electron shell starts — and
// must never be treated as a workspace or security boundary. Canonical session
// data always comes from the ACP runtime; this store must never shadow it
// (see desktop-acp-frontend-gap-proposal.md P1-4/P1-5).

export interface DesktopStoreData {
  theme: 'light' | 'dark';
  locale: 'zh' | 'en';
  homeBackgroundImage: string;
  homeBackgroundOpacity: number;
  homeBackgroundBlur: number;
  homeBackgroundScope: 'app' | 'home';
  homeBackgroundFit: 'cover' | 'contain' | 'stretch' | 'tile';
  homeBackgroundPosition: 'center' | 'left' | 'right' | 'top' | 'bottom';
  homeLogoVisible: boolean;
  homeLogoImage: string;
  // Which local mothx executable becomes the ACP runtime: the bundled one
  // (default) or a user-picked path (runtimeBinaryPath).
  runtimeSource: 'bundled' | 'custom';
  runtimeBinaryPath: string;
  lastWorkspace: string;
  recentWorkspaces: string[];
  sessionStatus: Record<string, string>;
}

const DEFAULTS: DesktopStoreData = {
  theme: 'light',
  locale: 'zh',
  homeBackgroundImage: '',
  homeBackgroundOpacity: 32,
  homeBackgroundBlur: 0,
  homeBackgroundScope: 'app',
  homeBackgroundFit: 'cover',
  homeBackgroundPosition: 'center',
  homeLogoVisible: true,
  homeLogoImage: '',
  runtimeSource: 'bundled',
  runtimeBinaryPath: '',
  lastWorkspace: '',
  recentWorkspaces: [],
  sessionStatus: {},
};

// A non-empty local image path is file-read authority. Only the privileged
// native chooser in ipc.ts may grant one; otherwise a renderer compromise could
// set an arbitrary *.png path through the generic store channel and retrieve
// it through the authorized-image bridge. The renderer may still clear a prior
// chooser-owned path with an empty string.
export function restrictRendererStorePatch(patch: Partial<DesktopStoreData>): Partial<DesktopStoreData> {
  const restricted = { ...patch };
  if (restricted.homeBackgroundImage !== undefined && restricted.homeBackgroundImage !== '') {
    delete restricted.homeBackgroundImage;
  }
  if (restricted.homeLogoImage !== undefined && restricted.homeLogoImage !== '') {
    delete restricted.homeLogoImage;
  }
  return restricted;
}

// Factory for the desktop:store-set handler. Kept with the store so the
// restriction can be unit-tested without pulling in Electron's IPC stack.
export function createStoreSetHandler(store: DesktopStore): (patch: unknown) => DesktopStoreData {
  return (patch: unknown) => {
    if (!patch || typeof patch !== 'object' || Array.isArray(patch)) return store.get();
    return store.set(restrictRendererStorePatch(patch as Partial<DesktopStoreData>));
  };
}

const MAX_RECENT_WORKSPACES = 12;
const MAX_STATUS_ENTRIES = 400;

function clampNumber(value: unknown, fallback: number, min: number, max: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback;
  return Math.min(max, Math.max(min, Math.round(value)));
}

function backgroundFit(value: unknown): DesktopStoreData['homeBackgroundFit'] {
  return value === 'contain' || value === 'stretch' || value === 'tile' ? value : 'cover';
}

function backgroundScope(value: unknown): DesktopStoreData['homeBackgroundScope'] {
  return value === 'home' ? 'home' : 'app';
}

function backgroundPosition(value: unknown): DesktopStoreData['homeBackgroundPosition'] {
  return value === 'left' || value === 'right' || value === 'top' || value === 'bottom' ? value : 'center';
}

function runtimeSource(value: unknown): DesktopStoreData['runtimeSource'] {
  return value === 'custom' ? 'custom' : 'bundled';
}

export class DesktopStore {
  private file: string;
  private data: DesktopStoreData;

  constructor(userDataDir: string) {
    this.file = join(userDataDir, 'desktop-store.json');
    this.data = this.load();
  }

  private load(): DesktopStoreData {
    try {
      const raw = readFileSync(this.file, 'utf8');
      const parsed = JSON.parse(raw) as Partial<DesktopStoreData>;
      return {
        theme: parsed.theme === 'dark' ? 'dark' : 'light',
        locale: parsed.locale === 'en' ? 'en' : 'zh',
        homeBackgroundImage: typeof parsed.homeBackgroundImage === 'string' ? parsed.homeBackgroundImage.slice(0, 4096) : '',
        homeBackgroundOpacity: clampNumber(parsed.homeBackgroundOpacity, DEFAULTS.homeBackgroundOpacity, 0, 100),
        homeBackgroundBlur: clampNumber(parsed.homeBackgroundBlur, DEFAULTS.homeBackgroundBlur, 0, 24),
        homeBackgroundScope: backgroundScope(parsed.homeBackgroundScope),
        homeBackgroundFit: backgroundFit(parsed.homeBackgroundFit),
        homeBackgroundPosition: backgroundPosition(parsed.homeBackgroundPosition),
        homeLogoVisible: parsed.homeLogoVisible !== false,
        homeLogoImage: typeof parsed.homeLogoImage === 'string' ? parsed.homeLogoImage.slice(0, 4096) : '',
        runtimeSource: runtimeSource(parsed.runtimeSource),
        runtimeBinaryPath: typeof parsed.runtimeBinaryPath === 'string' ? parsed.runtimeBinaryPath.slice(0, 4096) : '',
        lastWorkspace: typeof parsed.lastWorkspace === 'string' ? parsed.lastWorkspace : '',
        recentWorkspaces: Array.isArray(parsed.recentWorkspaces)
          ? parsed.recentWorkspaces.filter((entry): entry is string => typeof entry === 'string' && entry !== '')
          : [],
        sessionStatus:
          parsed.sessionStatus && typeof parsed.sessionStatus === 'object' && !Array.isArray(parsed.sessionStatus)
            ? (parsed.sessionStatus as Record<string, string>)
            : {},
      };
    } catch {
      return { ...DEFAULTS };
    }
  }

  get(): DesktopStoreData {
    return structuredClone(this.data);
  }

  set(patch: Partial<DesktopStoreData>): DesktopStoreData {
    if (patch.theme === 'light' || patch.theme === 'dark') this.data.theme = patch.theme;
    if (patch.locale === 'zh' || patch.locale === 'en') this.data.locale = patch.locale;
    if (typeof patch.homeBackgroundImage === 'string') this.data.homeBackgroundImage = patch.homeBackgroundImage.slice(0, 4096);
    if (patch.homeBackgroundOpacity !== undefined) this.data.homeBackgroundOpacity = clampNumber(patch.homeBackgroundOpacity, this.data.homeBackgroundOpacity, 0, 100);
    if (patch.homeBackgroundBlur !== undefined) this.data.homeBackgroundBlur = clampNumber(patch.homeBackgroundBlur, this.data.homeBackgroundBlur, 0, 24);
    if (patch.homeBackgroundScope !== undefined) this.data.homeBackgroundScope = backgroundScope(patch.homeBackgroundScope);
    if (patch.homeBackgroundFit !== undefined) this.data.homeBackgroundFit = backgroundFit(patch.homeBackgroundFit);
    if (patch.homeBackgroundPosition !== undefined) this.data.homeBackgroundPosition = backgroundPosition(patch.homeBackgroundPosition);
    if (typeof patch.homeLogoVisible === 'boolean') this.data.homeLogoVisible = patch.homeLogoVisible;
    if (typeof patch.homeLogoImage === 'string') this.data.homeLogoImage = patch.homeLogoImage.slice(0, 4096);
    if (patch.runtimeSource === 'bundled' || patch.runtimeSource === 'custom') this.data.runtimeSource = patch.runtimeSource;
    if (typeof patch.runtimeBinaryPath === 'string') this.data.runtimeBinaryPath = patch.runtimeBinaryPath.slice(0, 4096);
    if (typeof patch.lastWorkspace === 'string' && patch.lastWorkspace !== '') {
      this.data.lastWorkspace = patch.lastWorkspace;
      this.rememberWorkspace(patch.lastWorkspace);
    }
    if (Array.isArray(patch.recentWorkspaces)) {
      this.data.recentWorkspaces = patch.recentWorkspaces
        .filter((entry): entry is string => typeof entry === 'string' && entry !== '')
        .slice(0, MAX_RECENT_WORKSPACES);
    }
    if (patch.sessionStatus && typeof patch.sessionStatus === 'object') {
      for (const [key, value] of Object.entries(patch.sessionStatus)) {
        if (typeof value === 'string') this.data.sessionStatus[key] = value;
      }
      const keys = Object.keys(this.data.sessionStatus);
      if (keys.length > MAX_STATUS_ENTRIES) {
        for (const key of keys.slice(0, keys.length - MAX_STATUS_ENTRIES)) {
          delete this.data.sessionStatus[key];
        }
      }
    }
    this.persist();
    return this.get();
  }

  private rememberWorkspace(cwd: string): void {
    const list = this.data.recentWorkspaces.filter((entry) => entry !== cwd);
    list.unshift(cwd);
    this.data.recentWorkspaces = list.slice(0, MAX_RECENT_WORKSPACES);
  }

  private persist(): void {
    try {
      mkdirSync(join(this.file, '..'), { recursive: true });
      const tmp = `${this.file}.tmp`;
      writeFileSync(tmp, JSON.stringify(this.data, null, 2), 'utf8');
      renameSync(tmp, this.file);
    } catch {
      // UI-state persistence must never break the app loop.
    }
  }
}
