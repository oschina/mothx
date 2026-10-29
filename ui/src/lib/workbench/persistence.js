export const WORKBENCH_STORAGE_KEY = "mothx.workbench.v1";
// A real layout document is a few KB. Anything far beyond that is either
// corruption or a future/foreign payload; parse it only after rejecting it so
// a giant localStorage value cannot stall startup.
export const MAX_WORKBENCH_DOCUMENT_CHARS = 512 * 1024;

export function readWorkbenchDocument(storage = globalThis.localStorage) {
	if (!storage) return null;
	try {
		const raw = storage.getItem(WORKBENCH_STORAGE_KEY);
		if (!raw || raw.length > MAX_WORKBENCH_DOCUMENT_CHARS) return null;
		return JSON.parse(raw);
	} catch {
		return null;
	}
}

export function writeWorkbenchDocument(text, storage = globalThis.localStorage) {
	if (!storage || !text) return false;
	try {
		storage.setItem(WORKBENCH_STORAGE_KEY, text);
		return true;
	} catch {
		return false;
	}
}

export function clearWorkbenchDocument(storage = globalThis.localStorage) {
	try {
		storage?.removeItem(WORKBENCH_STORAGE_KEY);
	} catch {
		// Best effort.
	}
}
