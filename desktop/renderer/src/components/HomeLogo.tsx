import { MothxLogo } from './MothxLogo';
import { cn } from '@/lib/utils';
import type { HomeLogoDisplay } from '@/core/home-logo';

interface HomeLogoProps {
  logo: HomeLogoDisplay;
  className?: string;
}

// Render the home logo: a custom image when available, otherwise the built-in
// MothX logo. The caller (HomeView) decides whether to render the logo at all
// based on the `hidden` flag.
export function HomeLogo({ logo, className }: HomeLogoProps) {
  if (logo.custom && logo.src) {
    return (
      <img
        src={logo.src}
        alt=""
        aria-hidden="true"
        className={cn('size-full object-contain', className)}
      />
    );
  }
  return <MothxLogo className={className} />;
}
