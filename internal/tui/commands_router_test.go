package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// newRouterCommandApp builds the production command path: a real Runtime bound
// to a persisted session, with one eligible ACP worker profile.
func newRouterCommandApp(t *testing.T) *App {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}

	manager, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: workDir, SessionDir: sessionDir,
	})
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

	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", ContextWindow: 128000}}, nil)
	app := NewApp(p, p.Models()[0], settings, manager, runtime.Registry, "", runtime.ExtraContext, runtime.RuleContent,
		runtime.SkillsMgr, "yolo", false, false, nil, nil, nil)
	app.SetRuntime(runtime)
	return app
}

func TestRouterCommandActivatesRuntimeBinding(t *testing.T) {
	app := newRouterCommandApp(t)
	app.handleCommand("/router codex")

	output := stripANSI(strings.Join(app.messages, "\n"))
	if app.mode != agentruntime.ModeRouter {
		t.Fatalf("TUI mode = %q, want router (output: %s)", app.mode, output)
	}
	binding, ok := app.runtime.RouterBinding()
	if !ok || binding.ACPProfileID != "codex" || binding.ApprovalStrategy != config.ACPPermissionAutoAllowOnce {
		t.Fatalf("binding = %#v, ok=%v (output: %s)", binding, ok, output)
	}
	if !strings.Contains(output, "codex") {
		t.Fatalf("activation status missing the worker: %s", output)
	}
}

func TestRouterCommandRejectsUnknownProfileAndBusySession(t *testing.T) {
	app := newRouterCommandApp(t)

	// The generic mode switch cannot select Router without activation.
	app.handleCommand("/mode router")
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("/mode router bypassed Router activation")
	}

	app.handleCommand("/router missing")
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("unknown profile activated Router mode")
	}
	if _, ok := app.runtime.RouterBinding(); ok {
		t.Fatal("unknown profile persisted a Router binding")
	}

	// A canonical execution admission (as another process would hold) must make
	// activation fail even when the adapter-local running flag is false.
	lease, err := session.AcquireExecutionAdmission(app.session.GetSessionDir(), app.session.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	app.handleCommand("/router codex")
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("Router activation with an active execution admission was accepted")
	}
	if _, ok := app.runtime.RouterBinding(); ok {
		t.Fatal("busy-session activation persisted a Router binding")
	}
}
