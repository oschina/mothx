// Linux launch flags for the packaged Desktop shell.
//
// Packaged Linux builds used to disable the GPU and Chromium's /dev/shm usage
// unconditionally. That kept headless/AppImage hosts alive but forced normal
// desktops onto software compositing (scroll/animation jank) and a slower
// shared-memory path. These helpers keep the safety fallback only where it is
// actually needed.

import { accessSync, constants, statfsSync } from 'node:fs';

export interface LinuxLaunchEnvironment {
  platform: NodeJS.Platform;
  packaged: boolean;
  env: NodeJS.ProcessEnv;
}

function parseBoolean(value: string | undefined): boolean | undefined {
  if (value === undefined) return undefined;
  const normalized = value.trim().toLowerCase();
  if (normalized === '') return undefined;
  if (['1', 'true', 'yes', 'on'].includes(normalized)) return true;
  if (['0', 'false', 'no', 'off'].includes(normalized)) return false;
  return undefined;
}

// shouldDisableGpu keeps Chromium's GPU compositing on normal Linux desktops.
// A packaged host with no DISPLAY/WAYLAND_DISPLAY still gets the old fallback,
// and MOTHX_DESKTOP_DISABLE_GPU can force either choice for unusual hosts.
export function shouldDisableGpu(input: LinuxLaunchEnvironment): boolean {
  if (input.platform !== 'linux' || !input.packaged) return false;
  const override = parseBoolean(input.env.MOTHX_DESKTOP_DISABLE_GPU);
  if (override !== undefined) return override;
  return !input.env.DISPLAY && !input.env.WAYLAND_DISPLAY;
}

const MIN_DEV_SHM_BYTES = 128 * 1024 * 1024;

export interface DevShmStats {
  bsize: number;
  blocks: number;
}

export type StatfsLike = (path: string) => DevShmStats;
export type WritableCheck = (path: string) => boolean;

function defaultWritable(path: string): boolean {
  try {
    accessSync(path, constants.W_OK);
    return true;
  } catch {
    return false;
  }
}

// shouldDisableDevShm asks Chromium to use /tmp only when /dev/shm is missing,
// not writable, or too small (common in containers). A normal desktop keeps the
// faster shared-memory path.
export function shouldDisableDevShm(
  statfs: StatfsLike = statfsSync as unknown as StatfsLike,
  writable: WritableCheck = defaultWritable,
  path = '/dev/shm',
  minBytes = MIN_DEV_SHM_BYTES,
): boolean {
  if (!writable(path)) return true;
  try {
    const stats = statfs(path);
    if (!stats || !Number.isFinite(stats.bsize) || !Number.isFinite(stats.blocks)) return true;
    return stats.bsize * stats.blocks < minBytes;
  } catch {
    return true;
  }
}
