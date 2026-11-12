package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// callWithin bounds one ACP request so a wedged child process fails the test
// instead of hanging until the package timeout. The pending call goroutine is
// released when the client closes at the end of the test.
func callWithin(t *testing.T, client *acpClient, method string, params any) json.RawMessage {
	t.Helper()
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		raw, err := client.call(method, params)
		done <- result{raw: raw, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("%s: %v", method, res.err)
		}
		return res.raw
	case <-time.After(20 * time.Second):
		t.Fatalf("%s timed out", method)
		return nil
	}
}

// TestACPProcessManagementContracts drives the real `mothx acp` process with
// the same methods and payloads the Native panels use. It is a protocol smoke
// test, not a UI acceptance run: it proves the wire shapes and capability
// handshake against the shared Runtime, using an isolated MOTHX_DIR.
func TestACPProcessManagementContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("process-boundary smoke test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	binary := resolveMothxBinary()
	if binary == "" {
		t.Skip("no mothx binary available")
	}
	dir := t.TempDir()
	t.Setenv("MOTHX_DIR", dir)

	client := newACPClient(func(incoming) {}, nil, nil)
	if err := client.start(binary, dir, os.Environ()); err != nil {
		t.Fatalf("start %s: %v", binary, err)
	}
	defer client.close()

	raw := callWithin(t, client, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo":      map[string]any{"name": "mothx-native-test"},
	})
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatalf("initialize response: %v", err)
	}
	features := parseFeatures(init)
	for _, feature := range []string{"sessionListAll", "projects", "manageEnv", "manageStats", "manageDeliveries", "manageACPProfiles", "worktrees"} {
		if !features[feature] {
			t.Fatalf("real runtime did not advertise %q: %#v", feature, features)
		}
	}

	// Env: get must be value-free; patch set/unset round-trips through env.json.
	raw = callWithin(t, client, "mothx/manage/env/get", map[string]any{})
	var env envView
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("env/get: %v", err)
	}
	raw = callWithin(t, client, "mothx/manage/env/patch", map[string]any{
		"set": []map[string]string{{"name": "MOTHX_NATIVE_SMOKE", "value": "1"}},
	})
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("env/patch set: %v", err)
	}
	found := false
	for _, variable := range env.Variables {
		if variable.Name == "MOTHX_NATIVE_SMOKE" {
			found = variable.ValueConfigured
		}
	}
	if !found {
		t.Fatalf("env/patch set did not publish the configured flag: %+v", env)
	}
	raw = callWithin(t, client, "mothx/manage/env/patch", map[string]any{
		"unset": []string{"MOTHX_NATIVE_SMOKE"},
	})
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("env/patch unset: %v", err)
	}
	for _, variable := range env.Variables {
		if variable.Name == "MOTHX_NATIVE_SMOKE" {
			t.Fatalf("env/patch unset did not remove the variable: %+v", env)
		}
	}
	if _, err := os.Stat(dir + "/env.json"); err != nil {
		t.Fatalf("env.json was not written inside MOTHX_DIR: %v", err)
	}

	// Stats: an empty isolated database still projects the documented shape.
	raw = callWithin(t, client, "mothx/manage/stats/summary", map[string]any{})
	var summary statsSummaryView
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("stats/summary: %v", err)
	}
	if summary.Sessions < 0 || summary.Runs < 0 {
		t.Fatalf("stats summary = %+v", summary)
	}
	raw = callWithin(t, client, "mothx/manage/stats/timeseries", buildStatsTimeseriesRequest(7, "day", time.Now()))
	var series statsTimeseriesView
	if err := json.Unmarshal(raw, &series); err != nil {
		t.Fatalf("stats/timeseries: %v", err)
	}
	if series.Group != "day" {
		t.Fatalf("stats group = %q", series.Group)
	}

	// Session list + project lifecycle: the panel's request/response shapes.
	raw = callWithin(t, client, "mothx/session/listAll", buildHistoryListRequest("all", "", "", ""))
	var sessions sessionListResult
	if err := json.Unmarshal(raw, &sessions); err != nil {
		t.Fatalf("session/listAll: %v", err)
	}
	if sessions.Sessions == nil {
		t.Fatalf("session/listAll must return an array: %s", raw)
	}
	raw = callWithin(t, client, "mothx/projects/list", map[string]any{})
	var projects projectListResult
	if err := json.Unmarshal(raw, &projects); err != nil {
		t.Fatalf("projects/list: %v", err)
	}
	raw = callWithin(t, client, "mothx/projects/create", buildProjectRequest("", "Native Smoke"))
	var project projectView
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatalf("projects/create: %v", err)
	}
	if project.ID == "" {
		t.Fatalf("projects/create returned no id: %s", raw)
	}
	callWithin(t, client, "mothx/projects/rename", buildProjectRequest(project.ID, "Native Smoke Renamed"))
	callWithin(t, client, "mothx/projects/delete", map[string]any{"id": project.ID})

	// ACP worker catalog is the Router selection source.
	raw = callWithin(t, client, "mothx/manage/acp/profiles/list", map[string]any{})
	var catalog acpWorkerCatalogView
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("acp profiles/list: %v", err)
	}
	if catalog.Profiles == nil {
		t.Fatalf("acp profiles/list must return an array: %s", raw)
	}

	// Delivery list is an array even when the isolated store is empty.
	raw = callWithin(t, client, "mothx/manage/deliveries/list", map[string]any{})
	var deliveries deliveryListResult
	if err := json.Unmarshal(raw, &deliveries); err != nil {
		t.Fatalf("deliveries/list: %v", err)
	}
	if deliveries.Deliveries == nil {
		t.Fatalf("deliveries/list must return an array: %s", raw)
	}

	// Worktree lifecycle against a real git repository. Creation is
	// asynchronous, so poll the canonical list until the manager reports ready.
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=E2E", "GIT_AUTHOR_EMAIL=e2e@example.com",
			"GIT_COMMITTER_NAME=E2E", "GIT_COMMITTER_EMAIL=e2e@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-qm", "init")

	raw = callWithin(t, client, "mothx/worktree/create", buildWorktreeCreateRequest(repo, "native-smoke"))
	var created worktreeCreateResult
	if err := json.Unmarshal(raw, &created); err != nil || created.Worktree == nil || created.Worktree.Directory == "" {
		t.Fatalf("worktree/create: %v (%s)", err, raw)
	}
	createdDir := created.Worktree.Directory
	deadline := time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		raw = callWithin(t, client, "mothx/worktree/list", map[string]any{"repositoryRoot": repo})
		var list worktreeListResult
		if err := json.Unmarshal(raw, &list); err != nil {
			t.Fatalf("worktree/list: %v", err)
		}
		for _, tree := range list.Worktrees {
			if filepath.Clean(tree.Directory) != filepath.Clean(createdDir) {
				continue
			}
			switch tree.Status {
			case "ready":
				ready = true
			case "failed":
				t.Fatalf("worktree population failed: %s", tree.Error)
			}
		}
		if ready {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("worktree %s did not become ready", createdDir)
	}
	callWithin(t, client, "mothx/worktree/remove", map[string]any{"directory": createdDir})
}
