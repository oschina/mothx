package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/contextfiles"
	"github.com/oschina/mothx/internal/esm"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/tui/components/editor"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// ansiRe matches ANSI CSI escape sequences (colours, bold, etc.).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func trimLineRightSpace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func TestRenderHeaderShowsMothXRenameNotice(t *testing.T) {
	got := stripANSI(renderHeader(80, "1.2.3", "openai", "gpt-4o", "/repo"))

	for _, want := range []string{
		"██   ██  ███  ████ █  █ █  █",
		"MothX (1.2.3)",
		"openai | gpt-4o",
		"Make OSCHINA Tokens Harness eXecution",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderHeader() = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "VibeCoding (1.2.3)") {
		t.Fatalf("renderHeader() kept old title: %q", got)
	}
}

func TestRenderEditToolResultShowsCompactDiff(t *testing.T) {
	app := &App{}
	result := toolResult{
		toolName: "edit",
		toolArgs: map[string]any{"path": "internal/acp/acp.go"},
		diff: &tools.FileDiff{
			Path:    "internal/acp/acp.go",
			Added:   1,
			Deleted: 1,
			Unified: strings.Join([]string{
				"--- internal/acp/acp.go",
				"+++ internal/acp/acp.go",
				"@@ -551,3 +551,3 @@",
				" \tctx, cancel := context.WithCancel(context.Background())",
				"-\tpromptKey := rawIDKey(req.ID)",
				"+\tpromptKey := mcp.RawIDKey(req.ID)",
				" \trt.cancelMu.Lock()",
				"",
			}, "\n"),
		},
	}

	got := trimLineRightSpace(stripANSI(app.renderToolResult(result)))
	want := strings.Join([]string{
		"• Edited internal/acp/acp.go (+1 -1)",
		"    551       ctx, cancel := context.WithCancel(context.Background())",
		"    552  -    promptKey := rawIDKey(req.ID)",
		"    552  +    promptKey := mcp.RawIDKey(req.ID)",
		"    553       rt.cancelMu.Lock()",
	}, "\n")

	if got != want {
		t.Fatalf("renderToolResult(edit) =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderBashToolResultKeepsOutputRaw(t *testing.T) {
	app := &App{}
	summary := "[stdout]\n\u001b[32m+added\u001b[0m\n context\r\n[exit_code]\n0"
	got := app.renderToolResult(toolResult{
		toolName: "bash",
		summary:  summary,
	})

	parts := strings.SplitN(got, "\n", 2)
	if len(parts) != 2 {
		t.Fatalf("renderToolResult(bash) = %q, want header and body", got)
	}
	if parts[1] != summary {
		t.Fatalf("bash output body was modified:\n got %q\nwant %q", parts[1], summary)
	}
	if strings.Contains(parts[1], "\x1b[3m") {
		t.Fatalf("bash output body should not inherit TUI italic styling: %q", parts[1])
	}
}

func TestRenderBashCommandLineShowsCommandAndExitStatus(t *testing.T) {
	app := &App{compactMode: true, translator: i18n.New(i18n.LanguageEN)}
	result := toolResult{
		toolName:    "bash",
		toolArgs:    map[string]any{"command": "go test ./internal/tui"},
		status:      toolResultStatusCompleted,
		fullContent: "[runtime]\nbash\n[command]\ngo test ./internal/tui\n[exit_code]\n0",
		summary:     "[runtime]\nbash\n[command]\ngo test ./internal/tui\n[exit_code]\n0",
	}
	got := stripANSI(app.renderToolResult(result))
	if !strings.Contains(got, "🔧 [bash] go test ./internal/tui (succeeded)") {
		t.Fatalf("successful bash line = %q, want command and success status", got)
	}
	if strings.Contains(got, "[runtime]") {
		t.Fatalf("compact bash line leaked runtime metadata: %q", got)
	}

	unknown := stripANSI(app.renderToolResult(toolResult{
		toolName: "bash",
		status:   toolResultStatusCompleted,
	}))
	if strings.Contains(unknown, "🔧 [bash] bash ") || !strings.Contains(unknown, "command unavailable") {
		t.Fatalf("missing bash command should use an explicit fallback: %q", unknown)
	}

	result.fullContent = "[runtime]\nbash\n[command]\ngo test ./internal/tui\n[exit_code]\n1"
	result.summary = result.fullContent
	got = stripANSI(app.renderToolResult(result))
	if !strings.Contains(got, "go test ./internal/tui (failed (exit code 1))") {
		t.Fatalf("failed bash line = %q, want exit status", got)
	}
}

func TestRenderExpandedBashToolResultKeepsDetailsRaw(t *testing.T) {
	app := &App{}
	output := "\u001b[31m-deleted\u001b[0m\r\n+added"
	got := app.renderExpandedToolResult(toolResult{
		toolName:    "bash",
		fullContent: output,
	})

	if !strings.HasSuffix(got, "---\n"+output) {
		t.Fatalf("expanded bash output was modified: %q", got)
	}
	body := got[strings.Index(got, "\n")+1:]
	if strings.Contains(body, "\x1b[3m") {
		t.Fatalf("expanded bash output body should not inherit TUI italic styling: %q", body)
	}
}

func TestRenderExpandedEditToolResultDoesNotDuplicateDiffExcerpt(t *testing.T) {
	app := &App{}
	got := stripANSI(app.renderExpandedToolResult(toolResult{
		toolName: "edit",
		toolArgs: map[string]any{"path": "file.go"},
		diff: &tools.FileDiff{
			Path:    "file.go",
			Added:   1,
			Deleted: 1,
			Unified: strings.Join([]string{
				"--- file.go",
				"+++ file.go",
				"@@ -1,1 +1,1 @@",
				"-old line",
				"+new line",
				"",
			}, "\n"),
		},
	}))

	if !strings.Contains(got, "• Edited file.go (+1 -1)") {
		t.Fatalf("expanded edit output missing header: %q", got)
	}
	if count := strings.Count(got, "-old line"); count != 1 {
		t.Fatalf("expanded edit output duplicated diff excerpt, count=%d:\n%s", count, got)
	}
	if count := strings.Count(got, "+new line"); count != 1 {
		t.Fatalf("expanded edit output duplicated diff excerpt, count=%d:\n%s", count, got)
	}
}

func TestRenderFooterUsesBuiltinWhenStatusLineDisabled(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.width = 80
	a.ready = true
	a.statusLineOutput = "external footer"

	got := stripANSI(a.renderFooter())
	if strings.Contains(got, "external footer") {
		t.Fatalf("renderFooter() unexpectedly used external status line: %q", got)
	}
	if !strings.Contains(got, "AGENT") {
		t.Fatalf("renderFooter() = %q, want builtin footer", got)
	}
}

func TestRenderFooterUsesExternalStatusLineWhenEnabled(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.width = 80
	a.ready = true
	a.settings.StatusLine.Enabled = true
	a.settings.StatusLine.Command = "ccstatusline"
	a.statusLineOutput = "external footer"

	got := stripANSI(a.renderFooter())
	if !strings.Contains(got, "external footer") {
		t.Fatalf("renderFooter() = %q, want external footer", got)
	}
	if strings.Contains(got, "AGENT") {
		t.Fatalf("renderFooter() leaked builtin footer: %q", got)
	}
}

func TestStatusLineCommandShowsOffState(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.handleCommand("/statusline")

	if len(a.messages) == 0 {
		t.Fatal("messages empty after /statusline")
	}
	plain := stripANSI(a.messages[len(a.messages)-1])
	if !strings.Contains(plain, "Status line: OFF") {
		t.Fatalf("/statusline output = %q, want OFF state", plain)
	}
}

func TestStatusLineCommandShowsActiveStateAndError(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.settings.StatusLine.Enabled = true
	a.settings.StatusLine.Command = "ccstatusline"
	a.statusLineOutput = "ready"
	a.statusLineLastError = "boom"

	a.handleCommand("/statusline")

	if len(a.messages) == 0 {
		t.Fatal("messages empty after /statusline")
	}
	plain := stripANSI(a.messages[len(a.messages)-1])
	for _, want := range []string{"Status line: ON", "Command: ccstatusline", "Render: external footer active", "Last error: boom"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("/statusline output = %q, want substring %q", plain, want)
		}
	}
}

func TestStatusLineCommandOnWritesProjectSettings(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.handleCommand("/statusline on")

	data, err := os.ReadFile(config.ProjectSettingsPath())
	if err != nil {
		t.Fatalf("read project settings: %v", err)
	}
	if !strings.Contains(string(data), `"enabled": true`) {
		t.Fatalf("project settings = %s, want enabled true", string(data))
	}
	if !strings.Contains(string(data), `"command": "ccstatusline"`) {
		t.Fatalf("project settings = %s, want ccstatusline command", string(data))
	}
	if !a.settings.StatusLine.Enabled {
		t.Fatal("app settings statusLine not enabled")
	}
}

func TestStatusLineCommandOffClearsRuntimeOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.settings.StatusLine.Enabled = true
	a.settings.StatusLine.Command = "ccstatusline"
	a.statusLineOutput = "external footer"
	a.statusLineLastError = "boom"

	a.handleCommand("/statusline off")

	if a.settings.StatusLine.Enabled {
		t.Fatal("statusLine still enabled after off")
	}
	if a.statusLineOutput != "" {
		t.Fatalf("statusLineOutput = %q, want empty", a.statusLineOutput)
	}
	if a.statusLineLastError != "" {
		t.Fatalf("statusLineLastError = %q, want empty", a.statusLineLastError)
	}
}

func TestStatusLineCommandCommandWritesProjectSettings(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.handleCommand("/statusline command echo hello")

	data, err := os.ReadFile(config.ProjectSettingsPath())
	if err != nil {
		t.Fatalf("read project settings: %v", err)
	}
	if !strings.Contains(string(data), `"command": "echo hello"`) {
		t.Fatalf("project settings = %s, want command", string(data))
	}
	if got := a.settings.StatusLine.Command; got != "echo hello" {
		t.Fatalf("command = %q, want echo hello", got)
	}
}

func TestStatusLineCommandRefreshWritesProjectSettings(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.handleCommand("/statusline refresh 7")

	data, err := os.ReadFile(config.ProjectSettingsPath())
	if err != nil {
		t.Fatalf("read project settings: %v", err)
	}
	if !strings.Contains(string(data), `"refreshInterval": 7`) {
		t.Fatalf("project settings = %s, want refreshInterval", string(data))
	}
	if got := a.settings.StatusLine.RefreshInterval; got != 7 {
		t.Fatalf("refreshInterval = %d, want 7", got)
	}
}

func TestAssistantMarkdownRendererUsesViewportWidth(t *testing.T) {
	app := &App{
		width:               60,
		assistantRaw:        map[int]string{0: "请看 https://gitee.com/oschina/platform/pulls/11938 这里"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	got := stripANSI(app.renderAssistantMessage(0))
	flattened := strings.ReplaceAll(strings.ReplaceAll(got, "\n", ""), " ", "")
	if !strings.Contains(flattened, "https://gitee.com/oschina/platform/pulls/11938") {
		t.Fatalf("renderAssistantMessage() = %q, want URL order preserved", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("rendered line width = %d, want <= %d: %q", width, app.width, line)
		}
	}
}

func TestWindowResizeMarksAssistantMarkdownDirty(t *testing.T) {
	app := &App{
		assistantRaw:        map[int]string{0: "hello"},
		assistantRendered:   map[int]string{0: "old"},
		assistantDirty:      make(map[int]bool),
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
	}

	model, _ := app.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	updated := model.(*App)

	if updated.mdRenderer == nil {
		t.Fatal("mdRenderer is nil after resize")
	}
	if !updated.assistantDirty[0] {
		t.Fatal("assistantDirty[0] = false, want true after resize")
	}
}

func TestLiveAssistantMessageRendersCodeBlocks(t *testing.T) {
	app := &App{
		width:               80,
		assistantRaw:        map[int]string{0: "Here is code:\n\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\nDone"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	app.updateViewportContent()
	plain := stripANSI(app.liveContent)
	if !strings.Contains(plain, "Assistant:") {
		t.Fatalf("live content missing assistant prefix: %q", plain)
	}
	if !strings.Contains(plain, "func main") {
		t.Fatalf("live code block content missing 'func main': %q", plain)
	}
	if !strings.Contains(plain, "Done") {
		t.Fatalf("live content missing trailing text 'Done': %q", plain)
	}
	if strings.Contains(plain, "```") {
		t.Fatalf("live content must not contain raw backtick fences: %q", plain)
	}
}

func TestAssistantMarkdownRenderedLinesStayWithinViewport(t *testing.T) {
	app := &App{
		width: 42,
		assistantRaw: map[int]string{0: strings.Join([]string{
			"```text",
			"this-is-a-very-long-token-that-must-wrap-without-splitting-ansi",
			"```",
		}, "\n")},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	rendered := app.renderAssistantMessage(0)
	if !strings.Contains(stripANSI(rendered), "this-is-a-very-long-token") {
		t.Fatalf("rendered markdown missing code content: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("rendered line width = %d, want <= %d: %q", width, app.width, line)
		}
	}
}

func TestAssistantCommonMarkdownUsesRenderer(t *testing.T) {
	app := &App{
		width:               60,
		assistantRaw:        map[int]string{0: "# Summary\n\n- first item\n- second item"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	app.updateViewportContent()
	if len(app.assistantRendered) == 0 {
		t.Fatal("assistantRendered is empty, want common markdown rendered through gsm")
	}
	plain := stripANSI(app.liveContent)
	for _, want := range []string{"Assistant:", "Summary", "first item", "second item"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered markdown missing %q: %q", want, plain)
		}
	}
	for _, line := range strings.Split(app.liveContent, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("rendered line width = %d, want <= %d: %q", width, app.width, line)
		}
	}
}

func TestAssistantMarkdownTableDoesNotOffsetFirstTableLine(t *testing.T) {
	app := &App{
		width: 80,
		assistantRaw: map[int]string{0: strings.Join([]string{
			"| Name | Status |",
			"|------|--------|",
			"| Build | Passing |",
			"| Tests | Passing |",
		}, "\n")},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	plain := stripANSI(app.renderAssistantMessage(0))
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "Assistant:" {
		t.Fatalf("assistant table should render label on its own line: %q", plain)
	}
	tableLines := 0
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.ContainsAny(trimmed, "┌├└│") {
			continue
		}
		tableLines++
		if line != trimmed {
			t.Fatalf("table line has leading padding and will render misaligned: %q\nall:\n%s", line, plain)
		}
		if strings.Contains(line, "Assistant:") {
			t.Fatalf("assistant prefix leaked onto table line: %q\nall:\n%s", line, plain)
		}
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("table line width = %d, want <= %d: %q", width, app.width, line)
		}
	}
	if tableLines == 0 {
		t.Fatalf("rendered markdown table was not found: %q", plain)
	}
}

func TestAssistantMarkdownPreservesFilenameOrder(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "plain prose",
			raw:  "用户读取要求 AGENTS.md 文件",
		},
		{
			name: "inline code",
			raw:  "用户读取要求 `AGENTS.md` 文件",
		},
		{
			name: "agents discovery phrase",
			raw:  "- `internal/contextfiles/` — `AGENTS.md` / `CLAUDE.md` discovery",
		},
		{
			name: "inline directory path",
			raw:  "检查 `internal/agent/` 目录",
		},
	}
	for _, width := range []int{16, 20, 24, 32, 48, 72} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s/%d", tt.name, width), func(t *testing.T) {
				app := &App{
					width:               width,
					assistantRaw:        map[int]string{0: tt.raw},
					assistantRendered:   make(map[int]string),
					assistantDirty:      map[int]bool{0: true},
					currentAssistantIdx: 0,
					currentThinkIdx:     -1,
				}
				app.configureMarkdownRenderer()

				plain := stripANSI(app.renderAssistantMessage(0))
				flattened := removeWhitespace(plain)
				if strings.Contains(flattened, "AG.mdENTS") {
					t.Fatalf("rendered filename order is corrupted: %q", plain)
				}
				if strings.Contains(flattened, "/internalagent/") {
					t.Fatalf("rendered directory path order is corrupted: %q", plain)
				}
				if !strings.Contains(flattened, "AGENTS.md") {
					if strings.Contains(tt.raw, "AGENTS.md") {
						t.Fatalf("rendered output lost AGENTS.md order:\nraw: %q\nrendered: %q\nflattened: %q", tt.raw, plain, flattened)
					}
				}
				if strings.Contains(tt.raw, "internal/agent/") && !strings.Contains(flattened, "internal/agent/") {
					t.Fatalf("rendered output lost internal/agent/ order:\nraw: %q\nrendered: %q\nflattened: %q", tt.raw, plain, flattened)
				}
				if strings.Contains(tt.raw, "CLAUDE.md") && !strings.Contains(flattened, "CLAUDE.md") {
					t.Fatalf("rendered output lost CLAUDE.md order:\nraw: %q\nrendered: %q\nflattened: %q", tt.raw, plain, flattened)
				}
				for _, line := range strings.Split(app.renderAssistantMessage(0), "\n") {
					// Allow +2 tolerance for xansi.Wrap breakpoint behavior
					// where lines can slightly exceed target width when breaking at natural boundaries like /
					if lineWidth := lipgloss.Width(line); lineWidth > app.width+2 {
						t.Fatalf("rendered line width = %d, want <= %d: %q", lineWidth, app.width+2, line)
					}
				}
			})
		}
	}
}

