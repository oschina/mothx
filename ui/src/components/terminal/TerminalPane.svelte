<script>
  import { onMount, onDestroy } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { t } from '../../lib/preferences.js';
  import { features } from '../../lib/stores.js';
  import { sessionRunStates } from '../../lib/session-runs.js';
  import { restorableTerminal, terminalAgentWarningKey } from '../../lib/terminal-status.js';

  export let sessionId = '';
  export let active = true;
  // Optional path relative to the session cwd (requested from the Files
  // panel). It only takes effect when a fresh shell is created; an existing
  // reattached terminal keeps its own cwd.
  export let initialCwd = '';

  let host;
  let term = null;
  let fit = null;
  let ws = null;
  let resizeObserver = null;
  let terminalId = '';
  let terminalStorageKey = '';
  let state = 'starting';
  let error = '';
  let retryTimer = 0;
  let disposed = false;
  let connectAttempts = 0;
  let boundSessionId = '';
  let generation = 0;
  let throttled = false;
  let throttleTimer = 0;

  $: terminalWarning = $features?.terminalWarning === 'placeholder_token'
    ? $t('terminal.warning.placeholderToken')
    : ($features?.terminalWarning || '');
  $: sessionExecution = $sessionRunStates?.[sessionId]?.runtime?.execution || null;
  $: agentWarningKey = terminalAgentWarningKey(sessionExecution);

  // The dock is bound to the focused session. Switching focus must not keep the
  // previous session's shell attached and visible, so reconnect to the new cwd.
  $: if (term && sessionId && boundSessionId && sessionId !== boundSessionId) {
    boundSessionId = sessionId;
    rebindSession();
  }

  const encoder = new TextEncoder();

  onMount(() => {
    term = new Terminal({
      cursorBlink: true,
      convertEol: false,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      theme: { background: '#111318', foreground: '#e6e8ee' }
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();
    term.onData((data) => sendFrame(0x01, encoder.encode(data)));
    resizeObserver = new ResizeObserver(() => {
      if (!fit) return;
      try {
        fit.fit();
        sendResize();
      } catch {
        // Host may be hidden; the next visible resize will catch up.
      }
    });
    resizeObserver.observe(host);
    boundSessionId = sessionId;
    restoreTerminal();
    return () => cleanup();
  });

  onDestroy(() => cleanup());

  function setThrottled(active) {
    throttled = active;
    if (throttleTimer) {
      clearTimeout(throttleTimer);
      throttleTimer = 0;
    }
    // The server refreshes "active" while output is limited. A quiet period
    // means the limit no longer affects the user, so clear the notice.
    if (active) {
      throttleTimer = setTimeout(() => {
        throttleTimer = 0;
        throttled = false;
      }, 2000);
    }
  }

  function terminalStorageKeyFor(id) {
    return id ? `mothx.terminal.v1:${id}` : '';
  }

  function rememberTerminal(id, sid) {
    terminalStorageKey = terminalStorageKeyFor(sid);
    if (!terminalStorageKey || !id) return;
    try { sessionStorage.setItem(terminalStorageKey, id); } catch {}
  }

  function forgetTerminal() {
    const key = terminalStorageKey;
    terminalStorageKey = '';
    if (!key) return;
    try { sessionStorage.removeItem(key); } catch {}
  }

  function releaseConnection() {
    generation += 1;
    connectAttempts = 0;
    setThrottled(false);
    if (retryTimer) {
      clearTimeout(retryTimer);
      retryTimer = 0;
    }
    const socket = ws;
    ws = null;
    try { socket?.close(); } catch {}
    const id = terminalId;
    terminalId = '';
    forgetTerminal();
    if (id) {
      fetch(`/api/terminals/${encodeURIComponent(id)}`, { method: 'DELETE', keepalive: true }).catch(() => {});
    }
  }

  function rebindSession() {
    releaseConnection();
    error = '';
    state = 'starting';
    try { term?.reset(); } catch {}
    createTerminal();
  }

  function cleanup() {
    if (disposed) return;
    disposed = true;
    releaseConnection();
    resizeObserver?.disconnect();
    resizeObserver = null;
    try { term?.dispose(); } catch {}
    term = null;
    fit = null;
  }

  async function restoreTerminal() {
    if (!sessionId || disposed) return;
    const attemptGeneration = generation;
    let stored = '';
    try { stored = sessionStorage.getItem(terminalStorageKeyFor(sessionId)) || ''; } catch {}
    if (!stored) {
      createTerminal();
      return;
    }
    try {
      const res = await fetch(`/api/terminals?sessionId=${encodeURIComponent(sessionId)}`);
      if (!res.ok) throw new Error(`terminal list ${res.status}`);
      const body = await res.json().catch(() => ({}));
      if (attemptGeneration !== generation || disposed) return;
      const match = restorableTerminal(body?.terminals, stored);
      if (match) {
        terminalId = match.terminalId;
        terminalStorageKey = terminalStorageKeyFor(sessionId);
        state = match.state || 'detached';
        connect();
        return;
      }
    } catch {
      if (attemptGeneration !== generation || disposed) return;
    }
    try { sessionStorage.removeItem(terminalStorageKeyFor(sessionId)); } catch {}
    createTerminal();
  }

  async function createTerminal() {
    if (!sessionId || disposed) return;
    const attemptGeneration = generation;
    error = '';
    state = 'starting';
    try {
      const res = await fetch('/api/terminals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sessionId,
          cols: fit?.cols || 80,
          rows: fit?.rows || 24,
          ...(initialCwd ? { cwd: initialCwd } : {})
        })
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(body?.code || body?.error || `terminal ${res.status}`);
      }
      if (attemptGeneration !== generation || disposed) {
        // The focused session changed while the request was in flight.
        const staleId = body.terminalId;
        if (staleId) fetch(`/api/terminals/${encodeURIComponent(staleId)}`, { method: 'DELETE', keepalive: true }).catch(() => {});
        return;
      }
      terminalId = body.terminalId;
      rememberTerminal(terminalId, sessionId);
      state = body.state || 'running';
      connect();
    } catch (err) {
      if (attemptGeneration !== generation || disposed) return;
      error = String(err?.message || err);
      state = 'failed';
    }
  }

  function connect() {
    if (!terminalId || disposed) return;
    const attemptGeneration = generation;
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const socket = new WebSocket(`${scheme}://${location.host}/ws/terminals/${encodeURIComponent(terminalId)}`);
    ws = socket;
    socket.binaryType = 'arraybuffer';
    socket.onopen = () => {
      if (ws !== socket || attemptGeneration !== generation) return;
      connectAttempts = 0;
      if (state === 'failed') state = 'running';
      sendResize();
    };
    socket.onmessage = (event) => {
      if (ws !== socket || attemptGeneration !== generation) return;
      handleFrame(new Uint8Array(event.data));
    };
    socket.onclose = (event) => {
      if (attemptGeneration !== generation) return;
      if (ws === socket) ws = null;
      // Ignore a stale socket that closed after a newer connection was opened.
      if (disposed || ws !== null) return;
      // A takeover/closed/exited terminal keeps its terminal id but must not be
      // reported as a normal process exit or auto-reconnected.
      if (state === 'detached' || state === 'closed' || state === 'exited' || state === 'failed') return;
      if (event.code === 1000) {
        forgetTerminal();
        state = 'exited';
        return;
      }
      connectAttempts += 1;
      state = 'disconnected';
      // The serve process may have restarted; its in-memory terminal id is gone.
      // Stop retrying the dead id and let the explicit reconnect button create a
      // fresh terminal instead of looping forever.
      if (connectAttempts >= 5) {
        terminalId = '';
        state = 'failed';
        error = $t('terminal.lost') || 'terminal is gone';
        return;
      }
      retryTimer = setTimeout(() => {
        retryTimer = 0;
        if (!disposed) connect();
      }, 1000);
    };
    socket.onerror = () => {
      if (ws !== socket || attemptGeneration !== generation) return;
      state = 'disconnected';
    };
  }

  function sendFrame(type, payload = new Uint8Array()) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const frame = new Uint8Array(1 + payload.length);
    frame[0] = type;
    frame.set(payload, 1);
    ws.send(frame);
  }

  function sendResize() {
    if (!fit) return;
    const cols = Math.max(20, Math.min(500, fit.proposeDimensions?.()?.cols || fit.cols || 80));
    const rows = Math.max(5, Math.min(300, fit.proposeDimensions?.()?.rows || fit.rows || 24));
    const payload = new Uint8Array(4);
    const view = new DataView(payload.buffer);
    view.setUint16(0, cols);
    view.setUint16(2, rows);
    sendFrame(0x02, payload);
  }

  function handleFrame(frame) {
    if (!frame.length) return;
    const type = frame[0];
    if (type === 0x01) {
      term?.write(frame.subarray(1));
      return;
    }
    if (type === 0x02 && frame.length >= 5) {
      const code = new DataView(frame.buffer, frame.byteOffset + 1).getUint32(0);
      forgetTerminal();
      state = 'exited';
      term?.write(`\r\n[${$t('terminal.exited') || 'process exited'} (${code})]\r\n`);
      return;
    }
    if (type === 0x03) {
      try {
        const control = JSON.parse(new TextDecoder().decode(frame.subarray(1)));
        if (control.type === 'attached') {
          state = 'running';
          setThrottled(Boolean(control.throttled));
          if (control.cols && control.rows) term?.resize(control.cols, control.rows);
        } else if (control.type === 'backpressure') {
          setThrottled(Boolean(control.active));
        } else if (control.type === 'terminal_takeover') {
          state = 'detached';
          error = $t('terminal.takenOver') || 'Terminal taken over by another client';
          ws?.close(1000, 'takeover');
        } else if (control.type === 'closed') {
          forgetTerminal();
          state = 'closed';
          if (control.reason === 'cwd_changed' && !disposed) {
            terminalId = '';
            createTerminal();
          }
        } else if (control.type === 'error') {
          state = 'failed';
          error = control.message || 'terminal error';
          // The server no longer knows this id (for example after a restart),
          // or the shell already exited. Clearing it makes the reconnect button
          // create a fresh terminal instead of attaching to a dead one.
          if (/not found|exited/i.test(String(control.message || ''))) forgetTerminal();
        }
      } catch {
        // Ignore malformed control frames.
      }
    }
  }
  function reconnectTerminal() {
    error = '';
    if (!terminalId || ['exited', 'closed', 'failed'].includes(state)) {
      terminalId = '';
      createTerminal();
      return;
    }
    connect();
  }
