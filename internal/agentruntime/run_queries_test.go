package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestAnnotateDurableRunErrorOnlyTerminalizesEmptyErrors(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	now := time.Now()
	if err := store.Create(DurableRun{ID: "run-annotate", SessionID: "session-annotate", WorkDir: t.TempDir(), Source: "responses_background", Mode: "yolo", Status: "running", StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	// Active runs never accept an annotation; the lifecycle still owns them.
	applied, err := AnnotateDurableRunError(context.Background(), sessionDir, "run-annotate", "abandoned after interrupted tool execution")
	if err != nil || applied {
		t.Fatalf("annotate active run: applied=%v err=%v", applied, err)
	}

	if err := store.Finish("run-annotate", RunStateFailed, ""); err != nil {
		t.Fatal(err)
	}
	applied, err = AnnotateDurableRunError(context.Background(), sessionDir, "run-annotate", "abandoned after interrupted tool execution")
	if err != nil || !applied {
		t.Fatalf("annotate terminal run: applied=%v err=%v", applied, err)
	}
	run, err := session.GetSessionRun(sessionDir, "run-annotate")
	if err != nil || run == nil {
		t.Fatalf("load annotated run: %v", err)
	}
	if run.Status != "failed" || run.Error != "abandoned after interrupted tool execution" {
		t.Fatalf("annotated run = status %q error %q", run.Status, run.Error)
	}

	// A finalizer that already recorded a reason stays authoritative.
	applied, err = AnnotateDurableRunError(context.Background(), sessionDir, "run-annotate", "later reason")
	if err != nil || applied {
		t.Fatalf("second annotation: applied=%v err=%v", applied, err)
	}
	run, err = session.GetSessionRun(sessionDir, "run-annotate")
	if err != nil || run == nil {
		t.Fatalf("reload annotated run: %v", err)
	}
	if run.Error != "abandoned after interrupted tool execution" {
		t.Fatalf("annotation overwrote existing error: %q", run.Error)
	}

	applied, err = AnnotateDurableRunError(context.Background(), sessionDir, "missing-run", "reason")
	if err != nil || applied {
		t.Fatalf("missing run annotation: applied=%v err=%v", applied, err)
	}
}

func TestListLatestDurableRunsBySessionsProjectsNewestRunPerSession(t *testing.T) {
	sessionDir := t.TempDir()
	store := RunStore{SessionDir: sessionDir}
	now := time.Now()
	if err := store.Create(DurableRun{ID: "run-page-a1", SessionID: "session-page-a", WorkDir: t.TempDir(), Source: "acp", Mode: "yolo", Status: "running", StartedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish("run-page-a1", RunStateCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(DurableRun{ID: "run-page-a2", SessionID: "session-page-a", WorkDir: t.TempDir(), Source: "acp", Mode: "yolo", Status: "running", StartedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(DurableRun{ID: "run-page-b", SessionID: "session-page-b", WorkDir: t.TempDir(), Source: "acp", Mode: "yolo", Status: "running", StartedAt: now}); err != nil {
		t.Fatal(err)
	}

	latest, err := ListLatestDurableRunsBySessions(context.Background(), sessionDir, []string{"session-page-a", "session-page-b", "session-page-missing"})
	if err != nil {
		t.Fatalf("ListLatestDurableRunsBySessions: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest = %#v, want two sessions", latest)
	}
	if latest["session-page-a"].ID != "run-page-a2" || latest["session-page-a"].Status != "running" {
		t.Fatalf("session-a projection = %#v, want the newest run", latest["session-page-a"])
	}
	if _, ok := latest["session-page-missing"]; ok {
		t.Fatal("sessions without runs must be absent from the projection")
	}
	if latest["session-page-b"].ID != "run-page-b" {
		t.Fatalf("session-b projection = %#v, want run-page-b", latest["session-page-b"])
	}

	// The active-run boundary stays the authority for the active marker: the
	// finished older run of session-a is not active while its newest run is.
	active, err := GetActiveDurableRun(context.Background(), sessionDir, "session-page-a")
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.ID != "run-page-a2" {
		t.Fatalf("active run = %#v, %v, want run-page-a2", active, err)
	}

	empty, err := ListLatestDurableRunsBySessions(context.Background(), sessionDir, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input = %#v, %v, want an empty projection", empty, err)
	}
}
