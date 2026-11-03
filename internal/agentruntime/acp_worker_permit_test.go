package agentruntime

import (
	"strings"
	"testing"
)

// TestACPWorkerPermitHonorsCeiling proves the permit is a real counter over the
// Runtime-resolved ceiling, not a boolean that ignores the configured value.
// The configured value cannot exceed the effective ceiling in this release, so
// the test drives the permit owner directly with both limits.
func TestACPWorkerPermitHonorsCeiling(t *testing.T) {
	runtime := &SessionRuntime{}

	release, err := runtime.acquireACPWorkerPermit(1)
	if err != nil {
		t.Fatalf("first permit was refused: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermit(1); err == nil {
		t.Fatal("a second worker was admitted past a ceiling of one")
	} else if !strings.Contains(err.Error(), "already running for this session") {
		t.Fatalf("unexpected single-worker error: %v", err)
	}
	release()
	if _, err := runtime.acquireACPWorkerPermit(1); err != nil {
		t.Fatalf("released permit did not become available again: %v", err)
	}
}

// TestACPWorkerPermitCountsBeyondOne guards the counter itself: a raised ceiling
// must admit exactly that many workers, so the field is genuinely consumed
// rather than decorative.
func TestACPWorkerPermitCountsBeyondOne(t *testing.T) {
	runtime := &SessionRuntime{}

	first, err := runtime.acquireACPWorkerPermit(2)
	if err != nil {
		t.Fatalf("first permit was refused: %v", err)
	}
	second, err := runtime.acquireACPWorkerPermit(2)
	if err != nil {
		t.Fatalf("second permit was refused below the ceiling: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermit(2); err == nil {
		t.Fatal("a third worker was admitted past a ceiling of two")
	}
	second()
	if _, err := runtime.acquireACPWorkerPermit(2); err != nil {
		t.Fatalf("released slot was not reusable: %v", err)
	}
	first()
}

// TestACPWorkerPermitReleaseIsIdempotent protects the counter from being driven
// negative by a double release on a terminal path.
func TestACPWorkerPermitReleaseIsIdempotent(t *testing.T) {
	runtime := &SessionRuntime{}
	release, err := runtime.acquireACPWorkerPermit(1)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if _, err := runtime.acquireACPWorkerPermit(1); err != nil {
		t.Fatalf("permit was not reusable after a double release: %v", err)
	}
	runtime.acpWorkerMu.Lock()
	live := runtime.acpWorkersLive
	runtime.acpWorkerMu.Unlock()
	if live != 1 {
		t.Fatalf("live worker count = %d, want 1", live)
	}
}
