package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestRenderQueuedPromptsEmptyQueue(t *testing.T) {
	a := &App{translator: i18n.New(i18n.LanguageEN)}
	if got := a.renderQueuedPrompts(80); got != "" {
		t.Fatalf("empty queue rendered %q, want empty", got)
	}
	var nilApp *App
	if got := nilApp.renderQueuedPrompts(80); got != "" {
		t.Fatalf("nil app rendered %q, want empty", got)
	}
}

func TestRenderQueuedPromptsShowsCountAndPreviews(t *testing.T) {
	a := &App{translator: i18n.New(i18n.LanguageEN)}
	a.queuedPrompts = []queuedPrompt{
		{text: "fix the bug"},
		{text: "then run\ntests"},
	}
	out := a.renderQueuedPrompts(80)
	if !strings.Contains(out, "2 queued") {
		t.Fatalf("header missing count: %q", out)
	}
	if !strings.Contains(out, "1. fix the bug") {
		t.Fatalf("first preview missing: %q", out)
	}
	if !strings.Contains(out, "2. then run tests") {
		t.Fatalf("multi-line preview was not flattened: %q", out)
	}

	zh := &App{translator: i18n.New(i18n.LanguageZH)}
	zh.queuedPrompts = a.queuedPrompts
	if got := zh.renderQueuedPrompts(80); !strings.Contains(got, "已排队 2 条") {
		t.Fatalf("zh header = %q", got)
	}
}

func TestRenderQueuedPromptsCapsVisibleItems(t *testing.T) {
	a := &App{translator: i18n.New(i18n.LanguageEN)}
	for i := 0; i < 5; i++ {
		a.queuedPrompts = append(a.queuedPrompts, queuedPrompt{text: "prompt"})
	}
	out := a.renderQueuedPrompts(80)
	if !strings.Contains(out, "5 queued") {
		t.Fatalf("header must carry the true total: %q", out)
	}
	if !strings.Contains(out, "3. prompt") || strings.Contains(out, "4. prompt") {
		t.Fatalf("visible cap not applied: %q", out)
	}
	if !strings.Contains(out, "+2 …") {
		t.Fatalf("overflow line missing: %q", out)
	}
}

func TestQueuedPromptPreviewBoundsWidth(t *testing.T) {
	long := strings.Repeat("宽", 60)
	got := queuedPromptPreview(long, 20)
	if xansi.StringWidth(got) > 20 {
		t.Fatalf("preview width = %d, want <= 20 (%q)", xansi.StringWidth(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated preview must end with an ellipsis: %q", got)
	}
	if flat := queuedPromptPreview("  a \n\t b  ", 80); flat != "a b" {
		t.Fatalf("flattened preview = %q, want %q", flat, "a b")
	}
	if empty := queuedPromptPreview("   \n ", 80); empty != "…" {
		t.Fatalf("blank preview = %q, want ellipsis", empty)
	}
	// A non-positive width disables truncation instead of panicking.
	if got := queuedPromptPreview("short text", 0); got != "short text" {
		t.Fatalf("zero-width preview = %q", got)
	}
}
