package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// DefaultChangeSetPatchBytes bounds the patch retained in one immutable
// change-set snapshot. A truncated patch is still useful for review, but it is
// never safe to integrate without the complete snapshot.
const DefaultChangeSetPatchBytes = 512 << 10

// DefaultChangeSetUntrackedBytes bounds the raw content retained for untracked
// files. Git's patch format already handles tracked binary files.
const DefaultChangeSetUntrackedBytes = 512 << 10

const maxChangeSetStatusBytes = 4 << 20

// ChangeFile is one tracked path in a change-set snapshot.
type ChangeFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	OriginalPath string `json:"originalPath,omitempty"`
}

// UntrackedFile carries bounded content for a file git does not track. The
// content is base64 encoded so arbitrary bytes survive JSON persistence.
type UntrackedFile struct {
	Path             string `json:"path"`
	Size             int64  `json:"size"`
	Digest           string `json:"digest"`
	ContentBase64    string `json:"contentBase64,omitempty"`
	ContentTruncated bool   `json:"contentTruncated,omitempty"`
	Binary           bool   `json:"binary,omitempty"`
}

// ChangeSet is an immutable, bounded snapshot of a managed worktree relative
// to a recorded base revision. It is a review/integration input, not an
// artifact and never an authorization source.
type ChangeSet struct {
	Version            int             `json:"version"`
	BaseRevision       string          `json:"baseRevision"`
	HeadRevision       string          `json:"headRevision"`
	Digest             string          `json:"digest"`
	Files              []ChangeFile    `json:"files"`
	UntrackedFiles     []UntrackedFile `json:"untrackedFiles,omitempty"`
	Patch              string          `json:"patch,omitempty"`
	PatchTruncated     bool            `json:"patchTruncated,omitempty"`
	UntrackedTruncated bool            `json:"untrackedTruncated,omitempty"`
}

// Verify checks the immutable snapshot's digest and path/content integrity.
// Callers must also reject a truncated patch or untracked snapshot before
// applying it; truncation is explicit evidence, not a mergeable change.
func (c ChangeSet) Verify() error {
	if c.Version != 1 {
		return &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("unsupported change-set version %d", c.Version)}
	}
	if strings.TrimSpace(c.Digest) == "" {
		return &Error{Code: CodeChangeSetFailed, Message: "change set has no digest"}
	}
	for _, file := range c.Files {
		if err := validateChangeSetPath(file.Path); err != nil {
			return fmt.Errorf("change-set path %q: %w", file.Path, err)
		}
		if file.OriginalPath != "" {
			if err := validateChangeSetPath(file.OriginalPath); err != nil {
				return fmt.Errorf("change-set original path %q: %w", file.OriginalPath, err)
			}
		}
	}
	seen := make(map[string]struct{}, len(c.UntrackedFiles))
	for _, file := range c.UntrackedFiles {
		if err := validateChangeSetPath(file.Path); err != nil {
			return fmt.Errorf("untracked change-set path %q: %w", file.Path, err)
		}
		if _, duplicate := seen[file.Path]; duplicate {
			return &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("untracked path %q appears twice", file.Path)}
		}
		seen[file.Path] = struct{}{}
		if file.ContentTruncated {
			continue
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("decode untracked path %q: %v", file.Path, err)}
		}
		if int64(len(content)) != file.Size || sha256Hex(content) != file.Digest {
			return &Error{Code: CodeChangeSetFailed, Message: fmt.Sprintf("untracked path %q content does not match its snapshot", file.Path)}
		}
	}
	if expected := changeSetDigest(c); expected != c.Digest {
		return &Error{Code: CodeChangeSetFailed, Message: "change-set digest does not match its content"}
	}
	return nil
}

func validateChangeSetPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsRune(path, 0) {
		return errors.New("path is empty or contains NUL")
	}
	if filepath.IsAbs(filepath.FromSlash(path)) {
		return errors.New("path must be relative")
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New("path escapes the worktree")
	}
	return nil
}

// Revision resolves the commit checked out by a worktree or repository.
func (m *Manager) Revision(ctx context.Context, directory string) (string, error) {
	if strings.TrimSpace(directory) == "" {
		return "", &Error{Code: CodeRevisionFailed, Message: "worktree directory is required"}
	}
	out, stderr, code, err := m.run(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", &Error{Code: CodeRevisionFailed, Message: firstNonEmpty(stderr, out, "failed to resolve worktree revision")}
	}
	revision := strings.TrimSpace(out)
	if revision == "" {
		return "", &Error{Code: CodeRevisionFailed, Message: "git returned an empty revision"}
	}
	return revision, nil
}

