package agentruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tools"
)

func TestACPReadTextFileUsesRuntimeRootsAndLineWindow(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, "notes.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	handler := runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: "run-parent", ChildID: "agent-child",
		Profile: config.ACPAgentProfile{ID: "helper"}, Strategy: config.ACPPermissionAutoAllowOnce,
	}, nil)

	params, _ := json.Marshal(acpclient.ReadTextFileParams{SessionID: "remote", Path: path, Line: 2, Limit: 2})
	result, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("10"), Method: "fs/read_text_file", Params: params,
	})
	if rpcErr != nil {
		t.Fatalf("read handler: %v", rpcErr)
	}
	read, ok := result.(acpclient.ReadTextFileResult)
	if !ok || read.Content != "two\nthree\n" {
		t.Fatalf("read result = %#v", result)
	}
}

func TestACPReadTextFileRejectsPathsOutsideWorkspace(t *testing.T) {
	workDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	handler := runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: "run-parent", ChildID: "agent-child",
		Profile: config.ACPAgentProfile{ID: "helper"}, Strategy: config.ACPPermissionAutoAllowOnce,
	}, nil)
	params, _ := json.Marshal(acpclient.ReadTextFileParams{SessionID: "remote", Path: outside})
	result, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("11"), Method: "fs/read_text_file", Params: params,
	})
	if result != nil || rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("outside read = %#v, %#v", result, rpcErr)
	}
}

func TestACPWorkspaceBridgeIsScopedToIsolatedWorktree(t *testing.T) {
	mainDir, worktreeDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(mainDir, "secret.txt"), []byte("main secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(worktreeDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	visible := filepath.Join(worktreeDir, "visible.txt")
	if err := os.WriteFile(visible, []byte("worktree"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{WorkDir: mainDir, Registry: tools.NewRegistry(mainDir, nil)}
	workspace, err := runtime.acpWorkspaceFor(worktreeDir)
	if err != nil {
		t.Fatalf("acpWorkspaceFor: %v", err)
	}
	if workspace == nil || !workspace.isolated || workspace.registry.GetWorkDir() != worktreeDir {
		t.Fatalf("isolated workspace = %#v", workspace)
	}
	scope := acpWorkerRequestScope{
		ChildID: "agent-child", Workspace: workspace, WorkDir: worktreeDir,
		Profile: config.ACPAgentProfile{
			ID: "helper", Roles: []config.ACPAgentRole{config.ACPRoleImplement},
			WriteRoots: []string{"src"},
		},
		Contract: SubagentTaskContract{
			Role: SubagentRoleImplement, WriteScope: []string{"src"}, TargetProfile: "helper",
		},
		Strategy: config.ACPPermissionAutoAllowOnce,
	}
	handler := runtime.newACPRequestHandler(scope, nil)

	params, _ := json.Marshal(acpclient.ReadTextFileParams{SessionID: "remote", Path: visible})
	result, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("30"), Method: "fs/read_text_file", Params: params,
	})
	if rpcErr != nil {
		t.Fatalf("read worktree file: %v", rpcErr)
	}
	if read, ok := result.(acpclient.ReadTextFileResult); !ok || read.Content != "worktree" {
		t.Fatalf("worktree read = %#v", result)
	}

	params, _ = json.Marshal(acpclient.ReadTextFileParams{SessionID: "remote", Path: filepath.Join(mainDir, "secret.txt")})
	if _, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("31"), Method: "fs/read_text_file", Params: params,
	}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("main workspace read = %#v, want isolation rejection", rpcErr)
	}

	params, _ = json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: filepath.Join(worktreeDir, "src", "new.go"), Content: "package src\n",
	})
	if result, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("32"), Method: "fs/write_text_file", Params: params,
	}); rpcErr != nil || result != nil {
		t.Fatalf("worktree write = %#v, %#v", result, rpcErr)
	}
	if data, err := os.ReadFile(filepath.Join(worktreeDir, "src", "new.go")); err != nil || string(data) != "package src\n" {
		t.Fatalf("worktree write content = %q err=%v", data, err)
	}
}

func TestACPReadTextFileRejectsOversizedAndBinaryContent(t *testing.T) {
	workDir := t.TempDir()
	large := filepath.Join(workDir, "large.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", maxACPReadTextFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(workDir, "binary.dat")
	if err := os.WriteFile(binary, []byte{0xff, 0xfe, 0xfd}, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	handler := runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: "run-parent", ChildID: "agent-child",
		Profile: config.ACPAgentProfile{ID: "helper"}, Strategy: config.ACPPermissionAutoAllowOnce,
	}, nil)
	for _, path := range []string{large, binary} {
		params, _ := json.Marshal(acpclient.ReadTextFileParams{SessionID: "remote", Path: path})
		result, rpcErr := handler(t.Context(), acpclient.Message{
			JSONRPC: "2.0", ID: json.RawMessage("12"), Method: "fs/read_text_file", Params: params,
		})
		if result != nil || rpcErr == nil || rpcErr.Code != -32001 {
			t.Fatalf("read %s = %#v, %#v", filepath.Base(path), result, rpcErr)
		}
	}
}

