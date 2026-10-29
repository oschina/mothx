import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const srcRoot = fileURLToPath(new URL("../", import.meta.url));
const preferencesPath = join(srcRoot, "lib/preferences.js");
const source = readFileSync(preferencesPath, "utf8");

function dictionaryKeys(source, name) {
	const start = source.indexOf(`\n  ${name}: {`);
	assert.notEqual(start, -1, `missing ${name} dictionary`);
	const next = source.indexOf("\n  }", start + 1);
	assert.notEqual(next, -1, `unterminated ${name} dictionary`);
	return new Set([...source.slice(start, next).matchAll(/'([^']+)':/g)].map((match) => match[1]));
}

const zhKeys = dictionaryKeys(source, "zh");
const enKeys = dictionaryKeys(source, "en");

function sourceFiles(dir) {
	const out = [];
	for (const entry of readdirSync(dir)) {
		const path = join(dir, entry);
		if (statSync(path).isDirectory()) {
			out.push(...sourceFiles(path));
		} else if (entry.endsWith(".svelte") || entry.endsWith(".js")) {
			out.push(path);
		}
	}
	return out;
}

function usedKeys() {
	const used = new Set();
	for (const path of sourceFiles(srcRoot)) {
		if (path === preferencesPath || path.endsWith(".test.js")) continue;
		const text = readFileSync(path, "utf8");
		for (const match of text.matchAll(/\$?\bt\('([^']+)'/g)) {
			used.add(match[1]);
		}
	}
	return used;
}

test("zh and en dictionaries expose the same keys", () => {
	assert.deepEqual([...zhKeys].sort(), [...enKeys].sort());
});

test("every translated key used in ui/src exists in both dictionaries", () => {
	const missingZh = [...usedKeys()].filter((key) => !zhKeys.has(key)).sort();
	const missingEn = [...usedKeys()].filter((key) => !enKeys.has(key)).sort();
	assert.deepEqual(missingZh, [], `missing zh translations: ${missingZh.join(", ")}`);
	assert.deepEqual(missingEn, [], `missing en translations: ${missingEn.join(", ")}`);
});
