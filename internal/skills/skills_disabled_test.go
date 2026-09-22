package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// writeDisabledSkillFile persists a sparse global settings file carrying the
// additive skills.disabled list.
func writeDisabledSkillFile(t *testing.T, configDir string, disabled []string) {
	t.Helper()
	updates := map[string]any{}
	if len(disabled) == 0 {
		updates["skills"] = nil
	} else {
		updates["skills"] = map[string]any{"disabled": disabled}
	}
	if err := config.SaveGlobalSettingsPatch(updates); err != nil {
		t.Fatal(err)
	}
}

func seedDisabledTestSkills(t *testing.T, globalDir, projectDir string) {
	t.Helper()
	for dir, name := range map[string]string{globalDir: "gen-skill", projectDir: "proj-skill"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name+"\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadAppliesGlobalDisabledSkills(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	globalDir := filepath.Join(t.TempDir(), "global")
	projectDir := filepath.Join(t.TempDir(), "project")
	seedDisabledTestSkills(t, globalDir, projectDir)
	writeDisabledSkillFile(t, configDir, []string{"gen-skill"})

	manager := NewManager(globalDir, projectDir)
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if !manager.IsSkillDisabled("gen-skill") || manager.IsSkillDisabled("proj-skill") {
		t.Fatalf("disabled set = %#v", manager.DisabledSkills())
	}
	if got := manager.List(); len(got) != 3 || manager.Get("proj-skill") == nil || manager.Get(ExpertCreaterSkillName) == nil || manager.Get("vibe-browser") == nil {
		t.Fatalf("List must filter disabled skills: %#v", got)
	}
	if got := manager.ListBySource("global"); len(got) != 0 {
		t.Fatalf("ListBySource must filter disabled skills: %#v", got)
	}
	if got := manager.Names(); len(got) != 3 || got[0] != ExpertCreaterSkillName || got[1] != "proj-skill" || got[2] != "vibe-browser" {
		t.Fatalf("Names must filter disabled skills: %#v", got)
	}
	if manager.Get("gen-skill") != nil {
		t.Fatal("Get must hide disabled skills")
	}
	if manager.Get("proj-skill") == nil {
		t.Fatal("Get must keep enabled skills")
	}
	if got := manager.ListAll(); len(got) != 4 {
		t.Fatalf("ListAll must keep disabled skills: %#v", got)
	}
	if context := manager.BuildSkillContext("gen-skill"); context != "" {
		t.Fatalf("disabled skill must not build prompt context: %q", context)
	}
	listing := manager.BuildAllSkillsContext()
	if strings.Contains(listing, "gen-skill") || !strings.Contains(listing, "proj-skill") {
		t.Fatalf("skills listing must filter disabled skills: %q", listing)
	}
	if content, ok := manager.LoadReference("gen-skill", "references/x.md"); ok || content != "" {
		t.Fatal("disabled skill references must stay unreachable")
	}
	if refs := manager.ListReferences("gen-skill"); refs != nil {
		t.Fatalf("disabled skill references = %#v", refs)
	}
}

func TestSetDisabledSkillsLiveUpdate(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	globalDir := filepath.Join(t.TempDir(), "global")
	seedDisabledTestSkills(t, globalDir, filepath.Join(t.TempDir(), "empty-project"))

	manager := NewManager(globalDir, "")
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if len(manager.List()) != 3 {
		t.Fatalf("baseline skills = %#v", manager.List())
	}

	manager.SetDisabledSkills([]string{" gen-skill ", ""})
	if !manager.IsSkillDisabled("gen-skill") {
		t.Fatal("SetDisabledSkills must trim and apply names")
	}
	if len(manager.List()) != 2 || manager.Get("gen-skill") != nil || manager.Get(ExpertCreaterSkillName) == nil || manager.Get("vibe-browser") == nil {
		t.Fatal("live disable must hide the skill immediately")
	}
	if got := manager.DisabledSkills(); len(got) != 1 || got[0] != "gen-skill" {
		t.Fatalf("DisabledSkills = %#v", got)
	}

	manager.SetDisabledSkills(nil)
	if manager.IsSkillDisabled("gen-skill") || manager.Get("gen-skill") == nil {
		t.Fatal("SetDisabledSkills(nil) must re-enable every skill")
	}
	if got := manager.DisabledSkills(); got != nil {
		t.Fatalf("empty DisabledSkills = %#v, want nil", got)
	}
}

func TestLoadWithoutDisabledSectionKeepsEverythingEnabled(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	globalDir := filepath.Join(t.TempDir(), "global")
	seedDisabledTestSkills(t, globalDir, filepath.Join(t.TempDir(), "empty-project"))
	// A settings file without the skills section must not change behavior.
	if err := config.SaveGlobalSettingsPatch(map[string]any{"theme": "light"}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(globalDir, "")
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	if len(manager.List()) != 3 || manager.Get("gen-skill") == nil || manager.Get(ExpertCreaterSkillName) == nil || manager.Get("vibe-browser") == nil {
		t.Fatal("absent skills section must keep every skill enabled")
	}
	data, err := os.ReadFile(config.GlobalSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["skills"]; ok {
		t.Fatalf("sparse settings must not grow a skills section: %s", data)
	}
}
