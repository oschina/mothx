// Package worktree owns the git worktree mechanics used by the Runtime: name
// planning, materialization, listing, removal and reset. It never touches SQL,
// sessions or the Runtime; git is the only content authority it consults.
package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Error is a typed mechanism error carrying a stable machine code so adapters
// can map it to protocol errors without string matching.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Error codes shared with the ACP/CLI projections.
const (
	CodeNotGit               = "not_git"
	CodeNameGenerationFailed = "name_generation_failed"
	CodeCreateFailed         = "create_failed"
	CodePopulateFailed       = "populate_failed"
	CodeListFailed           = "list_failed"
	CodeRemoveFailed         = "remove_failed"
	CodeResetFailed          = "reset_failed"
	CodeDefaultBranchFailed  = "default_branch_failed"
)

// Info describes one git worktree derived from a repository. An empty Branch
// means the worktree is detached.
type Info struct {
	Name      string
	Branch    string
	Directory string
}

// Runner executes git in cwd and returns stdout, stderr and the exit code. A
// non-nil error means the process could not be started at all; a non-zero code
// with a nil error is a normal git failure.
type Runner func(ctx context.Context, cwd string, args ...string) (stdout, stderr string, code int, err error)

// Manager owns git worktree mechanics under Root. It is safe for concurrent
// use as long as callers serialize per repository root (the Runtime does).
type Manager struct {
	root         string
	branchPrefix string
	run          Runner
	maxAttempts  int
}

// NewManager builds a Manager whose worktrees live under root. An empty
// branchPrefix falls back to "mothx".
func NewManager(root, branchPrefix string) *Manager {
	if strings.TrimSpace(branchPrefix) == "" {
		branchPrefix = "mothx"
	}
	return &Manager{root: filepath.Clean(root), branchPrefix: branchPrefix, run: execGit, maxAttempts: 26}
}

// Root returns the directory all managed worktrees live under.
func (m *Manager) Root() string { return m.root }

// RepoRoot resolves the primary worktree (repository root) for dir.
func (m *Manager) RepoRoot(ctx context.Context, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", &Error{Code: CodeNotGit, Message: "working directory is required"}
	}
	out, stderr, code, err := m.run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", &Error{Code: CodeNotGit, Message: firstNonEmpty(stderr, out, "not a git repository")}
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", &Error{Code: CodeNotGit, Message: "git returned an empty repository root"}
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		root = abs
	}
	return filepath.Clean(root), nil
}

// DirectoryFor returns the canonical worktree directory for name under the
// repository's key directory.
func (m *Manager) DirectoryFor(repoRoot, name string) string {
	return filepath.Join(m.root, repoKey(repoRoot), name)
}

// Plan generates a unique name/directory/branch for a new worktree. requested
// is slugified; when empty, the repository basename is used. Conflicts on the
// directory or branch are retried with a random suffix.
func (m *Manager) Plan(ctx context.Context, repoRoot, requested string, detached bool) (Info, error) {
	base := Slugify(requested)
	if base == "" {
		base = Slugify(filepath.Base(filepath.Clean(repoRoot)))
	}
	if base == "" {
		base = "worktree"
	}
	for attempt := 0; attempt < m.maxAttempts; attempt++ {
		name := base
		if attempt > 0 {
			name = base + "-" + shortRandom()
		}
		dir := m.DirectoryFor(repoRoot, name)
		if _, statErr := os.Stat(dir); statErr == nil {
			continue
		}
		branch := ""
		if !detached {
			branch = m.branchPrefix + "/" + name
			exists, err := m.branchExists(ctx, repoRoot, branch)
			if err != nil {
				return Info{}, err
			}
			if exists {
				continue
			}
		}
		return Info{Name: name, Branch: branch, Directory: dir}, nil
	}
	return Info{}, &Error{Code: CodeNameGenerationFailed, Message: "failed to generate a unique worktree name"}
}

// Add materializes the worktree directory without checking out files. The
// directory exists after a successful Add, so it can be authorized before the
// (slower) Populate step runs.
func (m *Manager) Add(ctx context.Context, repoRoot string, info Info) error {
	args := []string{"worktree", "add", "--no-checkout"}
	if info.Branch != "" {
		args = append(args, "-b", info.Branch, info.Directory)
	} else {
		args = append(args, "--detach", info.Directory)
	}
	out, stderr, code, err := m.run(ctx, repoRoot, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return &Error{Code: CodeCreateFailed, Message: firstNonEmpty(stderr, out, "failed to create git worktree")}
	}
	return nil
}

