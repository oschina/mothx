// 资料库视图:知识库状态的 ACP 投影。完整编辑集中在设置页(知识库条目),
// 这里只提供只读状态与跳转,避免 renderer 复制配置或索引生命周期。

import { useCallback, useEffect, useRef, useState } from 'react';
import { BookOpen } from 'lucide-react';

import { PageHead, PageInner, PageScroll, RowItem, RowList } from '@/components/layout';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { t } from '@/core/i18n';
import { formatKnowledgeBaseStatus, KNOWLEDGE_BASE_FEATURE, loadKnowledgeBaseViews } from '@/core/library';
import type { KnowledgeBaseView } from '@/core/manage-api';
import { openSettingsTab } from '@/core/settings-nav';
import { hasFeature } from '@/core/state';
import { switchView } from '@/core/views';

type LibraryLoadState =
  | { status: 'loading' }
  | { status: 'ready'; knowledgeBases: KnowledgeBaseView[] }
  | { status: 'error'; message: string };

// 后台扫描进行中时展示周期轮询拿到的进度。
function knowledgeIndexingText(indexing: NonNullable<KnowledgeBaseView['indexing']>): string {
  const label = t('library.knowledgeIndexing', { p: indexing.phase || '' });
  return indexing.filesTotal > 0 ? `${label} · ${indexing.filesDone}/${indexing.filesTotal}` : label;
}

export function LibraryView() {
  const [load, setLoad] = useState<LibraryLoadState>({ status: 'loading' });
  const supported = hasFeature(KNOWLEDGE_BASE_FEATURE);
  const loadingRef = useRef(false);

  const reload = useCallback(async () => {
    if (loadingRef.current) return;
    loadingRef.current = true;
    try {
      const knowledgeBases = await loadKnowledgeBaseViews();
      setLoad({ status: 'ready', knowledgeBases });
    } catch (error) {
      setLoad({ status: 'error', message: error instanceof Error ? error.message : String(error) });
    } finally {
      loadingRef.current = false;
    }
  }, []);

  useEffect(() => {
    if (!supported) return;
    void reload();
  }, [supported, reload]);

  // 后台扫描进行中时周期轮询进度,避免阻塞或停留在过期状态。
  const anyIndexing = load.status === 'ready' && load.knowledgeBases.some((view) => view.indexing?.running);
  useEffect(() => {
    if (!anyIndexing) return;
    const timer = window.setInterval(() => void reload(), 4000);
    return () => window.clearInterval(timer);
  }, [anyIndexing, reload]);

  return (
    <PageScroll>
      <PageInner>
        <PageHead title={t('library.title')} subtitle={t('library.subtitle')} />
        <section className="mb-4">
          <div className="mb-2 flex items-start justify-between gap-4">
            <div>
              <div className="pb-[3px] text-[14px] font-semibold text-strong">{t('library.knowledgeTitle')}</div>
              <div className="text-[11.5px] text-muted-foreground">{t('library.knowledgeDesc')}</div>
            </div>
            {supported ? (
              <Button
                variant="outline"
                className="shrink-0"
                onClick={() => {
                  switchView('settings');
                  openSettingsTab('knowledge');
                }}
              >
                {t('library.manageKnowledge')}
              </Button>
            ) : null}
          </div>
          {!supported ? (
            <div className="text-[11.5px] text-muted-foreground">{t('library.knowledgeUnavailable')}</div>
          ) : load.status === 'loading' ? (
            <RowList>
              <div className="px-4 py-3 text-[12.5px] text-muted-foreground">{t('library.knowledgeLoading')}</div>
            </RowList>
          ) : load.status === 'error' ? (
            <div className="text-[11.5px] text-danger">{load.message}</div>
          ) : load.knowledgeBases.length === 0 ? (
            <div className="text-[11.5px] text-muted-foreground">{t('library.knowledgeEmpty')}</div>
          ) : (
            <RowList>
              {load.knowledgeBases.map((view) => {
                const base = view.knowledgeBase;
                if (!base?.id) return null;
                const snapshot = view.snapshot;
                return (
                  <RowItem
                    key={base.id}
                    icon={<BookOpen />}
                    title={base.name || base.id}
                    desc={
                      <>
                        <div>{view.indexing?.running ? knowledgeIndexingText(view.indexing) : formatKnowledgeBaseStatus(view)}</div>
                        {snapshot ? (
                          <div>
                            {t('library.knowledgeStats', {
                              f: snapshot.fileCount ?? 0,
                              c: snapshot.chunkCount ?? 0,
                              n: snapshot.nodeCount ?? 0,
                              e: snapshot.edgeCount ?? 0,
                            })}
                          </div>
                        ) : null}
                      </>
                    }
                  >
                    <Badge variant="accent">{base.enabled === false ? t('settings.off') : t('settings.on')}</Badge>
                  </RowItem>
                );
              })}
            </RowList>
          )}
        </section>
      </PageInner>
    </PageScroll>
  );
}
