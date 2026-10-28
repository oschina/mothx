// 主角团(Expert Team)面板:全局/项目作用域的创建、编辑与删除;内置团队
// 只读。团队数据完全来自 mothx/manage/experts 投影。

import { useCallback, useEffect, useState } from 'react';
import { BookOpen, Plus, Users } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { Field, FieldGrid, ManageCard, ManageHeader, ManageWorkspace, OptionSelect, UnsupportedRow } from '@/components/manage-primitives';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { t, getLocale } from '@/core/i18n';
import {
  deleteExpert,
  expertWorkspace,
  getExpertBundle,
  loadExperts,
  saveExpertBundle,
  type ExpertBundle,
  type ExpertListView,
  type ExpertMemberMeta,
  type ExpertScope,
  type ExpertSummary,
  type ExpertTeamInfo,
} from '@/core/manage-api';
import { hasFeature } from '@/core/state';
import { confirmDanger, promptModal, toast } from '@/core/ui-host';
import { useAppSelector } from '@/hooks/useAppState';
import { cn } from '@/lib/utils';

function displayName(item: ExpertSummary): string {
  if (item.displayName) {
    const locale = getLocale();
    if (locale === 'en') return item.displayName.en || item.displayName.zh || item.name;
    return item.displayName.zh || item.displayName.en || item.name;
  }
  return item.name;
}

function sourceLabel(source?: string): string {
  if (source === 'builtin') return t('settings.expertsBuiltin');
  if (source === 'project') return t('settings.expertsProject');
  if (source === 'global') return t('settings.expertsGlobal');
  return source || '';
}

