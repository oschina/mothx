// MCP 服务器面板：mothx/manage/mcp 投影。本地编辑器只通过 manage-api
// 与 ACP 交互；密钥、环境变量和请求头值作为普通 MCP 字段直接往返，
// 不做 Desktop 本地缓存或持久化。

import { useEffect, useId, useState } from 'react';
import { Globe, Pencil, Plus, Trash2 } from 'lucide-react';

import { GroupLabel, RowItem, RowList } from '@/components/layout';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { t } from '@/core/i18n';
import { loadMcp, setMcpServers, type McpScope, type McpServerView } from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';

interface ServerDraft {
  name: string;
  type: string;
  command: string;
  argsInput: string;
  url: string;
  messageUrl: string;
  enabled: boolean;
  env: { name: string; value: string }[];
  headers: { name: string; value: string }[];
}

function createEmptyDraft(): ServerDraft {
  return {
    name: '',
    type: 'stdio',
    command: '',
    argsInput: '',
    url: '',
    messageUrl: '',
    enabled: true,
    env: [],
    headers: [],
  };
}

function serverToDraft(server: McpServerView): ServerDraft {
  return {
    name: server.name,
    type: server.type || 'stdio',
    command: server.command || '',
    argsInput: (server.args || []).join(' '),
    url: server.url || '',
    messageUrl: server.messageUrl || '',
    enabled: server.enabled !== false,
    env: server.env ? server.env.map((entry) => ({ ...entry })) : [],
    headers: server.headers ? server.headers.map((entry) => ({ ...entry })) : [],
  };
}

