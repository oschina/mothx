package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestApprovalArgumentLabelsFollowTranslator(t *testing.T) {
	args := map[string]any{
		"command": "go test ./internal/tui/...",
		"timeout": 120,
		"async":   true,
	}
	english := formatApprovalArgsWithTranslator(i18n.New(i18n.LanguageEN), "bash", args)
	chinese := formatApprovalArgsWithTranslator(i18n.New(i18n.LanguageZH), "bash", args)
	if !strings.Contains(english, "command:") || !strings.Contains(english, "timeout: 120") || !strings.Contains(english, "async: true") {
		t.Fatalf("English approval details missing labels: %q", english)
	}
	if !strings.Contains(chinese, "命令：") || !strings.Contains(chinese, "超时：120") || !strings.Contains(chinese, "异步：true") {
		t.Fatalf("Chinese approval details missing translated labels: %q", chinese)
	}
	for _, raw := range []string{english, chinese} {
		if !strings.Contains(raw, "go test ./internal/tui/...") {
			t.Fatalf("approval command was not preserved: %q", raw)
		}
	}
}

func TestEditApprovalArgumentLabelsFollowTranslator(t *testing.T) {
	args := map[string]any{
		"path": "main.go",
		"edits": []any{
			map[string]any{"oldText": "package old", "newText": "package new"},
		},
	}
	english := formatEditApprovalArgsWithTranslator(i18n.New(i18n.LanguageEN), args)
	chinese := formatEditApprovalArgsWithTranslator(i18n.New(i18n.LanguageZH), args)
	if !strings.HasPrefix(english, "path: main.go\n") || !strings.Contains(english, "--- main.go") {
		t.Fatalf("English edit details = %q", english)
	}
	if !strings.HasPrefix(chinese, "路径：main.go\n") || !strings.Contains(chinese, "--- main.go") {
		t.Fatalf("Chinese edit details = %q", chinese)
	}
}