func TestProcessInputBlocksWhileManualCompactionRuns(t *testing.T) {
	app := &App{
		input:                  editor.New(80),
		manualCompactionActive: true,
	}

	cmd := app.processInput("continue work")
	if cmd != nil {
		t.Fatal("processInput returned command while manual compaction was active")
	}
	if len(app.messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(app.messages))
	}
	if !strings.Contains(stripANSI(app.messages[0]), "Cannot send input while context compaction is running.") {
		t.Fatalf("unexpected status message: %q", stripANSI(app.messages[0]))
	}
}

func TestAssistantMarkdownDoesNotRenderBlankAfterPrefix(t *testing.T) {
	app := &App{
		width:               80,
		assistantRaw:        map[int]string{0: "用户读取要求 `AGENTS.md` 文件"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	rendered := stripANSI(app.renderAssistantMessage(0))
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		t.Fatal("rendered assistant message is empty")
	}
	if strings.TrimSpace(lines[0]) == "Assistant:" {
		t.Fatalf("assistant prefix rendered on a blank line: %q", rendered)
	}
	if !strings.Contains(lines[0], "用户读取要求") || !strings.Contains(removeWhitespace(lines[0]), "AGENTS.md") {
		t.Fatalf("first rendered line missing assistant content: %q", rendered)
	}
}

func TestAssistantRendersAGENTSMarkdownFixture(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md fixture: %v", err)
	}
	if strings.TrimSpace(string(content)) == "" {
		t.Fatal("AGENTS.md fixture is empty")
	}

	app := &App{
		width:               72,
		assistantRaw:        map[int]string{0: string(content)},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	rendered := app.renderAssistantMessage(0)
	plain := stripANSI(rendered)
	if len(app.assistantRendered) == 0 {
		t.Fatal("assistantRendered is empty, want AGENTS.md rendered through gsm")
	}
	if renderedLen := len(app.assistantRendered[0]); renderedLen > len(content)*20 {
		t.Fatalf("rendered AGENTS.md intermediate is too large: got %d bytes from %d input bytes", renderedLen, len(content))
	}
	for _, want := range []string{
		"MothX",
		"Project snapshot",
		"Architecture notes",
		"AGENTS.md",
		"make build",
		"make test",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered AGENTS.md missing %q", want)
		}
	}
	if strings.Contains(plain, "```") {
		t.Fatalf("rendered AGENTS.md should not expose raw markdown fences")
	}
	flattened := removeWhitespace(plain)
	for _, want := range []string{"AGENTS.md", "docs/en/", "docs/zh/"} {
		if !strings.Contains(flattened, want) {
			t.Fatalf("rendered AGENTS.md lost filename order for %q", want)
		}
	}
	if strings.Contains(flattened, "AG.mdENTS") {
		t.Fatalf("rendered AGENTS.md contains corrupted AGENTS.md ordering")
	}
	if maxBlank := maxConsecutiveBlankLines(plain); maxBlank > 2 {
		t.Fatalf("rendered AGENTS.md has %d consecutive blank lines", maxBlank)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("rendered AGENTS.md line width = %d, want <= %d: %q", width, app.width, line)
		}
	}
}

func removeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func maxConsecutiveBlankLines(s string) int {
	maxBlank := 0
	current := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			current++
			if current > maxBlank {
				maxBlank = current
			}
			continue
		}
		current = 0
	}
	return maxBlank
}

func TestLiveAssistantMessageRendersMarkdown(t *testing.T) {
	app := &App{
		width:               50,
		assistantRaw:        map[int]string{0: strings.Repeat("https://example.com/path/", 8)},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	app.updateViewportContent()
	if len(app.assistantRendered) != 0 {
		t.Fatalf("assistantRendered len = %d, want 0 for prose without fenced code blocks", len(app.assistantRendered))
	}
	if !strings.Contains(stripANSI(app.liveContent), "Assistant: ") {
		t.Fatalf("liveContent missing assistant prefix: %q", app.liveContent)
	}
}

func TestPlainAssistantMessageWrapsWithoutMarkdownWordSplitting(t *testing.T) {
	app := &App{
		width:               40,
		assistantRaw:        map[int]string{0: "修复 /clear 未清理 transcript rendering state"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      map[int]bool{0: true},
		currentAssistantIdx: 0,
		currentThinkIdx:     -1,
	}
	app.configureMarkdownRenderer()

	app.updateViewportContent()
	plain := stripANSI(app.liveContent)
	if strings.Contains(plain, "修  /复clear") || strings.Contains(plain, "v.01\n36") {
		t.Fatalf("plain assistant text was awkwardly split: %q", plain)
	}
	if !strings.Contains(plain, "修复 /clear") {
		t.Fatalf("plain assistant text missing expected phrase: %q", plain)
	}
	if len(app.assistantRendered) != 0 {
		t.Fatalf("assistantRendered len = %d, want 0 for plain prose", len(app.assistantRendered))
	}
}

func TestThinkMessageWrapsAndPreservesContent(t *testing.T) {
	a := &App{
		width:               34,
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
		printedMessageIdx:   make(map[int]bool),
		thinkRaw:            make(map[int]string),
	}
	first := "正在分析用户读取要求 AGENTS.md 文件的上下文，"
	second := "then-checking-a-very-long-unspaced-token-for-wrapping"

	a.handleAgentEvent(agent.Event{Type: agent.EventThinkDelta, ThinkDelta: first})
	a.handleAgentEvent(agent.Event{Type: agent.EventThinkDelta, ThinkDelta: second})
	a.updateViewportContent()

	rendered := a.renderThinkMessage(a.currentThinkIdx)
	plain := stripANSI(rendered)
	flattened := removeWhitespace(plain)
	for _, want := range []string{"think:", "AGENTS.md", "then-checking-a-very-long-unspaced-token-for-wrapping"} {
		if !strings.Contains(flattened, removeWhitespace(want)) {
			t.Fatalf("rendered think message missing %q:\n%s", want, plain)
		}
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := lipgloss.Width(line); width > a.width {
			t.Fatalf("think line width = %d, want <= %d: %q", width, a.width, line)
		}
	}

	a.printMessageOnce(a.currentThinkIdx)
	transcript := stripANSI(a.liveContent)
	if !strings.Contains(removeWhitespace(transcript), "AGENTS.md") ||
		!strings.Contains(removeWhitespace(transcript), "then-checking-a-very-long-unspaced-token-for-wrapping") {
		t.Fatalf("transcript lost think content: %q", transcript)
	}
}

func TestThinkMessageUsesPlainMixedCJKASCIIWrapping(t *testing.T) {
	for _, width := range []int{18, 22, 26} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			a := &App{
				width:               width,
				currentAssistantIdx: -1,
				currentThinkIdx:     -1,
				thinkRaw:            map[int]string{0: "用户查看想 AGENTS 文件内容"},
			}

			rendered := a.renderThinkMessage(0)
			plain := stripANSI(rendered)
			flattened := removeWhitespace(plain)
			if !strings.Contains(flattened, "用户查看想AGENTS文件内容") {
				t.Fatalf("rendered think message order changed:\nplain: %q\nflattened: %q", plain, flattened)
			}
			if strings.Contains(flattened, "AG文件ENTS") {
				t.Fatalf("rendered think message interleaved CJK and ASCII token:\nplain: %q\nflattened: %q", plain, flattened)
			}
			for _, line := range strings.Split(rendered, "\n") {
				if lineWidth := lipgloss.Width(line); lineWidth > a.width {
					t.Fatalf("think line width = %d, want <= %d: %q", lineWidth, a.width, line)
				}
			}
		})
	}
}

func TestViewKeepsTranscriptInMainOutputWhenNoProgram(t *testing.T) {
	app := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.ready = true
	app.width = 80
	app.height = 8
	app.input = app.input.SetWidth(76)
	app.messages = []string{strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
	}, "\n")}
	app.updateViewportContent()

	got := stripANSI(app.View())
	if !strings.Contains(got, "line 1") {
		t.Fatalf("View() missing oldest transcript line:\n%s", got)
	}
	if !strings.Contains(got, "line 8") {
		t.Fatalf("View() missing newest transcript line:\n%s", got)
	}
	if !strings.Contains(got, app.input.Placeholder()) {
		t.Fatalf("View() missing input placeholder:\n%s", got)
	}
	if !strings.Contains(got, app.translator.Text(i18n.MsgFooterMainHints)) {
		t.Fatalf("View() missing footer:\n%s", got)
	}
}

func TestViewDoesNotForceOuterHeight(t *testing.T) {
	app := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.ready = true
	app.width = 80
	app.height = 8
	app.input = app.input.SetWidth(76)
	app.messages = []string{strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
	}, "\n")}
	app.updateViewportContent()

	if got := lipgloss.Height(app.View()); got <= app.height {
		t.Fatalf("View() height = %d, want > %d so terminal scrollback can own transcript history", got, app.height)
	}
}

func TestMouseWheelDoesNotScrollTranscript(t *testing.T) {
	app := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.ready = true
	app.width = 80
	app.height = 12
	app.input = app.input.SetWidth(76)
	app.messages = []string{strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
		"line 10",
		"line 11",
		"line 12",
	}, "\n")}
	app.updateViewportContent()

	before := app.liveContent

	app.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})

	if after := app.liveContent; after != before {
		t.Fatalf("mouse wheel changed transcript content:\nbefore:\n%s\n\nafter:\n%s", before, after)
	}
}

// ─── formatCachePercent ───────────────────────────────────────────────────────

