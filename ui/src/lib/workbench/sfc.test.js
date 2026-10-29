import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { compile } from "svelte/compiler";

const root = new URL("../../", import.meta.url).pathname;

function compileAll(dir, files) {
	for (const file of files) {
		const path = join(root, dir, file);
		const source = readFileSync(path, "utf8");
		assert.doesNotThrow(() => compile(source, { filename: file, generate: "client" }), `${dir}/${file}`);
	}
}

test("workbench, chat, and terminal Svelte components compile", () => {
	compileAll("components/workbench", readdirSync(join(root, "components/workbench")).filter((f) => f.endsWith(".svelte")));
	compileAll("components/chat", readdirSync(join(root, "components/chat")).filter((f) => f.endsWith(".svelte")));
	compileAll("components/terminal", readdirSync(join(root, "components/terminal")).filter((f) => f.endsWith(".svelte")));
	compileAll("views", ["Chat.svelte"]);
});