func TestACPWorkerClientCapabilitiesAdvertiseReadOnlyFilesystem(t *testing.T) {
	capabilities := acpWorkerClientCapabilities(true, false, false)
	fs, ok := capabilities["fs"].(map[string]bool)
	if !ok || !fs["readTextFile"] || fs["writeTextFile"] {
		t.Fatalf("filesystem capabilities = %#v", capabilities["fs"])
	}
	if terminal, _ := capabilities["terminal"].(bool); terminal {
		t.Fatalf("terminal capability must remain disabled: %#v", capabilities)
	}
	capabilities = acpWorkerClientCapabilities(false, false, false)
	fs = capabilities["fs"].(map[string]bool)
	if fs["readTextFile"] {
		t.Fatalf("read capability advertised without a Runtime registry: %#v", capabilities["fs"])
	}
}

func TestACPWriteTextFileWritesInsideScopedRoots(t *testing.T) {
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	profile := config.ACPAgentProfile{
		ID: "helper", Roles: []config.ACPAgentRole{config.ACPRoleImplement},
		WriteRoots: []string{"src"},
	}
	scope := acpWorkerRequestScope{
		ChildID: "agent-child", Profile: profile, WorkDir: workDir,
		Contract: SubagentTaskContract{
			Role: SubagentRoleImplement, WriteScope: []string{"src"},
			TargetProfile: "helper",
		},
		Strategy: config.ACPPermissionAutoAllowOnce,
	}
	if !runtime.acpWriteTextFileAvailable(profile, scope.Contract) {
		t.Fatal("write bridge is not available for a write contract with declared roots")
	}
	handler := runtime.newACPRequestHandler(scope, nil)
	target := filepath.Join(workDir, "src", "new.go")
	params, _ := json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: target, Content: "package src\n",
	})
	result, rpcErr := handler(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("20"), Method: "fs/write_text_file", Params: params,
	})
	if rpcErr != nil || result != nil {
		t.Fatalf("write result = %#v, %#v", result, rpcErr)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "package src\n" {
		t.Fatalf("written content = %q", data)
	}
}

func TestACPWriteTextFileRejectsReadOnlyAndOutOfScopePaths(t *testing.T) {
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	profile := config.ACPAgentProfile{
		ID: "helper", Roles: []config.ACPAgentRole{config.ACPRoleImplement},
		WriteRoots: []string{"src"},
	}
	writeContract := SubagentTaskContract{
		Role: SubagentRoleImplement, WriteScope: []string{"src"},
		TargetProfile: "helper",
	}

	readOnly := acpWorkerRequestScope{
		ChildID: "read-only", Profile: profile, WorkDir: workDir,
		Contract: SubagentTaskContract{
			Role: SubagentRoleInvestigate, TargetProfile: "helper",
		},
	}
	if runtime.acpWriteTextFileAvailable(profile, readOnly.Contract) {
		t.Fatal("write bridge advertised for a read-only contract")
	}
	params, _ := json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: filepath.Join(workDir, "src", "x.go"), Content: "x",
	})
	if _, rpcErr := runtime.newACPRequestHandler(readOnly, nil)(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("21"), Method: "fs/write_text_file", Params: params,
	}); rpcErr == nil || rpcErr.Code != -32601 {
		t.Fatalf("read-only write = %#v, want method unavailable", rpcErr)
	}

	outOfScope := acpWorkerRequestScope{
		ChildID: "writer", Profile: profile, WorkDir: workDir, Contract: writeContract,
	}
	params, _ = json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: filepath.Join(workDir, "other.go"), Content: "x",
	})
	if _, rpcErr := runtime.newACPRequestHandler(outOfScope, nil)(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("22"), Method: "fs/write_text_file", Params: params,
	}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("out-of-scope write = %#v, want policy rejection", rpcErr)
	}
	if _, err := os.Stat(filepath.Join(workDir, "other.go")); !os.IsNotExist(err) {
		t.Fatalf("out-of-scope file exists: %v", err)
	}
}

func TestACPWriteTextFileResolvesSymlinkBeforeScopeCheck(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideScope := filepath.Join(workDir, "other.go")
	if err := os.WriteFile(outsideScope, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workDir, "src", "link.go")
	if err := os.Symlink(outsideScope, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	profile := config.ACPAgentProfile{
		ID: "helper", Roles: []config.ACPAgentRole{config.ACPRoleImplement},
		WriteRoots: []string{"src"},
	}
	scope := acpWorkerRequestScope{
		ChildID: "agent-child", Profile: profile, WorkDir: workDir,
		Contract: SubagentTaskContract{
			Role: SubagentRoleImplement, WriteScope: []string{"src"}, TargetProfile: "helper",
		},
	}
	params, _ := json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: link, Content: "new",
	})
	if _, rpcErr := runtime.newACPRequestHandler(scope, nil)(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("23"), Method: "fs/write_text_file", Params: params,
	}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("symlink write = %#v, want policy rejection", rpcErr)
	}
	data, err := os.ReadFile(outsideScope)
	if err != nil || string(data) != "old" {
		t.Fatalf("symlink target was modified: %q, %v", data, err)
	}
}
