<script>
  import { onMount, onDestroy, tick } from 'svelte';
  import { get } from 'svelte/store';
  import Workbench from '../components/workbench/Workbench.svelte';
  import ChatPane from '../components/chat/ChatPane.svelte';
  import DraftPane from '../components/chat/DraftPane.svelte';
  import {
    workbench,
    openSessionInWorkbench,
    openDraftInWorkbench,
    replaceDraftInWorkbench,
    selectTabInWorkbench,
    focusGroupInWorkbench,
    closeTabInWorkbench,
    closeOtherTabsInWorkbench,
    closeTabsRightInWorkbench,
    moveTabInWorkbench,
    splitWorkbench,
    removeGroupFromWorkbench,
    resizeWorkbenchSplit,
    setTabModeInWorkbench,
    applyPresetInWorkbench,
    resetWorkbench,
    reconcileWorkbenchSessions,
    focusedSessionId
  } from '../lib/workbench/store.js';
  import { collectTabs, countLeaves, findSessionTab, findTab } from '../lib/workbench/model.js';
  import { currentSession, sessions, sessionsTotal, features, isMobile, setNotice, sidebarOpen } from '../lib/stores.js';
  import { route, replaceNavigate, navigate } from '../lib/router.js';
  import { setVisibleSessions } from '../lib/session-stream.js';
  import { getSessionState, isCompletionActive, isSessionRuntimeBusy } from '../lib/session-runs.js';
  import { t } from '../lib/preferences.js';

  let mounted = false;
  let width = 0;
  let host;
  let terminalOpen = false;
  let dragTabId = '';
  let lastRouteSession = '';
  let knownSessionIDs = new Set();

  $: focusedSession = focusedSessionId($workbench);
  $: tabs = collectTabs($workbench);

  onMount(() => {
    mounted = true;
    const routeSession = $route.query?.session || '';
    lastRouteSession = routeSession;
    if (routeSession) openSessionInWorkbench(routeSession);
    else if ($currentSession) openSessionInWorkbench($currentSession);
    applyRouteViewMode(routeSession || $currentSession);
    return () => {
      mounted = false;
      setVisibleSessions([]);
    };
  });

  onDestroy(() => setVisibleSessions([]));

  // Route -> workbench. The hash is the shareable entry point; focus changes
  // use replaceState below so clicking a pane does not grow history.
  $: if (mounted && $route.section === 'chat') {
    const routeSession = $route.query?.session || '';
    if (routeSession && routeSession !== lastRouteSession) {
      lastRouteSession = routeSession;
      openSessionInWorkbench(routeSession);
      applyRouteViewMode(routeSession);
    }
  }

  // Workbench focus -> currentSession (the shared focus projection used by the
  // sidebar/status bar) and the URL. Opens are driven by the route watcher above
  // because Sidebar/Sessions/Experts navigate as part of setting currentSession;
  // reacting to currentSession separately reopened sessions that had just been
  // closed while a stale route transition was still settling.

  function applyRouteViewMode(sessionId) {
    if (!sessionId || $route.query?.view !== 'trajectory') return;
    const found = findSessionTab(get(workbench), sessionId);
    if (found) setTabModeInWorkbench(found.tab.id, 'trajectory');
  }

  // Focus changes write the URL; currentSession is updated by the action that
  // changed focus so a transient stale focus cannot race route-driven opens.
  $: if (mounted) syncRouteToFocus(focusedSession);

  $: if (mounted) {
    setVisibleSessions(tabs.filter((tab) => tab.kind === 'session').map((tab) => tab.sessionId));
  }

  // Remove tabs for sessions deleted elsewhere after list refreshes.
  $: if (mounted) reconcileDeletedSessions($sessions || []);

  function reconcileDeletedSessions(list) {
    const previous = knownSessionIDs;
    const current = new Set((list || []).map((item) => item?.id).filter(Boolean));
    // /api/sessions is paginated (the sidebar requests the first 100). A tab
    // whose session is simply beyond that page is not deleted. Only reconcile
    // against a page that covers the server-reported total.
    const total = Number(get(sessionsTotal)) || (list || []).length;
    const completePage = total <= (list || []).length;
    // A newly allocated session can be absent from /api/sessions until its
    // first run materializes, and the optimistic list update races that
    // refresh. Never tear down the focused tab or an in-flight submission just
    // because one list response has not caught up yet.
    const focused = focusedSessionId(get(workbench));
    const deleted = completePage
      ? [...previous].filter((id) => !current.has(id) && id !== focused && !isCompletionActive(getSessionState(id)))
      : [];
    if (previous.size > 0 && deleted.length > 0) reconcileWorkbenchSessions(deleted);
    knownSessionIDs = current;
  }

  function syncRouteToFocus(sessionId) {
    const nextSession = sessionId || '';
    const query = { ...($route.query || {}) };
    if (nextSession) query.session = nextSession;
    else delete query.session;
    const currentQuery = $route.query?.session || '';
    // Keep the route->workbench watcher in sync even when the URL already
    // matches: after a reset or a close, the previous value can otherwise make
    // the next explicit open look like a repeated route transition.
    lastRouteSession = nextSession;
    if (currentQuery === nextSession) return;
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value == null || value === '') continue;
      params.set(key, String(value));
    }
    const text = params.toString();
    replaceNavigate(text ? `/chat?${text}` : '/chat');
  }

  function focusSession(sessionId) {
    if (!sessionId) return;
    currentSession.set(sessionId);
    navigate(`/chat?session=${encodeURIComponent(sessionId)}`);
  }

  function reportLayoutFailure(result) {
    if (!result || result.ok) return false;
    const reason = result.reason || {};
    if (reason.code === 'same-position') return false;
    const translate = get(t);
    let message = translate('workbench.limit.generic');
    if (reason.code === 'center-limit') message = translate('workbench.limit.pane', { limit: reason.limit });
    else if (reason.code === 'tab-limit') message = translate('workbench.limit.tab', { limit: reason.limit });
    setNotice(message);
    return true;
  }

  function handleSelectTab(groupId, tabId) {
    const result = selectTabInWorkbench(groupId, tabId);
    if (!result.ok) return;
    const found = findTab(get(workbench), tabId);
    if (found?.tab?.kind === 'session') currentSession.set(found.tab.sessionId);
    if (found?.tab?.kind === 'draft') currentSession.set('');
  }

  function handleFocusGroup(groupId) {
    focusGroupInWorkbench(groupId);
    const state = get(workbench);
    const tab = state.view?.groups?.[groupId]?.tabs?.find((item) => item.id === state.view.groups[groupId].selectedTabId);
    if (tab?.kind === 'session') currentSession.set(tab.sessionId);
  }

  function runningSessionIDs() {
    const ids = [];
    for (const tab of collectTabs(get(workbench))) {
      if (tab.kind !== 'session') continue;
      const state = getSessionState(tab.sessionId);
      if (isCompletionActive(state) || isSessionRuntimeBusy(state?.runtime)) ids.push(tab.sessionId);
    }
    return ids;
  }

  // Closing never blocks or cancels a running task. Say once that it keeps
  // going in the background so the disappearance of the tab is not mistaken
  // for cancellation.
  function noteBackgroundRuns(previousRunningIDs) {
    if (!previousRunningIDs.length) return;
    const stillOpen = new Set(
      collectTabs(get(workbench))
        .filter((tab) => tab.kind === 'session')
        .map((tab) => tab.sessionId)
    );
    if (previousRunningIDs.some((id) => !stillOpen.has(id))) {
      setNotice(get(t)('workbench.backgroundNotice'));
    }
  }

  function handleCloseTab(tabId) {
    const running = runningSessionIDs();
    closeTabInWorkbench(tabId);
    noteBackgroundRuns(running);
    // Always project the resulting focus, even when the closed tab was not
    // focused: a stale currentSession/route can otherwise reopen the closed
    // session on the next navigation.
    const next = focusedSessionId(get(workbench));
    currentSession.set(next);
    syncRouteToFocus(next);
  }

  function handleCloseOthers(groupId, tabId) {
    const running = runningSessionIDs();
    closeOtherTabsInWorkbench(groupId, tabId);
    noteBackgroundRuns(running);
    syncFocusToWorkbench();
  }

  function handleCloseRight(groupId, tabId) {
    const running = runningSessionIDs();
    closeTabsRightInWorkbench(groupId, tabId);
    noteBackgroundRuns(running);
    syncFocusToWorkbench();
  }

  function handleNewTask() {
    const draftId = globalThis.crypto?.randomUUID
      ? globalThis.crypto.randomUUID()
      : `draft-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    const result = openDraftInWorkbench({ draftId, workDir: '' });
    if (result.ok) currentSession.set('');
    else reportLayoutFailure(result);
  }

  function handleSplit(direction) {
    const state = get(workbench);
    const groupId = state.attention?.focusedGroupId;
    const groupView = state.view?.groups?.[groupId];
    const selected = (groupView?.tabs || []).find((tab) => tab.id === groupView?.selectedTabId);
    reportLayoutFailure(splitWorkbench(groupId, direction, selected?.id || '', 'after'));
    syncFocusToWorkbench();
  }

  function handleRemoveGroup(groupId) {
    const running = runningSessionIDs();
    removeGroupFromWorkbench(groupId);
    noteBackgroundRuns(running);
    syncFocusToWorkbench();
  }

  function syncFocusToWorkbench() {
    const state = get(workbench);
    const groupId = state.attention?.focusedGroupId;
    const view = state.view?.groups?.[groupId];
    const tab = (view?.tabs || []).find((item) => item.id === view?.selectedTabId);
    currentSession.set(tab?.kind === 'session' ? tab.sessionId : '');
  }

  function handleResize(splitId, weights) {
    resizeWorkbenchSplit(splitId, weights);
  }

  function handleDropTab(tabId, groupId, side, index) {
    if (!tabId) return;
    if (side === 'middle') {
      reportLayoutFailure(moveTabInWorkbench(tabId, groupId, Number.isFinite(index) ? index : Number.MAX_SAFE_INTEGER));
      syncFocusToWorkbench();
      return;
    }
    const direction = side === 'left' || side === 'right' ? 'horizontal' : 'vertical';
    const placement = side === 'left' || side === 'top' ? 'before' : 'after';
    reportLayoutFailure(splitWorkbench(groupId, direction, tabId, placement));
    syncFocusToWorkbench();
  }

  function handlePreset(presetId) {
    reportLayoutFailure(applyPresetInWorkbench(presetId));
    const focus = focusedSessionId(get(workbench));
    if (focus) currentSession.set(focus);
  }

  function handleReset() {
    resetWorkbench();
    currentSession.set('');
    const routeParams = new URLSearchParams($route.query || {});
    routeParams.delete('session');
    const text = routeParams.toString();
    replaceNavigate(text ? `/chat?${text}` : '/chat');
  }

  function handleModeChange(tab, mode) {
    if (!tab) return;
    setTabModeInWorkbench(tab.id, mode);
    const state = get(workbench);
    const groupId = state.attention?.focusedGroupId;
    const view = state.view?.groups?.[groupId];
    if (view?.selectedTabId === tab.id) {
      const query = { ...($route.query || {}) };
      if (mode === 'trajectory') query.view = 'trajectory';
      else delete query.view;
      const params = new URLSearchParams();
      for (const [key, value] of Object.entries(query)) {
        if (value == null || value === '') continue;
        params.set(key, String(value));
      }
      const text = params.toString();
      replaceNavigate(text ? `/chat?${text}` : '/chat');
    }
  }

  function handleToggleTerminal() {
    terminalOpen = !terminalOpen;
  }

  function handleAttention() {
    const state = get(workbench);
    for (const tab of collectTabs(state)) {
      if (tab.kind !== 'session') continue;
      const runtime = getSessionState(tab.sessionId).runtime;
      const pending = (runtime?.pendingApprovals || []).length + (runtime?.pendingQuestions || []).length;
      if (pending > 0) {
        const found = findSessionTab(state, tab.sessionId);
        if (found) {
          selectTabInWorkbench(found.groupId, found.tab.id);
          focusGroupInWorkbench(found.groupId);
          currentSession.set(tab.sessionId);
        }
        return;
      }
    }
  }

  $: if (mounted) {
    const title = focusedSession ? `MothX · ${focusedSession.slice(0, 8)}` : 'MothX';
    document.title = title;
  }
</script>

<div class="chat-workbench-host" bind:this={host} bind:clientWidth={width}>
    {#snippet pane(groupId, tab)}
      {#if tab?.kind === 'session'}
        {#key tab.id}
          <ChatPane
            sessionId={tab.sessionId}
            active={groupId === $workbench.attention?.focusedGroupId}
            viewMode={tab.mode}
            onModeChange={(mode) => handleModeChange(tab, mode)}
            onFocusSession={focusSession}
          />
        {/key}
      {:else if tab?.kind === 'draft'}
        {#key tab.id}
          <DraftPane
            draftId={tab.draftId}
            active={groupId === $workbench.attention?.focusedGroupId}
            workDir={tab.workDir}
            viewMode={tab.mode}
            onModeChange={(mode) => handleModeChange(tab, mode)}
            onSessionAllocated={(sessionId) => {
              replaceDraftInWorkbench(tab.draftId, sessionId);
              currentSession.set(sessionId);
              syncRouteToFocus(sessionId);
            }}
          />
        {/key}
      {/if}
    {/snippet}
    <!-- Read the terminal focus projection directly from the store: a route-driven
         tab open updates the workbench in the same flush as currentSession, so an
         intermediate $: focus value can lag and leave the dock on the old
         session. The template expression always tracks the store. -->
    <Workbench
      state={$workbench}
      mobile={$isMobile}
      {width}
      {terminalOpen}
      terminalSessionId={focusedSessionId($workbench)}
      terminalAvailable={$features?.terminal === true}
      onResize={handleResize}
      onSelectTab={handleSelectTab}
      onCloseTab={handleCloseTab}
      onCloseOthers={handleCloseOthers}
      onCloseRight={handleCloseRight}
      onFocusGroup={handleFocusGroup}
      onNewTask={handleNewTask}
      onSplit={handleSplit}
      onRemoveGroup={handleRemoveGroup}
      onDropTab={handleDropTab}
      onDragStartTab={(id) => { dragTabId = id; }}
      onPreset={handlePreset}
      onReset={handleReset}
      onToggleTerminal={handleToggleTerminal}
      onAttention={handleAttention}
      onMenu={() => sidebarOpen.update((value) => !value)}
      {pane}
    >
    </Workbench>
</div>

<style>
  .chat-workbench-host { width: 100%; height: 100%; min-width: 0; min-height: 0; overflow: hidden; }
</style>