func TestFormatCachePercent(t *testing.T) {
	tests := []struct {
		name             string
		totalInputTokens int
		totalCacheRead   int
		totalCacheWrite  int
		want             string
	}{
		// ── No data ──────────────────────────────────────────────────────────
		{
			name: "no_data_empty",
		},
		// ── Input tokens present ─────────────────────────────────────────────
		{
			name:             "input_no_cache_zero_pct",
			totalInputTokens: 1000,
			want:             "Cache: 0%",
		},
		{
			name:             "cache_25pct",
			totalInputTokens: 1000,
			totalCacheRead:   250,
			want:             "Cache: 25%",
		},
		{
			name:             "cache_50pct",
			totalInputTokens: 1000,
			totalCacheRead:   500,
			want:             "Cache: 50%",
		},
		{
			name:             "cache_75pct",
			totalInputTokens: 1000,
			totalCacheRead:   750,
			want:             "Cache: 75%",
		},
		{
			name:             "cache_100pct_exact",
			totalInputTokens: 1000,
			totalCacheRead:   1000,
			want:             "Cache: 100%",
		},
		// Defensive cap when read > input
		{
			name:             "cache_read_exceeds_input_capped_at_100pct",
			totalInputTokens: 100,
			totalCacheRead:   200,
			want:             "Cache: 100%",
		},
		// Multi-turn accumulation across several requests
		{
			name:             "multi_turn_accumulated_75pct",
			totalInputTokens: 4000,
			totalCacheRead:   3000,
			want:             "Cache: 75%",
		},
		// ── Fallback path: no input tokens yet ───────────────────────────────
		// CacheRead takes priority over CacheWrite in the fallback
		{
			name:           "no_input_cache_read_fallback",
			totalCacheRead: 500,
			want:           "CacheRead: 500",
		},
		{
			name:            "no_input_cache_write_fallback",
			totalCacheWrite: 1000,
			want:            "CacheWrite: 1000",
		},
		{
			name:            "no_input_both_read_wins_over_write",
			totalCacheRead:  500,
			totalCacheWrite: 1000,
			want:            "CacheRead: 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{
				totalInputTokens: tt.totalInputTokens,
				totalCacheRead:   tt.totalCacheRead,
				totalCacheWrite:  tt.totalCacheWrite,
			}
			got := a.formatCachePercent()
			if got != tt.want {
				t.Errorf("formatCachePercent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ─── renderFooter cache content ───────────────────────────────────────────────

func TestRenderFooterCacheContent(t *testing.T) {
	tests := []struct {
		name             string
		totalInputTokens int
		totalCacheRead   int
		totalCacheWrite  int
		wantContains     string // expected substring in stripped footer
		wantAbsent       string // must NOT appear in stripped footer
	}{
		// No cache data → "Cache:" must not appear at all
		{
			name:       "no_data_cache_absent",
			wantAbsent: "Cache:",
		},
		{
			name:             "zero_pct_shown",
			totalInputTokens: 1000,
			wantContains:     "Cache: 0%",
		},
		{
			name:             "cache_25pct_shown",
			totalInputTokens: 1000,
			totalCacheRead:   250,
			wantContains:     "Cache: 25%",
		},
		// Boundary just below 50% threshold
		{
			name:             "cache_49pct_shown",
			totalInputTokens: 1000,
			totalCacheRead:   490,
			wantContains:     "Cache: 49%",
		},
		// Boundary at exactly 50%
		{
			name:             "cache_50pct_shown",
			totalInputTokens: 1000,
			totalCacheRead:   500,
			wantContains:     "Cache: 50%",
		},
		{
			name:             "cache_75pct_shown",
			totalInputTokens: 1000,
			totalCacheRead:   750,
			wantContains:     "Cache: 75%",
		},
		{
			name:             "cache_100pct_shown",
			totalInputTokens: 1000,
			totalCacheRead:   1000,
			wantContains:     "Cache: 100%",
		},
		// Fallback paths visible in footer
		{
			name:            "cache_write_fallback_shown",
			totalCacheWrite: 5000,
			wantContains:    "CacheWrite: 5000",
		},
		{
			name:           "cache_read_fallback_shown",
			totalCacheRead: 800,
			wantContains:   "CacheRead: 800",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{
				totalInputTokens: tt.totalInputTokens,
				totalCacheRead:   tt.totalCacheRead,
				totalCacheWrite:  tt.totalCacheWrite,
			}
			footer := stripANSI(a.renderFooter())

			if tt.wantContains != "" && !strings.Contains(footer, tt.wantContains) {
				t.Errorf("renderFooter() = %q\n\twant substring %q", footer, tt.wantContains)
			}
			if tt.wantAbsent != "" && strings.Contains(footer, tt.wantAbsent) {
				t.Errorf("renderFooter() = %q\n\twant %q to be absent", footer, tt.wantAbsent)
			}
		})
	}
}

// ─── Highlight threshold ──────────────────────────────────────────────────────

// TestCacheHighlightThreshold verifies the ≥50% rule that gates statusStyle
// in renderFooter. At exactly 49% the cache string must not be highlighted;
// at exactly 50% it must be.
//
// Because lipgloss omits ANSI codes when there is no TTY, we verify the
// decision by checking whether the raw footer embeds statusStyle.Render()
// output for the specific cache string. statusStyle.Render(x) == x when the
// renderer is in Ascii mode, but the branch taken in renderFooter differs:
// the ≥50% branch always passes through statusStyle.Render(), the <50%
// branch uses the plain string directly. We therefore compare the two raw
// footers: they should differ iff ANSI codes are emitted, and must be
// identical only in purely Ascii rendering environments — in which case the
// test degrades gracefully to a content-only assertion.
func TestCacheHighlightThreshold(t *testing.T) {
	below := &App{totalInputTokens: 1000, totalCacheRead: 490} // 49%
	at := &App{totalInputTokens: 1000, totalCacheRead: 500}    // 50%
	above := &App{totalInputTokens: 1000, totalCacheRead: 750} // 75%

	footerBelow := below.renderFooter()
	footerAt := at.renderFooter()
	footerAbove := above.renderFooter()

	// Content must always be correct regardless of colour support.
	if !strings.Contains(stripANSI(footerBelow), "Cache: 49%") {
		t.Errorf("below-threshold footer = %q, want 'Cache: 49%%'", stripANSI(footerBelow))
	}
	if !strings.Contains(stripANSI(footerAt), "Cache: 50%") {
		t.Errorf("at-threshold footer = %q, want 'Cache: 50%%'", stripANSI(footerAt))
	}
	if !strings.Contains(stripANSI(footerAbove), "Cache: 75%") {
		t.Errorf("above-threshold footer = %q, want 'Cache: 75%%'", stripANSI(footerAbove))
	}

	// When the renderer does produce ANSI codes (e.g. in a real terminal or
	// when the test is run with COLORTERM set), the highlighted footers must
	// contain the statusStyle-rendered string, and the un-highlighted one must
	// not contain it.
	styledAt := statusStyle.Render("Cache: 50%")
	styledAbove := statusStyle.Render("Cache: 75%")
	styledBelow := statusStyle.Render("Cache: 49%")

	if styledAt != "Cache: 50%" {
		// ANSI codes are being emitted; verify correct highlighting.
		if !strings.Contains(footerAt, styledAt) {
			t.Errorf("at-threshold (50%%) footer should apply statusStyle; raw = %q", footerAt)
		}
		if !strings.Contains(footerAbove, styledAbove) {
			t.Errorf("above-threshold (75%%) footer should apply statusStyle; raw = %q", footerAbove)
		}
		if strings.Contains(footerBelow, styledBelow) {
			t.Errorf("below-threshold (49%%) footer must NOT apply statusStyle; raw = %q", footerBelow)
		}
	}
}

func TestRenderFooterShowsBlinkingApprovalAlert(t *testing.T) {
	a := &App{
		waitingForApproval: true,
		spinnerIndex:       0,
		width:              120,
	}
	visible := stripANSI(a.renderFooter())
	if !strings.Contains(visible, "! APPROVAL REQUIRED: ↑/↓ Enter") {
		t.Fatalf("approval footer alert missing: %q", visible)
	}

	a.spinnerIndex = 1
	hidden := stripANSI(a.renderFooter())
	if strings.Contains(hidden, "! APPROVAL REQUIRED: ↑/↓ Enter") {
		t.Fatalf("approval footer alert should blink off on odd tick: %q", hidden)
	}
}

func TestRenderFooterTruncatesRightColumnOnNarrowWidth(t *testing.T) {
	percent := 99.9
	a := &App{
		width:            24,
		sandboxInfo:      "sandbox:none",
		contextUsage:     &ctxpkg.ContextUsage{ContextWindow: 200000, Percent: &percent},
		totalInputTokens: 1000,
		totalCacheRead:   800,
	}
	footer := a.renderBuiltinFooter()
	if h := lipgloss.Height(footer); h != 2 {
		t.Fatalf("footer height = %d, want 2 (right column must not wrap): %q", h, stripANSI(footer))
	}
	for i, line := range strings.Split(footer, "\n") {
		if w := lipgloss.Width(line); w > a.width {
			t.Fatalf("footer line %d width = %d, want <= %d: %q", i, w, a.width, line)
		}
	}
}
func TestPlanUpdateStaysInStickyPanel(t *testing.T) {
	a := &App{
		messages: []string{"You: inspect the repository"},
	}
	plan := &tools.TaskPlan{
		Title: "Fix the TUI",
		Steps: []tools.PlanStep{{Title: "Inspect rendering", Status: "running"}},
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventPlanUpdate, Plan: plan})

	if got, want := len(a.messages), 1; got != want {
		t.Fatalf("plan update changed transcript message count: got %d, want %d", got, want)
	}
	if a.currentPlan != plan {
		t.Fatal("plan update did not set currentPlan")
	}
	if got := stripANSI(renderStickyTodoList(a.currentPlan, 80, 5)); !strings.Contains(got, "Inspect rendering") {
		t.Fatalf("sticky plan panel missing step: %q", got)
	}
}

func TestHandleAgentEventReservesAssistantSlotBeforeTextDelta(t *testing.T) {
	a := &App{
		messages:          []string{"You: hi"},
		assistantRaw:      make(map[int]string),
		assistantRendered: make(map[int]string),
		assistantDirty:    make(map[int]bool),
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	if got, want := len(a.messages), 2; got != want {
		t.Fatalf("len(messages) after turn start = %d, want %d", got, want)
	}
	if got, want := a.currentAssistantIdx, 1; got != want {
		t.Fatalf("currentAssistantIdx = %d, want %d", got, want)
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "Hello"})
	if got, want := a.assistantRaw[1], "Hello"; got != want {
		t.Fatalf("assistantRaw[1] = %q, want %q", got, want)
	}
	if got, want := len(a.messages), 2; got != want {
		t.Fatalf("len(messages) after text delta = %d, want %d", got, want)
	}
}

func TestStreamingAssistantDeltaUsesBuilderUntilTurnEnd(t *testing.T) {
	a := &App{
		messages:            []string{"You: hi"},
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
		assistantRaw:        make(map[int]string),
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "Hello"})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: " world"})

	if got, want := a.assistantRaw[a.currentAssistantIdx], "Hello world"; got != want {
		t.Fatalf("assistantRaw = %q, want %q", got, want)
	}
	if _, ok := a.assistantBuilders[a.currentAssistantIdx]; !ok {
		t.Fatal("assistant builder missing during active stream")
	}

	idx := a.currentAssistantIdx
	a.handleAgentEvent(agent.Event{Type: agent.EventTurnEnd})
	if _, ok := a.assistantBuilders[idx]; ok {
		t.Fatal("assistant builder still retained after turn end")
	}
}

func TestHandleAgentEventCommitsStreamBeforeApproval(t *testing.T) {
	a := &App{
		messages:            []string{"You: hi"},
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
		printedMessageIdx:   make(map[int]bool),
		assistantRaw:        make(map[int]string),
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	a.handleAgentEvent(agent.Event{Type: agent.EventThinkDelta, ThinkDelta: "thinking"})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "I need to run a command."})
	a.handleAgentEvent(agent.Event{
		Type:         agent.EventToolApprovalRequest,
		ApprovalID:   "approval-1",
		ApprovalTool: "bash",
		ApprovalArgs: map[string]any{"command": "go test ./internal/tui"},
	})

	joined := stripANSI(a.liveContent)
	thinkAt := strings.Index(joined, "think: thinking")
	assistantAt := strings.Index(joined, "Assistant: I need to run a command.")
	approvalAt := strings.Index(joined, "Approval required: bash")
	if thinkAt < 0 || assistantAt < 0 || approvalAt < 0 {
		t.Fatalf("transcript missing expected content: %q", joined)
	}
	if !(thinkAt < assistantAt && assistantAt < approvalAt) {
		t.Fatalf("transcript out of order: %q", joined)
	}
	if a.currentThinkIdx != -1 || a.currentAssistantIdx != -1 {
		t.Fatalf("active stream indices = think %d assistant %d, want both reset", a.currentThinkIdx, a.currentAssistantIdx)
	}
}

func TestHandleAgentEventCommitsStreamBeforeError(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnStart})
	a.handleAgentEvent(agent.Event{Type: agent.EventTextDelta, TextDelta: "partial response"})
	a.handleAgentEvent(agent.Event{Type: agent.EventError, Error: assertErr("provider failed")})

	a.printMu.Lock()
	defer a.printMu.Unlock()
	if len(a.printQueue) != 2 {
		t.Fatalf("print queue len = %d, want partial response and error: %#v", len(a.printQueue), a.printQueue)
	}
	if plain := stripANSI(a.printQueue[0]); !strings.Contains(plain, "Assistant: partial response") {
		t.Fatalf("first queued print = %q, want partial assistant response", plain)
	}
	if plain := stripANSI(a.printQueue[1]); !strings.Contains(plain, "Error: provider failed") {
		t.Fatalf("second queued print = %q, want provider error", plain)
	}
	if a.currentThinkIdx != -1 || a.currentAssistantIdx != -1 {
		t.Fatalf("active stream indices = think %d assistant %d, want both reset", a.currentThinkIdx, a.currentAssistantIdx)
	}
}

func TestFormatApprovalArgsBashShowsCommandWithoutJSON(t *testing.T) {
	got := stripANSI(formatApprovalArgs("bash", map[string]any{
		"command": "git diff\nmake test",
		"timeout": float64(30),
	}))

	if strings.Contains(got, "{") || strings.Contains(got, `"command"`) {
		t.Fatalf("formatApprovalArgs(bash) should not render raw JSON: %q", got)
	}
	if !strings.Contains(got, "command:\n  git diff\n  make test") {
		t.Fatalf("formatApprovalArgs(bash) missing formatted command: %q", got)
	}
	if !strings.Contains(got, "timeout: 30") {
		t.Fatalf("formatApprovalArgs(bash) missing timeout: %q", got)
	}
}

func TestRenderApprovalDialogShowsBashChoicesAndWrapsLongCommand(t *testing.T) {
	command := "go test ./internal/tui ./internal/config ./internal/agent ./internal/serve/openaiapi -run TestVeryLongApprovalCommandName -count=1"
	a := &App{
		waitingForApproval: true,
		currentApproval: pendingApproval{
			approvalID: "approval-1",
			toolName:   "bash",
			args: map[string]any{
				"command": command,
				"timeout": 120,
			},
		},
		width: 64,
	}

	plain := stripANSI(a.renderApprovalDialog())
	for _, want := range []string{
		"Approval Required: bash",
		"Approve Once",
		"Deny",
		"Always Allow Exact Command",
		"Always Allow Command Prefix",
		"go test ./internal/tui",
		"Timeout: 120",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("approval dialog missing %q: %q", want, plain)
		}
	}
	for _, line := range strings.Split(plain, "\n") {
		if lipgloss.Width(line) > 70 {
			t.Fatalf("approval dialog line too wide (%d): %q\nfull dialog:\n%s", lipgloss.Width(line), line, plain)
		}
	}
}

func TestApprovalPrefixSelectionPersistsProjectAllowRule(t *testing.T) {
	withTempTUIAllowPaths(t)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.waitingForApproval = true
	a.pendingApprovalID = "approval-1"
	a.currentApproval = pendingApproval{
		approvalID: "approval-1",
		toolName:   "bash",
		args:       map[string]any{"command": "go test ./internal/tui -run TestApproval"},
	}
	a.approvalCursor = 3

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))

	if a.waitingForApproval {
		t.Fatal("waitingForApproval = true, want false")
	}
	if a.allow == nil || !a.allow.MatchBashCommand("go test ./internal/config") {
		t.Fatal("saved prefix should match future go test commands")
	}
	data, err := os.ReadFile(config.ProjectAllowPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"bashPrefixes"`) || !strings.Contains(string(data), `"go test "`) {
		t.Fatalf("project allow.json missing saved prefix: %s", string(data))
	}
}

func TestFormatApprovalArgsWriteRedactsContent(t *testing.T) {
	got := formatApprovalArgs("write", map[string]any{
		"path":    "README.md",
		"content": "secret content",
	})

	if strings.Contains(got, "secret content") {
		t.Fatalf("formatApprovalArgs(write) leaked content: %q", got)
	}
	if !strings.Contains(got, "path: README.md") || !strings.Contains(got, "content: (14 bytes)") {
		t.Fatalf("formatApprovalArgs(write) missing path/content summary: %q", got)
	}
}

func TestFormatApprovalArgsEditShowsPathAndDiff(t *testing.T) {
	args := map[string]any{
		"path": "README.md",
		"edits": []any{
			map[string]any{
				"oldText": "Hello\nWorld\n",
				"newText": "Hello\nGophers\n",
			},
		},
	}

	got := formatApprovalArgs("edit", args)
	if !strings.Contains(got, "path: README.md") {
		t.Fatalf("formatApprovalArgs(edit) missing path: %q", got)
	}
	if !strings.Contains(got, "@@ -1,2 +1,2 @@") {
		t.Fatalf("formatApprovalArgs(edit) missing hunk header: %q", got)
	}
	if !strings.Contains(got, "-World") || !strings.Contains(got, "+Gophers") {
		t.Fatalf("formatApprovalArgs(edit) missing line diff: %q", got)
	}
}

func TestAbortClearsQueuedInput(t *testing.T) {
	a := &App{
		inputQueue: make([]InputEvent, 0, 4),
	}

	a.queueInput(teaKeyMsgForTest("a"))
	a.queueInput(teaKeyMsgForTest("b"))
	if got := len(a.inputQueue); got != 2 {
		t.Fatalf("len(inputQueue) before abort = %d, want 2", got)
	}

	a.inputQueueMu.Lock()
	a.inputQueue = a.inputQueue[:0]
	a.lastInputTime = time.Time{}
	a.inputQueueMu.Unlock()

	if got := len(a.inputQueue); got != 0 {
		t.Fatalf("len(inputQueue) after abort = %d, want 0", got)
	}
}

func TestQueueInputStartsInputTickOnDemand(t *testing.T) {
	a := &App{
		inputQueue: make([]InputEvent, 0, 4),
		inputDelay: time.Hour,
	}

	if cmd := a.queueInput(teaKeyMsgForTest("a")); cmd == nil {
		t.Fatal("first queued input did not start input queue tick")
	}
	if cmd := a.queueInput(teaKeyMsgForTest("b")); cmd != nil {
		t.Fatal("second queued input started a duplicate input queue tick")
	}
	if !a.inputQueueTickActive {
		t.Fatal("input queue tick is not marked active")
	}
	if got := len(a.inputQueue); got != 2 {
		t.Fatalf("len(inputQueue) = %d, want 2", got)
	}
}

func TestInputQueueTickContinuesUntilQueueIsIdle(t *testing.T) {
	a := &App{
		inputQueue: make([]InputEvent, 0, 4),
		inputDelay: time.Hour,
	}
	_ = a.queueInput(teaKeyMsgForTest("a"))

	_, cmd := a.Update(inputQueueTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("non-idle input queue tick did not schedule the next tick")
	}
	if !a.inputQueueTickActive {
		t.Fatal("input queue tick stopped while queue was not idle")
	}
	if got := len(a.inputQueue); got != 1 {
		t.Fatalf("len(inputQueue) = %d, want 1", got)
	}
}

func TestInputQueueTickStopsWhenQueueIsEmpty(t *testing.T) {
	a := &App{
		inputQueue:           make([]InputEvent, 0, 4),
		inputQueueTickActive: true,
		inputDelay:           time.Hour,
	}

	_, cmd := a.Update(inputQueueTickMsg(time.Now()))
	if cmd != nil {
		t.Fatal("empty input queue tick scheduled another tick")
	}
	if a.inputQueueTickActive {
		t.Fatal("empty input queue tick left tick marked active")
	}
}

func TestInputQueueTickStopsAfterIdleFlush(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.inputDelay = time.Millisecond
	_ = a.queueInput(teaKeyMsgForTest("a"))
	a.inputQueueMu.Lock()
	a.lastInputTime = time.Now().Add(-2 * a.inputDelay)
	a.inputQueueMu.Unlock()

	_, cmd := a.Update(inputQueueTickMsg(time.Now()))
	if cmd != nil {
		t.Fatal("idle input queue flush scheduled another tick")
	}
	if a.inputQueueTickActive {
		t.Fatal("idle input queue flush left tick marked active")
	}
	if got := a.input.Value(); got != "a" {
		t.Fatalf("input value = %q, want %q", got, "a")
	}
}

func TestHandleAgentEventStatusAndWarningMessage(t *testing.T) {
	a := &App{}

	a.handleAgentEvent(agent.Event{Type: agent.EventStatus, StatusMessage: "stream warning"})
	a.handleAgentEvent(agent.Event{
		Type:    agent.EventMessageStart,
		Message: provider.NewUserMessage("[System] explain what you are doing"),
	})
	a.handleAgentEvent(agent.Event{Type: agent.EventContextPressure, PressureMessage: "context high"})
	a.handleAgentEvent(agent.Event{Type: agent.EventBudgetPressure, PressureMessage: "budget low"})

	joined := stripANSI(strings.Join(a.messages, "\n"))
	if !strings.Contains(joined, "stream warning") {
		t.Fatalf("messages = %q, want status message", joined)
	}
	if !strings.Contains(joined, "[System] explain what you are doing") {
		t.Fatalf("messages = %q, want warning user message", joined)
	}
	if !strings.Contains(joined, "context high") || !strings.Contains(joined, "budget low") {
		t.Fatalf("messages = %q, want pressure warnings", joined)
	}
}

func TestHandleAgentEventRetryUsesStructuredMetadata(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.handleAgentEvent(agent.Event{
		Type:             agent.EventRetry,
		RetryAttempt:     2,
		RetryMaxAttempts: 4,
		RetryAfterMS:     1500,
		RetryReason:      "internal provider detail that must not be rendered",
	})

	joined := stripANSI(strings.Join(a.messages, "\n"))
	if !strings.Contains(joined, "Retrying (attempt 2/4); waiting 1.5s...") {
		t.Fatalf("messages = %q, want structured retry status", joined)
	}
	if strings.Contains(joined, "internal provider detail") {
		t.Fatalf("messages = %q, must not render RetryReason", joined)
	}
}

func TestHandleAgentEventRetryShowsErrorMessage(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	// The sanitized provider diagnostic rides on EventRetry.StatusMessage and
	// must appear as a supplementary detail under the friendly i18n summary.
	a.handleAgentEvent(agent.Event{
		Type:             agent.EventRetry,
		StatusMessage:    `"auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser to be set`,
		RetryAttempt:     1,
		RetryMaxAttempts: 3,
		RetryAfterMS:     2000,
		RetryReason:      "provider",
	})

	joined := stripANSI(strings.Join(a.messages, "\n"))
	// The friendly i18n summary stays the primary display.
	if !strings.Contains(joined, "Retrying (attempt 1/3); waiting 2s...") {
		t.Fatalf("messages = %q, want friendly retry summary", joined)
	}
	// The provider error detail is shown as supplementary info.
	if !strings.Contains(joined, `"auto" tool choice requires --enable-auto-tool-choice`) {
		t.Fatalf("messages = %q, want error details from provider", joined)
	}
}

