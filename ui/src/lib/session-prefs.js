const STORAGE_KEY = "mothx.session-prefs.v1";
const prefs = new Map();

function storage() {
	return globalThis.localStorage;
}

function load() {
	if (prefs.size > 0) return;
	try {
		const raw = storage()?.getItem(STORAGE_KEY);
		const parsed = raw ? JSON.parse(raw) : {};
		for (const [sessionId, value] of Object.entries(parsed || {})) {
			if (!sessionId || !value || typeof value !== "object") continue;
			prefs.set(sessionId, {
				model: typeof value.model === "string" ? value.model.slice(0, 256) : "",
				provider: typeof value.provider === "string" ? value.provider.slice(0, 128) : ""
			});
		}
	} catch {
		// Ignore corrupt local preferences.
	}
}

function persist() {
	try {
		storage()?.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(prefs)));
	} catch {
		// Quota/private mode: keep the in-memory copy.
	}
}

export function readSessionPreferences(sessionId) {
	if (!sessionId) return { model: "", provider: "" };
	load();
	const value = prefs.get(sessionId);
	return value ? { ...value } : { model: "", provider: "" };
}

export function writeSessionPreferences(sessionId, { model = "", provider = "" } = {}) {
	if (!sessionId) return;
	load();
	prefs.set(sessionId, {
		model: String(model || "").slice(0, 256),
		provider: String(provider || "").slice(0, 128)
	});
	persist();
}

export function clearSessionPreferences(sessionId) {
	if (!sessionId) return;
	load();
	prefs.delete(sessionId);
	persist();
}
