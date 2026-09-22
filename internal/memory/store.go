// Package memory implements persistent memory storage for serve channels.
// Memory is stored as a human-readable Markdown file (memory.md).
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// Store manages reading and writing of memory.md files.
type Store struct {
	mu sync.Mutex

	// explicitPath overrides auto-discovery when set via config.
	explicitPath string
	// workDir is the project working directory, used as fallback for default write path.
	workDir string
}

// NewStore creates a memory store.
// If explicitPath is non-empty, it overrides the default discovery logic.
// workDir is used as fallback directory for creating new memory files.
func NewStore(explicitPath, workDir string) *Store {
	return &Store{explicitPath: explicitPath, workDir: workDir}
}

// defaultTemplate is the initial content for a new memory.md file.
const defaultTemplate = `# Agent Memory

## User Profile

## Working Memory

## Lessons Learned
`

// Resolve finds the memory.md file to use.
// Priority: explicit path → .mothx/memory.md → <GLOBAL_DIR>/memory.md
// Returns (path, source, error). source is "explicit", "project", "global", or "".
func (s *Store) Resolve() (path string, source string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveNoLock()
}

func (s *Store) resolveNoLock() (path string, source string, err error) {
	// 1. Explicit path from config
	if s.explicitPath != "" {
		if _, err := os.Stat(s.explicitPath); err == nil {
			return s.explicitPath, "explicit", nil
		}
		// Explicit path configured but doesn't exist yet — will create here on write
		return s.explicitPath, "explicit", nil
	}

	// 2. Project-level: .mothx/memory.md
	projectPath := config.ProjectPath("memory.md")
	if s.workDir != "" {
		projectPath = config.ProjectPathFor(s.workDir, "memory.md")
	}
	if _, err := os.Stat(projectPath); err == nil {
		return projectPath, "project", nil
	}

	// 3. Global: <GLOBAL_DIR>/memory.md
	globalPath := filepath.Join(config.ConfigDir(), "memory.md")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath, "global", nil
	}

	// None exists — return empty (will be created on first write)
	return "", "", nil
}

// Read returns the full content of memory.md.
func (s *Store) Read() (content string, path string, source string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readNoLock()
}

func (s *Store) readNoLock() (content string, path string, source string, err error) {
	path, source, err = s.resolveNoLock()
	if err != nil {
		return "", "", "", err
	}
	if path == "" {
		return "", "", "", nil // no memory file exists
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", path, source, nil
		}
		return "", "", "", fmt.Errorf("read memory file: %w", err)
	}

	return string(data), path, source, nil
}

// ReadSection returns the content of a specific ## section.
func (s *Store) ReadSection(section string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, _, _, err := s.readNoLock()
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", nil
	}

	return extractSection(content, section), nil
}

// Add appends a line to a specific section.
func (s *Store) Add(section, entry string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, path, _, err := s.readNoLock()
	if err != nil {
		return err
	}

	if path == "" {
		// Create new file
		path = s.defaultWritePath()
		content = defaultTemplate
	}

	updated := addToSection(content, section, entry)
	return s.writeFile(path, updated)
}

// Update replaces old text with new text in a section.
func (s *Store) Update(section, oldText, newText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, path, _, err := s.readNoLock()
	if err != nil {
		return err
	}
	if path == "" || content == "" {
		return fmt.Errorf("no memory file to update")
	}

	sectionContent := extractSection(content, section)
	if sectionContent == "" {
		return fmt.Errorf("section '%s' not found", section)
	}

	if !strings.Contains(sectionContent, oldText) {
		return fmt.Errorf("text not found in section '%s'", section)
	}

	updated, ok := replaceInSection(content, section, oldText, newText)
	if !ok {
		return fmt.Errorf("text not found in section '%s'", section)
	}
	return s.writeFile(path, updated)
}

// Delete removes a line from a section.
func (s *Store) Delete(section, entry string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, path, _, err := s.readNoLock()
	if err != nil {
		return err
	}
	if path == "" || content == "" {
		return fmt.Errorf("no memory file to delete from")
	}

	updated, found := deleteFromSection(content, section, entry)
	if !found {
		return fmt.Errorf("entry not found in section '%s'", section)
	}

	return s.writeFile(path, updated)
}

// WriteAll overwrites the entire memory.md content.
func (s *Store) WriteAll(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, path, _, err := s.readNoLock()
	if err != nil {
		return err
	}
	if path == "" {
		path = s.defaultWritePath()
	}
	return s.writeFile(path, content)
}

