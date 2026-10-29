import { group, split } from "./model.js";

export const BUILTIN_PRESETS = [
	{ id: "single", name: "workbench.preset.single", frame: { version: 1, center: group("g1") } },
	{ id: "two-column", name: "workbench.preset.twoColumn", frame: { version: 1, center: split("s1", "horizontal", group("g1"), group("g2")) } },
	{ id: "two-row", name: "workbench.preset.twoRow", frame: { version: 1, center: split("s1", "vertical", group("g1"), group("g2")) } },
	{
		id: "quad",
		name: "workbench.preset.quad",
		frame: {
			version: 1,
			center: split("s1", "horizontal",
				split("s2", "vertical", group("g1"), group("g2")),
				split("s3", "vertical", group("g3"), group("g4")))
		}
	}
];

export function presetById(id) {
	return BUILTIN_PRESETS.find((preset) => preset.id === id) || null;
}
