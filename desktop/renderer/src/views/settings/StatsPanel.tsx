// 用量统计面板:mothx/manage/stats 投影(汇总卡片 + 近 14 天运行柱状图)。

import { useEffect, useState } from 'react';

import { UnsupportedRow } from '@/components/manage-primitives';
import { t } from '@/core/i18n';
import { loadStats, type StatsPoint, type StatsSummary } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { useAppSelector } from '@/hooks/useAppState';

export function StatsPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageStats');
  const [summary, setSummary] = useState<StatsSummary>({});
  const [points, setPoints] = useState<StatsPoint[]>([]);

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadStats().then((result) => {
      if (cancelled || !result) return;
      setSummary(result.summary);
      setPoints(result.points);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;

  const cards: [string, string][] = [
    [t('stats.sessions'), String(summary.sessions ?? 0)],
    [t('stats.runs'), String(summary.runs ?? 0)],
    [t('stats.tokens'), String((summary.tokens?.input ?? 0) + (summary.tokens?.output ?? 0))],
    [t('stats.cost'), summary.cost ? `$${summary.cost.toFixed(4)}` : '$0'],
  ];
  const max = Math.max(1, ...points.map((point) => point.runs || 0));

  return (
    <section>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-3">
        {cards.map(([label, value]) => (
          <div key={label} className="rounded-xl border border-border bg-card p-3.5">
            <div className="text-[13.5px] font-semibold text-strong">{label}</div>
            <div className="mt-1 text-[12px] text-muted-foreground">{value}</div>
          </div>
        ))}
      </div>
      <div className="px-1 pt-3.5 pb-1.5 text-[11px] font-semibold tracking-[.3px] text-muted-foreground">{t('stats.days')}</div>
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="flex h-[90px] items-end gap-1 px-1 py-2">
          {points.map((point, index) => {
            const height = Math.round(((point.runs || 0) / max) * 70) + 4;
            return (
              <div
                key={`${point.date || index}`}
                className={point.runs ? 'bg-primary' : 'bg-placeholder'}
                style={{ width: 14, height, borderRadius: 3 }}
                title={`${point.date || ''} · ${point.runs || 0} runs`}
              />
            );
          })}
        </div>
      </div>
    </section>
  );
}