func TestESMCommandPreservesObjectiveTextAndRegistersTools(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	a := NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	cmd := a.handleCommand("/esm build   the thing: 你好")
	if cmd == nil {
		t.Fatal("/esm create did not return an immediate continuation command")
	}

	store := esm.NewStore(sessionDir)
	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load ESM objective: %v", err)
	}
	if obj.Objective != "build   the thing: 你好" {
		t.Fatalf("objective = %q", obj.Objective)
	}
	if _, ok := a.registry.Get("get_esm"); !ok {
		t.Fatal("get_esm tool was not registered")
	}
	if _, ok := a.registry.Get("update_esm"); !ok {
		t.Fatal("update_esm tool was not registered")
	}
}

func TestESMResumeReturnsImmediateContinuationCommand(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	a := NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	if cmd := a.handleCommand("/esm keep going"); cmd == nil {
		t.Fatal("/esm create did not return an immediate continuation command")
	}
	if cmd := a.handleCommand("/esm pause"); cmd != nil {
		t.Fatal("/esm pause returned a continuation command")
	}
	if cmd := a.handleCommand("/esm resume"); cmd == nil {
		t.Fatal("/esm resume did not return an immediate continuation command")
	}
}

func TestESMUsageAccountingStaysActiveWithoutBudget(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	a := NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	store := esm.NewStore(sessionDir)
	if _, err := store.Create(context.Background(), sess.GetHeader().ID, "finish the real objective"); err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}

	a.prepareESMRun()
	a.recordESMUsage(&provider.Usage{TotalTokens: 11})

	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load ESM objective: %v", err)
	}
	// Usage is observability only: accounting never changes the status.
	if obj.Status != esm.StatusActive {
		t.Fatalf("status = %s, want active", obj.Status)
	}
	if obj.TokensUsed != 11 {
		t.Fatalf("tokens used = %d, want 11", obj.TokensUsed)
	}
}

func TestESMSupervisorContinuationDoesNotDoubleAccountTime(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	newTestApp := func() *App {
		return NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)
	}

	store := esm.NewStore(sessionDir)
	if _, err := store.Create(context.Background(), sess.GetHeader().ID, "finish the real objective"); err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}

	// Supervisor-driven continuations account each role's duration inside the
	// ESM core; finishESMRun must not add the wrapper wall time again.
	a := newTestApp()
	a.prepareESMRun()
	a.esmMu.Lock()
	a.esmSupervisorRun = true
	a.esmMu.Unlock()
	a.lastDuration = 5 * time.Second
	a.finishESMRun(nil)

	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load ESM objective: %v", err)
	}
	if obj.TimeUsedMS != 0 {
		t.Fatalf("TimeUsedMS = %d, want 0 for supervisor-driven runs", obj.TimeUsedMS)
	}
	if obj.Status != esm.StatusActive {
		t.Fatalf("status = %s, want active", obj.Status)
	}

	// Control: non-supervisor runs still account the wrapper wall time once.
	b := newTestApp()
	b.prepareESMRun()
	b.lastDuration = 5 * time.Second
	b.finishESMRun(nil)

	obj, err = store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("reload ESM objective: %v", err)
	}
	if obj.TimeUsedMS != 5000 {
		t.Fatalf("TimeUsedMS = %d, want 5000 for non-supervisor runs", obj.TimeUsedMS)
	}
}

func TestESMSubAgentWorkerCandidateRequiresAuditPass(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	settings.DefaultThinkingLevel = "off"
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	p := &esmSequenceProvider{name: "underlying-vendor", withInspection: true, responses: []string{
		`{"status":"complete_candidate","summary":"implemented","evidence":["unit tests pass"],"remaining_work":[],"blockers":[]}`,
		`{"verdict":"pass","review":"no demo-level gap found","requirements_checked":["objective -> candidate has evidence"],"missing_work":[],"evidence":["read files"]}`,
		`{"verdict":"pass","review":"verified all requirements","requirements_checked":["objective -> tests pass"],"missing_work":[],"evidence":["go test"]}`,
	}}
	model := p.Models()[0]
	factory := agent.NewAgentFactoryWithOptions(p, model, settings, nil, "", "", nil, ctxpkg.CompactionSettings{ReserveTokens: 16384, KeepRecentTokens: 20000}, nil, agent.AgentFactoryOptions{MultiAgentEnabled: true, ProviderName: "configured-provider"})
	mgr := agent.NewAgentManager(factory)
	a := NewApp(p, model, settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, mgr, nil, nil)

	cmd := a.handleCommand("/esm ship the real thing")
	if cmd == nil {
		t.Fatal("/esm create returned nil command")
	}
	msg := cmd()
	start, ok := msg.(agentStreamStartMsg)
	if !ok {
		t.Fatalf("command message = %#v, want agentStreamStartMsg", msg)
	}
	for range start.eventCh {
	}

	store := esm.NewStore(sessionDir)
	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load ESM objective: %v", err)
	}
	if obj.Status != esm.StatusComplete {
		t.Fatalf("status = %s, want complete after audit pass; obj=%#v", obj.Status, obj)
	}
	if p.getCalls() != 6 {
		t.Fatalf("provider calls = %d, want inspection + final for worker, critic, audit", p.getCalls())
	}
	assertOnlyRequestStatProvider(t, sessionDir, "configured-provider")
}

func assertOnlyRequestStatProvider(t *testing.T, sessionDir string, want string) {
	t.Helper()
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root session db: %v", err)
	}
	rows, err := db.Bun().Query("SELECT DISTINCT provider FROM request_stats")
	if err != nil {
		t.Fatalf("query request_stats providers: %v", err)
	}
	defer rows.Close()

	var providers []string
	for rows.Next() {
		var providerName string
		if err := rows.Scan(&providerName); err != nil {
			t.Fatalf("scan request_stats provider: %v", err)
		}
		providers = append(providers, providerName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate request_stats providers: %v", err)
	}
	if len(providers) != 1 || providers[0] != want {
		t.Fatalf("request_stats providers = %#v, want only %q", providers, want)
	}
}

func TestESMSubAgentPassWithoutInspectionIsRejected(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	settings.DefaultThinkingLevel = "off"
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	p := &esmSequenceProvider{responses: []string{
		`{"status":"complete_candidate","summary":"implemented","evidence":["unit tests pass"],"remaining_work":[],"blockers":[]}`,
	}}
	model := p.Models()[0]
	factory := agent.NewAgentFactoryWithOptions(p, model, settings, nil, "", "", nil, ctxpkg.CompactionSettings{ReserveTokens: 16384, KeepRecentTokens: 20000}, nil, agent.AgentFactoryOptions{MultiAgentEnabled: true})
	mgr := agent.NewAgentManager(factory)
	a := NewApp(p, model, settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, mgr, nil, nil)

	cmd := a.handleCommand("/esm ship the real thing")
	if cmd == nil {
		t.Fatal("/esm create returned nil command")
	}
	msg := cmd()
	start, ok := msg.(agentStreamStartMsg)
	if !ok {
		t.Fatalf("command message = %#v, want agentStreamStartMsg", msg)
	}
	for range start.eventCh {
	}

	store := esm.NewStore(sessionDir)
	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("load ESM objective: %v", err)
	}
	if obj.Status != esm.StatusActive {
		t.Fatalf("status = %s, want active after uninspected completion candidate; obj=%#v", obj.Status, obj)
	}
	if !strings.Contains(obj.CompletionReview, "without any tool-backed inspection") {
		t.Fatalf("completion review = %q, want tool-backed rejection reason", obj.CompletionReview)
	}
}

func TestESMRoleModeSimplifiesApproval(t *testing.T) {
	tests := map[string]string{
		"yolo":  "yolo",
		"agent": "yolo",
		"plan":  "yolo",
		"os":    "os",
		"":      "yolo",
	}
	for mode, want := range tests {
		a := &App{mode: mode}
		if got := a.esmRoleMode(); got != want {
			t.Fatalf("esmRoleMode(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestLoadHistoryMessagesSkipsSystemInjectedUserMessages(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	if _, err := sess.AppendMessage(provider.NewSystemInjectedUserMessage("[ESM continuation] hidden")); err != nil {
		t.Fatalf("Append system injected: %v", err)
	}
	if _, err := sess.AppendMessage(provider.NewUserMessage("visible user")); err != nil {
		t.Fatalf("Append user: %v", err)
	}

	a := &App{session: sess}
	a.LoadHistoryMessages()

	joined := stripANSI(strings.Join(a.messages, "\n"))
	if strings.Contains(joined, "[ESM continuation] hidden") {
		t.Fatalf("system injected message was displayed: %q", joined)
	}
	if !strings.Contains(joined, "visible user") {
		t.Fatalf("visible user message missing: %q", joined)
	}
}

func TestAgentErrorIncludesAbortReason(t *testing.T) {
	app := &App{pendingAbortReason: "user pressed Esc"}
	app.handleAgentEvent(agent.Event{Type: agent.EventError, Error: assertErr("aborted"), StopReason: "aborted"})

	joined := stripANSI(strings.Join(app.messages, "\n"))
	if !strings.Contains(joined, "Error: aborted (reason: user pressed Esc)") {
		t.Fatalf("messages = %q, want provider error and abort reason", joined)
	}
	if app.pendingAbortReason != "" {
		t.Fatalf("pendingAbortReason = %q, want cleared", app.pendingAbortReason)
	}
}

func TestListenEventsPassesThroughDoneAndError(t *testing.T) {
	eventCh := make(chan agent.Event, 2)
	eventCh <- agent.Event{Type: agent.EventDone}
	eventCh <- agent.Event{Type: agent.EventError, Error: assertErr("boom")}
	close(eventCh)
	app := &App{eventCh: eventCh}

	msg := app.listenAgentEvents()()
	if ev, ok := msg.(agentEventMsg); !ok || ev.event.Type != agent.EventDone {
		t.Fatalf("first msg = %#v, want agentEventMsg(EventDone)", msg)
	}

	msg = app.listenAgentEvents()()
	if ev, ok := msg.(agentEventMsg); !ok || ev.event.Type != agent.EventError || ev.event.Error == nil || ev.event.Error.Error() != "boom" {
		t.Fatalf("second msg = %#v, want agentEventMsg(EventError boom)", msg)
	}

	msg = app.listenAgentEvents()()
	if _, ok := msg.(agentDoneMsg); !ok {
		t.Fatalf("third msg = %#v, want agentDoneMsg", msg)
	}
}

func TestStaleAgentEventsDoNotMutateNextRun(t *testing.T) {
	oldEvents := make(chan agent.Event)
	newEvents := make(chan agent.Event)
	oldRun := newTUIRun()
	newRun := newTUIRun()
	app := &App{run: newRun, eventCh: newEvents, isThinking: true}

	_, _ = app.Update(agentStreamStartMsg{run: oldRun, eventCh: oldEvents})
	if app.eventCh != newEvents {
		t.Fatal("stale stream start replaced the active event channel")
	}

	_, _ = app.Update(agentEventMsg{
		event:   agent.Event{Type: agent.EventRunFinished, Status: agent.TaskCanceled},
		eventCh: oldEvents,
	})
	if !app.isThinking {
		t.Fatal("stale terminal event changed the active run state")
	}

	_, _ = app.Update(agentDoneMsg{eventCh: oldEvents})
	if !app.isThinking {
		t.Fatal("stale channel completion changed the active run state")
	}
}

func TestCompactCommandStartsImmediateCompaction(t *testing.T) {
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "m1", Name: "Model 1", ContextWindow: 100000},
	}, []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "## Goal\nCompacted summary"},
		{Type: provider.StreamDone},
	})
	model := mockProvider.Models()[0]
	settings := config.DefaultSettings()
	registry := tools.NewRegistry(t.TempDir(), nil)
	app := NewApp(mockProvider, model, settings, nil, registry, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.agent = agent.New(agent.Config{
		Provider: mockProvider,
		Model:    model,
		Mode:     "agent",
		Settings: settings,
		CompactionSettings: ctxpkg.CompactionSettings{
			Enabled:          true,
			ReserveTokens:    100,
			KeepRecentTokens: 1,
		},
	}, registry)
	app.agent.LoadHistoryMessages([]provider.Message{
		provider.NewUserMessage("old user message"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant message"}}),
		provider.NewUserMessage("recent user message"),
		provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent assistant message"}}),
	})

	cmd := app.handleCommand("/compact")
	if cmd == nil {
		t.Fatal("/compact returned nil command, want immediate compaction command")
	}
	startMsg := cmd()
	streamMsg, ok := startMsg.(agentStreamStartMsg)
	if !ok {
		t.Fatalf("/compact command message = %#v, want agentStreamStartMsg", startMsg)
	}
	if !streamMsg.compacting {
		t.Fatalf("/compact command message compacting = false, want true")
	}
	app.eventCh = streamMsg.eventCh

	msg := app.listenAgentEvents()()
	if ev, ok := msg.(agentEventMsg); !ok || ev.event.Type != agent.EventCompactionStart {
		t.Fatalf("first compaction event = %#v, want EventCompactionStart", msg)
	}
	msg = app.listenAgentEvents()()
	if ev, ok := msg.(agentEventMsg); !ok || ev.event.Type != agent.EventCompactionEnd || ev.event.Error != nil {
		t.Fatalf("second compaction event = %#v, want successful EventCompactionEnd", msg)
	}
	if got := mockProvider.GetCallCount(); got != 1 {
		t.Fatalf("provider call count = %d, want 1", got)
	}
	msgs := app.agent.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("compacted messages len = %d, want 3", len(msgs))
	}
	if !msgs[0].SystemInjected || !strings.Contains(msgs[0].Content, "Compacted summary") {
		t.Fatalf("first compacted message = %#v, want system-injected summary", msgs[0])
	}
}

func TestHelpCommandRendersAsSingleCommandOutput(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.handleCommand("/help")

	if len(a.messages) != 1 {
		t.Fatalf("/help messages len = %d, want 1", len(a.messages))
	}
	plain := stripANSI(a.messages[0])
	for _, want := range []string{"Commands:", "/mode [plan|agent|yolo|os]", "/help", "Keyboard shortcuts:"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("/help output = %q, want substring %q", plain, want)
		}
	}
}

