import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

const root = new URL("../../", import.meta.url).pathname;

function read(path) {
	return readFileSync(join(root, path), "utf8");
}

test("workbench components do not reach back into the chat view", () => {
	for (const file of readdirSync(join(root, "components/workbench")).filter((name) => name.endsWith(".svelte"))) {
		const source = read(`components/workbench/${file}`);
		assert.equal(/views\/Chat\.svelte/.test(source), false, `${file} must not import Chat.svelte`);
	}
});

test("workbench model modules stay free of Svelte stores and API state", () => {
	for (const file of ["model.js", "normalize.js", "presets.js", "persistence.js"]) {
		const source = read(`lib/workbench/${file}`);
		assert.equal(/from ["']svelte\/store["']/.test(source), false, `${file} must stay store-free`);
		assert.equal(/from ["'].*stores\.js["']/.test(source), false, `${file} must not import stores.js`);
		assert.equal(/from ["'].*api\.js["']/.test(source), false, `${file} must not import api.js`);
	}
});

test("pane-scoped DOM ids cannot collide in split layouts", () => {
	const pane = read("components/chat/ChatPane.svelte");
	assert.equal(/id="chat-command-suggestions"/.test(pane), false);
	assert.equal(/id=\{`chat-command-suggestion-/.test(pane), false);
	assert.match(pane, /id=\{`\$\{paneId\}-command-suggestions`\}/);
	assert.match(pane, /id=\{`\$\{paneId\}-command-suggestion-\$\{index\}`\}/);
	assert.match(pane, /aria-activedescendant=.*\$\{paneId\}-command-suggestion-/);
});

test("terminal dock loads xterm styles and keeps recovery hooks", () => {
	const main = read("main.js");
	assert.match(main, /@xterm\/xterm\/css\/xterm\.css/);
	const pane = read("components/terminal/TerminalPane.svelte");
	assert.match(pane, /function restoreTerminal/);
	assert.match(pane, /function reconnectTerminal/);
	assert.match(pane, /sessionStorage/);
});
