package agentruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tools"
)

func TestACPTerminalRunsAndWaitsForOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	manager := newTestACPTerminalManager(t, runtime, workDir)
	defer manager.Close()

	result, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/create", acpclient.TerminalCreateParams{
		SessionID: "remote", Command: "sh", Args: []string{"-c", "printf terminal-ok"},
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/create: %v", rpcErr)
	}
	created, ok := result.(acpclient.TerminalCreateResult)
	if !ok || created.TerminalID == "" {
		t.Fatalf("terminal/create result = %#v", result)
	}
	finished, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/wait_for_exit", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: created.TerminalID,
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/wait_for_exit: %v", rpcErr)
	}
	status, ok := finished.(acpclient.TerminalExitStatus)
	if !ok || status.ExitCode == nil || *status.ExitCode != 0 {
		t.Fatalf("exit status = %#v", finished)
	}
	outputResult, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/output", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: created.TerminalID,
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/output: %v", rpcErr)
	}
	output, ok := outputResult.(acpclient.TerminalOutputResult)
	if !ok || output.Output != "terminal-ok" {
		t.Fatalf("terminal output = %#v", outputResult)
	}
}

func TestACPTerminalRejectsCwdOutsideWorkspace(t *testing.T) {
	workDir := t.TempDir()
	outside := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	manager := newTestACPTerminalManager(t, runtime, workDir)
	defer manager.Close()

	_, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/create", acpclient.TerminalCreateParams{
		SessionID: "remote", Command: "sh", Args: []string{"-c", "true"}, Cwd: outside,
	}))
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("outside cwd = %#v, want workspace rejection", rpcErr)
	}
}

func TestACPTerminalKillAndReleaseStopProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	manager := newTestACPTerminalManager(t, runtime, workDir)
	defer manager.Close()

	result, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/create", acpclient.TerminalCreateParams{
		SessionID: "remote", Command: "sh", Args: []string{"-c", "sleep 30"},
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/create: %v", rpcErr)
	}
	id := result.(acpclient.TerminalCreateResult).TerminalID
	if _, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/kill", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: id,
	})); rpcErr != nil {
		t.Fatalf("terminal/kill: %v", rpcErr)
	}
	if _, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/wait_for_exit", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: id,
	})); rpcErr != nil {
		t.Fatalf("terminal/wait_for_exit after kill: %v", rpcErr)
	}
	if _, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/release", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: id,
	})); rpcErr != nil {
		t.Fatalf("terminal/release: %v", rpcErr)
	}
	if manager.get(id) != nil {
		t.Fatal("released terminal is still registered")
	}
}

func TestACPTerminalCapabilityIsTruthful(t *testing.T) {
	capabilities := acpWorkerClientCapabilities(false, false, true)
	if terminal, _ := capabilities["terminal"].(bool); !terminal {
		t.Fatalf("terminal capability = %#v, want enabled", capabilities)
	}
	capabilities = acpWorkerClientCapabilities(false, false, false)
	if terminal, _ := capabilities["terminal"].(bool); terminal {
		t.Fatalf("terminal capability = %#v, want disabled", capabilities)
	}
}

func newTestACPTerminalManager(t *testing.T, runtime *SessionRuntime, workDir string) *acpTerminalManager {
	t.Helper()
	env, err := acpWorkerEnvironment(config.ACPAgentProfile{ID: "terminal-test"})
	if err != nil {
		t.Fatalf("resolve test environment: %v", err)
	}
	manager := newACPTerminalManager(runtime.Registry, workDir, env)
	if manager == nil {
		t.Fatal("terminal manager is nil")
	}
	return manager
}

func terminalMessage(t *testing.T, method string, params any) acpclient.Message {
	t.Helper()
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return acpclient.Message{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: method, Params: encoded}
}

