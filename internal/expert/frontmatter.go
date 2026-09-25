package expert

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// frontmatterDelimiter opens and closes a frontmatter block.
const frontmatterDelimiter = "---"

// parseFrontmatter splits a persona markdown file into its frontmatter fields
// and the trimmed body prompt. fallbackName (typically the md file name
// without extension) is used when the frontmatter is absent or omits name.
// An error is reported only for unparsable frontmatter: an unclosed block or
// an invalid integer field.
//
// Supported syntax (minimal, hand-written; the repo intentionally has no YAML
// dependency):
//   - file starting with a "---" line, closed by the next "---" line;
//   - "key: value" with optional single/double quotes (quotes are stripped);
//   - inline lists "key: [a, b, \"c d\"]" and block lists "key:" followed by
//     "- item" lines;
//   - "#" comment lines and unknown keys are ignored; CJK values are kept
//     verbatim; keys match case-insensitively.
func parseFrontmatter(content, fallbackName string) (Frontmatter, string, error) {
	lines := splitLines(content)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		// No frontmatter: the whole file is the prompt.
		return Frontmatter{Name: fallbackName}, strings.TrimSpace(strings.Join(lines, "\n")), nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelimiter {
			end = i
			break
		}
	}
	if end < 0 {
		return Frontmatter{}, "", errors.New("unclosed frontmatter: missing terminating --- line")
	}
	fm := Frontmatter{Name: fallbackName}
	if err := parseFrontmatterFields(lines[1:end], &fm); err != nil {
		return Frontmatter{}, "", err
	}
	if strings.TrimSpace(fm.Name) == "" {
		fm.Name = fallbackName
	}
	prompt := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return fm, prompt, nil
}

// splitLines splits content on \n and trims \r so CRLF files parse identically.
func splitLines(content string) []string {
	raw := strings.Split(content, "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// parseFrontmatterFields applies "key: value" lines to fm. Unknown keys and
// malformed lines are ignored except for invalid integer fields, which are
// reported as errors.
func parseFrontmatterFields(lines []string, fm *Frontmatter) error {
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			// Possible block list: consume subsequent "- item" lines.
			items, next := collectBlockList(lines, i+1)
			i = next - 1
			if key == "tools" && len(items) > 0 {
				fm.Tools = append(fm.Tools, items...)
			}
			continue
		}
		switch key {
		case "name":
			fm.Name = unquote(value)
		case "description":
			fm.Description = unquote(value)
		case "role":
			fm.Role = unquote(value)
		case "emoji":
			fm.Emoji = unquote(value)
		case "color":
			fm.Color = unquote(value)
		case "vibe":
			fm.Vibe = unquote(value)
		case "mode":
			fm.Mode = unquote(value)
		case "work_dir":
			fm.WorkDir = unquote(value)
		case "worktree":
			fm.Worktree = strings.EqualFold(unquote(value), "true")
		case "tools":
			fm.Tools = parseInlineList(value)
		case "max_iterations":
			n, err := strconv.Atoi(unquote(value))
			if err != nil {
				return fmt.Errorf("invalid max_iterations %q", value)
			}
			fm.MaxIterations = n
		default:
			// Unknown keys are ignored.
		}
	}
	return nil
}

// collectBlockList consumes "- item" lines starting at index start and
// returns the unquoted items plus the index of the first non-item line.
func collectBlockList(lines []string, start int) ([]string, int) {
	var items []string
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "-") {
			break
		}
		item := unquote(strings.TrimSpace(trimmed[1:]))
		if item != "" {
			items = append(items, item)
		}
	}
	return items, i
}

// parseInlineList parses "[a, b, \"c d\"]" (quote-aware comma splitting) and
// tolerates a bare scalar value as a single-item list.
func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = value[1 : len(value)-1]
	}
	var parts []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case quote != 0:
			cur.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
			cur.WriteByte(ch)
		case ch == ',':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	parts = append(parts, cur.String())
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := unquote(strings.TrimSpace(part))
		if item != "" {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

// unquote strips one layer of matching single or double quotes.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
