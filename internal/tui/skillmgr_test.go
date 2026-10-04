package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

func writeTestSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# " + name + "\n\n" + desc + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func newSkillMgrApp(t *testing.T, names ...string) *App {
	t.Helper()
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, ".skills")
	for _, name := range names {
		writeTestSkill(t, skillsDir, name, name+" skill")
	}
	mgr := skills.NewManagerWithProjectDirs("", skills.ProjectSkillDirs(tmpDir))
	if err := mgr.Load(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	for _, name := range names {
		if mgr.Get(name) == nil {
			t.Fatalf("test skill %q did not load", name)
		}
	}
	registry := tools.NewRegistry(tmpDir, nil)
	app := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, registry, "", "", "", mgr, "agent", false, false, nil, nil, nil)
	app.cwd = tmpDir
	app.width, app.height = 100, 40
	return app
}

func skillMgrCursorFor(a *App, name string) int {
	for i, s := range a.skillMgrItems {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// TestSkillMgrPanelSpaceTogglesActivation covers the merged /skillmgr panel:
// Space toggles a skill under the cursor, Enter applies the pending set through
// the existing activation path (activeSkills -> extraContext), and reopening
// reflects the active state so a skill can be deactivated the same way.
func TestSkillMgrPanelSpaceTogglesActivation(t *testing.T) {
	app := newSkillMgrApp(t, "alpha", "beta")

	app.openSkillMgr()
	if !app.skillMgrOpen {
		t.Fatal("panel should be open")
	}
	alphaIdx := skillMgrCursorFor(app, "alpha")
	betaIdx := skillMgrCursorFor(app, "beta")
	if alphaIdx < 0 || betaIdx < 0 {
		t.Fatalf("alpha/beta not listed among %d items", len(app.skillMgrItems))
	}
	if app.skillMgrSelected["alpha"] || app.skillMgrSelected["beta"] {
		t.Fatal("no skill should start selected")
	}

	// Space toggles the skill under the cursor on, then off.
	app.skillMgrCursor = alphaIdx
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	if !app.skillMgrSelected["alpha"] {
		t.Fatal("space should select alpha")
	}
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	if app.skillMgrSelected["alpha"] {
		t.Fatal("second space should deselect alpha")
	}

	// Select alpha + beta and apply.
	app.skillMgrCursor = alphaIdx
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	app.skillMgrCursor = betaIdx
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeyEnter})

	if app.skillMgrOpen {
		t.Fatal("panel should close after apply")
	}
	if _, ok := app.activeSkills["alpha"]; !ok {
		t.Fatal("alpha should be active after apply")
	}
	if _, ok := app.activeSkills["beta"]; !ok {
		t.Fatal("beta should be active after apply")
	}
	if !strings.Contains(app.extraContext, "Active Skill: alpha") || !strings.Contains(app.extraContext, "Active Skill: beta") {
		t.Fatalf("extraContext should include both activated skills:\n%s", app.extraContext)
	}

	// Reopen: active skills come back pre-selected; deselect alpha and apply.
	app.openSkillMgr()
	if !app.skillMgrSelected["alpha"] || !app.skillMgrSelected["beta"] {
		t.Fatal("active skills should reopen selected")
	}
	app.skillMgrCursor = skillMgrCursorFor(app, "alpha")
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeyEnter})

	if _, ok := app.activeSkills["alpha"]; ok {
		t.Fatal("alpha should be deactivated")
	}
	if _, ok := app.activeSkills["beta"]; !ok {
		t.Fatal("beta should remain active")
	}
	if strings.Contains(app.extraContext, "Active Skill: alpha") {
		t.Fatalf("extraContext should drop the deactivated skill:\n%s", app.extraContext)
	}
	if !strings.Contains(app.extraContext, "Active Skill: beta") {
		t.Fatalf("extraContext should keep beta:\n%s", app.extraContext)
	}
}

// TestSkillMgrEscClosesWithoutApplying ensures the pending selection is discarded
// on Esc/q so closing the panel never mutates the active skill set.
func TestSkillMgrEscClosesWithoutApplying(t *testing.T) {
	app := newSkillMgrApp(t, "gamma")

	app.openSkillMgr()
	idx := skillMgrCursorFor(app, "gamma")
	if idx < 0 {
		t.Fatal("gamma not listed")
	}
	app.skillMgrCursor = idx
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	if !app.skillMgrSelected["gamma"] {
		t.Fatal("space should select gamma")
	}
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeyEsc})
	if app.skillMgrOpen {
		t.Fatal("esc should close the panel")
	}
	if _, ok := app.activeSkills["gamma"]; ok {
		t.Fatal("esc must not apply the pending selection")
	}
	if strings.Contains(app.extraContext, "Active Skill: gamma") {
		t.Fatalf("extraContext must be unchanged after esc:\n%s", app.extraContext)
	}
}

// TestSkillMgrRenderShowsCheckboxAndCursor is a smoke check that the panel
// renders the checkbox/cursor chrome and the active-count footer.
func TestSkillMgrRenderShowsCheckboxAndCursor(t *testing.T) {
	app := newSkillMgrApp(t, "alpha", "beta")
	app.openSkillMgr()
	app.skillMgrCursor = skillMgrCursorFor(app, "alpha")
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})

	out := app.renderSkillMgr()
	if !strings.Contains(out, "Skill Manager") {
		t.Fatalf("render missing title:\n%s", out)
	}
	if !strings.Contains(out, "[x]") || !strings.Contains(out, "[ ]") {
		t.Fatalf("render should show checked and unchecked boxes:\n%s", out)
	}
	wantFooter := fmt.Sprintf("1/%d active", len(app.skillMgrItems))
	if !strings.Contains(out, wantFooter) {
		t.Fatalf("render missing active-count footer %q:\n%s", wantFooter, out)
	}
}
