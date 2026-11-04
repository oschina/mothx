package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// WriteTool writes content to files.
type WriteTool struct {
	registry *Registry
}

// NewWriteTool creates a new write tool.
func NewWriteTool(r *Registry) *WriteTool {
	return &WriteTool{registry: r}
}

// WriteFile resolves path through the workspace roots, acquires the shared
// in-process file lock, and writes content atomically. It is the shared write
// path for non-tool Runtime bridges so they cannot bypass the local tool's
// path, lock, and atomicity rules.
func (r *Registry) WriteFile(ctx context.Context, path, content, owner string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("registry is nil")
	}
	resolved, err := r.ResolvePath(path)
	if err != nil {
		return "", err
	}
	release, err := r.acquireFileLock(ctx, resolved, owner)
	if err != nil {
		return "", err
	}
	defer release()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if err := writeFileAtomic(resolved, []byte(content)); err != nil {
		return "", err
	}
	return resolved, nil
}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories."
}

func (t *WriteTool) PromptSnippet() string {
	return "Create or overwrite files"
}

func (t *WriteTool) PromptGuidelines() []string {
	return []string{"Use write only for new files or complete rewrites."}
}

func (t *WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to write"
			},
			"content": {
				"type": "string",
				"description": "Content to write to the file"
			}
		},
		"required": ["path", "content"]
	}`)
}

func (t *WriteTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	content, ok := params["content"].(string)
	if !ok {
		return ToolResult{}, fmt.Errorf("content is required")
	}

	if path == "" {
		return ToolResult{}, fmt.Errorf("path is required")
	}

	path, err := t.registry.ResolvePath(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("invalid path: %w", err)
	}

	release, err := t.registry.acquireFileLock(ctx, path, t.Name())
	if err != nil {
		return ToolResult{}, err
	}

	oldContent := ""
	oldExists := false
	if data, err := os.ReadFile(path); err == nil {
		oldContent = string(data)
		oldExists = true
	}

	// Write file atomically, preserving existing permissions
	writeErr := writeFileAtomic(path, []byte(content))
	release()
	if writeErr != nil {
		return ToolResult{}, fmt.Errorf("write file: %w", writeErr)
	}

	diff := BuildFileDiff(path, oldContent, content)
	if !oldExists {
		diff.OldText = nil
	}
	return NewDiffToolResult(fmt.Sprintf("File written: %s (%d bytes)\n%s", path, len(content), formatFileDiffSummary(diff)), diff), nil
}

func formatWriteDiffSummary(oldContent, newContent string) string {
	return formatFileDiffSummary(BuildFileDiff("", oldContent, newContent))
}

func formatFileDiffSummary(diff *FileDiff) string {
	if diff == nil {
		return "Diff: +0 -0\n- lines: none\n+ lines: none"
	}
	suffix := ""
	if diff.Truncated {
		suffix = " (large file; line ranges approximate)"
	}
	return fmt.Sprintf("Diff: +%d -%d%s\n- lines: %s\n+ lines: %s",
		diff.Added,
		diff.Deleted,
		suffix,
		formatLineRanges(diff.DeletedLines),
		formatLineRanges(diff.AddedLines),
	)
}

// BuildFileDiff returns a compact, structured line diff for display and audit.
func BuildFileDiff(path, oldContent, newContent string) *FileDiff {
	oldLines := splitDiffLines(oldContent)
	newLines := splitDiffLines(newContent)
	deleted, added := diffLineChanges(oldLines, newLines)
	truncated := len(oldLines)*len(newLines) > 200000
	return &FileDiff{
		Path:         path,
		Added:        len(added),
		Deleted:      len(deleted),
		AddedLines:   added,
		DeletedLines: deleted,
		Unified:      formatUnifiedDiff(path, oldLines, newLines, deleted, added, truncated),
		OldText:      stringPointer(oldContent),
		NewText:      newContent,
		Truncated:    truncated,
	}
}

func stringPointer(value string) *string { return &value }

func splitDiffLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func diffLineChanges(oldLines, newLines []string) ([]int, []int) {
	if len(oldLines) == 0 && len(newLines) == 0 {
		return nil, nil
	}
	if len(oldLines)*len(newLines) > 200000 {
		return allLineNumbers(len(oldLines)), allLineNumbers(len(newLines))
	}

	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var deleted, added []int
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			deleted = append(deleted, i+1)
			i++
		default:
			added = append(added, j+1)
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		deleted = append(deleted, i+1)
	}
	for ; j < len(newLines); j++ {
		added = append(added, j+1)
	}
	return deleted, added
}

func formatUnifiedDiff(path string, oldLines, newLines []string, deleted, added []int, truncated bool) string {
	var sb strings.Builder
	oldPath := path
	newPath := path
	if oldPath == "" {
		oldPath = "old"
		newPath = "new"
	}
	sb.WriteString("--- " + oldPath + "\n")
	sb.WriteString("+++ " + newPath + "\n")
	if truncated {
		sb.WriteString("@@ large file diff omitted @@\n")
		sb.WriteString(fmt.Sprintf("-%s\n", formatLineRanges(deleted)))
		sb.WriteString(fmt.Sprintf("+%s\n", formatLineRanges(added)))
		return sb.String()
	}
	if len(deleted) == 0 && len(added) == 0 {
		return sb.String()
	}
	deletedSet := lineSet(deleted)
	addedSet := lineSet(added)
	records := makeDiffRecords(oldLines, newLines, deletedSet, addedSet)
	for _, hunk := range selectDiffHunks(records, 3) {
		oldStart, oldCount, newStart, newCount := hunkRanges(records[hunk.start:hunk.end])
		sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))
		for _, record := range records[hunk.start:hunk.end] {
			sb.WriteByte(record.kind)
			sb.WriteString(record.text)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

type diffRecord struct {
	kind    byte
	text    string
	oldLine int
	newLine int
}

type diffHunk struct {
	start int
	end   int
}

func makeDiffRecords(oldLines, newLines []string, deletedSet, addedSet map[int]bool) []diffRecord {
	var records []diffRecord
	oldIdx, newIdx := 1, 1
	for oldIdx <= len(oldLines) || newIdx <= len(newLines) {
		switch {
		case oldIdx <= len(oldLines) && deletedSet[oldIdx]:
			records = append(records, diffRecord{kind: '-', text: oldLines[oldIdx-1], oldLine: oldIdx})
			oldIdx++
		case newIdx <= len(newLines) && addedSet[newIdx]:
			records = append(records, diffRecord{kind: '+', text: newLines[newIdx-1], newLine: newIdx})
			newIdx++
		case oldIdx <= len(oldLines) && newIdx <= len(newLines):
			records = append(records, diffRecord{kind: ' ', text: oldLines[oldIdx-1], oldLine: oldIdx, newLine: newIdx})
			oldIdx++
			newIdx++
		case oldIdx <= len(oldLines):
			records = append(records, diffRecord{kind: '-', text: oldLines[oldIdx-1], oldLine: oldIdx})
			oldIdx++
		case newIdx <= len(newLines):
			records = append(records, diffRecord{kind: '+', text: newLines[newIdx-1], newLine: newIdx})
			newIdx++
		}
	}
	return records
}

func selectDiffHunks(records []diffRecord, contextLines int) []diffHunk {
	var hunks []diffHunk
	for i, record := range records {
		if record.kind == ' ' {
			continue
		}
		start := i - contextLines
		if start < 0 {
			start = 0
		}
		end := i + contextLines + 1
		if end > len(records) {
			end = len(records)
		}
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			if end > hunks[len(hunks)-1].end {
				hunks[len(hunks)-1].end = end
			}
			continue
		}
		hunks = append(hunks, diffHunk{start: start, end: end})
	}
	return hunks
}

func hunkRanges(records []diffRecord) (int, int, int, int) {
	oldStart, newStart := 0, 0
	oldCount, newCount := 0, 0
	for _, record := range records {
		if record.oldLine > 0 {
			if oldStart == 0 {
				oldStart = record.oldLine
			}
			oldCount++
		}
		if record.newLine > 0 {
			if newStart == 0 {
				newStart = record.newLine
			}
			newCount++
		}
	}
	if oldStart == 0 {
		oldStart = 1
	}
	if newStart == 0 {
		newStart = 1
	}
	return oldStart, oldCount, newStart, newCount
}

func lineSet(lines []int) map[int]bool {
	result := make(map[int]bool, len(lines))
	for _, line := range lines {
		result[line] = true
	}
	return result
}

func allLineNumbers(count int) []int {
	lines := make([]int, count)
	for i := range lines {
		lines[i] = i + 1
	}
	return lines
}

func formatLineRanges(lines []int) string {
	if len(lines) == 0 {
		return "none"
	}
	var ranges []string
	start, prev := lines[0], lines[0]
	for _, line := range lines[1:] {
		if line == prev+1 {
			prev = line
			continue
		}
		ranges = append(ranges, formatLineRange(start, prev))
		start, prev = line, line
	}
	ranges = append(ranges, formatLineRange(start, prev))
	return strings.Join(ranges, ",")
}

func formatLineRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}