function ExpertEditor({
  draft,
  isNew,
  scope,
  onDone,
  onCancel,
}: {
  draft: ExpertBundle;
  isNew: boolean;
  scope: ExpertScope;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(draft.manifest.name);
  const [displayZh, setDisplayZh] = useState(draft.manifest.displayName?.zh || '');
  const [displayEn, setDisplayEn] = useState(draft.manifest.displayName?.en || '');
  const [expertType, setExpertType] = useState(draft.manifest.expertType || 'agent');
  const [agentName, setAgentName] = useState(draft.manifest.agentName || '');
  const [categoryId, setCategoryId] = useState(draft.manifest.categoryId || '');
  const [members, setMembers] = useState<ExpertMemberMeta[]>([...(draft.manifest.members || [])]);
  const [agents, setAgents] = useState<Record<string, string>>({ ...(draft.agents || {}) });
  const [saving, setSaving] = useState(false);

  const addMember = async () => {
    const id = await promptModal({ title: t('settings.expertMemberId'), initialValue: '', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
    const trimmed = id?.trim();
    if (!trimmed) return;
    if (members.some((member) => member.id === trimmed)) {
      toast(t('settings.expertMemberIdExists'));
      return;
    }
    setMembers((current) => [...current, { id: trimmed, name: { zh: '', en: '' }, role: 'member' }]);
  };

  const addAgent = async () => {
    const id = await promptModal({ title: t('settings.expertAgentId'), initialValue: '', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
    const trimmed = id?.trim();
    if (!trimmed) return;
    if (agents[trimmed] !== undefined) {
      toast(t('settings.expertAgentIdExists'));
      return;
    }
    setAgents((current) => ({ ...current, [trimmed]: `---\nname: ${trimmed}\n---\n` }));
  };

  const save = async () => {
    const nextName = isNew ? name.trim() : draft.manifest.name;
    if (!nextName) {
      toast(t('settings.expertNameRequired'));
      return;
    }
    const nextAgents: Record<string, string> = {};
    for (const [id, source] of Object.entries(agents)) {
      if (!id.trim()) continue;
      nextAgents[id.trim()] = source;
    }
    if (Object.keys(nextAgents).length === 0) {
      toast(t('settings.expertAgentsRequired'));
      return;
    }
    const nextMembers: ExpertMemberMeta[] = members
      .filter((member) => member.id.trim() !== '')
      .map((member) => ({
        id: member.id.trim(),
        role: member.role || 'member',
        name: { zh: (member.name?.zh || '').trim(), en: (member.name?.en || '').trim() },
      }));
    if (nextMembers.length === 0) {
      toast(t('settings.expertMembersRequired'));
      return;
    }
    const lead = nextMembers.find((member) => member.role === 'lead') || nextMembers[0];
    if (expertType === 'team' && (!lead || lead.role !== 'lead')) {
      toast(t('settings.expertLeadRequired'));
      return;
    }
    const teamInfo: ExpertTeamInfo | undefined =
      expertType === 'team'
        ? { leadAgent: lead.id, memberAgents: nextMembers.filter((member) => member.id !== lead.id).map((member) => member.id) }
        : undefined;
    const next: ExpertBundle = {
      scope: draft.scope || scope,
      manifest: {
        ...draft.manifest,
        schemaVersion: draft.manifest.schemaVersion || 1,
        name: nextName,
        displayName: { zh: displayZh.trim(), en: displayEn.trim() },
        expertType,
        agentName: agentName.trim() || lead.id,
        categoryId: categoryId.trim(),
        members: nextMembers,
        ...(teamInfo ? { teamInfo } : {}),
      },
      agents: nextAgents,
    };
    setSaving(true);
    try {
      await saveExpertBundle(isNew, next);
      toast(isNew ? t('settings.expertCreated') : t('settings.expertSaved'));
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!await confirmDanger(t('settings.expertsDeleteConfirm', { n: draft.manifest.name }))) return;
    try {
      await deleteExpert(scope, draft.manifest.name);
      toast(t('settings.expertDeleted'));
      onDone();
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  return (
    <>
      <ManageCard title={isNew ? t('settings.expertNew') : draft.manifest.name} desc={isNew ? t('settings.expertNewDesc') : t('settings.expertEditDesc')}>
        <FieldGrid>
          <Field label={t('settings.expertName')}>
            <Input value={isNew ? name : draft.manifest.name} disabled={!isNew} onChange={(event) => setName(event.target.value)} />
          </Field>
          <Field label={t('settings.expertDisplayZh')}>
            <Input value={displayZh} onChange={(event) => setDisplayZh(event.target.value)} />
          </Field>
          <Field label={t('settings.expertDisplayEn')}>
            <Input value={displayEn} onChange={(event) => setDisplayEn(event.target.value)} />
          </Field>
          <Field label={t('settings.expertType')}>
            <OptionSelect value={expertType} options={['agent', 'team']} onChange={setExpertType} />
          </Field>
          <Field label={t('settings.expertAgentName')}>
            <Input value={agentName} onChange={(event) => setAgentName(event.target.value)} />
          </Field>
          <Field label={t('settings.expertCategoryId')}>
            <Input value={categoryId} onChange={(event) => setCategoryId(event.target.value)} />
          </Field>
        </FieldGrid>
      </ManageCard>

      <ManageCard title={t('settings.expertMembers')} desc={t('settings.expertMembersDesc')}>
        <div className="flex flex-col gap-2">
          {members.map((member, index) => (
            <div key={member.id} className="grid grid-cols-2 items-end gap-[9px] rounded-[10px] border border-border bg-background p-[11px] max-[760px]:grid-cols-1">
              <Field label={t('settings.expertMemberId')}>
                <Input value={member.id} disabled />
              </Field>
              <Field label={t('settings.expertMemberRole')}>
                <OptionSelect
                  value={member.role || 'member'}
                  options={['lead', 'member']}
                  onChange={(value) =>
                    setMembers((current) => current.map((entry, entryIndex) => (entryIndex === index ? { ...entry, role: value } : entry)))
                  }
                />
              </Field>
              <Field label={t('settings.expertMemberNameZh')}>
                <Input
                  value={member.name?.zh || ''}
                  onChange={(event) =>
                    setMembers((current) =>
                      current.map((entry, entryIndex) =>
                        entryIndex === index ? { ...entry, name: { ...entry.name, zh: event.target.value } } : entry,
                      ),
                    )
                  }
                />
              </Field>
              <Field label={t('settings.expertMemberNameEn')}>
                <Input
                  value={member.name?.en || ''}
                  onChange={(event) =>
                    setMembers((current) =>
                      current.map((entry, entryIndex) =>
                        entryIndex === index ? { ...entry, name: { ...entry.name, en: event.target.value } } : entry,
                      ),
                    )
                  }
                />
              </Field>
              <div className="col-span-full flex justify-end max-[760px]:col-auto">
                <Button variant="deny" size="sm" onClick={() => setMembers((current) => current.filter((_, entryIndex) => entryIndex !== index))}>
                  {t('settings.expertRemoveAgent')}
                </Button>
              </div>
            </div>
          ))}
          <Button variant="outline" className="self-start" onClick={() => void addMember()}>
            <Plus />
            {t('settings.expertAddMember')}
          </Button>
        </div>
      </ManageCard>

      <ManageCard title={t('settings.expertAgents')} desc={t('settings.expertAgentsDesc')}>
        <div className="flex flex-col gap-2">
          {Object.entries(agents).map(([id, source]) => (
            <div key={id} className="grid grid-cols-2 items-end gap-[9px] rounded-[10px] border border-border bg-background p-[11px] max-[760px]:grid-cols-1">
              <Field label={t('settings.expertAgentId')}>
                <Input value={id} disabled />
              </Field>
              <Field label={t('settings.expertAgentSource')} full>
                <Textarea
                  rows={5}
                  className="font-mono text-[12px]"
                  value={source}
                  onChange={(event) => setAgents((current) => ({ ...current, [id]: event.target.value }))}
                />
              </Field>
              <div className="col-span-full flex justify-end max-[760px]:col-auto">
                <Button
                  variant="deny"
                  size="sm"
                  onClick={() =>
                    setAgents((current) => {
                      const next = { ...current };
                      delete next[id];
                      return next;
                    })
                  }
                >
                  {t('settings.expertRemoveAgent')}
                </Button>
              </div>
            </div>
          ))}
          <Button variant="outline" className="self-start" onClick={() => void addAgent()}>
            <Plus />
            {t('settings.expertAddAgent')}
          </Button>
        </div>
      </ManageCard>

      <div className="sticky bottom-0 z-1 flex flex-wrap items-center justify-end gap-[7px] border-t border-border bg-card/94 px-4 py-[11px] backdrop-blur-[10px]">
        <Button disabled={saving} onClick={() => void save()}>
          {saving ? t('settings.expertSaving') : isNew ? t('settings.expertCreate') : t('settings.expertSave')}
        </Button>
        <Button variant="outline" onClick={onCancel}>
          {t('modal.cancel')}
        </Button>
        {!isNew ? (
          <Button variant="deny" onClick={() => void remove()}>
            {t('settings.expertDelete')}
          </Button>
        ) : null}
      </div>
    </>
  );
}

export function ExpertsPanel() {
  const ready = useAppSelector((state) => state.connection.state === 'ready');
  const supported = hasFeature('manageExperts');  const [scope, setScope] = useState<ExpertScope>('global');
  const [listView, setListView] = useState<ExpertListView | null>(null);
  const [draft, setDraft] = useState<ExpertBundle | null>(null);
  const [creating, setCreating] = useState(false);

  const reload = useCallback(
    async (nextScope: ExpertScope) => {
      const view = await loadExperts(nextScope);
      setListView(view || null);
    },
    [],
  );

  useEffect(() => {
    if (!ready || !supported) return;
    let cancelled = false;
    void loadExperts(scope).then((view) => {
      if (!cancelled) setListView(view || null);
    });
    return () => {
      cancelled = true;
    };
  }, [ready, scope, supported]);

  if (!supported) return <UnsupportedRow text={t('manage.unsupported')} />;

  const switchScope = (next: ExpertScope) => {
    if (scope === next) return;
    if (next === 'project' && !expertWorkspace()) {
      toast(t('settings.expertsProjectCwdRequired'));
      return;
    }
    setScope(next);
    setDraft(null);
    setCreating(false);
    setListView(null);
  };

  const createDraft = async () => {
    const name = await promptModal({ title: t('settings.expertsNewName'), initialValue: '', okLabel: t('modal.ok'), cancelLabel: t('modal.cancel') });
    const trimmed = name?.trim();
    if (!trimmed) return;
    setCreating(true);
    setDraft({
      scope,
      manifest: {
        schemaVersion: 1,
        name: trimmed,
        expertType: 'agent',
        agentName: 'lead',
        displayName: { zh: '', en: '' },
        members: [{ id: 'lead', name: { zh: '主角', en: 'Lead' }, role: 'lead' }],
      },
      agents: { lead: '---\nname: lead\n---\n' },
    });
  };

  const editExpert = async (item: ExpertSummary) => {
    try {
      const bundle = await getExpertBundle(scope, item.name);
      if (bundle) {
        setCreating(false);
        setDraft(bundle);
      }
    } catch (error) {
      toast(error instanceof Error ? error.message : String(error));
    }
  };

  const items = listView?.effectiveExperts || listView?.experts || [];
  const cwd = listView?.cwd || expertWorkspace();

  return (
    <ManageWorkspace>
      <ManageHeader
        eyebrow={t('settings.experts')}
        title={t('settings.expertsTitle')}
        desc={t('settings.expertsDesc')}
        actions={
          <Button onClick={() => void createDraft()}>
            <Plus />
            {t('settings.expertsAdd')}
          </Button>
        }
      />

      <ManageCard title={t('settings.expertsScope')} desc={t('settings.expertsScopeDesc')}>
        <div className="grid max-w-60 grid-cols-2 gap-[3px] rounded-[9px] bg-placeholder p-[3px]">
          {(['global', 'project'] as const).map((entry) => (
            <button
              key={entry}
              type="button"
              className={cn(
                'min-w-0 rounded-md px-[7px] py-[5px] text-center text-[11px] font-semibold text-muted-foreground hover:bg-hoverbg hover:text-foreground',
                scope === entry && 'bg-card text-strong shadow-panel'
              )}
              onClick={() => switchScope(entry)}
            >
              {entry === 'global' ? t('settings.expertsScopeGlobal') : t('settings.expertsScopeProject')}
            </button>
          ))}
        </div>
        {scope === 'project' ? <div className="mt-2 text-[11.5px] text-muted-foreground">{t('settings.expertsProjectCwd', { w: cwd })}</div> : null}
      </ManageCard>

      {draft ? (
        <ExpertEditor
          draft={draft}
          isNew={creating}
          scope={scope}
          onDone={() => {
            setDraft(null);
            setCreating(false);
            void reload(scope);
          }}
          onCancel={() => {
            setDraft(null);
            setCreating(false);
          }}
        />
      ) : null}

      <ManageCard title={t('settings.expertsCatalog')} desc={t('settings.expertsCatalogDesc')}>
        {listView === null ? (
          <div className="text-[11.5px] text-muted-foreground">…</div>
        ) : (
          <RowList className="border-0 bg-transparent">
            {items.length === 0 ? (
              <div className="px-4 py-3 text-[12.5px] text-muted-foreground">{t('settings.expertsEmpty')}</div>
            ) : (
              items.map((item) => {
                const canEdit = item.source === scope;
                return (
                  <RowItem
                    key={item.name}
                    icon={item.source === 'builtin' ? <BookOpen /> : <Users />}
                    title={
                      <>
                        <span className="truncate">{displayName(item)}</span>
                        <Badge variant={item.source === 'builtin' ? 'info' : 'accent'}>{sourceLabel(item.source)}</Badge>
                        {item.invalid ? <Badge variant="outline">{t('settings.expertsInvalid')}</Badge> : null}
                      </>
                    }
                    desc={[item.name, item.expertType, item.invalidReason].filter(Boolean).join(' · ')}
                  >
                    {item.source !== 'builtin' ? (
                      <>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={!canEdit}
                          title={canEdit ? undefined : t('settings.expertsEditScopeHint')}
                          onClick={() => canEdit && void editExpert(item)}
                        >
                          {t('settings.expertsEdit')}
                        </Button>
                        <Button
                          variant="deny"
                          size="sm"
                          disabled={!canEdit}
                          title={canEdit ? undefined : t('settings.expertsDeleteScopeHint')}
                          onClick={async () => {
                            if (!canEdit) return;
                            if (!await confirmDanger(t('settings.expertsDeleteConfirm', { n: item.name }))) return;
                            try {
                              await deleteExpert(scope, item.name);
                              toast(t('settings.expertDeleted'));
                              await reload(scope);
                            } catch (error) {
                              toast(error instanceof Error ? error.message : String(error));
                            }
                          }}
                        >
                          {t('settings.expertsDelete')}
                        </Button>
                      </>
                    ) : null}
                  </RowItem>
                );
              })
            )}
          </RowList>
        )}
      </ManageCard>
    </ManageWorkspace>
  );
}
