package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
)

func executionProjectionTestServer(t *testing.T, cwd, dir string, out *bytes.Buffer) *server {
	t.Helper()
	model := &provider.Model{ID: "projection-model", Name: "Projection Model"}
	p := provider.NewMockProvider("projection-provider", []*provider.Model{model}, nil)
	s := testSessionServer(cwd, dir, out)
	s.p = p
	s.providerName = "projection-provider"
	s.m = model
	s.providers = map[string]provider.Provider{"projection-provider": p}
	return s
}

func seedOrphanedRun(t *testing.T, dir, sessionID, runID string) {
	t.Helper()
	if err := (agentruntime.RunStore{SessionDir: dir}).Create(agentruntime.DurableRun{
		ID: runID, SessionID: sessionID, Source: "tui", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed orphan run: %v", err)
	}
}

func loadResponseResult(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	for _, message := range jsonLines(t, out) {
		if message["id"] != float64(1) {
			continue
		}
		if result, ok := message["result"].(map[string]any); ok {
			return result
		}
		t.Fatalf("load response is an error: %#v", message)
	}
	t.Fatal("missing load response")
	return nil
}

// TestLoadSessionProjectsOrphanedExecutionState pins the additive
// sessionExecutionState projection: loading a session whose previous owner
// crashed must expose the canonical orphaned state so Desktop can surface it
// before the next prompt admission.
func TestLoadSessionProjectsOrphanedExecutionState(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "execution-orphan", 1)
	seedOrphanedRun(t, dir, "execution-orphan", "execution-orphan-run")

	var out bytes.Buffer
	s := executionProjectionTestServer(t, cwd, dir, &out)
	s.handleLoadSession(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"execution-orphan","cwd":%q}`, cwd))})

	result := loadResponseResult(t, &out)
	execution, ok := result["execution"].(map[string]any)
	if !ok {
		t.Fatalf("load result has no execution projection: %#v", result)
	}
	if execution["state"] != "orphaned" {
		t.Fatalf("execution state = %v, want orphaned", execution["state"])
	}
	activeRun, ok := execution["activeRun"].(map[string]any)
	if !ok || activeRun["runId"] != "execution-orphan-run" {
		t.Fatalf("execution activeRun = %#v", execution["activeRun"])
	}
	if _, err := s.closeSessionRuntime("execution-orphan"); err != nil {
		t.Fatal(err)
	}
}

// TestLoadSessionOmitsExecutionWhenIdle keeps the projection additive: an
// idle session carries no execution key at all.
func TestLoadSessionOmitsExecutionWhenIdle(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "execution-idle", 1)

	var out bytes.Buffer
	s := executionProjectionTestServer(t, cwd, dir, &out)
	s.handleLoadSession(rpcRequest{ID: json.RawMessage("1"), Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"execution-idle","cwd":%q}`, cwd))})

	result := loadResponseResult(t, &out)
	if _, present := result["execution"]; present {
		t.Fatalf("idle session must not carry an execution projection: %#v", result["execution"])
	}
	if _, err := s.closeSessionRuntime("execution-idle"); err != nil {
		t.Fatal(err)
	}
}

// TestListSessionsProjectsExecutionMeta covers the bounded per-page list
// projection: only non-idle sessions carry _meta.execution.
func TestListSessionsProjectsExecutionMeta(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "list-orphan", 1)
	newTestSession(t, cwd, dir, "list-idle", 1)
	seedOrphanedRun(t, dir, "list-orphan", "list-orphan-run")

	var out bytes.Buffer
	s := executionProjectionTestServer(t, cwd, dir, &out)
	s.handleListSessions(rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, cwd))})

	var result map[string]any
	for _, message := range jsonLines(t, &out) {
		if message["id"] == float64(2) {
			result, _ = message["result"].(map[string]any)
		}
	}
	if result == nil {
		t.Fatal("missing list response")
	}
	sessions, _ := result["sessions"].([]any)
	seen := map[string]map[string]any{}
	for _, entry := range sessions {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := item["sessionId"].(string)
		meta, _ := item["_meta"].(map[string]any)
		seen[id] = meta
	}
	orphanMeta, ok := seen["list-orphan"]
	if !ok {
		t.Fatalf("list-orphan missing from page: %#v", seen)
	}
	execution, ok := orphanMeta["execution"].(map[string]any)
	if !ok || execution["state"] != "orphaned" {
		t.Fatalf("list-orphan execution meta = %#v", orphanMeta["execution"])
	}
	idleMeta, ok := seen["list-idle"]
	if !ok {
		t.Fatalf("list-idle missing from page: %#v", seen)
	}
	if _, present := idleMeta["execution"]; present {
		t.Fatalf("idle session must not carry execution meta: %#v", idleMeta["execution"])
	}
}
