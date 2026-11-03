package agentruntime

import (
	"strings"

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
			Note:    "Native: `opencode acp` starts the ACP server.",
		},
		{
			ID:      "claude-code",
			Title:   "Claude Code",
			Command: []string{"claude-agent-acp"},
			Note:    "Claude Code has no `acp` subcommand; install the adapter with `npm i -g @zed-industries/claude-agent-acp`.",
		},
		{
			ID:      "codex",
			Title:   "Codex",
			Command: []string{"codex-acp"},
			Note:    "Codex has no `acp` subcommand; install the adapter with `npm i -g @zed-industries/codex-acp`.",
		},
		{
			ID:      "gemini",
			Title:   "Gemini CLI",
			Command: []string{"gemini", "--acp"},
			Note:    "Native. Older releases only accept the deprecated `--experimental-acp`.",
		},
		{
			ID:      "qwen-code",
			Title:   "Qwen Code",
			Command: []string{"qwen", "--acp"},
			Note:    "Native. Older releases only accept the deprecated `--experimental-acp`.",
		},
		{
			ID:      "copilot",
			Title:   "GitHub Copilot CLI",
			Command: []string{"copilot", "--acp", "--stdio"},
			Note:    "Native public preview: `copilot --acp` starts ACP over stdio.",
		},
		{
			ID:      "cursor",
			Title:   "Cursor Agent",
			Command: []string{"agent", "acp"},
			Note:    "Native. The CLI binary is `agent` (also shipped as `cursor-agent`).",
		},
		{
			ID:      "kiro",
			Title:   "Kiro CLI",
			Command: []string{"kiro-cli", "acp"},
			Note:    "Native: `kiro-cli acp`.",
		},
		{
			ID:      "openclaw",
			Title:   "OpenClaw",
			Command: []string{"openclaw", "acp"},
			Note:    "Native: `openclaw acp`.",
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

// ACPAgentProfileFromPreset expands a preset into a ready-to-edit profile with the
// safe defaults: investigate-only, non-blocking approval, enabled.
func ACPAgentProfileFromPreset(preset ACPAgentPreset) config.ACPAgentProfile {
	return config.ACPAgentProfile{
		ID:               preset.ID,
		Title:            preset.Title,
		Command:          append([]string(nil), preset.Command...),
		Enabled:          true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}
}
