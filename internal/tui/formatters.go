package tui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
)

func formatToolArgs(toolName string, args map[string]any) string {
	return formatToolArgsWithTranslator(i18n.New(i18n.LanguageEN), toolName, args)
}

func formatToolArgsWithTranslator(tr i18n.Translator, toolName string, args map[string]any) string {
	var parts []string

	switch toolName {
	case "write":
		if path, ok := args["path"]; ok {
			parts = append(parts, tr.Text(i18n.MsgToolArgsPath, path))
		}
		if content, ok := args["content"]; ok {
			contentStr := fmt.Sprintf("%v", content)
			parts = append(parts, tr.Text(i18n.MsgToolArgsContent, contentStr))
		}
	case "edit":
		if path, ok := args["path"]; ok {
			parts = append(parts, tr.Text(i18n.MsgToolArgsPath, path))
		}
		if editList, ok := args["edits"]; ok {
			if arr, ok := editList.([]any); ok {
				for idx, e := range arr {
					if m, ok := e.(map[string]any); ok {
						oldT, _ := m["oldText"].(string)
						newT, _ := m["newText"].(string)
						parts = append(parts, tr.Text(i18n.MsgToolArgsEdit, idx+1, oldT, newT))
					}
				}
			}
		}
	case "read":
		if path, ok := args["path"]; ok {
			parts = append(parts, tr.Text(i18n.MsgToolArgsPath, path))
		}
	case "bash":
		if cmd, ok := args["command"]; ok {
			parts = append(parts, tr.Text(i18n.MsgToolArgsCommand, cmd))
		}
	default:
		keys := make([]string, 0, len(args))
		for key := range args {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s: %v", key, args[key]))
		}
	}

	return strings.Join(parts, "\n")
}

func formatToolExecutionStart(result toolResult) string {
	return formatToolExecutionStartWithTranslator(i18n.New(i18n.LanguageEN), result)
}

func formatToolExecutionStartWithTranslator(tr i18n.Translator, result toolResult) string {
	if result.toolName == "bash" {
		return formatBashCommandLine(tr, result)
	}
	header := formatToolHeader(result)
	switch result.toolName {
	case "grep", "find":
		if pattern, ok := result.toolArgs["pattern"]; ok {
			return tr.Text(i18n.MsgToolExecutionRunning, header, pattern)
		}
	case "ls":
		if path, ok := result.toolArgs["path"]; ok {
			return tr.Text(i18n.MsgToolExecutionRunning, header, path)
		}
	}
	return header + " running"
}

func formatBashCommandLine(tr i18n.Translator, result toolResult) string {
	command := bashCommand(result)
	if command == "" {
		command = tr.Text(i18n.MsgToolCommandUnavailable)
	}
	command = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(command), "\r\n", "; "), "\n", "; ")
	command = truncate(command, 160)
	return fmt.Sprintf("🔧 [bash] %s (%s)", command, bashCommandStatus(tr, result))
}

func bashCommandStatus(tr i18n.Translator, result toolResult) string {
	if result.status == toolResultStatusRunning {
		return tr.Text(i18n.MsgToolCommandRunning)
	}
	if result.status == toolResultStatusInterrupted {
		return tr.Text(i18n.MsgToolModalStateCanceled)
	}
	if result.toolError != "" || strings.EqualFold(result.executionState, "interrupted") || strings.EqualFold(result.executionState, "failed") {
		return tr.Text(i18n.MsgToolCommandFailed)
	}
	if exitCode, ok := bashExitCode(result); ok {
		if exitCode == 0 {
			return tr.Text(i18n.MsgToolCommandSucceeded)
		}
		return tr.Text(i18n.MsgToolCommandFailedExit, exitCode)
	}
	if strings.Contains(result.fullContent, "Use 'jobs' tool to check status") {
		return tr.Text(i18n.MsgToolCommandStarted)
	}
	return tr.Text(i18n.MsgToolCommandSucceeded)
}

func bashCommand(result toolResult) string {
	if result.toolArgs != nil {
		if command, ok := result.toolArgs["command"].(string); ok && strings.TrimSpace(command) != "" {
			return command
		}
	}
	for _, content := range []string{result.fullContent, result.summary} {
		if command := toolSectionValue(content, "[command]"); command != "" {
			return command
		}
	}
	return ""
}

func bashExitCode(result toolResult) (int, bool) {
	for _, content := range []string{result.fullContent, result.summary} {
		value := toolSectionValue(content, "[exit_code]")
		if value == "" {
			continue
		}
		code, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return code, true
		}
	}
	return 0, false
}

func toolSectionValue(content, section string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != section || i+1 >= len(lines) {
			continue
		}
		return strings.TrimSpace(lines[i+1])
	}
	return ""
}

