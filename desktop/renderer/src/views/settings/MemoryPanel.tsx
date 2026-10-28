// 记忆面板:memory.md 内容投影。文本可编辑并整体保存;内容不进入本地 store。

import { useEffect, useState } from 'react';

import { GroupLabel } from '@/components/layout';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { t } from '@/core/i18n';
import { loadMemory, saveMemory } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

export function MemoryPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageMemory');
  const [content, setContent] = useState('');

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadMemory().then((value) => {
      if (!cancelled && value !== undefined) setContent(value);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported]);

  const save = async () => {
    try {
      const size = await saveMemory(content);
      toast(t('settings.memorySaved', { n: size }));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <section>
      <div className="overflow-hidden rounded-xl border border-border bg-card">
        <div className="flex items-start gap-3 border-b border-border px-4 py-3">
          <div className="min-w-0 flex-1">
            <Textarea
              rows={6}
              className="w-full resize-y"
              disabled={!supported}
              placeholder={supported ? '' : t('manage.unsupported')}
              value={content}
              onChange={(event) => setContent(event.target.value)}
              aria-label="memory.md"
            />
          </div>
          <Button variant="outline" disabled={!supported} onClick={() => void save()}>
            {t('settings.memorySave')}
          </Button>
        </div>
      </div>
      <GroupLabel>{t('settings.memoryGroup')}</GroupLabel>
    </section>
  );
}
