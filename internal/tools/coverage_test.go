package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
)

// TestToolMetadata tests PromptSnippet, PromptGuidelines, Description for all tools.
func TestToolMetadata(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	for _, tool := range r.All() {
		name := tool.Name()
		if name == "" {
			t.Errorf("tool %T has empty name", tool)
		}
		if tool.Description() == "" {
			t.Errorf("tool %s has empty description", name)
		}
		if tool.Parameters() == nil {
			t.Errorf("tool %s has nil parameters", name)
		}
		// PromptSnippet and PromptGuidelines - just call them
		_ = tool.PromptSnippet()
		_ = tool.PromptGuidelines()
	}
}

// TestRegistryConfig tests NewRegistryWithConfig and RegisterFiltered.
func TestRegistryConfig(t *testing.T) {
	sb := sandbox.NewNoneSandbox()

	// With empty filter = all defaults
	r := NewRegistryWithConfig(RegistryConfig{
		WorkDir: "/tmp",
		Sandbox: sb,
	})
	if len(r.All()) == 0 {
		t.Error("expected default tools to be registered")
	}

	// With filter
	r2 := NewRegistryWithConfig(RegistryConfig{
		WorkDir:    "/tmp",
		Sandbox:    sb,
		ToolFilter: []string{"read", "write"},
	})
	if len(r2.All()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(r2.All()))
	}
	if _, ok := r2.Get("read"); !ok {
		t.Error("expected 'read' tool")
	}
	if _, ok := r2.Get("write"); !ok {
		t.Error("expected 'write' tool")
	}
	if _, ok := r2.Get("bash"); ok {
		t.Error("did not expect 'bash' tool in filtered registry")
	}
}

// TestRegistryJobManager tests per-registry JobManager.
func TestRegistryJobManager(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r1 := NewRegistry("/tmp", sb)
	r2 := NewRegistry("/tmp", sb)

	jm1 := r1.JobManager()
	jm2 := r2.JobManager()

	if jm1 == nil || jm2 == nil {
		t.Fatal("expected non-nil JobManagers")
	}
	if jm1 == jm2 {
		t.Error("expected different JobManager instances per registry")
	}
}

// TestRegistryModeTools tests ModeTools filtering.
func TestRegistryModeTools(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	planTools := r.ModeTools("plan")
	allTools := r.ModeTools("agent")

	if len(planTools) >= len(allTools) {
		t.Errorf("plan should have fewer tools than agent: plan=%d agent=%d", len(planTools), len(allTools))
	}

	// Plan mode should only have read-only tools
	planNames := make(map[string]bool)
	for _, td := range planTools {
		planNames[td.Name] = true
	}
	for _, name := range []string{"read", "grep", "find", "ls", "plan"} {
		if !planNames[name] {
			t.Errorf("plan mode missing tool: %s", name)
		}
	}
	if planNames["write"] {
		t.Error("plan mode should not have write tool")
	}
	if planNames["bash"] {
		t.Error("plan mode should not have bash tool")
	}
}

// TestToolSnippets tests ToolSnippets and ToolGuidelines.
func TestToolSnippets(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	snippets := r.ToolSnippets([]string{"read", "write", "bash"})
	if len(snippets) == 0 {
		t.Error("expected non-empty snippets")
	}

	guidelines := r.ToolGuidelines([]string{"read", "write", "bash"})
	// Guidelines may be nil if tools don't define them
	_ = guidelines
}

