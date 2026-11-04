package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ApplyResult reports the paths a Runtime-owned integration actually changed.
type ApplyResult struct {
	AppliedFiles     []string
	PatchApplied     bool
	CreatedUntracked []string
}

// ApplyChangeSet applies one verified change-set snapshot to a repository
// worktree. It never uses the mutable child worktree: the patch and untracked
// bytes in the snapshot are the only inputs. The tracked patch is dry-run before
// mutation; untracked paths are resolved component-by-component with a symlink
// fence so a snapshot can never redirect a write outside root.
func ApplyChangeSet(ctx context.Context, root string, changeSet ChangeSet) (ApplyResult, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." || !filepath.IsAbs(root) {
		return ApplyResult{}, &Error{Code: CodeChangeSetFailed, Message: "integration worktree must be an absolute path"}
	}
	if err := changeSet.Verify(); err != nil {
		return ApplyResult{}, err
	}
	if changeSet.PatchTruncated || changeSet.UntrackedTruncated {
		return ApplyResult{}, &Error{Code: CodeChangeSetFailed, Message: "a truncated change set cannot be integrated"}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return ApplyResult{}, &Error{Code: CodeChangeSetFailed, Message: "integration worktree is not a directory"}
	}

	targets := make([]applyTarget, 0, len(changeSet.UntrackedFiles))
	for _, file := range changeSet.UntrackedFiles {
		target, err := prepareApplyTarget(root, file)
		if err != nil {
			return ApplyResult{}, err
		}
		targets = append(targets, target)
	}

	result := ApplyResult{}
	if strings.TrimSpace(changeSet.Patch) != "" {
		if err := runGitApply(ctx, root, changeSet.Patch, true); err != nil {
			return result, err
		}
		if err := runGitApply(ctx, root, changeSet.Patch, false); err != nil {
			return result, err
		}
		result.PatchApplied = true
	}
	for _, file := range changeSet.Files {
		// Untracked entries are installed from their bounded content below; the
		// status list must not report them twice.
		if file.Status == "untracked" {
			continue
		}
		result.AppliedFiles = append(result.AppliedFiles, file.Path)
	}
	for _, target := range targets {
		applied, err := writeApplyTarget(root, target)
		if err != nil {
			return result, err
		}
		if applied {
			result.AppliedFiles = append(result.AppliedFiles, target.relative)
			if !target.exists {
				result.CreatedUntracked = append(result.CreatedUntracked, target.relative)
			}
		}
	}
	return result, nil
}

// RollbackChangeSet reverses one ApplyChangeSet that already ran against root.
// It is used when a later selection in the same integration request conflicts:
// the Runtime must not leave a partially applied multi-selection behind. The
// function re-verifies created untracked bytes before deleting them and never
// removes a file whose content changed after integration.
func RollbackChangeSet(ctx context.Context, root string, changeSet ChangeSet, applied ApplyResult) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." || !filepath.IsAbs(root) {
		return &Error{Code: CodeChangeSetFailed, Message: "integration worktree must be an absolute path"}
	}
	if err := changeSet.Verify(); err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return &Error{Code: CodeChangeSetFailed, Message: "integration worktree is not a directory"}
	}

	var failures []string
	if applied.PatchApplied && strings.TrimSpace(changeSet.Patch) != "" {
		if err := runGitApplyReverse(ctx, root, changeSet.Patch, true); err != nil {
			failures = append(failures, err.Error())
		} else if err := runGitApplyReverse(ctx, root, changeSet.Patch, false); err != nil {
			failures = append(failures, err.Error())
		}
	}

	expected := make(map[string]string, len(changeSet.UntrackedFiles))
	for _, file := range changeSet.UntrackedFiles {
		expected[file.Path] = file.Digest
	}
	for index := len(applied.CreatedUntracked) - 1; index >= 0; index-- {
		relative := applied.CreatedUntracked[index]
		digest, ok := expected[relative]
		if !ok {
			failures = append(failures, fmt.Sprintf("untracked path %q is not in the snapshot", relative))
			continue
		}
		path, err := resolveApplyPath(root, relative)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("stat rollback path %q: %v", relative, err))
			continue
		}
		if !info.Mode().IsRegular() {
			failures = append(failures, fmt.Sprintf("rollback path %q is not a regular file", relative))
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("read rollback path %q: %v", relative, err))
			continue
		}
		sum := sha256.Sum256(content)
		if "sha256:"+hex.EncodeToString(sum[:]) != digest {
			failures = append(failures, fmt.Sprintf("untracked path %q changed after integration", relative))
			continue
		}
		if err := os.Remove(path); err != nil {
			failures = append(failures, fmt.Sprintf("remove rollback path %q: %v", relative, err))
		}
	}
	if len(failures) > 0 {
		return &Error{Code: CodeApplyConflict, Message: "rollback change set: " + strings.Join(failures, "; ")}
	}
	return nil
}