// --- Helpers ---

// defaultWritePath determines where to create a new memory.md.
// Default: project-level (.mothx/memory.md). Only uses global if explicitly configured.
func (s *Store) defaultWritePath() string {
	if s.explicitPath != "" {
		return s.explicitPath
	}
	// Default to project-level: workDir/.mothx/memory.md
	if s.workDir != "" {
		return config.ProjectPathFor(s.workDir, "memory.md")
	}
	// Fallback: cwd/.mothx/memory.md
	return config.ProjectPath("memory.md")
}

func replaceInSection(content, section, oldText, newText string) (string, bool) {
	start, end, ok := sectionBounds(content, section)
	if !ok {
		return content, false
	}
	segment := content[start:end]
	if !strings.Contains(segment, oldText) {
		return content, false
	}
	segment = strings.Replace(segment, oldText, newText, 1)
	return content[:start] + segment + content[end:], true
}

func deleteFromSection(content, section, entry string) (string, bool) {
	start, end, ok := sectionBounds(content, section)
	if !ok {
		return content, false
	}
	segment := content[start:end]
	lines := strings.Split(segment, "\n")
	result := make([]string, 0, len(lines))
	found := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match "- entry" or "entry" (with or without bullet)
		cleanEntry := strings.TrimPrefix(strings.TrimSpace(entry), "- ")
		cleanLine := strings.TrimPrefix(trimmed, "- ")
		if cleanLine == cleanEntry && !found {
			found = true
			continue // skip this line
		}
		result = append(result, line)
	}
	if !found {
		return content, false
	}
	return content[:start] + strings.Join(result, "\n") + content[end:], true
}

func sectionBounds(content, section string) (start, end int, ok bool) {
	header := "## " + section
	idx := strings.Index(content, header)
	if idx < 0 {
		return 0, 0, false
	}
	afterHeader := content[idx+len(header):]
	nlIdx := strings.Index(afterHeader, "\n")
	if nlIdx < 0 {
		return len(content), len(content), true
	}
	start = idx + len(header) + nlIdx + 1
	rest := content[start:]
	nextSection := strings.Index(rest, "\n## ")
	if nextSection >= 0 {
		return start, start + nextSection, true
	}
	return start, len(content), true
}

// writeFile writes content to path, creating parent dirs as needed.
func (s *Store) writeFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	return os.WriteFile(path, []byte(content), 0600)
}

// extractSection extracts content under a ## heading.
func extractSection(content, section string) string {
	header := "## " + section
	idx := strings.Index(content, header)
	if idx < 0 {
		return ""
	}

	// Find the start of content after the header line
	afterHeader := content[idx+len(header):]
	nlIdx := strings.Index(afterHeader, "\n")
	if nlIdx < 0 {
		return ""
	}
	afterHeader = afterHeader[nlIdx+1:]

	// Find the next ## heading or end of file
	nextSection := strings.Index(afterHeader, "\n## ")
	if nextSection >= 0 {
		afterHeader = afterHeader[:nextSection]
	}

	return strings.TrimSpace(afterHeader)
}

// addToSection appends an entry to a section. Creates the section if missing.
func addToSection(content, section, entry string) string {
	header := "## " + section

	// Ensure entry has bullet prefix
	trimmedEntry := strings.TrimSpace(entry)
	if !strings.HasPrefix(trimmedEntry, "- ") {
		trimmedEntry = "- " + trimmedEntry
	}

	idx := strings.Index(content, header)
	if idx < 0 {
		// Section doesn't exist — append at end
		return strings.TrimRight(content, "\n") + "\n\n" + header + "\n\n" + trimmedEntry + "\n"
	}

	// Find the end of this section (next ## or EOF)
	afterHeader := content[idx+len(header):]
	nlIdx := strings.Index(afterHeader, "\n")
	if nlIdx < 0 {
		return content + "\n\n" + trimmedEntry + "\n"
	}

	sectionStart := idx + len(header) + nlIdx + 1
	rest := content[sectionStart:]

	nextSection := strings.Index(rest, "\n## ")
	if nextSection >= 0 {
		// Insert before next section
		insertPoint := sectionStart + nextSection
		return content[:insertPoint] + trimmedEntry + "\n" + content[insertPoint:]
	}

	// Append at end
	return strings.TrimRight(content, "\n") + "\n" + trimmedEntry + "\n"
}
