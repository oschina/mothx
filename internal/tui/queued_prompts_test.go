package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/agentruntime"
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

// Only a submission the coordinator holds behind another execution is
// "queued". The head-of-line submission keeps its projection row until the
// admitted hand-off, but it is starting (admitting/running), so a single prompt
// must never flash the queue indicator.
func TestRenderQueuedPromptsOmitsHeadOfLineSubmissions(t *testing.T) {
	// Admission never completes on its own, so this handle stays "admitting".
	admitting := agentruntime.NewSubmissionCoordinator()
	defer admitting.Close()
	admittingHandle, err := admitting.Submit(context.Background(), "admitting", agentruntime.SubmitPolicy{QueueBehindLocal: true},
		func(ctx context.Context) (func(), error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("submit admitting: %v", err)
	}

	// This runner is admitted at once and then blocks, so later submissions
	// really queue behind it.
	running := agentruntime.NewSubmissionCoordinator()
	defer running.Close()
	entered := make(chan struct{})
	runningHandle, err := running.Submit(context.Background(), "running", agentruntime.SubmitPolicy{QueueBehindLocal: true},
		func(context.Context) (func(), error) { return func() {}, nil },
		func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if err != nil {
		t.Fatalf("submit running: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner was not started")
	}
	queuedHandle, err := running.Submit(context.Background(), "queued", agentruntime.SubmitPolicy{QueueBehindLocal: true},
		func(context.Context) (func(), error) { return func() {}, nil },
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("submit queued: %v", err)
	}
	wantStates := map[*agentruntime.ExecutionHandle]agentruntime.SubmissionState{
		admittingHandle: agentruntime.SubmissionAdmitting,
		runningHandle:   agentruntime.SubmissionRunning,
		queuedHandle:    agentruntime.SubmissionQueued,
	}
	for handle, want := range wantStates {
		if got := handle.State(); got != want {
			t.Fatalf("handle %s state = %q, want %q", handle.RunID, got, want)
		}
	}

	a := &App{translator: i18n.New(i18n.LanguageEN)}
	// Rows without a coordinator handle, and nil rows, are never "queued" either.
	a.pendingSubmissions = []*tuiSubmission{nil, {text: "orphan"}}
	a.pendingSubmissions = append(a.pendingSubmissions,
		&tuiSubmission{text: "admitting prompt", handle: admittingHandle},
		&tuiSubmission{text: "running prompt", handle: runningHandle},
	)
	if got := a.renderQueuedPrompts(80); got != "" {
		t.Fatalf("head-of-line submissions rendered as queued: %q", stripANSI(got))
	}

	a.pendingSubmissions = append(a.pendingSubmissions, &tuiSubmission{text: "queued prompt", handle: queuedHandle})
	got := stripANSI(a.renderQueuedPrompts(80))
	if !strings.Contains(got, "1 queued") || !strings.Contains(got, "1. queued prompt") {
		t.Fatalf("queued submission not rendered: %q", got)
	}
	for _, unexpected := range []string{"admitting prompt", "running prompt", "orphan", "2."} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("queue indicator lists %q: %q", unexpected, got)
		}
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
