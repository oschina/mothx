import assert from "node:assert/strict";
import test from "node:test";
import { clearAllComposerDrafts, readComposerDraft, writeComposerDraft } from "./composer-drafts.js";
import { clearSessionPreferences, readSessionPreferences, writeSessionPreferences } from "./session-prefs.js";

function memoryStorage() {
	const data = new Map();
	return {
		getItem: (key) => data.get(key) ?? null,
		setItem: (key, value) => data.set(key, String(value)),
		removeItem: (key) => data.delete(key)
	};
}

test("composer drafts keep text and uploaded references", () => {
	globalThis.localStorage = memoryStorage();
	clearAllComposerDrafts();
	writeComposerDraft("session:s1", {
		text: "hello",
		attachments: [{ providerRef: "ref_1", name: "a.png", mediaType: "image/png", size: 12 }]
	});
	const draft = readComposerDraft("session:s1");
	assert.equal(draft.text, "hello");
	assert.equal(draft.attachments.length, 1);
	assert.equal(draft.attachments[0].providerRef, "ref_1");
});

test("session preferences round-trip model/provider", () => {
	globalThis.localStorage = memoryStorage();
	clearSessionPreferences("s1");
	writeSessionPreferences("s1", { model: "m1", provider: "p1" });
	const prefs = readSessionPreferences("s1");
	assert.deepEqual(prefs, { model: "m1", provider: "p1" });
});
