package agentruntime

import (
	"strings"
	"testing"
	"time"

	agentcore "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
)

func TestLocalChildRunRecorderPersistsSharedHistoryShape(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("session-child-history"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "session-child-history"}
	now := time.Now().UTC()
	recorder := localChildRunRecorder{runtime: runtime}
	if err := recorder.RecordChildRun(agentcore.ChildRunObservation{
		ChildID: "local-child", SessionID: "session-child-history", ParentRunID: "run-1",
		Backend: "local", WorkDir: workDir, Status: "success",
		ContractJSON: "{}", TranscriptJSON: `[{"Content":"finished"}]`,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RecordChildRun: %v", err)
	}
	record, found, err := session.LoadChildRun(sessionDir, "session-child-history", "local-child")
	if err != nil || !found {
		t.Fatalf("LoadChildRun: found=%t err=%v", found, err)
	}
	if record.Backend != "local" || record.ParentRunID != "run-1" ||
		record.Status != "success" || record.WorkDir != workDir ||
		!strings.Contains(record.TranscriptJSON, "finished") {
		t.Fatalf("durable local child history = %#v", record)
	}
}