// Populate checks out the worktree's committed tree.
func (m *Manager) Populate(ctx context.Context, info Info) error {
	out, stderr, code, err := m.run(ctx, info.Directory, "reset", "--hard")
	if err != nil {
		return err
	}
	if code != 0 {
		return &Error{Code: CodePopulateFailed, Message: firstNonEmpty(stderr, out, "failed to populate git worktree")}
	}
	return nil
}

// List returns the repository's worktrees excluding the primary worktree.
func (m *Manager) List(ctx context.Context, repoRoot string) ([]Info, error) {
	out, stderr, code, err := m.run(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &Error{Code: CodeListFailed, Message: firstNonEmpty(stderr, out, "failed to list git worktrees")}
	}
	primary := filepath.Clean(repoRoot)
	var infos []Info
	for _, entry := range parsePorcelain(out) {
		if entry.Path == "" {
			continue
		}
		dir := filepath.Clean(entry.Path)
		if dir == primary {
			continue
		}
		infos = append(infos, Info{
			Name:      filepath.Base(dir),
			Branch:    strings.TrimPrefix(entry.Branch, "refs/heads/"),
			Directory: dir,
		})
	}
	return infos, nil
}

// Remove deletes a worktree directory and, when non-empty, its branch. It is
// the caller's responsibility to prove directory belongs to repoRoot's
// registered worktrees before calling Remove.
func (m *Manager) Remove(ctx context.Context, repoRoot, directory, branch string) error {
	out, stderr, code, err := m.run(ctx, repoRoot, "worktree", "remove", "--force", directory)
	if err != nil {
		return err
	}
	if code != 0 {
		// git can report a non-zero exit after removing the worktree; only a
		// worktree still present in `git worktree list` is a real failure.
		remaining, listErr := m.List(ctx, repoRoot)
		if listErr != nil {
			return &Error{Code: CodeRemoveFailed, Message: firstNonEmpty(stderr, out, "failed to remove git worktree")}
		}
		for _, entry := range remaining {
			if filepath.Clean(entry.Directory) == filepath.Clean(directory) {
				return &Error{Code: CodeRemoveFailed, Message: firstNonEmpty(stderr, out, "failed to remove git worktree")}
			}
		}
	}
	// Clean a leftover directory only when it lives under the manager root, so
	// Remove can never delete an arbitrary user path.
	if m.underRoot(directory) {
		if _, statErr := os.Stat(directory); statErr == nil {
			_ = os.RemoveAll(directory)
		}
	}
	if branch != "" {
		// Branch deletion is best-effort: a worktree that is already gone must
		// not fail removal because its branch was checked out elsewhere.
		_, _, _, _ = m.run(ctx, repoRoot, "branch", "-D", branch)
	}
	return nil
}

// DefaultBranch resolves the repository's default branch ref, preferring
// origin/HEAD and falling back to main/master.
func (m *Manager) DefaultBranch(ctx context.Context, repoRoot string) (string, error) {
	if out, _, code, err := m.run(ctx, repoRoot, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil && code == 0 {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref, nil
		}
	}
	for _, candidate := range []string{"refs/heads/main", "refs/heads/master"} {
		if _, _, code, err := m.run(ctx, repoRoot, "show-ref", "--verify", "--quiet", candidate); err == nil && code == 0 {
			return candidate, nil
		}
	}
	return "", &Error{Code: CodeDefaultBranchFailed, Message: "default branch not found"}
}

// Reset returns a worktree to the repository's default branch, discarding
// local changes. It fails rather than silently leaving the tree dirty.
func (m *Manager) Reset(ctx context.Context, repoRoot, directory string) error {
	base, err := m.DefaultBranch(ctx, repoRoot)
	if err != nil {
		return err
	}
	if remote, branch, ok := splitRemoteTracking(base); ok {
		out, stderr, code, runErr := m.run(ctx, repoRoot, "fetch", remote, branch)
		if runErr != nil {
			return runErr
		}
		if code != 0 {
			return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, out, fmt.Sprintf("failed to fetch %s", base))}
		}
	}
	if out, stderr, code, runErr := m.run(ctx, directory, "reset", "--hard", base); runErr != nil {
		return runErr
	} else if code != 0 {
		return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, out, "failed to reset worktree")}
	}
	if err := m.sweep(ctx, directory); err != nil {
		return err
	}
	if err := m.resetSubmodules(ctx, directory); err != nil {
		return err
	}
	status, stderr, code, runErr := m.run(ctx, directory, "status", "--porcelain")
	if runErr != nil {
		return runErr
	}
	if code != 0 {
		return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, status, "failed to read worktree status")}
	}
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		return &Error{Code: CodeResetFailed, Message: "worktree reset left local changes:\n" + trimmed}
	}
	return nil
}

