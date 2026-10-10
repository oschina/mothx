import { cn } from '@/lib/utils';
import type { RunStatusVariant } from '@/core/transcript';

const CHIP_CLASS: Record<RunStatusVariant, string> = {
  idle: 'text-muted-foreground bg-placeholder',
  planning: 'text-info bg-info-soft',
  working: 'text-primary bg-primary/8',
  pending: 'text-warning bg-warning-soft',
  cancelling: 'text-warning bg-warning-soft',
  completed: 'text-success bg-success-soft',
  failed: 'text-danger bg-danger-soft',
  cancelled: 'text-muted-foreground bg-placeholder',
};

export function StatusChip({
  variant,
  label,
  className,
}: {
  variant: RunStatusVariant;
  label: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1.5 rounded-[10px] px-2.5 py-0.5 text-[11px] font-semibold',
        CHIP_CLASS[variant],
        className
      )}
    >
      <span
        className={cn(
          'size-1.5 rounded-full bg-current',
          (variant === 'working' || variant === 'planning' || variant === 'cancelling') && 'animate-pulse'
        )}
      />
      <span>{label}</span>
    </span>
  );
}
