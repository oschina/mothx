import { useEffect, useState } from 'react';

import { desktop } from '../core/api';
import { getHomeLogoDisplay, type HomeLogoDisplay } from '../core/home-logo';
import { useAppState } from './useAppState';

// Resolve the currently configured home logo into a transient data URL using
// the same authorized-read pattern as the home background. Only the exact path
// stored in the DesktopStore can be read, and the result is cached locally.
export function useHomeLogo(): HomeLogoDisplay {
  const { store } = useAppState();
  const [dataUrl, setDataUrl] = useState<string | null>(null);

  useEffect(() => {
    if (!store.homeLogoVisible || !store.homeLogoImage) {
      setDataUrl(null);
      return;
    }
    setDataUrl(null);
    let cancelled = false;
    desktop
      .homeLogoDataURL(store.homeLogoImage)
      .then((result) => {
        if (!cancelled) setDataUrl(result.ok ? result.dataUrl : null);
      })
      .catch(() => {
        if (!cancelled) setDataUrl(null);
      });
    return () => {
      cancelled = true;
    };
  }, [store.homeLogoVisible, store.homeLogoImage]);

  return getHomeLogoDisplay(store.homeLogoVisible, store.homeLogoImage, dataUrl);
}
