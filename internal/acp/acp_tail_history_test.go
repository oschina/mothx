package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func newEventStoreTestSession(t *testing.T, cwd, dir, id string, messages int) *session.Manager {
	t.Helper()
	mgr := session.New(cwd, dir)
	if err := mgr.InitWithID(id); err != nil {
		t.Fatal(err)
	}
	if err := mgr.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < messages; i++ {
		if _, err := mgr.AppendMessage(provider.NewUserMessage(fmt.Sprintf("tail-message-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.AppendSessionTitle("Tail session", "manual"); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestLoadSessionUsesTailHistoryUntilPrompt(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	const sessionID = "tail-load"
	newEventStoreTestSession(t, cwd, dir, sessionID, 6)

	var out bytes.Buffer
	s := executionProjectionTestServer(t, cwd, dir, &out)
	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"historyLimit":2}`, sessionID, cwd)),
	})
	result := loadResponseResult(t, &out)
	history, ok := result["history"].(map[string]any)
	if !ok {
		t.Fatalf("load result has no history: %#v", result)
	}
	updates := history["updates"].([]any)
	if len(updates) != 2 {
		t.Fatalf("tail history updates = %d, want 2", len(updates))
	}
	cursor, _ := history["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("tail history missing nextCursor")
	}
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.mgr == nil || !rt.mgr.Partial() {
		t.Fatal("session/load should tail-open a partial Manager")
	}

	out.Reset()
	s.handleSessionHistory(rpcRequest{
		ID:     json.RawMessage("2"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"cursor":%q,"limit":2}`, sessionID, cursor)),
	})
	response := jsonLines(t, &out)[0]
	history, ok = response["result"].(map[string]any)
	if !ok {
		t.Fatalf("history response = %#v", response)
	}
	if got := len(history["updates"].([]any)); got != 2 {
		t.Fatalf("older tail history updates = %d, want 2", got)
	}

	// The first prompt path calls Reload; it must turn the partial Manager back
	// into canonical replay before any append or provider request.
	if err := rt.mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if rt.mgr.Partial() {
		t.Fatal("Reload did not clear the partial Manager")
	}
	if got := len(rt.mgr.GetMessages()); got != 6 {
		t.Fatalf("canonical messages after Reload = %d, want 6", got)
	}
	if _, err := s.closeSessionRuntime(sessionID); err != nil {
		t.Fatal(err)
	}
}
