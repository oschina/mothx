package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestSessionRuntimeEventStoreWriteStatsRequiresBoundSession(t *testing.T) {
	runtime := &SessionRuntime{}
	if _, err := runtime.EventStoreWriteStats(); err == nil {
		t.Fatal("unbound runtime exposed EventStore stats")
	}
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.InitWithID("runtime-event-stats"); err != nil {
		t.Fatal(err)
	}
	runtime.Manager = manager
	if _, err := runtime.EventStoreWriteStats(); err != nil {
		t.Fatalf("bound runtime EventStore stats: %v", err)
	}
}
