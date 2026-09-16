package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/session"
)

func TestRunKnowledgeBaseCronJobRoutesNamespacedJobsOnly(t *testing.T) {
	sessionDir := t.TempDir()
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}

	handled, response, err := RunKnowledgeBaseCronJob(t.Context(), service, "plain-cron-job")
	if handled || response != "" || err != nil {
		t.Fatalf("foreign job = (%v, %q, %v), want fallthrough to the scheduler's own path", handled, response, err)
	}

	handled, _, err = RunKnowledgeBaseCronJob(t.Context(), service, KnowledgeBaseCronJobID("missing"))
	if !handled || err == nil {
		t.Fatalf("missing base = (%v, %v), want a handled failure", handled, err)
	}

	handled, _, err = RunKnowledgeBaseCronJob(t.Context(), nil, KnowledgeBaseCronJobID("any"))
	if !handled || err == nil {
		t.Fatalf("nil service = (%v, %v), want a handled failure", handled, err)
	}
}

func TestRunKnowledgeBaseCronJobIndexesThroughCanonicalBackgroundPath(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "guide.md"), []byte("# Guide\n\nScheduled scans reuse the canonical index path.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Scheduled", RootDir: source, PreprocessProfile: "documents", Schedule: "daily", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}

	jobID := KnowledgeBaseCronJobID(base.ID)
	if id, ok := KnowledgeBaseIDFromCronJobID(jobID); !ok || id != base.ID {
		t.Fatalf("cron job identity = (%q, %v)", id, ok)
	}
	handled, response, err := RunKnowledgeBaseCronJob(t.Context(), service, jobID)
	if !handled || err != nil {
		t.Fatalf("cron reindex = (%v, %v)", handled, err)
	}
	if !strings.Contains(response, "indexed knowledge base "+base.ID) || !strings.Contains(response, "1 files") {
		t.Fatalf("cron response = %q", response)
	}

	reloaded, err := session.GetKnowledgeBase(t.Context(), sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(reloaded.ActiveSnapshotID) == "" {
		t.Fatalf("cron reindex left no active snapshot: %#v", reloaded)
	}

	// The same registry must expose the finished job for progress polling.
	progress, running := service.IndexProgress(base.ID)
	if running || progress.Running {
		t.Fatalf("finished cron job still reports running: %#v", progress)
	}
}

func TestRunKnowledgeBaseCronJobHonorsContextCancellation(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Cancelled", RootDir: source, PreprocessProfile: "documents", Schedule: "daily", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	handled, _, err := RunKnowledgeBaseCronJob(ctx, service, KnowledgeBaseCronJobID(base.ID))
	if !handled || err == nil {
		t.Fatalf("cancelled cron reindex = (%v, %v), want a handled failure", handled, err)
	}
}