func TestACPTerminalOutputIsBoundedAndMarksTruncation(t *testing.T) {
	buffer := newACPTerminalBuffer(8)
	if _, err := buffer.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	output, truncated := buffer.snapshot()
	if output != "89abcdef" || !truncated {
		t.Fatalf("bounded output = %q, truncated=%t", output, truncated)
	}
	if strings.HasSuffix(output, "\x00") {
		t.Fatalf("bounded output contains trailing NUL: %q", output)
	}
}

func TestACPTerminalOutputTruncatesAtUTF8Boundary(t *testing.T) {
	buffer := newACPTerminalBuffer(4)
	if _, err := buffer.Write([]byte("界界")); err != nil {
		t.Fatal(err)
	}
	output, truncated := buffer.snapshot()
	if !truncated {
		t.Fatal("oversized UTF-8 output was not marked truncated")
	}
	if !utf8.ValidString(output) {
		t.Fatalf("bounded output is not valid UTF-8: %q", output)
	}
	if len(output) > 4 || output != "界" {
		t.Fatalf("bounded output = %q (%d bytes), want one complete rune", output, len(output))
	}
}

func TestACPTerminalOutputDoesNotMarkExactLimitAsTruncated(t *testing.T) {
	buffer := newACPTerminalBuffer(4)
	if _, err := buffer.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	output, truncated := buffer.snapshot()
	if output != "1234" || truncated {
		t.Fatalf("exact-limit output = %q, truncated=%t", output, truncated)
	}
}

func TestACPTerminalDoesNotInheritFullParentEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	t.Setenv("MOTHX_ACP_TERMINAL_SECRET", "must-not-leak")
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	manager := newACPTerminalManager(runtime.Registry, workDir, []string{
		"PATH=" + os.Getenv("PATH"),
	})
	if manager == nil {
		t.Fatal("terminal manager is nil")
	}
	defer manager.Close()

	result, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/create", acpclient.TerminalCreateParams{
		SessionID: "remote", Command: "sh",
		Args: []string{"-c", `printf '%s' "${MOTHX_ACP_TERMINAL_SECRET-unset}"`},
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/create: %v", rpcErr)
	}
	id := result.(acpclient.TerminalCreateResult).TerminalID
	if _, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/wait_for_exit", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: id,
	})); rpcErr != nil {
		t.Fatalf("terminal/wait_for_exit: %v", rpcErr)
	}
	outputResult, rpcErr := manager.Handle(t.Context(), terminalMessage(t, "terminal/output", acpclient.TerminalRefParams{
		SessionID: "remote", TerminalID: id,
	}))
	if rpcErr != nil {
		t.Fatalf("terminal/output: %v", rpcErr)
	}
	if output := outputResult.(acpclient.TerminalOutputResult).Output; output != "unset" {
		t.Fatalf("terminal output = %q, parent environment leaked", output)
	}
}

func TestACPWriteTextFileRejectsOversizedContent(t *testing.T) {
	workDir := t.TempDir()
	runtime := &SessionRuntime{Registry: tools.NewRegistry(workDir, nil)}
	profile := config.ACPAgentProfile{ID: "helper"}
	scope := acpWorkerRequestScope{
		ChildID: "agent-child", Profile: profile, WorkDir: workDir,
		Contract: SubagentTaskContract{
			Role: SubagentRoleImplement, WriteScope: []string{"."}, TargetProfile: "helper",
		},
	}
	params, _ := json.Marshal(acpclient.WriteTextFileParams{
		SessionID: "remote", Path: filepath.Join(workDir, "large.txt"),
		Content: strings.Repeat("x", maxACPWriteTextFileBytes+1),
	})
	if _, rpcErr := runtime.newACPRequestHandler(scope, nil)(t.Context(), acpclient.Message{
		JSONRPC: "2.0", ID: json.RawMessage("24"), Method: "fs/write_text_file", Params: params,
	}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("oversized write = %#v, want bound rejection", rpcErr)
	}
	if _, err := os.Stat(filepath.Join(workDir, "large.txt")); !os.IsNotExist(err) {
		t.Fatalf("oversized write created a file: %v", err)
	}
}
