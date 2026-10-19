package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
)

// TestLoadLegacySessionMigratesContentStore pins the ACP boundary of the
// unified migration entry point: loading a session whose transcript still lives
// in the legacy SQLite store must export it into the session EventStore and flip
// the content store, while the load keeps replaying the same transcript.
func TestLoadLegacySessionMigratesContentStore(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	const sessionID = "legacy-migrate"
	newTestSession(t, cwd, dir, sessionID, 3)

	if before := contentStoreOf(t, dir, sessionID); before != "sqlite" {
		t.Fatalf("precondition content store = %q, want sqlite", before)
	}

	var out bytes.Buffer
	s := &server{
		settings:   &config.Settings{SessionDir: dir},
		sbMgr:      sandbox.NewManager(cwd),
		sessions:   make(map[string]*sessionRuntime),
		toolTitles: make(map[string]string),
		mcpNotify:  make(map[string]bool),
		w:          &out,
	}
	s.handleLoadSession(rpcRequest{
		ID:     json.RawMessage("1"),
		Params: json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionID, cwd)),
	})

	if after := contentStoreOf(t, dir, sessionID); after != "event_store" {
		t.Fatalf("content store after ACP load = %q, want event_store", after)
	}

	updates := 0
	for _, message := range jsonLines(t, &out) {
		if message["method"] == "session/update" {
			updates++
		}
	}
	if updates != 3 {
		t.Fatalf("replayed updates after migration = %d, want 3", updates)
	}
	if _, err := s.closeSessionRuntime(sessionID); err != nil {
		t.Fatal(err)
	}
}

func contentStoreOf(t *testing.T, dir, sessionID string) string {
	t.Helper()
	db, err := session.OpenRootDB(dir)
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	header, err := dao.NewSessionDAO(db.Bun()).Header(context.Background(), "sessions", sessionID)
	if err != nil {
		t.Fatalf("read session header: %v", err)
	}
	return header.ContentStore
}
