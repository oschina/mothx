import { readFileSync, statSync } from 'node:fs';
import { extname } from 'node:path';

// Shared implementation for local home imagery (background, logo). The renderer
// has no generic file-read capability; it may only ask for the exact image path
// that the main-process DesktopStore currently authorizes. This avoids file://
// access and keeps the image inside the Electron sandbox.
export const MAX_HOME_IMAGE_BYTES = 20 * 1024 * 1024;

export const SUPPORTED_IMAGE_MIME_TYPES: Record<string, string> = {
  '.avif': 'image/avif',
  '.bmp': 'image/bmp',
  '.gif': 'image/gif',
  '.jpeg': 'image/jpeg',
  '.jpg': 'image/jpeg',
  '.png': 'image/png',
  '.webp': 'image/webp',
};

export const HOME_IMAGE_ERROR_REASONS = ['unsupported', 'missing', 'oversized', 'unauthorized'] as const;
export type HomeImageErrorReason = (typeof HOME_IMAGE_ERROR_REASONS)[number];

export type ReadHomeImageResult =
  | { ok: true; dataUrl: string }
  | { ok: false; reason: HomeImageErrorReason };

export function readHomeImageDataURL(
  requestedPath: unknown,
  configuredPath: string,
  maxBytes = MAX_HOME_IMAGE_BYTES,
): ReadHomeImageResult {
  if (typeof requestedPath !== 'string' || requestedPath === '' || requestedPath !== configuredPath) {
    return { ok: false, reason: 'unauthorized' };
  }
  const mime = SUPPORTED_IMAGE_MIME_TYPES[extname(requestedPath).toLowerCase()];
  if (!mime) {
    return { ok: false, reason: 'unsupported' };
  }
  try {
    const stat = statSync(requestedPath);
    if (!stat.isFile()) {
      return { ok: false, reason: 'missing' };
    }
    if (stat.size > maxBytes) {
      return { ok: false, reason: 'oversized' };
    }
    return { ok: true, dataUrl: `data:${mime};base64,${readFileSync(requestedPath).toString('base64')}` };
  } catch {
    return { ok: false, reason: 'missing' };
  }
}
