package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

func TestListAllDetailedQueryPageMatchesOnlyThePage(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	for i := 0; i < 2; i++ {
		mgr := New(t.TempDir(), dir)
		id := fmt.Sprintf("legacy-query-%d", i)
		if err := mgr.InitWithID(id); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.AppendMessage(provider.NewUserMessage("needle legacy preview")); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.AppendSessionTitle("Needle legacy title", "manual"); err != nil {
			t.Fatal(err)
		}
	}

	eventMgr := convergedEventStoreSessionForTest(t, dir, "event-query-page")
	if _, err := eventMgr.AppendMessage(provider.NewUserMessage("event body")); err != nil {
		t.Fatal(err)
	}
	if _, err := eventMgr.AppendSessionTitle("Needle event title", "manual"); err != nil {
		t.Fatal(err)
	}

	details, hasMore, fallback, err := ListAllDetailedQueryPage(dir, "needle", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fallback {
		t.Fatal("event-store sessions with display projections must use the page-scoped path")
	}
	if len(details) != 2 || !hasMore {
		t.Fatalf("first page = %d details, hasMore=%v; want 2 and true", len(details), hasMore)
	}
	FillSessionDetailPreviews(dir, details)
	for _, detail := range details {
		if detail.Name == "" {
			t.Fatalf("page detail did not resolve its title: %+v", detail)
		}
	}

	second, secondMore, secondFallback, err := ListAllDetailedQueryPage(dir, "needle", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if secondFallback || secondMore || len(second) != 1 {
		t.Fatalf("second page = %d details, hasMore=%v, fallback=%v; want 1/false/false", len(second), secondMore, secondFallback)
	}
}

func TestListAllDetailedQueryPageFallsBackWithoutDisplayProjection(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = CloseDatabases() })

	mgr := convergedEventStoreSessionForTest(t, dir, "event-unprojected")
	if _, err := mgr.AppendMessage(provider.NewUserMessage("unprojected needle")); err != nil {
		t.Fatal(err)
	}
	if err := WriteRootDatabase(context.Background(), dir, func(tx *dao.Tx) error {
		return dao.NewSessionDisplayProjectionDAO(nil).DeleteSession(context.Background(), tx, "event-unprojected")
	}); err != nil {
		t.Fatal(err)
	}

	_, _, fallback, err := ListAllDetailedQueryPage(dir, "needle", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !fallback {
		t.Fatal("missing display projection must request the legacy full-detail fallback")
	}
}
