package config

import (
	"os"
	"strings"
	"testing"
)

func acpProfileFixture() ACPAgentProfile {
	return ACPAgentProfile{
		ID:               "codex",
		Title:            "Codex",
		Command:          []string{"/opt/codex/bin/codex", "acp"},
		Enabled:          true,
		Environment:      []string{"OPENAI_API_KEY"},
		PermissionPolicy: ACPPermissionAutoAllowOnce,
		Roles:            []ACPAgentRole{ACPRoleInvestigate, ACPRoleImplement},
		WriteRoots:       []string{"src"},
	}
}

func TestSaveACPAgentProfilesRejectsInvalidSetBeforeWriting(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	// Seed an unrelated key so a rejected write can be proven to have changed
	// nothing at all.
	if err := SaveGlobalSettingsPatch(map[string]any{"defaultMode": "plan"}); err != nil {
		t.Fatal(err)
	}
	path := GlobalSettingsPath()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	duplicate := []ACPAgentProfile{acpProfileFixture(), acpProfileFixture()}
	if err := SaveACPAgentProfiles(duplicate); err == nil {
		t.Fatal("duplicate profile IDs were persisted")
	}
	emptyCommand := acpProfileFixture()
	emptyCommand.ID = "helper"
	emptyCommand.Command = []string{"  "}
	if err := SaveACPAgentProfiles([]ACPAgentProfile{emptyCommand}); err == nil {
		t.Fatal("an empty command[0] was persisted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected profile set modified settings.json:\nbefore: %s\nafter: %s", before, after)
	}
	if strings.Contains(string(after), "acpAgents") {
		t.Fatal("a rejected profile set wrote an acpAgents key")
	}
}

func TestSaveACPAgentProfilesPersistsNormalizedSparseList(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	if err := SaveGlobalSettingsPatch(map[string]any{"defaultMode": "plan"}); err != nil {
		t.Fatal(err)
	}

	profile := acpProfileFixture()
	profile.ID = "  codex  "
	profile.Model = "  fast-model  "
	profile.Command = []string{" /opt/codex/bin/codex ", " acp "}
	profile.Environment = []string{" OPENAI_API_KEY "}
	profile.WriteRoots = []string{" src "}
	if err := SaveACPAgentProfiles([]ACPAgentProfile{profile}); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ACPAgents) != 1 {
		t.Fatalf("loaded profiles = %#v", loaded.ACPAgents)
	}
	stored := loaded.ACPAgents[0]
	if stored.ID != "codex" || stored.Command[0] != "/opt/codex/bin/codex" || stored.Command[1] != "acp" {
		t.Fatalf("stored profile was not normalized: %#v", stored)
	}
	if stored.Model != "fast-model" || stored.EffectiveModel() != "fast-model" {
		t.Fatalf("stored model = %q, want the trimmed value", stored.Model)
	}
	if len(stored.Environment) != 1 || stored.Environment[0] != "OPENAI_API_KEY" {
		t.Fatalf("stored environment = %#v", stored.Environment)
	}
	if len(stored.WriteRoots) != 1 || stored.WriteRoots[0] != "src" {
		t.Fatalf("stored writeRoots = %#v", stored.WriteRoots)
	}
	// The unrelated key survives, proving the write stayed sparse.
	if loaded.DefaultMode != "plan" {
		t.Fatalf("defaultMode = %q, want the pre-existing value", loaded.DefaultMode)
	}
	if _, ok := loaded.ACPAgentProfile("codex"); !ok {
		t.Fatal("the saved profile is not resolvable through the Runtime-facing accessor")
	}
}

func TestSaveACPAgentProfilesRejectsInvalidModel(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	profile := acpProfileFixture()
	profile.Model = "bad\nmodel"
	if err := SaveACPAgentProfiles([]ACPAgentProfile{profile}); err == nil {
		t.Fatal("a model containing a control character was persisted")
	}
	// Empty means the worker keeps its own default and is always valid.
	profile.Model = "   "
	if err := SaveACPAgentProfiles([]ACPAgentProfile{profile}); err != nil {
		t.Fatalf("an empty model should mean the worker default: %v", err)
	}
}

func TestSaveACPAgentProfilesAcceptsLegacyRoleWithoutWriteRoots(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	profile := acpProfileFixture()
	profile.Roles = []ACPAgentRole{ACPRoleImplement, "deploy"}
	profile.WriteRoots = nil
	if err := SaveACPAgentProfiles([]ACPAgentProfile{profile}); err != nil {
		t.Fatalf("legacy role fields are still treated as authorization: %v", err)
	}
	loaded, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := loaded.ACPAgentProfile("codex")
	if !ok {
		t.Fatal("the profile is not resolvable")
	}
	if len(stored.Roles) != 2 || stored.Roles[0] != ACPRoleImplement || stored.Roles[1] != "deploy" {
		t.Fatalf("legacy roles were not preserved: %#v", stored.Roles)
	}
	if len(stored.WriteRoots) != 0 {
		t.Fatalf("legacy writeRoots = %#v, want empty", stored.WriteRoots)
	}
}

func TestSaveACPAgentProfilesAcceptsMothxSelfACPTarget(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	profile := ACPAgentProfile{
		ID: "mothx", Command: []string{"mothx", "acp"}, Enabled: true,
		PermissionPolicy: ACPPermissionAutoAllowOnce,
	}
	if err := SaveACPAgentProfiles([]ACPAgentProfile{profile}); err != nil {
		t.Fatalf("MothX self-ACP profile was rejected: %v", err)
	}
	loaded, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := loaded.ACPAgentProfile("mothx")
	if !ok {
		t.Fatal("MothX self-ACP profile is not resolvable")
	}
	if len(stored.Roles) != 0 || len(stored.WriteRoots) != 0 {
		t.Fatalf("self-ACP profile unexpectedly gained legacy authorization fields: %#v", stored)
	}
}

func TestSaveACPAgentProfilesEmptyListClearsTheKey(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	if err := SaveACPAgentProfiles([]ACPAgentProfile{acpProfileFixture()}); err != nil {
		t.Fatal(err)
	}
	if err := SaveACPAgentProfiles(nil); err != nil {
		t.Fatalf("clear profiles: %v", err)
	}
	raw, err := os.ReadFile(GlobalSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"acpAgents"`) {
		t.Fatalf("clearing profiles left an acpAgents key: %s", raw)
	}
	loaded, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ACPAgents) != 0 {
		t.Fatalf("loaded profiles after clear = %#v", loaded.ACPAgents)
	}
}
