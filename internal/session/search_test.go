package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func newSearchTestSession(t *testing.T, sessionDir, cwd, id string) *Manager {
	t.Helper()
	manager := New(cwd, sessionDir)
	if err := manager.InitWithID(id); err != nil {
		t.Fatalf("init session %q: %v", id, err)
	}
	return manager
}

func searchResultIDs(details []SessionDetail) map[string]SessionDetail {
	result := make(map[string]SessionDetail, len(details))
	for _, detail := range details {
		result[detail.ID] = detail
	}
	return result
}

func TestSearchAllDetailedSemantics(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	// Legacy session matching through ID, cwd, title, content, and rich text.
	legacy := newSearchTestSession(t, sessionDir, "/tmp/search-cwd-marker", "legacy-search-alpha")
	if _, err := legacy.AppendSessionInfo("Release Title Marker"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendMessage(provider.NewUserMessage("alpha body marker")); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.AppendMessage(provider.Message{Role: "user", Contents: []provider.ContentBlock{{Type: "text", Text: "rich block marker"}}}); err != nil {
		t.Fatal(err)
	}

	// A content override replaces the effective message text; the raw original
	// payload must no longer match.
	overridden := newSearchTestSession(t, sessionDir, "/tmp/search-override", "override-search-session")
	originalID, err := overridden.AppendMessage(provider.NewUserMessage("original-only-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overridden.AppendContentOverride(originalID, provider.NewUserMessage("replacement marker"), "test", ""); err != nil {
		t.Fatal(err)
	}

	// Compaction replaces the effective history with its summary, so text that
	// was compacted away must no longer match.
	compacted := newSearchTestSession(t, sessionDir, "/tmp/search-compaction", "compaction-search-session")
	if _, err := compacted.AppendMessage(provider.NewUserMessage("discarded-marker")); err != nil {
		t.Fatal(err)
	}
	if _, err := compacted.AppendCompaction("compaction-summary-marker", "", 42); err != nil {
		t.Fatal(err)
	}

	// A bound channel session matches on channel metadata.
	bound, err := CreateBound(t.TempDir(), sessionDir, "wechat", "channel-search-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bound.AppendMessage(provider.NewUserMessage("bound channel body")); err != nil {
		t.Fatal(err)
	}

	// EventStore session: title, replayed message text, and preview all come from
	// the same read-only projection.
	event := convergedEventStoreSessionForTest(t, sessionDir, "event-search-session")
	if _, err := event.AppendSessionInfo("Event Title Marker"); err != nil {
		t.Fatal(err)
	}
	if _, err := event.AppendMessage(provider.NewUserMessage("event body marker")); err != nil {
		t.Fatal(err)
	}

	// Missing EventStore bytes degrade to cheap matching: the ID still matches,
	// content does not, and the search as a whole succeeds.
	broken := convergedEventStoreSessionForTest(t, sessionDir, "broken-store")
	if _, err := broken.AppendMessage(provider.NewUserMessage("broken content marker")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEventStore(sessionDir, "broken-store"); err != nil {
		t.Fatal(err)
	}

	beforeOpens := OpenByIDExactCallCount()
	beforeWrites := writeRootTransactionCount()

	cases := []struct {
		query   string
		wantIDs []string
	}{
		{"legacy-search-alpha", []string{"legacy-search-alpha"}},
		{"search-cwd-marker", []string{"legacy-search-alpha"}},
		{"release title marker", []string{"legacy-search-alpha"}},
		{"ALPHA BODY MARKER", []string{"legacy-search-alpha"}},
		{"rich block marker", []string{"legacy-search-alpha"}},
		{"channel-search-token", []string{bound.GetHeader().ID}},
		{"replacement marker", []string{"override-search-session"}},
		{"original-only-marker", nil},
		{"compaction-summary-marker", []string{"compaction-search-session"}},
		{"discarded-marker", nil},
		{"event body marker", []string{"event-search-session"}},
		{"event title marker", []string{"event-search-session"}},
		{"broken content marker", nil},
		{"broken-store", []string{"broken-store"}},
		{"absolutely-no-match", nil},
	}
	for _, tc := range cases {
		details, total, err := SearchAllDetailed(sessionDir, tc.query, 0, 0)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		got := searchResultIDs(details)
		if total != len(tc.wantIDs) || len(got) != len(tc.wantIDs) {
			t.Fatalf("search %q total/page = %d/%d, want %d; details=%#v", tc.query, total, len(got), len(tc.wantIDs), details)
		}
		for _, wantID := range tc.wantIDs {
			if _, ok := got[wantID]; !ok {
				t.Fatalf("search %q missing %q; got %#v", tc.query, wantID, details)
			}
		}
	}

	// EventStore preview and title are filled from the one read-only projection.
	details, _, err := SearchAllDetailed(sessionDir, "event body marker", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Preview != "message-0" || details[0].Name != "Event Title Marker" {
		t.Fatalf("event-store detail = %#v", details)
	}

	if opens := OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("search opened %d sessions through Manager, want 0", opens)
	}
	if writes := writeRootTransactionCount() - beforeWrites; writes != 0 {
		t.Fatalf("search opened %d root write transactions, want 0", writes)
	}
}

func TestSearchAllDetailedTotalAndPageFromOneScan(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	for i := 0; i < 7; i++ {
		manager := newSearchTestSession(t, sessionDir, fmt.Sprintf("/tmp/page-cwd-%d", i), fmt.Sprintf("page-session-%d", i))
		if _, err := manager.AppendMessage(provider.NewUserMessage("page-marker body")); err != nil {
			t.Fatal(err)
		}
		// Keep last_active ordering deterministic across sessions.
		time.Sleep(2 * time.Millisecond)
	}
	nonMatching := newSearchTestSession(t, sessionDir, "/tmp/page-cwd-other", "page-nonmatching")
	if _, err := nonMatching.AppendMessage(provider.NewUserMessage("unrelated body")); err != nil {
		t.Fatal(err)
	}

	all, err := ListAll(sessionDir, WithMessagesOnly())
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]string, 0, 7)
	for _, info := range all {
		if strings.HasPrefix(sessionFileID(info.Path), "page-session-") {
			expected = append(expected, sessionFileID(info.Path))
		}
	}
	if len(expected) != 7 {
		t.Fatalf("expected 7 matching sessions, got %v", expected)
	}

	beforeOpens := OpenByIDExactCallCount()
	beforeWrites := writeRootTransactionCount()
	details, total, err := SearchAllDetailed(sessionDir, "page-marker", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Fatalf("total = %d, want 7", total)
	}
	if len(details) != 2 || details[0].ID != expected[2] || details[1].ID != expected[3] {
		t.Fatalf("page = %#v, want %v", details, expected[2:4])
	}
	if opens := OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("search opened %d managers, want 0", opens)
	}
	if writes := writeRootTransactionCount() - beforeWrites; writes != 0 {
		t.Fatalf("search opened %d write transactions, want 0", writes)
	}

	tail, tailTotal, err := SearchAllDetailed(sessionDir, "page-marker", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if tailTotal != 7 || len(tail) != 1 || tail[0].ID != expected[6] {
		t.Fatalf("tail page = %#v total=%d", tail, tailTotal)
	}
	pastEnd, pastTotal, err := SearchAllDetailed(sessionDir, "page-marker", 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if pastTotal != 7 || len(pastEnd) != 0 {
		t.Fatalf("past-end page = %#v total=%d", pastEnd, pastTotal)
	}

	count, err := CountWithMessages(sessionDir, WithSearch("page-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("search count = %d, want 7", count)
	}
}

func TestSearchAllDetailedReadsEventStoreOnce(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := convergedEventStoreSessionForTest(t, sessionDir, "search-event-once")
	if _, err := manager.AppendMessage(provider.NewUserMessage("event-once-marker")); err != nil {
		t.Fatal(err)
	}

	beforeScans := EventStoreWriteStats().FullScans
	beforeOpens := OpenByIDExactCallCount()
	beforeWrites := writeRootTransactionCount()
	details, total, err := SearchAllDetailed(sessionDir, "event-once-marker", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(details) != 1 || details[0].ID != "search-event-once" {
		t.Fatalf("search result = %#v total=%d", details, total)
	}
	if scans := EventStoreWriteStats().FullScans - beforeScans; scans != 1 {
		t.Fatalf("search performed %d EventStore scans, want 1", scans)
	}
	if opens := OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("search opened %d managers, want 0", opens)
	}
	if writes := writeRootTransactionCount() - beforeWrites; writes != 0 {
		t.Fatalf("search opened %d write transactions, want 0", writes)
	}
}

func TestListSessionParentsIsReadOnlyProjection(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	if err := New("/tmp/parent-cwd", sessionDir).InitWithID("parent-source"); err != nil {
		t.Fatal(err)
	}
	childID := "parent-child"
	if err := WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).InsertSession(context.Background(), tx, "sessions", childID, "/tmp/parent-cwd-child", time.Now().Format(time.RFC3339Nano), "parent-source", 1, "", "", 0, 0, "", "")
	}); err != nil {
		t.Fatal(err)
	}

	beforeOpens := OpenByIDExactCallCount()
	beforeWrites := writeRootTransactionCount()
	parents, err := ListSessionParents(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if opens := OpenByIDExactCallCount() - beforeOpens; opens != 0 {
		t.Fatalf("ListSessionParents opened %d managers, want 0", opens)
	}
	if writes := writeRootTransactionCount() - beforeWrites; writes != 0 {
		t.Fatalf("ListSessionParents opened %d write transactions, want 0", writes)
	}
	byID := make(map[string]string, len(parents))
	for _, link := range parents {
		byID[link.ID] = link.ParentSession
	}
	if byID[childID] != "parent-source" {
		t.Fatalf("parent projection = %#v", byID)
	}
}
