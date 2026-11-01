package serve

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitPorcelainZ(t *testing.T) {
	out := "M  src/a.go\x00 M src/b.go\x00?? src/new.txt\x00R  src/newname.go\x00src/oldname.go\x00"
	entries := parseGitPorcelainZ(out)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].index != 'M' || entries[0].worktree != ' ' || entries[0].path != "src/a.go" {
		t.Errorf("staged modify parsed wrong: %+v", entries[0])
	}
	if entries[1].index != ' ' || entries[1].worktree != 'M' || entries[1].path != "src/b.go" {
		t.Errorf("worktree modify parsed wrong: %+v", entries[1])
	}
	if entries[2].index != '?' || entries[2].path != "src/new.txt" {
		t.Errorf("untracked parsed wrong: %+v", entries[2])
	}
	if entries[3].index != 'R' || entries[3].path != "src/newname.go" || entries[3].oldPath != "src/oldname.go" {
		t.Errorf("rename parsed wrong: %+v", entries[3])
	}
}

func TestParseGitNumstat(t *testing.T) {
	out := "3\t1\tf.go\x004\t0\t\x00src/oldname.go\x00src/newname.go\x00-\t-\tsrc/bin.dat\x00"
	stats := parseGitNumstat(out)
	if got := stats["f.go"]; got[0] != 3 || got[1] != 1 {
		t.Errorf("f.go stats = %v", got)
	}
	if got := stats["src/newname.go"]; got[0] != 4 || got[1] != 0 {
		t.Errorf("renamed stats = %v", got)
	}
	if _, ok := stats["src/oldname.go"]; ok {
		t.Errorf("old rename path leaked into stats map")
	}
	if got := stats["src/bin.dat"]; got[0] != 0 || got[1] != 0 {
		t.Errorf("binary stats = %v", got)
	}
}

func TestCollectGitChangesRealRepo(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary not available")
	}
	_ = gitPath
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.go")
	run("commit", "-m", "init")
	// staged modification
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package a\n\nvar X int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked.go")
	// unstaged modification: change it again after staging
	if err := os.WriteFile(filepath.Join(dir, "tracked.go"), []byte("package a\n\nvar X int\nvar Y int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// untracked file
	if err := os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := collectGitChanges(context.Background(), dir)
	if !resp.Repo {
		t.Fatalf("expected repo, error=%q", resp.Error)
	}
	if resp.Branch != "main" {
		t.Errorf("branch = %q", resp.Branch)
	}
	var stagedMod, workMod, untracked bool
	for _, f := range resp.Files {
		switch {
		case f.Path == "tracked.go" && f.Staged && f.Status == "M":
			stagedMod = true
			if f.Added < 1 {
				t.Errorf("staged modify missing added stat: %+v", f)
			}
		case f.Path == "tracked.go" && !f.Staged && f.Status == "M":
			workMod = true
		case f.Path == "fresh.txt" && f.Status == "?":
			untracked = true
		}
	}
	if !stagedMod || !workMod || !untracked {
		t.Fatalf("missing entries staged=%v work=%v untracked=%v files=%+v", stagedMod, workMod, untracked, resp.Files)
	}
	if resp.Staged != 1 || resp.Unstaged != 1 || resp.Untracked != 1 {
		t.Errorf("counts staged=%d unstaged=%d untracked=%d", resp.Staged, resp.Unstaged, resp.Untracked)
	}
}

func TestCollectGitChangesNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	resp := collectGitChanges(context.Background(), t.TempDir())
	if resp.Repo {
		t.Fatalf("expected non-repo result")
	}
	if len(resp.Files) != 0 {
		t.Fatalf("expected no files")
	}
}

func TestGitSessionWorkDirValidation(t *testing.T) {
	req, _ := http.NewRequest("GET", "/api/git/changes", nil)
	if _, problem := sessionWorkDirTarget(nil, req); problem == "" {
		t.Errorf("expected missing-parameter error")
	}
	req, _ = http.NewRequest("GET", "/api/git/changes?sessionId=missing", nil)
	if _, problem := sessionWorkDirTarget(nil, req); problem != "session not found" {
		t.Errorf("unknown sessionId without a manager = %q", problem)
	}
	req, _ = http.NewRequest("GET", "/api/git/changes?path=relative/dir", nil)
	dir, problem := sessionWorkDirTarget(nil, req)
	if problem != "" {
		t.Fatalf("unexpected problem: %v", problem)
	}
	if !filepath.IsAbs(dir) || !strings.HasSuffix(dir, "relative/dir") {
		t.Errorf("abs resolution = %q", dir)
	}
}
