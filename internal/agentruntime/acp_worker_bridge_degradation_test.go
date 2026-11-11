package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// A bridge preparation failure is an availability degradation, not a task
// failure: Open must still create the remote session, Prompt must still run, and
// the degraded capability projection must reach both ChildMetadata and the
// durable child run row.
func TestACPWorkerBridgePrepareFailureRunsPromptAndPersistsDegradedChild(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	profile := config.ACPAgentProfile{
		ID: "helper", Command: []string{testExecutable(t), "-test.run=TestACPWorkerBridgeHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_BRIDGE_HELPER"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	contract := SubagentTaskContract{
		Objective: "inspect this task", Role: SubagentRoleInvestigate,
		ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings}, TargetProfile: "helper",
	}
	prepareCalls := 0
	worker, err := (ACPWorkerBridge{}).Open(context.Background(), ACPWorkerInvocation{
		Profile: profile, WorkDir: t.TempDir(), Launcher: acpProcessLauncher{},
		Contract: contract, MCPBridgeAvailable: true,
		PrepareMCPServers: func(caps workerdriver.WorkerCapabilities) ([]any, func(), error) {
			prepareCalls++
			if !caps.MCPBridge() {
				t.Error("PrepareMCPServers called without a resolved MCP bridge capability")
			}
			return nil, nil, errors.New("bridge registration exploded")
		},
	})
	if err != nil {
		t.Fatalf("Open failed on a bridge preparation error: %v", err)
	}
	defer func() { _ = worker.Close(context.Background()) }()
	if prepareCalls != 1 {
		t.Fatalf("PrepareMCPServers calls = %d, want 1", prepareCalls)
	}
	if worker.SessionID() != "bridge-session" {
		t.Fatalf("Open did not create a remote session: %q", worker.SessionID())
	}
	result, err := worker.Prompt(context.Background(), acpPromptContent(worker.Capabilities(), contract, t.TempDir()), nil)
	if err != nil {
		t.Fatalf("Prompt failed after bridge preparation error: %v", err)
	}
	if result.Output != "bridge output" || result.StopReason != "end_turn" {
		t.Fatalf("prompt result = %#v", result)
	}
	caps := worker.WorkerCapabilities()
	if caps.MCPHTTP {
		t.Fatalf("MCPHTTP stayed enabled after preparation failure: %#v", caps)
	}
	if caps.Source != "degraded" {
		t.Fatalf("capability source = %q, want degraded", caps.Source)
	}
	if !strings.Contains(strings.Join(caps.Degradations, ","), "worker bridge registration failed") {
		t.Fatalf("degradation reason missing: %#v", caps.Degradations)
	}

	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("session-bridge-degraded"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{ID: "session-bridge-degraded", Manager: manager, Source: SourceTUI, Mode: ModeRouter}
	t.Cleanup(runtime.Close)
	proxy := &acpChildProxy{
		runtime: runtime, id: "child-degraded", parent: "lead", mode: ModeRouter,
		profile: profile, contract: contract, workDir: workDir, parentRunID: "run-parent",
		capabilities: caps, createdAt: time.Now().UTC(),
	}

	metadata := proxy.ChildMetadata()
	if metadata["mcpBridge"] != "unavailable" {
		t.Fatalf("ChildMetadata mcpBridge = %v, want unavailable", metadata["mcpBridge"])
	}
	degradations, _ := metadata["capabilityDegradations"].([]string)
	if !strings.Contains(strings.Join(degradations, ","), "worker bridge registration failed") {
		t.Fatalf("ChildMetadata degradations = %#v", metadata["capabilityDegradations"])
	}
	resolved, _ := metadata["capabilities"].(map[string]any)
	if resolved["mcpHttp"] != false {
		t.Fatalf("ChildMetadata resolved mcpHttp = %v, want false", resolved["mcpHttp"])
	}

	proxy.persistChildRun("success", nil)
	record, found, err := session.LoadChildRun(sessionDir, "session-bridge-degraded", "child-degraded")
	if err != nil || !found {
		t.Fatalf("LoadChildRun: found=%t err=%v", found, err)
	}
	if record.MCPBridge != "unavailable" {
		t.Fatalf("persisted mcp_bridge = %q, want unavailable", record.MCPBridge)
	}
	var persistedCaps map[string]any
	if err := json.Unmarshal([]byte(record.ResolvedCapabilitiesJSON), &persistedCaps); err != nil {
		t.Fatalf("decode persisted capabilities: %v", err)
	}
	if persistedCaps["mcpHttp"] != false {
		t.Fatalf("persisted mcpHttp = %v, want false", persistedCaps["mcpHttp"])
	}
	if !strings.Contains(record.CapabilityDegradationsJSON, "worker bridge registration failed") {
		t.Fatalf("persisted degradations = %s", record.CapabilityDegradationsJSON)
	}
}
