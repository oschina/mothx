// Package workspacefiles provides the single read-only file projection used
// by every front-end adapter (WebUI/API, future ACP). It owns nothing: no
// Run, no lease, no mutation. The caller resolves the session root through
// its own canonical source (Runtime/SessionManager); this package only maps
// panel-relative paths onto that root with containment guarantees and reads
// directory/file facts.
package workspacefiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	// MaxEntries bounds one directory listing.
	MaxEntries = 2000
	// MaxRead bounds one file preview payload.
	MaxRead = 2 << 20 // 2 MiB
	// SniffBytes bounds the binary-detection prefix.
	SniffBytes = 8192
)

// Entry is one directory child. Path is always slash-separated and relative
// to the projection root; adapters must never expose absolute paths.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size,omitempty"`
	ModTime int64  `json:"mtime,omitempty"`
}

// DirResult is one lazy listing. Dir is the queried relative directory ("" is
// the root).
type DirResult struct {
	Dir       string  `json:"dir"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// FileResult is one bounded file read. Binary, TooLarge, and Content are
// mutually informative: a binary or oversize file returns no content.
type FileResult struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"mtime"`
	Lang     string `json:"lang"`
	Binary   bool   `json:"binary"`
	TooLarge bool   `json:"tooLarge"`
	Content  string `json:"content"`
}

var (
	// ErrInvalidPath rejects absolute inputs, parent traversal, and NUL bytes.
	ErrInvalidPath = errors.New("invalid path")
	// ErrNotFound reports a missing file target for a content read.
	ErrNotFound = errors.New("target not found")
)

// WithinRoot accepts target only while its deepest existing ancestor
// resolves (symlinks included) inside rootAbs. Non-existing trailing segments
// are allowed so callers can stat or list afterwards.
func WithinRoot(rootAbs, target string) bool {
	rootReal := rootAbs
	if real, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootReal = real
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	for p := target; ; {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			behind, relErr := filepath.Rel(rootReal, real)
			return relErr == nil && behind != ".." && !strings.HasPrefix(behind, ".."+string(filepath.Separator))
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		parent := filepath.Dir(p)
		if parent == p {
			return true
		}
		p = parent
	}
}

// Resolve maps a panel-relative path (slash style, "" is the root) onto the
// absolute target inside root, rejecting absolute paths, parent traversal,
// and symlink escapes beyond the root boundary.
func Resolve(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel = strings.TrimSpace(filepath.FromSlash(rel))
	if rel == "" || rel == "." {
		return rootAbs, nil
	}
	if filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", ErrInvalidPath
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	target := filepath.Join(rootAbs, cleaned)
	if !WithinRoot(rootAbs, target) {
		return "", ErrInvalidPath
	}
	return target, nil
}

// List reads one directory lazily. dirRel selects the subdirectory ("" is the
// root); entries come back directories-first in case-insensitive name order.
func List(root, dirRel string) (DirResult, error) {
	dir, err := Resolve(root, dirRel)
	if err != nil {
		return DirResult{}, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return DirResult{}, err
	}
	relDir := relFromRoot(root, dir)
	out := make([]Entry, 0, len(items))
	truncated := false
	for _, item := range items {
		if len(out) >= MaxEntries {
			truncated = true
			break
		}
		entry := Entry{
			Name: item.Name(),
			Path: joinRel(relDir, item.Name()),
			Dir:  item.IsDir(),
		}
		if info, statErr := item.Info(); statErr == nil {
			entry.Size = info.Size()
			entry.ModTime = info.ModTime().Unix()
		}
		out = append(out, entry)
	}
	sortEntries(out)
	return DirResult{Dir: relDir, Entries: out, Truncated: truncated}, nil
}

// Read returns one bounded text preview of fileRel inside root.
func Read(root, fileRel string) (FileResult, error) {
	file, err := Resolve(root, fileRel)
	if err != nil || file == root {
		return FileResult{}, ErrInvalidPath
	}
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return FileResult{}, ErrNotFound
	}
	result := FileResult{
		Path:    relFromRoot(root, file),
		Size:    info.Size(),
		ModTime: info.ModTime().Unix(),
		Lang:    LangFor(file),
	}
	if info.Size() > MaxRead {
		result.TooLarge = true
		return result, nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return FileResult{}, err
	}
	if LooksBinary(data) {
		result.Binary = true
		return result, nil
	}
	result.Content = string(data)
	return result, nil
}

func relFromRoot(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func joinRel(dirRel, name string) string {
	if dirRel == "" {
		return name
	}
	return dirRel + "/" + name
}

func sortEntries(entries []Entry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entryLess(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func entryLess(a, b Entry) bool {
	if a.Dir != b.Dir {
		return a.Dir
	}
	return strings.ToLower(a.Name) < strings.ToLower(b.Name)
}

// LooksBinary detects non-text payloads via invalid UTF-8 or NUL bytes in the
// sniff prefix.
func LooksBinary(data []byte) bool {
	sniff := data
	if len(sniff) > SniffBytes {
		sniff = sniff[:SniffBytes]
	}
	if !utf8.Valid(sniff) {
		return true
	}
	for _, b := range sniff {
		if b == 0 {
			return true
		}
	}
	return false
}

var langByExt = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "tsx", ".jsx": "jsx", ".svelte": "svelte", ".json": "json",
	".md": "markdown", ".markdown": "markdown", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml",
	".rs": "rust", ".c": "c", ".h": "c", ".cpp": "cpp", ".hpp": "cpp", ".java": "java",
	".sh": "shell", ".bash": "shell", ".zsh": "shell", ".ps1": "powershell", ".sql": "sql",
	".html": "html", ".css": "css", ".scss": "scss", ".xml": "xml",
}

// LangFor maps a path to a coarse language hint for preview rendering.
func LangFor(path string) string {
	base := filepath.Base(path)
	if strings.EqualFold(base, "Dockerfile") {
		return "dockerfile"
	}
	if lang, ok := langByExt[strings.ToLower(filepath.Ext(path))]; ok {
		return lang
	}
	return "text"
}
