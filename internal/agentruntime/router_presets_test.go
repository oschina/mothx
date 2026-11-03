package agentruntime

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// Every built-in preset must be valid configuration. A preset that the config
// validator rejects would be a recipe for a failed save.
func TestACPAgentPresetsAreValidProfiles(t *testing.T) {
	presets := ACPAgentPresets()
	if len(presets) == 0 {
		t.Fatal("no presets defined")
	}
	ids := map[string]bool{}
	for _, preset := range presets {
		t.Run(preset.ID, func(t *testing.T) {
			if strings.TrimSpace(preset.ID) == "" || strings.TrimSpace(preset.Title) == "" {
				t.Fatalf("preset is missing an id or title: %#v", preset)
			}
			if ids[preset.ID] {
				t.Fatalf("duplicate preset id %q", preset.ID)
			}
			ids[preset.ID] = true
			if len(preset.Command) == 0 {
				t.Fatal("preset has no command")
			}
			profile := ACPAgentProfileFromPreset(preset)
			if err := profile.Validate(); err != nil {
				t.Fatalf("preset expands to an invalid profile: %v", err)
			}
			if err := config.ValidateACPAgentProfileSet([]config.ACPAgentProfile{profile}); err != nil {
				t.Fatalf("preset fails the shared save validator: %v", err)
			}
		})
	}
}

// A preset must never be rejected as "not eligible": these are the entries the
// picker offers, so a disabled or interactive default would be a dead option.
func TestACPAgentPresetsAreRouterEligible(t *testing.T) {
	settings := config.DefaultSettings()
	for _, preset := range ACPAgentPresets() {
		settings.ACPAgents = append(settings.ACPAgents, ACPAgentProfileFromPreset(preset))
	}
	byID := map[string]ACPWorkerProfileDescriptor{}
	for _, descriptor := range ListACPWorkerProfiles(settings) {
		byID[descriptor.ID] = descriptor
	}
	for _, preset := range ACPAgentPresets() {
		descriptor, ok := byID[preset.ID]
		if !ok {
			t.Fatalf("preset %q missing from the catalog", preset.ID)
		}
		if !descriptor.RouterEligible {
			t.Fatalf("preset %q is not Router-eligible: %#v", preset.ID, descriptor)
		}
		if descriptor.InvalidReason != "" {
			t.Fatalf("preset %q is invalid: %s", preset.ID, descriptor.InvalidReason)
		}
	}
}

// The presets exist because several popular CLIs do not start an ACP server
// themselves. Lock that knowledge in, so a future edit cannot reintroduce the
// `codex acp` / `claude acp` mistake.
func TestACPAgentPresetsAvoidNonACPSelfServerCommands(t *testing.T) {
	nonACP := map[string]bool{
		// These binaries treat `acp` as a prompt/unknown subcommand rather than
		// starting an ACP server, so the adapter binary is required instead.
		"codex acp":  true,
		"claude acp": true,
	}
	for _, preset := range ACPAgentPresets() {
		joined := strings.Join(preset.Command, " ")
		if nonACP[joined] {
			t.Fatalf("preset %q uses %q, which does not start an ACP server", preset.ID, joined)
		}
	}
}

// The lookup is what the panel uses to jump straight to a preset.
func TestFindACPAgentPreset(t *testing.T) {
	preset, ok := FindACPAgentPreset("opencode")
	if !ok {
		t.Fatal("opencode preset not found")
	}
	if strings.Join(preset.Command, " ") != "opencode acp" {
		t.Fatalf("opencode command = %#v", preset.Command)
	}
	if _, ok := FindACPAgentPreset("OPENCODE"); !ok {
		t.Fatal("preset lookup is not case-insensitive")
	}
	if _, ok := FindACPAgentPreset("definitely-not-an-agent"); ok {
		t.Fatal("an unknown id resolved to a preset")
	}
}

// Every preset must state its launch caveat: the note is the only place a user
// learns that an adapter install is required.
func TestACPAgentPresetsDocumentInstallNotes(t *testing.T) {
	for _, preset := range ACPAgentPresets() {
		if strings.TrimSpace(preset.Note) == "" {
			t.Fatalf("preset %q has no note explaining how to launch it", preset.ID)
		}
	}
}

// The preset must not alias the shared slice: editing a draft profile must never
// mutate the built-in recipe.
func TestACPAgentProfileFromPresetCopiesCommand(t *testing.T) {
	preset, ok := FindACPAgentPreset("opencode")
	if !ok {
		t.Fatal("opencode preset not found")
	}
	profile := ACPAgentProfileFromPreset(preset)
	profile.Command[0] = "mutated"
	fresh, _ := FindACPAgentPreset("opencode")
	if fresh.Command[0] != "opencode" {
		t.Fatalf("preset command was mutated through the profile: %#v", fresh.Command)
	}
}