// TestRegistryResolvePath tests path resolution.
func TestRegistryResolvePath(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/home/user/project", sb)

	// Relative path
	resolved, err := r.ResolvePath("src/main.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/home/user/project/src/main.go" {
		t.Errorf("expected /home/user/project/src/main.go, got %s", resolved)
	}

	// Absolute path within workdir
	resolved, err = r.ResolvePath("/home/user/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != "/home/user/project" {
		t.Errorf("expected /home/user/project, got %s", resolved)
	}

	// Path escape should fail
	_, err = r.ResolvePath("../../etc/passwd")
	if err == nil {
		t.Error("expected error for path escape")
	}

	// Sibling directory with same prefix should fail.
	_, err = r.ResolvePath("/home/user/project2/file.txt")
	if err == nil {
		t.Error("expected error for sibling prefix path escape")
	}

	// Tilde expansion - may fail if home is outside workdir
	_, err = r.ResolvePath("~")
	// This is expected to fail if home dir is outside workdir
	_ = err
}

// TestSetSandbox tests SetSandbox.
func TestSetSandbox(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)

	newSb := sandbox.NewNoneSandbox()
	r.SetSandbox(newSb)

	if r.GetSandbox() != newSb {
		t.Error("expected updated sandbox")
	}
}

// TestLimitedBuffer_Truncate verifies that limitedBuffer truncates output at maxSize.
func TestLimitedBuffer_Truncate(t *testing.T) {
	lb := newLimitedBuffer(100)

	// Write less than max — no truncation
	lb.Write([]byte("hello"))
	out := lb.Bytes()
	if string(out) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(out))
	}

	// Write more than max — should truncate
	lb2 := newLimitedBuffer(100)
	bigData := make([]byte, 200)
	for i := range bigData {
		bigData[i] = 'A'
	}
	lb2.Write(bigData)
	out2 := lb2.Bytes()
	if len(out2) != 100+len("\n... (truncated 100 bytes)") {
		t.Errorf("expected truncated output of length %d, got %d: %q",
			100+len("\n... (truncated 100 bytes)"), len(out2), string(out2))
	}
	if !strings.Contains(string(out2), "truncated") {
		t.Error("expected truncation suffix")
	}
}

// TestJobManager_GCStaleJobs verifies stale finished jobs are cleaned up.
func TestJobManager_GCStaleJobs(t *testing.T) {
	jm := NewJobManager()

	// Simulate jobs by directly inserting them.
	// Running job should survive GC.
	runningJob := &BackgroundJob{ID: 1, Command: "running", StartTime: time.Now().Add(-1 * time.Hour)}
	jm.jobs[1] = runningJob

	// Finished job that's young — should survive GC.
	youngDone := &BackgroundJob{ID: 2, Command: "young-done", StartTime: time.Now(), done: true}
	jm.jobs[2] = youngDone

	// Finished job that's stale (finished >30min ago) — should be cleaned.
	staleDone := &BackgroundJob{ID: 3, Command: "stale-done", StartTime: time.Now().Add(-1 * time.Hour), done: true}
	jm.jobs[3] = staleDone

	// Trigger GC via AddJob (we need a real exec.Cmd for AddJob, so call gcStaleJobsLocked directly).
	jm.mu.Lock()
	jm.lastGC = time.Time{} // force GC
	jm.gcStaleJobsLocked()
	jm.mu.Unlock()

	if _, ok := jm.jobs[1]; !ok {
		t.Error("running job should not be removed")
	}
	if _, ok := jm.jobs[2]; !ok {
		t.Error("young done job should not be removed")
	}
	if _, ok := jm.jobs[3]; ok {
		t.Error("stale done job should have been removed")
	}
}

// TestJobManager_GCSkipIfRecent verifies GC is skipped if last GC was recent.
func TestJobManager_GCSkipIfRecent(t *testing.T) {
	jm := NewJobManager()

	staleDone := &BackgroundJob{ID: 1, Command: "stale", StartTime: time.Now().Add(-1 * time.Hour), done: true}
	jm.jobs[1] = staleDone

	jm.lastGC = time.Now() // recent GC — should skip

	jm.mu.Lock()
	jm.gcStaleJobsLocked()
	jm.mu.Unlock()

	if _, ok := jm.jobs[1]; !ok {
		t.Error("stale job should NOT be removed when GC was recent")
	}
}
