<script module>
  // DOM ids must be pane-local: two panes render the same ChatPane markup.
  let paneSequence = 0;
</script>

<script>
  import { onDestroy, onMount, tick } from 'svelte';
  import { get } from 'svelte/store';
  import { markdownToHTML, highlightedCodeToHTML } from '../../lib/markdown.js';
  import { ApiError, patchJSON, postJSON, request } from '../../lib/api.js';
  import { approvalSessionID, approvalRequestOwnership, approvalHistoryFromRunEvents, applyApprovalRequestToRuntime } from '../../lib/approval.js';
  import {
    sessions,
    capabilities,
    upsertSession,
    currentSession,
    selectedModel,
    modelCatalog,
    features,
    setError,
    setNotice,
    clearBanners,
    refreshSessions,
    refreshStatsSummary,
    resetSelectedModelToDefault,
    getSessionMessages,
    getSessionMessagesLatest,
    getSessionMessagesBefore,
    getSessionToolResult,
    getSessionSubAgents,
    getSessionSubAgentMessages,
    getSessionRunEvents,
    getSessionCapabilityEvents,
    getSessionRuntime,
    patchSessionRuntime,
    activeApproval,
    sessionToolOptions,
    sessionToolsFor,
    setSessionTools,
    moveSessionTools
  } from '../../lib/stores.js';
  import { shortID, formatArgs } from '../../lib/format.js';
  import {
    buildToolCallView,
    normalizeSessionMessage,
    shouldRenderAssistantMessage,
    mergeLoadedSessionMessages,
    upsertMessageInList,
    emptySessionView,
    viewFromSessionState,
    sessionStateWithView,
    reduceTranscriptEvent,
    reduceToolStatusEvent,
    reduceRunEvent,
    reduceRunSubmission,
    reduceCapabilityEvent,
    reduceRuntimeSnapshot,
    reduceStreamError,
    reduceApprovalRequest,
    reduceApprovalResolved,
    reduceQuestionRequest,
    reduceQuestionResolved,
    supportsAttachmentDownload,
    maxSeq,
    textFromContents,
    toolResultKind,
    parseReadResult,
    parseLsResult,
    parseGrepResult,
    parseBashResult,
    parseBrowserResult,
    parseSubAgentResult,
    parseWorkflowLintResult
  } from '../../lib/session-view.js';
  import {
    sessionRunStates,
    ensureSessionState,
    getSessionState,
    updateSessionState,
    isCompletionActive,
    isSessionRuntimeBusy,
    isSessionRuntimeRunning,
    canStopSessionRuntime,
    registerCompletion,
    markCompletion,
    clearCompletion,
    abortCompletion,
    completionOwnedBy,
    eventBelongsToActiveRun,
    stopObserver,
    setCompletionRun
  } from '../../lib/session-runs.js';
  import MCPConfigEditor from '../MCPConfigEditor.svelte';
  import DirBrowser from '../DirBrowser.svelte';
  import { language, t } from '../../lib/preferences.js';
  import { safeAttachmentURL, validProviderRef } from '../../lib/attachments.js';
  import { canRetryError, errorDisplayMessage, normalizeErrorInfo, requiresRetryConfirmation } from '../../lib/run-error.js';
  import { route, navigate } from '../../lib/router.js';
  import { createSessionRuntimeManager, displayModeFromSnapshot } from '../../lib/chat-runtime-state.js';
  import { readSessionPreferences, writeSessionPreferences } from '../../lib/session-prefs.js';
  import { clearComposerDraft, readComposerDraft, writeComposerDraft } from '../../lib/composer-drafts.js';
  import { getScrollPosition, saveScrollPosition } from '../../lib/scroll-positions.js';
  import { sessionStateStore, forgetSessionStateStore } from '../../lib/session-store.js';
  import SearchSelect from '../../views/settings/SearchSelect.svelte';
  import ModelPicker from '../ModelPicker.svelte';
  import TrajectoryView from "./TrajectoryView.svelte";
  import SessionHeader from "./SessionHeader.svelte";
  import { Button } from '$lib/components/ui/button';
  import { GitFork, RefreshCw, AlertCircle, RotateCcw, X } from '@lucide/svelte';

  // A ChatPane renders exactly one session. The workbench passes an explicit
  // sessionId; the legacy single-chat wrapper passes the focused session (or
  // '' for a not-yet-created chat).
  export let sessionId = '';
  // Draft tabs have no session id yet. Their composer state is keyed by the
  // draft id so switching tabs (or refreshing) does not lose the text and
  // attachments the user already staged.
  export let draftKey = '';
  export let active = true;
  export let viewMode = '';
  export let initialWorkDir = '';
  export let onModeChange = null;
  export let onFocusSession = null;
  export let onSessionAllocated = null;

  const paneId = `chat-pane-${++paneSequence}`;

  let sessionID = sessionId || '';
  $: sessionID = sessionId || '';
  // Existing drafts are stored under the bare session id; drafts use a
  // separate namespace so a draft id can never collide with a session id.
  $: composerKey = draftKey ? `draft:${draftKey}` : sessionID;
  let selectedModelID = '';
  let prevModelSession = null;
  let paneStateStore = sessionStateStore(sessionID);
  $: paneStateStore = sessionStateStore(sessionID);
  $: paneState = $paneStateStore;
  // The canonical session view lives in sessionRunStates. This pane only
  // mirrors it: every transcript/run/runtime mutation goes through the shared
  // reducers or updateSessionState, so no local copy has to be "persisted"
  // back into the store when the pane switches tabs or unmounts.
  $: view = paneState ? viewFromSessionState(paneState) : emptySessionView();
  $: messages = view.messages;
  $: chatEvents = view.toolEvents;
  $: sessionRunEvents = view.runEvents;
  $: sessionCapabilityEvents = view.capabilityEvents;
  $: sessionRuntimeValue = view.runtime;
  $: sessionStreamCursor = view.cursor;
  $: streamUsesTranscript = Boolean(paneState?.streamUsesTranscript);
  $: optimisticRunEventID = paneState?.optimisticRunEventID || '';
  $: currentRunID = view.currentRunId;
  $: currentIntentID = view.intentId;
  $: currentRunAttempt = view.attempt;
  $: retryProgress = view.retry;
  $: lastRunError = view.lastError;
  $: subAgents = view.subAgents;
  $: subAgentTranscripts = view.subAgentTranscripts;
  $: hostedItems = view.hostedItems;
  $: earliestSeq = paneState?.earliestSeq ?? null;
  $: hasMoreHistory = Boolean(paneState?.hasMore);
  $: loadingHistory = Boolean(paneState?.loading);
  $: sessionHistoryLoadedFor = paneState?.historyLoaded ? sessionID : '';
  $: sessionStreamCompletedFor = view.streamCompleted ? sessionID : '';
  $: approvalHistory = approvalHistoryFromRunEvents(sessionRunEvents);
  $: if (sessionID !== prevModelSession) {
    prevModelSession = sessionID;
    selectedModelID = sessionID ? (readSessionPreferences(sessionID).model || $selectedModel || 'default') : ($selectedModel || 'default');
    selectedProviderID = sessionID ? (readSessionPreferences(sessionID).provider || '') : '';
  }
  // Hydrate the composer from its local draft whenever the pane switches to
  // another session/draft. dataUrl-only attachments are still in memory; after
  // a page reload only providerRef attachments remain and the rest are listed
  // as needing to be selected again.
  let prevComposerKey = null;
  $: if (composerKey !== prevComposerKey) {
    // Legacy single-chat can switch sessions without unmounting this pane;
    // save the outgoing composer before hydrating the next one.
    if (prevComposerKey && composerDirty) flushComposerDraft(prevComposerKey);
    prevComposerKey = composerKey;
    const saved = composerKey ? readComposerDraft(composerKey) : { text: '', attachments: [], missingAttachments: [] };
    prompt = saved.text;
    pendingAttachments = saved.attachments;
    composerDirty = false;
    composerHydrated = true;
    if (saved.missingAttachments.length > 0) {
      setNotice($t('chat.notice.attachmentsNeedReselect', { count: saved.missingAttachments.length }));
    }
  }
  // The catalog can resolve after the pane mounts (especially after a reload),
  // so a session without an explicit preference must adopt the settings default
  // instead of staying on the 'default' placeholder forever.
  $: if (selectedModelID === 'default' && $selectedModel && $selectedModel !== 'default') {
    selectedModelID = $selectedModel;
  }

  let prompt = '';
  let draftTimer = 0;
  let composerHydrated = false;
  let composerDirty = false;
  $: if (composerKey && composerHydrated && composerDirty) scheduleComposerDraft(composerKey, prompt, pendingAttachments);
  function scheduleComposerDraft(id, text, attachments) {
    if (!id) return;
    if (draftTimer) clearTimeout(draftTimer);
    draftTimer = setTimeout(() => {
      draftTimer = 0;
      writeComposerDraft(id, { text, attachments });
    }, 300);
  }
  function flushComposerDraft(key = composerKey) {
    if (!key || !composerDirty) return;
    if (draftTimer) clearTimeout(draftTimer);
    draftTimer = 0;
    writeComposerDraft(key, { text: prompt, attachments: pendingAttachments });
    composerDirty = false;
  }
  function discardComposerDraft() {
    if (draftTimer) clearTimeout(draftTimer);
    draftTimer = 0;
    if (composerKey) clearComposerDraft(composerKey);
    composerDirty = false;
  }
  let composerTextarea;
  let commandSuggestionIndex = 0;
  let commandSuggestionDismissedFor = '';
  let commandSuggestions = [];
  let availableSkills = [];
  let activeSkills = [];
  let showSkillPicker = false;
  let showToolMenu = false;
  let showComposerConfig = false;
  let toolMenuBtn;
  let loadedSkillsKey = '';
  let historyLoadError = null;
  // Scroll-top auto-load gating: only user scrolls that enter the top zone
  // after the initial scroll-to-bottom may trigger history loading.
  // Programmatic scroll resets (refresh, session switch, content replacement
  // after a run) must not trigger it — one spurious event would cascade into
  // loading the entire history.
  let historyAutoLoadReady = false;
  let lastChatScrollTop = -1;
  let workDir = '';
  let dirBrowserOpen = false;
  let sessionCreated = false;
  let attachmentInput;
  let pendingAttachments = [];
  let chatScroll;
  let shouldFollowOutput = true;
  let scrollFrame = 0;
  let historyLoadVersion = 0;
  let retrySubmitting = false;
  let sessionToolKey = '__new__';
  let sessionTools = sessionToolsFor({}, sessionToolKey);
  let showSubAgentModal = false;
  let selectedSubAgentID = '';
  let subAgentModalMessages = [];
  let subAgentHistory;
  let subAgentScrollFrame = 0;
  let subAgentModalLoading = false;
  let subAgentModalError = '';
  let subAgentRefreshTimer = 0;
  let newSessionMode = 'yolo';
  let runtimeDisplayMode = 'work';
  let workToolsExpanded = false;
  let runtimeUpdating = false;
  let approvalHistory = [];
  let runEventCursor = 0;
  let skillPicker;
  let selectedProviderID = '';
  let providerID = '';
  let teamExperts = [];
  let selectedTeamID = '';
  let boundExpertID = '';
  let boundExpertType = '';
  let teamLoading = false;
  let teamApplying = false;
  let teamCatalogKey = '';
  let showApprovalCenter = false;
  // Approvals the user explicitly closed. A closed request stays pending (the
  // agent is still waiting for it) but must not keep re-opening the center; any
  // newer, not-dismissed request re-opens it so permission prompts are never
  // stranded inside the mode/runtime panel.
  let dismissedApprovalIDs = [];
  let showMCPConfig = false;
  let selectedApprovalID = '';
  let approvalSubmitting = false;
  let questionSubmitting = false;
  let selectedQuestionID = '';
  let selectedQuestionAnswers = {};
  // Full-size image preview (lightbox) over the transcript. urls is the gallery
  // opened from a thumbnail (message images, attachment previews, tool detail
  // images); index points at the currently displayed entry.
  let lightbox = null;
  let stopSubmitting = false;
  let runLifecycleVersion = 0;
  // Session runtime state (loading, PATCH updates, mode/display-mode, tool
  // capability sync) lives in the injected manager; it writes canonical
  // snapshots straight into sessionRunStates so every projection agrees.
  const runtimeManager = createSessionRuntimeManager({
    store: {
      get: () => (sessionID ? getSessionState(sessionID).runtime : null),
      set: (next) => {
        if (!sessionID) return;
        updateSessionState(sessionID, (state) => ({ ...state, runtime: next }));
      }
    },
    currentSession: () => sessionID,
    runLifecycle: () => runLifecycleVersion,
    isBusy: () => busy,
    getSessionRuntime,
    patchSessionRuntime,
    getSessionTools: () => sessionTools,
    setSessionTools,
    upsertSession,
    refreshSessions,
    onError: (err) => setError(err),
    onSnapshot: (next) => { runtimeDisplayMode = displayModeFromSnapshot(next); },
    onDisplayMode: (next) => { runtimeDisplayMode = next; },
    onNewSessionMode: (mode) => { newSessionMode = mode; },
    onUpdatingChange: (flag) => { runtimeUpdating = flag; }
  });
  $: chatView = viewMode || ($route.query?.view === 'trajectory' ? 'trajectory' : 'chat');
  $: activeSession = ($sessions || []).find((item) => item?.id === sessionID);
  $: channelBadge = activeSession?.channelLabel || $t('sessions.local');

  function focusSession(id) {
    if (!id) return;
    if (onFocusSession) onFocusSession(id);
    else currentSession.set(id);
  }

  function allocateSession(id) {
    if (!id) return;
    if (onSessionAllocated) onSessionAllocated(id);
    else currentSession.set(id);
  }

  function setChatView(view) {
    if (onModeChange) {
      onModeChange(view === 'trajectory' ? 'trajectory' : 'chat');
      return;
    }
    const params = new URLSearchParams();
    if (sessionID) params.set('session', sessionID);
    if (view === 'trajectory') params.set('view', 'trajectory');
    const query = params.toString();
    navigate(query ? `/chat?${query}` : '/chat');
  }

  const suggestions = [
    'chat.suggestion.projectSummary',
    'chat.suggestion.reviewChanges',
    'chat.suggestion.addTests',
    'chat.suggestion.fixTests',
    'chat.suggestion.refactor',
    'chat.suggestion.configAudit',
    'chat.suggestion.readme',
    'chat.suggestion.multiAgent'
  ];

  // webSearch is MothX's configured local search capability. Provider-native
  // hosted tools are intentionally not listed here because they are enabled by
  // their provider/API rather than a WebUI session switch.
  const toolToggles = [
    { key: 'webSearch', label: 'web_search' },
    { key: 'browser', label: 'browser' },
    { key: 'a2aMaster', label: 'a2aMaster' },
    { key: 'delegate', label: 'delegate' },
    { key: 'multiAgent', label: 'multi-agent' },
    { key: 'workflows', label: 'workflow' }
  ];

  function attachmentDownloadURL(attachment) {
    if (!attachment || !validProviderRef(attachment.providerRef) || !sessionID) return '';
    if (!supportsAttachmentDownload($capabilities)) return '';
    return `/api/attachments/${encodeURIComponent(attachment.providerRef)}?session_id=${encodeURIComponent(sessionID)}`;
  }

  // Reset or load state when the selected session changes.
  let prevSession = sessionID;
  onMount(() => {
    const handleRuntimeOutsidePointer = (event) => {
      if (showSkillPicker && skillPicker && !skillPicker.contains(event.target)) {
        showSkillPicker = false;
      }
      if (showToolMenu && toolMenuBtn && !toolMenuBtn.contains(event.target)) {
        showToolMenu = false;
      }
    };
    document.addEventListener('pointerdown', handleRuntimeOutsidePointer);
    if (sessionID) {
      loadSessionMessages(sessionID);
    } else if (initialWorkDir && !workDir) {
      workDir = initialWorkDir;
    }
    loadSkills();
    return () => document.removeEventListener('pointerdown', handleRuntimeOutsidePointer);
  });
  onDestroy(() => {
    flushComposerDraft();
    saveCurrentScrollPosition();
    if (sessionID) forgetSessionStateStore(sessionID);
    if (subAgentRefreshTimer) clearTimeout(subAgentRefreshTimer);
  });

  $: {
    const nextSession = sessionID;
    if (nextSession !== prevSession) {
      if (prevSession) forgetSessionStateStore(prevSession);
      historyLoadVersion += 1;
      historyLoadError = null;
      historyAutoLoadReady = false;
      lastChatScrollTop = -1;
      closeSubAgentModal();
      activeApproval.set(null);
      selectedApprovalID = '';
      if (nextSession === '') {
        runtimeDisplayMode = 'work';
        sessionCreated = false;
        workDir = '';
        selectedTeamID = '';
        boundExpertID = '';
        boundExpertType = '';
        resetSelectedModelToDefault();
        shouldFollowOutput = true;
      } else {
        const cached = getSessionState(nextSession);
        if (cached.historyLoaded || isCompletionActive(cached)) {
          // The pane renders from sessionRunStates, so a session that is still
          // cached is immediately visible; only scroll/pagination UI resets.
          sessionCreated = true;
          runtimeDisplayMode = displayModeFromSnapshot(cached.runtime);
          void restoreChatScroll(nextSession);
          markHistoryAutoLoadWhenScrolled(nextSession);
        } else {
          loadSessionMessages(nextSession);
        }
      }
      prevSession = nextSession;
    }
  }

  // applySessionViewReducer runs a pure view reducer for session `id` and
  // writes the result back into the canonical sessionRunStates entry. Every
  // pane renders from the same store, so this is the only write path for
  // optimistic submissions and protocol resolutions.
  function applySessionViewReducer(id, reduce, { scroll = false } = {}) {
    if (!id || typeof reduce !== 'function') return { effects: {} };
    let effects = {};
    let previousMessages = null;
    updateSessionState(id, (state) => {
      previousMessages = state.messages;
      const result = reduce(viewFromSessionState(state));
      effects = result.effects || {};
      return sessionStateWithView(state, result.view);
    });
    if (id === sessionID) {
      if (effects.forceScroll) scrollChatToBottom({ force: true });
      else if ((scroll || effects.scroll) && previousMessages !== getSessionState(id).messages) scrollChatToBottom();
    }
    return { effects };
  }

  // Enable scroll-top auto-loading only after the initial scroll-to-bottom
  // completed. The double rAF runs after scrollChatToBottom's own rAF, so
  // scroll events fired by programmatic resets during loading are ignored.
  function markHistoryAutoLoadWhenScrolled(id) {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (id === sessionID) historyAutoLoadReady = true;
      });
    });
  }

  async function loadSessionMessages(id, expectedRunLifecycleVersion = null) {
    const loadVersion = ++historyLoadVersion;
    const requestRunLifecycleVersion = expectedRunLifecycleVersion == null
      ? runLifecycleVersion
      : expectedRunLifecycleVersion;
    historyAutoLoadReady = false;
    historyLoadError = null;
    try {
      const { messages: msgs, hasMore } = await getSessionMessagesLatest(id, 50);
      if (id !== sessionID || requestRunLifecycleVersion !== runLifecycleVersion || loadVersion !== historyLoadVersion) return;
      const normalized = (msgs || []).map((msg) => normalizeSessionMessage(msg, $t)).filter(Boolean);
      updateSessionState(id, (state) => ({
        ...state,
        messages: mergeLoadedSessionMessages(normalized, state.messages),
        earliestSeq: msgs && msgs.length > 0 ? msgs[0].seq : null,
        hasMore: Boolean(hasMore),
        toolEvents: []
      }));
      await loadSessionEvents(id, expectedRunLifecycleVersion);
      if (requestRunLifecycleVersion !== runLifecycleVersion || loadVersion !== historyLoadVersion) return;
      await runtimeManager.load(id, expectedRunLifecycleVersion);
      if (requestRunLifecycleVersion !== runLifecycleVersion || loadVersion !== historyLoadVersion) return;
      updateSessionState(id, (state) => ({
        ...state,
        historyLoaded: true,
        cursor: {
          entrySeq: maxSeq(state.messages),
          runSeq: maxSeq(state.runEvents),
          capabilitySeq: maxSeq(state.capabilityEvents)
        }
      }));
      await restoreChatScroll(id);
      markHistoryAutoLoadWhenScrolled(id);
    } catch (err) {
      if (id !== sessionID || requestRunLifecycleVersion !== runLifecycleVersion || loadVersion !== historyLoadVersion) return;
      // Keep the last known transcript and make the failed load actionable;
      // an unavailable history endpoint must not look like an empty session.
      historyLoadError = normalizeErrorInfo(err) || { message: $t('chat.history.loadFailed') };
      updateSessionState(id, (state) => ({ ...state, historyLoaded: false }));
    }
    sessionCreated = true; // existing session, not "new"
  }

  function minLoadedMessageSeq(list) {
    let min = null;
    for (const m of list || []) {
      const s = Number(m?.seq || 0);
      if (s > 0 && (min == null || s < min)) min = s;
    }
    return min;
  }

  // isOlderThanLoadedHistory reports whether a replayed transcript frame is
  // older than the session's loaded history window. Such frames belong to the
  // paginated region and are fetched on demand via REST instead.
  function isOlderThanLoadedHistory(id, seq) {
    if (id === sessionID) {
      return sessionHistoryLoadedFor === id && earliestSeq != null && seq < earliestSeq;
    }
    const state = getSessionState(id);
    if (!state?.historyLoaded) return false;
    const min = minLoadedMessageSeq(state.messages);
    return min != null && seq < min;
  }

  async function loadMoreHistory() {
    if (loadingHistory || !hasMoreHistory || earliestSeq == null) return;
    const id = sessionID;
    if (!id) return;
    const requestedEarliestSeq = earliestSeq;
    updateSessionState(id, (state) => ({ ...state, loading: true }));
    try {
      const { messages: older, hasMore } = await getSessionMessagesBefore(id, requestedEarliestSeq, 50);
      if (id !== sessionID) return;
      const beforeScrollHeight = chatScroll?.scrollHeight || 0;
      const beforeScrollTop = chatScroll?.scrollTop || 0;
      const normalized = (older || []).map((msg) => normalizeSessionMessage(msg, $t)).filter(Boolean);
      let prepended = 0;
      updateSessionState(id, (state) => {
        // The runs WebSocket replays persisted transcripts on subscribe and may
        // have already merged some of these older entries into the view.
        // Prepending them again would duplicate blocks, so skip entries whose
        // id is already rendered.
        const known = new Set(state.messages.map((m) => m.id).filter(Boolean));
        const fresh = normalized.filter((m) => !m.id || !known.has(m.id));
        prepended = fresh.length;
        return {
          ...state,
          earliestSeq: older && older.length > 0 ? older[0].seq : state.earliestSeq,
          hasMore: Boolean(hasMore),
          messages: fresh.length > 0 ? [...fresh, ...state.messages] : state.messages
        };
      });
      if (prepended > 0) {
        // Preserve scroll position after prepending, keeping the offset the
        // user had within the top zone when the load was triggered.
        await tick();
        if (chatScroll) {
          chatScroll.scrollTop = chatScroll.scrollHeight - beforeScrollHeight + beforeScrollTop;
          // Keep in sync so the restore itself isn't seen as a user scroll.
          lastChatScrollTop = chatScroll.scrollTop;
          saveCurrentScrollPosition();
        }
      }
    } catch (err) {
      if (id === sessionID) {
        historyLoadError = normalizeErrorInfo(err) || { message: $t('chat.history.loadFailed') };
      }
    } finally {
      updateSessionState(id, (state) => (state.loading ? { ...state, loading: false } : state));
    }
  }

  $: selectedRunState = sessionID ? $sessionRunStates[sessionID] : null;
  $: effectiveSessionRuntime = sessionRuntimeValue || selectedRunState?.runtime || null;
  // The local completion covers the short interval before admission returns;
  // after that, Runtime's execution snapshot is the single source of truth.
  $: busy = isCompletionActive(selectedRunState) || isSessionRuntimeBusy(effectiveSessionRuntime);
  $: commandSuggestions = commandSuggestionsFor(prompt);
  $: if (commandSuggestionIndex >= commandSuggestions.length) commandSuggestionIndex = 0;
  $: canStop = isCompletionActive(selectedRunState) || canStopSessionRuntime(effectiveSessionRuntime);
  $: runtimeMode = sessionRuntimeValue?.mode || activeSession?.mode || (!sessionID ? newSessionMode : 'yolo');
  $: toolMessageCount = messages.filter((message) => message.role === 'toolCall' || message.role === 'toolResult').length;
  $: workToolNames = [...new Set(messages
    .filter((message) => message.role === 'toolCall' || message.role === 'toolResult')
    .map((message) => message.toolName)
    .filter(Boolean))];
  $: firstToolMessageIndex = messages.findIndex((message) => message.role === 'toolCall' || message.role === 'toolResult');
  $: pendingApprovalCount = (sessionRuntimeValue?.pendingApprovals || []).length;
  $: pendingQuestions = sessionRuntimeValue?.pendingQuestions || [];
  $: pendingQuestionCount = pendingQuestions.length;
  $: selectedQuestion = pendingQuestions.find((question) => question?.questionId === selectedQuestionID) || pendingQuestions[0] || null;
  $: {
    // Auto-pop the approval center from state, not only from the transient
    // approval_request event: a request can arrive while this session is not
    // the visible one (replay after refresh, background observer, session
    // switch) and would otherwise stay hidden behind the mode/runtime badge.
    const pending = sessionRuntimeValue?.pendingApprovals || [];
    if (pending.length === 0) {
      if (selectedApprovalID) {
        selectedApprovalID = '';
        if (active) activeApproval.set(null);
      }
      if (dismissedApprovalIDs.length) dismissedApprovalIDs = [];
    } else {
      let next = pending.find((approval) => approval.approvalId === selectedApprovalID);
      if (!next) {
        // A request the user has not explicitly closed is selected. A newer
        // request replaces the previous selection.
        next = pending.find((approval) => !dismissedApprovalIDs.includes(approval.approvalId)) || pending[0];
        selectedApprovalID = next.approvalId;
      }
      // Selecting the owning pane (attention bell, tab click, initial mount)
      // must reveal an approval that arrived while the pane was in the
      // background. A dismissed request stays suppressed until it resolves.
      if (active && !dismissedApprovalIDs.includes(next.approvalId)) {
        activeApproval.set(next);
        showApprovalCenter = true;
      }
    }
  }
  $: approvalToolViewValue = approvalToolView(selectedApproval);
  $: selectedApproval = (sessionRuntimeValue?.pendingApprovals || []).find((approval) => approval.approvalId === selectedApprovalID)
    || (approvalSessionID($activeApproval, '') === sessionID ? $activeApproval : null)
    || null;
  $: sessionToolKey = sessionID || '__new__';
  $: sessionTools = sessionToolsFor($sessionToolOptions, sessionToolKey, activeSession || $features);
  $: availableToolToggles = toolToggles.filter((item) => isToolToggleVisible(item, $features));
  $: visibleSessionTools = filterHiddenSessionTools(sessionTools, $features);
  $: sessionEventSummary = buildSessionEventSummary(sessionRunEvents, sessionCapabilityEvents, activeSessionWorkDir, selectedModelID);
  $: subAgentSummary = buildSubAgentSummary(subAgents);
  // The model catalog is resolved server-side with the same provider-factory
  // logic the TUI uses; the picker only projects it.
  $: catalogModels = $modelCatalog.models || [];
  $: providerOptions = ($modelCatalog.providers || []).map((id) => ({ value: id, label: id, id }));
  $: providerID = resolveEffectiveProvider(selectedProviderID, selectedModelID, $modelCatalog, providerOptions);
  $: providerModels = providerID
    ? catalogModels.filter((m) => m.provider === providerID)
    : catalogModels;
  $: activeModel = catalogModels.find((m) => m.id === selectedModelID && m.provider === providerID);
  $: selectedModelSupportsImages = (activeModel?.input || []).includes('image');
  $: apiEnabled = $features.api;
  $: persistentRunError = lastRunError && !messages.some((message) => message?.transientError && message?.runId === lastRunError?.runId)
    ? lastRunError
    : null;
  $: persistentRunErrorMessage = persistentRunError
    ? errorDisplayMessage(persistentRunError, $t, $t('chat.taskFailed'))
    : '';
  $: skillNames = activeSkills;
  $: skillsWorkDir = activeSessionWorkDir || workDir.trim();
  $: skillsContextKey = `${sessionID || ''}:${skillsWorkDir}`;
  $: if (apiEnabled && skillsContextKey && skillsContextKey !== loadedSkillsKey) {
    loadedSkillsKey = skillsContextKey;
    loadSkills();
  }
  $: isNewSession = !sessionID && !sessionCreated;
  $: activeToolCount = availableToolToggles.filter(i => sessionTools[i.key]).length;
  $: activeSessionWorkDir = activeSession?.workDir || workDir.trim();
  $: teamExpertOptions = [
    { value: '', label: $t('chat.expertTeam.none') },
    ...teamExperts.map((expert) => ({
      value: expert.id,
      label: expert.invalid
        ? `${teamExpertLabel(expert, $language)} · ${$t('experts.invalid')}`
        : teamExpertLabel(expert, $language),
      disabled: Boolean(expert.invalid)
    }))
  ];
  $: teamSelectionBound = boundExpertID;
  $: teamSelectionChanged = selectedTeamID !== teamSelectionBound;
  $: teamMustFork = boundExpertID && selectedTeamID && boundExpertID !== selectedTeamID;
  $: selectedTeamInvalid = Boolean(teamExperts.find((expert) => expert.id === selectedTeamID)?.invalid);
  $: teamCanApply = !isNewSession && !busy && !teamApplying && teamSelectionChanged && !teamMustFork && !selectedTeamInvalid;
  // An allocated ID is not a durable session until its first run has been
  // accepted. Keep using the new-chat catalog during that short interval so
  // the selector never queries a not-yet-materialized session.
  $: teamExpertSessionID = sessionID && sessionCreated ? sessionID : '';
  $: teamExpertCatalogContext = `${teamExpertSessionID || '__new__'}:${isNewSession ? workDir.trim() : activeSessionWorkDir}`;
  $: if (apiEnabled && teamExpertCatalogContext !== teamCatalogKey) {
    teamCatalogKey = teamExpertCatalogContext;
    void loadTeamExperts(teamExpertCatalogContext, teamExpertSessionID);
  }
  $: if (sessionID && activeSession?.workDir && workDir !== activeSession.workDir) {
    workDir = activeSession.workDir;
  }
  $: if (!selectedModelSupportsImages && pendingAttachments.some((a) => a.kind === 'image')) {
    pendingAttachments = pendingAttachments.filter((a) => a.kind !== 'image');
    composerDirty = true;
  }
  // The session-stream dispatcher is the only consumer of /ws/runs. Each
  // pane renders its own session's canonical state from sessionRunStates and
  // writes optimistic view changes back through the same reducer/store path.
  let lastHistoryReloadEpoch = 0;
  let lastSubAgentEpoch = 0;
  $: if (paneState?.effects) {
    const effects = paneState.effects;
    const historyEpoch = Number(effects.historyReloadEpoch || 0);
    if (historyEpoch !== lastHistoryReloadEpoch) {
      lastHistoryReloadEpoch = historyEpoch;
      if (historyEpoch > 0 && sessionID) loadSessionMessages(sessionID).catch(() => {});
    }
    const subAgentEpoch = Number(effects.subAgentEpoch || 0);
    if (subAgentEpoch !== lastSubAgentEpoch) {
      lastSubAgentEpoch = subAgentEpoch;
      if (subAgentEpoch > 0 && sessionID) loadSubAgents(sessionID).catch(() => {});
    }
  }

  function pick(text) {
    if (busy) return;
    prompt = text;
    sendPrompt();
  }

  function slashCommandItems() {
    return [
      { value: '/clear', insert: '/clear', description: $t('chat.command.clear') },
      { value: '/mode [plan|agent|yolo|os]', insert: '/mode ', description: $t('chat.command.mode') },
      { value: '/model [model_id]', insert: '/model ', description: $t('chat.command.model') },
      { value: '/defaultModel <provider> <model> [project|global]', insert: '/defaultModel ', description: $t('chat.command.defaultModel') },
      { value: '/models', insert: '/models', description: $t('chat.command.models') },
      { value: '/sessions [ls|clear|del <id>]', insert: '/sessions ', description: $t('chat.command.sessions') },
      { value: '/status', insert: '/status', description: $t('chat.command.status') },
      { value: '/compact', insert: '/compact', description: $t('chat.command.compact') },
      { value: '/delegate [on|off|status]', insert: '/delegate ', description: $t('chat.command.delegate') },
      { value: '/alloweditpath [add <glob>|remove <glob>|clear]', insert: '/alloweditpath ', description: $t('chat.command.allowEditPath') },
      { value: '/allowautoedit [on|off] [global]', insert: '/allowautoedit ', description: $t('chat.command.allowAutoEdit') },
      { value: '/workflows [list|show <id>|cancel <id>]', insert: '/workflows ', description: $t('chat.command.workflows') },
      { value: '/skill <name>', insert: '/skill ', description: $t('chat.command.skill') },
      { value: '/skills', insert: '/skills', description: $t('chat.command.skills') },
      { value: '/rule [force]', insert: '/rule ', description: $t('chat.command.rule') },
      { value: '/esm [objective|edit|pause|resume|clear|guide]', insert: '/esm ', description: $t('chat.command.esm') },
      { value: '/help', insert: '/help', description: $t('chat.command.help') }
    ];
  }

  function esmCommandItems() {
    return [
      { value: '/esm <objective>', insert: '/esm ', description: $t('chat.command.esm') },
      { value: '/esm edit <objective>', insert: '/esm edit ', description: $t('chat.command.esmEdit') },
      { value: '/esm pause', insert: '/esm pause', description: $t('chat.command.esmPause') },
      { value: '/esm resume', insert: '/esm resume', description: $t('chat.command.esmResume') },
      { value: '/esm clear', insert: '/esm clear', description: $t('chat.command.esmClear') },
      { value: '/esm guide <text>', insert: '/esm guide ', description: $t('chat.command.esmGuide') }
    ];
  }

  function commandSuggestionsFor(value) {
    if (busy || !apiEnabled || value.includes('\n') || !value.startsWith('/')) return [];
    const esmMatch = value.match(/^\/esm(?:\s(.*)|$)/);
    if (esmMatch) {
      const query = (esmMatch[1] || '').trim().toLowerCase();
      return esmCommandItems().filter((item) => !query || item.value.slice('/esm '.length).toLowerCase().startsWith(query));
    }
    const query = value.toLowerCase();
    return slashCommandItems().filter((item) => item.value.toLowerCase().startsWith(query));
  }

  function handlePromptInput(event) {
    const next = event.currentTarget.value;
    if (commandSuggestionDismissedFor !== next) commandSuggestionDismissedFor = '';
    commandSuggestionIndex = 0;
    composerDirty = true;
  }

  function acceptCommandSuggestion(item) {
    if (!item) return;
    prompt = item.insert;
    composerDirty = true;
    commandSuggestionIndex = 0;
    commandSuggestionDismissedFor = '';
    tick().then(() => {
      composerTextarea?.focus();
      composerTextarea?.setSelectionRange(prompt.length, prompt.length);
    });
  }

  function handleKeydown(event) {
    const suggestions = commandSuggestionsFor(prompt);
    const visible = suggestions.length > 0 && commandSuggestionDismissedFor !== prompt;
    if (visible) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        commandSuggestionIndex = (commandSuggestionIndex + 1) % suggestions.length;
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        commandSuggestionIndex = (commandSuggestionIndex - 1 + suggestions.length) % suggestions.length;
        return;
      }
      if (event.key === 'Tab') {
        event.preventDefault();
        acceptCommandSuggestion(suggestions[commandSuggestionIndex]);
        return;
      }
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        const item = suggestions[commandSuggestionIndex];
        if (item && item.insert !== prompt) {
          acceptCommandSuggestion(item);
        } else {
          sendPrompt();
        }
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        commandSuggestionDismissedFor = prompt;
        return;
      }
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      sendPrompt();
    }
  }

  async function loadSkills() {
    if (!apiEnabled) return;
    try {
      const params = new URLSearchParams();
      if (sessionID) params.set('sessionId', sessionID);
      const requestedWorkDir = activeSessionWorkDir || workDir.trim();
      // A newly selected WebUI session ID is still only optimistic here. Carry
      // its chosen workDir so this SkillHub preflight cannot materialize the
      // session under the serve-level default before the first run is submitted.
      if (requestedWorkDir) params.set('workDir', requestedWorkDir);
      const data = await fetch(`/api/skillhub/installed?${params}`).then((r) => r.ok ? r.json() : null);
      availableSkills = (data?.installed || []).filter((item) => item?.name).map((item) => ({ name: item.name, description: item.name, active: Boolean(item.active) }));
      const serverActive = data?.session?.activeSkills;
      activeSkills = Array.isArray(serverActive) ? serverActive : availableSkills.filter((item) => item.active).map((item) => item.name);
    } catch { availableSkills = []; }
  }

  function localizedExpertName(value, locale) {
    if (!value || typeof value !== 'object') return '';
    return locale === 'zh' ? (value.zh || value.en || '') : (value.en || value.zh || '');
  }

  function teamExpertLabel(expert, locale) {
    const name = localizedExpertName(expert?.displayName, locale) || expert?.id || '';
    return expert?.id ? `${name} · ${expert.id}` : name;
  }

  async function loadTeamExperts(key, sessionID) {
    teamLoading = true;
    try {
      const suffix = sessionID ? `?sessionId=${encodeURIComponent(sessionID)}` : '';
      const [catalog, state] = await Promise.all([
        request(`/api/experts${suffix}`),
        sessionID ? request(`/api/sessions/${encodeURIComponent(sessionID)}/expert`) : Promise.resolve(null)
      ]);
      if (key !== teamCatalogKey) return;
      teamExperts = (catalog?.experts || []).filter((expert) => expert?.expertType === 'team' || expert?.expertType === 'agent');
      boundExpertID = state?.expert?.id || '';
      boundExpertType = state?.expert?.expertType || '';
      if (sessionID) {
        selectedTeamID = boundExpertID;
      } else if (selectedTeamID && !teamExperts.some((expert) => expert.id === selectedTeamID)) {
        selectedTeamID = '';
      }
    } catch (err) {
      if (key === teamCatalogKey) {
        teamExperts = [];
        boundExpertID = '';
        boundExpertType = '';
        setError(err);
      }
    } finally {
      if (key === teamCatalogKey) teamLoading = false;
    }
  }

  function selectTeamExpert(nextID) {
    const expert = teamExperts.find((item) => item.id === nextID);
    if (expert?.invalid) return;
    if (isNewSession) {
      selectedTeamID = String(nextID || '');
      return;
    }
    if (!sessionID || busy) return;
    selectedTeamID = String(nextID || '');
  }

  async function applyTeamSelection() {
    if (isNewSession || !sessionID || teamApplying || busy) return;
    const expertID = selectedTeamID;
    if (!teamSelectionChanged) return;
    const expert = teamExperts.find((item) => item.id === expertID);
    if (expert?.invalid) return;
    if (boundExpertID && expertID && boundExpertID !== expertID) {
      setNotice($t('chat.expertTeam.switchInExperts'));
      navigate('/experts');
      return;
    }
    clearBanners();
    teamApplying = true;
    try {
      const next = await patchJSON(`/api/sessions/${encodeURIComponent(sessionID)}/expert`, { expertId: expertID });
      boundExpertID = next?.expert?.id || '';
      boundExpertType = next?.expert?.expertType || '';
      selectedTeamID = boundExpertID;
      setNotice(boundExpertID ? $t('chat.expertTeam.bound', { id: boundExpertID }) : $t('chat.expertTeam.unbound'));
      await refreshSessions();
    } catch (err) {
      setError(err);
    } finally {
      teamApplying = false;
    }
  }

  async function toggleSkill(name, event) {
    const next = event.currentTarget.checked ? [...activeSkills, name] : activeSkills.filter((item) => item !== name);
    activeSkills = [...new Set(next)];
    if (!sessionID) return;
    try {
      await postJSON('/api/skillhub/set-active', { sessionId: sessionID, names: activeSkills });
    } catch (err) { setError(err); await loadSkills(); }
  }

  // A POST can be accepted while its HTTP response is lost. Re-submit the
  // exact same body with the same idempotency key a bounded number of times so
  // Serve can return the existing Run instead of creating a second one. A
  // final failure is explicitly transport-unknown and never replays the
  // prompt through a new key.
  async function submitRunWithReconcile(path, payload, options = {}) {
    const signal = options.signal;
    const headers = options.headers || {};
    const maxAttempts = 3;
    let lastError = null;
    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
      if (signal?.aborted) throw new DOMException('The request was aborted.', 'AbortError');
      try {
        return await postJSON(path, payload, { ...options, headers });
      } catch (error) {
        lastError = error;
        if (signal?.aborted || !isSubmissionTransportUnknown(error) || attempt >= maxAttempts) break;
        const retryAfter = Number(error?.retryAfterMs || 0) || 0;
        const delayMs = Math.max(retryAfter, Math.min(4000, 500 * (2 ** (attempt - 1))));
        await waitForReconcileDelay(delayMs, signal);
      }
    }
    if (signal?.aborted) throw new DOMException('The request was aborted.', 'AbortError');
    // Non-transport errors (e.g. session ownership/recovery conflicts, validation) are definitive
    // server responses — rethrow them as-is so callers can inspect the original
    // code/type. Only wrap transport-level ambiguity into submission_unknown.
    if (lastError && !isSubmissionTransportUnknown(lastError)) throw lastError;
    const cause = normalizeErrorInfo(lastError);
    throw new ApiError($t('chat.run.reconcileUnknown'), {
      code: 'submission_unknown',
      type: 'transport_error',
      failureClass: 'transport',
      phase: 'transport',
      messageKey: 'chat.run.reconcileUnknown',
      retryMode: 'reconcile',
      retryable: true,
      runId: cause?.runId || '',
      intentId: cause?.intentId || '',
      cause: lastError
    });
  }

  function isSubmissionTransportUnknown(error) {
    if (!error) return false;
    if (error.name === 'AbortError') return false;
    if (error.name === 'TimeoutError') return true;
    if (error.code === 'network_error' || error.code === 'request_timeout' || error.code === 'submission_unknown') return true;
    return Boolean(error.retryable && ['transport', 'transient'].includes(String(error.failureClass || '').toLowerCase()));
  }

  function waitForReconcileDelay(delayMs, signal) {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(new DOMException('The request was aborted.', 'AbortError'));
        return;
      }
      const timer = setTimeout(() => {
        signal?.removeEventListener('abort', onAbort);
        resolve();
      }, delayMs);
      const onAbort = () => {
        clearTimeout(timer);
        signal?.removeEventListener('abort', onAbort);
        reject(new DOMException('The request was aborted.', 'AbortError'));
      };
      signal?.addEventListener('abort', onAbort, { once: true });
    });
  }

  function applyRunSnapshot(sessionID, snapshot, runId) {
    if (!snapshot || !runId) return false;
    if (!eventBelongsToActiveRun(getSessionState(sessionID), runId)) return false;
    const status = String(snapshot.status || '').toLowerCase();
    const terminal = ['completed', 'failed', 'incomplete', 'cancelled', 'canceled', 'timed_out', 'expired'].includes(status);
    const retrying = snapshot.progress && String(snapshot.progress.state || 'retrying') === 'retrying';
    const eventType = terminal
      ? (status === 'completed' ? 'finished' : status === 'canceled' ? 'canceled' : status)
      : retrying ? 'run_retrying' : 'status';
    const data = {
      ...(snapshot.progress ? { retry: snapshot.progress, progress: snapshot.progress } : {}),
      ...(snapshot.errorInfo ? { errorInfo: snapshot.errorInfo } : {}),
      ...(snapshot.error ? { error: snapshot.error } : {}),
      intentId: snapshot.intentId || '',
      attempt: snapshot.attempt || 0,
      retryOf: snapshot.retryOf || ''
    };
    applySessionViewReducer(sessionID, (view) => ({
      view: reduceRunEvent(view, {
        id: `run-snapshot-${runId}-${snapshot.lastEventSeq || status}`,
        runId,
        intentId: snapshot.intentId || '',
        attempt: snapshot.attempt || 0,
        eventType,
        status: snapshot.status || '',
        seq: snapshot.lastEventSeq || 0,
        data
      })
    }));
    if (terminal) {
      const terminalError = normalizeErrorInfo(snapshot.errorInfo || snapshot.error);
      markCompletion(sessionID, status === 'completed' ? 'completed' : status, terminalError);
    }
    return terminal;
  }

  async function reloadRunStatus(error = persistentRunError) {
    const runId = String(error?.runId || currentRunID || '').trim();
    if (!sessionID) return;
    try {
      if (!runId) {
        await loadSessionMessages(sessionID);
        return;
      }
      const snapshot = await request(`/api/runs/${encodeURIComponent(runId)}`, { timeoutMs: 10000 });
      applyRunSnapshot(sessionID, snapshot, runId);
      await runtimeManager.load(sessionID);
    } catch (err) {
      setError(errorDisplayMessage(normalizeErrorInfo(err) || err, $t, $t('chat.run.reconcileUnknown')));
    }
  }

  async function sendPrompt() {
    const outgoing = prompt.trim();
    const outgoingAttachments = pendingAttachments;
    if ((!outgoing && outgoingAttachments.length === 0) || !apiEnabled) return;
    const outgoingImages = outgoingAttachments.filter((a) => a.kind === 'image');
    if (outgoingImages.length > 0 && !selectedModelSupportsImages) {
      setError($t('chat.error.modelNoImages'));
      return;
    }
    const creatingSession = isNewSession;
    if (creatingSession && !workDir.trim()) {
      setError($t('chat.error.needWorkDir'));
      return;
    }

    let runSessionID = sessionID;
    let creatingExplicitSession = false;
    if (!runSessionID) {
      try {
        const allocated = await postJSON('/api/session-id', {});
        runSessionID = String(allocated?.sessionId || '').trim();
        if (!runSessionID) throw new Error('The server did not return a session ID.');

        // The server owns session identity. Durable session creation remains
        // deferred to the run submission, preserving the new-chat UX.
        creatingExplicitSession = true;
        allocateSession(runSessionID);
      } catch (err) {
        setError(err);
        return;
      }
    }
    const existingState = getSessionState(runSessionID);
    if (
      isCompletionActive(existingState)
      || isSessionRuntimeBusy(sessionRuntimeValue || existingState.runtime)
    ) {
      setError($t('chat.error.sessionActive'));
      return;
    }
    if (creatingExplicitSession) {
      ensureSessionState(runSessionID);
      moveSessionTools('__new__', runSessionID);
      allocateSession(runSessionID);
    }
    stopObserver(runSessionID);
    const runLifecycle = ++runLifecycleVersion;

    const controller = new AbortController();
    const idempotencyKey = newRunRequestKey();
    registerCompletion(runSessionID, controller, { idempotencyKey });
    const optimisticID = beginOptimisticRunEvent(runSessionID);
    updateSessionState(runSessionID, (state) => ({
      ...state,
      streamCompleted: false,
      toolEvents: [],
      streamUsesTranscript: false,
      optimisticRunEventID: optimisticID,
      messages: [...state.messages, { role: 'user', content: outgoing, images: outgoingImages, files: outgoingAttachments.filter((a) => a.kind === 'file') }]
    }));
    if (creatingExplicitSession) {
      upsertSession(buildOptimisticSessionInfo(runSessionID, outgoing));
      refreshSessions().catch(() => {});
    }
    scrollChatToBottom({ force: true });
    prompt = '';
    pendingAttachments = [];
    discardComposerDraft();
    if (attachmentInput) attachmentInput.value = '';
    try {
      // The run is submitted to the server and events are received via
      // WebSocket/SSE. Keep this key for the full submission attempt so a
      // transport-level retry can reconcile rather than create another Run.
      const submitResult = await submitRunWithReconcile(`/api/sessions/${encodeURIComponent(runSessionID)}/runs`, {
        message: outgoing,
        model: selectedModelID || 'default',
        provider: providerID || undefined,
        mode: creatingSession ? newSessionMode : undefined,
        expertId: creatingSession && !selectedTeamInvalid ? selectedTeamID || undefined : undefined,
        tools: visibleSessionTools ? Object.keys(visibleSessionTools).filter(k => visibleSessionTools[k]) : [],
        skills: activeSkills,
        attachments: outgoingAttachments.map((a) => ({
          kind: a.kind,
          filename: a.filename,
          mediaType: a.mediaType,
          dataUrl: a.dataUrl,
          size: a.size
        })),
        transcript: true,
        workDir: workDir.trim() || undefined
      }, {
        signal: controller.signal,
        headers: { 'Idempotency-Key': idempotencyKey }
      });
      if (!completionOwnedBy(getSessionState(runSessionID), controller)) return;
      if (submitResult && submitResult.command) {
        // Slash commands run synchronously on the server and never create a
        // durable Run; render the result as a chat message like the TUI.
        finishOptimisticRunEvent(runSessionID, submitResult.error ? 'failed' : 'completed', submitResult.error ? new Error(submitResult.message || 'command failed') : null, optimisticID);
        applySessionViewReducer(runSessionID, (view) => ({
          view: { ...view, messages: [...view.messages, { role: 'assistant', content: submitResult.message || '', isError: !!submitResult.error }] },
          effects: { forceScroll: true }
        }));
        markCompletion(runSessionID, submitResult.error ? 'failed' : 'completed');
        return;
      }
      recordAcceptedRun(runSessionID, controller, submitResult, idempotencyKey);
      markCompletion(runSessionID, 'running');
      // The run may have reached an approval/question wait before the new
      // session was subscribed to the runs WebSocket. Reconcile the runtime
      // immediately after acceptance so that early decision events cannot
      // leave the agent blocked with no visible prompt.
      await runtimeManager.load(runSessionID, runLifecycle);
      if (creatingExplicitSession) {
        upsertSession(buildOptimisticSessionInfo(runSessionID, outgoing, { running: true }));
        refreshSessions().catch(() => {});
      }
      applySessionViewReducer(runSessionID, (view) => ({
        view: { ...view, messages: [...view.messages, { role: 'assistant', content: '' }] },
        effects: { forceScroll: true }
      }));
      // Events are reduced by the app-wide session stream dispatcher; this
      // pane only waits for the canonical completion in sessionRunStates.
      await waitForRunCompletion(runSessionID, controller.signal);
      finalizeSubmittedRun(runSessionID, controller, optimisticID);
    } catch (err) {
      if (!completionOwnedBy(getSessionState(runSessionID), controller)) return;
      const canceled = err?.name === 'AbortError';
      const error = normalizeErrorInfo(err);
      finishOptimisticRunEvent(runSessionID, canceled ? 'canceled' : 'failed', canceled ? null : error || err, optimisticID);
      if (!canceled) {
        applySessionViewReducer(runSessionID, (view) => reduceStreamError(view, error || err, $t, {
          runId: error?.runId || '',
          intentId: error?.intentId || ''
        }));
      }
      markCompletion(runSessionID, canceled ? 'canceled' : 'failed', canceled ? '' : err);
      if (canceled) setNotice($t('chat.notice.stopped'));
      else if (!error?.runId) setError(errorDisplayMessage(error || err, $t, $t('chat.taskFailed')));
      // When the server reports an execution ownership/recovery conflict, fetch
      // the canonical runtime snapshot so the UI can render the blocking state.
      const executionConflictCodes = new Set([
        'session_run_active',
        'session_run_owned_elsewhere',
        'session_reserved',
        'session_recovery_in_progress',
        'session_recovery_failed',
        'session_execution_state_unavailable'
      ]);
      if (executionConflictCodes.has(error?.code) && runLifecycle === runLifecycleVersion) {
        try {
          const snapshot = await getSessionRuntime(runSessionID);
          if (runLifecycle === runLifecycleVersion) {
            runtimeDisplayMode = displayModeFromSnapshot(snapshot);
            updateSessionState(runSessionID, (s) => ({ ...s, runtime: snapshot }));
          }
        } catch {
          // opportunistic
        }
      }
    } finally {
      clearCompletion(runSessionID, controller);
      try { await refreshSessions(); } catch {
        // opportunistic
      }
      try { await refreshStatsSummary(); } catch {
        // opportunistic
      }
      if (runLifecycle === runLifecycleVersion && !getSessionState(runSessionID).completion) {
        try { await loadSessionMessages(runSessionID, runLifecycle); } catch {
          // opportunistic
        }
        try { await loadSubAgents(runSessionID); } catch {
          // opportunistic
        }
      }
      updateSessionState(runSessionID, (state) => state.optimisticRunEventID === optimisticID
        ? { ...state, optimisticRunEventID: '' }
        : state);
    }
  }

  function newWebUIRequestID() {
    if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
    return `webui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  }

  function newRunRequestKey() {
    return `webui-run-${newWebUIRequestID()}`;
  }

  function acceptedRun(result = {}) {
    return {
      runId: String(result.runId || result.runID || '').trim(),
      intentId: String(result.intentId || result.intentID || '').trim(),
      attempt: Number(result.attempt || 0) || 0,
      status: String(result.status || 'queued').trim() || 'queued'
    };
  }

  function recordAcceptedRun(sessionID, controller, result, idempotencyKey) {
    const submission = acceptedRun(result);
    if (!submission.runId) throw new Error($t('chat.error.runNotAccepted'));
    setCompletionRun(sessionID, controller, { ...submission, idempotencyKey });
    const optimisticID = getSessionState(sessionID).optimisticRunEventID || optimisticRunEventID;
    applySessionViewReducer(sessionID, (view) => {
      let next = reduceRunSubmission(view, submission);
      const index = next.runEvents.findIndex((item) => item.id === optimisticID);
      if (index >= 0) {
        const runEvents = [...next.runEvents];
        runEvents[index] = {
          ...runEvents[index],
          runId: submission.runId,
          status: submission.status,
          data: {
            ...(runEvents[index].data || {}),
            optimistic: false,
            intentId: submission.intentId,
            attempt: submission.attempt
          }
        };
        next = { ...next, runEvents };
      }
      return { view: next };
    });
    return submission;
  }

  function finalizeSubmittedRun(sessionID, controller, optimisticID = '') {
    if (!completionOwnedBy(getSessionState(sessionID), controller)) return;
    const state = getSessionState(sessionID);
    const error = state.lastError;
    const canceled = state.completion?.status === 'cancel_requested';
    const status = canceled ? 'canceled' : error ? 'failed' : 'completed';
    finishOptimisticRunEvent(sessionID, status, error, optimisticID);
    markCompletion(sessionID, status, error);
    sessionCreated = true;
  }

  async function retryRun(message, confirmSideEffects = false) {
    const previousRunID = String(message?.runId || message?.error?.runId || currentRunID || '').trim();
    if (!sessionID || !previousRunID || busy || retrySubmitting) return;

    retrySubmitting = true;
    stopObserver(sessionID);
    const runLifecycle = ++runLifecycleVersion;
    const controller = new AbortController();
    const idempotencyKey = newRunRequestKey();
    registerCompletion(sessionID, controller, { idempotencyKey });
    const optimisticID = beginOptimisticRunEvent(sessionID);
    updateSessionState(sessionID, (state) => ({
      ...state,
      streamCompleted: false,
      streamUsesTranscript: false,
      optimisticRunEventID: optimisticID
    }));
    try {
      const result = await submitRunWithReconcile(`/api/runs/${encodeURIComponent(previousRunID)}/retry`, { confirmSideEffects }, {
        signal: controller.signal,
        headers: { 'Idempotency-Key': idempotencyKey }
      });
      if (!completionOwnedBy(getSessionState(sessionID), controller)) return;
      recordAcceptedRun(sessionID, controller, result, idempotencyKey);
      markCompletion(sessionID, 'running');
      upsertSession({ id: sessionID, active: true, running: true });
      applySessionViewReducer(sessionID, (view) => ({
        view: { ...view, messages: [...view.messages, { role: 'assistant', content: '' }] },
        effects: { forceScroll: true }
      }));
      await waitForRunCompletion(sessionID, controller.signal);
      finalizeSubmittedRun(sessionID, controller, optimisticID);
    } catch (err) {
      if (!completionOwnedBy(getSessionState(sessionID), controller)) return;
      const canceled = err?.name === 'AbortError';
      const error = normalizeErrorInfo(err);
      finishOptimisticRunEvent(sessionID, canceled ? 'canceled' : 'failed', canceled ? null : error || err, optimisticID);
      if (!canceled) {
        applySessionViewReducer(sessionID, (view) => reduceStreamError(view, error || err, $t, {
          runId: error?.runId || previousRunID,
          intentId: error?.intentId || currentIntentID
        }));
      }
      markCompletion(sessionID, canceled ? 'canceled' : 'failed', canceled ? null : err);
      if (canceled) setNotice($t('chat.notice.stopped'));
      else if (!error?.runId) setError(errorDisplayMessage(error || err, $t, $t('chat.taskFailed')));
    } finally {
      clearCompletion(sessionID, controller);
      updateSessionState(sessionID, (state) => state.optimisticRunEventID === optimisticID
        ? { ...state, optimisticRunEventID: '' }
        : state);
      retrySubmitting = false;
      try { await refreshSessions(); } catch {
        // opportunistic
      }
      try { await refreshStatsSummary(); } catch {
        // opportunistic
      }
      if (runLifecycle === runLifecycleVersion && !getSessionState(sessionID).completion) {
        try { await loadSessionMessages(sessionID, runLifecycle); } catch {
          // opportunistic
        }
        try { await loadSubAgents(sessionID); } catch {
          // opportunistic
        }
      }
    }
  }

  function canForkAssistantMessage(message, index) {
    if (busy || !message || message.role !== 'assistant' || message.isError || !message.content || !Number.isInteger(message.seq) || message.seq <= 0) return false;
    for (let i = index + 1; i < messages.length; i += 1) {
      const next = messages[i];
      if (next?.role === 'toolCall' || next?.role === 'toolResult' || next?.role === 'plan') return false;
      if (next?.role === 'assistant') return false;
      if (next?.role === 'user') break;
    }
    return true;
  }

  async function forkFromAssistantMessage(message) {
    if (!sessionID || !canForkAssistantMessage(message, messages.indexOf(message))) return;
    clearBanners();
    const key = globalThis.crypto?.randomUUID
      ? globalThis.crypto.randomUUID()
      : `webui-message-fork-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    try {
      const result = await postJSON(`/api/sessions/${encodeURIComponent(sessionID)}/fork`, { atSeq: message.seq }, {
        headers: { 'Idempotency-Key': key }
      });
      if (!result?.sessionId) return;
      focusSession(result.sessionId);
      if (!onFocusSession) navigate(`/chat?session=${encodeURIComponent(result.sessionId)}`);
    } catch (err) {
      setError(err);
    }
  }

  // waitForRunCompletion uses local events for responsiveness, then queries
  // the canonical Run snapshot to converge after WS/SSE loss. A bounded
  // timeout turns an unconfirmed transport state into an actionable error;
  // it never submits a second request or replays the prompt in the browser.
  async function waitForRunCompletion(sessionID, signal) {
    const pollIntervalMs = 1000;
    const maxWaitMs = 120000;
    const startedAt = Date.now();
    return new Promise((resolve, reject) => {
      let timer = 0;
      let stopped = false;
      const cleanup = () => {
        stopped = true;
        if (timer) clearTimeout(timer);
        signal?.removeEventListener('abort', onAbort);
      };
      const finish = (err) => {
        cleanup();
        if (err) reject(err);
        else resolve();
      };
      const onAbort = () => finish();
      signal?.addEventListener('abort', onAbort, { once: true });

      const locallyFinished = (state) => {
        const status = state?.completion?.status;
        return Boolean(state?.streamCompleted)
          || Boolean(state?.lastError && !state?.retry)
          || status === 'completed'
          || status === 'failed'
          || status === 'canceled'
          || status === 'cancelled'
          || status === 'cancel_requested';
      };

      const poll = async () => {
        if (stopped || signal?.aborted) return finish();
        const state = getSessionState(sessionID);
        if (locallyFinished(state)) return finish();
        const runId = String(state?.completion?.runId || state?.currentRunId || '').trim();
        if (runId) {
          try {
            const snapshot = await request(`/api/runs/${encodeURIComponent(runId)}`, { timeoutMs: 5000, signal });
            applyRunSnapshot(sessionID, snapshot, runId);
            if (locallyFinished(getSessionState(sessionID))) return finish();
          } catch (err) {
            if (err?.name === 'AbortError') return finish();
            // A transient GET failure is not a new execution failure. Keep
            // reconciling until the bounded deadline below.
          }
        }
        if (Date.now() - startedAt >= maxWaitMs) {
          const timeoutError = new ApiError($t('chat.run.reconcileTimeout'), {
            code: 'submission_unknown',
            type: 'transport_error',
            phase: 'transport',
            messageKey: 'chat.run.reconcileTimeout',
            retryMode: 'reconcile',
            retryable: true,
            runId
          });
          return finish(timeoutError);
        }
        timer = setTimeout(() => { poll().catch(finish); }, pollIntervalMs);
      };
      poll().catch(finish);
    });
  }

  function buildOptimisticSessionInfo(id, firstMessage = '', overrides = {}) {
    const now = new Date().toISOString();
    const cwd = workDir.trim() || activeSessionWorkDir || '';
    return {
      id,
      workDir: cwd,
      mode: overrides.mode || newSessionMode || runtimeMode || 'yolo',
      active: true,
      running: false,
      lastUsed: now,
      messageCount: Math.max(1, messages.length || 1),
      preview: firstMessage,
      title: '',
      ...overrides
    };
  }

  async function stop() {
    if (!sessionID || stopSubmitting || !canStop) return;
    const id = sessionID;
    const stopLifecycle = runLifecycleVersion;
    const stopController = getSessionState(id).completion?.controller;
    const responseRun = sessionRuntimeValue?.responsesRun;
    const activeRun = sessionRuntimeValue?.execution?.activeRun || sessionRuntimeValue?.activeRun;
    stopSubmitting = true;
    if (stopController && !completionOwnedBy(getSessionState(id), stopController)) {
      stopSubmitting = false;
      return;
    }
    markCompletion(id, 'cancel_requested');
    if (activeRun || responseRun) {
      updateSessionState(id, (state) => ({
        ...state,
        runtime: {
          ...state.runtime,
          ...(activeRun
            ? { activeRun: { ...activeRun, status: 'cancelling' } }
            : { responsesRun: { ...responseRun, state: 'cancelling', cancelRequested: true } })
        }
      }));
    }
    try {
      await postJSON(`/api/sessions/${encodeURIComponent(id)}/stop`, {});
      if (stopLifecycle === runLifecycleVersion
        && (!stopController || completionOwnedBy(getSessionState(id), stopController))) {
        abortCompletion(id);
      }
      if (stopLifecycle === runLifecycleVersion) setNotice($t('chat.notice.stopped'));
      const snapshot = await getSessionRuntime(id);
      if (stopLifecycle === runLifecycleVersion) {
        runtimeDisplayMode = displayModeFromSnapshot(snapshot);
        updateSessionState(id, (state) => ({ ...state, runtime: snapshot }));
      }
    } catch (err) {
      if (stopLifecycle === runLifecycleVersion) setError(err);
      if (stopLifecycle === runLifecycleVersion && err?.code === 'no_active_run') {
        markCompletion(id, 'failed', err);
      }
      if (stopLifecycle === runLifecycleVersion) {
        await runtimeManager.load(id, stopLifecycle);
      }
    } finally {
      stopSubmitting = false;
    }
  }

  function handleChatScroll() {
    if (!chatScroll) return;
    shouldFollowOutput = isChatNearBottom();
    saveCurrentScrollPosition();
    // Scroll to top: load more history. Edge-triggered: only a user scroll
    // that moves from outside into the top zone counts; programmatic scroll
    // resets (refresh, session switch, message reload after a run) fire
    // scroll events too and must not start a load cascade.
    const top = chatScroll.scrollTop;
    const enteredTopZone = top < 80 && lastChatScrollTop >= 80;
    lastChatScrollTop = top;
    if (historyAutoLoadReady && enteredTopZone && hasMoreHistory && !loadingHistory) {
      loadMoreHistory();
    }
  }

  function isChatNearBottom() {
    if (!chatScroll) return true;
    const distance = chatScroll.scrollHeight - chatScroll.scrollTop - chatScroll.clientHeight;
    return distance < 96;
  }

  function saveCurrentScrollPosition() {
    if (!sessionID || !chatScroll) return;
    saveScrollPosition(sessionID, {
      top: chatScroll.scrollTop,
      following: isChatNearBottom()
    });
  }

  // restoreChatScroll restores the per-session position saved before the pane
  // was unmounted. A pane that was following the output goes back to the
  // bottom (new content may have arrived while hidden); a pane the user had
  // scrolled up keeps its exact offset.
  async function restoreChatScroll(id) {
    const saved = getScrollPosition(id);
    await tick();
    if (!chatScroll || id !== sessionID) return;
    if (!saved || saved.following) {
      shouldFollowOutput = true;
      await scrollChatToBottom({ force: true });
      saveCurrentScrollPosition();
      return;
    }
    chatScroll.scrollTop = saved.top;
    lastChatScrollTop = chatScroll.scrollTop;
    shouldFollowOutput = false;
    saveCurrentScrollPosition();
  }

  async function scrollChatToBottom({ force = false } = {}) {
    if (!chatScroll) return;
    if (!force && !shouldFollowOutput) return;
    await tick();
    if (!chatScroll) return;
    if (scrollFrame) cancelAnimationFrame(scrollFrame);
    scrollFrame = requestAnimationFrame(() => {
      scrollFrame = 0;
      if (!chatScroll) return;
      if (!force && !shouldFollowOutput) return;
      chatScroll.scrollTop = chatScroll.scrollHeight;
      shouldFollowOutput = true;
    });
  }

  async function updateToolOption(key, event) {
    const targetSession = sessionID;
    const previousTools = sessionTools;
    const nextTools = filterHiddenSessionTools({
      ...sessionTools,
      [key]: Boolean(event.currentTarget.checked)
    }, $features);
    setSessionTools(sessionToolKey, nextTools);
    if (!targetSession) return;
    try {
      const updated = await patchSessionRuntime(
        targetSession,
        { capabilities: { [key]: Boolean(event.currentTarget.checked) } }
      );
      if (targetSession === sessionID) {
        updateSessionState(targetSession, (state) => ({ ...state, runtime: updated }));
        setSessionTools(targetSession, { ...nextTools, [key]: Boolean(updated?.capabilities?.[key]?.enabled) });
        await loadSessionEvents(targetSession);
      }
      await refreshSessions();
    } catch (err) {
      setSessionTools(targetSession, previousTools);
      setError(err);
    }
  }

  function isWebSearchAvailable(featureState = {}) {
    // /api/status is the effective runtime configuration. Capabilities only
    // describes what the server can support and remains available even when
    // the configured local web_search service is disabled.
    return featureState.webSearch === true;
  }

  function isToolToggleVisible(item, featureState = {}) {
    if (item?.key === 'webSearch') return isWebSearchAvailable(featureState);
    if (item?.key === 'a2aMaster') return featureState.a2aMaster === true;
    return true;
  }

  function filterHiddenSessionTools(tools = {}, featureState = {}) {
    return {
      ...tools,
      webSearch: isWebSearchAvailable(featureState) && tools.webSearch === true,
      a2aMaster: featureState.a2aMaster === true && tools.a2aMaster === true
    };
  }

  async function chooseWorkDir() {
    const desktop = globalThis.__MOTHX_DESKTOP__;
    if (desktop?.chooseDirectory) {
      try {
        const selected = await desktop.chooseDirectory(workDir.trim());
        if (selected) workDir = selected;
      } catch (err) {
        setError(err);
      }
      return;
    }
    try {
      const result = await postJSON('/api/select-directory', { defaultPath: workDir.trim() }, { timeoutMs: 0 });
      if (result?.canceled === false && result?.path) {
        workDir = result.path;
      }
    } catch (err) {
      // The native OS picker can be unavailable (e.g. headless servers)
      // or fail to open (500/501). Fall back to the built-in web directory
      // browser so any directory stays selectable by default.
      if (err?.status === 501 || err?.status === 500) {
        dirBrowserOpen = true;
        return;
      }
      setError(err);
    }
  }

  async function handleFileSelect(event) {
    const files = Array.from(event.target.files || []);
    if (files.length === 0) return;
    let rejectedImages = false;
    try {
      const results = await Promise.all(files.map((file) => readFile(file).catch((err) => {
        if (err?.message === 'image_model_required') {
          rejectedImages = true;
          return null;
        }
        throw err;
      })));
      if (rejectedImages) {
        setError($t('chat.error.modelNoImages'));
      }
      const valid = results.filter(Boolean);
      pendingAttachments = [...pendingAttachments, ...valid];
      if (valid.length > 0) composerDirty = true;
    } catch (err) {
      setError(err);
    } finally {
      event.target.value = '';
    }
  }

  function readFile(file) {
    const isImage = file.type.startsWith('image/');
    if (isImage && !selectedModelSupportsImages) {
      return Promise.reject(new Error('image_model_required'));
    }
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve({
        kind: isImage ? 'image' : 'file',
        name: file.name,
        filename: file.name,
        mediaType: file.type || 'application/octet-stream',
        size: file.size,
        dataUrl: String(reader.result || '')
      });
      reader.onerror = () => reject(new Error($t('chat.error.attachmentReadFailed', { name: file.name })));
      reader.readAsDataURL(file);
    });
  }

  function removeAttachment(index) {
    pendingAttachments = pendingAttachments.filter((_, i) => i !== index);
    composerDirty = true;
  }

  function clearAttachments() {
    pendingAttachments = [];
    composerDirty = true;
    if (attachmentInput) attachmentInput.value = '';
  }

  function formatFileSize(bytes) {
    if (!bytes) return '';
    if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }

  // --- Image lightbox ---
  // All transcript image thumbnails (message images, attachment previews, tool
  // detail images) open the same full-size overlay so previews never send users
  // to a separate tab just to see the image.

  function openLightbox(urls, index = 0) {
    if (!urls?.length) return;
    lightbox = { urls, index: Math.min(Math.max(0, index), urls.length - 1) };
  }

  function closeLightbox() {
    lightbox = null;
  }

  function stepLightbox(dir) {
    if (!lightbox?.urls?.length) return;
    const count = lightbox.urls.length;
    lightbox = { ...lightbox, index: (lightbox.index + dir + count) % count };
  }

  function handleLightboxKeydown(event) {
    // Every pane registers the window listener; only the focused one may act.
    if (!active || !lightbox) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      closeLightbox();
    } else if (event.key === 'ArrowLeft') {
      event.preventDefault();
      stepLightbox(-1);
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      stepLightbox(1);
    }
  }

  // Collect the image preview entries of an assistant message's attachments so
  // previewing one attachment can navigate through all of its images.
  function attachmentImageGallery(attachments = []) {
    const gallery = [];
    for (const attachment of attachments) {
      let src = '';
      if (attachment?.kind === 'image' && safeAttachmentURL(attachment.url)) {
        src = safeAttachmentURL(attachment.url);
      } else if ((attachment?.mediaType || '').startsWith('image/') && attachmentDownloadURL(attachment)) {
        src = attachmentDownloadURL(attachment);
      }
      if (src) gallery.push({ src, name: attachment?.name || attachment?.kind || '' });
    }
    return gallery;
  }

  // Intercept clicks on image attachments: preview in the lightbox instead of
  // navigating away. Non-image attachments keep their normal link behavior.
  function openAttachmentPreview(event, attachment, attachments = []) {
    const isImage = attachment?.kind === 'image' || (attachment?.mediaType || '').startsWith('image/');
    if (!isImage) return;
    event.preventDefault();
    const gallery = attachmentImageGallery(attachments);
    const src = attachment?.kind === 'image' && safeAttachmentURL(attachment.url)
      ? safeAttachmentURL(attachment.url)
      : attachmentDownloadURL(attachment);
    openLightbox(gallery, Math.max(0, gallery.findIndex((item) => item.src === src)));
  }


  function recordApprovalResolution(resolution, sessionID) {
    if (!resolution?.approvalId || !sessionID) return;
    const id = `approval-resolution-${resolution.approvalId}-${resolution.status || 'resolved'}`;
    applySessionViewReducer(sessionID, (view) => ({
      view: reduceRunEvent(view, {
        id,
        sessionId: sessionID,
        eventType: 'approval_resolved',
        status: resolution.status || 'resolved',
        timestamp: resolution.timestamp || new Date().toISOString(),
        data: { resolution }
      })
    }));
  }

  function approvalToolView(approval) {
    const tool = approval?.tool || {};
    return buildToolCallView(tool.name || '', tool.args || tool.details || {}, '', $t);
  }

  function approvalBashCommand(approval) {
    const args = approval?.tool?.args || {};
    return approval?.tool?.details?.command || args.command || args.cmd || '';
  }

  function approvalBashWorkDir(approval) {
    return approval?.tool?.details?.workDir || approval?.context?.workDir || '';
  }

  async function respondApproval(approval, action) {
    const targetSessionID = approvalSessionID(approval, sessionID);
    if (!approval?.approvalId || !targetSessionID || approvalSubmitting) return;
    approvalSubmitting = true;
    try {
      const resolved = await postJSON(`/api/sessions/${encodeURIComponent(targetSessionID)}/approvals/${encodeURIComponent(approval.approvalId)}`, { action });
      recordApprovalResolution(resolved, targetSessionID);
      applySessionViewReducer(targetSessionID, (view) => ({ view: reduceApprovalResolved(view, resolved) }));
      if (targetSessionID === sessionID) {
        activeApproval.set(null);
        selectedApprovalID = '';
        showApprovalCenter = false;
      }
    } catch (err) { setError(err); }
    finally { approvalSubmitting = false; }
  }

  async function respondQuestion(question, answer) {
    const targetSessionID = question?.sessionId || sessionID;
    if (!question?.questionId || !targetSessionID || questionSubmitting) return;
    questionSubmitting = true;
    try {
      const resolved = await postJSON(`/api/sessions/${encodeURIComponent(targetSessionID)}/questions/${encodeURIComponent(question.questionId)}`, { answer });
      applySessionViewReducer(targetSessionID, (view) => ({ view: reduceQuestionResolved(view, resolved) }));
      if (targetSessionID === sessionID) {
        selectedQuestionID = '';
        delete selectedQuestionAnswers[question.questionId];
      }
    } catch (err) { setError(err); }
    finally { questionSubmitting = false; }
  }
  async function loadSessionEvents(id, expectedRunLifecycleVersion = null) {
    if (!id) return;
    const requestRunLifecycleVersion = expectedRunLifecycleVersion == null
      ? runLifecycleVersion
      : expectedRunLifecycleVersion;
    try {
      const [runs, caps] = await Promise.all([
        getSessionRunEvents(id),
        getSessionCapabilityEvents(id)
      ]);
      if (id !== sessionID
        || requestRunLifecycleVersion !== runLifecycleVersion) return;
      updateSessionState(id, (state) => {
        let next = {
          ...viewFromSessionState(state),
          runEvents: [],
          capabilityEvents: caps || [],
          cursor: { ...state.cursor, runSeq: 0 },
          currentRunId: '',
          intentId: '',
          attempt: 0,
          retry: null,
          lastError: null
        };
        for (const event of runs || []) next = reduceRunEvent(next, event);
        return sessionStateWithView(state, next);
      });
    } catch (err) {
      if (id !== sessionID
        || requestRunLifecycleVersion !== runLifecycleVersion) return;
      updateSessionState(id, (state) => ({ ...state, runEvents: [], capabilityEvents: [] }));
      throw err;
    }
  }

  async function loadSubAgents(id) {
    if (!id) return;
    try {
      const agents = await getSessionSubAgents(id);
      if (id !== sessionID) return;
      let merged = [];
      updateSessionState(id, (state) => {
        merged = mergeSubAgents(state.subAgents, agents || []);
        return { ...state, subAgents: merged };
      });
      if (showSubAgentModal) {
        if (!selectedSubAgentID && merged.length > 0) {
          selectedSubAgentID = merged[0].id;
        }
        if (selectedSubAgentID) {
          await loadSubAgentMessages(selectedSubAgentID);
        }
      }
    } catch (err) {
      if (id === sessionID && showSubAgentModal) {
        subAgentModalError = errorDisplayMessage(normalizeErrorInfo(err) || err, $t, $t('chat.subagents.loadFailed'));
      }
      throw err;
    }
  }

  function scheduleSubAgentRefresh(delay = 250) {
    if (!sessionID) return;
    if (subAgentRefreshTimer) clearTimeout(subAgentRefreshTimer);
    const targetSession = sessionID;
    subAgentRefreshTimer = setTimeout(() => {
      subAgentRefreshTimer = 0;
      if (targetSession === sessionID) {
        loadSubAgents(targetSession).catch(() => {});
      }
    }, delay);
  }

  function mergeSubAgents(existing = [], incoming = []) {
    const byID = new Map();
    for (const item of existing) {
      if (item?.id) byID.set(item.id, item);
    }
    for (const item of incoming) {
      if (!item?.id) continue;
      byID.set(item.id, { ...byID.get(item.id), ...item });
    }
    return Array.from(byID.values()).sort((a, b) => {
      const left = Date.parse(a.startedAt || a.updatedAt || '') || 0;
      const right = Date.parse(b.startedAt || b.updatedAt || '') || 0;
      if (left !== right) return left - right;
      return String(a.id).localeCompare(String(b.id));
    });
  }

  async function loadSubAgentMessages(agentID) {
    if (!sessionID || !agentID) {
      subAgentModalMessages = [];
      return;
    }
    subAgentModalLoading = true;
    subAgentModalError = '';
    try {
      const msgs = await getSessionSubAgentMessages(sessionID, agentID);
      if (agentID !== selectedSubAgentID) return;
      const normalized = (msgs || []).map((msg) => normalizeSessionMessage(msg, $t)).filter(Boolean);
      const live = subAgentTranscripts[agentID] || [];
      subAgentModalMessages = mergeMessageLists(normalized, live);
    } catch (err) {
      subAgentModalError = errorDisplayMessage(normalizeErrorInfo(err) || err, $t, $t('chat.subagents.loadFailed'));
      subAgentModalMessages = subAgentTranscripts[agentID] || [];
    } finally {
      subAgentModalLoading = false;
    }
  }

  function mergeMessageLists(base = [], live = []) {
    let out = [...base];
    for (const item of live) {
      out = upsertMessageInList(out, item);
    }
    return out;
  }

  function openSubAgentModal(agentID = '') {
    selectedSubAgentID = agentID || selectedSubAgentID || subAgents[0]?.id || '';
    showSubAgentModal = true;
    if (sessionID) {
      loadSubAgents(sessionID).catch(() => {});
    }
    if (selectedSubAgentID) {
      loadSubAgentMessages(selectedSubAgentID).catch(() => {});
    }
  }

  function closeSubAgentModal() {
    showSubAgentModal = false;
    subAgentModalError = '';
  }

  function selectSubAgent(agentID) {
    selectedSubAgentID = agentID;
    subAgentModalMessages = subAgentTranscripts[agentID] || [];
    loadSubAgentMessages(agentID).catch(() => {});
  }

  function beginOptimisticRunEvent(targetSessionID = sessionID) {
    const id = `local-run-${Date.now()}`;
    const runID = `local_${Date.now()}`;
    const event = {
      id,
      runId: runID,
      sessionId: targetSessionID || '',
      eventType: 'started',
      source: 'webui',
      status: 'running',
      model: selectedModelID || 'default',
      mode: activeSession?.mode || '',
      timestamp: new Date().toISOString(),
      data: {
        workDir: isNewSession ? workDir.trim() : activeSessionWorkDir,
        optimistic: true
      }
    };
    if (targetSessionID) {
      updateSessionState(targetSessionID, (state) => ({
        ...state,
        runEvents: [...state.runEvents.filter((item) => item.id !== id), event]
      }));
    }
    return id;
  }

  function finishOptimisticRunEvent(sessionID, status, error = null, optimisticID = '') {
    const localID = sessionID ? optimisticID || getSessionState(sessionID).optimisticRunEventID || optimisticRunEventID : '';
    if (localID && sessionID) {
      applySessionViewReducer(sessionID, (view) => {
        const idx = view.runEvents.findIndex((item) => item.id === localID);
        if (idx < 0) return { view };
        const eventType = status === 'failed' ? 'failed' : status === 'canceled' ? 'canceled' : 'finished';
        const runEvents = [...view.runEvents];
        runEvents[idx] = {
          ...runEvents[idx], eventType, status, timestamp: new Date().toISOString(),
          data: { ...(runEvents[idx].data || {}), ...(error ? { error } : {}) }
        };
        return { view: { ...view, runEvents } };
      });
    }
    if (sessionID) upsertSession({ id: sessionID, active: true, running: false });
  }


  function retryProgressLabel(progress) {
    const attempt = Number(progress?.attempt || 0) || 1;
    const maxAttempts = Number(progress?.maxAttempts || 0) || attempt;
    const localized = progress?.messageKey
      ? $t(progress.messageKey, { attempt, maxAttempts, retryAfterMs: Number(progress.retryAfterMs || 0) || 0 })
      : '';
    const base = localized && localized !== progress?.messageKey
      ? localized
      : $t('chat.retrying', { attempt, maxAttempts });
    const retryAfterMs = Number(progress?.retryAfterMs || 0) || 0;
    const label = retryAfterMs
      ? `${base} ${$t('chat.retry.after', { seconds: Math.max(1, Math.ceil(retryAfterMs / 1000)) })}`
      : base;
    // Append the sanitized provider diagnostic (when the Runtime persisted one)
    // after the friendly label, mirroring errorDisplayMessage's message+detail.
    const detail = String(progress?.message || '').trim();
    return detail ? `${label}: ${detail}` : label;
  }

  // handleSubAgentEffects applies view-only side effects reported by reducers
  // (sub-agent list refresh, open modal sync). Visible session only.
  function handleSubAgentEffects(effects = {}) {
    if (effects.subAgentRefresh) scheduleSubAgentRefresh();
    if (showSubAgentModal && selectedSubAgentID && effects.subAgentTranscriptAgent === selectedSubAgentID) {
      subAgentModalMessages = subAgentTranscripts[selectedSubAgentID] || [];
    }
  }

  // The history view is a live stream. Keep the newest event visible after the
  // modal opens and whenever the selected sub-agent receives another event.
  $: if (showSubAgentModal && subAgentModalMessages) scrollSubAgentHistoryToBottom();

  async function scrollSubAgentHistoryToBottom() {
    await tick();
    if (!subAgentHistory) return;
    if (subAgentScrollFrame) cancelAnimationFrame(subAgentScrollFrame);
    subAgentScrollFrame = requestAnimationFrame(() => {
      subAgentScrollFrame = 0;
      if (subAgentHistory) subAgentHistory.scrollTop = subAgentHistory.scrollHeight;
    });
  }

  function buildSessionEventSummary(runEvents = [], capabilityEvents = [], workDir = '', model = '') {
    const runs = mergeRunEvents(runEvents);
    const currentModel = model && model !== 'default' ? model : '';
    const matchingRuns = runs.filter((run) => {
      if (!run.usage && !run.contextUsage) return false;
      if (currentModel && run.model && run.model !== currentModel) return false;
      if (workDir && run.workDir && run.workDir !== workDir) return false;
      return true;
    });
    const contextRun = matchingRuns.find((run) => run.contextUsage) || runs.find((run) => run.contextUsage);
    const totals = runs.reduce((acc, run) => {
      if (!run.usage) return acc;
      acc.promptTokens += run.usage.promptTokens;
      acc.completionTokens += run.usage.completionTokens;
      acc.totalTokens += run.usage.totalTokens;
      acc.cacheReadTokens += run.usage.cacheReadTokens;
      acc.cacheWriteTokens += run.usage.cacheWriteTokens;
      return acc;
    }, { promptTokens: 0, completionTokens: 0, totalTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0 });
    return {
      visible: runs.length > 0 || capabilityEvents.length > 0,
      lastRun: runs[0] || null,
      runCount: runs.length,
      capabilityCount: capabilityEvents.length,
      model: currentModel || runs[0]?.model || '',
      workDir: workDir || runs[0]?.workDir || '',
      matchingRuns: matchingRuns.length,
      contextUsage: contextRun?.contextUsage || null,
      ...totals
    };
  }

  function buildSubAgentSummary(agents = []) {
    const list = (agents || []).filter((item) => item?.id);
    const running = list.filter((item) => item.status === 'running' || item.status === 'ready').length;
    const failed = list.filter((item) => item.status === 'error' || item.status === 'failed').length;
    const done = list.filter((item) => item.status === 'done' || item.status === 'destroyed').length;
    return {
      visible: list.length > 0,
      count: list.length,
      running,
      failed,
      done,
      label: running > 0
        ? $t('chat.subagents.running', { count: running, total: list.length })
        : failed > 0
          ? $t('chat.subagents.failed', { count: failed, total: list.length })
          : $t('chat.subagents.done', { count: done || list.length, total: list.length })
    };
  }

  function selectModel(modelID) {
    selectedModelID = modelID;
    persistSessionModel();
  }

  function persistSessionModel() {
    if (!sessionID) return;
    writeSessionPreferences(sessionID, { model: selectedModelID, provider: selectedProviderID });
  }

  function defaultModelForProvider(providerID, catalog) {
    if (!providerID) return '';
    const filtered = (catalog?.models || []).filter((m) => m.provider === providerID);
    if (filtered.length === 0) return '';
    const defaultModel = catalog?.defaultModel;
    if (defaultModel && filtered.some((m) => m.id === defaultModel)) return defaultModel;
    return filtered[0]?.id || '';
  }

  function resolveSelectedProvider(currentModel, catalog, options) {
    if (options.length === 0) return '';
    const fromModel = (catalog?.models || []).find((m) => m.id === currentModel);
    if (fromModel?.provider && options.some((o) => o.value === fromModel.provider)) {
      return fromModel.provider;
    }
    // Fall back to the server's active provider, then to the first option.
    const fallback = catalog?.defaultProvider;
    if (fallback && options.some((o) => o.value === fallback)) {
      return fallback;
    }
    return options[0]?.value || '';
  }

  function resolveEffectiveProvider(selectedProvider, currentModel, catalog, options) {
    if (selectedProvider && options.some((o) => o.value === selectedProvider)) {
      if ((catalog?.models || []).some((m) => m.id === currentModel && m.provider === selectedProvider)) {
        return selectedProvider;
      }
    }
    return resolveSelectedProvider(currentModel, catalog, options);
  }

  function handleProviderChange(newProviderID) {
    if (!newProviderID) return;
    const current = selectedModelID;
    const available = catalogModels.filter((m) => m.provider === newProviderID);
    if (available.length === 0) return;
    selectedProviderID = newProviderID;
    if (!available.some((m) => m.id === current)) {
      selectedModelID = defaultModelForProvider(newProviderID, $modelCatalog);
    }
    persistSessionModel();
  }
  function subAgentStateClass(agent) {
    if (!agent) return 'done';
    if (agent.status === 'error' || agent.status === 'failed') return 'error';
    if (agent.status === 'running' || agent.status === 'ready') return 'running';
    return 'done';
  }

  $: selectedSubAgent = subAgents.find((item) => item.id === selectedSubAgentID) || null;

  function subAgentStatusLabel(status) {
    if (status === 'running' || status === 'ready') return $t('common.running');
    if (status === 'error' || status === 'failed') return $t('common.failed');
    if (status === 'destroyed') return $t('chat.subagents.destroyed');
    return $t('common.completed');
  }

  function mergeRunEvents(events = []) {
    const byRun = new Map();
    for (const event of events) {
      const runId = event.runId || event.id || '';
      if (!runId) continue;
      const run = byRun.get(runId) || {
        runId,
        eventType: '',
        status: '',
        source: '',
        data: null,
        model: '',
        mode: '',
        workDir: '',
        timestamp: '',
        usage: null
      };
      const eventTime = Date.parse(event.timestamp || '') || 0;
      const runTime = Date.parse(run.timestamp || '') || 0;
      if (eventTime >= runTime) {
        run.timestamp = event.timestamp || run.timestamp;
        run.eventType = event.eventType || run.eventType;
        run.status = event.status || run.status;
      }
      if (event.model) run.model = event.model;
      if (event.mode) run.mode = event.mode;
      if (event.source) run.source = event.source;
      if (event.data && typeof event.data === 'object') run.data = { ...(run.data || {}), ...event.data };
      if (event.data?.workDir) run.workDir = event.data.workDir;
      const usage = normalizeRunUsage(event.data?.usage);
      if (usage) run.usage = usage;
      const contextUsage = normalizeContextUsage(event.data?.contextUsage || event.data?.context_usage);
      if (contextUsage) run.contextUsage = contextUsage;
      byRun.set(runId, run);
    }
    return Array.from(byRun.values())
      .sort((a, b) => (Date.parse(b.timestamp || '') || 0) - (Date.parse(a.timestamp || '') || 0));
  }

  function normalizeRunUsage(raw) {
    if (!raw || typeof raw !== 'object') return null;
    const promptTokens = readNumber(raw, ['prompt_tokens', 'promptTokens', 'inputTokens', 'input']);
    const completionTokens = readNumber(raw, ['completion_tokens', 'completionTokens', 'outputTokens', 'output']);
    const cacheReadTokens = readNumber(raw, ['cache_read_tokens', 'cacheReadTokens', 'cacheRead', 'cached_tokens']);
    const cacheWriteTokens = readNumber(raw, ['cache_write_tokens', 'cacheWriteTokens', 'cacheWrite']);
    const explicitTotal = readNumber(raw, ['total_tokens', 'totalTokens']);
    const totalTokens = explicitTotal || promptTokens + completionTokens;
    if (promptTokens === 0 && completionTokens === 0 && totalTokens === 0 && cacheReadTokens === 0 && cacheWriteTokens === 0) return null;
    return { promptTokens, completionTokens, totalTokens, cacheReadTokens, cacheWriteTokens };
  }

  function normalizeContextUsage(raw) {
    if (!raw || typeof raw !== 'object') return null;
    const totalTokens = readNumber(raw, ['total_tokens', 'totalTokens', 'tokens']);
    const contextWindow = readNumber(raw, ['context_window', 'contextWindow']);
    if (contextWindow <= 0) return null;
    const reportedPercent = Number(raw.percent);
    const percent = Number.isFinite(reportedPercent)
      ? reportedPercent
      : (totalTokens / contextWindow) * 100;
    return { totalTokens, contextWindow, percent };
  }

  function readNumber(source, keys) {
    for (const key of keys) {
      const value = Number(source?.[key]);
      if (Number.isFinite(value) && value > 0) return value;
    }
    return 0;
  }

  function sessionRunStateClass(run) {
    if (!run) return 'done';
    if (run.status === 'failed' || run.eventType === 'failed') return 'error';
    if (run.status === 'running' || run.eventType === 'started') return 'running';
    return 'done';
  }

  function sessionRunLabel(run) {
    if (!run) return $t('chat.sessionEvents.idle');
    if (run.status === 'running' || run.eventType === 'started') return $t('common.running');
    if (run.status === 'failed' || run.eventType === 'failed') return $t('common.failed');
    if (run.status === 'canceled' || run.eventType === 'canceled') return $t('chat.sessionEvents.canceled');
    return $t('common.completed');
  }

  function isCronRun(run) {
    return run?.source === 'cron' || Boolean(run?.data?.cronJobId);
  }

  function cronRunName(run) {
    return run?.data?.cronJobName || '';
  }

  function formatCompactTokens(value) {
    const n = Number(value) || 0;
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(n >= 10_000 ? 0 : 1)}K`;
    return String(n);
  }

  function formatCacheRate(summary) {
    if (!summary || summary.promptTokens <= 0) return '--';
    const pct = Math.min(100, Math.max(0, (summary.cacheReadTokens / summary.promptTokens) * 100));
    return `${Math.round(pct)}%`;
  }

  function formatContextUsageRate(usage) {
    if (!usage || usage.contextWindow <= 0) return '--';
    return `${Math.round(Math.max(0, usage.percent))}%`;
  }

  function compactPath(path) {
    if (!path) return '';
    const normalized = String(path).replace(/[\\/]+$/, '');
    const parts = normalized.split(/[\\/]/).filter(Boolean);
    if (parts.length <= 2) return normalized || '/';
    return `.../${parts.slice(-2).join('/')}`;
  }

  function compactWorkDir(path) {
    if (!path) return '';
    const parts = String(path).split(/[\\/]/).filter(Boolean);
    if (parts.length === 0) return '/';
    return parts[parts.length - 1];
  }

  function sessionEventTooltip(summary) {
    if (!summary) return '';
    const parts = [];
    if (isCronRun(summary.lastRun)) {
      parts.push($t('chat.sessionEvents.cron'));
      if (cronRunName(summary.lastRun)) parts.push(cronRunName(summary.lastRun));
    }
    if (summary.workDir) parts.push(summary.workDir);
    if (summary.model) parts.push(summary.model);
    parts.push(`${formatCompactTokens(summary.totalTokens)} tokens`);
    if (summary.contextUsage) {
      parts.push($t('chat.sessionEvents.context', { rate: formatContextUsageRate(summary.contextUsage) }));
    }
    parts.push(`cache ${formatCacheRate(summary)}`);
    if (summary.lastRun?.timestamp) parts.push(formatEventTime(summary.lastRun.timestamp));
    return parts.join(' · ');
  }

  function formatEventTime(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }

  function planStatusLabel(status) {
    switch (status) {
      case 'done': return $t('chat.plan.done');
      case 'running': return $t('chat.plan.running');
      case 'failed': return $t('chat.plan.failed');
      default: return $t('chat.plan.pending');
    }
  }

  async function loadToolResultDetail(msg, event) {
    if (!event.currentTarget.open || !msg.hasDetail || msg.detailLoaded || msg.detailLoading) return;
    if (!sessionID || !msg.toolCallId) return;
    msg.detailLoading = true;
    msg.detailError = '';
    touchMessages();
    try {
      const detail = await getSessionToolResult(sessionID, msg.toolCallId);
      msg.detail = normalizeToolResultDetail(detail);
      msg.detailLoaded = true;
    } catch (err) {
      msg.detailError = err instanceof Error ? err.message : String(err || $t('chat.tool.detailLoadFailed'));
    } finally {
      msg.detailLoading = false;
      touchMessages();
    }
  }

  // Tool detail is mutated in place on a renderer-owned message object; cloning
  // the array is the minimal store write needed to re-render the pane.
  function touchMessages() {
    if (!sessionID) return;
    updateSessionState(sessionID, (state) => ({ ...state, messages: [...state.messages] }));
  }

  function normalizeToolResultDetail(detail) {
    if (!detail) return { content: '', images: [] };
    const images = [];
    for (const block of detail.contents || []) {
      if (block.type !== 'image' || !block.image?.data || !block.image?.mimeType) continue;
      images.push({
        name: block.image.mimeType,
        type: block.image.mimeType,
        size: block.image.bytes || block.image.originalBytes || 0,
        dataUrl: `data:${block.image.mimeType};base64,${block.image.data}`
      });
    }
    const content = detail.content || textFromContents(detail.contents);
    return {
      toolName: detail.toolName || '',
      kind: toolResultKind(detail.toolName, content),
      content: detail.content || textFromContents(detail.contents),
      images,
      readLines: parseReadResult(content),
      lsEntries: parseLsResult(content),
      grepMatches: parseGrepResult(content),
      bashResult: parseBashResult(content),
      browserResult: parseBrowserResult(content),
      subAgentResult: parseSubAgentResult(content),
      workflowLintResult: parseWorkflowLintResult(content)
    };
  }

  function codeBlockControls(node) {
    const handleClick = (event) => { void copyCodeBlock(event); };
    node.addEventListener('click', handleClick);
    node.addEventListener('toggle', updateCodeBlockToggle);
    return {
      destroy() {
        node.removeEventListener('click', handleClick);
        node.removeEventListener('toggle', updateCodeBlockToggle);
      }
    };
  }

  async function copyCodeBlock(event) {
    const button = event.target.closest('.code-copy');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();
    const code = button.closest('.code-block')?.querySelector('code')?.textContent || '';
    if (!code) return;
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(code);
      } else {
        const textarea = document.createElement('textarea');
        textarea.value = code;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();
        document.execCommand('copy');
        textarea.remove();
      }
      button.textContent = 'Copied';
      button.classList.add('copied');
      window.setTimeout(() => {
        button.textContent = 'Copy';
        button.classList.remove('copied');
      }, 1600);
    } catch {
      button.textContent = 'Failed';
      window.setTimeout(() => { button.textContent = 'Copy'; }, 1600);
    }
  }

  function updateCodeBlockToggle(event) {
    const block = event.target.closest('.code-block');
    if (!block) return;
    const toggle = block.querySelector('.code-block-toggle');
    if (toggle) toggle.textContent = block.open ? toggle.dataset.collapse : toggle.dataset.expand;
  }
</script>

<section class="chat-view">
  <SessionHeader
    title={activeSession?.title && activeSession.title !== sessionID
      ? activeSession.title
      : (activeSession?.preview || (sessionID ? shortID(sessionID) : ''))}
    channelLabel={activeSession?.channelLabel || ''}
    sessionID={sessionID}
    workDir={sessionID ? activeSessionWorkDir : ''}
    view={chatView}
    {busy}
    onViewChange={setChatView}
  />
  {#if chatView === 'trajectory'}
    <TrajectoryView
      sessionID={sessionID}
      messages={messages}
      runEvents={sessionRunEvents}
      capabilityEvents={sessionCapabilityEvents}
      toolEvents={chatEvents}
      busy={busy}
    />
  {:else}
  {#if subAgentSummary.visible}
    <button type="button" class="subagent-strip" on:click={() => openSubAgentModal()}>
      <span class="dot {subAgentSummary.failed > 0 ? 'error' : subAgentSummary.running > 0 ? 'running' : 'done'}"></span>
      <strong>{$t('chat.subagents.title')}</strong>
      <span>{subAgentSummary.label}</span>
      <em>{$t('chat.subagents.open')}</em>
    </button>
  {/if}
  <div class="chat-scroll" bind:this={chatScroll} on:scroll={handleChatScroll}>
    {#if loadingHistory}
      <div class="chat-history-loading">{$t('common.loading')}</div>
    {/if}
    {#if historyLoadError}
      <div class="chat-history-error" role="alert">
        <span>{errorDisplayMessage(historyLoadError, $t, $t('chat.history.loadFailed'))}</span>
        <Button type="button" variant="ghost" size="sm" onclick={() => loadSessionMessages(sessionID)}>
          <RefreshCw size={14} />
          <span>{$t('chat.history.retryLoad')}</span>
        </Button>
      </div>
    {/if}
    {#if messages.length === 0 && !busy}
      <div class="welcome">
        <h2>{$t('chat.welcome')}</h2>
        <div class="suggestions">
          {#each suggestions as key}
            <button
              type="button"
              class="chip"
              disabled={!apiEnabled || (isNewSession && !workDir.trim())}
              on:click={() => pick($t(key))}
            >
              {$t(key)}
            </button>
          {/each}
        </div>
      </div>
    {:else}
      <div class="transcript">
        {#each messages as msg, idx}
          {#if msg.role === 'user'}
            <article class="msg user">
              <div class="meta">
                <strong>{$t('chat.you')}</strong>
              </div>
              <p>{msg.content}</p>
              {#if msg.images?.length}
                <div class="msg-images">
                  {#each msg.images as image, imageIndex}
                    <button type="button" class="msg-image-button" aria-label={image.name || $t('chat.imagePreview')} on:click={() => openLightbox(msg.images.map((img) => ({ src: img.dataUrl, name: img.name })), imageIndex)}>
                      <img src={image.dataUrl} alt={image.name} on:load={() => scrollChatToBottom()} />
                    </button>
                  {/each}
                </div>
              {/if}
              {#if msg.files?.length}
                <div class="msg-files">
                  {#each msg.files as file}
                    <span class="msg-file">
                      <span class="file-icon">📄</span>
                      <span class="file-name" title={file.filename}>{file.filename}</span>
                      <span class="file-meta">{file.mediaType}</span>
                      <span class="file-meta">{formatFileSize(file.size)}</span>
                    </span>
                  {/each}
                </div>
              {/if}
            </article>
          {:else if shouldRenderAssistantMessage(msg, idx, messages.length, busy)}
            <article class="msg assistant" class:error={msg.isError}>
              <div class="meta">
                <strong>MothX</strong>
                <span>{msg.isError ? $t('common.failed') : busy && idx === messages.length - 1 ? $t('chat.generating') : $t('common.completed')}</span>
                {#if canForkAssistantMessage(msg, idx)}
                  <Button type="button" variant="ghost" size="xs" onclick={() => forkFromAssistantMessage(msg)} title={$t('chat.forkFromMessage')}>
                    <GitFork size={14} />
                    <span>{$t('chat.forkFromMessage')}</span>
                  </Button>
                {/if}
              </div>
              {#if msg.content}
                <div class="markdown" use:codeBlockControls>{@html markdownToHTML(msg.content)}</div>
              {:else if busy && idx === messages.length - 1}
                <p class="pending-text">{retryProgress ? retryProgressLabel(retryProgress) : $t('chat.waitingModel')}</p>
              {/if}
              {#if msg.content && retryProgress && busy && idx === messages.length - 1 && !msg.isError}
                <p class="pending-text">{retryProgressLabel(retryProgress)}</p>
              {/if}
              {#if msg.isError && (msg.retryable || requiresRetryConfirmation(msg.error))}
                <div class="msg-retry">
                  {#if msg.retryable && msg.runId}
                    <Button type="button" variant="ghost" size="sm" disabled={busy || retrySubmitting} onclick={() => retryRun(msg)}>
                      <RotateCcw size={14} />
                      <span>{$t('chat.retry')}</span>
                    </Button>
                  {/if}
                  {#if requiresRetryConfirmation(msg.error)}
                    <Button type="button" variant="destructive" size="sm" disabled={busy || retrySubmitting} onclick={() => retryRun(msg, true)}>
                      <AlertCircle size={14} />
                      <span>{$t('chat.retry.confirm')}</span>
                    </Button>
                  {/if}
                </div>
              {/if}
              {#if msg.attachments?.length}
                <div class="response-attachments" aria-label={$t('chat.attachments')}>
                  {#each msg.attachments as attachment}
                    {#if safeAttachmentURL(attachment.url)}
                      <a href={safeAttachmentURL(attachment.url)} target="_blank" rel="noreferrer" class="response-attachment" on:click={(event) => openAttachmentPreview(event, attachment, msg.attachments)}>
                        {#if attachment.kind === 'image'}
                          <img class="response-attachment-preview" src={safeAttachmentURL(attachment.url)} alt={attachment.name || attachment.kind} loading="lazy" />
                        {/if}
                        <span>{attachment.name || attachment.kind}</span>
                        <span class="response-attachment-kind">{attachment.kind}</span>
                      </a>
                    {:else if attachmentDownloadURL(attachment)}
                      <a href={attachmentDownloadURL(attachment)} download class="response-attachment" on:click={(event) => openAttachmentPreview(event, attachment, msg.attachments)}>
                        {#if attachment.mediaType?.startsWith('image/')}
                          <img class="response-attachment-preview" src={attachmentDownloadURL(attachment)} alt={attachment.name || attachment.kind} loading="lazy" />
                        {/if}
                        <span>{attachment.name || attachment.kind}</span>
                        <span class="response-attachment-kind">{attachment.providerRef}</span>
                      </a>
                    {:else}
                      <span class="response-attachment"><span>{attachment.name || attachment.kind}</span><span class="response-attachment-kind">{attachment.providerRef}</span></span>
                    {/if}
                  {/each}
                </div>
              {/if}
            </article>
          {:else if msg.role === 'plan'}
            <article class="msg plan-card">
              <div class="meta">
                <strong>{$t('chat.plan')}</strong>
                {#if msg.toolCallId}<span>{shortID(msg.toolCallId)}</span>{/if}
              </div>
              <section class="todo-plan">
                {#if msg.plan.title}
                  <h3>{msg.plan.title}</h3>
                {/if}
                <ol>
                  {#each msg.plan.steps as step}
                    <li class:done={step.status === 'done'} class:running={step.status === 'running'} class:failed={step.status === 'failed'}>
                      <span class="todo-mark" aria-hidden="true"></span>
                      <span class="todo-title">{step.title}</span>
                      <em>{planStatusLabel(step.status)}</em>
                    </li>
                  {/each}
                </ol>
                {#if msg.plan.note}
                  <p>{msg.plan.note}</p>
                {/if}
              </section>
            </article>
          {:else if msg.role === 'toolCall'}
            <article class="msg tool-call" class:work-tool-hidden={runtimeDisplayMode === 'work' && !workToolsExpanded}>
              <div class="meta">
                <strong>{$t('chat.toolCall')}</strong>
                <span>{msg.toolName}</span>
              </div>
              <div class="tool-call-body">
                <div class="tool-title">
                  <span class="dot running"></span>
                  <strong>{msg.callView?.label || msg.toolName}</strong>
                  {#if msg.callView?.target}
                    <span class="tool-target">{msg.callView.target}</span>
                  {/if}
                  {#if msg.toolCallId}<em>{shortID(msg.toolCallId)}</em>{/if}
                </div>
                {#if msg.callView?.details?.length}
                  <div class="tool-call-tags">
                    {#each msg.callView.details as item}
                      <span>{item}</span>
                    {/each}
                  </div>
                {/if}
                {#if msg.callView?.kind === 'edit' && msg.callView.edits?.length}
                  <div class="edit-call">
                    {#each msg.callView.edits as edit}
                      <section class="edit-block">
                        <div class="edit-block-head">
                          <strong>{$t('chat.tool.edit.editNumber', { number: edit.index })}</strong>
                          <span>{$t('chat.tool.edit.lineChange', { old: edit.oldLines, next: edit.newLines })}</span>
                        </div>
                        <div class="edit-columns">
                          <div class="edit-pane old">
                            <span>{$t('chat.tool.edit.oldText')}</span>
                            <pre class:empty={edit.oldText === ''}><code>{@html edit.oldText ? highlightedCodeToHTML(edit.oldText, msg.callView.target) : $t('chat.tool.edit.empty')}</code></pre>
                          </div>
                          <div class="edit-pane new">
                            <span>{$t('chat.tool.edit.newText')}</span>
                            <pre class:empty={edit.newText === ''}><code>{@html edit.newText ? highlightedCodeToHTML(edit.newText, msg.callView.target) : $t('chat.tool.edit.empty')}</code></pre>
                          </div>
                        </div>
                      </section>
                    {/each}
                  </div>
                {:else if msg.callView?.kind === 'write'}
                  <div class="write-call">
                    <div class="write-call-head">
                      <strong>{$t('chat.tool.write.preview')}</strong>
                      <span>{$t('chat.tool.write.summary', { lines: msg.callView.lines, chars: msg.callView.chars })}</span>
                    </div>
                    <span>{$t('chat.tool.write.content')}</span>
                    <pre class:empty={msg.callView.content === ''}>{msg.callView.content || $t('chat.tool.edit.empty')}</pre>
                  </div>
                {:else if msg.callView?.kind === 'insert'}
                  <div class="write-call">
                    <div class="write-call-head">
                      <strong>{$t('chat.tool.insert.preview')}</strong>
                      <span>{$t('chat.tool.insert.summary', { lines: msg.callView.lines, chars: msg.callView.chars })}</span>
                    </div>
                    <span>{$t('chat.tool.insert.content')}</span>
                    <pre class:empty={msg.callView.content === ''}>{msg.callView.content || $t('chat.tool.edit.empty')}</pre>
                  </div>
                {:else if msg.callView?.kind === 'find'}
                  <div class="find-call">
                    <div class="find-row">
                      <span>{$t('chat.tool.find.pattern')}</span>
                      <code>{msg.callView.pattern || $t('chat.tool.find.missing')}</code>
                    </div>
                    <div class="find-row">
                      <span>{$t('chat.tool.find.searchPath')}</span>
                      <code>{msg.callView.path}</code>
                    </div>
                    {#if msg.callView.maxDepth !== ''}
                      <div class="find-row">
                        <span>{$t('chat.tool.find.depth')}</span>
                        <code>{msg.callView.maxDepth}</code>
                      </div>
                    {/if}
                    {#if msg.callView.maxResults !== ''}
                      <div class="find-row">
                        <span>{$t('chat.tool.find.resultLimit')}</span>
                      <code>{msg.callView.maxResults}</code>
                    </div>
                  {/if}
                  </div>
                {:else if msg.callView?.kind === 'browser'}
                  <div class="browser-call">
                    <div class="find-row">
                      <span>{$t('chat.tool.browser.action')}</span>
                      <code>{msg.callView.action || $t('chat.tool.browser.missing')}</code>
                    </div>
                    {#if msg.callView.url}
                      <div class="find-row">
                        <span>{$t('chat.tool.browser.url')}</span>
                        <code>{msg.callView.url}</code>
                      </div>
                    {/if}
                    {#if msg.callView.selector}
                      <div class="find-row">
                        <span>{$t('chat.tool.browser.selectorLabel')}</span>
                        <code>{msg.callView.selector}</code>
                      </div>
                    {/if}
                    {#if msg.callView.value}
                      <div class="find-row">
                        <span>{$t('chat.tool.browser.value')}</span>
                        <code>{msg.callView.value}</code>
                      </div>
                    {/if}
                    {#if msg.callView.expression}
                      <div class="find-row">
                        <span>{$t('chat.tool.browser.expression')}</span>
                        <code>{msg.callView.expression}</code>
                      </div>
                    {/if}
                  </div>
                {:else if msg.callView?.kind === 'skill-ref'}
                  <div class="skill-ref-call">
                    <div class="find-row">
                      <span>{$t('chat.tool.skillRef.skillLabel')}</span>
                      <code>{msg.callView.skill || $t('chat.tool.skillRef.missing')}</code>
                    </div>
                    <div class="find-row">
                      <span>{$t('chat.tool.skillRef.refLabel')}</span>
                      <code>{msg.callView.ref || $t('chat.tool.skillRef.missing')}</code>
                    </div>
                  </div>
                {:else if msg.callView?.kind === 'workflow-lint'}
                  <div class="workflow-lint-call">
                    <div class="write-call-head">
                      <strong>{$t('chat.tool.workflowLint.source')}</strong>
                      <span>{$t('chat.tool.write.summary', { lines: msg.callView.lines, chars: msg.callView.chars })}</span>
                    </div>
                    <pre class:empty={msg.callView.source === ''}>{msg.callView.source || $t('chat.tool.workflowLint.missing')}</pre>
                  </div>
                {:else if msg.callView?.kind === 'subagent-task'}
                  <div class="subagent-call">
                    <span>{$t('chat.tool.subagent.task')}</span>
                    <p>{msg.callView.task || msg.callView.target}</p>
                  </div>
                {:else if msg.callView?.kind === 'subagent-handle'}
                  <div class="subagent-call compact">
                    <div class="find-row">
                      <span>{$t('chat.tool.subagent.handle')}</span>
                      <code>{msg.callView.handle || $t('chat.tool.subagent.handleMissing')}</code>
                    </div>
                    {#if msg.callView.message}
                      <div class="find-row">
                        <span>{$t('chat.tool.subagent.message')}</span>
                        <code>{msg.callView.message}</code>
                      </div>
                    {/if}
                  </div>
                {/if}
                {#if msg.callView?.kind !== 'generic' && msg.arguments}
                  <details class="tool-raw">
                    <summary>{$t('chat.argsJson')}</summary>
                    <pre>{formatArgs(msg.arguments)}</pre>
                  </details>
                {:else if msg.arguments}
                  <pre>{formatArgs(msg.arguments)}</pre>
                {:else if msg.invalidArguments}
                  <pre>{msg.invalidArguments}</pre>
                {/if}
              </div>
            </article>
          {:else if msg.role === 'toolResult'}
            <article class="msg tool-result" class:work-tool-hidden={runtimeDisplayMode === 'work' && !workToolsExpanded}>
              <details on:toggle={(event) => loadToolResultDetail(msg, event)}>
                <summary>
                  <span class="dot {msg.isError ? 'error' : 'done'}"></span>
                  <strong>{msg.toolName}</strong>
                  <span>{msg.isError ? $t('common.failed') : $t('common.completed')}</span>
                  <em>{msg.summary}</em>
                </summary>
                {#if msg.detailLoading}
                  <p class="pending-text">{$t('chat.loadingToolResult')}</p>
                {:else if msg.detailError}
                  <p class="error-text">{msg.detailError}</p>
                {:else if msg.detailLoaded}
                  {#if msg.detail?.kind === 'browser' && msg.detail.browserResult}
                    <div class="browser-result">
                      <div class="browser-result-head">
                        <strong>{msg.detail.browserResult.status}</strong>
                        {#if msg.detail.browserResult.title}<span>{msg.detail.browserResult.title}</span>{/if}
                      </div>
                      {#if msg.detail.browserResult.url}
                        <code>{msg.detail.browserResult.url}</code>
                      {/if}
                      {#if !msg.detail.browserResult.title && !msg.detail.browserResult.url && msg.detail.browserResult.content}
                        <pre>{msg.detail.browserResult.content}</pre>
                      {/if}
                    </div>
                  {:else if msg.detail?.kind === 'subagent' && msg.detail.subAgentResult}
                    <div class="subagent-result">
                      {#if msg.detail.subAgentResult.handle}
                        <div><span>{$t('chat.tool.subagent.handle')}</span><code>{msg.detail.subAgentResult.handle}</code></div>
                      {/if}
                      {#if msg.detail.subAgentResult.status}
                        <div><span>{$t('chat.tool.subagent.statusLabel')}</span><strong>{msg.detail.subAgentResult.status}</strong></div>
                      {/if}
                      {#if msg.detail.subAgentResult.duration}
                        <div><span>{$t('chat.tool.subagent.duration')}</span><code>{msg.detail.subAgentResult.duration}</code></div>
                      {/if}
                      {#if msg.detail.subAgentResult.tool_calls !== undefined}
                        <div><span>{$t('chat.tool.subagent.toolCalls')}</span><code>{msg.detail.subAgentResult.tool_calls}</code></div>
                      {/if}
                      {#if msg.detail.subAgentResult.error}
                        <p class="error-text">{msg.detail.subAgentResult.error}</p>
                      {/if}
                      {#if msg.detail.subAgentResult.result || msg.detail.subAgentResult.last_response || msg.detail.subAgentResult.partial_result}
                        <pre>{msg.detail.subAgentResult.result || msg.detail.subAgentResult.last_response || msg.detail.subAgentResult.partial_result}</pre>
                      {/if}
                    </div>
                  {:else if msg.detail?.kind === 'skill-ref' && msg.detail.content}
                    <div class="skill-ref-result">
                      <div class="markdown" use:codeBlockControls>{@html markdownToHTML(msg.detail.content)}</div>
                    </div>
                  {:else if msg.detail?.kind === 'workflow-lint' && msg.detail.workflowLintResult}
                    <div class="workflow-lint-result">
                      <div class="workflow-lint-head">
                        <strong class:failed={!msg.detail.workflowLintResult.valid}>
                          {msg.detail.workflowLintResult.valid ? $t('chat.tool.workflowLint.valid') : $t('chat.tool.workflowLint.invalid')}
                        </strong>
                        {#if msg.detail.workflowLintResult.status}
                          <span>{msg.detail.workflowLintResult.status}</span>
                        {/if}
                      </div>
                      {#if msg.detail.workflowLintResult.error}
                        <p class="error-text">{msg.detail.workflowLintResult.error}</p>
                      {/if}
                      {#if msg.detail.workflowLintResult.tasks.length}
                        <section>
                          <strong>{$t('chat.tool.workflowLint.tasks')}</strong>
                          <div class="workflow-chip-row">
                            {#each msg.detail.workflowLintResult.tasks as task}
                              <code>{task}</code>
                            {/each}
                          </div>
                        </section>
                      {/if}
                      {#if msg.detail.workflowLintResult.results.length}
                        <section>
                          <strong>{$t('chat.tool.workflowLint.results')}</strong>
                          <div class="workflow-chip-row">
                            {#each msg.detail.workflowLintResult.results as result}
                              <code>{result}</code>
                            {/each}
                          </div>
                        </section>
                      {/if}
                    </div>
                  {:else if msg.detail?.kind === 'bash' && msg.detail.bashResult}
                    <div class="bash-result">
                      <div class="bash-meta">
                        {#if msg.detail.bashResult.runtime}<span>{msg.detail.bashResult.runtime}</span>{/if}
                        {#if msg.detail.bashResult.cwd}<span>{msg.detail.bashResult.cwd}</span>{/if}
                        {#if msg.detail.bashResult.exitCode}
                          <strong class:failed={msg.detail.bashResult.exitCode !== '0'}>exit {msg.detail.bashResult.exitCode}</strong>
                        {/if}
                      </div>
                      {#if msg.detail.bashResult.prefix}
                        <p class="bash-note">{msg.detail.bashResult.prefix}</p>
                      {/if}
                      {#if msg.detail.bashResult.command}
                        <div class="bash-block">
                          <span>command</span>
                          <pre>{msg.detail.bashResult.command}</pre>
                        </div>
                      {/if}
                      {#if msg.detail.bashResult.stdout}
                        <div class="bash-block">
                          <span>stdout</span>
                          <pre class:empty={msg.detail.bashResult.stdout === '(no output)'}>{msg.detail.bashResult.stdout}</pre>
                        </div>
                      {/if}
                      {#if msg.detail.bashResult.stderr}
                        <div class="bash-block">
                          <span>stderr</span>
                          <pre class:empty={msg.detail.bashResult.stderr === '(no output)'}>{msg.detail.bashResult.stderr}</pre>
                        </div>
                      {/if}
                      {#if msg.detail.bashResult.note}
                        <p class="bash-note">{msg.detail.bashResult.note}</p>
                      {/if}
                    </div>
                  {:else if msg.detail?.kind === 'read' && msg.detail.readLines?.length}
                    <div class="read-result">
                      {#each msg.detail.readLines as line}
                        <div class="code-line">
                          <span>{line.number}</span>
                          <code>{line.text}</code>
                        </div>
                      {/each}
                    </div>
                  {:else if msg.detail?.kind === 'ls' && msg.detail.lsEntries?.length}
                    <div class="ls-result">
                      {#each msg.detail.lsEntries as entry}
                        <div class="ls-entry {entry.type}">
                          <span>{entry.type === 'dir' ? 'dir' : 'file'}</span>
                          <strong>{entry.name}</strong>
                          {#if entry.size}<em>{entry.size}</em>{/if}
                        </div>
                      {/each}
                    </div>
                  {:else if msg.detail?.kind === 'grep' && msg.detail.grepMatches?.matches?.length}
                    <div class="grep-result">
                      {#each msg.detail.grepMatches.matches as match}
                        <div class="grep-match">
                          <div><strong>{match.path}</strong><span>:{match.line}</span></div>
                          <code>{match.text}</code>
                        </div>
                      {/each}
                      {#if msg.detail.grepMatches.note}
                        <p>{msg.detail.grepMatches.note}</p>
                      {/if}
                    </div>
                  {:else if msg.detail?.content}
                    <pre>{msg.detail.content}</pre>
                  {/if}
                  {#if msg.detail?.images?.length}
                    <div class="msg-images">
                      {#each msg.detail.images as image, imageIndex}
                        <button type="button" class="msg-image-button" aria-label={image.name || $t('chat.imagePreview')} on:click={() => openLightbox(msg.detail.images.map((img) => ({ src: img.dataUrl, name: img.name })), imageIndex)}>
                          <img src={image.dataUrl} alt={image.name} on:load={() => scrollChatToBottom()} />
                        </button>
                      {/each}
                    </div>
                  {/if}
                {/if}
              </details>
            </article>
          {/if}
        {/each}
        {#if persistentRunError && persistentRunErrorMessage}
          <article class="msg assistant error run-error">
            <div class="meta">
              <strong>MothX</strong>
              <span>{$t('common.failed')}</span>
            </div>
            <p>{persistentRunErrorMessage}</p>
            {#if persistentRunError.retryMode === 'reconcile' || canRetryError(persistentRunError) || requiresRetryConfirmation(persistentRunError)}
              <div class="msg-retry">
                {#if persistentRunError.retryMode === 'reconcile'}
                  <Button type="button" variant="ghost" size="sm" disabled={busy || retrySubmitting} onclick={() => reloadRunStatus(persistentRunError)}>
                    <RefreshCw size={14} />
                    <span>{$t('chat.run.reloadStatus')}</span>
                  </Button>
                {/if}
                {#if canRetryError(persistentRunError)}
                  <Button type="button" variant="ghost" size="sm" disabled={busy || retrySubmitting} onclick={() => retryRun({ runId: persistentRunError.runId, error: persistentRunError })}>
                    <RotateCcw size={14} />
                    <span>{$t('chat.retry')}</span>
                  </Button>
                {/if}
                {#if requiresRetryConfirmation(persistentRunError)}
                  <Button type="button" variant="destructive" size="sm" disabled={busy || retrySubmitting} onclick={() => retryRun({ runId: persistentRunError.runId, error: persistentRunError }, true)}>
                    <AlertCircle size={14} />
                    <span>{$t('chat.retry.confirm')}</span>
                  </Button>
                {/if}
              </div>
            {/if}
          </article>
        {/if}
        {#if hostedItems.length}
          <div class="hosted-items" aria-label={$t('chat.hostedActivity')} aria-live="polite">
            {#each hostedItems as item}
              <span class="hosted-item-status">
                <span>{item.type || 'hosted'}</span>
                <span class="hosted-item-state">{item.status || 'updated'}</span>
              </span>
            {/each}
          </div>
        {/if}
        {#if runtimeDisplayMode === 'work' && (busy || toolMessageCount > 0)}
          <details class="work-tool-group" class:active={busy} class:running={busy} open={workToolsExpanded} on:toggle={(event) => (workToolsExpanded = event.currentTarget.open)}>
            <summary>
              <span class="work-tool-progress" aria-hidden="true"><span></span></span>
              <strong>{workToolNames.join(' · ') || $t('chat.runtime.workTools')}</strong>
            </summary>
          </details>
        {/if}
        {#if sessionEventSummary.visible}
          <aside class="session-event-strip" title={sessionEventTooltip(sessionEventSummary)}>
            <span class="dot {sessionRunStateClass(sessionEventSummary.lastRun)}"></span>
            <strong>{sessionRunLabel(sessionEventSummary.lastRun)}</strong>
            {#if isCronRun(sessionEventSummary.lastRun)}
              <span class="event-kind cron">{$t('chat.sessionEvents.cron')}</span>
              {#if cronRunName(sessionEventSummary.lastRun)}<span>{cronRunName(sessionEventSummary.lastRun)}</span>{/if}
            {/if}
            {#if sessionEventSummary.workDir}<span class="path">{compactPath(sessionEventSummary.workDir)}</span>{/if}
            {#if sessionEventSummary.model}<span>{sessionEventSummary.model}</span>{/if}
            <span class="metric">{$t('chat.sessionEvents.tokens', { tokens: formatCompactTokens(sessionEventSummary.totalTokens) })}</span>
            {#if sessionEventSummary.contextUsage}
              <span class="metric context-usage">{$t('chat.sessionEvents.context', { rate: formatContextUsageRate(sessionEventSummary.contextUsage) })}</span>
            {/if}
            <span class="metric">{$t('chat.sessionEvents.cache', { rate: formatCacheRate(sessionEventSummary) })}</span>
            {#if sessionEventSummary.capabilityCount > 0}
              <span>{$t('chat.sessionEvents.capabilities', { count: sessionEventSummary.capabilityCount })}</span>
            {/if}
          </aside>
        {/if}
      </div>
    {/if}
  </div>
  {/if}

  <div class="composer">
    <div class="composer-card">
      <div class="composer-row">
      {#if pendingAttachments.length > 0}
        <div class="composer-attachments">
          {#each pendingAttachments as attachment, idx}
            <div class="composer-attachment" class:image={attachment.kind === 'image'}>
              {#if attachment.kind === 'image' && attachment.dataUrl}
                <img src={attachment.dataUrl} alt={attachment.filename || attachment.name} />
              {:else}
                <span class="file-icon">📄</span>
              {/if}
              <div class="attachment-meta">
                <span title={attachment.filename || attachment.name}>{attachment.filename || attachment.name}</span>
                <em>{attachment.mediaType}</em>
                <em>{formatFileSize(attachment.size)}</em>
              </div>
              <button type="button" aria-label={$t('chat.removeAttachment')} on:click={() => removeAttachment(idx)}>×</button>
            </div>
          {/each}
        </div>
      {/if}
      <textarea
        bind:this={composerTextarea}
        bind:value={prompt}
        on:input={handlePromptInput}
        on:keydown={handleKeydown}
        placeholder={!apiEnabled ? $t('chat.apiDisabled') : busy ? $t('chat.runningPlaceholder') : (isNewSession && !workDir.trim()) ? $t('chat.error.needWorkDir') : $t('chat.messagePlaceholder')}
        disabled={!apiEnabled || busy}
        rows="1"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={commandSuggestions.length > 0 && commandSuggestionDismissedFor !== prompt}
        aria-controls={`${paneId}-command-suggestions`}
        aria-activedescendant={commandSuggestions.length > 0 && commandSuggestionDismissedFor !== prompt ? `${paneId}-command-suggestion-${commandSuggestionIndex}` : undefined}
      ></textarea>
      {#if commandSuggestions.length > 0 && commandSuggestionDismissedFor !== prompt}
        <div id={`${paneId}-command-suggestions`} class="command-suggestions" role="listbox" aria-label={$t('chat.command.suggestions')}>
          {#each commandSuggestions as item, index}
            <button
              id={`${paneId}-command-suggestion-${index}`}
              type="button"
              role="option"
              class:active={index === commandSuggestionIndex}
              aria-selected={index === commandSuggestionIndex}
              on:mousedown={(event) => event.preventDefault()}
              on:click={() => acceptCommandSuggestion(item)}
            >
              <code>{item.value}</code>
              <span>{item.description}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
      {#if showComposerConfig}
        <div id={`${paneId}-composer-config`} class="composer-controls composer-config-panel">
          <div class="composer-config-modes">
            <span class="composer-config-label">{$t('chat.runtime.agentMode')}</span>
            <div class="composer-mode-group" role="group" aria-label={$t('chat.runtime.agentMode')}>
              {#each ['plan', 'agent', 'yolo', 'os'] as mode}
                <button type="button" class:active={runtimeMode === mode} disabled={runtimeUpdating || busy} on:click={() => runtimeManager.setMode(mode)}>{mode}</button>
              {/each}
            </div>
            <span class="composer-config-label">{$t('chat.runtime.displayMode')}</span>
            <div class="composer-display-group" role="group" aria-label={$t('chat.runtime.displayMode')}>
              <button type="button" class:active={runtimeDisplayMode === 'work'} disabled={runtimeUpdating || busy} on:click={() => { workToolsExpanded = false; runtimeDisplayMode = 'work'; runtimeManager.setDisplayMode('work'); }}>{$t('chat.runtime.work')}</button>
              <button type="button" class:active={runtimeDisplayMode === 'code'} disabled={runtimeUpdating || busy} on:click={() => { runtimeDisplayMode = 'code'; runtimeManager.setDisplayMode('code'); }}>{$t('chat.runtime.code')}</button>
            </div>
            {#if pendingApprovalCount}
              <button type="button" class="composer-approval-link" on:click={() => (showApprovalCenter = true)}>{$t('chat.runtime.pendingApproval', { count: pendingApprovalCount })}</button>
            {/if}
          </div>
          <div class="composer-config-selects">
            <SearchSelect
              value={providerID}
              options={providerOptions}
              placeholder={$t('chat.selectProvider')}
              ariaLabel={$t('chat.selectProvider')}
              disabled={!apiEnabled || providerOptions.length === 0}
              className="provider-search-select"
              menuClassName="provider-search-select-menu"
              on:change={(event) => handleProviderChange(event.detail)}
            />
            <ModelPicker
              value={selectedModelID}
              options={providerModels}
              placeholder={$t('chat.selectModel')}
              ariaLabel={$t('chat.selectModel')}
              disabled={!apiEnabled || providerModels.length === 0}
              on:change={(event) => selectModel(event.detail)}
            />
            <SearchSelect
              value={selectedTeamID}
              options={teamExpertOptions}
              placeholder={$t('chat.expertTeam.placeholder')}
              ariaLabel={$t('chat.expertTeam.label')}
              noOptionsLabel={$t('chat.expertTeam.empty')}
              className="expert-team-search-select"
              menuClassName="expert-team-search-select-menu"
              disabled={!apiEnabled || busy || teamLoading || teamApplying}
              on:change={(event) => selectTeamExpert(event.detail)}
            />
            {#if !isNewSession && teamSelectionChanged}
              <Button
                type="button"
                variant={teamMustFork ? 'outline' : 'default'}
                size="sm"
                disabled={!teamCanApply && !teamMustFork}
                onclick={applyTeamSelection}
              >
                {teamMustFork ? $t('chat.expertTeam.switchAction') : $t('chat.expertTeam.apply')}
              </Button>
            {/if}
          </div>
        </div>
      {/if}
    <div class="composer-bar">
      <div class="left">
        <input
          bind:this={attachmentInput}
          class="file-input"
          type="file"
          multiple
          on:change={handleFileSelect}
        />
        <button
          type="button"
          class="icon-btn"
          disabled={!apiEnabled || busy}
          title={$t('chat.uploadAttachment')}
          aria-label={$t('chat.uploadAttachment')}
          on:click={() => attachmentInput?.click()}
        >
          📎
        </button>
        <button
          type="button"
          class="composer-config-toggle"
          class:open={showComposerConfig}
          aria-expanded={showComposerConfig}
          aria-controls={showComposerConfig ? `${paneId}-composer-config` : undefined}
          title={$t('chat.composerConfig') || 'Model and expert settings'}
          on:click={() => (showComposerConfig = !showComposerConfig)}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="2"/><path d="M9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4"/></svg>
          <span class="composer-config-model">{activeModel?.name || selectedModelID || $t('chat.selectModel')}</span>
          <span class="composer-config-mode">{runtimeMode}</span>
          <span class="runtime-chevron" aria-hidden="true">⌄</span>
        </button>
        <div bind:this={skillPicker} class="skill-picker" aria-label={$t('chat.skills.active')}>
          <button type="button" class="skill-picker-toggle" disabled={!apiEnabled || busy} on:click={() => (showSkillPicker = !showSkillPicker)} aria-expanded={showSkillPicker}>
            <span>{$t('chat.skills.active')}</span>
            <strong>{activeSkills.length ? $t('chat.skills.activeCount', { count: activeSkills.length }) : $t('chat.skills.noneActive')}</strong>
            <span class="runtime-chevron">⌄</span>
          </button>
          {#if showSkillPicker}
            <div class="skill-picker-menu">
              <header><strong>{$t('chat.skills.project')}</strong><span>{$t('chat.skills.activeCount', { count: activeSkills.length })} · {$t('chat.skills.pending', { count: availableSkills.length - activeSkills.length })}</span></header>
              {#if availableSkills.length === 0}
                <p class="skill-picker-empty">{$t('chat.skills.none')}</p>
              {:else}
                {#each availableSkills as skill}
                  <label class:active={activeSkills.includes(skill.name)}>
                    <input type="checkbox" checked={activeSkills.includes(skill.name)} disabled={busy} on:change={(event) => toggleSkill(skill.name, event)} />
                    <span class="skill-name">{skill.name}</span>
                    <em>{activeSkills.includes(skill.name) ? $t('common.active') : $t('common.pending')}</em>
                  </label>
                {/each}
              {/if}
            </div>
          {/if}
        </div>
        <div bind:this={toolMenuBtn} class="tool-menu" aria-label={$t('chat.tools')}>
          <button
            type="button"
            class="tool-menu-toggle"
            class:open={showToolMenu}
            disabled={!apiEnabled || busy}
            on:click={() => (showToolMenu = !showToolMenu)}
            aria-expanded={showToolMenu}
          >
            <span class="tool-menu-label">{$t('chat.tools')}</span>
            <strong>{activeToolCount}</strong>
            <span class="runtime-chevron">⌄</span>
          </button>
          {#if showToolMenu}
            <div class="tool-menu-popover">
              <header><strong>{$t('chat.tools')}</strong><span>{$t('chat.toolHint')}</span></header>
              {#each availableToolToggles as item}
                <label class="tool-menu-item" class:active={sessionTools[item.key]} title={$t(`chat.toolToggle.${item.key}`)}>
                  <input
                    type="checkbox"
                    checked={sessionTools[item.key]}
                    disabled={!apiEnabled || busy}
                    on:change={(event) => { updateToolOption(item.key, event); }}
                  />
                  <span class="tool-item-name">{item.label}</span>
                  <em>{sessionTools[item.key] ? 'on' : 'off'}</em>
                </label>
              {/each}
            </div>
          {/if}
        </div>
        {#if sessionID}
          <button
            type="button"
            class="tool-menu-toggle mcp-config-toggle"
            disabled={!apiEnabled || busy}
            on:click={() => (showMCPConfig = true)}
          >
            <span class="tool-menu-label">{$t('chat.mcp.short')}</span>
          </button>
        {/if}
        {#if isNewSession}
          <button
            type="button"
            class="workdir-pill"
            class:has-dir={Boolean(workDir.trim())}
            disabled={!apiEnabled || busy}
            on:click={chooseWorkDir}
            title={workDir || $t('chat.selectWorkDir')}
          >
            <span class="workdir-icon">📁</span>
            <span class="workdir-text">{workDir ? compactWorkDir(workDir) : $t('chat.selectWorkDir')}</span>
          </button>
        {/if}
      </div>
      <div class="right">
        {#if busy && canStop}
          <button type="button" class="stop-btn" disabled={stopSubmitting} on:click={stop} title={stopSubmitting ? $t('common.stopping') : $t('common.stop')} aria-label={stopSubmitting ? $t('common.stopping') : $t('common.stop')}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>
          </button>
        {/if}
        <button
          type="button"
          class="send-btn"
          disabled={busy || (!prompt.trim() && pendingAttachments.length === 0) || !apiEnabled || (isNewSession && !workDir.trim())}
          on:click={sendPrompt}
          title={busy ? $t('chat.sending') : $t('chat.send')}
          aria-label={busy ? $t('chat.sending') : $t('chat.send')}
        >
          {#if busy}
            <span class="spinner sm"></span>
          {:else}
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="19" x2="12" y2="5"/><polyline points="5 12 12 5 19 12"/></svg>
          {/if}
        </button>
      </div>
    </div>
  </div>
</section>

<DirBrowser
  bind:open={dirBrowserOpen}
  initialPath={workDir.trim()}
  on:select={(e) => { if (e.detail?.path) workDir = e.detail.path; }}
/>

<svelte:window on:keydown={handleLightboxKeydown} />

{#if lightbox}
  <div
    class="lightbox-overlay"
    role="dialog"
    aria-modal="true"
    aria-label={$t('chat.imagePreview')}
    tabindex="0"
    on:click={(event) => event.target === event.currentTarget && closeLightbox()}
    on:keydown={(event) => event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ') && closeLightbox()}
  >
    <div class="lightbox-toolbar">
      <span class="lightbox-caption" title={lightbox.urls[lightbox.index].name}>{lightbox.urls[lightbox.index].name || $t('chat.imagePreview')}</span>
      {#if lightbox.urls.length > 1}<span class="lightbox-count">{lightbox.index + 1} / {lightbox.urls.length}</span>{/if}
      <a class="lightbox-open" href={lightbox.urls[lightbox.index].src} target="_blank" rel="noreferrer">{$t('chat.imageOpen')}</a>
      <button type="button" class="lightbox-close" aria-label={$t('chat.closePreview')} on:click={closeLightbox}>×</button>
    </div>
    {#if lightbox.urls.length > 1}
      <button type="button" class="lightbox-nav lightbox-prev" aria-label={$t('chat.prevImage')} on:click={() => stepLightbox(-1)}>‹</button>
      <button type="button" class="lightbox-nav lightbox-next" aria-label={$t('chat.nextImage')} on:click={() => stepLightbox(1)}>›</button>
    {/if}
    <img class="lightbox-image" src={lightbox.urls[lightbox.index].src} alt={lightbox.urls[lightbox.index].name || $t('chat.imagePreview')} />
    <p class="lightbox-hint">{$t('chat.imageHint')}</p>
  </div>
{/if}


{#if showApprovalCenter}
  <div class="subagent-overlay" role="dialog" aria-modal="true" aria-label={$t('chat.approval.center')}>
    <div class="subagent-modal approval-center">
      <header>
        <div>
          <strong>{$t('chat.approval.center')}</strong>
          <span>{$t('chat.approval.pending', { pending: pendingApprovalCount, recorded: approvalHistory.length })}</span>
        </div>
        <button type="button" class="ghost sm" on:click={() => { dismissedApprovalIDs = selectedApprovalID ? [...dismissedApprovalIDs, selectedApprovalID] : dismissedApprovalIDs; showApprovalCenter = false; }}>{$t('chat.approval.close')}</button>
      </header>
      <div class="approval-list" aria-live="polite">
        {#if selectedApproval}
          <article class="approval-card" aria-labelledby="approval-title-{selectedApproval.approvalId}">
            <div class="approval-card-head">
              <div class="approval-title-group">
                <div class="approval-kicker"><span class="approval-risk {selectedApproval.risk || 'medium'}">{selectedApproval.risk || 'medium'} risk</span><span>{selectedApproval.mode || runtimeMode} mode</span></div>
                <strong id="approval-title-{selectedApproval.approvalId}">{selectedApproval.summary || selectedApproval.tool?.name}</strong>
                <p>{selectedApproval.reason || $t('chat.approval.reason')}</p>
              </div>
              {#if pendingApprovalCount > 1}
                <label class="approval-picker">{$t('chat.approval.request')}
                  <select aria-label={$t('chat.approval.select')} value={selectedApprovalID} on:change={(event) => { selectedApprovalID = event.currentTarget.value; activeApproval.set((sessionRuntimeValue?.pendingApprovals || []).find((approval) => approval.approvalId === selectedApprovalID) || null); }}>
                    {#each sessionRuntimeValue?.pendingApprovals || [] as approval}<option value={approval.approvalId}>{approval.summary || approval.tool?.name}</option>{/each}
                  </select>
                </label>
              {/if}
            </div>
            {#if selectedApproval.tool?.name === 'bash'}
              <div class="approval-bash tool-call-body embedded">
                <div class="tool-title">
                  <span class="dot running"></span>
                  <strong>Bash</strong>
                  {#if approvalBashWorkDir(selectedApproval)}<span class="tool-target">{approvalBashWorkDir(selectedApproval)}</span>{/if}
                </div>
                <div class="bash-block">
                  <span>command</span>
                  <pre>{approvalBashCommand(selectedApproval)}</pre>
                </div>
              </div>
            {:else}
              <div class="approval-tool tool-call-body embedded">
                <div class="tool-title">
                  <span class="dot running"></span>
                  <strong>{approvalToolViewValue.label || selectedApproval.tool?.label || selectedApproval.tool?.name}</strong>
                  {#if approvalToolViewValue.target}<span class="tool-target">{approvalToolViewValue.target}</span>{/if}
                </div>
                {#if approvalToolViewValue.details?.length}
                  <div class="tool-call-tags">
                    {#each approvalToolViewValue.details as detail}<span>{detail}</span>{/each}
                  </div>
                {/if}
                {#if approvalToolViewValue.kind === 'edit' && approvalToolViewValue.edits?.length}
                  <div class="edit-call">
                    {#each approvalToolViewValue.edits as edit}
                      <section class="edit-block">
                        <div class="edit-block-head"><strong>{$t('chat.tool.edit.editNumber', { number: edit.index })}</strong><span>{$t('chat.tool.edit.lineChange', { old: edit.oldLines, next: edit.newLines })}</span></div>
                        <div class="edit-columns"><div class="edit-pane old"><span>{$t('chat.tool.edit.oldText')}</span><pre class:empty={edit.oldText === ''}><code>{@html edit.oldText ? highlightedCodeToHTML(edit.oldText, approvalToolViewValue.target) : $t('chat.tool.edit.empty')}</code></pre></div><div class="edit-pane new"><span>{$t('chat.tool.edit.newText')}</span><pre class:empty={edit.newText === ''}><code>{@html edit.newText ? highlightedCodeToHTML(edit.newText, approvalToolViewValue.target) : $t('chat.tool.edit.empty')}</code></pre></div></div>
                      </section>
                    {/each}
                  </div>
                {:else if approvalToolViewValue.kind === 'write'}
                  <div class="write-call"><div class="write-call-head"><strong>{$t('chat.tool.write.preview')}</strong><span>{$t('chat.tool.write.summary', { lines: approvalToolViewValue.lines, chars: approvalToolViewValue.chars })}</span></div><span>{$t('chat.tool.write.content')}</span><pre class:empty={approvalToolViewValue.content === ''}>{approvalToolViewValue.content || $t('chat.tool.edit.empty')}</pre></div>
                {:else if approvalToolViewValue.kind === 'insert'}
                  <div class="write-call"><div class="write-call-head"><strong>{$t('chat.tool.insert.preview')}</strong><span>{$t('chat.tool.insert.summary', { lines: approvalToolViewValue.lines, chars: approvalToolViewValue.chars })}</span></div><span>{$t('chat.tool.insert.content')}</span><pre class:empty={approvalToolViewValue.content === ''}>{approvalToolViewValue.content || $t('chat.tool.edit.empty')}</pre></div>
                {:else if approvalToolViewValue.kind === 'find'}
                  <div class="find-call"><div class="find-row"><span>{$t('chat.tool.find.pattern')}</span><code>{approvalToolViewValue.pattern || $t('chat.tool.find.missing')}</code></div><div class="find-row"><span>{$t('chat.tool.find.searchPath')}</span><code>{approvalToolViewValue.path}</code></div></div>
                {:else if approvalToolViewValue.kind === 'browser'}
                  <div class="browser-call"><div class="find-row"><span>{$t('chat.tool.browser.action')}</span><code>{approvalToolViewValue.action || $t('chat.tool.browser.missing')}</code></div>{#if approvalToolViewValue.url}<div class="find-row"><span>{$t('chat.tool.browser.url')}</span><code>{approvalToolViewValue.url}</code></div>{/if}{#if approvalToolViewValue.selector}<div class="find-row"><span>{$t('chat.tool.browser.selectorLabel')}</span><code>{approvalToolViewValue.selector}</code></div>{/if}</div>
                {:else if approvalToolViewValue.kind === 'skill-ref'}
                  <div class="skill-ref-call"><div class="find-row"><span>{$t('chat.tool.skillRef.skillLabel')}</span><code>{approvalToolViewValue.skill || $t('chat.tool.skillRef.missing')}</code></div><div class="find-row"><span>{$t('chat.tool.skillRef.refLabel')}</span><code>{approvalToolViewValue.ref || $t('chat.tool.skillRef.missing')}</code></div></div>
                {:else}
                  <div class="approval-tool-summary"><span>{selectedApproval.tool?.details?.path || selectedApproval.context?.workDir || 'This action requires permission.'}</span></div>
                {/if}
              </div>
            {/if}
            <div class="approval-actions">
              <button class="primary" disabled={approvalSubmitting} on:click={() => respondApproval(selectedApproval, 'approve_once')}>{$t('chat.approval.approveOnce')}</button>
              <button class="ghost approval-deny" disabled={approvalSubmitting} on:click={() => respondApproval(selectedApproval, 'deny_once')}>{$t('chat.approval.deny')}</button>
              {#if selectedApproval.actions?.includes('remember_command')}<span class="approval-action-divider"></span><button class="ghost sm" disabled={approvalSubmitting} on:click={() => respondApproval(selectedApproval, 'remember_command')}>{$t('chat.approval.alwaysAllowCommand')}</button><button class="ghost sm" disabled={approvalSubmitting} on:click={() => respondApproval(selectedApproval, 'remember_prefix')}>{$t('chat.approval.alwaysAllowPrefix')}</button>{/if}
              {#if selectedApproval.actions?.includes('allow_edit_path')}<button class="ghost sm" disabled={approvalSubmitting} on:click={() => respondApproval(selectedApproval, 'allow_edit_path')}>{$t('chat.approval.allowPath')}</button>{/if}
            </div>
            <details class="approval-raw"><summary>{$t('chat.approval.requestJson')}</summary><pre>{JSON.stringify(selectedApproval, null, 2)}</pre></details>
          </article>
        {:else}
          <div class="approval-empty"><strong>{$t('chat.approval.none')}</strong><span>{$t('chat.approval.noneHint')}</span></div>
        {/if}
        {#if approvalHistory.length}
          <section class="approval-history" aria-label={$t('chat.approval.history')}>
            <div class="approval-history-head"><h4>{$t('chat.approval.auditHistory')}</h4><span>{$t('chat.approval.decisions', { count: approvalHistory.length })}</span></div>
            <div class="approval-history-list">
              {#each approvalHistory as item}
                <article class="approval-history-item">
                  <strong>{item.action === 'deny_once' ? $t('chat.approval.denied') : $t('chat.approval.approved')}</strong>
                  <span>{item.message || item.action}</span>
                </article>
              {/each}
            </div>
          </section>
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if active && selectedQuestion}
  <div class="subagent-overlay question-overlay" role="dialog" aria-modal="true" aria-label={$t('chat.question.title')}>
    <div class="subagent-modal approval-center">
      <header>
        <div>
          <strong>{$t('chat.question.title')}</strong>
          <span>{$t('chat.question.pending', { count: pendingQuestionCount })}</span>
        </div>
      </header>
      <div class="approval-list" aria-live="polite">
        <article class="approval-card">
          <div class="approval-card-head">
            <div class="approval-title-group">
              <strong>{selectedQuestion.question}</strong>
              {#if selectedQuestion.context}<p>{selectedQuestion.context}</p>{/if}
            </div>
          </div>
          <div class="approval-actions">
            {#each selectedQuestion.options || [] as option}
              <button class="primary" disabled={questionSubmitting} on:click={() => respondQuestion(selectedQuestion, option)}>{option}</button>
            {/each}
          </div>
        </article>
      </div>
    </div>
  </div>
{/if}

{#if showMCPConfig && sessionID}
  <div class="mcp-session-overlay" role="dialog" aria-modal="true" aria-label={$t('chat.mcp.sessionTitle')}>
    <div class="mcp-session-dialog">
      <div class="mcp-session-head">
        <div><strong>{$t('chat.mcp.sessionTitle')}</strong><span>{$t('chat.mcp.sessionHint')}</span></div>
        <Button type="button" variant="ghost" size="icon" onclick={() => (showMCPConfig = false)} title={$t('common.close')} aria-label={$t('common.close')}>
          <X size={16} />
        </Button>
      </div>
      {#key sessionID}
        <MCPConfigEditor
          endpoint={`/api/sessions/${encodeURIComponent(sessionID)}/mcp`}
          title={$t('chat.mcp.projectTitle')}
          hint={$t('chat.mcp.projectHint', { workDir: activeSession?.workDir || '' })}
        />
      {/key}
    </div>
  </div>
{/if}

{#if showSubAgentModal}
  <div class="subagent-overlay" role="dialog" aria-modal="true" aria-label={$t('chat.subagents.history')}>
    <div class="subagent-modal">
      <header>
        <div>
          <strong>{$t('chat.subagents.history')}</strong>
          <span>{$t('chat.subagents.subtitle', { count: subAgents.length })}</span>
        </div>
        <button type="button" class="ghost sm" on:click={closeSubAgentModal}>{$t('common.close')}</button>
      </header>
      <div class="subagent-modal-body">
        <aside class="subagent-list">
          {#each subAgents as agent}
            <button
              type="button"
              class:active={agent.id === selectedSubAgentID}
              on:click={() => selectSubAgent(agent.id)}
            >
              <span class="dot {subAgentStateClass(agent)}"></span>
              <strong>{shortID(agent.id)}</strong>
              <em>{subAgentStatusLabel(agent.status)}</em>
              {#if agent.messageCount}<small>{agent.messageCount}</small>{/if}
            </button>
          {/each}
        </aside>
        <section class="subagent-history" bind:this={subAgentHistory}>
          {#if selectedSubAgent?.error}
            <div class="subagent-error" role="status">
              <strong>{$t('chat.subagents.error')}</strong>
              <p>{selectedSubAgent.error}</p>
            </div>
          {/if}
          {#if subAgentModalLoading}
            <p class="pending-text">{$t('chat.subagents.loading')}</p>
          {:else if subAgentModalError}
            <div class="error-text">
              <p>{subAgentModalError}</p>
              <button type="button" class="ghost sm" on:click={() => selectedSubAgentID ? loadSubAgentMessages(selectedSubAgentID) : loadSubAgents(sessionID)}>{$t('chat.subagents.retryLoad')}</button>
            </div>
          {:else if subAgentModalMessages.length === 0}
            <p class="pending-text">{$t('chat.subagents.empty')}</p>
          {:else}
            {#each subAgentModalMessages as item}
              <article class="subagent-msg {item.role}">
                <div class="meta">
                  <strong>{item.role === 'assistant' ? 'assistant' : item.role}</strong>
                  {#if item.toolName}<span>{item.toolName}</span>{/if}
                </div>
                {#if item.role === 'assistant'}
                  <div class="markdown" use:codeBlockControls>{@html markdownToHTML(item.content || '')}</div>
                {:else if item.role === 'user'}
                  <p>{item.content}</p>
                {:else if item.role === 'toolCall'}
                  <div class="tool-call-body embedded">
                    <div class="tool-title">
                      <span class="dot running"></span>
                      <strong>{item.callView?.label || item.toolName}</strong>
                      {#if item.callView?.target}<span class="tool-target">{item.callView.target}</span>{/if}
                    </div>
                    {#if item.callView?.details?.length}
                      <div class="tool-call-tags">
                        {#each item.callView.details as detail}
                          <span>{detail}</span>
                        {/each}
                      </div>
                    {/if}
                    {#if item.callView?.kind === 'edit' && item.callView.edits?.length}
                      <div class="edit-call">
                        {#each item.callView.edits as edit}
                          <section class="edit-block">
                            <div class="edit-block-head">
                              <strong>{$t('chat.tool.edit.editNumber', { number: edit.index })}</strong>
                              <span>{$t('chat.tool.edit.lineChange', { old: edit.oldLines, next: edit.newLines })}</span>
                            </div>
                            <div class="edit-columns">
                              <div class="edit-pane old">
                                <span>{$t('chat.tool.edit.oldText')}</span>
                                <pre class:empty={edit.oldText === ''}><code>{@html edit.oldText ? highlightedCodeToHTML(edit.oldText, item.callView.target) : $t('chat.tool.edit.empty')}</code></pre>
                              </div>
                              <div class="edit-pane new">
                                <span>{$t('chat.tool.edit.newText')}</span>
                                <pre class:empty={edit.newText === ''}><code>{@html edit.newText ? highlightedCodeToHTML(edit.newText, item.callView.target) : $t('chat.tool.edit.empty')}</code></pre>
                              </div>
                            </div>
                          </section>
                        {/each}
                      </div>
                    {:else if item.callView?.kind === 'write'}
                      <div class="write-call">
                        <div class="write-call-head">
                          <strong>{$t('chat.tool.write.preview')}</strong>
                          <span>{$t('chat.tool.write.summary', { lines: item.callView.lines, chars: item.callView.chars })}</span>
                        </div>
                        <span>{$t('chat.tool.write.content')}</span>
                        <pre class:empty={item.callView.content === ''}>{item.callView.content || $t('chat.tool.edit.empty')}</pre>
                      </div>
                    {:else if item.callView?.kind === 'insert'}
                      <div class="write-call">
                        <div class="write-call-head">
                          <strong>{$t('chat.tool.insert.preview')}</strong>
                          <span>{$t('chat.tool.insert.summary', { lines: item.callView.lines, chars: item.callView.chars })}</span>
                        </div>
                        <span>{$t('chat.tool.insert.content')}</span>
                        <pre class:empty={item.callView.content === ''}>{item.callView.content || $t('chat.tool.edit.empty')}</pre>
                      </div>
                    {:else if item.callView?.kind === 'find'}
                      <div class="find-call">
                        <div class="find-row">
                          <span>{$t('chat.tool.find.pattern')}</span>
                          <code>{item.callView.pattern || $t('chat.tool.find.missing')}</code>
                        </div>
                        <div class="find-row">
                          <span>{$t('chat.tool.find.searchPath')}</span>
                          <code>{item.callView.path}</code>
                        </div>
                        {#if item.callView.maxDepth !== ''}
                          <div class="find-row">
                            <span>{$t('chat.tool.find.depth')}</span>
                            <code>{item.callView.maxDepth}</code>
                          </div>
                        {/if}
                        {#if item.callView.maxResults !== ''}
                          <div class="find-row">
                            <span>{$t('chat.tool.find.resultLimit')}</span>
                            <code>{item.callView.maxResults}</code>
                          </div>
                        {/if}
                      </div>
                    {:else if item.callView?.kind === 'browser'}
                      <div class="browser-call">
                        <div class="find-row">
                          <span>{$t('chat.tool.browser.action')}</span>
                          <code>{item.callView.action || $t('chat.tool.browser.missing')}</code>
                        </div>
                        {#if item.callView.url}
                          <div class="find-row">
                            <span>{$t('chat.tool.browser.url')}</span>
                            <code>{item.callView.url}</code>
                          </div>
                        {/if}
                        {#if item.callView.selector}
                          <div class="find-row">
                            <span>{$t('chat.tool.browser.selectorLabel')}</span>
                            <code>{item.callView.selector}</code>
                          </div>
                        {/if}
                      </div>
                    {:else if item.callView?.kind === 'skill-ref'}
                      <div class="skill-ref-call">
                        <div class="find-row">
                          <span>{$t('chat.tool.skillRef.skillLabel')}</span>
                          <code>{item.callView.skill || $t('chat.tool.skillRef.missing')}</code>
                        </div>
                        <div class="find-row">
                          <span>{$t('chat.tool.skillRef.refLabel')}</span>
                          <code>{item.callView.ref || $t('chat.tool.skillRef.missing')}</code>
                        </div>
                      </div>
                    {:else if item.callView?.kind === 'workflow-lint'}
                      <div class="workflow-lint-call">
                        <div class="write-call-head">
                          <strong>{$t('chat.tool.workflowLint.source')}</strong>
                          <span>{$t('chat.tool.write.summary', { lines: item.callView.lines, chars: item.callView.chars })}</span>
                        </div>
                        <pre class:empty={item.callView.source === ''}>{item.callView.source || $t('chat.tool.workflowLint.missing')}</pre>
                      </div>
                    {:else if item.callView?.kind === 'subagent-task'}
                      <div class="subagent-call">
                        <span>{$t('chat.tool.subagent.task')}</span>
                        <p>{item.callView.task || item.callView.target}</p>
                      </div>
                    {:else if item.callView?.kind === 'subagent-handle'}
                      <div class="subagent-call compact">
                        <div class="find-row">
                          <span>{$t('chat.tool.subagent.handle')}</span>
                          <code>{item.callView.handle || $t('chat.tool.subagent.handleMissing')}</code>
                        </div>
                      </div>
                    {/if}
                    {#if item.callView?.kind !== 'generic' && item.arguments}
                      <details class="tool-raw">
                        <summary>{$t('chat.argsJson')}</summary>
                        <pre>{formatArgs(item.arguments)}</pre>
                      </details>
                    {:else if item.arguments}
                      <pre>{formatArgs(item.arguments)}</pre>
                    {:else if item.invalidArguments}
                      <pre>{item.invalidArguments}</pre>
                    {/if}
                  </div>
                {:else if item.role === 'toolResult'}
                  <div class="tool-mini">
                    <span class="dot {item.isError ? 'error' : 'done'}"></span>
                    <strong>{item.toolName}</strong>
                    <span>{item.summary}</span>
                  </div>
                {:else if item.role === 'status'}
                  <div class="tool-mini">
                    <span class="dot {item.isError ? 'error' : 'done'}"></span>
                    <strong>{subAgentStatusLabel(item.content)}</strong>
                    {#if item.summary}<span>{item.summary}</span>{/if}
                  </div>
                {:else}
                  <pre>{item.content || formatArgs(item.arguments)}</pre>
                {/if}
              </article>
            {/each}
          {/if}
        </section>
      </div>
    </div>
  </div>
{/if}
