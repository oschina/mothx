package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// newSkillRuntimeApp builds the production TUI path with real Runtime-owned
// skills so activation tests exercise the shared state rather than the
// pre-session draft.
func newSkillRuntimeApp(t *testing.T, skills ...string) (*App, *agentruntime.SessionRuntime, *session.Manager) {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	for _, name := range skills {
		writeTestSkill(t, filepath.Join(workDir, ".skills"), name, name+" skill")
	}
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()

	manager, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (agentruntime.Builder{Settings: settings}).Build(context.Background(), agentruntime.BuildOptions{
		ID: manager.GetHeader().ID, Source: agentruntime.SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)
	t.Cleanup(func() { _ = session.CloseDatabases() })

	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", ContextWindow: 128000}}, nil)
	app := NewApp(p, p.Models()[0], settings, manager, runtime.Registry, "", runtime.ExtraContext, runtime.RuleContent,
		runtime.SkillsMgr, "yolo", false, false, nil, nil, nil)
	app.cwd = workDir
	app.width, app.height = 100, 40
	app.SetRuntime(runtime)
	return app, runtime, manager
}

// TestSkillMgrMidSessionActivationUsesRuntime covers activation in the middle
// of a session: the Runtime owns the set and the composed context, and the
// panel is only a projection.
func TestSkillMgrMidSessionActivationUsesRuntime(t *testing.T) {
	app, runtime, _ := newSkillRuntimeApp(t, "alpha")

	app.openSkillMgr()
	idx := skillMgrCursorFor(app, "alpha")
	if idx < 0 {
		t.Fatal("alpha not listed")
	}
	app.skillMgrCursor = idx
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeyEnter})

	if got := runtime.ActiveSkillsSnapshot(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("runtime active skills = %#v, want [alpha]", got)
	}
	if !strings.Contains(app.extraContext, "## Active Skill: alpha") {
		t.Fatalf("TUI context not projected from Runtime:\n%s", app.extraContext)
	}
	if len(app.activeSkills) != 1 || !app.activeSkills["alpha"] {
		t.Fatalf("TUI active projection = %#v, want alpha", app.activeSkills)
	}

	// A browser capability refresh is a Runtime resource refresh with a nil
	// active set; it must preserve the user's activation.
	app.handleCommand("/browser on")
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("browser refresh dropped active skills: %#v", got)
	}
	if !strings.Contains(runtime.ExtraContext, "## Active Skill: alpha") {
		t.Fatalf("browser refresh dropped active context:\n%s", runtime.ExtraContext)
	}
	if strings.Count(runtime.ExtraContext, "## Active Skill: alpha") != 1 {
		t.Fatalf("active skill context duplicated after refresh:\n%s", runtime.ExtraContext)
	}
}

// TestSkillMgrSessionSwitchDropsStaleActivation covers the reported
// mid-session divergence: switching sessions must clear Runtime-owned active
// skills and the TUI projection instead of leaving phantom checkboxes.
func TestSkillMgrSessionSwitchDropsStaleActivation(t *testing.T) {
	app, runtime, _ := newSkillRuntimeApp(t, "alpha")
	if err := runtime.SetActiveSkills([]string{"alpha"}); err != nil {
		t.Fatalf("activate alpha: %v", err)
	}
	app.adoptRuntimeResources()

	next, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: app.cwd, SessionDir: app.settings.SessionDir,
	})
	if err != nil {
		t.Fatalf("create next session: %v", err)
	}
	if err := app.activateSession(next); err != nil {
		t.Fatalf("switch session: %v", err)
	}

	if got := runtime.ActiveSkillsSnapshot(); len(got) != 0 {
		t.Fatalf("switched session kept active skills: %#v", got)
	}
	if len(app.activeSkills) != 0 {
		t.Fatalf("TUI projection kept stale skills: %#v", app.activeSkills)
	}
	if strings.Contains(app.extraContext, "## Active Skill: alpha") {
		t.Fatalf("switched session kept stale active context:\n%s", app.extraContext)
	}

	app.openSkillMgr()
	if app.skillMgrSelected["alpha"] {
		t.Fatal("panel seeded a stale active skill after session switch")
	}
}

// TestSkillMgrPreSessionActivationTransfersToRuntime covers activation before
// the first session, when no Runtime exists yet.
func TestSkillMgrPreSessionActivationTransfersToRuntime(t *testing.T) {
	app := newSkillMgrApp(t, "alpha")
	app.settings.SessionDir = t.TempDir()
	app.openSkillMgr()
	app.skillMgrCursor = skillMgrCursorFor(app, "alpha")
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	app.handleSkillMgrKey(tea.KeyMsg{Type: tea.KeyEnter})

	if app.runtime != nil {
		t.Fatal("test must start without a runtime")
	}
	if !strings.Contains(app.extraContext, "## Active Skill: alpha") {
		t.Fatalf("pre-session context missing draft skill:\n%s", app.extraContext)
	}

	sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: app.cwd, SessionDir: app.settings.SessionDir,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := app.activateSession(sess); err != nil {
		t.Fatalf("activate session: %v", err)
	}
	if err := app.ensureRuntime(); err != nil {
		t.Fatalf("ensure runtime: %v", err)
	}
	t.Cleanup(app.runtime.Close)
	t.Cleanup(func() { _ = session.CloseDatabases() })

	if got := app.runtime.ActiveSkillsSnapshot(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("runtime did not adopt pre-session draft: %#v", got)
	}
	if !strings.Contains(app.extraContext, "## Active Skill: alpha") {
		t.Fatalf("runtime context did not adopt pre-session draft:\n%s", app.extraContext)
	}
}
