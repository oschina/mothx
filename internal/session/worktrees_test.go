package session

import "testing"

func TestWorktreeRegistryCRUD(t *testing.T) {
	sessionDir := t.TempDir()

	created, err := CreateWorktreeRecord(sessionDir, Worktree{
		ID:             "wt-1",
		Name:           "feature",
		Branch:         "mothx/feature",
		Directory:      "/worktrees/repo-abc/feature",
		RepositoryRoot: "/repo",
		ProjectID:      "proj-1",
		StartCommand:   "echo hi",
	})
	if err != nil {
		t.Fatalf("CreateWorktreeRecord: %v", err)
	}
	if created.Status != WorktreeStatusPending {
		t.Fatalf("status = %q, want pending", created.Status)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("createdAt was not stamped")
	}

	byID, err := GetWorktreeByID(sessionDir, "wt-1")
	if err != nil {
		t.Fatalf("GetWorktreeByID: %v", err)
	}
	if byID.Name != "feature" || byID.Branch != "mothx/feature" || byID.ProjectID != "proj-1" {
		t.Fatalf("byID = %+v", byID)
	}

	byDir, err := GetWorktreeByDirectory(sessionDir, "/worktrees/repo-abc/feature")
	if err != nil {
		t.Fatalf("GetWorktreeByDirectory: %v", err)
	}
	if byDir.ID != "wt-1" {
		t.Fatalf("byDir id = %q", byDir.ID)
	}

	list, err := ListWorktrees(sessionDir, "/repo", "")
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(list) != 1 || list[0].ID != "wt-1" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].StartCommand != "echo hi" {
		t.Fatalf("startCommand = %q", list[0].StartCommand)
	}

	if err := UpdateWorktreeStatus(sessionDir, "wt-1", WorktreeStatusReady, ""); err != nil {
		t.Fatalf("UpdateWorktreeStatus: %v", err)
	}
	ready, err := GetWorktreeByID(sessionDir, "wt-1")
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != WorktreeStatusReady {
		t.Fatalf("status = %q, want ready", ready.Status)
	}

	if err := DeleteWorktreeRecord(sessionDir, "wt-1"); err != nil {
		t.Fatalf("DeleteWorktreeRecord: %v", err)
	}
	if got, err := GetWorktreeByID(sessionDir, "wt-1"); err != nil || got.ID != "" {
		t.Fatalf("after delete got %+v err %v", got, err)
	}
}

func TestWorktreeRegistryMissingAndValidation(t *testing.T) {
	sessionDir := t.TempDir()
	// A missing database yields an empty result, not an error.
	if got, err := GetWorktreeByID(sessionDir, "nope"); err != nil || got.ID != "" {
		t.Fatalf("missing get = %+v err %v", got, err)
	}
	if _, err := CreateWorktreeRecord(sessionDir, Worktree{Name: "x"}); err == nil {
		t.Fatal("expected validation error for missing id/directory/repositoryRoot")
	}
	if err := UpdateWorktreeStatus(sessionDir, "", "ready", ""); err == nil {
		t.Fatal("expected validation error for empty id")
	}
}

func TestWorktreeRegistryDirectoryUnique(t *testing.T) {
	sessionDir := t.TempDir()
	base := Worktree{ID: "a", Directory: "/wt/same", RepositoryRoot: "/repo"}
	if _, err := CreateWorktreeRecord(sessionDir, base); err != nil {
		t.Fatal(err)
	}
	dupe := Worktree{ID: "b", Directory: "/wt/same", RepositoryRoot: "/repo"}
	if _, err := CreateWorktreeRecord(sessionDir, dupe); err == nil {
		t.Fatal("expected unique directory constraint to reject the duplicate")
	}
}
