package agentruntime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// A closed bridge cannot bind a listener; Register must fail rather than hand
// the worker a dead URL, so Open can degrade instead of injecting it.
func TestWorkerBridgeRegisterFailsAfterClose(t *testing.T) {
	server := NewWorkerBridgeServer()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err := server.Register(&WorkerBridgeScope{
		SessionID: "session-closed", ChildID: "child-closed",
		Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
	})
	if err == nil {
		t.Fatal("Register succeeded on a closed bridge")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "closed") {
		t.Fatalf("Register error = %v, want a closed-bridge reason", err)
	}
}

// A registration/listener failure is a degradation, not a task failure: the
// worker keeps its prompt, and the resolved capability projection must stop
// advertising an available bridge.
func TestPrepareMCPServersDegradesCapabilitiesOnRegistrationFailure(t *testing.T) {
	server := NewWorkerBridgeServer()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	worker := &acpRemoteWorker{
		caps: workerdriver.WorkerCapabilities{MCPHTTP: true, MCPSSE: true, Source: "negotiated"},
	}
	invocation := ACPWorkerInvocation{
		PrepareMCPServers: func(caps workerdriver.WorkerCapabilities) ([]any, func(), error) {
			token, payload, err := server.Register(&WorkerBridgeScope{
				SessionID: "session-degraded", ChildID: "child-degraded", Capabilities: caps,
			})
			if err != nil {
				return nil, nil, err
			}
			return payload, func() { server.Revoke(token) }, nil
		},
	}
	if servers := worker.prepareMCPServers(invocation); servers != nil {
		t.Fatalf("failed registration still injected MCP servers: %#v", servers)
	}
	caps := worker.WorkerCapabilities()
	if caps.MCPHTTP || caps.MCPSSE || caps.MCPStdio {
		t.Fatalf("bridge capability stayed enabled after failure: %#v", caps)
	}
	if caps.Source != "degraded" {
		t.Fatalf("capability source = %q, want degraded", caps.Source)
	}
	found := false
	for _, degradation := range caps.Degradations {
		if strings.Contains(degradation, "worker bridge registration failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("registration failure was not recorded: %#v", caps.Degradations)
	}
	if state := workerMCPBridgeState(caps, config.ACPAgentProfile{}); state != "unavailable" {
		t.Fatalf("mcpBridge state = %q, want unavailable", state)
	}
	if summary := workerCapabilitySummary(caps); strings.Contains(summary, "report/ask") {
		t.Fatalf("model-facing summary still advertises report/ask: %q", summary)
	}
}

// registerWorkerBridge is the actual Runtime registration seam used by
// acpRemoteWorker; a closed bridge must return an error so the caller can
// degrade instead of silently injecting an unusable endpoint.
func TestRegisterWorkerBridgeReturnsErrorForClosedBridge(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("session-bridge-closed"); err != nil {
		t.Fatal(err)
	}
	bridge := NewWorkerBridgeServer()
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: "session-bridge-closed", Manager: manager, WorkerBridge: bridge}
	t.Cleanup(func() { runtime.Close() })
	_, _, err := runtime.registerWorkerBridge(
		"child-1", "run-1",
		config.ACPAgentProfile{ID: "generic", Enabled: true, Command: []string{"agent"}},
		SubagentTaskContract{Objective: "x", Role: SubagentRoleInvestigate},
		workerdriver.WorkerCapabilities{MCPHTTP: true},
		workDir,
	)
	if err == nil {
		t.Fatal("closed bridge registration returned no error")
	}
}

func TestWorkerBridgeAuthRejectsNonLoopbackRemoteAddr(t *testing.T) {
	server, _, token, _ := newBridgeTestScope(t, nil)
	defer func() { _ = server.Close() }()

	nonLoopback := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	nonLoopback.RemoteAddr = "203.0.113.9:4567"
	nonLoopback.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	server.authHandler().ServeHTTP(recorder, nonLoopback)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-loopback request status = %d, want 403", recorder.Code)
	}

	loopback := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	loopback.RemoteAddr = "127.0.0.1:4567"
	loopback.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	server.authHandler().ServeHTTP(recorder, loopback)
	if recorder.Code == http.StatusForbidden || recorder.Code == http.StatusUnauthorized {
		t.Fatalf("loopback request was rejected: %d", recorder.Code)
	}
}
