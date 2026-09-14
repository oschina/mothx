// Pure projection for the home logo. The async data URL resolution lives in
// the useHomeLogo hook; this file only decides what should be rendered given
// the current store state and any loaded image data.

export interface HomeLogoDisplay {
  hidden: boolean;
  custom: boolean;
  src: string | null;
}

/**
 * Decide how the Home page logo should be displayed.
 *
 * - When visibility is disabled, both built-in and custom logos are hidden.
 * - When a custom image is configured and its authorized data URL is available,
 *   the custom logo is shown.
 * - When a custom image is configured but its data URL is unavailable (missing,
 *   oversized, unauthorized, etc.), gracefully fall back to the built-in logo.
 * - Otherwise, the built-in MothX logo is shown.
 */
export function getHomeLogoDisplay(
  visible: boolean,
  configuredPath: string,
  dataUrl: string | null,
): HomeLogoDisplay {
  if (!visible) {
    return { hidden: true, custom: false, src: null };
  }
  if (configuredPath.trim() && dataUrl) {
    return { hidden: false, custom: true, src: dataUrl };
  }
  return { hidden: false, custom: false, src: null };
}
