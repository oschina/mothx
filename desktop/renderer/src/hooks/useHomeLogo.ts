import { useEffect, useState } from 'react';

import { desktop } from '../core/api';
import { getHomeLogoDisplay, type HomeLogoDisplay } from '../core/home-logo';
import { shallowEqual, useAppSelector } from './useAppState';

// Resolve the currently configured home logo into a transient data URL using
// the same authorized-read pattern as the home background. Only the exact path
// stored in the DesktopStore can be read, and the result is cached locally.
export function useHomeLogo(): HomeLogoDisplay {
  const { visible, image } = useAppSelector(
    (state) => ({ visible: state.store.homeLogoVisible, image: state.store.homeLogoImage }),
    shallowEqual,
  );
  const [dataUrl, setDataUrl] = useState<string | null>(null);

  useEffect(() => {
    if (!visible || !image) {
      setDataUrl(null);
      return;
    }
    setDataUrl(null);
    let cancelled = false;
    desktop
      .homeLogoDataURL(image)
      .then((result) => {
        if (!cancelled) setDataUrl(result.ok ? result.dataUrl : null);
      })
      .catch(() => {
        if (!cancelled) setDataUrl(null);
      });
    return () => {
      cancelled = true;
    };
  }, [visible, image]);

  return getHomeLogoDisplay(visible, image, dataUrl);
}
