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
	relativeCommand := acpProfileFixture()
	relativeCommand.ID = "helper"
	relativeCommand.Command = []string{"codex", "acp"}
	if err := SaveACPAgentProfiles([]ACPAgentProfile{relativeCommand}); err == nil {
		t.Fatal("a relative command[0] was persisted")
	}
	writeRoleWithoutRoots := acpProfileFixture()
	writeRoleWithoutRoots.WriteRoots = nil
	if err := SaveACPAgentProfiles([]ACPAgentProfile{writeRoleWithoutRoots}); err == nil {
		t.Fatal("a write role without writeRoots was persisted")
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
