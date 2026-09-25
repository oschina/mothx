package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

func TestIntegrationCreateListResetRemove(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	root := t.TempDir()
	m := NewManager(root, "mothx")
	ctx := context.Background()

	repoRoot, err := m.RepoRoot(ctx, repo)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	info, err := m.Plan(ctx, repoRoot, "feature", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := m.Add(ctx, repoRoot, info); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(info.Directory); err != nil {
		t.Fatalf("worktree directory missing after Add: %v", err)
	}
	if err := m.Populate(ctx, info); err != nil {
		t.Fatalf("Populate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(info.Directory, "README.md")); err != nil {
		t.Fatalf("populated file missing: %v", err)
	}

	infos, err := m.List(ctx, repoRoot)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 || filepath.Clean(infos[0].Directory) != filepath.Clean(info.Directory) {
		t.Fatalf("List = %+v, want the created worktree", infos)
	}

	// Reset discards local changes.
	if err := os.WriteFile(filepath.Join(info.Directory, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Reset(ctx, repoRoot, info.Directory); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(info.Directory, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("after reset README = %q", data)
	}

	if err := m.Remove(ctx, repoRoot, info.Directory, info.Branch); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(info.Directory); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still exists after Remove")
	}
	infos, err = m.List(ctx, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("List after remove = %+v, want empty", infos)
	}
}

func TestIntegrationRepoRootFromSubdir(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	sub := filepath.Join(repo, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(t.TempDir(), "mothx")
	root, err := m.RepoRoot(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(root) != filepath.Clean(repo) {
		t.Fatalf("RepoRoot(subdir) = %q, want %q", root, repo)
	}
}
