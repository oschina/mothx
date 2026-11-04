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

// Read-only sessions may share the worktree up to the profile ceiling, while a
// base-workspace write role requires exclusive access in both directions.
func TestACPWorkerPermitReadOnlyFanOutAndExclusiveWriter(t *testing.T) {
	runtime := &SessionRuntime{}
	first, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleInvestigate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleReview)
	if err != nil {
		t.Fatalf("second read-only worker: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleImplement); err == nil ||
		!strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("writer next to readers = %v", err)
	}
	first()
	second()

	writer, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleImplement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleInvestigate); err == nil ||
		!strings.Contains(err.Error(), "writer is active") {
		t.Fatalf("reader next to writer = %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleIntegrate); err == nil {
		t.Fatal("a second workspace writer was admitted")
	}
	writer()

	reader, err := runtime.acquireACPWorkerPermitForRole(2, SubagentRoleTest)
	if err != nil {
		t.Fatalf("reader after writer release: %v", err)
	}
	reader()
}

func TestACPWorkerPermitAllowsIsolatedWriterBesideReaders(t *testing.T) {
	runtime := &SessionRuntime{}
	reader, err := runtime.acquireACPWorkerPermitForRole(3, SubagentRoleInvestigate)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, true, false, nil)
	if err != nil {
		t.Fatalf("isolated writer beside reader: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleIntegrate, false, false, nil); err == nil ||
		!strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("base writer beside live workers = %v", err)
	}
	writer()
	reader()

	base, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleIntegrate, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, true, false, nil); err == nil ||
		!strings.Contains(err.Error(), "base-workspace writer is active") {
		t.Fatalf("isolated writer beside base writer = %v", err)
	}
	base()
}

// A profile that does not opt in keeps the exclusive base-workspace lane. This
// is the default for every existing settings.json profile.
func TestACPWorkerPermitBaseWriterStaysExclusiveByDefault(t *testing.T) {
	runtime := &SessionRuntime{}
	writer, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, false, []string{"internal/a"})
	if err != nil {
		t.Fatalf("first base writer: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, false, []string{"internal/b"}); err == nil ||
		!strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("second base writer without the opt-in = %v, want a refusal", err)
	}
	writer()
}

// The explicit opt-in admits parallel base-workspace writers, but only when
// their declared scopes cannot collide.
func TestACPWorkerPermitParallelBaseWritersAllowDisjointScopes(t *testing.T) {
	runtime := &SessionRuntime{}
	first, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/alpha"})
	if err != nil {
		t.Fatalf("first parallel base writer: %v", err)
	}
	second, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/beta"})
	if err != nil {
		t.Fatalf("second parallel base writer with a disjoint scope: %v", err)
	}
	// The ordinary ceiling still applies: the third writer fills it.
	third, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/gamma"})
	if err != nil {
		t.Fatalf("third parallel base writer filling the ceiling: %v", err)
	}
	if _, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/delta"}); err == nil ||
		!strings.Contains(err.Error(), "live external ACP workers") {
		t.Fatal("a fourth parallel base writer was admitted past the ceiling")
	}
	third()
	second()
	first()

	// A released slot is reusable and the scope bookkeeping must not leak.
	reused, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/alpha"})
	if err != nil {
		t.Fatalf("parallel base writer after release: %v", err)
	}
	reused()
}

// A statically visible overlap is refused even when the profile allows
// parallel base writers. The scope is the contract the worker was given, so
// this is the one part of the risk that can be checked without confining the
// worker's own tools.
func TestACPWorkerPermitParallelBaseWritersRefuseOverlappingScopes(t *testing.T) {
	cases := []struct {
		name  string
		left  []string
		right []string
	}{
		{name: "identical scope", left: []string{"internal/alpha"}, right: []string{"internal/alpha"}},
		{name: "nested scope", left: []string{"internal"}, right: []string{"internal/alpha"}},
		{name: "file inside scope", left: []string{"internal/alpha"}, right: []string{"internal/alpha/one.go"}},
		{name: "empty scope", left: nil, right: []string{"internal/alpha"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &SessionRuntime{}
			release, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, tc.left)
			if err != nil {
				t.Fatalf("first parallel base writer: %v", err)
			}
			defer release()
			if _, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, tc.right); err == nil ||
				!strings.Contains(err.Error(), "overlaps") {
				t.Fatalf("overlapping scope was admitted: %v", err)
			}
		})
	}
}

// The opt-in still serializes when the ceiling is one: raising concurrency is
// what enables parallel writers, not the flag alone.
func TestACPWorkerPermitParallelBaseWritersRespectCeilingOfOne(t *testing.T) {
	runtime := &SessionRuntime{}
	release, err := runtime.acquireACPWorkerPermitForWorkspace(1, SubagentRoleImplement, false, true, []string{"internal/alpha"})
	if err != nil {
		t.Fatalf("first base writer: %v", err)
	}
	defer release()
	if _, err := runtime.acquireACPWorkerPermitForWorkspace(1, SubagentRoleImplement, false, true, []string{"internal/beta"}); err == nil {
		t.Fatal("a second base writer was admitted with a ceiling of one")
	}
}

// A read-only worker may always join a parallel base writer: the reader does
// not modify the workspace.
func TestACPWorkerPermitAllowsReaderBesideParallelBaseWriter(t *testing.T) {
	runtime := &SessionRuntime{}
	writer, err := runtime.acquireACPWorkerPermitForWorkspace(3, SubagentRoleImplement, false, true, []string{"internal/alpha"})
	if err != nil {
		t.Fatalf("parallel base writer: %v", err)
	}
	defer writer()
	reader, err := runtime.acquireACPWorkerPermitForRole(3, SubagentRoleInvestigate)
	if err != nil {
		t.Fatalf("reader beside a parallel base writer: %v", err)
	}
	reader()
}
