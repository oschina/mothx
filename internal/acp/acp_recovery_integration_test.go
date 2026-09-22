package acp

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestACPSessionReconnectReplaysDurableRunState(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	mgr := session.New(cwd, dir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := (agentruntime.RunStore{SessionDir: dir}).Create(agentruntime.DurableRun{
		ID: "acp-reconnect-run", SessionID: mgr.GetHeader().ID, WorkDir: cwd,
		Source: "acp", Model: "test-model", Mode: "agent", Status: "running", StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: dir}).Record(agentruntime.RunEvent{
		SessionID: mgr.GetHeader().ID, RunID: "acp-reconnect-run", EventType: "started",
		Source: "acp", Status: "running", Model: "test-model", Mode: "agent", Timestamp: started,
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	s := &server{
		settings:   &config.Settings{SessionDir: dir},
		sessions:   make(map[string]*sessionRuntime),
		toolTitles: make(map[string]string),
		mcpNotify:  make(map[string]bool),
		w:          &out,
	}
	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(`{"sessionId":"` + mgr.GetHeader().ID + `","cwd":"` + cwd + `"}`),
	})
	if s.sessions[mgr.GetHeader().ID] == nil {
		t.Fatal("ACP session was not reconnected")
	}
	events, err := session.ListSessionRunEvents(dir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	replay := agentruntime.ReplayRunEvents(events, "acp-reconnect-run")
	if replay.Status != agentruntime.RunStateRunning || replay.Terminal || len(replay.Events) != 1 {
		t.Fatalf("replay = %#v", replay)
	}
}
