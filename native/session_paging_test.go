package main

import (
	"strconv"
	"testing"
)

func TestMergeSessionRowsKeepsFirstAndDropsDuplicates(t *testing.T) {
	base := []listedSession{{SessionID: "s1", Title: "one"}}
	added := []listedSession{
		{SessionID: "s1", Title: "duplicate"},
		{SessionID: "s2", Title: "two"},
		{SessionID: ""},
	}
	merged, addedCount := mergeSessionRows(base, added)
	if len(merged) != 2 || addedCount != 1 {
		t.Fatalf("merged = %+v, added = %d; want 2 rows and 1 add", merged, addedCount)
	}
	if merged[0].Title != "one" {
		t.Fatalf("duplicate replaced the first row: %+v", merged[0])
	}
}

func TestSessionPageKeyNormalizesScopes(t *testing.T) {
	cases := []struct {
		scope, projectID, want string
	}{
		{"all", "", sessionPageAll},
		{"ungrouped", "", sessionPageUngrouped},
		{"project", "p1", "project:p1"},
		{"", "p2", "project:p2"},
	}
	for _, tc := range cases {
		if got := sessionPageKey(tc.scope, tc.projectID); got != tc.want {
			t.Errorf("sessionPageKey(%q, %q) = %q, want %q", tc.scope, tc.projectID, got, tc.want)
		}
	}
}

func TestRebuildSessionCatalogUnionsLoadedPages(t *testing.T) {
	a := testApp()
	a.sessionPages = map[string]sessionPage{
		sessionPageAll: {
			Key:      sessionPageAll,
			Sessions: []listedSession{{SessionID: "s2", UpdatedAt: "2026-01-02T00:00:00Z"}},
		},
		sessionPageUngrouped: {
			Key:      sessionPageUngrouped,
			Sessions: []listedSession{{SessionID: "s1", UpdatedAt: "2026-01-01T00:00:00Z"}},
		},
		"project:p1": {
			Key:      "project:p1",
			Sessions: []listedSession{{SessionID: "s3", UpdatedAt: "2026-01-03T00:00:00Z"}},
		},
	}
	a.rebuildSessionCatalog()
	if len(a.sessions) != 3 {
		t.Fatalf("catalog = %+v, want 3 sessions", a.sessions)
	}
	if a.sessions[0].SessionID != "s3" || a.sessions[2].SessionID != "s1" {
		t.Fatalf("catalog not sorted by updatedAt: %+v", a.sessions)
	}
}

func TestTaskTreeCarriesBranchPageState(t *testing.T) {
	a := testApp()
	a.sessions = []listedSession{{SessionID: "s1", Meta: sessionMeta{ProjectID: "p1"}}}
	a.projects = []projectView{{ID: "p1", Name: "Project"}}
	a.sessionPages = map[string]sessionPage{
		"project:p1": {
			Key: "project:p1",
			sessionPageStatus: sessionPageStatus{
				Loaded:     true,
				NextCursor: "cursor-1",
			},
		},
		sessionPageUngrouped: {
			Key:               sessionPageUngrouped,
			sessionPageStatus: sessionPageStatus{Loaded: true, NextCursor: "cursor-2"},
		},
	}
	tree := a.taskTree()
	if len(tree.Projects) != 1 || tree.Projects[0].Page.NextCursor != "cursor-1" {
		t.Fatalf("project page = %+v", tree.Projects)
	}
	if tree.UngroupedPage.NextCursor != "cursor-2" {
		t.Fatalf("ungrouped page = %+v", tree.UngroupedPage)
	}
}

// The ACP server pages session/listAll at 50 rows. The sidebar must merge the
// second page instead of replacing the first one (session 51+ used to vanish).
func TestSidebarPaginationCrossesServerPageBoundary(t *testing.T) {
	a := testApp()
	a.setFeatures(map[string]bool{"sessionListAll": true})
	const serverPageSize = 50
	first := make([]listedSession, 0, serverPageSize)
	for index := 0; index < serverPageSize; index++ {
		first = append(first, listedSession{
			SessionID: "s" + strconv.Itoa(index+1),
			Title:     "Session " + strconv.Itoa(index+1),
			UpdatedAt: "2026-01-01T00:00:00Z",
		})
	}
	page := newSessionPage(sessionPageUngrouped, "ungrouped", "")
	a.setSessionPage(page)
	a.applySessionScopePage(page, sessionListResult{Sessions: first, NextCursor: "cursor-2"}, false)
	if len(a.sessions) != serverPageSize {
		t.Fatalf("first page catalog = %d rows, want %d", len(a.sessions), serverPageSize)
	}
	if got := a.sessionPageByKey(sessionPageUngrouped).NextCursor; got != "cursor-2" {
		t.Fatalf("first page next cursor = %q, want cursor-2", got)
	}

	// Second page: one older session beyond the server's 50-row page.
	page = a.sessionPageByKey(sessionPageUngrouped)
	a.applySessionScopePage(page, sessionListResult{
		Sessions:   []listedSession{{SessionID: "s51", Title: "Session 51", UpdatedAt: "2025-12-31T00:00:00Z"}},
		NextCursor: "",
	}, true)
	if len(a.sessions) != serverPageSize+1 {
		t.Fatalf("merged catalog = %d rows, want %d: %+v", len(a.sessions), serverPageSize+1, a.sessions)
	}
	found := false
	for _, session := range a.sessions {
		if session.SessionID == "s51" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session 51 disappeared after load more: %+v", a.sessions)
	}
	if got := a.sessionPageByKey(sessionPageUngrouped).NextCursor; got != "" {
		t.Fatalf("last page must clear next cursor, got %q", got)
	}
	if got := len(a.sessionPageByKey(sessionPageUngrouped).Sessions); got != serverPageSize+1 {
		t.Fatalf("branch page rows = %d, want %d", got, serverPageSize+1)
	}
}
