package openaiapi

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/util"
)

// toolCallInfo tracks a tool call through its lifecycle.
type toolCallInfo struct {
	Name   string
	Args   map[string]any
	Result string
	Diff   *tools.FileDiff
	Error  error
	Status string // "running", "completed", "failed"
}

// formatToolResult dispatches to collapsed or expanded based on detail level.
// detail: "collapsed" (default) or "expanded"
func formatToolResult(tc *toolCallInfo, detail string) string {
	if detail == "expanded" {
		return formatToolExpanded(tc)
	}
	return formatToolCollapsed(tc)
}

// formatToolCollapsed renders a one-line summary.
// Most tools: 🔧 `read` main.go ✅
// edit/write with diff: always shows path + diff (never fully collapsed)
// Errors: always shown
func formatToolCollapsed(tc *toolCallInfo) string {
	var sb strings.Builder

	// Errors are always shown in full
	if tc.Error != nil {
		sb.WriteString(formatToolHeaderMD(tc.Name, tc.Args))
		sb.WriteString("\n\n")
		sb.WriteString(fmt.Sprintf("> ❌ Error: %v\n\n", tc.Error))
		return sb.String()
	}

	// edit/write with diff — always show path + diff
	if (tc.Name == "edit" || tc.Name == "write" || tc.Name == "insert") && tc.Diff != nil && tc.Diff.Unified != "" {
		sb.WriteString(formatToolHeaderMD(tc.Name, tc.Args))
		sb.WriteString("\n\n")
		sb.WriteString(fmt.Sprintf("```diff\n%s", tc.Diff.Unified))
		if !strings.HasSuffix(tc.Diff.Unified, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
		return sb.String()
	}

	// Everything else: one-line summary
	status := "✅"
	if tc.Status == "failed" {
		status = "❌"
	}
	sb.WriteString(formatToolHeaderMD(tc.Name, tc.Args))
	sb.WriteString(" ")
	sb.WriteString(status)
	sb.WriteString("\n\n")
	return sb.String()
}

// formatToolExpanded renders a tool call with full output in code fences.
func formatToolExpanded(tc *toolCallInfo) string {
	var sb strings.Builder

	sb.WriteString(formatToolHeaderMD(tc.Name, tc.Args))
	sb.WriteString("\n\n")

	// Error
	if tc.Error != nil {
		sb.WriteString(fmt.Sprintf("> ❌ Error: %v\n\n", tc.Error))
		return sb.String()
	}

	// Diff output (edit/write with diff)
	if tc.Diff != nil && tc.Diff.Unified != "" {
		sb.WriteString(fmt.Sprintf("```diff\n%s", tc.Diff.Unified))
		if !strings.HasSuffix(tc.Diff.Unified, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
		return sb.String()
	}

	// Result output
	if tc.Result != "" {
		lang := inferCodeLang(tc.Name, tc.Args)
		sb.WriteString(fmt.Sprintf("```%s\n%s", lang, tc.Result))
		if !strings.HasSuffix(tc.Result, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
	}

	return sb.String()
}

// formatToolHeaderMD builds the tool header line.
// Uses plain text with emoji prefix — no markdown formatting to avoid
// rendering issues when streamed in chunks.
func formatToolHeaderMD(name string, args map[string]any) string {
	keyArg := toolKeyArg(name, args)
	if keyArg == "" {
		return fmt.Sprintf("🔧 %s", name)
	}
	return fmt.Sprintf("🔧 %s: %s", name, keyArg)
}

// formatToolRunning returns a status line when a tool starts executing.
func formatToolRunning(name string, args map[string]any) string {
	keyArg := toolKeyArg(name, args)
	if keyArg == "" {
		return fmt.Sprintf("⏳ %s running...\n\n", name)
	}
	return fmt.Sprintf("⏳ %s: %s\n\n", name, keyArg)
}

// formatToolHeader builds the header line (used by SSE content status).
func formatToolHeader(name string, args map[string]any) string {
	keyArg := toolKeyArg(name, args)
	if keyArg == "" {
		return fmt.Sprintf("🔧 [%s]", name)
	}
	return fmt.Sprintf("🔧 [%s] %s", name, keyArg)
}

// --- Language inference ---

// inferCodeLang guesses the code fence language from tool name and args.
func inferCodeLang(toolName string, args map[string]any) string {
	switch toolName {
	case "bash":
		return "bash"
	case "read", "write":
		if path, ok := args["path"].(string); ok {
			return langFromPath(path)
		}
	case "grep", "find", "ls":
		return "" // plain text
	}
	return ""
}

// langFromPath infers a code fence language from a file extension.
func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".sh", ".bash":
		return "bash"
	case ".zsh":
		return "zsh"
	case ".ps1":
		return "powershell"
	case ".sql":
		return "sql"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss":
		return "scss"
	case ".json":
		return "json"
	case ".jsonc":
		return "jsonc"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".md", ".markdown":
		return "markdown"
	case ".dockerfile":
		return "dockerfile"
	case ".tf":
		return "hcl"
	case ".lua":
		return "lua"
	case ".r":
		return "r"
	case ".php":
		return "php"
	case ".pl", ".pm":
		return "perl"
	case ".ex", ".exs":
		return "elixir"
	case ".erl":
		return "erlang"
	case ".hs":
		return "haskell"
	case ".scala":
		return "scala"
	case ".clj":
		return "clojure"
	case ".vim":
		return "vim"
	case ".proto":
		return "protobuf"
	case ".graphql", ".gql":
		return "graphql"
	case ".ini", ".cfg", ".conf":
		return "ini"
	case ".env":
		return "bash"
	case ".makefile":
		return "makefile"
	default:
		base := strings.ToLower(filepath.Base(path))
		switch base {
		case "makefile", "gnumakefile":
			return "makefile"
		case "dockerfile":
			return "dockerfile"
		case "vagrantfile", "gemfile":
			return "ruby"
		}
		return ""
	}
}

// --- Key arg extraction ---

// toolKeyArg extracts the most relevant argument for display.
func toolKeyArg(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch name {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			return util.TruncateWithSuffix(cmd, 120, "...")
		}
	case "read", "write", "edit", "insert", "ls":
		if path, ok := args["path"].(string); ok {
			return path
		}
	case "grep":
		var parts []string
		if pattern, ok := args["pattern"].(string); ok {
			parts = append(parts, pattern)
		}
		if path, ok := args["path"].(string); ok {
			parts = append(parts, path)
		}
		return strings.Join(parts, " ")
	case "find":
		var parts []string
		if pattern, ok := args["pattern"].(string); ok {
			parts = append(parts, pattern)
		}
		if path, ok := args["path"].(string); ok {
			parts = append(parts, path)
		}
		return strings.Join(parts, " ")
	default:
		for _, key := range []string{"path", "command", "pattern", "query", "name"} {
			if v, ok := args[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}
