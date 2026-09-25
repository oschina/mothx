package acp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

func initACPWorktreeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	return dir
}

func newACPWorktreeServer(t *testing.T, repo string) (*server, *syncedBuffer) {
	t.Helper()
	sessionDir := t.TempDir()
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.settings = &config.Settings{SessionDir: sessionDir}
	s.cwd = repo
	s.workspaceCwd = repo
	mgr, err := agentruntime.NewWorktreeManager(agentruntime.WorktreeManagerOptions{
		SessionDir: sessionDir,
		Root:       t.TempDir(),
		EventSink:  agentruntime.WorktreeEventSinkFunc(func(e agentruntime.WorktreeEvent) { s.notifyWorktreeStatus(e) }),
	})
	if err != nil {
		t.Fatalf("NewWorktreeManager: %v", err)
	}
	s.worktrees = mgr
	return s, output
}

func TestACPWorktreeLifecycleProjection(t *testing.T) {
	repo := initACPWorktreeRepo(t)
	s, output := newACPWorktreeServer(t, repo)

	// create
	s.handleWorktreeCreate(rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/worktree/create",
		Params: json.RawMessage(`{"baseCwd":` + strconv.Quote(repo) + `,"name":"demo"}`),
	})
	messages := parseACPMessages(t, output.String())
	var created map[string]any
	for _, m := range messages {
		if id, ok := m["id"].(float64); ok && id == 1 {
			result, _ := m["result"].(map[string]any)
			created, _ = result["worktree"].(map[string]any)
		}
	}
	if created == nil {
		t.Fatalf("create response = %#v", messages)
	}
	id, _ := created["id"].(string)
	dir, _ := created["directory"].(string)
	if id == "" || dir == "" {
		t.Fatalf("created worktree = %#v", created)
	}
	s.worktrees.Wait()

	// The created directory is authorized, so resolveWorkspace accepts it.
	if _, _, err := s.resolveWorkspace(requestMeta{}, dir); err != nil {
		t.Fatalf("created worktree directory not authorized: %v", err)
	}

	// list
	output.Reset()
	s.handleWorktreeList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/worktree/list", Params: json.RawMessage(`{"repositoryRoot":` + strconv.Quote(repo) + `}`)})
	messages = parseACPMessages(t, output.String())
	var listed []any
	for _, m := range messages {
		if rid, ok := m["id"].(float64); ok && rid == 2 {
			result, _ := m["result"].(map[string]any)
			listed, _ = result["worktrees"].([]any)
		}
	}
	if len(listed) != 1 {
		t.Fatalf("list = %#v, want one worktree", listed)
	}

	// remove
	output.Reset()
	s.handleWorktreeRemove(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "mothx/worktree/remove", Params: json.RawMessage(`{"id":` + strconv.Quote(id) + `}`)})
	messages = parseACPMessages(t, output.String())
	removed := false
	for _, m := range messages {
		if rid, ok := m["id"].(float64); ok && rid == 3 {
			result, _ := m["result"].(map[string]any)
			removed, _ = result["removed"].(bool)
		}
	}
	if !removed {
		t.Fatalf("remove response = %#v", messages)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still exists after remove")
	}
}

func TestACPWorktreeCreateRejectsOutsideWindow(t *testing.T) {
	repo := initACPWorktreeRepo(t)
	s, output := newACPWorktreeServer(t, repo)

	other := t.TempDir()
	s.handleWorktreeCreate(rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/worktree/create",
		Params: json.RawMessage(`{"baseCwd":` + strconv.Quote(other) + `}`),
	})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "unauthorized")
}

func TestACPWorktreeUnavailableWithoutManager(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.handleWorktreeList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/worktree/list"})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "worktrees_unavailable")
}

func TestACPWorktreeListRequiresRepository(t *testing.T) {
	repo := initACPWorktreeRepo(t)
	s, output := newACPWorktreeServer(t, repo)
	s.handleWorktreeCreate(rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/worktree/create",
		Params: json.RawMessage(`{"baseCwd":` + strconv.Quote(repo) + `,"name":"r"}`),
	})
	s.worktrees.Wait()
	output.Reset()

	// A list scoped to a repository outside the window is rejected.
	other := t.TempDir()
	s.handleWorktreeList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/worktree/list", Params: json.RawMessage(`{"repositoryRoot":` + strconv.Quote(other) + `}`)})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "unauthorized")
}

func TestACPReauthorizeWorktreesFromRegistry(t *testing.T) {
	repo := initACPWorktreeRepo(t)
	s, _ := newACPWorktreeServer(t, repo)
	created, err := s.worktrees.Create(context.Background(), agentruntime.CreateWorktreeRequest{BaseCwd: repo, Name: "persisted"})
	if err != nil {
		t.Fatal(err)
	}
	s.worktrees.Wait()

	// Simulate a restart: drop in-memory authorization, then re-authorize from
	// the registry within the negotiated window.
	fresh, err := agentruntime.NewWorktreeManager(agentruntime.WorktreeManagerOptions{SessionDir: s.settings.GetSessionDir(), Root: filepath.Dir(filepath.Dir(created.Directory))})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.IsAuthorized(created.Directory) {
		t.Fatal("fresh manager should start with no authorized worktrees")
	}
	s.worktrees = fresh
	s.reauthorizeWorktrees()
	if !s.worktrees.IsAuthorized(created.Directory) {
		t.Fatalf("registry worktree was not re-authorized within the window")
	}
}