function draftToServer(draft: ServerDraft): McpServerView {
  const args = draft.argsInput
    .split(/\s+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
  return {
    name: draft.name.trim(),
    type: draft.type,
    command: draft.type === 'stdio' ? draft.command.trim() : undefined,
    args: draft.type === 'stdio' ? args : undefined,
    url: draft.type === 'http' || draft.type === 'sse' ? draft.url.trim() : undefined,
    messageUrl: draft.type === 'sse' ? draft.messageUrl.trim() || undefined : undefined,
    enabled: draft.enabled,
    env: draft.env,
    headers: draft.headers,
  };
}

function validateDraft(
  draft: ServerDraft,
  existing: McpServerView[],
  editingIndex: number | null,
): string | null {
  const name = draft.name.trim();
  if (!name) {
    return t('settings.mcpNameRequired');
  }
  const duplicate = existing.some(
    (server, index) => server.name.trim() === name && index !== editingIndex,
  );
  if (duplicate) {
    return t('settings.mcpNameDuplicate');
  }
  if (draft.type === 'stdio') {
    if (!draft.command.trim()) {
      return t('settings.mcpCommandRequired');
    }
  } else if (draft.type === 'http' || draft.type === 'sse') {
    if (!draft.url.trim()) {
      return t('settings.mcpUrlRequired');
    }
  }
  return null;
}

export interface McpPanelProps {
  scope?: McpScope;
  sessionId?: string;
}

export function McpPanel({ scope = 'global', sessionId }: McpPanelProps) {
  const formId = useId();
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const activeSessionCwd = useAppSelector((state) => state.activeSessionCwd);
  const supported = hasFeature('manageMcp');
  const isProject = scope === 'project';
  const [servers, setServers] = useState<McpServerView[] | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingIndex, setEditingIndex] = useState<number | null>(null);
  const [draft, setDraft] = useState<ServerDraft>(createEmptyDraft());

  const reload = async () => {
    const loaded = await loadMcp(scope, sessionId);
    setServers(loaded || []);
  };

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadMcp(scope, sessionId).then((loaded) => {
      if (!cancelled) setServers(loaded || []);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, supported, scope, sessionId]);

  if (!supported) {
    return (
      <RowList>
        <div className="px-4 py-3 text-[11.5px] text-muted-foreground">{t('manage.unsupported')}</div>
      </RowList>
    );
  }

  const openAdd = () => {
    setEditingIndex(null);
    setDraft(createEmptyDraft());
    setDialogOpen(true);
  };

  const openEdit = (index: number) => {
    if (!servers) return;
    setEditingIndex(index);
    setDraft(serverToDraft(servers[index]));
    setDialogOpen(true);
  };

  const closeDialog = () => setDialogOpen(false);

  const saveServer = async () => {
    const error = validateDraft(draft, servers || [], editingIndex);
    if (error) {
      toast(error);
      return;
    }
    const next = servers ? [...servers] : [];
    const server = draftToServer(draft);
    if (editingIndex != null) {
      next[editingIndex] = server;
    } else {
      next.push(server);
    }
    try {
      await setMcpServers(next, scope, sessionId);
      setDialogOpen(false);
      setDraft(createEmptyDraft());
      setEditingIndex(null);
      await reload();
      toast(t('settings.mcpSaved'));
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  const removeServer = async (index: number) => {
    const next = (servers || []).filter((_, i) => i !== index);
    try {
      await setMcpServers(next, scope, sessionId);
      await reload();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  const updateDraft = (patch: Partial<ServerDraft>) => {
    setDraft((current) => ({ ...current, ...patch }));
  };

  const updatePair = (
    kind: 'env' | 'headers',
    index: number,
    patch: Partial<{ name: string; value: string }>,
  ) => {
    setDraft((current) => {
      const list = current[kind].map((entry) => ({ ...entry }));
      list[index] = { ...list[index], ...patch };
      return { ...current, [kind]: list };
    });
  };

  const addPair = (kind: 'env' | 'headers') => {
    setDraft((current) => ({
      ...current,
      [kind]: [...current[kind], { name: '', value: '' }],
    }));
  };

  const removePair = (kind: 'env' | 'headers', index: number) => {
    setDraft((current) => ({
      ...current,
      [kind]: current[kind].filter((_, i) => i !== index),
    }));
  };

  const transportSummary = (server: McpServerView) => {
    const type = server.type || 'stdio';
    if (type === 'stdio') {
      return `stdio: ${server.command || ''}`;
    }
    return `${type}: ${server.url || ''}`;
  };

  return (
    <section className="flex flex-col gap-3">
      {isProject ? (
        <div className="rounded-lg border border-border bg-muted/30 px-3.5 py-2.5 text-[11.5px] leading-relaxed text-muted-foreground">
          {t('chat.projectMcpHint', { cwd: activeSessionCwd || t('chat.projectMcpNoCwd') })}
        </div>
      ) : null}
      <RowList>
        {servers?.map((server, index) => (
          <RowItem
            key={server.name}
            icon={<Globe />}
            title={server.name}
            desc={[
              transportSummary(server),
              server.enabled === false ? t('settings.off') : t('settings.on'),
              server.env && server.env.length > 0 ? `env: ${server.env.length}` : '',
              server.headers && server.headers.length > 0 ? `headers: ${server.headers.length}` : '',
            ]
              .filter(Boolean)
              .join(' · ')}
          >
            <div className="flex items-center gap-1">
              <Button variant="ghost" size="icon" className="size-7" onClick={() => openEdit(index)}>
                <Pencil className="size-4" />
              </Button>
              <Button variant="deny" size="icon" className="size-7" onClick={() => void removeServer(index)}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          </RowItem>
        ))}
        {servers?.length === 0 ? (
          <div className="px-4 py-8 text-center text-[12px] text-muted-foreground">
            {t('settings.mcpEmpty')}
          </div>
        ) : null}
        {servers === null ? <div className="px-4 py-3 text-[11.5px] text-muted-foreground">…</div> : null}
        <RowItem>
          <Button variant="outline" onClick={openAdd}>
            <Plus className="size-4" />
            {t('settings.mcpAdd')}
          </Button>
        </RowItem>
      </RowList>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editingIndex != null ? t('settings.mcpEdit') : t('settings.mcpNew')}
            </DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
              <Label htmlFor={`${formId}-name`}>{t('settings.mcpName')}</Label>
              <Input
                id={`${formId}-name`}
                value={draft.name}
                onChange={(event) => updateDraft({ name: event.target.value })}
                autoFocus
              />
            </div>

            <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
              <Label htmlFor={`${formId}-type`}>{t('settings.mcpType')}</Label>
              <Select value={draft.type} onValueChange={(value) => updateDraft({ type: value })}>
                <SelectTrigger id={`${formId}-type`} className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="stdio">{t('settings.mcpTypeStdio')}</SelectItem>
                  <SelectItem value="http">{t('settings.mcpTypeHttp')}</SelectItem>
                  <SelectItem value="sse">{t('settings.mcpTypeSse')}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
              <Label htmlFor={`${formId}-enabled`}>{t('settings.mcpEnabled')}</Label>
              <div className="h-8.5 flex items-center">
                <Switch
                  id={`${formId}-enabled`}
                  checked={draft.enabled}
                  onCheckedChange={(checked) => updateDraft({ enabled: checked })}
                />
              </div>
            </div>

            {draft.type === 'stdio' ? (
              <>
                <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
                  <Label htmlFor={`${formId}-command`}>{t('settings.mcpCommand')}</Label>
                  <Input
                    id={`${formId}-command`}
                    value={draft.command}
                    placeholder="npx -y @modelcontextprotocol/server-everything"
                    onChange={(event) => updateDraft({ command: event.target.value })}
                  />
                </div>
                <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
                  <Label htmlFor={`${formId}-args`}>{t('settings.mcpArgs')}</Label>
                  <Input
                    id={`${formId}-args`}
                    value={draft.argsInput}
                    placeholder="--port 3000"
                    onChange={(event) => updateDraft({ argsInput: event.target.value })}
                  />
                </div>
              </>
            ) : (
              <>
                <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
                  <Label htmlFor={`${formId}-url`}>{t('settings.mcpUrl')}</Label>
                  <Input
                    id={`${formId}-url`}
                    value={draft.url}
                    placeholder="https://"
                    onChange={(event) => updateDraft({ url: event.target.value })}
                  />
                </div>
                {draft.type === 'sse' ? (
                  <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
                    <Label htmlFor={`${formId}-message-url`}>{t('settings.mcpMessageUrl')}</Label>
                    <Input
                      id={`${formId}-message-url`}
                      value={draft.messageUrl}
                      placeholder="https://"
                      onChange={(event) => updateDraft({ messageUrl: event.target.value })}
                    />
                  </div>
                ) : null}
              </>
            )}

            <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
              <Label>{t('settings.mcpHeaders')}</Label>
              <div className="flex flex-col gap-2">
                {draft.headers.map((header, index) => (
                  <div
                    key={index}
                    className="grid grid-cols-1 items-start gap-2 sm:grid-cols-[1fr_1fr_auto]"
                  >
                    <Input
                      value={header.name}
                      placeholder={t('settings.mcpHeaderName')}
                      onChange={(event) => updatePair('headers', index, { name: event.target.value })}
                    />
                    <Input
                      value={header.value}
                      placeholder={t('settings.mcpHeaderValue')}
                      onChange={(event) => updatePair('headers', index, { value: event.target.value })}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8.5 w-full shrink-0 sm:w-8.5"
                      onClick={() => removePair('headers', index)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button variant="outline" size="sm" className="w-fit" onClick={() => addPair('headers')}>
                  <Plus className="size-4" />
                  {t('settings.mcpAddHeader')}
                </Button>
              </div>
            </div>

            <div className="grid grid-cols-1 items-start gap-x-4 gap-y-1.5 sm:grid-cols-[140px_1fr]">
              <Label>{t('settings.mcpEnv')}</Label>
              <div className="flex flex-col gap-2">
                {draft.env.map((env, index) => (
                  <div
                    key={index}
                    className="grid grid-cols-1 items-start gap-2 sm:grid-cols-[1fr_1fr_auto]"
                  >
                    <Input
                      value={env.name}
                      placeholder={t('settings.mcpEnvName')}
                      onChange={(event) => updatePair('env', index, { name: event.target.value })}
                    />
                    <Input
                      value={env.value}
                      placeholder={t('settings.mcpEnvValue')}
                      onChange={(event) => updatePair('env', index, { value: event.target.value })}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8.5 w-full shrink-0 sm:w-8.5"
                      onClick={() => removePair('env', index)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button variant="outline" size="sm" className="w-fit" onClick={() => addPair('env')}>
                  <Plus className="size-4" />
                  {t('settings.mcpAddEnv')}
                </Button>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeDialog}>
              {t('modal.cancel')}
            </Button>
            <Button onClick={() => void saveServer()}>{t('settings.mcpSave')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}
