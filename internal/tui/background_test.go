package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	serviceruntime "github.com/oschina/mothx/internal/serve/runtime"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

type backgroundRuntimeProvider struct{ *recordingRuntimeProvider }

func (backgroundRuntimeProvider) ResponsesBackgroundEnabled() bool { return true }

func TestProcessInputUsesInjectedBackgroundSubmitter(t *testing.T) {
	workDir := t.TempDir()
	sess := session.New(workDir, workDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	base := &recordingRuntimeProvider{name: "test", models: []*provider.Model{{ID: "model-1"}}}
	app := NewApp(backgroundRuntimeProvider{base}, base.models[0], config.DefaultSettings(), sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)
	var got serviceruntime.BackgroundRequest
	app.SetBackgroundSubmitter(func(req serviceruntime.BackgroundRequest) (string, error) {
		got = req
		return "run-1", nil
	})

	cmd := app.processInput("run this later")
	if cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	msg, ok := cmd().(backgroundSubmittedMsg)
	if !ok {
		t.Fatalf("message type = %T, want backgroundSubmittedMsg", cmd())
	}
	if msg.Err != nil || msg.RunID != "run-1" {
		t.Fatalf("submitted message = %#v", msg)
	}
	if got.SessionID != sess.GetHeader().ID || got.Input.Text != "run this later" || got.ModelID != "model-1" || got.Platform != "tui" || got.RunID == "" {
		t.Fatalf("background request = %#v", got)
	}
	if base.callCount() != 0 {
		t.Fatalf("synchronous provider calls = %d, want 0", base.callCount())
	}
}

func TestPollBackgroundRunsReplaysCompletedAssistantMessage(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = workDir
	sess := session.New(workDir, workDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	base := &recordingRuntimeProvider{name: "test", models: []*provider.Model{{ID: "model-1"}}}
	app := NewApp(backgroundRuntimeProvider{base}, base.models[0], settings, sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	runID := "tui-background-replay"
	now := time.Now()
	if err := session.SaveSessionRun(settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sess.GetHeader().ID, WorkDir: workDir, Source: "channel:tui",
		Model: "model-1", Mode: "agent", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save background run: %v", err)
	}
	if _, err := sess.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recovered answer"}})); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if err := session.UpdateSessionRunStatus(settings.GetSessionDir(), runID, "completed", "", &now); err != nil {
		t.Fatalf("complete background run: %v", err)
	}
	app.backgroundRuns = map[string]int{runID: 0}
	app.pollBackgroundRuns()
	if len(app.messages) != 1 || !strings.Contains(stripANSI(app.messages[0]), "recovered answer") {
		t.Fatalf("replayed TUI messages = %#v", app.messages)
	}
	if len(app.backgroundRuns) != 0 {
		t.Fatalf("background runs still tracked: %#v", app.backgroundRuns)
	}
}
