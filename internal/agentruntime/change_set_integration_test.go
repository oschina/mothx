package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
	worktreepkg "github.com/oschina/mothx/internal/worktree"
)

func initIntegrationRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "initial")
	return repo
}

type integrationFixture struct {
	repo       string
	sessionDir string
	runtime    *SessionRuntime
	worktrees  *WorktreeManager
	git        *worktreepkg.Manager
	worktreeID string
	base       string
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	repo := initIntegrationRepo(t)
	sessionDir := t.TempDir()
	manager := session.New(repo, sessionDir)
	if err := manager.InitWithID("integration-session"); err != nil {
		t.Fatal(err)
	}
	git := worktreepkg.NewManager(t.TempDir(), "mothx")
	info, err := git.Plan(context.Background(), repo, "child", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := git.Add(context.Background(), repo, info); err != nil {
		t.Fatal(err)
	}
	if err := git.Populate(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	base, err := git.Revision(context.Background(), info.Directory)
	if err != nil {
		t.Fatal(err)
	}
	record, err := session.CreateWorktreeRecord(sessionDir, session.Worktree{
		ID: "wt-child", Name: info.Name, Branch: info.Branch, Directory: info.Directory,
		RepositoryRoot: repo, Status: session.WorktreeStatusReady,
	})
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := NewWorktreeManager(WorktreeManagerOptions{SessionDir: sessionDir, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "integration-session", Mode: ModeRouter, WorkDir: repo}
	return &integrationFixture{
		repo: repo, sessionDir: sessionDir, runtime: runtime,
		worktrees: worktrees, git: git, worktreeID: record.ID, base: base,
	}
}

func (f *integrationFixture) capture(t *testing.T, patchLimit int) worktreepkg.ChangeSet {
	t.Helper()
	changeSet, err := f.worktrees.CaptureChangeSet(context.Background(), f.worktreeID, f.base, patchLimit)
	if err != nil {
		t.Fatal(err)
	}
	return changeSet
}

func (f *integrationFixture) saveChild(t *testing.T, childID string, changeSet worktreepkg.ChangeSet) {
	t.Helper()
	encoded, err := json.Marshal(changeSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SaveChildRun(f.sessionDir, session.ChildRun{
		ChildID: childID, SessionID: "integration-session", Backend: "acp",
		Status: "success", WorktreeID: f.worktreeID, BaseRevision: f.base,
		ChangeSetJSON: string(encoded),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeChangeSetIntegrationAppliesSnapshotOnce(t *testing.T) {
	fixture := newIntegrationFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.worktreeIDDirectory(t), "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.worktreeIDDirectory(t), "new.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := fixture.capture(t, worktreepkg.DefaultChangeSetPatchBytes)
	fixture.saveChild(t, "worker-1", changeSet)

	integrator := &RuntimeChangeSetIntegrator{Runtime: fixture.runtime, Worktrees: fixture.worktrees}
	result, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{{
		Handle: "worker-1", Digest: changeSet.Digest,
	}})
	if err != nil {
		t.Fatalf("IntegrateChangeSets: %v", err)
	}
	if result.Status != ChangeSetIntegrationApplied || len(result.AppliedFiles) != 2 {
		t.Fatalf("integration result = %#v", result)
	}
	readme, _ := os.ReadFile(filepath.Join(fixture.repo, "README.md"))
	if string(readme) != "hello\nchanged\n" {
		t.Fatalf("base README = %q", readme)
	}
	if content, err := os.ReadFile(filepath.Join(fixture.repo, "new.txt")); err != nil || string(content) != "new file\n" {
		t.Fatalf("base new.txt = %q, err=%v", content, err)
	}
	record, found, err := session.LoadChildRun(fixture.sessionDir, "integration-session", "worker-1")
	if err != nil || !found || record.IntegrationStatus != ChangeSetIntegrationApplied || record.IntegrationDigest != changeSet.Digest {
		t.Fatalf("integration evidence = %#v, found=%t err=%v", record, found, err)
	}

	// A repeated selection is idempotent because the durable evidence proves the
	// exact snapshot already landed; it must not attempt a conflicting re-apply.
	again, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{{
		Handle: "worker-1", Digest: changeSet.Digest,
	}})
	if err != nil {
		t.Fatalf("repeat IntegrateChangeSets: %v", err)
	}
	if again.Status != ChangeSetIntegrationApplied || len(again.AppliedFiles) != 2 {
		t.Fatalf("repeat result = %#v", again)
	}
}

func TestRuntimeChangeSetIntegrationRejectsMismatchedDigest(t *testing.T) {
	fixture := newIntegrationFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.worktreeIDDirectory(t), "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := fixture.capture(t, worktreepkg.DefaultChangeSetPatchBytes)
	fixture.saveChild(t, "worker-1", changeSet)

	integrator := &RuntimeChangeSetIntegrator{Runtime: fixture.runtime, Worktrees: fixture.worktrees}
	result, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{{
		Handle: "worker-1", Digest: "sha256:not-the-snapshot",
	}})
	if err != nil {
		t.Fatalf("IntegrateChangeSets: %v", err)
	}
	if result.Status != ChangeSetIntegrationFailed || result.Children[0].Status != ChangeSetIntegrationRejected {
		t.Fatalf("mismatched digest result = %#v", result)
	}
	if readme, _ := os.ReadFile(filepath.Join(fixture.repo, "README.md")); string(readme) != "hello\n" {
		t.Fatalf("base workspace changed after rejected integration: %q", readme)
	}
}

func TestRuntimeChangeSetIntegrationRejectsTruncatedSnapshot(t *testing.T) {
	fixture := newIntegrationFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.worktreeIDDirectory(t), "README.md"), []byte(strings.Repeat("long line\n", 64)), 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := fixture.capture(t, 32)
	if !changeSet.PatchTruncated {
		t.Fatal("fixture did not produce a truncated patch")
	}
	fixture.saveChild(t, "worker-1", changeSet)

	integrator := &RuntimeChangeSetIntegrator{Runtime: fixture.runtime, Worktrees: fixture.worktrees}
	result, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{{
		Handle: "worker-1", Digest: changeSet.Digest,
	}})
	if err != nil {
		t.Fatalf("IntegrateChangeSets: %v", err)
	}
	if result.Status != ChangeSetIntegrationFailed || !strings.Contains(result.Children[0].Error, "truncated") {
		t.Fatalf("truncated snapshot result = %#v", result)
	}
}

func TestRuntimeChangeSetIntegrationRecordsConflict(t *testing.T) {
	fixture := newIntegrationFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.worktreeIDDirectory(t), "README.md"), []byte("hello\nworker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := fixture.capture(t, worktreepkg.DefaultChangeSetPatchBytes)
	fixture.saveChild(t, "worker-1", changeSet)
	if err := os.WriteFile(filepath.Join(fixture.repo, "README.md"), []byte("hello\nlocal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	integrator := &RuntimeChangeSetIntegrator{Runtime: fixture.runtime, Worktrees: fixture.worktrees}
	result, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{{
		Handle: "worker-1", Digest: changeSet.Digest,
	}})
	if err != nil {
		t.Fatalf("IntegrateChangeSets: %v", err)
	}
	if result.Status != ChangeSetIntegrationFailed || result.Children[0].Status != ChangeSetIntegrationConflict {
		t.Fatalf("conflict result = %#v", result)
	}
	record, found, err := session.LoadChildRun(fixture.sessionDir, "integration-session", "worker-1")
	if err != nil || !found || record.IntegrationStatus != ChangeSetIntegrationConflict {
		t.Fatalf("conflict evidence = %#v, found=%t err=%v", record, found, err)
	}
}

func TestRuntimeChangeSetIntegrationRollsBackEarlierSelectionOnConflict(t *testing.T) {
	fixture := newIntegrationFixture(t)
	directory := fixture.worktreeIDDirectory(t)

	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("hello\none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := fixture.capture(t, worktreepkg.DefaultChangeSetPatchBytes)
	fixture.saveChild(t, "worker-1", first)

	// Reset only the mutable child worktree. Both snapshots therefore describe
	// the same base revision but change the same line differently, which makes
	// the second selection conflict after the first was applied.
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("hello\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := fixture.capture(t, worktreepkg.DefaultChangeSetPatchBytes)
	fixture.saveChild(t, "worker-2", second)

	integrator := &RuntimeChangeSetIntegrator{Runtime: fixture.runtime, Worktrees: fixture.worktrees}
	result, err := integrator.IntegrateChangeSets(context.Background(), []agent.ChangeSetSelection{
		{Handle: "worker-1", Digest: first.Digest},
		{Handle: "worker-2", Digest: second.Digest},
	})
	if err != nil {
		t.Fatalf("IntegrateChangeSets: %v", err)
	}
	if result.Status != ChangeSetIntegrationFailed {
		t.Fatalf("integration status = %q, want failed", result.Status)
	}
	if result.Children[0].Status != ChangeSetIntegrationFailed {
		t.Fatalf("first child status = %q, want rolled-back failure", result.Children[0].Status)
	}
	if result.Children[1].Status != ChangeSetIntegrationConflict {
		t.Fatalf("second child status = %q, want conflict", result.Children[1].Status)
	}
	readme, err := os.ReadFile(filepath.Join(fixture.repo, "README.md"))
	if err != nil || string(readme) != "hello\n" {
		t.Fatalf("base workspace after rollback = %q, err=%v", readme, err)
	}
	record, found, err := session.LoadChildRun(fixture.sessionDir, "integration-session", "worker-1")
	if err != nil || !found {
		t.Fatalf("worker-1 history: found=%t err=%v", found, err)
	}
	if record.IntegrationStatus == ChangeSetIntegrationApplied {
		t.Fatalf("rolled-back child retained applied evidence: %#v", record)
	}
}

func (f *integrationFixture) worktreeIDDirectory(t *testing.T) string {
	t.Helper()
	record, err := session.GetWorktreeByID(f.sessionDir, f.worktreeID)
	if err != nil || record.Directory == "" {
		t.Fatalf("worktree %q: err=%v", f.worktreeID, err)
	}
	return record.Directory
}