func formatToolHeader(result toolResult) string {
	path := toolPath(result.toolArgs)
	if path == "" {
		return fmt.Sprintf("🔧 [%s]", result.toolName)
	}
	return fmt.Sprintf("🔧 [%s] %s", result.toolName, path)
}

func formatEditedToolResult(result toolResult) string {
	return formatEditedToolResultWithTranslator(i18n.New(i18n.LanguageEN), result)
}

func formatEditedToolResultWithTranslator(tr i18n.Translator, result toolResult) string {
	path := toolPath(result.toolArgs)
	if result.diff != nil && result.diff.Path != "" {
		path = result.diff.Path
	}
	if path == "" {
		path = "(unknown)"
	}

	summary := result.summary
	if result.diff != nil {
		summary = fmt.Sprintf("(+%d -%d)", result.diff.Added, result.diff.Deleted)
	}

	header := tr.Text(i18n.MsgToolEdited, path)
	if summary != "" {
		header += " " + summary
	}

	if result.diff == nil || strings.TrimSpace(result.diff.Unified) == "" {
		return header
	}
	diffLines := formatUnifiedDiffExcerpt(result.diff.Unified)
	if diffLines == "" {
		return header
	}
	return header + "\n" + diffLines
}

var unifiedHunkRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

func formatUnifiedDiffExcerpt(unified string) string {
	var lines []string
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(strings.TrimRight(unified, "\n"), "\n") {
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || line == "" {
			continue
		}
		if matches := unifiedHunkRe.FindStringSubmatch(line); matches != nil {
			oldLine, _ = strconv.Atoi(matches[1])
			newLine, _ = strconv.Atoi(matches[2])
			continue
		}
		if oldLine == 0 && newLine == 0 {
			continue
		}

		kind := line[0]
		text := ""
		if len(line) > 1 {
			text = line[1:]
		}

		switch kind {
		case ' ':
			lines = append(lines, formatDiffExcerptLine(newLine, ' ', text))
			oldLine++
			newLine++
		case '-':
			lines = append(lines, formatDiffExcerptLine(oldLine, '-', text))
			oldLine++
		case '+':
			lines = append(lines, formatDiffExcerptLine(newLine, '+', text))
			newLine++
		}
	}
	return strings.Join(lines, "\n")
}

func formatDiffExcerptLine(lineNo int, kind byte, text string) string {
	return fmt.Sprintf("    %-4d %c%s", lineNo, kind, text)
}

func toolPath(args map[string]any) string {
	if args == nil {
		return ""
	}
	path, _ := args["path"].(string)
	return path
}

func summarizeWriteToolResult(result string) string {
	lines := strings.Split(result, "\n")
	diff := ""
	deleted := ""
	added := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "Diff: ") {
			diff = strings.TrimPrefix(line, "Diff: ")
			continue
		}
		if strings.HasPrefix(line, "- lines: ") {
			deleted = strings.TrimPrefix(line, "- lines: ")
			continue
		}
		if strings.HasPrefix(line, "+ lines: ") {
			added = strings.TrimPrefix(line, "+ lines: ")
		}
	}
	if diff != "" && (deleted != "" || added != "") {
		return fmt.Sprintf("%s (-%s +%s)", diff, deleted, added)
	}
	if diff != "" {
		return diff
	}
	return i18n.New(i18n.LanguageEN).Text(i18n.MsgToolResultWritten)
}

func summarizeFileDiff(diff *tools.FileDiff) string {
	if diff == nil {
		return ""
	}
	suffix := ""
	if diff.Truncated {
		suffix = " large"
	}
	return fmt.Sprintf("+%d -%d%s (-%s +%s)",
		diff.Added,
		diff.Deleted,
		suffix,
		formatLineRangesForDisplay(diff.DeletedLines),
		formatLineRangesForDisplay(diff.AddedLines),
	)
}

func formatLineRangesForDisplay(lines []int) string {
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
		ranges = append(ranges, formatLineRangeForDisplay(start, prev))
		start, prev = line, line
	}
	ranges = append(ranges, formatLineRangeForDisplay(start, prev))
	return strings.Join(ranges, ",")
}

func formatLineRangeForDisplay(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// compactBashOutput compresses bash tool output for summary display by removing blank lines.
func compactBashOutput(s string) string {
	var sb strings.Builder
	prevBlank := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevBlank {
				sb.WriteString("\n")
			}
			prevBlank = true
			continue
		}
		prevBlank = false
		sb.WriteString(trimmed)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

// truncate shortens s so its terminal display width does not exceed maxWidth,
// appending "..." when truncation occurs. Width is measured in display cells
// (CJK runes count as 2, ANSI escape sequences as 0) so the result lines up
// correctly in the TUI grid.
func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	const suffix = "..."
	target := maxWidth - lipgloss.Width(suffix)
	if target <= 0 {
		return suffix
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > target {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + suffix
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