func TestHelpCommandRendersCommandOutputWithoutProgram(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.width = 80
	a.height = 10
	for i := 0; i < 8; i++ {
		a.addMessage(fmt.Sprintf("line %d", i))
	}

	a.handleCommand("/help")

	if len(a.messages) == 0 {
		t.Fatal("messages empty after /help")
	}
	idx := len(a.messages) - 1
	if a.printedMessageIdx[idx] {
		t.Fatal("/help output was marked printed without a Bubble Tea program")
	}
	if plain := stripANSI(a.messages[idx]); !strings.Contains(plain, "Commands:") {
		t.Fatalf("/help output = %q, want command list", plain)
	}
	if plain := stripANSI(a.liveContent); !strings.Contains(plain, "Commands:") {
		t.Fatalf("live transcript = %q, want command list", plain)
	}
}

func TestRuleCommandCreatesRuleFileAndLoadsContent(t *testing.T) {
	tmpDir := t.TempDir()
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.cwd = tmpDir

	a.handleCommand("/rule")

	rulePath := contextfiles.RuleFilePath(tmpDir)
	data, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read rule file: %v", err)
	}
	if string(data) != contextfiles.DefaultRuleContent {
		t.Fatal("rule file content mismatch")
	}
	if a.ruleContent != contextfiles.DefaultRuleContent {
		t.Fatal("app ruleContent was not updated")
	}
}

func TestRuleCommandPreservesExistingUnlessForced(t *testing.T) {
	tmpDir := t.TempDir()
	rulePath := contextfiles.RuleFilePath(tmpDir)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0755); err != nil {
		t.Fatalf("mkdir rule dir: %v", err)
	}
	if err := os.WriteFile(rulePath, []byte("custom rule"), 0644); err != nil {
		t.Fatalf("write rule file: %v", err)
	}
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.cwd = tmpDir

	a.handleCommand("/rule")
	if a.ruleContent != "custom rule" {
		t.Fatalf("ruleContent = %q", a.ruleContent)
	}

	a.handleCommand("/rule force")
	data, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read rule file: %v", err)
	}
	if string(data) != contextfiles.DefaultRuleContent {
		t.Fatal("force did not overwrite with default rule")
	}
	if a.ruleContent != contextfiles.DefaultRuleContent {
		t.Fatal("app ruleContent was not updated after force")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func teaKeyMsgForTest(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func teaSpecialKeyMsgForTest(key tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: key}
}

func withSplitPasteCoalescing(t *testing.T, enabled bool) {
	t.Helper()
	old := splitPasteCoalescingEnabled
	splitPasteCoalescingEnabled = func() bool { return enabled }
	t.Cleanup(func() { splitPasteCoalescingEnabled = old })
}

func withTempTUIAllowPaths(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTHX_DIR", filepath.Join(tmp, "global"))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
}

func TestInputHomeEndKeysReachTextInput(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input.SetValue("abc")

	a.Update(teaSpecialKeyMsgForTest(tea.KeyHome))
	a.flushInputQueue()
	a.Update(teaKeyMsgForTest("X"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "Xabc" {
		t.Fatalf("value after home insert = %q, want Xabc", got)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnd))
	a.flushInputQueue()
	a.Update(teaKeyMsgForTest("Z"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "XabcZ" {
		t.Fatalf("value after end insert = %q, want XabcZ", got)
	}
}

func TestInputHistoryNavigationPreservesDraft(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.recordInputHistory("first")
	a.recordInputHistory("second")
	a.input.SetValue("draft")

	if !a.navigateInputHistory(-1) || a.input.Value() != "second" {
		t.Fatalf("first up value = %q, want second", a.input.Value())
	}
	if !a.navigateInputHistory(-1) || a.input.Value() != "first" {
		t.Fatalf("second up value = %q, want first", a.input.Value())
	}
	if !a.navigateInputHistory(-1) || a.input.Value() != "first" {
		t.Fatalf("third up value = %q, want first", a.input.Value())
	}
	if !a.navigateInputHistory(1) || a.input.Value() != "second" {
		t.Fatalf("first down value = %q, want second", a.input.Value())
	}
	if !a.navigateInputHistory(1) || a.input.Value() != "draft" {
		t.Fatalf("second down value = %q, want draft", a.input.Value())
	}
	if a.navigateInputHistory(1) {
		t.Fatal("down outside history returned true, want false")
	}
}

func TestInputHistoryNavigationFlushesQueuedDraft(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.recordInputHistory("previous")

	a.Update(teaKeyMsgForTest("draft"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyUp))

	if got := a.input.Value(); got != "previous" {
		t.Fatalf("up value = %q, want previous", got)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyDown))
	if got := a.input.Value(); got != "draft" {
		t.Fatalf("down value = %q, want queued draft restored", got)
	}
}

func TestInputAltEnterAndCtrlJInsertNewlines(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("one"))
	a.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	a.Update(teaKeyMsgForTest("two"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyCtrlJ))
	a.Update(teaKeyMsgForTest("three"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "one\ntwo\nthree" {
		t.Fatalf("input = %q, want multiline value", got)
	}
}

func TestInputSmallMultilinePastePreservesNewlines(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo"), Paste: true})

	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("pasted input = %q, want newlines preserved", got)
	}
}

func TestInputSplitMultilinePastePreservesNewlines(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("one"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("two"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("split pasted input = %q, want newlines preserved", got)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputSplitPasteCoalescesAfterFirstLineFlush(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("one"))
	a.flushInputQueue()
	if got := a.input.Value(); got != "one" {
		t.Fatalf("input after first line flush = %q, want one", got)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	if got := a.input.Value(); got != "one" {
		t.Fatalf("input after deferred enter = %q, want one", got)
	}

	a.Update(teaKeyMsgForTest("two"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("split pasted input = %q, want newlines preserved", got)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputDeferredEnterSubmitsWhenPasteDoesNotContinue(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("/clear"))
	a.flushInputQueue()
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.flushInputQueue()

	if got := a.input.Value(); got != "" {
		t.Fatalf("input after deferred submit = %q, want empty", got)
	}
	if len(a.messages) == 0 || !strings.Contains(stripANSI(a.messages[len(a.messages)-1]), a.translator.Text(i18n.MsgConversationCleared)) {
		t.Fatalf("expected /clear to execute, messages = %#v", a.messages)
	}
}

func TestInputSplitPasteCtrlJPreservesNewline(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("one"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyCtrlJ))
	a.Update(teaKeyMsgForTest("two"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("split pasted input = %q, want Ctrl+J preserved as newline", got)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputSplitLargePasteCreatesMarker(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("one"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("two"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("three"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("four"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("five"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("six"))
	a.flushInputQueue()

	const wantPaste = "one\ntwo\nthree\nfour\nfive\nsix"
	if got := a.input.Value(); got != "[paste #1 +6 lines]" {
		t.Fatalf("split large pasted input = %q, want marker", got)
	}
	if got := a.pastes[1]; got != wantPaste {
		t.Fatalf("stored paste = %q, want %q", got, wantPaste)
	}
	if got := a.expandPasteMarkers(a.input.Value()); got != wantPaste {
		t.Fatalf("expanded paste = %q, want %q", got, wantPaste)
	}
}

func TestInputSplitPasteWaitsPastNormalIdleWindow(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.inputDelay = time.Millisecond

	a.Update(teaKeyMsgForTest("one"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.inputQueueMu.Lock()
	a.lastInputTime = time.Now().Add(-2 * a.inputDelay)
	a.inputQueueMu.Unlock()

	if a.inputQueueIdle() {
		t.Fatal("split paste candidate became idle after normal input delay")
	}
}

func TestInputDelayedTwentyTwoLineSplitPasteCreatesMarker(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.inputDelay = time.Millisecond

	lines := make([]string, 22)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%02d", i+1)
		a.Update(teaKeyMsgForTest(lines[i]))
		if i < len(lines)-1 {
			a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
		}
		a.inputQueueMu.Lock()
		a.lastInputTime = time.Now().Add(-2 * a.inputDelay)
		a.inputQueueMu.Unlock()
		if a.inputQueueIdle() {
			t.Fatalf("split paste queue became idle before line %d was coalesced", i+1)
		}
	}

	a.inputQueueMu.Lock()
	a.lastInputTime = time.Now().Add(-splitPasteIdleDelay - time.Millisecond)
	a.inputQueueMu.Unlock()
	if !a.inputQueueIdle() {
		t.Fatal("split paste queue did not become idle after paste delay")
	}

	a.flushInputQueue()

	wantPaste := strings.Join(lines, "\n")
	if got := a.input.Value(); got != "[paste #1 +22 lines]" {
		t.Fatalf("split large pasted input = %q, want marker", got)
	}
	if got := a.pastes[1]; got != wantPaste {
		t.Fatalf("stored paste = %q, want %q", got, wantPaste)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputLoneDeferredEnterKeepsNormalIdleWindow(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.inputDelay = time.Millisecond

	a.Update(teaKeyMsgForTest("one"))
	a.flushInputQueue()
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.inputQueueMu.Lock()
	a.lastInputTime = time.Now().Add(-2 * a.inputDelay)
	a.inputQueueMu.Unlock()

	// A lone deferred Enter carries no split-paste evidence: any preceding
	// chunks were already flushed into the input. It must not pay the extended
	// paste window, otherwise every quick type-then-Enter send feels sticky.
	if !a.inputQueueIdle() {
		t.Fatal("lone deferred Enter waited for the extended paste window")
	}
}

func TestInputDeferredEnterWithFollowingTextKeepsPasteWindow(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.inputDelay = time.Millisecond

	a.Update(teaKeyMsgForTest("one"))
	a.flushInputQueue()
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.Update(teaKeyMsgForTest("two"))
	a.inputQueueMu.Lock()
	a.lastInputTime = time.Now().Add(-2 * a.inputDelay)
	a.inputQueueMu.Unlock()

	// Text arriving after a deferred Enter is split-paste continuation and
	// must keep waiting for the extended window so it coalesces into a paste.
	if a.inputQueueIdle() {
		t.Fatal("split paste continuation became idle after normal input delay")
	}

	a.flushInputQueue()
	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("split pasted input = %q, want newlines preserved", got)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputLoneDeferredEnterSubmitsWithinNormalIdleWindow(t *testing.T) {
	withSplitPasteCoalescing(t, true)
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	a := NewApp(&historyInjectMockProvider{}, &provider.Model{ID: "mock-model", Name: "Mock"}, settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	// Press Enter immediately after typing and drive the real Bubble Tea
	// command loop. Before the lone-Enter fix this needed ~8 queue ticks
	// (~130ms) because the deferred Enter kept the extended paste window.
	queue := []tea.Cmd{}
	appendCmd := func(cmd tea.Cmd) {
		if cmd != nil {
			queue = append(queue, cmd)
		}
	}
	_, cmd := a.Update(teaKeyMsgForTest("hello"))
	a.flushInputQueue()
	appendCmd(cmd)
	_, cmd = a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	appendCmd(cmd)
	ticks := 0
	submitted := false
	for len(queue) > 0 && !submitted && ticks <= 20 {
		cmd := queue[0]
		queue = queue[1:]
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case inputQueueTickMsg:
			ticks++
			if _, next := a.Update(msg); next != nil {
				queue = append(queue, next)
			}
		case agentStreamStartMsg:
			submitted = true
			if msg.err != nil {
				t.Fatalf("run start failed: %v", msg.err)
			}
		default:
			if _, next := a.Update(msg); next != nil {
				queue = append(queue, next)
			}
		}
	}

	if !submitted {
		t.Fatal("plain-text message was never submitted")
	}
	if ticks > 2 {
		t.Fatalf("lone deferred Enter needed %d queue ticks, want <= 2", ticks)
	}
}

func TestInputSplitSchedulerLogPasteCreatesMarkerByDefault(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	const logPaste = `Container geo-stats-scheduler  Recreate
 Container geo-stats-scheduler  Recreated
 Container geo-stats-scheduler  Starting
 Container geo-stats-scheduler  Started
=========================================
  geo_stats Scheduler Starting
  Time: 2026-07-07 16:32:41
=========================================
DB: mysql+pymysql://***:***@127.0.0.1:3306/oscdb?charset=utf8mb4
Interval: 1800s
Limit: 0
Workers: 1
Browser: enabled

2026-07-07 16:32:41,723 [INFO] 调度器启动 interval=1800s limit=0 desc=True browser=True workers=1
2026-07-07 16:32:41,723 [INFO] ===== 调度轮次开始 [2026-07-07 16:32:41] limit=0 desc=True browser=True workers=1 =====
读取分发记录（带周期过滤）失败: (pymysql.err.OperationalError) (1045, "Access denied for user 'oschina'@'172.17.0.1' (using password: YES)")
(Background on this error at: https://sqlalche.me/e/20/e3q8)
2026-07-07 16:32:41,742 [INFO] 本轮到期回调记录: 0 条
2026-07-07 16:32:41,742 [INFO] 无到期任务，跳过本轮
2026-07-07 16:32:41,742 [INFO] 休眠 1800 秒后进入下一轮...`

	lines := strings.Split(logPaste, "\n")
	for i, line := range lines {
		a.Update(teaKeyMsgForTest(line))
		if i < len(lines)-1 {
			a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
		}
	}
	a.flushInputQueue()

	if got := a.input.Value(); got != "[paste #1 +21 lines]" {
		t.Fatalf("split log paste input = %q, want marker", got)
	}
	if got := a.pastes[1]; got != logPaste {
		t.Fatalf("stored log paste = %q, want %q", got, logPaste)
	}
	if len(a.messages) != 0 {
		t.Fatalf("split log paste submitted messages = %#v, want none", a.messages)
	}
}

func TestInputQueuedTextEnterSubmits(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("/clear"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.flushInputQueue()

	if got := a.input.Value(); got != "" {
		t.Fatalf("input after submit = %q, want empty", got)
	}
	if len(a.messages) == 0 || !strings.Contains(stripANSI(a.messages[len(a.messages)-1]), a.translator.Text(i18n.MsgConversationCleared)) {
		t.Fatalf("expected /clear to execute, messages = %#v", a.messages)
	}
}

func TestInputSplitPasteCoalescingDisabledEnterSubmits(t *testing.T) {
	withSplitPasteCoalescing(t, false)
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)

	a.Update(teaKeyMsgForTest("/clear"))
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))
	a.flushInputQueue()

	if got := a.input.Value(); got != "" {
		t.Fatalf("input after submit = %q, want empty", got)
	}
	if len(a.messages) == 0 || !strings.Contains(stripANSI(a.messages[len(a.messages)-1]), a.translator.Text(i18n.MsgConversationCleared)) {
		t.Fatalf("expected /clear to execute immediately, messages = %#v", a.messages)
	}
}

func TestInputUpDownMovesWithinMultilineBeforeHistory(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.recordInputHistory("previous")
	a.input.SetValue("one\ntwo")

	a.Update(teaSpecialKeyMsgForTest(tea.KeyUp))
	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("up inside multiline changed value = %q", got)
	}
	if line, _ := a.input.CursorPos(); line != 0 {
		t.Fatalf("cursor line after first up = %d, want 0", line)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyUp))
	if got := a.input.Value(); got != "previous" {
		t.Fatalf("up at first line value = %q, want history entry", got)
	}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyDown))
	if got := a.input.Value(); got != "one\ntwo" {
		t.Fatalf("down while browsing value = %q, want draft restored", got)
	}
}

func TestEscAbortClearsApprovalState(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.isThinking = true
	a.waitingForApproval = true
	a.pendingApprovalID = "approval-1"
	a.approvalQueue = []pendingApproval{{approvalID: "approval-2", toolName: "bash"}}

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEsc))

	if a.waitingForApproval {
		t.Fatal("waitingForApproval = true, want false")
	}
	if a.pendingApprovalID != "" {
		t.Fatalf("pendingApprovalID = %q, want empty", a.pendingApprovalID)
	}
	if len(a.approvalQueue) != 0 {
		t.Fatalf("len(approvalQueue) = %d, want 0", len(a.approvalQueue))
	}
}

func TestClearCommandResetsTranscriptState(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "base", "", nil, "agent", false, false, nil, nil, nil)
	a.messages = []string{"old"}
	a.toolResults = []toolResult{{toolCallID: "tool-1", msgIndex: 0}}
	a.liveContent = "live"
	a.currentPlan = &tools.TaskPlan{Title: "old plan", Steps: []tools.PlanStep{{Title: "step", Status: "running"}}}
	a.assistantRaw[0] = "raw"
	a.assistantRendered[0] = "rendered"
	a.assistantDirty[0] = true
	a.printedMessageIdx[0] = true
	a.currentAssistantIdx = 0
	a.currentThinkIdx = 1
	a.toolModalOpen = true
	a.activeSkills["x"] = "skill"
	a.extraContext = "base skill"

	a.handleCommand("/clear")

	if len(a.toolResults) != 0 || len(a.assistantRaw) != 0 || len(a.assistantRendered) != 0 || len(a.assistantDirty) != 0 || len(a.printedMessageIdx) != 0 {
		t.Fatalf("transcript state not reset: tools=%d raw=%d rendered=%d dirty=%d printed=%d", len(a.toolResults), len(a.assistantRaw), len(a.assistantRendered), len(a.assistantDirty), len(a.printedMessageIdx))
	}
	if a.currentAssistantIdx != -1 || a.currentThinkIdx != -1 || a.toolModalOpen || a.currentPlan != nil {
		t.Fatalf("active state not reset: assistant=%d think=%d modal=%v plan=%v", a.currentAssistantIdx, a.currentThinkIdx, a.toolModalOpen, a.currentPlan)
	}
	if a.extraContext != "base" || len(a.activeSkills) != 0 {
		t.Fatalf("skill context not reset: extra=%q active=%d", a.extraContext, len(a.activeSkills))
	}
	joined := stripANSI(strings.Join(a.messages, "\n"))
	if !strings.Contains(joined, a.translator.Text(i18n.MsgConversationCleared)) || strings.Contains(joined, "old") {
		t.Fatalf("messages after clear = %q, want only clear confirmation", joined)
	}
}

func TestOpenLatestToolModalRequiresContent(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	if a.openLatestToolModal() {
		t.Fatal("openLatestToolModal on empty app = true, want false")
	}
	a.messages = []string{"hello"}
	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal with content = false, want true")
	}
}

func TestBackgroundAgentEventRecordsActivityOnly(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)
	a.messages = []string{"main"}

	a.handleAgentEvent(agent.Event{
		Type:      agent.EventTextDelta,
		AgentID:   "sub-1",
		TextDelta: "subagent progress",
	})

	if len(a.messages) != 1 || a.messages[0] != "main" {
		t.Fatalf("background event changed main messages: %#v", a.messages)
	}
	act := a.agentActivities["sub-1"]
	if act == nil {
		t.Fatal("missing subagent activity")
	}
	if !strings.Contains(act.LastText, "subagent progress") {
		t.Fatalf("activity LastText = %q", act.LastText)
	}

	a.handleAgentEvent(agent.Event{
		Type:             agent.EventRetry,
		AgentID:          "sub-1",
		RetryAttempt:     1,
		RetryMaxAttempts: 3,
		RetryAfterMS:     1000,
		RetryReason:      "provider diagnostic that must not be rendered",
	})
	if !strings.Contains(act.LastResult, "Retrying (attempt 1/3); waiting 1s...") {
		t.Fatalf("activity LastResult = %q, want structured retry status", act.LastResult)
	}
	if strings.Contains(act.LastResult, "provider diagnostic") {
		t.Fatalf("activity LastResult = %q, must not render RetryReason", act.LastResult)
	}
}

func TestSubAgentApprovalStillShowsPrompt(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)

	a.handleAgentEvent(agent.Event{
		Type:         agent.EventToolApprovalRequest,
		AgentID:      "sub-1",
		ApprovalID:   "approval-1",
		ApprovalTool: "bash",
		ApprovalArgs: map[string]any{"cmd": "make test"},
	})

	if !a.waitingForApproval {
		t.Fatal("waitingForApproval = false, want true")
	}
	if a.currentApproval.agentID != "sub-1" {
		t.Fatalf("approval agentID = %q, want sub-1", a.currentApproval.agentID)
	}
}

func TestQueuedApprovalShowsNextDetailsInLiveView(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)

	a.handleAgentEvent(agent.Event{
		Type:         agent.EventToolApprovalRequest,
		ApprovalID:   "approval-1",
		ApprovalTool: "bash",
		ApprovalArgs: map[string]any{"command": "echo first"},
	})
	a.handleAgentEvent(agent.Event{
		Type:         agent.EventToolApprovalRequest,
		ApprovalID:   "approval-2",
		ApprovalTool: "bash",
		ApprovalArgs: map[string]any{"command": "echo second"},
	})

	firstLive := stripANSI(a.renderLiveTranscriptContent())
	if !strings.Contains(firstLive, "echo first") {
		t.Fatalf("first approval details missing from live view: %q", firstLive)
	}
	if strings.Contains(firstLive, "echo second") {
		t.Fatalf("queued approval shown before current approval is answered: %q", firstLive)
	}

	a.input.SetValue("y")
	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))

	nextLive := stripANSI(a.renderLiveTranscriptContent())
	if !strings.Contains(nextLive, "Approval required: bash") || !strings.Contains(nextLive, "echo second") {
		t.Fatalf("next approval details missing from live view: %q", nextLive)
	}
	if strings.Contains(nextLive, "echo first") {
		t.Fatalf("answered approval still shown in live view: %q", nextLive)
	}
	if got, want := a.pendingApprovalID, "approval-2"; got != want {
		t.Fatalf("pendingApprovalID = %q, want %q", got, want)
	}
}

func TestDuplicateApprovalIDIsNotQueuedTwice(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	event := agent.Event{
		Type:         agent.EventToolApprovalRequest,
		ApprovalID:   "approval-1",
		ApprovalTool: "bash",
		ApprovalArgs: map[string]any{"command": "go test ./internal/tui"},
	}

	a.handleAgentEvent(event)
	a.handleAgentEvent(event)

	if !a.waitingForApproval {
		t.Fatal("waitingForApproval = false, want true")
	}
	if got := len(a.approvalQueue); got != 0 {
		t.Fatalf("duplicate approval queued %d extra item(s), want 0", got)
	}
	if got := strings.Count(stripANSI(a.renderLiveTranscriptContent()), "Approval required: bash"); got != 1 {
		t.Fatalf("approval prompt count = %d, want 1", got)
	}
}

func TestAlwaysAllowApprovesQueuedMatchingBashApprovals(t *testing.T) {
	withTempTUIAllowPaths(t)
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.waitingForApproval = true
	a.pendingApprovalID = "approval-1"
	a.currentApproval = pendingApproval{
		approvalID: "approval-1",
		toolName:   "bash",
		args:       map[string]any{"command": "go test ./internal/tui"},
	}
	a.approvalQueue = []pendingApproval{
		{approvalID: "approval-2", toolName: "bash", args: map[string]any{"command": "go test ./internal/tui"}},
		{approvalID: "approval-3", toolName: "bash", args: map[string]any{"command": "go env"}},
	}
	a.approvalCursor = 2

	a.Update(teaSpecialKeyMsgForTest(tea.KeyEnter))

	if got, want := a.pendingApprovalID, "approval-3"; got != want {
		t.Fatalf("pendingApprovalID = %q, want next non-matching approval %q", got, want)
	}
	if got := len(a.approvalQueue); got != 0 {
		t.Fatalf("approvalQueue len = %d, want 0 after matching item is auto-approved", got)
	}
	if !a.allow.MatchBashCommand("go test ./internal/tui") {
		t.Fatal("exact command allow rule was not saved")
	}
	joined := stripANSI(strings.Join(a.messages, "\n"))
	if !strings.Contains(joined, "Approved 1 queued matching command") {
		t.Fatalf("auto-approval status missing: %q", joined)
	}
}

func TestToolModalDefaultsMainAndSwitchesToSubAgent(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)
	a.messages = []string{"main transcript"}
	a.recordAgentActivity(agent.Event{
		Type:          agent.EventToolCall,
		AgentID:       "sub-1",
		ToolName:      "grep",
		ToolArgs:      map[string]any{"pattern": "workflow"},
		StatusMessage: "ignored",
	})

	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal = false, want true")
	}
	if a.toolModalActive != 0 {
		t.Fatalf("toolModalActive = %d, want main index 0", a.toolModalActive)
	}
	main := stripANSI(a.renderExpandedTranscript())
	if !strings.Contains(main, "main transcript") || strings.Contains(main, "grep") {
		t.Fatalf("main modal content = %q", main)
	}

	a.switchToolModalTarget(1)
	sub := stripANSI(a.renderExpandedTranscript())
	if !strings.Contains(sub, "sub-1") || !strings.Contains(sub, "grep") {
		t.Fatalf("subagent modal content = %q", sub)
	}
}

func TestToolModalSubAgentShowsFullActivityDetails(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)
	a.messages = []string{"main transcript"}
	command := "printf '" + strings.Repeat("command-detail-", 30) + "end'"
	thinking := "thinking-start " + strings.Repeat("reasoning-detail ", 30) + "thinking-end"
	response := "response-start " + strings.Repeat("response-detail ", 30) + "response-end"
	result := "result-start\n" + strings.Repeat("result-detail\n", 80) + "result-end"

	a.recordAgentActivity(agent.Event{
		Type:     agent.EventToolExecutionStart,
		AgentID:  "sub-1",
		ToolName: "bash",
		ToolArgs: map[string]any{"command": command},
	})
	a.recordAgentActivity(agent.Event{Type: agent.EventThinkDelta, AgentID: "sub-1", ThinkDelta: thinking})
	a.recordAgentActivity(agent.Event{Type: agent.EventTextDelta, AgentID: "sub-1", TextDelta: response})
	a.recordAgentActivity(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		AgentID:    "sub-1",
		ToolName:   "bash",
		ToolResult: result,
	})

	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal = false, want true")
	}
	a.switchToolModalTarget(1)
	content := stripANSI(a.renderExpandedTranscript())
	for _, want := range []string{"printf", "command-detail-", "end'", "thinking-start", "thinking-end", "response-start", "response-end", "result-start", "result-end", a.translator.Text(i18n.MsgActivityTimeline)} {
		if !strings.Contains(content, want) {
			t.Fatalf("sub-agent details missing full content %q:\n%s", want, content)
		}
	}
	if got, want := strings.Count(content, "command-detail-"), 60; got != want {
		t.Fatalf("command detail occurrences = %d, want %d; content was truncated:\n%s", got, want, content)
	}
}

