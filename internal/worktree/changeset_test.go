package worktree

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func initChangeSetRepo(t *testing.T) string {
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

func materializeChangeSetWorktree(t *testing.T, repo string) (*Manager, Info) {
	t.Helper()
	manager := NewManager(t.TempDir(), "mothx")
	info, err := manager.Plan(context.Background(), repo, "changes", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Add(context.Background(), repo, info); err != nil {
		t.Fatal(err)
	}
	if err := manager.Populate(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	return manager, info
}

func TestCaptureChangeSetRecordsTrackedAndUntrackedFiles(t *testing.T) {
	repo := initChangeSetRepo(t)
	manager, info := materializeChangeSetWorktree(t, repo)
	base, err := manager.Revision(context.Background(), info.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Directory, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Directory, "new.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := []byte{0x00, 0x01, 0x02, 0xff}
	if err := os.WriteFile(filepath.Join(info.Directory, "blob.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}

	changeSet, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, DefaultChangeSetPatchBytes)
	if err != nil {
		t.Fatalf("CaptureChangeSet: %v", err)
	}
	if changeSet.BaseRevision != base || changeSet.HeadRevision != base {
		t.Fatalf("revisions = base %q head %q, want %q", changeSet.BaseRevision, changeSet.HeadRevision, base)
	}
	if !strings.HasPrefix(changeSet.Digest, "sha256:") {
		t.Fatalf("digest = %q", changeSet.Digest)
	}
	if len(changeSet.Files) != 3 {
		t.Fatalf("files = %#v, want README/new.txt/blob.bin", changeSet.Files)
	}
	statuses := map[string]string{}
	for _, file := range changeSet.Files {
		statuses[file.Path] = file.Status
	}
	if statuses["README.md"] != "modified" || statuses["new.txt"] != "untracked" || statuses["blob.bin"] != "untracked" {
		t.Fatalf("statuses = %#v", statuses)
	}
	if !strings.Contains(changeSet.Patch, "README.md") || !strings.Contains(changeSet.Patch, "changed") {
		t.Fatalf("tracked patch did not include the modified file:\n%s", changeSet.Patch)
	}
	untracked := map[string]UntrackedFile{}
	for _, file := range changeSet.UntrackedFiles {
		untracked[file.Path] = file
	}
	newFile, ok := untracked["new.txt"]
	if !ok || newFile.Binary {
		t.Fatalf("new.txt metadata = %#v", newFile)
	}
	decoded, err := base64.StdEncoding.DecodeString(newFile.ContentBase64)
	if err != nil || string(decoded) != "new file\n" {
		t.Fatalf("new.txt content = %q, err=%v", decoded, err)
	}
	blob, ok := untracked["blob.bin"]
	if !ok || !blob.Binary {
		t.Fatalf("blob.bin metadata = %#v", blob)
	}
	decodedBlob, err := base64.StdEncoding.DecodeString(blob.ContentBase64)
	if err != nil || string(decodedBlob) != string(binary) {
		t.Fatalf("blob.bin content = %v, err=%v", decodedBlob, err)
	}
}

func TestCaptureChangeSetIsDeterministicAndBounded(t *testing.T) {
	repo := initChangeSetRepo(t)
	manager, info := materializeChangeSetWorktree(t, repo)
	base, err := manager.Revision(context.Background(), info.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Directory, "README.md"), []byte(strings.Repeat("long line\n", 32)), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, 24)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, 24)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("change-set digest changed between identical captures: %q != %q", first.Digest, second.Digest)
	}
	if !first.PatchTruncated {
		t.Fatal("a small patch limit must mark the patch truncated")
	}
	if len(first.Patch) > 24 || !utf8.ValidString(first.Patch) {
		t.Fatalf("truncated patch = %q (%d bytes)", first.Patch, len(first.Patch))
	}
}

func TestCaptureChangeSetRejectsUnmanagedDirectory(t *testing.T) {
	repo := initChangeSetRepo(t)
	manager := NewManager(t.TempDir(), "mothx")
	if _, err := manager.CaptureChangeSet(context.Background(), repo, "", DefaultChangeSetPatchBytes); err == nil {
		t.Fatal("capture outside the managed worktree root was accepted")
	}
}

func TestCaptureChangeSetMarksUncapturableUntrackedPathsTruncated(t *testing.T) {
	repo := initChangeSetRepo(t)
	manager, info := materializeChangeSetWorktree(t, repo)
	base, err := manager.Revision(context.Background(), info.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", filepath.Join(info.Directory, "untracked-link")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	changeSet, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, DefaultChangeSetPatchBytes)
	if err != nil {
		t.Fatalf("CaptureChangeSet: %v", err)
	}
	if !changeSet.UntrackedTruncated {
		t.Fatal("an uncapturable untracked path was silently omitted from a complete-looking snapshot")
	}
}