type applyTarget struct {
	relative string
	path     string
	content  []byte
	digest   string
	exists   bool
}

func prepareApplyTarget(root string, file UntrackedFile) (applyTarget, error) {
	if file.ContentTruncated {
		return applyTarget{}, &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("untracked path %q is truncated", file.Path)}
	}
	content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
	if err != nil {
		return applyTarget{}, &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("decode untracked path %q: %v", file.Path, err)}
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if int64(len(content)) != file.Size || digest != file.Digest {
		return applyTarget{}, &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("untracked path %q content does not match its snapshot", file.Path)}
	}
	path, err := resolveApplyPath(root, file.Path)
	if err != nil {
		return applyTarget{}, err
	}
	target := applyTarget{relative: filepath.ToSlash(file.Path), path: path, content: content, digest: digest}
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil && info.Mode()&os.ModeSymlink != 0:
		return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("untracked path %q is a symlink", file.Path)}
	case statErr == nil && info.IsDir():
		return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("untracked path %q is a directory", file.Path)}
	case statErr == nil && !info.Mode().IsRegular():
		return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("untracked path %q is not a regular file", file.Path)}
	case statErr == nil:
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("read existing untracked path %q: %v", file.Path, readErr)}
		}
		existingSum := sha256.Sum256(existing)
		if "sha256:"+hex.EncodeToString(existingSum[:]) != digest {
			return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("untracked path %q already exists with different content", file.Path)}
		}
		target.exists = true
	case !os.IsNotExist(statErr):
		return applyTarget{}, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("stat untracked path %q: %v", file.Path, statErr)}
	}
	return target, nil
}

func resolveApplyPath(root, relative string) (string, error) {
	if err := validateChangeSetPath(relative); err != nil {
		return "", &Error{Code: CodeChangeSetFailed, Message: err.Error()}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("resolve integration worktree: %v", err)}
	}
	current := resolvedRoot
	parts := strings.Split(filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))), "/")
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("stat integration path %q: %v", relative, statErr)}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("integration path %q traverses a symlink", relative)}
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("integration path %q has a non-directory parent", relative)}
		}
	}
	rel, err := filepath.Rel(resolvedRoot, current)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("integration path %q escapes the worktree", relative)}
	}
	return current, nil
}

func writeApplyTarget(root string, target applyTarget) (bool, error) {
	if target.exists {
		return false, nil
	}
	parent := filepath.Dir(target.path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("create parent for %q: %v", target.relative, err)}
	}
	// Re-resolve after MkdirAll: a concurrently created symlink must not turn the
	// final write into an escape from the integration worktree.
	resolved, err := resolveApplyPath(root, target.relative)
	if err != nil {
		return false, err
	}
	if resolved != target.path {
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("integration path %q changed during apply", target.relative)}
	}
	temp, err := os.CreateTemp(parent, ".mothx-integrate-*")
	if err != nil {
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("create integration file %q: %v", target.relative, err)}
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, bytes.NewReader(target.content)); err != nil {
		temp.Close()
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("write integration file %q: %v", target.relative, err)}
	}
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("chmod integration file %q: %v", target.relative, err)}
	}
	if err := temp.Close(); err != nil {
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("close integration file %q: %v", target.relative, err)}
	}
	if err := os.Rename(tempName, target.path); err != nil {
		return false, &Error{Code: CodeApplyConflict, Message: fmt.Sprintf("install integration file %q: %v", target.relative, err)}
	}
	return true, nil
}

func runGitApply(ctx context.Context, root, patch string, check bool) error {
	return runGitApplyMode(ctx, root, patch, check, false)
}

func runGitApplyReverse(ctx context.Context, root, patch string, check bool) error {
	return runGitApplyMode(ctx, root, patch, check, true)
}

func runGitApplyMode(ctx context.Context, root, patch string, check, reverse bool) error {
	args := []string{"apply", "--binary", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--reverse")
	}
	if check {
		args = append(args, "--check")
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		code := CodeApplyFailed
		if check {
			code = CodeApplyConflict
		}
		return &Error{Code: code, Message: message}
	}
	return nil
}
