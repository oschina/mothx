package agentruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func initWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	return dir
}

func newTestWorktreeManager(t *testing.T, sessionDir string) (*WorktreeManager, *[]WorktreeEvent) {
	t.Helper()
	events := &[]WorktreeEvent{}
	mgr, err := NewWorktreeManager(WorktreeManagerOptions{
		SessionDir: sessionDir,
		Root:       t.TempDir(),
		EventSink:  WorktreeEventSinkFunc(func(e WorktreeEvent) { *events = append(*events, e) }),
	})
	if err != nil {
		t.Fatalf("NewWorktreeManager: %v", err)
	}
	return mgr, events
}

func TestWorktreeManagerCreateAuthorizesAndPopulates(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, events := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{BaseCwd: repo, Name: "task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != session.WorktreeStatusPending {
		t.Fatalf("created status = %q, want pending", created.Status)
	}
	// The directory exists and is authorized synchronously, before population.
	if _, err := os.Stat(created.Directory); err != nil {
		t.Fatalf("worktree directory missing: %v", err)
	}
	if !mgr.IsAuthorized(created.Directory) {
		t.Fatal("worktree directory was not authorized")
	}

	mgr.Wait()
	final, err := mgr.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != session.WorktreeStatusReady {
		t.Fatalf("final status = %q, want ready", final.Status)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, "README.md")); err != nil {
		t.Fatalf("worktree was not populated: %v", err)
	}

	types := make([]string, 0, len(*events))
	for _, e := range *events {
		types = append(types, e.Type)
	}
	if !contains(types, WorktreeEventPending) || !contains(types, WorktreeEventReady) {
		t.Fatalf("events = %v, want pending and ready", types)
	}
}

func TestWorktreeManagerListReconcilesRemovedWorktree(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{BaseCwd: repo, Name: "recon"})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Wait()

	list, err := mgr.List(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("List = %+v", list)
	}

	// Remove the worktree out of band; List must reconcile the registry row.
	cmd := exec.Command("git", "worktree", "remove", "--force", created.Directory)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("out-of-band remove: %v\n%s", err, out)
	}
	list, err = mgr.List(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("List after out-of-band remove = %+v, want empty", list)
	}
	reconciled, err := mgr.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Status != session.WorktreeStatusRemoved {
		t.Fatalf("reconciled status = %q, want removed", reconciled.Status)
	}
}

func TestWorktreeManagerListExternal(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)

	// Create an external worktree by hand.
	external := filepath.Join(t.TempDir(), "external")
	cmd := exec.Command("git", "worktree", "add", "--detach", external)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external worktree: %v\n%s", err, out)
	}

	list, err := mgr.List(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].External {
		t.Fatalf("List = %+v, want one external item", list)
	}
}

func TestWorktreeManagerRemoveDeletesAndDeauthorizes(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{BaseCwd: repo, Name: "gone"})
	if err != nil {
		t.Fatal(err)
	}
	mgr.Wait()

	if err := mgr.Remove(context.Background(), created.ID, ""); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(created.Directory); !os.IsNotExist(err) {
		t.Fatalf("directory still exists after Remove")
	}
	if mgr.IsAuthorized(created.Directory) {
		t.Fatal("directory still authorized after Remove")
	}
	if got, _ := mgr.Get(context.Background(), created.ID); got.ID != "" {
		t.Fatalf("registry row still present after Remove: %+v", got)
	}
}

func TestWorktreeManagerCreateNotGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)
	_, err := mgr.Create(context.Background(), CreateWorktreeRequest{BaseCwd: t.TempDir()})
	if err == nil {
		t.Fatal("expected not_git error")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestWorktreeManagerRunsStartCommand(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{
		BaseCwd: repo, Name: "started", StartCommand: "printf ready > marker.txt",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mgr.Wait()
	final, err := mgr.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != session.WorktreeStatusReady {
		t.Fatalf("status = %q, want ready", final.Status)
	}
	if _, err := os.Stat(filepath.Join(created.Directory, "marker.txt")); err != nil {
		t.Fatalf("start command did not run: %v", err)
	}
}

func TestWorktreeManagerStartCommandFailureMarksFailed(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, events := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{
		BaseCwd: repo, Name: "failing", StartCommand: "exit 3",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mgr.Wait()
	final, err := mgr.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != session.WorktreeStatusFailed {
		t.Fatalf("status = %q, want failed", final.Status)
	}
	if final.Error == "" {
		t.Fatal("failed status must carry the start command error")
	}
	if !contains(eventTypes(*events), WorktreeEventFailed) {
		t.Fatalf("expected a failed event, got %v", *events)
	}
}

func eventTypes(events []WorktreeEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

func TestWorktreeProviderAdapterResolve(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)
	provider := NewWorktreeProviderAdapter(mgr)

	dir, err := provider.ResolveWorktree(repo, agent.WorktreeSpec{Name: "child"})
	if err != nil {
		t.Fatalf("ResolveWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("resolved worktree is not populated: %v", err)
	}
	if !mgr.IsAuthorized(dir) {
		t.Fatal("resolved worktree directory was not authorized")
	}
}

func TestWorktreeProviderAdapterReuseSharesDirectory(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)
	provider := NewWorktreeProviderAdapter(mgr)

	first, err := provider.ResolveWorktree(repo, agent.WorktreeSpec{Name: "objective-1", Reuse: true})
	if err != nil {
		t.Fatalf("first reuse: %v", err)
	}
	second, err := provider.ResolveWorktree(repo, agent.WorktreeSpec{Name: "objective-1", Reuse: true})
	if err != nil {
		t.Fatalf("second reuse: %v", err)
	}
	if first != second {
		t.Fatalf("reuse returned different directories: %q vs %q", first, second)
	}
}

func TestWorktreeManagerWaitForReadyFailure(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	mgr, _ := newTestWorktreeManager(t, sessionDir)

	created, err := mgr.Create(context.Background(), CreateWorktreeRequest{BaseCwd: repo, Name: "bad", StartCommand: "exit 1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.WaitForReady(context.Background(), created.ID); err == nil {
		t.Fatal("WaitForReady must fail for a failed worktree")
	}
}

func TestNewDefaultWorktreeManagerFromSettings(t *testing.T) {
	disabled := &config.Settings{Worktree: config.WorktreeSettings{Enabled: boolPointer(false)}}
	if _, err := NewDefaultWorktreeManager(disabled); err == nil {
		t.Fatal("disabled worktrees must not build a manager")
	}
	if _, err := NewDefaultWorktreeManager(nil); err == nil {
		t.Fatal("nil settings must not build a manager")
	}

	sessionDir := t.TempDir()
	enabled := &config.Settings{
		SessionDir: sessionDir,
		Worktree:   config.WorktreeSettings{Enabled: boolPointer(true), BranchPrefix: "wt-"},
	}
	mgr, err := NewDefaultWorktreeManager(enabled)
	if err != nil {
		t.Fatalf("NewDefaultWorktreeManager: %v", err)
	}
	if mgr == nil || mgr.Root() == "" {
		t.Fatalf("manager root = %q", mgr.Root())
	}
}

func boolPointer(v bool) *bool { return &v }
