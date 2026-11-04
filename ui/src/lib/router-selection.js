// Router worker selection for the Chat composer.
//
// The WebUI may enter Router mode by binding one or more configured external
// ACP workers to the session. The catalog and the activation both live on the
// server; this module owns only the projection and the local selection state,
// so the behavior stays unit-testable without a browser.
//
// Router is an explicit choice: entering the mode requires naming a worker,
// because the Runtime refuses a Router session without a bound worker.

/** Normalizes the catalog into the shape the composer renders. */
export function normalizeWorkerCatalog(profiles) {
  if (!Array.isArray(profiles)) return [];
  return profiles
    .filter((profile) => profile && typeof profile.id === 'string' && profile.id !== '')
    .map((profile) => ({
      id: profile.id,
      title: profile.title || profile.id,
      enabled: Boolean(profile.enabled),
      // Accept both the wire name and the normalized one so the projection is
      // idempotent: a re-normalized catalog must not lose its eligibility.
      eligible: Boolean(profile.routerEligible ?? profile.eligible),
      roles: Array.isArray(profile.roles) ? profile.roles : [],
      maxConcurrentChildren: Number(profile.maxConcurrentChildren) || 1,
      parallelBaseWorkspaceWrites: Boolean(profile.parallelBaseWorkspaceWrites),
      invalidReason: profile.invalidReason || ''
    }));
}

/**
 * Returns the workers the user can actually pick: enabled and Router-eligible.
 * An ineligible worker stays in the catalog so the UI can explain why it is
 * unavailable instead of silently hiding it.
 */
export function selectableWorkers(catalog) {
  return normalizeWorkerCatalog(catalog).filter((worker) => worker.enabled && worker.eligible);
}

/** Validates a selection before it is sent to the server. */
export function validateWorkerSelection(catalog, selection) {
  const selectable = selectableWorkers(catalog);
  const ids = Array.isArray(selection) ? selection.map((id) => String(id || '').trim()).filter(Boolean) : [];
  if (ids.length === 0) {
    return { ok: false, error: 'select at least one ACP worker' };
  }
  const known = new Set(selectable.map((worker) => worker.id));
  const unknown = ids.filter((id) => !known.has(id));
  if (unknown.length > 0) {
    return { ok: false, error: `not an available ACP worker: ${unknown.join(', ')}` };
  }
  return { ok: true, profileIds: ids };
}

/**
 * Creates the Router selection state used by the composer.
 *
 * The manager is pure state plus injected server calls: it never talks to the
 * network itself.
 */
export function createRouterSelectionManager(deps) {
  let catalog = [];
  let selection = [];
  let loading = false;
  let error = '';

  const publish = () => {
    deps.onState({
      catalog: normalizeWorkerCatalog(catalog),
      selectable: selectableWorkers(catalog),
      selection: [...selection],
      loading,
      error
    });
  };

  async function load() {
    loading = true;
    error = '';
    publish();
    try {
      catalog = normalizeWorkerCatalog(await deps.listWorkers());
      // A selection that is no longer available must not survive a reload.
      const known = new Set(catalog.filter((worker) => worker.enabled && worker.eligible).map((worker) => worker.id));
      selection = selection.filter((id) => known.has(id));
    } catch (err) {
      error = err?.message || String(err);
      catalog = [];
      selection = [];
    } finally {
      loading = false;
      publish();
    }
  }

  function toggle(id) {
    const trimmed = String(id || '').trim();
    if (!trimmed) return;
    selection = selection.includes(trimmed)
      ? selection.filter((value) => value !== trimmed)
      : [...selection, trimmed];
    error = '';
    publish();
  }

  async function activate() {
    const validated = validateWorkerSelection(catalog, selection);
    if (!validated.ok) {
      error = validated.error;
      publish();
      return { ok: false, error: validated.error };
    }
    loading = true;
    error = '';
    publish();
    try {
      const state = await deps.activate(validated.profileIds);
      publish();
      return { ok: true, state };
    } catch (err) {
      error = err?.message || String(err);
      publish();
      return { ok: false, error: error };
    } finally {
      loading = false;
      publish();
    }
  }

  async function deactivate() {
    loading = true;
    error = '';
    publish();
    try {
      const state = await deps.deactivate();
      publish();
      return { ok: true, state };
    } catch (err) {
      error = err?.message || String(err);
      publish();
      return { ok: false, error: error };
    } finally {
      loading = false;
      publish();
    }
  }

  function setSelection(ids) {
    selection = Array.isArray(ids) ? ids.map((id) => String(id || '').trim()).filter(Boolean) : [];
    publish();
  }

  publish();

  return { load, toggle, activate, deactivate, setSelection };
}
