package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	}, {
		ID: "reviewer", Command: []string{"/opt/reviewer/bin/reviewer", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoReject,
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

// runRouterActivation executes the deferred Bubble Tea command and feeds its result
// back through Update, matching the real Bubble Tea loop.
func runRouterActivation(t *testing.T, app *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("Router activation returned no command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("Router activation command returned no message")
	}
	if _, ok := msg.(routerActivatedMsg); !ok {
		t.Fatalf("Router activation command returned %T, want routerActivatedMsg", msg)
	}
	app.Update(msg)
}

func TestRouterCommandActivatesRuntimeBinding(t *testing.T) {
	app := newRouterCommandApp(t)
	runRouterActivation(t, app, app.handleCommand("/router codex"))

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

func TestRouterCommandActivatesMultipleWorkers(t *testing.T) {
	app := newRouterCommandApp(t)
	runRouterActivation(t, app, app.handleCommand("/router codex,reviewer"))

	binding, ok := app.runtime.RouterBinding()
	if !ok {
		t.Fatal("multi-worker binding was not persisted")
	}
	if got := binding.ProfileIDs(); len(got) != 2 || got[0] != "codex" || got[1] != "reviewer" {
		t.Fatalf("bound profiles = %v", got)
	}
	if len(binding.Profiles) != 2 || binding.Profiles[1].ApprovalStrategy != config.ACPPermissionAutoReject {
		t.Fatalf("profiles = %#v", binding.Profiles)
	}
}

func TestRouterCommandRejectsUnknownProfileAndBusySession(t *testing.T) {
	app := newRouterCommandApp(t)

	// The generic mode switch cannot select Router without activation.
	app.handleCommand("/mode router")
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("/mode router bypassed Router activation")
	}

	runRouterActivation(t, app, app.handleCommand("/router missing"))
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
	runRouterActivation(t, app, app.handleCommand("/router codex"))
	if app.mode == agentruntime.ModeRouter {
		t.Fatal("Router activation with an active execution admission was accepted")
	}
	if _, ok := app.runtime.RouterBinding(); ok {
		t.Fatal("busy-session activation persisted a Router binding")
	}
}
