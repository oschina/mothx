package agentruntime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/tools"
)

func TestBeginArtifactCollectionDisabledByDefault(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	runtime := &SessionRuntime{Registry: registry}
	collector, err := runtime.BeginArtifactCollection("run-disabled")
	if err != nil {
		t.Fatal(err)
	}
	if collector != nil {
		t.Fatalf("collector = %#v, want nil while artifacts are disabled", collector)
	}
	if _, ok := registry.Get("publish_artifact"); ok {
		t.Fatal("publish_artifact must not be registered by default")
	}
}

func TestSetArtifactEnabledRejectsClosedRuntime(t *testing.T) {
	runtime := &SessionRuntime{}
	if err := runtime.SetArtifactEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !runtime.ArtifactCapabilitySnapshot() {
		t.Fatal("artifact capability was not enabled")
	}
	runtime.Close()
	if err := runtime.SetArtifactEnabled(false); err == nil {
		t.Fatal("expected a closed runtime update to fail")
	}
	if !runtime.ArtifactCapabilitySnapshot() {
		t.Fatal("closed runtime capability changed")
	}
}

func TestArtifactCollectorObserverReceivesPersistedRecord(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Attachments: service, Registry: tools.NewRegistry(workDir, nil), ArtifactEnabled: true}
	collector, err := runtime.BeginArtifactCollection("run-observer")
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()

	var observed []SessionAttachment
	collector.SetObserver(func(record SessionAttachment) {
		observed = append(observed, record)
	})
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), []byte("observer content"), 0600); err != nil {
		t.Fatal(err)
	}
	tool, ok := runtime.Registry.Get("publish_artifact")
	if !ok {
		t.Fatal("publish_artifact was not registered")
	}
	if _, err := tool.Execute(t.Context(), map[string]any{"path": "report.txt"}); err != nil {
		t.Fatalf("publish artifact: %v", err)
	}
	items := collector.Artifacts()
	if len(items) != 1 || len(observed) != 1 {
		t.Fatalf("items = %#v, observed = %#v", items, observed)
	}
	record := observed[0]
	if record.ID != items[0].ID || record.Status != "generated" || record.Filename != "report.txt" || record.Bytes != items[0].Bytes || record.Kind != AttachmentFile {
		t.Fatalf("observed record = %#v, want persisted %#v", record, items[0])
	}
	// The observer must only run after durable persistence, so the record is
	// already readable through the Runtime-owned attachment service.
	stored, err := service.Get(t.Context(), mgr.GetHeader().ID, record.ID)
	if err != nil || stored.Status != "generated" {
		t.Fatalf("observed record is not persisted: %#v, %v", stored, err)
	}
}

func TestArtifactCollectorObserverPanicDoesNotAffectRegistration(t *testing.T) {
	root, workDir, mgr := inputTestSession(t)
	service, err := NewAttachmentService(root, DefaultAttachmentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: mgr.GetHeader().ID, WorkDir: workDir, Attachments: service, Registry: tools.NewRegistry(workDir, nil), ArtifactEnabled: true}
	collector, err := runtime.BeginArtifactCollection("run-panic")
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	collector.SetObserver(func(SessionAttachment) { panic("observer projection failed") })
	if err := os.WriteFile(filepath.Join(workDir, "panic.txt"), []byte("panic safe"), 0600); err != nil {
		t.Fatal(err)
	}
	record, err := collector.Register(t.Context(), "panic.txt", "", "auto")
	if err != nil {
		t.Fatalf("registration must survive an observer panic: %v", err)
	}
	if record.Status != "generated" || record.ID == "" {
		t.Fatalf("record = %#v", record)
	}
	// Removing the observer with nil is safe and later registrations work.
	collector.SetObserver(nil)
	if err := os.WriteFile(filepath.Join(workDir, "second.txt"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := collector.Register(t.Context(), "second.txt", "", "auto"); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	if items := collector.Artifacts(); len(items) != 2 {
		t.Fatalf("artifacts = %#v, want both registrations", items)
	}
	stored, err := service.Get(t.Context(), mgr.GetHeader().ID, record.ID)
	if err != nil || stored.Status != "generated" {
		t.Fatalf("panicking-observer artifact was not persisted: %#v, %v", stored, err)
	}
}

func TestArtifactCollectorObserverNilCollectorSafe(t *testing.T) {
	var collector *ArtifactCollector
	collector.SetObserver(func(SessionAttachment) { t.Fatal("nil collector must not invoke the observer") })
	collector.notifyObserver(SessionAttachment{ID: "ignored"})
}
