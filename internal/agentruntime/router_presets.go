package agentruntime

import (
	"strings"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// ACPAgentPreset is one well-known external ACP agent launch recipe. It exists so
// configuring a common worker is one keystroke instead of remembering which
// binary speaks ACP and which flags start it.
//
// Command is argv and is written verbatim: a bare name is resolved through PATH at
// launch, exactly like any other process launch. The presets are launch recipes,
// not a trust decision — the user still owns the profile and can edit every field.
type ACPAgentPreset struct {
	// ID is the profile ID suggested for the preset.
	ID string
	// Title is the human name shown in the picker.
	Title string
	// Command is the argv that starts the agent's ACP server.
	Command []string
	// Note explains a non-obvious installation or naming detail.
	Note string
	// Driver is the ACP flavor. It is generic-acp for every verified preset
	// today; a flavor is only introduced when real interop exposes a protocol
	// gap that generic ACP cannot describe.
	Driver workerdriver.WorkerDriver
}

// ACPAgentPresets returns the built-in launch recipes for well-known ACP agents.
//
// Every entry was verified against the tool's own documentation or CLI help at the
// time of writing. Several popular CLIs do NOT start an ACP server themselves —
// `codex acp` and `claude acp` are not valid — so those entries use the official
// adapter packages instead. Getting that wrong is the single most common
// configuration mistake, which is why the notes exist.
func ACPAgentPresets() []ACPAgentPreset {
	return []ACPAgentPreset{
		{
			ID:      "opencode",
			Title:   "OpenCode",
			Command: []string{"opencode", "acp"},
			Note:    "Native: `opencode acp` starts the ACP server. Verified with OpenCode 2.0.16 (initialize, session/new, and the loopback worker MCP bridge).",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "claude-code",
			Title:   "Claude Code",
			Command: []string{"claude-agent-acp"},
			Note:    "Claude Code has no `acp` subcommand; install the adapter with `npm i -g @zed-industries/claude-agent-acp`.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "codex",
			Title:   "Codex",
			Command: []string{"codex-acp"},
			Note:    "Codex has no `acp` subcommand; install the adapter with `npm i -g @zed-industries/codex-acp`. Verified with codex-acp 0.16.0 (initialize, session/new, and the loopback worker MCP handshake).",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "gemini",
			Title:   "Gemini CLI",
			Command: []string{"gemini", "--acp"},
			Note:    "Native. Older releases only accept the deprecated `--experimental-acp`.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "qwen-code",
			Title:   "Qwen Code",
			Command: []string{"qwen", "--acp"},
			Note:    "Native. Older releases only accept the deprecated `--experimental-acp`.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "copilot",
			Title:   "GitHub Copilot CLI",
			Command: []string{"copilot", "--acp", "--stdio"},
			Note:    "Native public preview: `copilot --acp` starts ACP over stdio.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "cursor",
			Title:   "Cursor Agent",
			Command: []string{"agent", "acp"},
			Note:    "Native. The CLI binary is `agent` (also shipped as `cursor-agent`).",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "kiro",
			Title:   "Kiro CLI",
			Command: []string{"kiro-cli", "acp"},
			Note:    "Native: `kiro-cli acp`.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
		{
			ID:      "openclaw",
			Title:   "OpenClaw",
			Command: []string{"openclaw", "acp"},
			Note:    "Native: `openclaw acp`.",
			Driver:  workerdriver.WorkerDriverGenericACP,
		},
	}
}

// FindACPAgentPreset returns the preset whose ID matches, case-insensitively.
func FindACPAgentPreset(id string) (ACPAgentPreset, bool) {
	id = strings.TrimSpace(id)
	for _, preset := range ACPAgentPresets() {
		if strings.EqualFold(preset.ID, id) {
			return preset, true
		}
	}
	return ACPAgentPreset{}, false
}

// ACPAgentProfileFromPreset expands a preset into a ready-to-edit profile with a
// non-blocking approval policy and enabled state. Role and write scope are
// supplied per delegation, never stored on the profile.
func ACPAgentProfileFromPreset(preset ACPAgentPreset) config.ACPAgentProfile {
	return config.ACPAgentProfile{
		ID:               preset.ID,
		Title:            preset.Title,
		Command:          append([]string(nil), preset.Command...),
		Enabled:          true,
		Driver:           string(preset.Driver),
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
}