func (m *Manager) branchExists(ctx context.Context, repoRoot, branch string) (bool, error) {
	_, _, code, err := m.run(ctx, repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

func (m *Manager) underRoot(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	root := filepath.Clean(m.root)
	clean := filepath.Clean(dir)
	return clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator))
}

var failedRemovePattern = regexp.MustCompile(`(?i)^warning:\s+failed to remove\s+(.+?):\s+`)

// sweep cleans the worktree, retrying once after pruning directories git could
// not remove on the first pass (for example a nested worktree or a read-only
// tree).
func (m *Manager) sweep(ctx context.Context, directory string) error {
	out, stderr, code, err := m.run(ctx, directory, "clean", "-ffdx")
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	entries := failedRemoves(stderr, out)
	if len(entries) == 0 {
		return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, out, "failed to clean worktree")}
	}
	m.prune(directory, entries)
	out, stderr, code, err = m.run(ctx, directory, "clean", "-ffdx")
	if err != nil {
		return err
	}
	if code != 0 {
		return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, out, "failed to clean worktree")}
	}
	return nil
}

// resetSubmodules returns initialized submodules to their committed state. It
// is a no-op for repositories without a .gitmodules file.
func (m *Manager) resetSubmodules(ctx context.Context, directory string) error {
	if _, err := os.Stat(filepath.Join(directory, ".gitmodules")); err != nil {
		return nil
	}
	steps := [][]string{
		{"submodule", "update", "--init", "--recursive", "--force"},
		{"submodule", "foreach", "--recursive", "git", "reset", "--hard"},
		{"submodule", "foreach", "--recursive", "git", "clean", "-fdx"},
	}
	for _, args := range steps {
		out, stderr, code, err := m.run(ctx, directory, args...)
		if err != nil {
			return err
		}
		if code != 0 {
			return &Error{Code: CodeResetFailed, Message: firstNonEmpty(stderr, out, "failed to reset submodules")}
		}
	}
	return nil
}

func failedRemoves(chunks ...string) []string {
	var out []string
	for _, chunk := range chunks {
		for _, raw := range strings.Split(chunk, "\n") {
			match := failedRemovePattern.FindStringSubmatch(strings.TrimSpace(raw))
			if match == nil {
				continue
			}
			value := strings.Trim(strings.TrimSpace(match[1]), "'\"")
			if value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

// prune removes the entries git reported as unremovable, but only when they
// resolve under the worktree directory. It never deletes outside the tree.
func (m *Manager) prune(directory string, entries []string) {
	base := filepath.Clean(directory)
	for _, entry := range entries {
		target := entry
		if !filepath.IsAbs(target) {
			target = filepath.Join(directory, entry)
		}
		target = filepath.Clean(target)
		if target == base || !strings.HasPrefix(target, base+string(os.PathSeparator)) {
			continue
		}
		_ = os.RemoveAll(target)
	}
}

type porcelainEntry struct {
	Path     string
	Branch   string
	Detached bool
}

func parsePorcelain(text string) []porcelainEntry {
	var entries []porcelainEntry
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, porcelainEntry{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))})
		case strings.HasPrefix(line, "branch "):
			if len(entries) > 0 {
				entries[len(entries)-1].Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			}
		case line == "detached":
			if len(entries) > 0 {
				entries[len(entries)-1].Detached = true
			}
		}
	}
	return entries
}

func splitRemoteTracking(ref string) (remote, branch string, ok bool) {
	const prefix = "refs/remotes/"
	if !strings.HasPrefix(ref, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, prefix)
	idx := strings.Index(rest, "/")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// Slugify lowercases input and reduces every run of non-alphanumeric runes to a
// single dash, trimming leading/trailing dashes.
func Slugify(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	prevDash := false
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func repoKey(repoRoot string) string {
	base := Slugify(filepath.Base(filepath.Clean(repoRoot)))
	if base == "" {
		base = "repo"
	}
	sum := sha1.Sum([]byte(filepath.Clean(repoRoot)))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

func shortRandom() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", os.Getpid())
	}
	return hex.EncodeToString(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func execGit(ctx context.Context, cwd string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), 1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}
