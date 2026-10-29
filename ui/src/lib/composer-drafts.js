// Composer drafts are local presentation state, not session facts. They are
// kept out of the workbench layout document so a layout write can never carry
// message text or attachment payloads.
//
// Shape per key (`<sessionId>` or `draft:<draftId>`):
//   { text: string, attachments: DraftAttachment[], updatedAt: number }
//
// Attachments that only carry a `dataUrl` live in this module's in-memory map
// so switching tabs (which unmounts ChatPane) does not lose them. They are not
// written to localStorage: only `providerRef` attachments survive a reload.
// Metadata for the rest is persisted as `missing` so the composer can say which
// files must be selected again after a refresh.
const STORAGE_KEY = "mothx.composer-drafts.v1";
const MAX_DRAFTS = 16;
const MAX_TEXT = 16 * 1024;
const MAX_ATTACHMENTS = 12;

const drafts = new Map();
let loaded = false;
let lastUpdatedAt = 0;

function nextUpdatedAt() {
	// Several writes can share one millisecond (file selection, hydration,
	// immediate send). Ordering must still be strictly monotonic or the LRU
	// cap would keep the oldest entries instead of the newest.
	lastUpdatedAt = Math.max(Date.now(), lastUpdatedAt + 1);
	return lastUpdatedAt;
}

function storage() {
	return globalThis.localStorage;
}

function text(value, limit) {
	return typeof value === "string" ? value.slice(0, limit) : "";
}

function normalizeMemoryAttachments(value) {
	if (!Array.isArray(value)) return [];
	return value
		.filter((item) => item && typeof item === "object")
		.slice(0, MAX_ATTACHMENTS)
		.map((item) => {
			const attachment = {
				kind: item.kind === "image" ? "image" : "file",
				name: text(item.name || item.filename, 256),
				filename: text(item.filename || item.name, 256),
				mediaType: text(item.mediaType, 128),
				size: Number(item.size) || 0
			};
			const dataUrl = typeof item.dataUrl === "string" ? item.dataUrl : "";
			if (dataUrl.startsWith("data:")) attachment.dataUrl = dataUrl;
			const providerRef = text(item.providerRef, 256);
			if (providerRef) attachment.providerRef = providerRef;
			const attachmentID = text(item.attachmentId, 256);
			if (attachmentID) attachment.attachmentId = attachmentID;
			return attachment;
		});
}

function normalizeMissingAttachments(value) {
	if (!Array.isArray(value)) return [];
	return value
		.filter((item) => item && typeof item === "object")
		.slice(0, MAX_ATTACHMENTS)
		.map((item) => ({
			kind: item.kind === "image" ? "image" : "file",
			name: text(item.name || item.filename, 256),
			filename: text(item.filename || item.name, 256),
			mediaType: text(item.mediaType, 128),
			size: Number(item.size) || 0
		}));
}

function persistedAttachments(attachments) {
	// Only references the server can resolve without a blob may cross a reload.
	return attachments
		.filter((item) => item.providerRef || item.attachmentId)
		.map((item) => ({
			kind: item.kind,
			name: item.name,
			filename: item.filename,
			mediaType: item.mediaType,
			size: item.size,
			providerRef: item.providerRef,
			attachmentId: item.attachmentId
		}));
}

function missingAttachments(attachments) {
	return attachments
		.filter((item) => !item.providerRef && !item.attachmentId)
		.map((item) => ({
			kind: item.kind,
			name: item.name,
			filename: item.filename,
			mediaType: item.mediaType,
			size: item.size
		}));
}

function load() {
	if (loaded) return;
	loaded = true;
	try {
		const raw = storage()?.getItem(STORAGE_KEY);
		const parsed = raw ? JSON.parse(raw) : {};
		for (const [key, value] of Object.entries(parsed || {})) {
			if (!key || !value || typeof value !== "object") continue;
			const updatedAt = Number(value.updatedAt) || 0;
			if (updatedAt > lastUpdatedAt) lastUpdatedAt = updatedAt;
			drafts.set(key, {
				text: text(value.text, MAX_TEXT),
				attachments: normalizeMemoryAttachments(value.attachments),
				missing: normalizeMissingAttachments(value.missing),
				updatedAt,
				fromStorage: true
			});
		}
	} catch {
		// Corrupt local drafts must not break the composer.
	}
}

function persist() {
	const entries = [...drafts.entries()]
		.sort((a, b) => (b[1].updatedAt || 0) - (a[1].updatedAt || 0))
		.slice(0, MAX_DRAFTS);
	const keep = new Set(entries.map(([key]) => key));
	for (const key of [...drafts.keys()]) {
		if (!keep.has(key)) drafts.delete(key);
	}
	const payload = {};
	for (const [key, value] of entries) {
		payload[key] = {
			text: value.text,
			attachments: persistedAttachments(value.attachments),
			missing: missingAttachments(value.attachments),
			updatedAt: value.updatedAt
		};
	}
	try {
		storage()?.setItem(STORAGE_KEY, JSON.stringify(payload));
	} catch {
		// Quota/private mode: keep the in-memory copy.
	}
}

export function readComposerDraft(key) {
	if (!key) return { text: "", attachments: [], missingAttachments: [] };
	load();
	const value = drafts.get(key);
	if (!value) return { text: "", attachments: [], missingAttachments: [] };
	return {
		text: value.text,
		attachments: value.attachments.map((item) => ({ ...item })),
		// `missing` only describes attachments dropped by the last persisted
		// write. While the in-memory copy still holds them (same page session),
		// there is nothing to reselect, so never surface it.
		missingAttachments: value.fromStorage
			? (value.missing || []).map((item) => ({ ...item }))
			: []
	};
}

export function writeComposerDraft(key, draft = {}) {
	if (!key) return;
	load();
	const current = drafts.get(key);
	const nextText = typeof draft.text === "string" ? draft.text.slice(0, MAX_TEXT) : current?.text || "";
	const nextAttachments = draft.attachments === undefined
		? (current?.attachments || [])
		: normalizeMemoryAttachments(draft.attachments);
	if (!nextText && nextAttachments.length === 0) {
		drafts.delete(key);
	} else {
		drafts.set(key, {
			text: nextText,
			attachments: nextAttachments,
			missing: [],
			updatedAt: nextUpdatedAt(),
			fromStorage: false
		});
	}
	persist();
}

export function clearComposerDraft(key) {
	if (!key) return;
	load();
	if (!drafts.delete(key)) return;
	persist();
}

export function clearAllComposerDrafts() {
	drafts.clear();
	loaded = false;
	try {
		storage()?.removeItem(STORAGE_KEY);
	} catch {
		// Best effort.
	}
}
