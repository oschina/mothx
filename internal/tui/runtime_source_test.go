package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func TestTUIRuntimeUsesAuthoritativeSourceAndMode(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	mgr := session.New(workDir, sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(workDir, nil)
	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), mgr, registry, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.SetRuntime(tuiRuntime(mgr, registry, "", "", "", nil, app.settings))
	mode, err := app.effectiveRuntimeMode()
	if err != nil {
		t.Fatal(err)
	}
	if mode != agentruntime.ModeAgent {
		t.Fatalf("mode = %q, want agent", mode)
	}
}

func TestTUIDefaultModeIsYoloWhenInitialModeIsEmpty(t *testing.T) {
	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), nil, tools.NewRegistry(t.TempDir(), nil), "", "", "", nil, "", false, false, nil, nil, nil)
	if app.mode != agentruntime.ModeYolo {
		t.Fatalf("default TUI mode = %q, want %q", app.mode, agentruntime.ModeYolo)
	}
}
