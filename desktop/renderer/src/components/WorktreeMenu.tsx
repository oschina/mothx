// 隔离工作区菜单:列出仓库的受管 worktree,并支持按需创建/重置/删除。
// home composer 工具栏与 chat 头部右上角复用同一组件;所有动作都走共享
// core 模块,renderer 绝不直接运行 git 或读取注册表。

import { useCallback, useEffect, useState } from 'react';
import { GitBranch, Loader2, Plus, RotateCcw, Trash2 } from 'lucide-react';

import { t } from '@/core/i18n';
import { createIsolatedWorktree } from '@/core/sessions';
import { confirmDanger, toast } from '@/core/ui-host';
import { listWorktrees, removeWorktree, resetWorktree, type WorktreeShape } from '@/core/worktrees';
import { useAppState } from '@/hooks/useAppState';
import { cn, basename } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';

// toolbar = home composer 工具栏按钮(带文案);header = chat 头部右上角图标按钮。
export function WorktreeMenu({ variant = 'toolbar', isHome = false }: { variant?: 'toolbar' | 'header'; isHome?: boolean }) {
  const appState = useAppState();
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<WorktreeShape[]>([]);
  const [loading, setLoading] = useState(false);
  const workspace = appState.activeSessionCwd || appState.newSessionCwd || appState.store.lastWorkspace || '';

  const refresh = useCallback(async () => {
    if (!workspace) {
      setItems([]);
      return;
    }
    setLoading(true);
    try {
      setItems(await listWorktrees(workspace));
    } catch {
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, [workspace]);

  useEffect(() => {
    if (open) void refresh();
  }, [open, refresh]);

  const onReset = async (worktree: WorktreeShape) => {
    if (!(await confirmDanger(t('composer.worktreeConfirmReset')))) return;
    try {
      await resetWorktree({ id: worktree.id, directory: worktree.directory });
      toast(t('composer.worktreeReady'));
      await refresh();
    } catch (error) {
      toast(t('composer.worktreeFailed', { e: error instanceof Error ? error.message : String(error) }));
    }
  };

  const onRemove = async (worktree: WorktreeShape) => {
    if (!(await confirmDanger(t('composer.worktreeConfirmRemove')))) return;
    try {
      await removeWorktree({ id: worktree.id, directory: worktree.directory });
      await refresh();
    } catch (error) {
      toast(t('composer.worktreeFailed', { e: error instanceof Error ? error.message : String(error) }));
    }
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        {variant === 'header' ? (
          <Button variant="ghost" size="icon" title={t('composer.worktree')} aria-label={t('composer.worktree')} aria-expanded={open}>
            <GitBranch />
          </Button>
        ) : (
          <button
            type="button"
            title={t('composer.worktree')}
            aria-label={t('composer.worktree')}
            aria-expanded={open}
            className={cn(
              'flex min-h-7 max-w-[190px] min-w-0 shrink-0 items-center gap-[5px] rounded-[7px] border border-borderstrong bg-card px-2.5 py-1 text-[12px] text-muted-foreground transition-colors hover:bg-hoverbg hover:text-strong',
              '[&>span:not(.sr-only)]:truncate',
              isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent'
            )}
          >
            <GitBranch className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
            <span>{t('composer.worktree')}</span>
          </button>
        )}
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align={variant === 'header' ? 'end' : 'start'}>
        <div className="flex items-center justify-between px-3 py-1.5 text-[11px] text-muted-foreground">
          <span>{t('composer.worktree')}</span>
          {loading ? <Loader2 className="size-3.5 animate-spin" /> : null}
        </div>
        <button
          type="button"
          className="flex w-full items-center gap-2 px-3 py-2 text-left text-[12.5px] hover:bg-accent"
          onClick={() => {
            setOpen(false);
            void createIsolatedWorktree();
          }}
        >
          <Plus className="size-3.5 shrink-0" />
          <span>{t('composer.worktreeCreate')}</span>
        </button>
        {items.map((worktree) => (
          <div key={worktree.directory} className="flex items-center gap-2 px-3 py-2 text-[12.5px]">
            <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <div className="truncate">{worktree.name || basename(worktree.directory)}</div>
              <div className="truncate text-[11px] text-muted-foreground">
                {worktree.branch || t('composer.worktreeMain')}
                {worktree.status ? ` · ${worktree.status}` : ''}
              </div>
            </div>
            {worktree.external ? null : (
              <>
                <Button size="icon" variant="ghost" title={t('composer.worktreeReset')} aria-label={t('composer.worktreeReset')} onClick={() => void onReset(worktree)}>
                  <RotateCcw className="size-3.5" />
                </Button>
                <Button size="icon" variant="ghost" title={t('composer.worktreeRemove')} aria-label={t('composer.worktreeRemove')} onClick={() => void onRemove(worktree)}>
                  <Trash2 className="size-3.5" />
                </Button>
              </>
            )}
          </div>
        ))}
      </PopoverContent>
    </Popover>
  );
}
