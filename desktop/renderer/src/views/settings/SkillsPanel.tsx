// 技能启停面板:mothx/manage/skills 投影,行内开关即时生效。

import { useEffect, useState } from 'react';
import { Zap } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Button } from '@/components/ui/button';
import { t } from '@/core/i18n';
import { loadSkills, setSkillEnabled, type SkillView } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

export function SkillsPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageSkills');
  const [skills, setSkills] = useState<SkillView[] | null>(null);

  const reload = async () => {
    const loaded = await loadSkills();
    setSkills(loaded || []);
  };

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadSkills().then((loaded) => {
      if (!cancelled) setSkills(loaded || []);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  if (!supported) {
    return (
      <RowList>
        <div className="px-4 py-3 text-[11.5px] text-muted-foreground">{t('manage.unsupported')}</div>
      </RowList>
    );
  }
  if (skills === null) {
    return (
      <RowList>
        <div className="px-4 py-3 text-[11.5px] text-muted-foreground">…</div>
      </RowList>
    );
  }
  if (skills.length === 0) {
    return (
      <RowList>
        <div className="px-4 py-3 text-[11.5px] text-muted-foreground">{t('manage.unsupported')}</div>
      </RowList>
    );
  }

  return (
    <RowList>
      {skills.map((skill) => (
        <RowItem
          key={skill.name}
          icon={<Zap />}
          title={skill.name}
          desc={[skill.description, skill.source].filter(Boolean).join(' · ')}
        >
          <Button
            variant={skill.enabled === false ? 'outline' : 'secondary'}
            size="sm"
            className={skill.enabled !== false ? 'bg-primary/8 text-primary' : undefined}
            onClick={async () => {
              try {
                await setSkillEnabled(skill.name, skill.enabled === false);
                await reload();
              } catch (error) {
                toast(error instanceof Error ? error.message : String(error));
              }
            }}
          >
            {skill.enabled === false ? t('settings.off') : t('settings.on')}
          </Button>
        </RowItem>
      ))}
    </RowList>
  );
}
