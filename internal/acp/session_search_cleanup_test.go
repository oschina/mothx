package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func insertSessionRowForTest(t *testing.T, sessionDir, id, cwd, parent string, created time.Time) {
	t.Helper()
	if err := session.WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).InsertSession(context.Background(), tx, "sessions", id, cwd, created.Format(time.RFC3339Nano), parent, 1, "", "", 0, 0, "", "")
	}); err != nil {
		t.Fatalf("insert session %q: %v", id, err)
	}
}

func TestSessionCascadeIDsUsesLightweightParentProjection(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()
	root := session.New("/tmp/cascade-root", sessionDir)
	if err := root.InitWithID("cascade-root"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertSessionRowForTest(t, sessionDir, "cascade-child", "/tmp/cascade-child", "cascade-root", now.Add(time.Second))
	insertSessionRowForTest(t, sessionDir, "cascade-grandchild", "/tmp/cascade-grandchild", "cascade-child", now.Add(2*time.Second))
	insertSessionRowForTest(t, sessionDir, "cascade-unrelated", "/tmp/cascade-unrelated", "", now.Add(3*time.Second))

	srv := &server{settings: &config.Settings{SessionDir: sessionDir}}
	beforeOpens := session.OpenByIDExactCallCount()
	ids, err := srv.sessionCascadeIDs("cascade-root")
	if err != nil {
		t.Fatal(err)
	}
	if opens := session.OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("sessionCascadeIDs opened %d session managers, want 0", opens)
	}
	want := []string{"cascade-root", "cascade-child", "cascade-grandchild"}
	if len(ids) != len(want) {
		t.Fatalf("cascade = %#v, want %#v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("cascade = %#v, want %#v", ids, want)
		}
	}
}

func TestManageStatsSessionsCountCountsAllPersistedSessions(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()
	withMessage := session.New("/tmp/stats-with-message", sessionDir)
	if err := withMessage.InitWithID("stats-with-message"); err != nil {
		t.Fatal(err)
	}
	if _, err := withMessage.AppendMessage(provider.NewUserMessage("stats body")); err != nil {
		t.Fatal(err)
	}
	empty := session.New("/tmp/stats-empty", sessionDir)
	if err := empty.InitWithID("stats-empty"); err != nil {
		t.Fatal(err)
	}

	out := &syncedBuffer{}
	srv := newPhase1FixtureServer(out)
	srv.settings = &config.Settings{SessionDir: sessionDir}
	srv.handleManageStatsSummary(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1")})
	var response map[string]any
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatalf("decode response %q: %v", out.String(), err)
	}
	if errObj := response["error"]; errObj != nil {
		t.Fatalf("stats error = %#v", errObj)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("stats result = %#v", response)
	}
	if got, _ := result["sessions"].(float64); got != 2 {
		t.Fatalf("sessions count = %v, want 2 (including the empty session)", result["sessions"])
	}
}
