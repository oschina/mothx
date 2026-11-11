package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
)

func writeRuntimeTestSkill(t *testing.T, workDir, name, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# " + name + "\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func newActiveSkillsRuntime(t *testing.T, workDir string, manager *session.Manager) *SessionRuntime {
	t.Helper()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

// TestSetActiveSkillsOwnsAndPreservesContext covers the mid-session contract:
// the Runtime owns the set, internal resource refreshes preserve it, explicit
// refresh with a non-nil set replaces it, and clearing removes its prompt text.
func TestSetActiveSkillsOwnsAndPreservesContext(t *testing.T) {
	workDir := t.TempDir()
	writeRuntimeTestSkill(t, workDir, "alpha", "ALPHA-BODY")
	writeRuntimeTestSkill(t, workDir, "beta", "BETA-BODY")
	runtime := newActiveSkillsRuntime(t, workDir, nil)

	if err := runtime.SetActiveSkills([]string{"beta", "alpha"}); err != nil {
		t.Fatalf("SetActiveSkills: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("active snapshot = %#v, want sorted [alpha beta]", got)
	}
	if !strings.Contains(runtime.ExtraContext, "## Active Skill: alpha") ||
		!strings.Contains(runtime.ExtraContext, "## Active Skill: beta") {
		t.Fatalf("active skills missing from context:\n%s", runtime.ExtraContext)
	}
	if strings.Index(runtime.ExtraContext, "## Active Skill: alpha") > strings.Index(runtime.ExtraContext, "## Active Skill: beta") {
		t.Fatal("active skill context is not deterministically ordered")
	}

	// Expert/browser/session-style internal refreshes pass nil and must preserve.
	refreshSettings := config.DefaultSettings()
	refreshSettings.ContextFiles.Enabled = false
	if err := runtime.RefreshResources(refreshSettings, RefreshOptions{}); err != nil {
		t.Fatalf("preserving refresh: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 2 {
		t.Fatalf("internal refresh dropped active skills: %#v", got)
	}
	if !strings.Contains(runtime.ExtraContext, "ALPHA-BODY") {
		t.Fatalf("internal refresh lost active context:\n%s", runtime.ExtraContext)
	}

	// Explicit empty set clears.
	if err := runtime.SetActiveSkills(nil); err != nil {
		t.Fatalf("clear active skills: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 0 {
		t.Fatalf("active snapshot after clear = %#v, want empty", got)
	}
	if strings.Contains(runtime.ExtraContext, "## Active Skill: alpha") || strings.Contains(runtime.ExtraContext, "## Active Skill: beta") {
		t.Fatalf("active context remained after clear:\n%s", runtime.ExtraContext)
	}

	// A strict failure must leave the previous set in place.
	if err := runtime.SetActiveSkills([]string{"alpha", "missing"}); err == nil || !strings.Contains(err.Error(), "skill not found") {
		t.Fatalf("unknown skill error = %v, want skill not found", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 0 {
		t.Fatalf("failed activation changed state: %#v", got)
	}
}

// TestBindSessionScopesActiveSkills checks that switching session identity drops
// the previous session's activation set while a same-session rebind preserves it.
func TestBindSessionScopesActiveSkills(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	writeRuntimeTestSkill(t, workDir, "alpha", "ALPHA-BODY")

	current := session.New(workDir, sessionDir)
	if err := current.Init(); err != nil {
		t.Fatalf("init current session: %v", err)
	}
	next := session.New(workDir, sessionDir)
	if err := next.Init(); err != nil {
		t.Fatalf("init next session: %v", err)
	}
	runtime := newActiveSkillsRuntime(t, workDir, current)
	t.Cleanup(func() { _ = session.CloseDatabases() })
	if err := runtime.SetActiveSkills([]string{"alpha"}); err != nil {
		t.Fatalf("SetActiveSkills: %v", err)
	}
	if err := runtime.BindSession(current, SourceTUI); err != nil {
		t.Fatalf("same-session rebind: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("same-session rebind dropped active skills: %#v", got)
	}

	if err := runtime.BindSession(next, SourceTUI); err != nil {
		t.Fatalf("bind next session: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 0 {
		t.Fatalf("session switch kept stale active skills: %#v", got)
	}
	if strings.Contains(runtime.ExtraContext, "ALPHA-BODY") {
		t.Fatalf("session switch kept stale active context:\n%s", runtime.ExtraContext)
	}

	// Unbinding ends the session scope too; the next lazily-created session must
	// not inherit the activation set.
	if err := runtime.SetActiveSkills([]string{"alpha"}); err != nil {
		t.Fatalf("reactivate alpha: %v", err)
	}
	if err := runtime.UnbindSession(); err != nil {
		t.Fatalf("unbind session: %v", err)
	}
	if got := runtime.ActiveSkillsSnapshot(); len(got) != 0 {
		t.Fatalf("unbind kept active skills: %#v", got)
	}
	if strings.Contains(runtime.ExtraContext, "ALPHA-BODY") {
		t.Fatalf("unbind kept active context:\n%s", runtime.ExtraContext)
	}
}
