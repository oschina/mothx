<script>
  import { onDestroy, onMount } from 'svelte';
  import { currentSession, sessions, setError, setNotice, clearBanners, refreshSessions } from '../lib/stores.js';
  import { request, patchJSON, postJSON } from '../lib/api.js';
  import { shortID } from '../lib/format.js';
  import { navigate } from '../lib/router.js';
  import { language, t } from '../lib/preferences.js';
  import SearchSelect from './settings/SearchSelect.svelte';
  import { Badge } from '$lib/components/ui/badge';
  import { Button } from '$lib/components/ui/button';
  import * as Card from '$lib/components/ui/card';
  import { GitFork, Link2Off, RefreshCw, UsersRound } from '@lucide/svelte';

  let selectedSession = '';
  let experts = [];
  let state = null;
  let selectedID = '';
  let detail = null;
  let loading = false;
  let actionLoading = false;
  let requestGeneration = 0;
  let previousSessionID = '';
  let memberRuns = [];
  let memberRefreshTimer = 0;
  let memberPollingKey = '';

  $: sessionID = selectedSession || $currentSession || '';
  $: selectedSessionInfo = $sessions.find((item) => item.id === sessionID) || null;
  $: sessionBusy = Boolean(selectedSessionInfo?.execution?.busy || selectedSessionInfo?.running);
  // Session titles often mirror the ID for untitled sessions; fall back to the
  // preview so the picker never shows "id · id".
  $: sessionOptions = ($sessions || []).map((item) => {
    const name = item.title && item.title !== item.id ? item.title : (item.preview || '');
    return { value: item.id, label: name ? `${shortID(item.id)} · ${name}` : shortID(item.id) };
  });
  $: boundID = state?.expert?.id || '';
  $: teamBound = state?.sessionId === sessionID && state?.expert?.expertType === 'team';
  $: selectedSummary = experts.find((item) => item.id === selectedID) || null;
  $: if (sessionID !== previousSessionID) {
    previousSessionID = sessionID;
    void loadForSession();
  }

  onMount(() => {
    if (!selectedSession && $currentSession) selectedSession = $currentSession;
    if (!selectedSession && $sessions.length) selectedSession = $sessions[0].id;
  });

  onDestroy(stopMemberPolling);

  $: {
    const nextPollingKey = teamBound && sessionID && boundID ? `${sessionID}:${boundID}` : '';
    if (nextPollingKey !== memberPollingKey) {
      memberPollingKey = nextPollingKey;
      stopMemberPolling();
      if (nextPollingKey) startMemberPolling();
      else memberRuns = [];
    }
  }

  function localized(value) {
    if (!value || typeof value !== 'object') return '';
    return $language === 'zh' ? (value.zh || value.en || '') : (value.en || value.zh || '');
  }

  function isTeam(item) {
    return item?.expertType === 'team';
  }

  function idempotencyKey() {
    if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
    return `webui-expert-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  }

  async function loadForSession() {
    const generation = ++requestGeneration;
    experts = [];
    state = null;
    detail = null;
    selectedID = '';
    if (!sessionID) return;
    loading = true;
    try {
      const encodedSession = encodeURIComponent(sessionID);
      const [catalogResponse, stateResponse] = await Promise.all([
        request(`/api/experts?sessionId=${encodedSession}`),
        request(`/api/sessions/${encodedSession}/expert`)
      ]);
      if (generation !== requestGeneration) return;
      experts = catalogResponse?.experts || [];
      state = stateResponse || null;
      selectedID = state?.expert?.id || experts.find((item) => !item.invalid)?.id || '';
      await loadDetail(selectedID, generation);
    } catch (err) {
      if (generation !== requestGeneration) return;
      setError(err);
    } finally {
      if (generation === requestGeneration) loading = false;
    }
  }

  function startMemberPolling() {
    void loadMemberRuns();
    memberRefreshTimer = window.setInterval(() => void loadMemberRuns(), 1500);
  }

  function stopMemberPolling() {
    if (memberRefreshTimer) window.clearInterval(memberRefreshTimer);
    memberRefreshTimer = 0;
  }

  async function loadMemberRuns() {
    const expectedSession = sessionID;
    const expectedExpert = boundID;
    if (!expectedSession || !expectedExpert || !teamBound) return;
    try {
      const result = await request(`/api/sessions/${encodeURIComponent(expectedSession)}/subagents`);
      if (sessionID === expectedSession && boundID === expectedExpert && teamBound) {
        memberRuns = (result?.subagents || []).filter((item) => item?.expertId === expectedExpert && item?.memberId);
      }
    } catch {
      // This is a live projection refresh. Keep the last canonical snapshot;
      // changing a member card to an error state on a transient poll failure
      // would fabricate a lifecycle transition in the adapter.
    }
  }

  function memberRun(member) {
    if (!member?.id || selectedID !== boundID) return null;
    const matches = memberRuns.filter((item) => item.memberId === member.id);
    if (!matches.length) return null;
    return matches.reduce((latest, item) => {
      if (!latest) return item;
      return String(item.updatedAt || item.startedAt || '') > String(latest.updatedAt || latest.startedAt || '') ? item : latest;
    }, null);
  }

  function memberStatus(member) {
    const run = memberRun(member);
    if (!run) return 'idle';
    if (run.active || run.status === 'ready' || run.status === 'running') return 'working';
    return String(run.status || 'idle').toLowerCase();
  }

  function memberStatusLabel(member) {
    return $t(`experts.memberState.${memberStatus(member)}`);
  }

  function memberStatusVariant(member) {
    const status = memberStatus(member);
    if (status === 'error') return 'destructive';
    if (status === 'working') return 'default';
    return 'secondary';
  }

  async function loadDetail(id, generation = requestGeneration) {
    if (!id || !sessionID) {
      detail = null;
      return;
    }
    try {
      const next = await request(`/api/sessions/${encodeURIComponent(sessionID)}/expert/${encodeURIComponent(id)}`);
      if (generation === requestGeneration) detail = next;
    } catch (err) {
      if (generation === requestGeneration) {
        detail = null;
        setError(err);
      }
    }
  }

  function selectExpert(id) {
    selectedID = id;
    void loadDetail(id);
  }

  async function bindOrSwitch() {
    if (!sessionID || !selectedID || selectedSummary?.invalid || actionLoading || sessionBusy) return;
    clearBanners();
    actionLoading = true;
    try {
      if (boundID && boundID !== selectedID) {
        if (!window.confirm($t('experts.switchConfirm'))) return;
        const result = await postJSON(
          `/api/sessions/${encodeURIComponent(sessionID)}/fork`,
          { expertId: selectedID },
          { headers: { 'Idempotency-Key': idempotencyKey() } }
        );
        const childID = result?.sessionId || '';
        if (!childID) throw new Error($t('experts.forkFailed'));
        selectedSession = childID;
        currentSession.set(childID);
        setNotice($t('experts.switched', { id: selectedID }));
        await refreshSessions();
        navigate(`/chat?session=${encodeURIComponent(childID)}`);
        return;
      }
      const next = await patchJSON(`/api/sessions/${encodeURIComponent(sessionID)}/expert`, { expertId: selectedID });
      state = next;
      setNotice($t('experts.bound', { id: selectedID }));
      await refreshSessions();
    } catch (err) {
      setError(err);
    } finally {
      actionLoading = false;
    }
  }

  async function unbind() {
    if (!sessionID || !boundID || actionLoading || sessionBusy) return;
    clearBanners();
    actionLoading = true;
    try {
      state = await patchJSON(`/api/sessions/${encodeURIComponent(sessionID)}/expert`, { expertId: '' });
      setNotice($t('experts.unbound', { id: boundID }));
      await refreshSessions();
    } catch (err) {
      setError(err);
    } finally {
      actionLoading = false;
    }
  }

  function selectSession(id) {
    selectedSession = id;
  }
</script>

<section class="page experts-page">
  <div class="page-toolbar end">
    <div class="experts-toolbar-actions">
      <label class="experts-session-select">
        <span>{$t('experts.session')}</span>
        <SearchSelect
          value={sessionID}
          options={sessionOptions}
          placeholder={$t('experts.searchSession')}
          ariaLabel={$t('experts.searchSession')}
          noOptionsLabel={$t('experts.noMatchingSession')}
          className="experts-session-search"
          menuClassName="experts-session-search-menu"
          on:change={(event) => selectSession(event.detail)}
        />
      </label>
      <Button type="button" variant="outline" size="sm" onclick={() => loadForSession()} disabled={loading || actionLoading || !sessionID}>
        <RefreshCw size={15} aria-hidden="true" />
        {$t('common.refresh')}
      </Button>
    </div>
  </div>

  {#if !sessionID}
    <Card.Root>
      <Card.Content class="py-8 text-center text-sm text-muted-foreground">{$t('experts.noSession')}</Card.Content>
    </Card.Root>
  {:else}
    {#if state?.expert}
      <div class="experts-current" aria-live="polite">
        <span class="experts-current-label">{$t('experts.current')}</span>
        <strong>{localized(state.expert.displayName) || state.expert.id}</strong>
        <Badge variant="secondary">{$t(`experts.type.${state.expert.expertType}`)}</Badge>
        {#if sessionBusy}<Badge variant="outline">{$t('experts.sessionBusy')}</Badge>{/if}
        <Button type="button" variant="outline" size="sm" disabled={actionLoading || sessionBusy} onclick={unbind}>
          <Link2Off size={15} aria-hidden="true" />
          {$t('experts.unbind')}
        </Button>
      </div>
    {/if}

    <div class="experts-workbench">
      <section class="experts-list" aria-label={$t('experts.available')}>
        <div class="experts-section-heading">
          <h2>{$t('experts.available')}</h2>
          {#if loading}<span class="loading-row"><span class="spinner sm"></span>{$t('common.loading')}</span>{/if}
        </div>
        {#if !loading && experts.length === 0}
          <p class="empty">{$t('experts.empty')}</p>
        {:else}
          <div class="experts-items">
            {#each experts as item (item.id)}
              <button
                type="button"
                class:selected={selectedID === item.id}
                class:invalid={item.invalid}
                class="experts-item"
                onclick={() => selectExpert(item.id)}
                aria-pressed={selectedID === item.id}
              >
                <span class="experts-item-icon" aria-hidden="true"><UsersRound size={17} /></span>
                <span class="experts-item-copy">
                  <strong>{localized(item.displayName) || item.id}</strong>
                  <small>{item.id} · {$t(`experts.type.${item.expertType}`)}</small>
                  <small>{item.source}</small>
                </span>
                {#if item.id === boundID}<Badge variant="success">{$t('experts.boundBadge')}</Badge>{/if}
                {#if item.invalid}<Badge variant="destructive">{$t('experts.invalid')}</Badge>{/if}
              </button>
            {/each}
          </div>
        {/if}
      </section>

      <section class="experts-detail" aria-label={$t('experts.detail')}>
        {#if detail}
          <div class="experts-detail-head">
            <div>
              <div class="experts-kicker">{detail.id}</div>
              <h2>{localized(detail.displayName) || detail.id}</h2>
              <p>{detail.categoryId || $t('experts.uncategorized')}</p>
            </div>
            <Badge variant="secondary">{$t(`experts.type.${detail.expertType}`)}</Badge>
          </div>
          {#if detail.invalid}
            <p class="experts-invalid">{detail.reason || $t('experts.invalid')}</p>
          {:else}
            {#if localized(detail.defaultInitPrompt)}<p class="experts-intro">{localized(detail.defaultInitPrompt)}</p>{/if}
            {#if isTeam(detail)}
              <p class="experts-usage">{$t('experts.usageHint')}</p>
            {/if}
            <div class="experts-actions">
              {#if boundID === selectedID}
                <Badge variant="success">{$t('experts.alreadyBound')}</Badge>
              {:else}
                <Button type="button" disabled={actionLoading || sessionBusy || selectedSummary?.invalid} onclick={bindOrSwitch}>
                  {#if actionLoading}<span class="spinner sm"></span>{/if}
                  {#if boundID}<GitFork size={15} aria-hidden="true" />{/if}
                  {boundID ? $t('experts.switchFork') : $t('experts.bind')}
                </Button>
              {/if}
            </div>
            {#if detail.members?.length}
              <div class="experts-members">
                <h3>{$t('experts.members')}</h3>
                <div class="experts-member-grid">
                  {#each detail.members as member (member.id)}
                    <Card.Root size="sm" class="experts-member-card">
                      <Card.Content class="p-3">
                        <div class="experts-member-head">
                          <div class="experts-member-copy">
                            <strong>{localized(member.name) || member.id}</strong>
                            <span>{localized(member.profession) || member.id}</span>
                            <small>{member.role}</small>
                          </div>
                          <Badge variant={memberStatusVariant(member)}>{memberStatusLabel(member)}</Badge>
                        </div>
                        {#if memberRun(member)}
                          <small class="experts-member-run">{$t('experts.memberRunCount', { count: memberRun(member)?.messageCount || 0 })}</small>
                        {/if}
                      </Card.Content>
                    </Card.Root>
                  {/each}
                </div>
              </div>
            {/if}
          {/if}
        {:else if loading}
          <div class="spinner-center"><span class="spinner lg"></span><span>{$t('common.loading')}</span></div>
        {:else}
          <p class="empty">{$t('experts.selectExpert')}</p>
        {/if}
      </section>
    </div>
  {/if}
</section>

<style>
  .experts-page { min-width: 0; }
  .experts-page h1, .experts-page h2, .experts-page h3, .experts-page p { margin: 0; }
  .experts-toolbar-actions, .experts-current, .experts-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  .experts-session-select { display: flex; align-items: center; gap: 6px; color: var(--text-secondary); font-size: 12px; }
  .experts-session-select :global(.experts-session-search) { width: min(360px, 52vw); }
  .experts-session-select :global(.experts-session-search-menu) { max-height: 300px; }
  .experts-current { border: 1px solid var(--border); background: var(--panel-bg); border-radius: 10px; padding: 10px 12px; font-size: 13px; }
  .experts-current-label, .experts-kicker { color: var(--text-muted); font-size: 11px; text-transform: uppercase; letter-spacing: .08em; }
  .experts-workbench { display: grid; grid-template-columns: minmax(250px, .82fr) minmax(0, 1.4fr); gap: 16px; min-width: 0; align-items: start; }
  .experts-list, .experts-detail { min-width: 0; border: 1px solid var(--border); border-radius: 12px; background: var(--panel-bg); }
  .experts-section-heading { display: flex; justify-content: space-between; align-items: center; padding: 14px 16px; border-bottom: 1px solid var(--border); }
  .experts-section-heading h2, .experts-detail h2 { font-size: 16px; }
  .experts-items { padding: 8px; display: grid; gap: 6px; }
  .experts-item { width: 100%; display: flex; align-items: center; gap: 10px; text-align: left; padding: 10px; border: 1px solid transparent; border-radius: 9px; background: transparent; color: inherit; cursor: pointer; }
  .experts-item:hover, .experts-item.selected { background: var(--hover-bg); border-color: var(--hover-border); }
  .experts-item.invalid { opacity: .65; }
  .experts-item-icon { display: grid; place-items: center; width: 30px; height: 30px; flex: 0 0 auto; color: var(--primary); background: var(--primary-bg); border-radius: 8px; }
  .experts-item-copy { min-width: 0; display: flex; flex: 1; flex-direction: column; gap: 2px; }
  .experts-item-copy strong, .experts-item-copy small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .experts-item-copy small { color: var(--text-muted); font-size: 11px; }
  .experts-detail { padding: 20px; display: flex; flex-direction: column; gap: 16px; }
  .experts-detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .experts-detail-head p { margin-top: 4px; color: var(--text-muted); font-size: 12px; }
  .experts-intro { color: var(--text-secondary); line-height: 1.6; }
  .experts-usage { border-left: 3px solid var(--warning); padding: 8px 10px; color: var(--text-secondary); background: color-mix(in srgb, var(--warning) 8%, transparent); font-size: 13px; line-height: 1.5; }
  .experts-invalid { color: var(--danger); }
  .experts-members { display: flex; flex-direction: column; gap: 10px; }
  .experts-members h3 { font-size: 13px; }
  .experts-member-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 8px; }
  .experts-member-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; }
  .experts-member-copy strong, .experts-member-copy span, .experts-member-copy small { display: block; }
  .experts-member-copy span { margin-top: 3px; color: var(--text-secondary); font-size: 12px; }
  .experts-member-copy small { margin-top: 7px; color: var(--text-muted); font-size: 11px; text-transform: uppercase; }
  .experts-member-run { display: block; margin-top: 10px; color: var(--text-muted); font-size: 11px; }
  .muted { color: var(--text-muted); font-size: 13px; margin-top: 4px !important; }

  @media (max-width: 900px) {
    .experts-workbench { grid-template-columns: minmax(0, 1fr); }
    .experts-toolbar-actions { width: 100%; }
    .experts-session-select { flex: 1; }
    .experts-session-select :global(.experts-session-search) { flex: 1; width: auto; }
  }
</style>
