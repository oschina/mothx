package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/sandbox"
)

func TestInsertToolPositions(t *testing.T) {
	tests := []struct {
		name     string
		position map[string]any
		want     string
	}{
		{"head", map[string]any{"type": "head"}, "X\na\nb\n"},
		{"tail", map[string]any{"type": "tail"}, "a\nb\nX"},
		{"before line", map[string]any{"type": "before_line", "line": float64(2)}, "a\nX\nb\n"},
		{"after line", map[string]any{"type": "after_line", "line": float64(1)}, "a\nX\nb\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file.txt")
			if err := os.WriteFile(path, []byte("a\nb\n"), 0644); err != nil {
				t.Fatal(err)
			}
			tool := NewInsertTool(NewRegistry(dir, sandbox.NewNoneSandbox()))
			_, err := tool.Execute(context.Background(), map[string]any{
				"path": "file.txt", "content": "X", "position": tt.position,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInsertToolDedupeAndDryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewInsertTool(NewRegistry(dir, sandbox.NewNoneSandbox()))

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "file.txt", "content": "b\n", "position": map[string]any{"type": "tail"},
		"dedupe": map[string]any{"enabled": true, "mode": "line"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diff != nil || !strings.Contains(result.Text, "already exists") {
		t.Fatalf("expected dedupe result without diff, got %#v", result)
	}

	result, err = tool.Execute(context.Background(), map[string]any{
		"path": "file.txt", "content": "c\n", "position": map[string]any{"type": "tail"}, "dry_run": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Diff == nil {
		t.Fatal("expected dry-run diff")
	}
	if result.Insert == nil || !result.Insert.DryRun || result.Insert.Position != "tail" {
		t.Fatalf("unexpected insert metadata: %#v", result.Insert)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "a\nb\n" {
		t.Fatalf("dry-run modified file: %q", got)
	}
}

func TestInsertToolLargeFileStreaming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	data := bytes.Repeat([]byte("a"), int(insertInMemoryLimit)+1)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewInsertTool(NewRegistry(dir, sandbox.NewNoneSandbox()))
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "large.txt", "content": "tail", "position": map[string]any{"type": "tail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Insert == nil || result.Insert.Offset != int64(len(data)) {
		t.Fatalf("unexpected metadata: %#v", result.Insert)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data)+len("\ntail") || !bytes.HasSuffix(got, []byte("\ntail")) {
		t.Fatalf("large file insertion incorrect: size=%d", len(got))
	}
}
func TestInsertToolRejectsMatchAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("a\n"), 0644)
	tool := NewInsertTool(NewRegistry(dir, sandbox.NewNoneSandbox()))

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "file.txt", "content": "x", "position": map[string]any{"type": "after_match", "match": "a"},
	})
	if err == nil || (!strings.Contains(err.Error(), "invalid position type") && !strings.Contains(err.Error(), "unsupported position field")) {
		t.Fatalf("expected match position rejection, got %v", err)
	}

	_, err = tool.Execute(context.Background(), map[string]any{
		"path": "file.txt", "content": "x", "position": map[string]any{"type": "before_line", "line": 3},
	})
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected line range error, got %v", err)
	}
}
