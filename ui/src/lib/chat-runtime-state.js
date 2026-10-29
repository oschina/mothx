// Session runtime state manager for the Chat view.
//
// Owns the runtime snapshot lifecycle: loading (GET /runtime), PATCH updates
// with optimistic apply + rollback, mode/display-mode switching, and
// capability -> session-tool synchronization. All server actions and the
// canonical sessionRunStates writer are injected, so the manager is pure JS
// and unit-testable without Svelte.
//
// ChatPane stays a thin adapter: it injects real store/API functions and
// renders the snapshot the manager publishes into sessionRunStates.


// Snapshot display-mode normalization: the UI collapses everything but 'code'
// into 'work' (the expanded/runtime panel state is kept in the view).
export function displayModeFromSnapshot(snapshot) {
  return snapshot?.displayMode === 'code' ? 'code' : 'work';
}

// Boolean { key: enabled } map directly usable by the session tool options.
export function enabledToolsFromCapabilities(snapshot) {
  return Object.fromEntries(
    Object.entries(snapshot?.capabilities || {}).map(([key, state]) => [key, Boolean(state?.enabled)])
  );
}

/**
 * @param {object} deps Injected dependencies:
 *   store: { get(): snapshot|null, set(snapshot): void }  — sessionRuntime store adapter.
 *   currentSession(): string  — selected session id ('' when composing a new session).
 *   runLifecycle(): number    — monotonic run lifecycle version owned by the view.
 *   isBusy(): boolean         — completion/runtime-busy state from the view.
 *   getSessionRuntime(id), patchSessionRuntime(id, patch),
 *   getSessionTools(): object, setSessionTools(id, tools): void
 *   upsertSession(patch), refreshSessions(): Promise
 *   onError(err), onSnapshot(next), onDisplayMode(mode),
 *   onNewSessionMode(mode), onUpdatingChange(flag)
 */
export function createSessionRuntimeManager(deps) {
  // Guards against stale GETs: every PATCH bumps this; an in-flight load that
  // started before the mutation must not overwrite the authoritative response.
  let mutationVersion = 0;
  let updating = false;

  const currentSnapshot = () => deps.store.get();

  function publish(next) {
    deps.store.set(next);
    deps.onSnapshot(next);
  }

  function syncToolsFromCapabilities(id, snapshot) {
    const enabledTools = enabledToolsFromCapabilities(snapshot);
    deps.setSessionTools(id, { ...deps.getSessionTools(), ...enabledTools });
  }

  function stillCurrent(id, snapshotMutationVersion, lifecycleVersion) {
    return id !== '' && id === deps.currentSession()
      && snapshotMutationVersion === mutationVersion
      && lifecycleVersion === deps.runLifecycle();
  }

  return {
    get updating() {
      return updating;
    },

    // Load the persisted runtime snapshot for `id`. The response only lands if
    // the session, the mutation counter, and the run lifecycle are unchanged
    // since the request started; otherwise it is dropped as stale.
    async load(id, expectedRunLifecycleVersion = null) {
      if (!id) {
        publish(null);
        return;
      }
      const snapshotMutationVersion = mutationVersion;
      const requestRunLifecycleVersion = expectedRunLifecycleVersion == null
        ? deps.runLifecycle()
        : expectedRunLifecycleVersion;
      try {
        const snapshot = await deps.getSessionRuntime(id);
        if (!stillCurrent(id, snapshotMutationVersion, requestRunLifecycleVersion)) return;
        publish(snapshot);
        syncToolsFromCapabilities(id, snapshot);
      } catch (err) {
        if (stillCurrent(id, snapshotMutationVersion, requestRunLifecycleVersion)) deps.onError(err);
      }
    },

    // PATCH the runtime with optimistic reflect + rollback on failure.
    // Incrementing mutationVersion invalidates any older in-flight session-load
    // GET so a stale snapshot can never restore the previous mode.
    async update(patch) {
      const id = deps.currentSession();
      if (!id || updating) return;
      const previous = currentSnapshot();
      const snapshotMutationVersion = ++mutationVersion;
      updating = true;
      deps.onUpdatingChange(true);

      const optimistic = {
        ...(previous || { sessionId: id }),
        ...(patch.mode ? { mode: patch.mode } : {}),
        ...(patch.displayMode ? { displayMode: patch.displayMode } : {})
      };
      publish(optimistic);

      try {
        const snapshot = await deps.patchSessionRuntime(id, patch);
        if (stillCurrent(id, snapshotMutationVersion, deps.runLifecycle())) {
          publish(snapshot);
          syncToolsFromCapabilities(id, snapshot);
        }
        // The PATCH response is authoritative; the mode controls must not stay
        // disabled while the independent session-list refresh waits for
        // first-start initialization endpoints.
        deps.upsertSession({ id, mode: snapshot?.mode });
        void deps.refreshSessions().catch((refreshErr) => {
          console.warn('Failed to refresh sessions after runtime update:', refreshErr);
        });
      } catch (err) {
        if (stillCurrent(id, snapshotMutationVersion, deps.runLifecycle())) {
          publish(previous);
          deps.onError(err);
        }
      } finally {
        updating = false;
        deps.onUpdatingChange(false);
      }
    },

    async setMode(mode) {
      if (deps.isBusy() || updating) return;
      if (!deps.currentSession()) {
        deps.onNewSessionMode(mode);
        return;
      }
      await this.update({ mode });
    },

    async setDisplayMode(displayMode) {
      if (deps.isBusy() || updating) return;
      const next = displayMode === 'code' ? 'code' : 'work';
      // Reflect immediately so the control stays responsive while the PATCH is
      // in flight; the authoritative response re-derives it on success, and a
      // failure rolls it back to the previous snapshot's value.
      deps.onDisplayMode(next);
      if (!deps.currentSession()) return;
      await this.update({ displayMode: next });
    },

  };
}