func TestRecordAgentActivityHostedItem(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)
	a.recordAgentActivity(agent.Event{Type: agent.EventHostedItem, AgentID: "sub-hosted", HostedItem: &provider.HostedItem{
		Type: "web_search_call", Status: "completed",
	}})
	activity := a.agentActivities["sub-hosted"]
	if activity == nil || activity.LastResult != a.translator.Text(i18n.MsgActivityHostedItem)+" [web_search_call]: completed" {
		t.Fatalf("hosted activity = %#v", activity)
	}
}

func TestToolModalMainShowsRawAssistantAndThinkingContent(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.messages = []string{"", ""}
	a.assistantRaw = map[int]string{0: "assistant raw: <tag>\n```go\nfmt.Println(\"detail\")\n```"}
	a.thinkRaw = map[int]string{1: "thinking raw: investigate every branch"}

	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal = false, want true")
	}
	content := stripANSI(a.renderExpandedTranscript())
	for _, want := range []string{"Assistant:", "assistant raw:", "<tag>", "```go", "Thinking:", "thinking raw:", "investigate every", "branch"} {
		if !strings.Contains(content, want) {
			t.Fatalf("main details missing raw content %q:\n%s", want, content)
		}
	}
}

func TestToolCallShowsRunningMessageBeforeResult(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.compactMode = false
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:     agent.EventToolCall,
		ToolCall: &provider.ToolCallBlock{ID: "tool-1", Name: "bash"},
		ToolArgs: map[string]any{"command": "sleep 10"},
	})

	if len(a.messages) < 2 {
		t.Fatalf("messages len = %d, want running message appended", len(a.messages))
	}
	got := stripANSI(a.messages[1])
	if !strings.Contains(got, "🔧 [bash] sleep 10 (running)") {
		t.Fatalf("running message = %q, want tool header running line", got)
	}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "done",
	})

	if len(a.messages) != 2 {
		t.Fatalf("messages len = %d, want one coalesced tool message", len(a.messages))
	}
	if len(a.toolResults) != 1 || a.toolResults[0].status != toolResultStatusCompleted {
		t.Fatalf("toolResults = %#v, want one completed entry", a.toolResults)
	}
	if got := stripANSI(a.renderMessageAt(1)); !strings.Contains(got, "done") || strings.Contains(got, "(running)") {
		t.Fatalf("rendered coalesced tool result = %q, want final output only", got)
	}
}

func TestToolResultReprintsAfterRunningMessage(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.compactMode = false
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:     agent.EventToolCall,
		ToolCall: &provider.ToolCallBlock{ID: "tool-1", Name: "bash"},
		ToolArgs: map[string]any{"command": "echo hello"},
	})
	if len(a.toolResults) != 1 || a.toolResults[0].status != toolResultStatusRunning {
		t.Fatalf("toolResults = %#v, want running tool entry", a.toolResults)
	}
	if got := stripANSI(a.renderMessageAt(1)); !strings.Contains(got, "🔧 [bash] echo hello (running)") {
		t.Fatalf("running message = %q, want tool header running line", got)
	}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "hello",
	})

	if len(a.toolResults) != 1 || a.toolResults[0].status != toolResultStatusCompleted {
		t.Fatalf("toolResults = %#v, want one completed entry", a.toolResults)
	}
	if got := stripANSI(a.renderMessageAt(1)); !strings.Contains(got, "hello") || strings.Contains(got, "(running)") {
		t.Fatalf("rendered coalesced tool result = %q, want final output", got)
	}
}

func TestToolExecutionStartAndEndPrintToTUIScrollback(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.compactMode = false
	a.program = tea.NewProgram(a)
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "echo hello"},
	})
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "hello",
	})

	a.printMu.Lock()
	defer a.printMu.Unlock()
	if got := len(a.printQueue); got != 1 {
		t.Fatalf("print queue len = %d, want only completed result: %#v", got, a.printQueue)
	}
	if plain := stripANSI(a.printQueue[0]); !strings.Contains(plain, "hello") || strings.Contains(plain, "(running)") {
		t.Fatalf("result print = %q, want completed execution result", plain)
	}
}

func TestToolCallExecutionEventsPrintOnce(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.compactMode = false
	a.program = tea.NewProgram(a)
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:     agent.EventToolCall,
		ToolCall: &provider.ToolCallBlock{ID: "tool-1", Name: "bash"},
		ToolArgs: map[string]any{"command": "echo hello"},
	})
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "echo hello"},
	})
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "hello",
	})
	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolResult,
		ToolCallID: "tool-1",
		ToolName:   "bash",
		ToolResult: "hello",
	})

	a.printMu.Lock()
	defer a.printMu.Unlock()
	if got := len(a.printQueue); got != 1 {
		t.Fatalf("print queue len = %d, want one completed result: %#v", got, a.printQueue)
	}
	if got := len(a.toolResults); got != 1 || a.toolResults[0].status != toolResultStatusCompleted {
		t.Fatalf("toolResults = %#v, want one completed entry", a.toolResults)
	}
}

func TestCompactToolExecutionCoalescesRunningAndResult(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-compact",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "echo hello"},
	})
	if len(a.toolResults) != 1 || a.toolResults[0].status != toolResultStatusRunning {
		t.Fatalf("toolResults = %#v, want one running entry", a.toolResults)
	}
	a.printMu.Lock()
	if got := len(a.printQueue); got != 0 {
		a.printMu.Unlock()
		t.Fatalf("running event was printed in compact mode: %d entries", got)
	}
	a.printMu.Unlock()

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "tool-compact",
		ToolName:   "bash",
		ToolResult: "hello",
	})

	if len(a.toolResults) != 1 || a.toolResults[0].status != toolResultStatusCompleted {
		t.Fatalf("toolResults = %#v, want one completed entry", a.toolResults)
	}
	if got := stripANSI(a.renderMessageAt(a.toolResults[0].msgIndex)); !strings.Contains(got, "hello") {
		t.Fatalf("compact tool result = %q, want final summary", got)
	}
	a.printMu.Lock()
	defer a.printMu.Unlock()
	if got := len(a.printQueue); got != 1 {
		t.Fatalf("print queue len = %d, want one completed tool line: %#v", got, a.printQueue)
	}
	if strings.Contains(stripANSI(a.printQueue[0]), "running:") {
		t.Fatalf("compact print queue kept running event: %q", a.printQueue[0])
	}
}

