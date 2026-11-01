package serve

// Read-only git projections for the workbench "Git Changes" panel. The panel
// is a pure output projection of the focused tab's working directory: it
// shells out to the system `git` binary with status/diff commands and never
// mutates the repository. Routes are registered through the shared serve mux,
// so they inherit the existing WebUI auth middleware.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	gitCommandTimeout = 15 * time.Second
	gitDiffMaxBytes   = 512 * 1024
	gitMaxStatusFiles = 5000
)

type gitChangeFile struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	Status  string `json:"status"`
	Staged  bool   `json:"staged"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

type gitChangesResponse struct {
	Repo      bool            `json:"repo"`
	Root      string          `json:"root,omitempty"`
	Branch    string          `json:"branch,omitempty"`
	Error     string          `json:"error,omitempty"`
	Files     []gitChangeFile `json:"files"`
	Staged    int             `json:"staged"`
	Unstaged  int             `json:"unstaged"`
	Untracked int             `json:"untracked"`
}

func (rt *channelRuntime) handleGitChanges(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		dir, problem := sessionWorkDirTarget(sessions, r)
		if dir == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), gitCommandTimeout)
		defer cancel()
		resp := collectGitChanges(ctx, dir)
		writeJSON(w, http.StatusOK, resp)
	}
}

func (rt *channelRuntime) handleGitDiff(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		dir, problem := sessionWorkDirTarget(sessions, r)
		if dir == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		file := r.URL.Query().Get("file")
		if file == "" || filepath.IsAbs(file) || strings.Contains(file, "..") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path"})
			return
		}
		staged := r.URL.Query().Get("staged") == "1"
		untracked := r.URL.Query().Get("untracked") == "1"
		ctx, cancel := context.WithTimeout(r.Context(), gitCommandTimeout)
		defer cancel()
		var args []string
		if untracked {
			// Synthesize an added-file diff so untracked content is reviewable.
			args = []string{"-C", dir, "diff", "--no-color", "--no-index", "--", os.DevNull, file}
		} else {
			args = []string{"-C", dir, "diff", "--no-color"}
			if staged {
				args = append(args, "--cached")
			}
			args = append(args, "--", file)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil && !untracked {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": gitCommandError(err, stderr)})
			return
		}
		if err != nil && untracked && stdout.Len() == 0 {
			// --no-index outside-repo failures other than "files differ".
			if _, statErr := os.Stat(filepath.Join(dir, file)); statErr != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": gitCommandError(err, stderr)})
				return
			}
		}
		text := stdout.String()
		truncated := false
		if len(text) > gitDiffMaxBytes {
			text = text[:gitDiffMaxBytes]
			truncated = true
		}
		binary := strings.Contains(text, "Binary files ") && strings.Contains(text, "differ")
		writeJSON(w, http.StatusOK, map[string]any{
			"diff":      text,
			"truncated": truncated,
			"binary":    binary,
		})
	}
}

func collectGitChanges(ctx context.Context, dir string) gitChangesResponse {
	resp := gitChangesResponse{Files: []gitChangeFile{}}
	root, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		resp.Repo = false
		return resp
	}
	resp.Repo = true
	resp.Root = strings.TrimSpace(root)
	if branch, err := runGit(ctx, resp.Root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		resp.Branch = strings.TrimSpace(branch)
	} else {
		// Detached HEAD: fall back to the short commit.
		if sha, err := runGit(ctx, resp.Root, "rev-parse", "--short", "HEAD"); err == nil {
			resp.Branch = strings.TrimSpace(sha)
		}
	}
	porcelain, err := runGit(ctx, resp.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		resp.Error = strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
		return resp
	}
	workStats := parseGitNumstat(runGitNumstat(ctx, resp.Root, "diff", "--numstat", "-z", "--"))
	stagedStats := parseGitNumstat(runGitNumstat(ctx, resp.Root, "diff", "--numstat", "-z", "--cached"))

	for _, entry := range parseGitPorcelainZ(porcelain) {
		x, y := entry.index, entry.worktree
		if x == '?' || x == '!' {
			resp.Files = append(resp.Files, gitChangeFile{
				Path: entry.path, Status: "?", Staged: false,
			})
			resp.Untracked++
			continue
		}
		if x != ' ' {
			add, del := pickStat(stagedStats, entry.path)
			resp.Files = append(resp.Files, gitChangeFile{
				Path: entry.path, OldPath: entry.oldPath, Status: string(x), Staged: true,
				Added: add, Removed: del,
			})
			resp.Staged++
		}
		if y != ' ' {
			add, del := pickStat(workStats, entry.path)
			resp.Files = append(resp.Files, gitChangeFile{
				Path: entry.path, Status: string(y), Staged: false,
				Added: add, Removed: del,
			})
			resp.Unstaged++
		}
	}
	if len(resp.Files) > gitMaxStatusFiles {
		resp.Files = resp.Files[:gitMaxStatusFiles]
	}
	return resp
}

type porcelainEntry struct {
	index    byte
	worktree byte
	path     string
	oldPath  string
}

// parseGitPorcelainZ parses `git status --porcelain=v1 -z` output. Rename and
// copy entries carry the source path in the NUL-terminated field right after
// the destination path.
func parseGitPorcelainZ(out string) []porcelainEntry {
	fields := strings.Split(out, "\x00")
	var entries []porcelainEntry
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 {
			continue
		}
		entry := porcelainEntry{index: rec[0], worktree: rec[1], path: rec[3:]}
		if entry.index == 'R' || entry.index == 'C' || entry.worktree == 'R' || entry.worktree == 'C' {
			if i+1 < len(fields) && fields[i+1] != "" {
				entry.oldPath = fields[i+1]
				i++
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func runGitNumstat(ctx context.Context, dir string, args ...string) string {
	out, err := runGit(ctx, dir, args...)
	if err != nil {
		return ""
	}
	return out
}

// parseGitNumstat parses `git diff --numstat -z` output. Regular records are
// "added\tremoved\tpath\0". When rename/copy detection empties the path field,
// the record becomes "added\tremoved\t\0old\0new\0" with the old and new paths
// in the following NUL fields.
func parseGitNumstat(out string) map[string][2]int {
	stats := map[string][2]int{}
	if out == "" {
		return stats
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) < 2 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		removed, _ := strconv.Atoi(parts[1])
		if len(parts) == 3 && parts[2] != "" {
			stats[parts[2]] = [2]int{added, removed}
			continue
		}
		// Rename record: "old\0new\0" follows the empty path field.
		if i+2 < len(fields) && fields[i+1] != "" && fields[i+2] != "" {
			stats[fields[i+2]] = [2]int{added, removed}
			i += 2
		}
	}
	return stats
}

func pickStat(stats map[string][2]int, path string) (int, int) {
	if v, ok := stats[path]; ok {
		return v[0], v[1]
	}
	return 0, 0
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if dir == "" {
		return "", errors.New("working directory is required")
	}
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), errors.New(gitCommandError(err, stderr))
	}
	return stdout.String(), nil
}

func gitCommandError(err error, stderr bytes.Buffer) string {
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return msg
}
