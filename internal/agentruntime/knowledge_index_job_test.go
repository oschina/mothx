package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// Scans must never run on the caller's goroutine: StartIndex returns while the
// job is still running, concurrent starts share one job, and progress moves
// through the documented phases until the snapshot commits.
func TestKnowledgeBaseStartIndexRunsInBackgroundWithProgress(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	for name, body := range map[string]string{
		"notes.md": "# Notes\n\nBackground indexing keeps transports responsive.",
		"guide.md": "# Guide\n\nProgress is polled periodically by hosts.",
		"extra.md": "# Extra\n\nConcurrent starts share a single job.",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base, err := session.CreateKnowledgeBase(context.Background(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Async notes", RootDir: source, PreprocessProfile: "documents",
		Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}

	job, err := service.StartIndex(context.Background(), base.ID, SourceACP)
	if err != nil {
		t.Fatal(err)
	}
	if !job.Progress().Running {
		t.Fatalf("StartIndex must return while the scan is running: %#v", job.Progress())
	}
	shared, err := service.StartIndex(context.Background(), base.ID, SourceACP)
	if err != nil {
		t.Fatal(err)
	}
	if shared != job {
		t.Fatalf("concurrent StartIndex must reuse the running job")
	}
	if _, running := service.IndexProgress(base.ID); !running {
		t.Fatalf("IndexProgress must report the running scan")
	}

	snapshot, err := job.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "completed" || snapshot.FileCount != 3 {
		t.Fatalf("snapshot = %#v, want completed with three files", snapshot)
	}
	progress := job.Progress()
	if progress.Running {
		t.Fatalf("finished job must not report running: %#v", progress)
	}
	if progress.FilesTotal != 3 || progress.FilesDone != 3 {
		t.Fatalf("progress = %#v, want all files counted", progress)
	}
	if progress.Phase != KnowledgeIndexPhaseCommitting {
		t.Fatalf("progress phase = %q, want %q", progress.Phase, KnowledgeIndexPhaseCommitting)
	}
	// The concurrent start above was coalesced into exactly one follow-up pass;
	// wait for it before asserting the idle state.
	follow := waitForFollowUpJob(t, service, base.ID, job)
	if _, err := follow.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, running := service.IndexProgress(base.ID); running {
		t.Fatalf("IndexProgress must stop reporting once the scan finished")
	}

	// A start after completion launches a fresh job that reuses the snapshot
	// for an unchanged directory.
	again, err := service.StartIndex(context.Background(), base.ID, SourceACP)
	if err != nil {
		t.Fatal(err)
	}
	if again == job {
		t.Fatalf("a start after completion must create a new job")
	}
	reused, err := again.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != snapshot.ID {
		t.Fatalf("unchanged directory must reuse the active snapshot: %#v", reused)
	}
}

func TestKnowledgeBaseStartIndexRejectsDisabledBaseSynchronously(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	base, err := session.CreateKnowledgeBase(context.Background(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Disabled", RootDir: source, PreprocessProfile: "documents",
		Mode: "yolo", Schedule: "manual", Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartIndex(context.Background(), base.ID, SourceACP); err == nil {
		t.Fatalf("disabled base must be rejected before any job starts")
	}
	if _, ok := service.IndexJob(base.ID); ok {
		t.Fatalf("rejected start must not register a job")
	}
}

// waitForFollowUpJob waits until a scan different from previous is registered for
// the base, which is how the coalesced follow-up pass becomes observable.
func waitForFollowUpJob(t *testing.T, service *KnowledgeBaseService, baseID string, previous *KnowledgeIndexJob) *KnowledgeIndexJob {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		service.indexJobsMu.Lock()
		current := service.indexJobs[baseID]
		service.indexJobsMu.Unlock()
		if current != nil && current != previous {
			return current
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("coalesced follow-up job was not started")
	return nil
}

// TestKnowledgeIndexCoalescesTriggersWhileScanning pins the coalesce contract: a
// trigger that arrives while a scan is running is not dropped and does not start
// a parallel scan; it is merged into exactly one follow-up pass, and the most
// recent trigger source wins.
func TestKnowledgeIndexCoalescesTriggersWhileScanning(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a.md"), []byte("# A\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Coalesce", RootDir: source, PreprocessProfile: "documents",
		Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an in-flight scan without starting a real goroutine.
	inflight := newKnowledgeIndexJob()
	service.indexJobs = map[string]*KnowledgeIndexJob{base.ID: inflight}

	job, err := service.StartIndex(context.Background(), base.ID, SourceCron)
	if err != nil {
		t.Fatal(err)
	}
	if job != inflight {
		t.Fatalf("trigger during a scan must reuse the running job")
	}
	if _, err := service.StartIndex(context.Background(), base.ID, SourceWebUI); err != nil {
		t.Fatal(err)
	}
	service.indexJobsMu.Lock()
	pending := service.indexPending[base.ID]
	service.indexJobsMu.Unlock()
	if pending != SourceWebUI {
		t.Fatalf("pending source = %q, want the most recent trigger %q", pending, SourceWebUI)
	}

	// Finishing the in-flight scan must start exactly one coalesced follow-up.
	inflight.finish(session.KnowledgeSnapshot{}, nil)
	service.afterIndexJob(base.ID)
	follow := waitForFollowUpJob(t, service, base.ID, inflight)
	service.indexJobsMu.Lock()
	_, stillPending := service.indexPending[base.ID]
	service.indexJobsMu.Unlock()
	if stillPending {
		t.Fatalf("pending trigger must be consumed by the follow-up run")
	}
	if _, err := follow.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