</script>

<section class="terminal-pane" class:terminal-pane-active={active} aria-label={$t('terminal.title') || 'Terminal'}>
  <div class="terminal-status">
    <span class="terminal-title">{$t('terminal.title') || 'Terminal'}</span>
    <span class="terminal-state">{state}</span>
    {#if throttled}<span class="terminal-chip terminal-throttled" role="status" title={$t('terminal.throttled')}>⇅</span>{/if}
    <span class="terminal-chip terminal-sandbox" role="note" title={$t('terminal.sandboxWarning')}>⚠</span>
    <span class="terminal-chip terminal-agent-warning" class:terminal-agent-warning-strong={agentWarningKey !== 'terminal.agentWarning.shared'} role="note" title={$t(agentWarningKey)}>◉</span>
    {#if terminalWarning}<span class="terminal-message terminal-warning" role="alert" title={terminalWarning}>{terminalWarning}</span>{/if}
    {#if error}<span class="terminal-message terminal-error" role="alert" title={error}>{error}</span>{/if}
    {#if ['disconnected', 'failed', 'exited', 'closed'].includes(state)}
      <button type="button" class="terminal-reconnect" onclick={reconnectTerminal}>↻</button>
    {/if}
  </div>
  <div class="terminal-host" bind:this={host}></div>
</section>

<style>
  .terminal-pane {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    height: 100%;
    background: #111318;
    color: #e6e8ee;
  }
  .terminal-status {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 24px;
    padding: 2px 6px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    font-size: 11px;
    line-height: 1;
  }
  .terminal-title { opacity: 0.78; }
  .terminal-state { opacity: 0.55; text-transform: uppercase; }
  .terminal-chip,
  .terminal-reconnect {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    flex: 0 0 auto;
    border-radius: 4px;
  }
  .terminal-chip { opacity: 0.8; cursor: help; }
  .terminal-message {
    min-width: 0;
    max-width: min(36ch, 45%);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .terminal-throttled { color: #ffd479; }
  .terminal-sandbox { color: #ffd479; }
  .terminal-warning { color: #ffb86b; }
  .terminal-agent-warning { color: #9bb7cc; }
  .terminal-agent-warning-strong { color: #ffd479; }
  .terminal-error { color: #ff8a80; }
  .terminal-reconnect {
    margin-left: auto;
    background: transparent;
    color: inherit;
    border: 1px solid rgba(255,255,255,.18);
    padding: 0;
    cursor: pointer;
  }
  .terminal-reconnect:hover { background: rgba(255,255,255,.08); }
  .terminal-host { flex: 1; min-height: 0; padding: 2px; overflow: hidden; }
</style>
