package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestListWithCWDRootsMatchesOnlyAuthorizedDirectories(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	base := t.TempDir()
	authorized := filepath.Join(base, "work")
	authorizedOtherCase := filepath.Join(base, "WORK")
	sibling := filepath.Join(base, "work-other")
	for _, tc := range []struct {
		id, cwd, body string
	}{
		{"root-a", authorized, "root search marker"},
		{"root-b", authorizedOtherCase, "root search marker"},
		{"sibling", sibling, "root search marker"},
	} {
		manager := New(tc.cwd, sessionDir)
		if err := manager.InitWithID(tc.id); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(provider.NewUserMessage(tc.body)); err != nil {
			t.Fatal(err)
		}
	}

	// A non-canonical spelling of the root still authorizes that directory, but
	// the similarly named sibling must not become visible through a prefix match.
	roots := []string{filepath.Join(authorized, ".")}
	details, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithCWDRoots(roots))
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].ID != "root-a" {
		t.Fatalf("root-filtered details = %#v, want only root-a", details)
	}

	searched, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithSearch("root search marker"), WithCWDRoots(roots))
	if err != nil {
		t.Fatal(err)
	}
	if len(searched) != 1 || searched[0].ID != "root-a" {
		t.Fatalf("root-filtered search = %#v, want only root-a", searched)
	}
	count, err := CountWithMessages(sessionDir, WithSearch("root search marker"), WithCWDRoots(roots))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("root-filtered count = %d, want 1", count)
	}
}

func TestListWithProjectScopeFiltersBeforePagination(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })
	project, err := CreateProject(sessionDir, "Scoped project")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 52; i++ {
		id := fmt.Sprintf("project-session-%03d", i)
		manager := New(t.TempDir(), sessionDir)
		if err := manager.InitWithID(id); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(provider.NewUserMessage("project-scope-marker")); err != nil {
			t.Fatal(err)
		}
		if err := SetSessionMetadata(sessionDir, id, SessionMetadata{ProjectID: project.ID}); err != nil {
			t.Fatal(err)
		}
	}
	// These newer ungrouped sessions would fill the first page if scope were
	// applied after LIMIT rather than in SQL before it.
	for i := 0; i < 10; i++ {
		manager := New(t.TempDir(), sessionDir)
		if err := manager.InitWithID(fmt.Sprintf("ungrouped-session-%03d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(provider.NewUserMessage("project-scope-marker")); err != nil {
			t.Fatal(err)
		}
	}

	details, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithProjectScope(project.ID), WithLimit(51))
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 51 {
		t.Fatalf("project page size = %d, want 51", len(details))
	}
	for _, detail := range details {
		if !strings.HasPrefix(detail.ID, "project-session-") {
			t.Fatalf("project page leaked %q", detail.ID)
		}
	}

	ungrouped, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithProjectScope(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(ungrouped) != 10 {
		t.Fatalf("ungrouped size = %d, want 10", len(ungrouped))
	}
	for _, detail := range ungrouped {
		if !strings.HasPrefix(detail.ID, "ungrouped-session-") {
			t.Fatalf("ungrouped page leaked %q", detail.ID)
		}
	}

	searched, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithSearch("project-scope-marker"), WithProjectScope(project.ID), WithLimit(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(searched) != 2 {
		t.Fatalf("project search page size = %d, want 2", len(searched))
	}
	count, err := CountWithMessages(sessionDir, WithSearch("project-scope-marker"), WithProjectScope(project.ID))
	if err != nil {
		t.Fatal(err)
	}
	if count != 52 {
		t.Fatalf("project search count = %d, want 52", count)
	}
}

func TestListWithEmptyCWDRootsListsNothing(t *testing.T) {
	sessionDir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	manager := New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("empty-roots-visible-without-filter"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("body")); err != nil {
		t.Fatal(err)
	}

	details, err := ListAllDetailed(sessionDir, WithMessagesOnly(), WithCWDRoots(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 0 {
		t.Fatalf("empty roots listed %#v, want no sessions", details)
	}
	count, err := CountWithMessages(sessionDir, WithCWDRoots(nil))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty roots count = %d, want 0", count)
	}
}
