package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// A profile written by an earlier build with maxConcurrentChildren=2 must keep
// working: this release accepts the value and enforces one live worker.
func TestStaleConcurrencyProfileStillResolves(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy:      config.ACPPermissionAutoAllowOnce,
		MaxConcurrentChildren: 2,
	}}
	runtime, err := (Builder{Settings: settings}).Build(t.Context(), BuildOptions{
		ID: manager.GetHeader().ID, Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)

	if err := runtime.SetRouterBinding(RouterBinding{
		ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce,
	}); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.Mode = ModeRouter
	runtime.mu.Unlock()

	if _, ok := runtime.RouterBinding(); !ok {
		t.Fatal("fixture lost its persisted binding")
	}
	if _, _, err := runtime.ResolveRouterACPProfile(settings); err != nil {
		t.Fatalf("a Router session whose profile was written by an earlier build can no longer resolve its worker: %v", err)
	}
}
