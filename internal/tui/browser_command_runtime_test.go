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

// newBrowserRuntimeApp builds the production TUI path: a real Runtime bound to
// a persisted session.
func newBrowserRuntimeApp(t *testing.T) *App {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()

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

func TestBrowserCommandUpdatesRuntimeStateAndSkill(t *testing.T) {
	app := newBrowserRuntimeApp(t)
	app.handleCommand("/browser on")

	if _, ok := app.registry.Get("browser"); !ok {
		t.Fatal("registry has no browser tool")
	}
	_, browserEnabled, _ := app.runtime.CapabilitySnapshot()
	if !browserEnabled {
		t.Fatal("runtime browser capability is false after /browser on")
	}
	if !strings.Contains(app.extraContext, "Vibe Browser") {
		t.Fatalf("browser skill missing from agent context:\n%s", app.extraContext)
	}
}

// TestBrowserCommandSurvivesFirstSessionBind mirrors the production TUI flow: the
// runtime is built without a session, the user runs /browser on, and the first
// prompt creates + binds a session.
func TestBrowserCommandSurvivesFirstSessionBind(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()

	runtime, err := (agentruntime.Builder{Settings: settings}).Build(context.Background(), agentruntime.BuildOptions{
		Source: agentruntime.SourceTUI, WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model", ContextWindow: 128000}}, nil)
	app := NewApp(p, p.Models()[0], settings, nil, runtime.Registry, "", runtime.ExtraContext, runtime.RuleContent,
		runtime.SkillsMgr, "yolo", false, false, nil, nil, nil)
	app.SetRuntime(runtime)
	app.cwd = workDir

	app.handleCommand("/browser on")
	if _, ok := app.registry.Get("browser"); !ok {
		t.Fatal("registry has no browser tool after /browser on")
	}

	// First prompt: the TUI creates the session and binds it to the Runtime.
	sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := app.activateSession(sess); err != nil {
		t.Fatalf("activate session: %v", err)
	}

	if _, ok := app.registry.Get("browser"); !ok {
		t.Fatal("browser tool was removed from the shared registry by the session bind")
	}
	_, browserEnabled, _ := app.runtime.CapabilitySnapshot()
	if !browserEnabled {
		t.Fatal("browser capability was reset by the session bind")
	}
	caps, ok, err := session.LoadSessionCapabilities(sessionDir, sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load persisted capabilities: %v", err)
	}
	if !ok || !caps.Browser {
		t.Fatalf("persisted browser capability = %#v, want enabled", caps)
	}
}
