import assert from "node:assert/strict";
import test from "node:test";
import {
	MAX_WORKBENCH_DOCUMENT_CHARS,
	WORKBENCH_STORAGE_KEY,
	clearWorkbenchDocument,
	readWorkbenchDocument,
	writeWorkbenchDocument
} from "./persistence.js";

function memoryStorage(initial = {}) {
	const data = new Map(Object.entries(initial));
	return {
		getItem: (key) => data.get(key) ?? null,
		setItem: (key, value) => data.set(key, String(value)),
		removeItem: (key) => data.delete(key)
	};
}

test("workbench document round-trips through localStorage", () => {
	const storage = memoryStorage();
	assert.equal(readWorkbenchDocument(storage), null);
	assert.equal(writeWorkbenchDocument(JSON.stringify({ version: 1 }), storage), true);
	assert.deepEqual(readWorkbenchDocument(storage), { version: 1 });
	clearWorkbenchDocument(storage);
	assert.equal(readWorkbenchDocument(storage), null);
});

test("corrupt workbench documents are ignored", () => {
	const storage = memoryStorage({ [WORKBENCH_STORAGE_KEY]: "{not json" });
	assert.equal(readWorkbenchDocument(storage), null);
});

test("oversized workbench documents are rejected before parsing", () => {
	const storage = memoryStorage({
		[WORKBENCH_STORAGE_KEY]: "x".repeat(MAX_WORKBENCH_DOCUMENT_CHARS + 1)
	});
	assert.equal(readWorkbenchDocument(storage), null);
});

test("write failures do not throw", () => {
	const storage = {
		getItem: () => null,
		setItem: () => { throw new Error("quota"); },
		removeItem: () => { throw new Error("quota"); }
	};
	assert.equal(writeWorkbenchDocument("{}", storage), false);
	assert.doesNotThrow(() => clearWorkbenchDocument(storage));
});
