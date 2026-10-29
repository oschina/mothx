import assert from "node:assert/strict";
import test from "node:test";
import { clearAllComposerDrafts, clearComposerDraft, readComposerDraft, writeComposerDraft } from "./composer-drafts.js";

function memoryStorage() {
	const data = new Map();
	return {
		getItem: (key) => data.get(key) ?? null,
		setItem: (key, value) => data.set(key, String(value)),
		removeItem: (key) => data.delete(key)
	};
}

// Node caches ES modules per URL, so a query string gives us a fresh module
// instance with the same localStorage — exactly what a page reload does.
function freshModule(tag) {
	return import(`${new URL("./composer-drafts.js", import.meta.url).href}?reload=${tag}`);
}

test("dataUrl attachments survive a tab switch without being persisted", () => {
	globalThis.localStorage = memoryStorage();
	clearAllComposerDrafts();
	writeComposerDraft("session:s1", {
		text: "hello",
		attachments: [{
			kind: "image",
			filename: "a.png",
			name: "a.png",
			mediaType: "image/png",
			size: 12,
			dataUrl: "data:image/png;base64,AAAA"
		}]
	});
	const draft = readComposerDraft("session:s1");
	assert.equal(draft.text, "hello");
	assert.equal(draft.attachments.length, 1);
	assert.equal(draft.attachments[0].dataUrl, "data:image/png;base64,AAAA");
	assert.equal(draft.missingAttachments.length, 0, "same page session has the blob in memory");
	const raw = globalThis.localStorage.getItem("mothx.composer-drafts.v1");
	assert.ok(raw, "text is persisted");
	assert.equal(raw.includes("data:"), false, "blob payloads must never be persisted");
});

test("a reload keeps text and asks the user to reselect blob attachments", async () => {
	globalThis.localStorage = memoryStorage();
	const before = await freshModule("before");
	before.clearAllComposerDrafts();
	before.writeComposerDraft("draft:new-1", {
		text: "unfinished prompt",
		attachments: [{
			kind: "file",
			filename: "notes.txt",
			name: "notes.txt",
			mediaType: "text/plain",
			size: 42,
			dataUrl: "data:text/plain;base64,bm90ZXM="
		}]
	});
	const after = await freshModule("after");
	const restored = after.readComposerDraft("draft:new-1");
	assert.equal(restored.text, "unfinished prompt");
	assert.equal(restored.attachments.length, 0);
	assert.equal(restored.missingAttachments.length, 1);
	assert.equal(restored.missingAttachments[0].name, "notes.txt");
});

test("providerRef attachments are restored across a reload", async () => {
	globalThis.localStorage = memoryStorage();
	const before = await freshModule("provider-before");
	before.clearAllComposerDrafts();
	before.writeComposerDraft("session:s2", {
		text: "",
		attachments: [{
			kind: "file",
			filename: "report.pdf",
			name: "report.pdf",
			mediaType: "application/pdf",
			size: 9,
			providerRef: "file_123"
		}]
	});
	const after = await freshModule("provider-after");
	const restored = after.readComposerDraft("session:s2");
	assert.equal(restored.attachments.length, 1);
	assert.equal(restored.attachments[0].providerRef, "file_123");
	assert.equal(restored.missingAttachments.length, 0);
});

test("sending clears text and attachments for that key", () => {
	globalThis.localStorage = memoryStorage();
	clearAllComposerDrafts();
	writeComposerDraft("session:s3", { text: "send me", attachments: [] });
	clearComposerDraft("session:s3");
	assert.deepEqual(readComposerDraft("session:s3"), { text: "", attachments: [], missingAttachments: [] });
	assert.equal(globalThis.localStorage.getItem("mothx.composer-drafts.v1"), "{}");
});

test("draft keys are independent from session keys", () => {
	globalThis.localStorage = memoryStorage();
	clearAllComposerDrafts();
	writeComposerDraft("session:draft:x", { text: "session" });
	writeComposerDraft("draft:x", { text: "draft" });
	assert.equal(readComposerDraft("session:draft:x").text, "session");
	assert.equal(readComposerDraft("draft:x").text, "draft");
});

test("draft storage is capped at 16 entries", () => {
	globalThis.localStorage = memoryStorage();
	clearAllComposerDrafts();
	for (let index = 0; index < 20; index += 1) {
		writeComposerDraft(`session:lru-${index}`, { text: `text-${index}` });
	}
	const stored = JSON.parse(globalThis.localStorage.getItem("mothx.composer-drafts.v1"));
	assert.equal(Object.keys(stored).length, 16);
	assert.equal(stored["session:lru-19"].text, "text-19");
});