func TestCompactEventDisplayFiltersRoutineEvents(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.handleAgentEvent(agent.Event{Type: agent.EventStatus, StatusMessage: "working"})
	if got := stripANSI(a.renderTranscriptContent()); strings.Contains(got, "working") {
		t.Fatalf("routine status was rendered in compact mode: %q", got)
	}
	a.handleAgentEvent(agent.Event{Type: agent.EventStatus, StatusMessage: "Warning: retrying tool"})
	if !strings.Contains(stripANSI(a.renderTranscriptContent()), "Warning") {
		t.Fatalf("important status was hidden in compact mode: %#v", a.messages)
	}
	a.handleAgentEvent(agent.Event{Type: agent.EventHostedItem, HostedItem: &provider.HostedItem{Type: "web_search_call", Status: "in_progress"}})
	if got := stripANSI(a.renderTranscriptContent()); strings.Contains(got, "in_progress") {
		t.Fatalf("in-progress hosted item was rendered in compact mode: %q", got)
	}
	a.handleAgentEvent(agent.Event{Type: agent.EventHostedItem, HostedItem: &provider.HostedItem{Type: "web_search_call", Status: "completed"}})
	if got := stripANSI(a.renderTranscriptContent()); !strings.Contains(got, "completed") {
		t.Fatalf("completed hosted item was hidden in compact mode: %q", got)
	}
}

func TestCompactEventDisplayKeepsThinkingVisible(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)
	thinking := "investigate first " + strings.Repeat("reasoning detail ", 80) + "investigate last"
	a.handleAgentEvent(agent.Event{Type: agent.EventThinkDelta, ThinkDelta: thinking})

	active := stripANSI(a.renderLiveTranscriptContent())
	// Thinking text is wrapped to the current terminal width, so a line break
	// may be inserted between words without hiding any content.
	normalizedActive := strings.Join(strings.Fields(active), " ")
	if !strings.Contains(normalizedActive, "investigate first") || !strings.Contains(normalizedActive, "investigate last") {
		t.Fatalf("compact live view hid thinking content: %q", active)
	}

	a.handleAgentEvent(agent.Event{Type: agent.EventTurnEnd})

	a.printMu.Lock()
	if got := len(a.printQueue); got != 1 {
		a.printMu.Unlock()
		t.Fatalf("compact thinking print queue len = %d, want 1: %#v", got, a.printQueue)
	}
	printed := stripANSI(a.printQueue[0])
	normalizedPrinted := strings.Join(strings.Fields(printed), " ")
	if !strings.Contains(normalizedPrinted, "investigate first") || !strings.Contains(normalizedPrinted, "investigate last") {
		a.printMu.Unlock()
		t.Fatalf("compact thinking print = %q, want full content", printed)
	}
	a.printMu.Unlock()

	a.Update(teaSpecialKeyMsgForTest(tea.KeyCtrlG))
	a.printMu.Lock()
	defer a.printMu.Unlock()
	if got := len(a.printQueue); got != 2 {
		t.Fatalf("full mode queue len = %d, want thinking plus mode status: %#v", got, a.printQueue)
	}
	if got := strings.Count(stripANSI(strings.Join(a.printQueue, "\n")), "investigate first"); got != 1 {
		t.Fatalf("thinking was duplicated after switching to full mode: %d occurrences", got)
	}
}

func TestSwitchingToFullEventDisplayReplaysFilteredEvents(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)
	a.handleAgentEvent(agent.Event{Type: agent.EventStatus, StatusMessage: "working"})
	a.handleAgentEvent(agent.Event{Type: agent.EventHostedItem, HostedItem: &provider.HostedItem{Type: "search", Status: "in_progress"}})

	a.Update(teaSpecialKeyMsgForTest(tea.KeyCtrlG))
	a.printMu.Lock()
	defer a.printMu.Unlock()
	joined := stripANSI(strings.Join(a.printQueue, "\n"))
	if !strings.Contains(joined, "working") || !strings.Contains(joined, "in_progress") {
		t.Fatalf("full mode did not replay filtered events: %q", joined)
	}
}

func TestSwitchingToFullEventDisplayKeepsRunningToolOutOfScrollback(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)
	a.messages = []string{"assistant start"}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionStart,
		ToolCallID: "tool-switch",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "echo switch"},
	})
	// Treat the pre-existing assistant message as already committed so the
	// assertion below only observes events caused by the mode switch.
	a.printedMessageIdx = map[int]bool{0: true}
	// Compact is the default. Switching to full while the command is active
	// must leave it in the managed viewport, not terminal scrollback.
	a.Update(teaSpecialKeyMsgForTest(tea.KeyCtrlG))
	a.printMu.Lock()
	queued := strings.Join(a.printQueue, "\n")
	if strings.Contains(stripANSI(queued), "(running)") {
		a.printMu.Unlock()
		t.Fatalf("running tool was printed during compact->full switch: %#v", a.printQueue)
	}
	a.printMu.Unlock()
	if live := stripANSI(a.renderLiveTranscriptContent()); !strings.Contains(live, "echo switch (running)") {
		t.Fatalf("full live transcript missing running tool: %q", live)
	}

	a.handleAgentEvent(agent.Event{
		Type:       agent.EventToolExecutionEnd,
		ToolCallID: "tool-switch",
		ToolName:   "bash",
		ToolResult: "switch done",
	})
	a.printMu.Lock()
	defer a.printMu.Unlock()
	joined := stripANSI(strings.Join(a.printQueue, "\n"))
	if strings.Contains(joined, "(running)") {
		t.Fatalf("completed print retained running row: %#v", a.printQueue)
	}
	if got := strings.Count(joined, "switch done"); got != 1 {
		t.Fatalf("completed result occurrences = %d, want one: %#v", got, a.printQueue)
	}
}

func TestModeCommandShowsOSPermissions(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "os", false, false, nil, nil, nil)
	a.handleCommand("/mode")
	joined := stripANSI(strings.Join(a.messages, "\n"))
	if !strings.Contains(joined, "Current mode: OS") {
		t.Fatalf("current OS mode missing: %q", joined)
	}
	if !strings.Contains(joined, "BASH only") || !strings.Contains(joined, "no sandbox") {
		t.Fatalf("OS permission description missing: %q", joined)
	}
}

func TestCycleModeShowsOSLabel(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "yolo", false, false, nil, nil, nil)
	a.cycleMode()
	if a.mode != "os" {
		t.Fatalf("mode after cycling from yolo = %q, want os", a.mode)
	}
	if got := stripANSI(a.messages[len(a.messages)-1]); !strings.Contains(got, "OS - Bash only, no sandbox") {
		t.Fatalf("OS mode label = %q", got)
	}
}

func TestToolModalHeightFitsTerminalWithoutCroppingTop(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.ready = true
	a.width = 80
	a.height = 24
	a.messages = []string{strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
		"line 10",
		"line 11",
		"line 12",
		"line 13",
		"line 14",
		"line 15",
		"line 16",
		"line 17",
		"line 18",
		"line 19",
		"line 20",
	}, "\n")}

	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal = false, want true")
	}
	view := a.View()
	if got := lipgloss.Height(view); got != a.height {
		t.Fatalf("View() height = %d, want %d", got, a.height)
	}
	if plain := stripANSI(view); !strings.Contains(plain, "Agent details") {
		t.Fatalf("modal top was cropped from view:\n%s", plain)
	}
}

func TestToolModalLongSubAgentLogDoesNotCropTop(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", true, false, nil, nil, nil)
	a.ready = true
	a.width = 80
	a.height = 24
	a.messages = []string{"main transcript"}
	for range 12 {
		a.recordAgentActivity(agent.Event{
			Type:          agent.EventStatus,
			AgentID:       "sub-1",
			StatusMessage: strings.Repeat("long subagent output ", 40),
		})
	}

	if !a.openLatestToolModal() {
		t.Fatal("openLatestToolModal = false, want true")
	}
	a.switchToolModalTarget(1)
	modal := a.renderToolModal()
	availableHeight := a.height - lipgloss.Height(a.renderFooter())
	if got := lipgloss.Height(modal); got != availableHeight {
		t.Fatalf("modal height = %d, want available height %d", got, availableHeight)
	}
	view := stripANSI(a.View())
	lines := strings.Split(view, "\n")
	if got := len(lines); got != a.height {
		t.Fatalf("View() height = %d, want %d", got, a.height)
	}
	if !strings.HasPrefix(lines[0], "╭") {
		t.Fatalf("modal top border was cropped from view:\n%s", view)
	}
	if !strings.Contains(view, "Agent details") {
		t.Fatalf("modal title was cropped from view:\n%s", view)
	}
}

func TestShowNextQuestionTracksCurrentQuestionAndClearResetsIt(t *testing.T) {
	a := &App{questionQueue: []pendingQuestion{{questionID: "q1", question: "Pick?", options: []string{"A", "B"}}}}
	a.showNextQuestion()
	if !a.waitingForQuestion || a.pendingQuestionID != "q1" || len(a.currentQuestion.options) != 2 {
		t.Fatalf("question state = waiting %v id %q options %v", a.waitingForQuestion, a.pendingQuestionID, a.currentQuestion.options)
	}
	a.clearQuestionState()
	if a.waitingForQuestion || a.pendingQuestionID != "" || len(a.currentQuestion.options) != 0 {
		t.Fatalf("question state after clear = waiting %v id %q options %v", a.waitingForQuestion, a.pendingQuestionID, a.currentQuestion.options)
	}
}

func TestRuneInputTabDoesNotCycleMode(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input.SetValue("prefix ")

	a.Update(teaKeyMsgForTest("tab"))
	a.flushInputQueue()

	if got := a.mode; got != "agent" {
		t.Fatalf("mode = %q, want agent", got)
	}
	if got := a.input.Value(); got != "prefix tab" {
		t.Fatalf("input = %q, want %q", got, "prefix tab")
	}
}

func TestRuneInputEscDoesNotAbortOrClearInput(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input.SetValue("prefix ")

	a.Update(teaKeyMsgForTest("esc"))
	a.flushInputQueue()

	if got := a.input.Value(); got != "prefix esc" {
		t.Fatalf("input = %q, want %q", got, "prefix esc")
	}
}

func TestInitWithProgramDoesNotBlock(t *testing.T) {
	a := NewApp(
		&historyInjectMockProvider{},
		&provider.Model{ID: "mock-model", Name: "Mock"},
		config.DefaultSettings(),
		nil,
		tools.NewRegistry(t.TempDir(), nil),
		"",
		"",
		"",
		nil,
		"agent",
		false,
		false,
		nil,
		nil,
		nil,
	)
	a.SetInitialMessage("hello")
	p := tea.NewProgram(a)
	a.SetProgram(p)

	done := make(chan struct{})
	go func() {
		_ = a.Init()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Init blocked while printing initial history")
	}
}

func TestInitAutoOpenAuthDialog(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.SetAutoOpenAuthDialog(true)

	_ = a.Init()

	if !a.auth.Open {
		t.Fatal("auth dialog should open on init")
	}
}

func TestPrintMessageOnceQueuesMoreThanInitialBuffer(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)

	const count = 300
	for i := 0; i < count; i++ {
		a.addMessage(fmt.Sprintf("line %03d", i))
	}

	a.printMu.Lock()
	defer a.printMu.Unlock()
	if got := len(a.printQueue); got != count {
		t.Fatalf("print queue len = %d, want %d", got, count)
	}
	if got, want := a.printQueue[0], "line 000"; got != want {
		t.Fatalf("first queued print = %q, want %q", got, want)
	}
	if got, want := a.printQueue[count-1], "line 299"; got != want {
		t.Fatalf("last queued print = %q, want %q", got, want)
	}
	for i := 0; i < count; i++ {
		if !a.printedMessageIdx[i] {
			t.Fatalf("message %d was not marked printed after queueing", i)
		}
	}
}

func TestProgramBackedTranscriptDoesNotMaintainFullLiveContent(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.program = tea.NewProgram(a)

	for i := 0; i < 20; i++ {
		a.addMessage(fmt.Sprintf("line %03d", i))
	}

	if a.liveContent != "" {
		t.Fatalf("liveContent = %q, want empty when program-backed scrollback is active", a.liveContent)
	}
}

// TestCacheHighlightThresholdMath verifies the arithmetic of the 50% boundary
// independent of any rendering logic.
func TestCacheHighlightThresholdMath(t *testing.T) {
	type tc struct {
		input     int
		cacheRead int
		wantHigh  bool
	}
	cases := []tc{
		{1000, 0, false},   // 0%
		{1000, 499, false}, // 49.9%
		{1000, 490, false}, // 49%
		{1000, 500, true},  // 50% — boundary: highlighted
		{1000, 501, true},  // 50.1%
		{1000, 750, true},  // 75%
		{1000, 1000, true}, // 100%
		{3, 2, true},       // 66.7% — small counts
		{3, 1, false},      // 33.3%
	}
	for _, c := range cases {
		pct := float64(c.cacheRead) / float64(c.input) * 100
		got := pct >= 50.0
		if got != c.wantHigh {
			t.Errorf("input=%d cacheRead=%d pct=%.4f: highlight=%v, want %v",
				c.input, c.cacheRead, pct, got, c.wantHigh)
		}
	}
}

type historyInjectMockProvider struct{}

func (p *historyInjectMockProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "ok"}
	ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "end_turn"}
	close(ch)
	return ch
}

func (p *historyInjectMockProvider) Name() string { return "mock" }
func (p *historyInjectMockProvider) API() string  { return "openai-chat" }
func (p *historyInjectMockProvider) Models() []*provider.Model {
	return []*provider.Model{{ID: "mock-model", Name: "Mock"}}
}
func (p *historyInjectMockProvider) GetModel(id string) *provider.Model {
	for _, m := range p.Models() {
		if m.ID == id {
			return m
		}
	}
	return nil
}

type esmSequenceProvider struct {
	mu             sync.Mutex
	name           string
	responses      []string
	withInspection bool
	calls          int
}

func (p *esmSequenceProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	withInspection := p.withInspection
	text := `{"status":"continue","summary":"default","evidence":[],"remaining_work":["not done"],"blockers":[]}`
	responseIdx := idx
	if withInspection {
		responseIdx = idx / 2
	}
	if responseIdx >= 0 && responseIdx < len(p.responses) {
		text = p.responses[responseIdx]
	}
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(ch)
		if withInspection && idx%2 == 0 {
			select {
			case <-ctx.Done():
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
				return
			case ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID:        fmt.Sprintf("inspect-%d", idx),
				Name:      "ls",
				Arguments: []byte(`{"path":"."}`),
			}}:
			}
			ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 3, Output: 1, TotalTokens: 4}}
			ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_use"}
			return
		}
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
			return
		case ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: text}:
		}
		ch <- provider.StreamEvent{Type: provider.StreamUsage, Usage: &provider.Usage{Input: 5, Output: 5, TotalTokens: 10}}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "end_turn"}
	}()
	return ch
}

func (p *esmSequenceProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "mock"
}

func (p *esmSequenceProvider) API() string { return "openai-chat" }

func (p *esmSequenceProvider) Models() []*provider.Model {
	return []*provider.Model{{ID: "mock-model", Name: "Mock"}}
}

func (p *esmSequenceProvider) GetModel(id string) *provider.Model {
	for _, m := range p.Models() {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *esmSequenceProvider) getCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func cloneMessages(msgs []provider.Message) []provider.Message {
	if len(msgs) == 0 {
		return nil
	}
	cloned := make([]provider.Message, len(msgs))
	for i, msg := range msgs {
		cloned[i] = msg
		if len(msg.Contents) > 0 {
			cloned[i].Contents = append([]provider.ContentBlock(nil), msg.Contents...)
		}
	}
	return cloned
}

type recordingHistoryProvider struct {
	mu         sync.Mutex
	calls      int
	lastParams provider.ChatParams
}

func (p *recordingHistoryProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls++
	p.lastParams = provider.ChatParams{
		SystemPrompt:  params.SystemPrompt,
		Messages:      cloneMessages(params.Messages),
		Tools:         append([]provider.ToolDefinition(nil), params.Tools...),
		ThinkingLevel: params.ThinkingLevel,
		MaxTokens:     params.MaxTokens,
		ModelID:       params.ModelID,
	}
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 2)
	ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "end_turn"}
	close(ch)
	return ch
}

