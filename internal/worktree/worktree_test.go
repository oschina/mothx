package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
	fn    func(cwd string, args []string) (string, string, int, error)
}

func (f *fakeRunner) run(_ context.Context, cwd string, args ...string) (string, string, int, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	return f.fn(cwd, args)
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Repo Name":   "my-repo-name",
		"  --Weird--  ":  "weird",
		"feature/thing":  "feature-thing",
		"café":           "caf",
		"":               "",
		"already-slug-1": "already-slug-1",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoKeyStableAndDistinct(t *testing.T) {
	a := repoKey("/home/user/projects/mothx")
	b := repoKey("/home/user/projects/mothx")
	c := repoKey("/home/user/projects/other")
	if a != b {
		t.Fatalf("repoKey not deterministic: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("repoKey collision: %q", a)
	}
	if !strings.HasPrefix(a, "mothx-") {
		t.Fatalf("repoKey = %q, want readable prefix", a)
	}
}

func TestParsePorcelain(t *testing.T) {
	text := "worktree /repo\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /wt/one\nHEAD def\nbranch refs/heads/mothx/one\n\n" +
		"worktree /wt/two\nHEAD 123\ndetached\n"
	entries := parsePorcelain(text)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[1].Path != "/wt/one" || entries[1].Branch != "refs/heads/mothx/one" {
		t.Fatalf("entry[1] = %+v", entries[1])
	}
	if !entries[2].Detached {
		t.Fatalf("entry[2] should be detached: %+v", entries[2])
	}
}

func TestSplitRemoteTracking(t *testing.T) {
	remote, branch, ok := splitRemoteTracking("refs/remotes/origin/main")
	if !ok || remote != "origin" || branch != "main" {
		t.Fatalf("got %q %q %v", remote, branch, ok)
	}
	if _, _, ok := splitRemoteTracking("refs/heads/main"); ok {
		t.Fatal("local ref should not be remote tracking")
	}
}

func TestListExcludesPrimary(t *testing.T) {
	fake := &fakeRunner{fn: func(_ string, args []string) (string, string, int, error) {
		return "worktree /repo\nHEAD a\nbranch refs/heads/main\n\n" +
			"worktree /wt/one\nHEAD b\nbranch refs/heads/mothx/one\n", "", 0, nil
	}}
	m := NewManager("/root", "mothx")
	m.run = fake.run
	infos, err := m.List(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "one" || infos[0].Branch != "mothx/one" {
		t.Fatalf("infos = %+v", infos)
	}
}

func TestDefaultBranchFallback(t *testing.T) {
	fake := &fakeRunner{fn: func(_ string, args []string) (string, string, int, error) {
		switch {
		case args[0] == "symbolic-ref":
			return "", "", 1, nil
		case args[0] == "show-ref" && args[len(args)-1] == "refs/heads/main":
			return "", "", 1, nil
		case args[0] == "show-ref" && args[len(args)-1] == "refs/heads/master":
			return "", "", 0, nil
		}
		return "", "", 1, nil
	}}
	m := NewManager("/root", "mothx")
	m.run = fake.run
	ref, err := m.DefaultBranch(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "refs/heads/master" {
		t.Fatalf("ref = %q, want refs/heads/master", ref)
	}
}

func TestDefaultBranchMissing(t *testing.T) {
	fake := &fakeRunner{fn: func(_ string, _ []string) (string, string, int, error) { return "", "", 1, nil }}
	m := NewManager("/root", "mothx")
	m.run = fake.run
	if _, err := m.DefaultBranch(context.Background(), "/repo"); err == nil {
		t.Fatal("expected default_branch_failed")
	}
}

func TestPlanUsesRequestedNameAndDetectsConflict(t *testing.T) {
	root := t.TempDir()
	// Pre-create the requested directory so the first attempt conflicts.
	conflictDir := filepath.Join(root, repoKey("/repo"), "feature")
	if err := os.MkdirAll(conflictDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{fn: func(_ string, args []string) (string, string, int, error) {
		// branchExists -> not found so only the directory conflict matters.
		return "", "", 1, nil
	}}
	m := NewManager(root, "mothx")
	m.run = fake.run
	info, err := m.Plan(context.Background(), "/repo", "feature", false)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == "feature" {
		t.Fatalf("expected a suffixed name on conflict, got %q", info.Name)
	}
	if !strings.HasPrefix(info.Name, "feature-") {
		t.Fatalf("name = %q, want feature-<suffix>", info.Name)
	}
	if info.Branch != "mothx/"+info.Name {
		t.Fatalf("branch = %q", info.Branch)
	}
}

func TestPlanDetachedHasNoBranch(t *testing.T) {
	root := t.TempDir()
	fake := &fakeRunner{fn: func(_ string, _ []string) (string, string, int, error) { return "", "", 1, nil }}
	m := NewManager(root, "mothx")
	m.run = fake.run
	info, err := m.Plan(context.Background(), "/repo", "exp", true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "" {
		t.Fatalf("detached branch = %q, want empty", info.Branch)
	}
}

func TestRepoRootNotGit(t *testing.T) {
	fake := &fakeRunner{fn: func(_ string, _ []string) (string, string, int, error) {
		return "", "fatal: not a git repository", 128, nil
	}}
	m := NewManager("/root", "mothx")
	m.run = fake.run
	_, err := m.RepoRoot(context.Background(), "/tmp")
	var wtErr *Error
	if err == nil || !asWorktreeError(err, &wtErr) || wtErr.Code != CodeNotGit {
		t.Fatalf("err = %v, want not_git", err)
	}
}

func asWorktreeError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestFailedRemovesParsing(t *testing.T) {
	stderr := "warning: failed to remove nested/worktree: Directory not empty\n" +
		"warning: failed to remove 'read only dir': Permission denied\n" +
		"unrelated line\n"
	entries := failedRemoves(stderr)
	if len(entries) != 2 {
		t.Fatalf("entries = %#v, want 2", entries)
	}
	if entries[0] != "nested/worktree" || entries[1] != "read only dir" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestPruneStaysInsideWorktree(t *testing.T) {
	root := t.TempDir()
	wtDir := filepath.Join(root, "wt")
	if err := os.MkdirAll(filepath.Join(wtDir, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(root, "mothx")
	// Entries escaping the worktree must not be pruned.
	m.prune(wtDir, []string{"inside", "../outside", "/"})
	if _, err := os.Stat(filepath.Join(wtDir, "inside")); !os.IsNotExist(err) {
		t.Fatal("inside entry should have been pruned")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory must be preserved: %v", err)
	}
}
