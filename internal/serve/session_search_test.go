package serve

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestListPersistedSessionsSearchUsesOneScan(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()

	for _, id := range []string{"search-a", "search-b", "search-c"} {
		m := session.New("/tmp/serve-search", sessionDir)
		if err := m.InitWithID(id); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AppendMessage(provider.NewUserMessage("serve search needle")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	other := session.New("/tmp/serve-search-other", sessionDir)
	if err := other.InitWithID("search-other"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(provider.NewUserMessage("unrelated")); err != nil {
		t.Fatal(err)
	}

	rt := &channelRuntime{cfg: DefaultConfig()}
	beforeOpens := session.OpenByIDExactCallCount()
	page, total, err := rt.listPersistedSessions(sessionDir, "serve search needle", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(page) != 2 {
		t.Fatalf("page len = %d, want 2", len(page))
	}
	if opens := session.OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("search page opened %d session managers, want 0", opens)
	}

	all, err := session.ListAllDetailed(sessionDir, session.WithMessagesOnly(), session.WithDeferredEventStorePreview())
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]string, 0, 3)
	for _, detail := range all {
		if detail.Cwd == "/tmp/serve-search" {
			expected = append(expected, detail.ID)
		}
	}
	if len(expected) != 3 {
		t.Fatalf("expected order = %#v", expected)
	}
	for i := range page {
		if page[i].ID != expected[i] {
			t.Fatalf("page[%d] = %q, want %q", i, page[i].ID, expected[i])
		}
	}
}

func TestListPersistedSessionsWithoutSearchKeepsPaginatedPath(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()

	for _, id := range []string{"no-search-a", "no-search-b", "no-search-c"} {
		m := session.New("/tmp/serve-no-search", sessionDir)
		if err := m.InitWithID(id); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AppendMessage(provider.NewUserMessage("body")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	rt := &channelRuntime{cfg: DefaultConfig()}
	page, total, err := rt.listPersistedSessions(sessionDir, "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 2 {
		t.Fatalf("page/total = %d/%d, want 2/3", len(page), total)
	}
}

func TestListPersistedSessionsBatchMatchesPerItemProjection(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()

	ids := []string{"batch-a", "batch-b", "batch-c", "batch-d"}
	for _, id := range ids {
		m := session.New("/tmp/serve-batch", sessionDir)
		if err := m.InitWithID(id); err != nil {
			t.Fatal(err)
		}
		if _, err := m.AppendMessage(provider.NewUserMessage("batch body " + id)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	project, err := session.CreateProject(sessionDir, "Batch Project")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetSessionMetadata(sessionDir, "batch-a", session.SessionMetadata{ProjectID: project.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetSessionMetadata(sessionDir, "batch-b", session.SessionMetadata{Pinned: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := (agentruntime.RunStore{SessionDir: sessionDir}).Create(agentruntime.DurableRun{
		ID: "batch-running-run", SessionID: "batch-c", Status: string(agentruntime.RunStateRunning),
		StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	rt := &channelRuntime{cfg: DefaultConfig()}
	page, total, err := rt.listPersistedSessions(sessionDir, "", len(ids), 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(ids) || len(page) != len(ids) {
		t.Fatalf("page/total = %d/%d, want %d/%d", len(page), total, len(ids), len(ids))
	}
	byID := make(map[string]int, len(page))
	for i := range page {
		byID[page[i].ID] = i
	}

	// Compare every page item with the per-item projection the batch replaced:
	// metadata from GetSessionMetadata and execution from InspectSessionExecution.
	for _, id := range ids {
		index, ok := byID[id]
		if !ok {
			t.Fatalf("page is missing session %q", id)
		}
		item := page[index]
		metadata, err := session.GetSessionMetadata(sessionDir, id)
		if err != nil {
			t.Fatalf("GetSessionMetadata(%q): %v", id, err)
		}
		if item.ProjectID != metadata.ProjectID || item.Pinned != metadata.Pinned {
			t.Fatalf("session %q metadata = (%q,%v), want (%q,%v)", id, item.ProjectID, item.Pinned, metadata.ProjectID, metadata.Pinned)
		}
		expected, err := agentruntime.InspectSessionExecution(sessionDir, id)
		if err != nil {
			t.Fatalf("InspectSessionExecution(%q): %v", id, err)
		}
		if item.Execution == nil {
			t.Fatalf("session %q has no execution projection", id)
		}
		gotJSON, err := json.Marshal(item.Execution)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("session %q execution = %s, want %s", id, gotJSON, wantJSON)
		}
		if item.Running != expected.Running {
			t.Fatalf("session %q running = %v, want %v", id, item.Running, expected.Running)
		}
	}

	// The running session must stay non-idle in the page projection.
	running := page[byID["batch-c"]].Execution
	if running.State == agentruntime.SessionExecutionIdle || !running.Busy || running.CanSubmit {
		t.Fatalf("running session execution = %#v, want non-idle", running)
	}
}

func TestPersistedSessionExecutionMissingIsUnknownNotIdle(t *testing.T) {
	execution := persistedSessionExecution("missing-execution", map[string]agentruntime.SessionExecutionSnapshot{})
	if execution.State != agentruntime.SessionExecutionUnknown {
		t.Fatalf("state = %q, want %q", execution.State, agentruntime.SessionExecutionUnknown)
	}
	if !execution.Busy || execution.CanSubmit || execution.Running {
		t.Fatalf("missing execution = %#v, want unknown and not idle", execution)
	}
	if execution.SessionID != "missing-execution" {
		t.Fatalf("session id = %q", execution.SessionID)
	}
}

func TestListPersistedSessionsSearchReadsEventStoreOnce(t *testing.T) {
	sessionDir := t.TempDir()
	defer session.CloseDatabases()
	manager := session.New("/tmp/serve-event-search", sessionDir)
	if err := manager.InitWithID("serve-event-search"); err != nil {
		t.Fatal(err)
	}
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("serve event needle")); err != nil {
		t.Fatal(err)
	}

	rt := &channelRuntime{cfg: DefaultConfig()}
	beforeScans := session.EventStoreWriteStats().FullScans
	page, total, err := rt.listPersistedSessions(sessionDir, "serve event needle", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 || page[0].ID != "serve-event-search" {
		t.Fatalf("page = %#v total=%d", page, total)
	}
	if scans := session.EventStoreWriteStats().FullScans - beforeScans; scans != 1 {
		t.Fatalf("serve search performed %d EventStore scans, want 1", scans)
	}
}
