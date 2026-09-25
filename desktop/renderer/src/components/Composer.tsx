// Composer(home 与 chat 视图共用):附件、可搜索的供应商/模型选择、模式、
// 主角团、能力开关、工作目录、发送与取消。输入统一映射为 ACP content
// block,弹出菜单全部投影 state 中的 canonical config options。

import { useCallback, useEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react';
import {
  Check,
  ChevronRight,
  Cloud,
  Cpu,
  Folder,
  GitBranch,
  Loader2,
  Paperclip,
  RotateCcw,
  Trash2,
  Plus,
  Send,
  Shield,
  Slash,
  SlidersHorizontal,
  Sparkles,
  Square,
  Users,
  X,
  FileText,
  Image as ImageIcon,
} from 'lucide-react';

import {
  applyConfigOption,
  attachFiles,
  attachPastedFiles,
  cancelRun,
  chooseSessionExpert,
  injectToComposer,
  modalityLabel,
  modelCapability,
  removeAttachment,
  sendPrompt,
} from '@/core/composer';
import { t } from '@/core/i18n';
import { changeSessionWorkingDirectory, chooseWorkingDirectory, createIsolatedWorktree } from '@/core/sessions';
import { currentConfigOptions, currentModelLabel, currentProviderLabel, state, type SessionConfigOptionShape } from '@/core/state';
import { formatBytes } from '@/core/transcript';
import { applyWorkspacePickerTarget } from '@/core/workspace-picker';
import { worktreeSupported, listWorktrees, removeWorktree, resetWorktree, type WorktreeShape } from '@/core/worktrees';
import { composerFocusEvents, composerInjectEvents, composerReplaceEvents, consumePendingInjection, consumePendingReplacement } from '@/core/bus';
import { confirmDanger, toast } from '@/core/ui-host';
import { useAppState } from '@/hooks/useAppState';
import { useAppBackground } from '@/hooks/useAppBackground';
import { cn, basename } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

const FALLBACK_MODES = [
  { value: 'agent', name: 'Agent' },
  { value: 'plan', name: 'Plan' },
  { value: 'yolo', name: 'Yolo' },
  { value: 'os', name: 'OS' },
];

function AttachRow() {
  const appState = useAppState();
  if (appState.attachments.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1.5 px-3 pt-2.5">
      {appState.attachments.map((attachment) => (
        <div
          key={attachment.path}
          className="flex max-w-[220px] items-center gap-1.5 rounded-lg border border-borderstrong bg-background px-2 py-[3px] text-[11.5px]"
          title={`${attachment.path}${attachment.size ? ` · ${formatBytes(attachment.size)}` : ''}`}
        >
          {attachment.mimeType?.startsWith('image/') ? <ImageIcon className="size-3.5 shrink-0" /> : <FileText className="size-3.5 shrink-0" />}
          <span className="truncate">
            {attachment.name}
            {attachment.embedded ? t('attach.outside') : ''}
          </span>
          <button
            type="button"
            className="flex shrink-0 text-faint hover:text-danger"
            aria-label={`${t('chat.delete')}: ${attachment.name}`}
            onClick={() => removeAttachment(attachment.path)}
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}

function ToolButton({
  className,
  children,
  ...props
}: ComponentProps<'button'>) {
  return (
    <button
      type="button"
      className={cn(
        'flex min-h-7 max-w-60 min-w-0 shrink-0 items-center gap-[5px] rounded-[7px] border border-transparent px-2.5 py-1 text-[12px] text-muted-foreground transition-colors hover:bg-hoverbg hover:text-strong disabled:pointer-events-none disabled:opacity-55',
        '[&>span:not(.sr-only)]:truncate',
        className
      )}
      {...props}
    >
      {children}
    </button>
  );
}

function BorderedTool({ className, ...props }: ComponentProps<'button'>) {
  return <ToolButton className={cn('border-borderstrong bg-card', className)} {...props} />;
}

// WorktreeMenu lists the repository's managed worktrees and offers on-demand
// create / reset / remove. All actions go through the shared core module; the
// renderer never runs git or reads the registry directly.
function WorktreeMenu({ isHome }: { isHome: boolean }) {
  const appState = useAppState();
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<WorktreeShape[]>([]);
  const [loading, setLoading] = useState(false);
  const workspace = appState.activeSessionCwd || appState.newSessionCwd || appState.store.lastWorkspace || '';

  const refresh = useCallback(async () => {
    if (!worktreeSupported() || !workspace) {
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
        <BorderedTool
          title={t('composer.worktree')}
          aria-label={t('composer.worktree')}
          aria-expanded={open}
          className={cn('max-w-[190px]', isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent')}
        >
          <GitBranch className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
          <span>{t('composer.worktree')}</span>
        </BorderedTool>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="start">
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

function SearchablePicker({
  label,
  placeholder,
  icon,
  choices,
  currentValue,
  searchText,
  onSelect,
  triggerClassName,
  buttonLabel,
  renderItem,
}: {
  label: string;
  placeholder: string;
  icon: ReactNode;
  choices: NonNullable<SessionConfigOptionShape['options']>;
  currentValue: string;
  searchText: (choice: NonNullable<SessionConfigOptionShape['options']>[number]) => string;
  onSelect: (value: string) => void;
  triggerClassName?: string;
  buttonLabel: string;
  renderItem?: (choice: NonNullable<SessionConfigOptionShape['options']>[number], selected: boolean) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const term = query.trim().toLowerCase();
  const visible = choices.filter((choice) => !term || searchText(choice).toLowerCase().includes(term));
  return (
    <Popover open={open} onOpenChange={(next) => { setOpen(next); if (!next) setQuery(''); }}>
      <PopoverTrigger asChild>
        <BorderedTool className={triggerClassName} title={label} aria-label={label} aria-expanded={open}>
          {icon}
          <span>{buttonLabel}</span>
          <ChevronRight className="size-3.5 rotate-90 shrink-0" />
        </BorderedTool>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-0" align="start">
        <Command shouldFilter={false}>
          <CommandInput
            placeholder={placeholder}
            aria-label={placeholder}
            value={query}
            onValueChange={setQuery}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                const first = visible[0];
                if (first) {
                  event.preventDefault();
                  onSelect(first.value);
                  setOpen(false);
                  setQuery('');
                }
              }
            }}
          />
          <CommandList>
            <CommandEmpty>{t('composer.noMatches')}</CommandEmpty>
            <CommandGroup>
              {visible.map((choice) => {
                const selected = choice.value === currentValue;
                return (
                  <CommandItem
                    key={choice.value}
                    value={choice.value}
                    onSelect={() => {
                      onSelect(choice.value);
                      setOpen(false);
                      setQuery('');
                    }}
                    className={cn(selected && 'bg-primary/8')}
                  >
                    {renderItem ? (
                      renderItem(choice, selected)
                    ) : (
                      <>
                        <Cloud className="size-4 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1">
                          <span className={cn('block truncate', selected && 'font-semibold text-primary')}>{choice.name}</span>
                          {choice.description ? <span className="block truncate text-[10.5px] text-faint">{choice.description}</span> : null}
                        </span>
                        {selected ? <Check className="size-3.5 shrink-0 text-primary" /> : null}
                      </>
                    )}
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

export function Composer({ source }: { source: 'home' | 'chat' }) {
  const appState = useAppState();
  const background = useAppBackground();
  const [value, setValue] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const autoGrow = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = 'auto';
    textarea.style.height = `${Math.min(textarea.scrollHeight, 200)}px`;
  }, []);

  useEffect(autoGrow, [value, autoGrow]);

  // 注入(技能/指令点击)与聚焦(新建任务)通过总线送达当前挂载的 composer;
  // 若请求发生在挂载前,pendingInjection 会在挂载后立刻被消费。
  useEffect(() => {
    if (source === 'chat' && appState.view !== 'chat') return;
    if (source === 'home' && appState.view !== 'home') return;
    const pending = consumePendingInjection();
    if (pending) setValue((current) => pending + current);
    const replacement = consumePendingReplacement();
    if (replacement !== null) {
      setValue(replacement);
      textareaRef.current?.focus();
    }
    const offInject = composerInjectEvents.on(({ text }) => setValue((current) => text + current));
    const offReplace = composerReplaceEvents.on(({ text }) => {
      setValue(text);
      textareaRef.current?.focus();
    });
    const offFocus = composerFocusEvents.on(({ which }) => {
      if (which === source) textareaRef.current?.focus();
    });
    return () => {
      offInject();
      offReplace();
      offFocus();
    };
  }, [source, appState.view]);

  const doSend = useCallback(async () => {
    const sent = await sendPrompt(value, source);
    if (sent) {
      setValue('');
      requestAnimationFrame(autoGrow);
    }
  }, [autoGrow, source, value]);

  const configOptions = currentConfigOptions();
  const providerOption = configOptions.find((option) => option.id === 'provider');
  const modelOption = configOptions.find((option) => option.id === 'model');
  const modeOption = configOptions.find((option) => option.id === 'mode');
  const thinkingOption = configOptions.find((option) => option.id === 'thinking_level');
  const expertOption = configOptions.find((option) => option.id === 'expert');
  const modes = modeOption?.options?.length ? modeOption.options : FALLBACK_MODES;
  const currentModeValue = modeOption?.currentValue || appState.currentMode;

  const expertAvailable = Boolean(expertOption?.options?.length);
  const currentExpert = expertOption?.options?.find((choice) => choice.value === expertOption.currentValue);
  const expertLabel = currentExpert?.name || t('composer.expertNone');
  const expertTitle = `${t('composer.expert')}: ${expertLabel}`;

  const capsOptions = appState.configOptions.filter(
    (option) => option.id === 'sandbox' || option.id === 'browser' || option.id === 'web_search',
  );

  // 已打开的会话优先显示它自己的工作目录;没有会话时显示下一次新建任务的
  // 候选目录。connection.workspace 只是 ACP 进程目录,不能作为任一会话目录。
  const workspace = appState.activeSessionCwd || appState.newSessionCwd || appState.store.lastWorkspace || '…';
  const workspaceContext = appState.activeSessionId ? t('composer.workspaceSession') : t('composer.workspaceNew');
  const workspaceTitle = `${workspaceContext}: ${workspace}`;

  const runningHere = appState.promptInFlight && appState.runningSessionId === appState.activeSessionId;
  const sendDisabled = source === 'chat' ? runningHere : appState.promptInFlight;

  const isHome = source === 'home';
  const provider = providerOption?.currentValue || '';

  return (
    <div
      className={cn(
        'rounded-xl border shadow-panel transition-[border-color,box-shadow]',
        isHome
          ? 'border-home-border bg-home-solid shadow-[var(--home-shadow)] focus-within:border-home-accent focus-within:shadow-[var(--home-shadow),0_0_0_4px_var(--home-accent-softer)]'
          : 'border-borderstrong bg-inputbg focus-within:border-primary focus-within:ring-[3px] focus-within:ring-primary/10',
        background.app && 'app-control-surface'
      )}
    >
      <AttachRow />
      <textarea
        ref={textareaRef}
        rows={isHome ? 2 : 1}
        className={cn(
          'w-full resize-none border-none bg-transparent px-3.5 pt-3 pb-1.5 text-[13.5px] leading-relaxed outline-none',
          isHome ? 'min-h-16 text-home-text placeholder:text-home-faint' : 'min-h-10 text-foreground placeholder:text-faint'
        )}
        placeholder={isHome ? t('home.composerPlaceholder') : t('chat.inputPlaceholder')}
        value={value}
        onChange={(event) => setValue(event.target.value)}
        onPaste={(event) => {
          const files = Array.from(event.clipboardData?.files || []);
          if (files.some((file) => file.type.startsWith('image/'))) {
            event.preventDefault();
            attachPastedFiles(files);
          }
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void doSend();
          }
        }}
      />
      <div className="flex min-w-0 flex-wrap items-center gap-1.5 px-2 pt-1.5 pb-2 pl-2.5">
        {/* 添加文件 + 技能指令 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <ToolButton aria-label={t('menu.addFiles')} title={t('menu.addFiles')} className={cn('shrink-0', isHome && 'hover:bg-home-accent-softer hover:text-home-accent')}>
              <Plus className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
            </ToolButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="min-w-[220px]">
            <DropdownMenuItem
              onSelect={() => {
                void attachFiles();
              }}
            >
              <Paperclip />
              {t('menu.addFiles')}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>{t('menu.skillsHead')}</DropdownMenuLabel>
            {appState.availableCommands.length === 0 ? (
              <DropdownMenuItem disabled>{t('menu.noCommands')}</DropdownMenuItem>
            ) : (
              appState.availableCommands.slice(0, 30).map((command) => (
                <DropdownMenuItem
                  key={command.name}
                  onSelect={() => injectToComposer(command.name.startsWith('/') ? `${command.name} ` : `/${command.name} `)}
                >
                  <Slash className="shrink-0" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{command.name}</span>
                    {command.description ? <span className="block truncate text-[10.5px] text-faint">{command.description}</span> : null}
                  </span>
                </DropdownMenuItem>
              ))
            )}
          </DropdownMenuContent>
        </DropdownMenu>

        {/* 权限与能力 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <ToolButton aria-label={t('menu.capabilities')} title={t('menu.capabilities')} className={cn('shrink-0', isHome && 'hover:bg-home-accent-softer hover:text-home-accent')}>
              <Shield className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
            </ToolButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>{t('menu.capabilities')}</DropdownMenuLabel>
            {capsOptions.length === 0 ? (
              <DropdownMenuItem disabled>{t('menu.noCommands')}</DropdownMenuItem>
            ) : (
              capsOptions.map((option) => {
                const on = option.currentValue === 'true';
                return (
                  <DropdownMenuItem
                    key={option.id}
                    className={cn(on && 'bg-primary/8')}
                    onSelect={() => void applyConfigOption(option.id, on ? 'false' : 'true')}
                  >
                    {on ? <Check /> : <X />}
                    <span className="min-w-0 flex-1 truncate">{option.name || option.id}</span>
                    <span className="shrink-0 text-[10.5px] text-faint">{on ? 'on' : 'off'}</span>
                  </DropdownMenuItem>
                );
              })
            )}
          </DropdownMenuContent>
        </DropdownMenu>

        {/* 模式 + 思考等级 */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <ToolButton aria-label="Mode" title="Mode" className={cn(isHome && 'hover:bg-home-accent-softer hover:text-home-accent')}>
              <SlidersHorizontal className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
              <span>{appState.currentMode || '…'}</span>
            </ToolButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>Mode</DropdownMenuLabel>
            {modes.map((mode) => (
              <DropdownMenuItem
                key={mode.value}
                className={cn(mode.value === currentModeValue && 'bg-primary/8')}
                onSelect={() => void applyConfigOption('mode', mode.value)}
              >
                <SlidersHorizontal />
                <span className={cn('min-w-0 flex-1', mode.value === currentModeValue && 'font-semibold text-primary')}>{mode.name}</span>
                {mode.value === currentModeValue ? <Check className="size-3.5" /> : null}
              </DropdownMenuItem>
            ))}
            {thinkingOption?.options?.length ? (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>Thinking</DropdownMenuLabel>
                {thinkingOption.options.map((choice) => (
                  <DropdownMenuItem
                    key={choice.value}
                    className={cn(choice.value === thinkingOption.currentValue && 'bg-primary/8')}
                    onSelect={() => void applyConfigOption('thinking_level', choice.value)}
                  >
                    <Sparkles />
                    <span className={cn('min-w-0 flex-1', choice.value === thinkingOption.currentValue && 'font-semibold text-primary')}>{choice.name}</span>
                    {choice.value === thinkingOption.currentValue ? <Check className="size-3.5" /> : null}
                  </DropdownMenuItem>
                ))}
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>

        {/* 主角团(仅在 runtime 投影出 expert 配置项时可见) */}
        {expertAvailable ? (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <BorderedTool
                title={expertTitle}
                aria-label={expertTitle}
                className={cn(isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent')}
              >
                <Users className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
                <span>{expertLabel}</span>
                <ChevronRight className="size-3.5 rotate-90 shrink-0" />
              </BorderedTool>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuLabel>{t('composer.expert')}</DropdownMenuLabel>
              <DropdownMenuItem
                className={cn(expertOption?.currentValue === '' && 'bg-primary/8')}
                onSelect={() => expertOption && chooseSessionExpert(expertOption, '')}
              >
                <X />
                <span className={cn('flex-1', expertOption?.currentValue === '' && 'font-semibold text-primary')}>{t('composer.expertNone')}</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {expertOption?.options?.map((choice) => (
                <DropdownMenuItem
                  key={choice.value}
                  className={cn(choice.value === expertOption.currentValue && 'bg-primary/8')}
                  onSelect={() => chooseSessionExpert(expertOption, choice.value)}
                >
                  <Users />
                  <span className="min-w-0 flex-1">
                    <span className={cn('block truncate', choice.value === expertOption.currentValue && 'font-semibold text-primary')}>{choice.name}</span>
                    {choice.description ? <span className="block truncate text-[10.5px] text-faint">{choice.description}</span> : null}
                  </span>
                  {choice.value === expertOption.currentValue ? <Check className="size-3.5 shrink-0" /> : null}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}

        {/* 供应商(可搜索) */}
        <SearchablePicker
          label={t('composer.provider')}
          placeholder={t('composer.searchProvider')}
          icon={<Cloud className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />}
          choices={providerOption?.options || []}
          currentValue={providerOption?.currentValue || ''}
          searchText={(choice) => `${choice.value} ${choice.name} ${choice.description || ''}`}
          onSelect={(value) => void applyConfigOption('provider', value)}
          buttonLabel={currentProviderLabel() || '…'}
          triggerClassName={cn('max-w-[138px]', isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent')}
        />

        {/* 模型(可搜索,带能力徽章) */}
        <SearchablePicker
          label={t('composer.model')}
          placeholder={t('composer.searchModel')}
          icon={<Cpu className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />}
          choices={modelOption?.options || []}
          currentValue={modelOption?.currentValue || ''}
          searchText={(choice) => {
            const capability = modelCapability(provider, choice.value);
            return [choice.value, choice.name, choice.description, ...(capability?.input || []), capability?.reasoning ? t('composer.reasoning') : ''].join(' ');
          }}
          onSelect={(value) => void applyConfigOption('model', value)}
          buttonLabel={currentModelLabel()}
          triggerClassName={cn('max-w-[190px]', isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent')}
          renderItem={(choice, selected) => {
            const capability = modelCapability(provider, choice.value);
            return (
              <>
                <Cpu className="size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1">
                  <span className={cn('block truncate', selected && 'font-semibold text-primary')}>{choice.name}</span>
                  {choice.description ? <span className="block truncate text-[10.5px] text-faint">{choice.description}</span> : null}
                </span>
                <span className="ml-auto flex shrink-0 flex-wrap items-center gap-1">
                  {(capability?.input || []).map((input) => (
                    <span key={input} className="rounded bg-primary/8 px-1 py-px text-[10px] leading-snug whitespace-nowrap text-primary">
                      {modalityLabel(input)}
                    </span>
                  ))}
                  {capability?.reasoning ? (
                    <span className="rounded bg-expert-soft px-1 py-px text-[10px] leading-snug whitespace-nowrap text-expert">
                      {t('composer.reasoning')}
                    </span>
                  ) : null}
                  {selected ? <Check className="size-3.5 shrink-0 text-primary" /> : null}
                </span>
              </>
            );
          }}
        />

        {/* 工作目录:会话内改会话 cwd,无会话改下一任务默认目录 */}
        <Tooltip>
          <TooltipTrigger asChild>
            <BorderedTool
              title={workspaceTitle}
              aria-label={workspaceTitle}
              className={cn('max-w-[190px]', isHome && 'border-home-border bg-home-surface hover:bg-home-accent-softer hover:text-home-accent')}
              onClick={() =>
                applyWorkspacePickerTarget(appState.activeSessionId, {
                  changeSessionWorkingDirectory,
                  chooseNextSessionWorkingDirectory: chooseWorkingDirectory,
                })
              }
            >
              <Folder className={cn('size-3.5 shrink-0', isHome && 'text-home-text/85')} />
              <span>{basename(workspace)}</span>
            </BorderedTool>
          </TooltipTrigger>
          <TooltipContent>{workspaceTitle}</TooltipContent>
        </Tooltip>

        {/* 隔离工作区:按需为一个任务派生独立 git worktree,并列出/重置/删除 */}
        {worktreeSupported() ? <WorktreeMenu isHome={isHome} /> : null}

        <div className="flex-1" />

        {source === 'chat' && runningHere ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-danger text-destructive-foreground shadow-panel transition-transform hover:bg-danger-hover hover:-translate-y-px"
                aria-label={t('chat.cancelRun')}
                title={t('chat.cancelRun')}
                onClick={() => cancelRun()}
              >
                <Square className="size-4" />
              </button>
            </TooltipTrigger>
            <TooltipContent>{t('chat.cancelRun')}</TooltipContent>
          </Tooltip>
        ) : null}

        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              disabled={sendDisabled}
              aria-label={isHome ? t('nav.newTask') : t('chat.inputPlaceholder')}
              title={isHome ? t('nav.newTask') : undefined}
              className={cn(
                'flex size-8 shrink-0 items-center justify-center rounded-lg transition-all hover:-translate-y-px disabled:translate-y-0 disabled:opacity-55',
                isHome
                  ? 'bg-home-accent text-white shadow-[0_4px_12px_var(--home-accent-shadow)] hover:bg-home-accent-hover disabled:bg-placeholder disabled:text-home-faint disabled:shadow-none'
                  : 'bg-primary text-primary-foreground shadow-panel hover:bg-primary/88 disabled:bg-placeholder disabled:text-faint disabled:shadow-none'
              )}
              onClick={() => void doSend()}
            >
              <Send className="size-4" />
            </button>
          </TooltipTrigger>
          <TooltipContent>{isHome ? t('nav.newTask') : t('chat.inputPlaceholder')}</TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}