func (p *recordingHistoryProvider) Name() string { return "mock" }
func (p *recordingHistoryProvider) API() string  { return "openai-chat" }

func (p *recordingHistoryProvider) Models() []*provider.Model {
	return []*provider.Model{{ID: "mock-model", Name: "Mock"}}
}

func (p *recordingHistoryProvider) GetModel(id string) *provider.Model {
	for _, m := range p.Models() {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *recordingHistoryProvider) getCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *recordingHistoryProvider) getLastParams() provider.ChatParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastParams
}

func TestProcessInputLoadsSessionHistoryIntoAgentEvenWhenUIHistoryAlreadyLoaded(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	sessionDir := filepath.Join(tmp, "sessions")

	sess := session.New(cwd, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sess.AppendMessage(provider.NewUserMessage("old user"))
	sess.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant"}}))

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	a := &App{
		provider:            &historyInjectMockProvider{},
		model:               &provider.Model{ID: "mock-model", Name: "Mock"},
		settings:            settings,
		session:             sess,
		registry:            tools.NewRegistry(cwd, nil),
		historyLoaded:       true, // UI already rendered history
		assistantRaw:        make(map[int]string),
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
	}

	cmd := a.processInput("new question")
	if cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	_ = cmd()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if a.agent != nil {
			msgs := a.agent.GetMessages()
			if len(msgs) >= 4 {
				if msgs[0].Role != "user" || msgs[0].Content != "old user" {
					t.Fatalf("first message = %+v, want old history user message", msgs[0])
				}
				if msgs[1].Role != "assistant" {
					t.Fatalf("second message role = %s, want assistant", msgs[1].Role)
				}
				if msgs[2].Role != "user" || msgs[2].Content != "new question" {
					t.Fatalf("third message = %+v, want new user message", msgs[2])
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for agent messages")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestContinueSessionPromptPayloadOrder(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	sessionDir := filepath.Join(tmp, "sessions")

	sess := session.New(cwd, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := sess.AppendMessage(provider.NewUserMessage("old user")); err != nil {
		t.Fatalf("append old user: %v", err)
	}
	if _, err := sess.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant"}})); err != nil {
		t.Fatalf("append old assistant: %v", err)
	}

	recordingProvider := &recordingHistoryProvider{}
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(
		recordingProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		sess,
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		false,
		false,
		nil,
		nil,
		nil,
	)
	_ = app.Init()

	cmd := app.processInput("new question")
	if cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	_ = cmd()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if recordingProvider.getCalls() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for provider payload")
		}
		time.Sleep(10 * time.Millisecond)
	}

	params := recordingProvider.getLastParams()
	if params.SystemPrompt == "" {
		t.Fatal("expected system prompt to be passed separately")
	}
	msgs := params.Messages
	if len(msgs) < 3 {
		t.Fatalf("messages len = %d, want at least 3", len(msgs))
	}
	if !msgs[0].SystemInjected || msgs[0].Role != "user" || !strings.Contains(msgs[0].Content, "[session context]") {
		t.Fatalf("first payload message = %+v, want injected session context", msgs[0])
	}
	if msgs[1].Role != "user" || msgs[1].Content != "old user" {
		t.Fatalf("second payload message = %+v, want old user", msgs[1])
	}
	if msgs[2].Role != "assistant" || len(msgs[2].Contents) == 0 || msgs[2].Contents[0].Text != "old assistant" {
		t.Fatalf("third payload message = %+v, want old assistant", msgs[2])
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || len(last.Contents) == 0 || last.Contents[0].Text != "new question" {
		t.Fatalf("last payload message = %+v, want new question", last)
	}
}

func TestInitThenProcessInputStillInjectsSessionHistory(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	sessionDir := filepath.Join(tmp, "sessions")

	sess := session.New(cwd, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sess.AppendMessage(provider.NewUserMessage("history user"))
	sess.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "history assistant"}}))

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(
		&historyInjectMockProvider{},
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		sess,
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		false,
		false,
		nil,
		nil,
		nil,
	)

	// Simulate real startup flow: Init() loads history into UI and flips historyLoaded.
	_ = app.Init()

	if !app.historyLoaded {
		t.Fatalf("historyLoaded = false, want true after Init")
	}

	cmd := app.processInput("follow-up")
	if cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	_ = cmd()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if app.agent != nil {
			msgs := app.agent.GetMessages()
			if len(msgs) >= 4 {
				if msgs[0].Role != "user" || msgs[0].Content != "history user" {
					t.Fatalf("first message = %+v, want history user", msgs[0])
				}
				if msgs[1].Role != "assistant" {
					t.Fatalf("second message role = %s, want assistant", msgs[1].Role)
				}
				if msgs[2].Role != "user" || msgs[2].Content != "follow-up" {
					t.Fatalf("third message = %+v, want follow-up user message", msgs[2])
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for agent messages")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCycleModeReregistersManagedAgent(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	mockProvider := &historyInjectMockProvider{}
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	app := NewApp(
		mockProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		session.New(cwd, filepath.Join(tmp, "sessions")),
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		true,
		false,
		mgr,
		nil,
		nil,
	)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	firstID := app.agent.ID()
	if firstID != "agent-master" {
		t.Fatalf("main agent ID = %s, want agent-master", firstID)
	}
	if _, ok := mgr.Get(firstID); !ok {
		t.Fatalf("initial agent %s was not registered", firstID)
	}

	app.cycleMode()

	if app.agent == nil {
		t.Fatal("agent is nil after cycleMode")
	}
	if _, ok := mgr.Get(app.agent.ID()); !ok {
		t.Fatalf("rebuilt agent %s was not registered", app.agent.ID())
	}
	if got := mgr.Count(); got != 1 {
		t.Fatalf("manager count after cycleMode = %d, want 1", got)
	}
}

func TestModelCommandResetsManagedAgentWithoutLeavingOldTab(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	mockProvider := &historyInjectMockProvider{}
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	app := NewApp(
		mockProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		session.New(cwd, filepath.Join(tmp, "sessions")),
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		true,
		false,
		mgr,
		nil,
		nil,
	)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	firstID := app.agent.ID()
	if firstID != "agent-master" {
		t.Fatalf("main agent ID = %s, want agent-master", firstID)
	}
	if _, ok := mgr.Get(firstID); !ok {
		t.Fatalf("initial agent %s was not registered", firstID)
	}

	app.handleCommand("/model mock-model")

	if app.agent != nil {
		t.Fatal("agent should be reset after /model")
	}
	if _, ok := mgr.Get(firstID); ok {
		t.Fatalf("old agent %s should have been removed from manager", firstID)
	}
	if got := mgr.Count(); got != 0 {
		t.Fatalf("manager count after /model = %d, want 0", got)
	}

	if cmd := app.processInput("second"); cmd == nil {
		t.Fatal("second processInput returned nil command")
	}
	secondID := app.agent.ID()
	if secondID != "agent-master" {
		t.Fatalf("second main agent ID = %s, want agent-master", secondID)
	}
	if got := mgr.Count(); got != 1 {
		t.Fatalf("manager count after second input = %d, want 1", got)
	}
	if _, ok := mgr.Get(secondID); !ok {
		t.Fatalf("new agent %s was not registered", secondID)
	}
}

func TestTUIDoneKeepsManagedMainAgentForNextTurn(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	mockProvider := &historyInjectMockProvider{}
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	app := NewApp(
		mockProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		session.New(cwd, filepath.Join(tmp, "sessions")),
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		true,
		false,
		mgr,
		nil,
		nil,
	)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	id := app.agent.ID()
	if status, ok := mgr.Status(id); !ok || status.State != "running" {
		t.Fatalf("managed main status before completion = %#v, ok=%v; want running", status, ok)
	}
	app.handleAgentEvent(agent.Event{Type: agent.EventDone})

	if _, ok := mgr.Get(id); !ok {
		t.Fatalf("managed main agent %s was removed after EventDone", id)
	}
	if status, ok := mgr.Status(id); !ok || status.State != "done" {
		t.Fatalf("managed main status after completion = %#v, ok=%v; want done", status, ok)
	}
}

func TestTUIWorkflowErrorMarksManagedMainAgentError(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	mockProvider := &historyInjectMockProvider{}
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	app := NewAppWithWorkflows(
		mockProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		session.New(cwd, filepath.Join(tmp, "sessions")),
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		false,
		false,
		true,
		mgr,
		nil,
		nil,
	)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	id := app.agent.ID()
	if status, ok := mgr.Status(id); !ok || status.State != "running" {
		t.Fatalf("managed workflow status before error = %#v, ok=%v; want running", status, ok)
	}
	app.handleAgentEvent(agent.Event{Type: agent.EventError, Error: assertErr("workflow failed")})

	status, ok := mgr.Status(id)
	if !ok {
		t.Fatalf("managed workflow agent %s missing after EventError", id)
	}
	if status.State != "error" || status.Error != "workflow failed" {
		t.Fatalf("managed workflow status after error = %#v; want provider error message", status)
	}
}

func TestRecordAgentActivityPreservesProviderTerminalError(t *testing.T) {
	a := &App{}
	a.recordAgentActivity(agent.Event{
		AgentID: "sub-1",
		Type:    agent.EventRunFinished,
		Status:  agent.TaskFailed,
		Error:   assertErr("upstream HTTP 503 with request id secret-request-id"),
	})

	activity := a.agentActivities["sub-1"]
	if activity == nil {
		t.Fatal("activity missing")
	}
	if activity.LastResult != "upstream HTTP 503 with request id secret-request-id" {
		t.Fatalf("activity error = %#v, want provider diagnostic", activity)
	}
}

func TestDelegateToggleOffRemovesManagedMainAgent(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	mockProvider := &historyInjectMockProvider{}
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	app := NewApp(
		mockProvider,
		&provider.Model{ID: "mock-model", Name: "Mock"},
		settings,
		session.New(cwd, filepath.Join(tmp, "sessions")),
		tools.NewRegistry(cwd, nil),
		"",
		"",
		"",
		nil,
		"agent",
		false,
		true,
		mgr,
		nil,
		nil,
	)

	if cmd := app.processInput("first"); cmd == nil {
		t.Fatal("processInput returned nil command")
	}
	id := app.agent.ID()
	app.handleDelegateCommand([]string{"/delegate", "off"})

	if _, ok := mgr.Get(id); ok {
		t.Fatalf("managed delegate main agent %s remained after /delegate off", id)
	}
	if app.delegateMode {
		t.Fatal("delegateMode = true after /delegate off")
	}
}

func TestAgentSwitchMessageDoesNotClaimInputRetarget(t *testing.T) {
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	if _, err := mgr.Create(agent.AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if _, err := mgr.Create(agent.AgentOptions{ID: "sub-1", ParentID: "main"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	app := &App{
		multiAgent:          true,
		agentMgr:            mgr,
		activeAgent:         "main",
		assistantRaw:        make(map[int]string),
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
	}

	app.switchAgent("sub-1")

	if app.activeAgent != "sub-1" {
		t.Fatalf("activeAgent = %s, want sub-1", app.activeAgent)
	}
	joined := stripANSI(strings.Join(app.messages, "\n"))
	if !strings.Contains(joined, "Focused agent tab: sub-1") {
		t.Fatalf("switch message missing: %q", joined)
	}
	if !strings.Contains(joined, "Input still goes to the main agent") {
		t.Fatalf("switch message should explain input target: %q", joined)
	}
}

func TestRenderAgentTabBarShowsSubAgentStatus(t *testing.T) {
	mgr := agent.NewAgentManager(&agent.AgentFactory{})
	if _, err := mgr.Create(agent.AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if _, err := mgr.Create(agent.AgentOptions{ID: "sub-1", ParentID: "main"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	mgr.MarkRunning("sub-1")

	got := stripANSI(renderAgentTabBar(i18n.New(i18n.LanguageEN), mgr, "sub-1", 120))
	if !strings.Contains(got, "main") || !strings.Contains(got, "sub-1") {
		t.Fatalf("tab bar missing agent IDs: %q", got)
	}
	if strings.Index(got, "main") > strings.Index(got, "sub-1") {
		t.Fatalf("tab bar order should be stable with parent first: %q", got)
	}
	if !strings.Contains(got, "● sub-1") {
		t.Fatalf("tab bar missing running status for sub-agent: %q", got)
	}
}

func TestTranscriptAndToolModalPrefixesFollowLanguage(t *testing.T) {
	a := &App{
		translator:          i18n.New(i18n.LanguageZH),
		width:               40,
		assistantRaw:        map[int]string{0: "回答"},
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
		thinkRaw:            map[int]string{1: "检查"},
		messages:            make([]string, 2),
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
	}

	if got := stripANSI(a.renderAssistantMessage(0)); !strings.HasPrefix(got, "助手：") {
		t.Fatalf("assistant prefix not localized: %q", got)
	}
	if got := stripANSI(a.renderThinkMessage(1)); !strings.HasPrefix(got, "思考：") {
		t.Fatalf("thinking prefix not localized: %q", got)
	}
	if got := a.renderExpandedMessageAt(0); !strings.HasPrefix(got, "助手：\n") {
		t.Fatalf("expanded assistant title not localized: %q", got)
	}
	if got := a.renderExpandedMessageAt(1); !strings.HasPrefix(got, "思考：\n") {
		t.Fatalf("expanded thinking title not localized: %q", got)
	}
}

func TestESMGuideCommandQueuesGuidance(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	a := NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	store := esm.NewStore(sessionDir)
	if _, err := store.Create(context.Background(), sess.GetHeader().ID, "finish the real objective"); err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}

	a.handleESMCommand("/esm guide prioritize the failing tests")

	pending, err := store.PendingGuidance(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("PendingGuidance: %v", err)
	}
	if len(pending) != 1 || pending[0].Guidance != "prioritize the failing tests" {
		t.Fatalf("pending = %#v", pending)
	}

	// Empty guidance is rejected and queues nothing.
	a.handleESMCommand("/esm guide    ")
	pending, err = store.PendingGuidance(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("PendingGuidance after empty guide: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending after empty guide = %#v", pending)
	}
}

func TestESMObjectiveCanBeCreatedAndEditedDuringActiveRun(t *testing.T) {
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "mock-model", Name: "Mock Model"}}, nil)
	a := NewApp(mockProvider, mockProvider.Models()[0], settings, sess, tools.NewRegistry(tmp, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	// Model the normal foreground setup before any ESM objective exists. The
	// command must alter only persisted ESM state and steering, never reset or
	// start a second foreground run.
	a.isThinking = true
	a.prepareESMRun()
	a.handleESMCommand("/esm finish the active task")

	store := esm.NewStore(sessionDir)
	obj, err := store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("Get created objective: %v", err)
	}
	if obj.Objective != "finish the active task" {
		t.Fatalf("created objective = %#v", obj)
	}
	a.esmMu.Lock()
	tracked := a.esmRunTracked
	toolsRegistered := a.esmToolsRegistered
	a.esmMu.Unlock()
	if !tracked {
		t.Fatal("active run was not enrolled for ESM completion/continuation")
	}
	if toolsRegistered {
		t.Fatal("active run command must not mutate the live agent registry")
	}
	messages := a.nextESMSteeringMessages()
	if len(messages) != 1 || !messages[0].SystemInjected || !strings.Contains(messages[0].Content, "finish the active task") {
		t.Fatalf("created objective steering = %#v", messages)
	}
	if messages := a.nextESMSteeringMessages(); len(messages) != 0 {
		t.Fatalf("duplicate steering = %#v, want none", messages)
	}

	a.handleESMCommand("/esm edit finish the revised active task")
	messages = a.nextESMSteeringMessages()
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "finish the revised active task") {
		t.Fatalf("edited objective steering = %#v", messages)
	}

	a.handleESMCommand("/esm pause")
	obj, err = store.Get(context.Background(), sess.GetHeader().ID)
	if err != nil {
		t.Fatalf("Get objective after rejected pause: %v", err)
	}
	if obj.Status != esm.StatusActive {
		t.Fatalf("active-run pause should be rejected, objective = %#v", obj)
	}
}
