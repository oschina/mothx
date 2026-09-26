package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func TestSpillOversizedToolResultWritesFullOutput(t *testing.T) {
	workDir := t.TempDir()
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	a := &Agent{registry: registry}

	msg := provider.Message{
		Role:       "toolResult",
		ToolName:   "bash",
		ToolCallID: "call/1:odd chars",
		Content:    "line1\nline2\nline3",
	}

	spill, ok := a.spillOversizedToolResult(msg)
	if !ok {
		t.Fatal("spillOversizedToolResult failed, want success")
	}
	if spill.lines != 3 || spill.bytes != len(msg.Content) {
		t.Fatalf("spill stats = %+v, want lines 3 bytes %d", spill, len(msg.Content))
	}
	if !strings.HasPrefix(spill.path, filepath.Join(workDir, ".mothx", "tmp", "context-guard")) {
		t.Fatalf("spill path outside staging dir: %s", spill.path)
	}
	if strings.Contains(spill.path, "/") && strings.Contains(filepath.Base(spill.path), " ") {
		t.Fatalf("spill filename not sanitized: %s", spill.path)
	}
	data, err := os.ReadFile(spill.path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(data) != msg.Content {
		t.Fatalf("spill content = %q, want %q", data, msg.Content)
	}
}

func TestSpillOversizedToolResultFallbacks(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	a := &Agent{registry: registry}

	// Empty content cannot be spilled.
	if _, ok := a.spillOversizedToolResult(provider.Message{Role: "toolResult", ToolName: "bash"}); ok {
		t.Fatal("empty tool result should not spill")
	}

	// Text blocks are concatenated when Content is empty.
	blocks := provider.Message{
		Role:     "toolResult",
		ToolName: "bash",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "part1\n"},
			{Type: "text", Text: "part2"},
		},
	}
	spill, ok := a.spillOversizedToolResult(blocks)
	if !ok {
		t.Fatal("block-based tool result should spill")
	}
	data, err := os.ReadFile(spill.path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(data) != "part1\npart2" {
		t.Fatalf("spill content = %q", data)
	}

	// A nil registry has no workdir; spill must fail without panicking.
	noRegistry := &Agent{}
	if _, ok := noRegistry.spillOversizedToolResult(provider.Message{Role: "toolResult", Content: "x"}); ok {
		t.Fatal("spill without registry should fail")
	}
}

func TestContextGuardToolResultMessages(t *testing.T) {
	msg := provider.Message{Role: "toolResult", ToolName: "bash", ToolCallID: "c1", Content: strings.Repeat("y", 100)}

	spilled := contextGuardToolResult(msg, 100, 50, 80, 20, contextGuardSpill{path: "/wd/.mothx/tmp/context-guard/f.txt", lines: 9, bytes: 100}, true)
	if !spilled.IsError || !strings.HasPrefix(spilled.Content, "[Context guard]") {
		t.Fatalf("spilled guard result malformed: %#v", spilled)
	}
	if !strings.Contains(spilled.Content, "/wd/.mothx/tmp/context-guard/f.txt") || !strings.Contains(spilled.Content, "offset/limit") {
		t.Fatalf("spilled guard message missing path or paging guidance: %q", spilled.Content)
	}
	if !isContextGuardToolResult(spilled) {
		t.Fatal("spilled guard result not recognized by isContextGuardToolResult")
	}

	omitted := contextGuardToolResult(msg, 100, 50, 80, 20, contextGuardSpill{}, false)
	if !strings.Contains(omitted.Content, "maxResults") || strings.Contains(omitted.Content, "preserved at") {
		t.Fatalf("fallback guard message wording changed unexpectedly: %q", omitted.Content)
	}
	if !isContextGuardToolResult(omitted) {
		t.Fatal("fallback guard result not recognized by isContextGuardToolResult")
	}
}

func TestPruneContextGuardSpillsKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	total := contextGuardSpillMaxFiles + 10
	for i := 0; i < total; i++ {
		name := filepath.Join(dir, fmt.Sprintf("spill-%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed spill file: %v", err)
		}
		// Deterministic increasing mtimes so "newest" is well defined.
		stamp := modTimeForTest(i)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatalf("set mtime: %v", err)
		}
	}

	pruneContextGuardSpills(dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != contextGuardSpillMaxFiles {
		t.Fatalf("entries after prune = %d, want %d", len(entries), contextGuardSpillMaxFiles)
	}
	// The oldest seeded files must be the ones removed.
	if _, err := os.Stat(filepath.Join(dir, "spill-000.txt")); !os.IsNotExist(err) {
		t.Fatal("oldest spill file should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("spill-%03d.txt", total-1))); err != nil {
		t.Fatalf("newest spill file should survive prune: %v", err)
	}
}

func modTimeForTest(index int) time.Time {
	return time.Date(2026, 1, 1, 0, 0, index, 0, time.UTC)
}