// CaptureChangeSet snapshots tracked changes and bounded untracked content for
// a managed worktree. directory must live under the Manager root; callers must
// already have proved the directory is a registered worktree.
func (m *Manager) CaptureChangeSet(ctx context.Context, directory, baseRevision string, patchLimit int) (ChangeSet, error) {
	directory = filepath.Clean(strings.TrimSpace(directory))
	if directory == "" || directory == "." {
		return ChangeSet{}, &Error{Code: CodeChangeSetFailed, Message: "worktree directory is required"}
	}
	if !m.underRoot(directory) {
		return ChangeSet{}, &Error{Code: CodeChangeSetFailed, Message: "change-set capture is limited to managed worktrees"}
	}
	if patchLimit <= 0 {
		patchLimit = DefaultChangeSetPatchBytes
	}
	baseRevision = strings.TrimSpace(baseRevision)
	if baseRevision == "" {
		var err error
		baseRevision, err = m.Revision(ctx, directory)
		if err != nil {
			return ChangeSet{}, err
		}
	}

	status, truncated, code, err := runGitLimited(ctx, directory, maxChangeSetStatusBytes,
		"status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return ChangeSet{}, err
	}
	if code != 0 {
		return ChangeSet{}, &Error{Code: CodeChangeSetFailed, Message: firstNonEmpty(status, "failed to read worktree status")}
	}
	if truncated {
		return ChangeSet{}, &Error{Code: CodeChangeSetFailed, Message: "worktree status exceeds the change-set limit"}
	}
	files, untrackedPaths := parseChangeSetStatus(status)

	patch, patchTruncated, code, err := runGitLimited(ctx, directory, patchLimit,
		"diff", "--binary", "--no-ext-diff", "--no-color", baseRevision, "--")
	if err != nil {
		return ChangeSet{}, err
	}
	if code != 0 {
		return ChangeSet{}, &Error{Code: CodeChangeSetFailed, Message: firstNonEmpty(patch, "failed to read worktree diff")}
	}
	if patchTruncated {
		patch = truncateUTF8(patch, patchLimit)
	}

	untracked, untrackedTruncated := captureUntrackedFiles(directory, untrackedPaths, DefaultChangeSetUntrackedBytes)
	headRevision, err := m.Revision(ctx, directory)
	if err != nil {
		return ChangeSet{}, err
	}

	changeSet := ChangeSet{
		Version:            1,
		BaseRevision:       baseRevision,
		HeadRevision:       headRevision,
		Files:              files,
		UntrackedFiles:     untracked,
		Patch:              patch,
		PatchTruncated:     patchTruncated,
		UntrackedTruncated: untrackedTruncated,
	}
	changeSet.Digest = changeSetDigest(changeSet)
	return changeSet, nil
}

func parseChangeSetStatus(raw string) ([]ChangeFile, []string) {
	var files []ChangeFile
	var untracked []string
	entries := strings.Split(raw, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		path := entry[3:]
		file := ChangeFile{Path: filepath.ToSlash(path), Status: changeSetStatus(status)}
		if strings.ContainsAny(status, "RC") && i+1 < len(entries) {
			i++
			file.OriginalPath = filepath.ToSlash(entries[i])
		}
		files = append(files, file)
		if strings.TrimSpace(status) == "??" {
			untracked = append(untracked, path)
		}
	}
	return files, untracked
}

func changeSetStatus(status string) string {
	switch {
	case status == "??":
		return "untracked"
	case strings.Contains(status, "R"):
		return "renamed"
	case strings.Contains(status, "C"):
		return "copied"
	case strings.Contains(status, "D"):
		return "deleted"
	case strings.Contains(status, "A"):
		return "added"
	case strings.Contains(status, "M"):
		return "modified"
	case strings.Contains(status, "T"):
		return "typechanged"
	default:
		return strings.TrimSpace(status)
	}
}

func captureUntrackedFiles(root string, paths []string, totalLimit int) ([]UntrackedFile, bool) {
	out := make([]UntrackedFile, 0, len(paths))
	remaining := totalLimit
	truncated := false
	for index, relative := range paths {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if !pathWithin(root, full) {
			// A status path outside the managed worktree is unexpected; never
			// silently omit it from an otherwise "complete" snapshot.
			truncated = true
			continue
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			// Symlinks, special files, and unreadable entries cannot be
			// represented safely by an untracked-file snapshot. Mark the
			// snapshot incomplete so integration refuses it instead of
			// pretending those paths do not exist.
			truncated = true
			continue
		}
		item := UntrackedFile{Path: filepath.ToSlash(relative), Size: info.Size()}
		limit := info.Size()
		if limit < 0 {
			limit = 0
		}
		if limit > int64(remaining) {
			limit = int64(remaining)
			item.ContentTruncated = true
			truncated = true
		}
		data, err := readFileLimit(full, limit)
		if err != nil {
			truncated = true
			continue
		}
		item.Digest = sha256Hex(data)
		item.Binary = looksBinary(data)
		item.ContentBase64 = base64.StdEncoding.EncodeToString(data)
		out = append(out, item)
		remaining -= len(data)
		if remaining <= 0 {
			if index+1 < len(paths) {
				truncated = true
			}
			break
		}
	}
	return out, truncated
}

func readFileLimit(path string, limit int64) ([]byte, error) {
	if limit < 0 {
		limit = 0
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}

func looksBinary(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	return bytes.IndexByte(data, 0) >= 0
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func changeSetDigest(changeSet ChangeSet) string {
	copy := changeSet
	copy.Digest = ""
	encoded, _ := json.Marshal(copy)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func truncateUTF8(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

type limitedBuffer struct {
	limit     int
	data      []byte
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.data = append(b.data, p[:remaining]...)
		b.truncated = true
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func runGitLimited(ctx context.Context, cwd string, limit int, args ...string) (string, bool, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	var stdout limitedBuffer
	stdout.limit = limit
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(stdout.data), stdout.truncated, exitErr.ExitCode(), nil
		}
		return string(stdout.data), stdout.truncated, 1, err
	}
	return string(stdout.data), stdout.truncated, 0, nil
}
