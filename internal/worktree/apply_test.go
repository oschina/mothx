package worktree

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func changeSetWithUntracked(path, content string) ChangeSet {
	digest := sha256Hex([]byte(content))
	changeSet := ChangeSet{
		Version: 1,
		Files: []ChangeFile{
			{Path: path, Status: "untracked"},
		},
		UntrackedFiles: []UntrackedFile{{
			Path: path, Size: int64(len(content)), Digest: digest,
			ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
		}},
	}
	changeSet.Digest = changeSetDigest(changeSet)
	return changeSet
}

func TestApplyChangeSetAppliesTrackedAndUntrackedFiles(t *testing.T) {
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
	changeSet, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, DefaultChangeSetPatchBytes)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ApplyChangeSet(context.Background(), repo, changeSet)
	if err != nil {
		t.Fatalf("ApplyChangeSet: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(readme) != "hello\nchanged\n" {
		t.Fatalf("README after apply = %q, err=%v", readme, err)
	}
	newFile, err := os.ReadFile(filepath.Join(repo, "new.txt"))
	if err != nil || string(newFile) != "new file\n" {
		t.Fatalf("new.txt after apply = %q, err=%v", newFile, err)
	}
	if len(result.AppliedFiles) != 2 {
		t.Fatalf("applied files = %#v, want README.md and new.txt", result.AppliedFiles)
	}

	// The snapshot is immutable evidence, not an idempotent patch source. A
	// second apply must fail instead of silently duplicating the change.
	if _, err := ApplyChangeSet(context.Background(), repo, changeSet); err == nil {
		t.Fatal("applying the same tracked snapshot twice succeeded")
	} else {
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != CodeApplyConflict {
			t.Fatalf("second apply error = %v, want %s", err, CodeApplyConflict)
		}
	}
}

func TestApplyChangeSetRejectsExistingUntrackedFileWithDifferentContent(t *testing.T) {
	repo := initChangeSetRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changeSet := changeSetWithUntracked("new.txt", "worker content\n")
	_, err := ApplyChangeSet(context.Background(), repo, changeSet)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeApplyConflict {
		t.Fatalf("apply error = %v, want %s", err, CodeApplyConflict)
	}
	current, _ := os.ReadFile(filepath.Join(repo, "new.txt"))
	if string(current) != "someone else\n" {
		t.Fatalf("existing file was overwritten: %q", current)
	}
}

func TestApplyChangeSetRejectsSymlinkEscape(t *testing.T) {
	repo := initChangeSetRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "linked")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	changeSet := changeSetWithUntracked("linked/pwned.txt", "escaped\n")
	_, err := ApplyChangeSet(context.Background(), repo, changeSet)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeApplyConflict {
		t.Fatalf("apply error = %v, want %s", err, CodeApplyConflict)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("snapshot escaped the integration worktree: %v", statErr)
	}
}

func TestApplyChangeSetRejectsTruncatedSnapshot(t *testing.T) {
	repo := initChangeSetRepo(t)
	changeSet := changeSetWithUntracked("new.txt", "content\n")
	changeSet.UntrackedTruncated = true
	changeSet.Digest = changeSetDigest(changeSet)
	_, err := ApplyChangeSet(context.Background(), repo, changeSet)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeChangeSetFailed {
		t.Fatalf("apply error = %v, want %s", err, CodeChangeSetFailed)
	}
}

// TestApplyChangeSetReportsAppliedPatchWhenUntrackedWriteFails locks in the
// ordering contract the Runtime's rollback path depends on: the tracked patch
// is applied before the untracked files are written, so a failure while
// writing an untracked file must still report PatchApplied. Otherwise the
// caller would skip the rollback and leave a half-applied snapshot behind.
func TestApplyChangeSetReportsAppliedPatchWhenUntrackedWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		// A read-only directory does not stop root, so the write would
		// succeed and the test would prove nothing.
		t.Skip("test requires a non-root user")
	}
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
	changeSet, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, DefaultChangeSetPatchBytes)
	if err != nil {
		t.Fatal(err)
	}

	// Make the untracked write fail after the tracked patch has already been
	// applied: the parent directory exists but is not writable, so creating
	// the staged temporary file inside it fails.
	if err := os.Mkdir(filepath.Join(repo, "blocked"), 0o500); err != nil {
		t.Fatal(err)
	}
	blocked := changeSetWithUntracked("blocked/child.txt", "worker content\n")
	blocked.Patch = changeSet.Patch
	blocked.BaseRevision = changeSet.BaseRevision
	blocked.HeadRevision = changeSet.HeadRevision
	blocked.Files = changeSet.Files
	blocked.Digest = changeSetDigest(blocked)

	applied, err := ApplyChangeSet(context.Background(), repo, blocked)
	if err == nil {
		t.Fatal("ApplyChangeSet succeeded, want an untracked write failure")
	}
	if !applied.PatchApplied {
		t.Fatalf("apply metadata = %#v, want PatchApplied so the caller can roll back", applied)
	}
}

func TestRollbackChangeSetRestoresTrackedAndRemovesCreatedUntracked(t *testing.T) {
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
	changeSet, err := manager.CaptureChangeSet(context.Background(), info.Directory, base, DefaultChangeSetPatchBytes)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyChangeSet(context.Background(), repo, changeSet)
	if err != nil {
		t.Fatalf("ApplyChangeSet: %v", err)
	}
	if !applied.PatchApplied || len(applied.CreatedUntracked) != 1 {
		t.Fatalf("apply metadata = %#v", applied)
	}

	if err := RollbackChangeSet(context.Background(), repo, changeSet, applied); err != nil {
		t.Fatalf("RollbackChangeSet: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil || string(readme) != "hello\n" {
		t.Fatalf("README after rollback = %q, err=%v", readme, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("created untracked file survived rollback: %v", err)
	}
}

func TestRollbackChangeSetRefusesToDeleteModifiedUntrackedFile(t *testing.T) {
	repo := initChangeSetRepo(t)
	changeSet := changeSetWithUntracked("new.txt", "worker content\n")
	applied, err := ApplyChangeSet(context.Background(), repo, changeSet)
	if err != nil {
		t.Fatalf("ApplyChangeSet: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = RollbackChangeSet(context.Background(), repo, changeSet, applied)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != CodeApplyConflict {
		t.Fatalf("rollback error = %v, want %s", err, CodeApplyConflict)
	}
	if content, _ := os.ReadFile(filepath.Join(repo, "new.txt")); string(content) != "local edit\n" {
		t.Fatalf("rollback removed a locally modified file: %q", content)
	}
}

func TestChangeSetVerifyRejectsTamperedContentAndUnsafePaths(t *testing.T) {
	valid := changeSetWithUntracked("new.txt", "content\n")
	if err := valid.Verify(); err != nil {
		t.Fatalf("valid change set: %v", err)
	}

	tampered := valid
	tampered.UntrackedFiles = append([]UntrackedFile(nil), valid.UntrackedFiles...)
	tampered.UntrackedFiles[0].ContentBase64 = base64.StdEncoding.EncodeToString([]byte("different\n"))
	if err := tampered.Verify(); err == nil {
		t.Fatal("tampered untracked content passed verification")
	}

	for _, path := range []string{"../escape", "/absolute", "a/../../escape"} {
		changeSet := changeSetWithUntracked(path, "content\n")
		if err := changeSet.Verify(); err == nil {
			t.Fatalf("unsafe path %q passed verification", path)
		}
	}
}
