package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// realACPInteropSpec describes one externally installed ACP agent used for an
// explicit, opt-in interoperability check. Keeping the recipes here (rather than
// in the default test path) matches the proposal: real external interop is a
// manual compatibility run, not CI.
type realACPInteropSpec struct {
	name    string
	command []string
	env     []string
	driver  workerdriver.WorkerDriver
	// model optionally selects a model through ACP session/set_config_option.
	// The external Agent owns its model configuration; this exists only because
	// a test machine's default provider credentials may be unavailable.
	model string
}

func realACPInteropSpecs(t *testing.T) []realACPInteropSpec {
	t.Helper()
	specs := []realACPInteropSpec{{
		name:    "opencode",
		command: []string{"opencode", "acp"},
		env:     []string{"DEEPSEEK_API_KEY"},
		driver:  workerdriver.WorkerDriverGenericACP,
		model:   "deepseek/deepseek-flash",
	}}
	if path := strings.TrimSpace(os.Getenv("MOTHX_CODEX_ACP")); path != "" {
		specs = append(specs, realACPInteropSpec{
			name:    "codex",
			command: []string{path},
			env:     []string{"CODEX_HOME"},
			driver:  workerdriver.WorkerDriverGenericACP,
		})
	}
	if path := strings.TrimSpace(os.Getenv("MOTHX_CLAUDE_ACP")); path != "" {
		specs = append(specs, realACPInteropSpec{
			name:    "claude-code",
			command: []string{path},
			driver:  workerdriver.WorkerDriverGenericACP,
		})
	}
	return specs
}

// interopObservation counts real MCP requests and signals the first tools/list.
// It also counts worker_mgr actions so the opt-in assertion can prove the agent
// called context, not just report.
type interopObservation struct {
	requests     atomic.Int64
	contextCalls atomic.Int64
	reportCalls  atomic.Int64
	toolsList    chan struct{}
}

// wrapBridgeHandlerForObservation installs a test-only body observer so an
// opt-in run can explain what a real agent actually sent. The original body is
// restored before dispatch; report text is logged only when explicitly enabled.
func wrapBridgeHandlerForObservation(t *testing.T, bridge *WorkerBridgeServer, observation *interopObservation, debug bool) {
	t.Helper()
	bridge.mu.Lock()
	original := bridge.server.Handler
	bridge.server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.Body != nil {
			body, err := io.ReadAll(request.Body)
			if err == nil {
				observation.requests.Add(1)
				if debug {
					t.Logf("mcp request: %s", string(body))
				}
				var call struct {
					Method string `json:"method"`
					Params struct {
						Name      string `json:"name"`
						Arguments struct {
							Action string `json:"action"`
						} `json:"arguments"`
					} `json:"params"`
				}
				if json.Unmarshal(body, &call) == nil && call.Method == "tools/call" && call.Params.Name == workerMgrToolName {
					switch call.Params.Arguments.Action {
					case "context":
						observation.contextCalls.Add(1)
					case "report":
						observation.reportCalls.Add(1)
					}
				}
				if strings.Contains(string(body), `"tools/list"`) {
					select {
					case observation.toolsList <- struct{}{}:
					default:
					}
				}
				request.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		original.ServeHTTP(writer, request)
	})
	bridge.mu.Unlock()
}

// TestRealACPBridgeInterop is opt-in: set MOTHX_REAL_ACP_INTEROP=opencode (or
// codex/claude-code, plus MOTHX_CODEX_ACP / MOTHX_CLAUDE_ACP) to run one real
// external Agent through initialize, session/new, the loopback MCP bridge, and
// a prompt. The default test path only uses the scripted ACP helper.
//
// The assertion is the bridge contract, not the external model's answer: a real
// agent must call worker_mgr over the injected loopback MCP server and leave a
// finding in the Runtime-owned JSONL log. The prompt is cancelled as soon as
// that evidence exists so an unrelated provider/model delay cannot turn a
// successful bridge interop into a long, flaky test. The full prompt lifecycle
// is covered by the scripted ACP fixtures instead.
func TestRealACPBridgeInterop(t *testing.T) {
	wanted := strings.TrimSpace(os.Getenv("MOTHX_REAL_ACP_INTEROP"))
	if wanted == "" {
		t.Skip("set MOTHX_REAL_ACP_INTEROP to run real external ACP interoperability")
	}
	var selected *realACPInteropSpec
	for _, spec := range realACPInteropSpecs(t) {
		if strings.EqualFold(spec.name, wanted) {
			copied := spec
			selected = &copied
			break
		}
	}
	if selected == nil {
		t.Fatalf("MOTHX_REAL_ACP_INTEROP=%q is not a known local agent", wanted)
	}
	handshakeOnly := strings.TrimSpace(os.Getenv("MOTHX_REAL_ACP_INTEROP_HANDSHAKE_ONLY")) != ""
	debug := strings.TrimSpace(os.Getenv("MOTHX_REAL_ACP_INTEROP_DEBUG")) != ""
	if _, err := exec.LookPath(selected.command[0]); err != nil {
		t.Fatalf("real ACP agent %q is not installed: %v", selected.command[0], err)
	}
	for _, name := range selected.env {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			t.Fatalf("real ACP agent %q requires %s in the test environment", selected.name, name)
		}
	}

	bridge := NewWorkerBridgeServer()
	defer func() { _ = bridge.Close() }()
	messageLog, err := OpenWorkerMessageLog(t.TempDir(), "real-interop-session")
	if err != nil {
		t.Fatalf("open worker message log: %v", err)
	}
	defer func() { _ = messageLog.Close() }()

	workDir := t.TempDir()
	profile := config.ACPAgentProfile{
		ID:               selected.name,
		Title:            selected.name,
		Command:          selected.command,
		Enabled:          true,
		Environment:      selected.env,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Driver:           string(selected.driver),
	}
	findings := make(chan WorkerMessage, 4)
	observation := &interopObservation{toolsList: make(chan struct{}, 1)}
	var resolved workerdriver.WorkerCapabilities
	prepare := func(caps workerdriver.WorkerCapabilities) ([]any, func(), error) {
		resolved = caps
		scope := &WorkerBridgeScope{
			SessionID: "real-interop-session", ChildID: "real-interop-child",
			ParentRunID: "real-interop-run", ProfileID: selected.name,
			WorkDir:      workDir,
			TaskPrompt:   "Call worker_mgr once with action=report.",
			Capabilities: caps,
			Messages:     messageLog,
			Notify: func(message WorkerMessage) {
				select {
				case findings <- message:
				default:
				}
			},
		}
		token, payload, err := bridge.Register(scope)
		if err != nil {
			return nil, nil, err
		}
		wrapBridgeHandlerForObservation(t, bridge, observation, debug)
		return payload, func() { bridge.Revoke(token) }, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	contract := SubagentTaskContract{
		Objective:     "Verify the MothX worker bridge. Do not modify any files.",
		Role:          SubagentRoleInvestigate,
		TargetProfile: selected.name,
		ExpectedDeliverables: []SubagentDeliverable{
			SubagentDeliverableFindings,
		},
		SystemPromptExtra: "You have a MothX MCP tool named worker_mgr. " +
			"First call it with {\"action\":\"context\"}. " +
			"Then call it with {\"action\":\"report\",\"kind\":\"finding\",\"text\":\"interop-ok\",\"dedupe_key\":\"interop-ok\"}. " +
			"Finally reply with one line containing interop-ok. Do not run shell commands.",
	}
	invocation := ACPWorkerInvocation{
		Profile:            profile,
		Contract:           contract,
		WorkDir:            workDir,
		Launcher:           acpProcessLauncher{},
		MCPBridgeAvailable: true,
		PrepareMCPServers:  prepare,
	}
	worker, err := (ACPWorkerBridge{}).Open(ctx, invocation)
	if err != nil {
		t.Fatalf("open real ACP worker: %v", err)
	}
	if selected.model != "" {
		configureCtx, configureCancel := context.WithTimeout(ctx, 30*time.Second)
		var configureResult json.RawMessage
		configureErr := worker.conn.client.Call(configureCtx, "session/set_config_option", map[string]any{
			"sessionId": worker.SessionID(), "configId": "model", "value": selected.model,
		}, &configureResult)
		configureCancel()
		if configureErr != nil {
			t.Fatalf("select real ACP worker model %q: %v", selected.model, configureErr)
		}
	}
	promptCtx, stopPrompt := context.WithCancel(ctx)
	defer stopPrompt()
	type promptOutcome struct {
		result ACPWorkerResult
		err    error
	}
	done := make(chan promptOutcome, 1)
	go func() {
		result, runErr := worker.Prompt(promptCtx, acpPromptContent(worker.Capabilities(), contract, workDir), func(agentpkg.Event) {})
		done <- promptOutcome{result: result, err: runErr}
	}()

	var reported WorkerMessage
	deadline := time.After(2 * time.Minute)
	var outcome promptOutcome
	completed := false
	sawHandshake := false
wait:
	for {
		select {
		case message := <-findings:
			reported = message
			break wait
		case <-observation.toolsList:
			sawHandshake = true
			if handshakeOnly {
				break wait
			}
		case outcome = <-done:
			completed = true
			break wait
		case <-deadline:
			break wait
		}
	}
	stopPrompt()
	if !completed {
		select {
		case outcome = <-done:
		case <-time.After(15 * time.Second):
		}
	}
	closeErr := worker.Close(context.Background())
	if closeErr != nil {
		t.Logf("close real ACP worker: %v", closeErr)
	}

	if reported.ID == "" {
		if handshakeOnly && sawHandshake {
			if !resolved.MCPHTTP {
				t.Fatalf("resolved capabilities did not keep the HTTP bridge: %#v", resolved)
			}
			t.Logf("%s %s real ACP MCP handshake ok: command=%v requests=%d stopReason=%q output=%q",
				selected.name, outcome.result.AgentInfo.Version, selected.command, observation.requests.Load(), outcome.result.StopReason, outcome.result.Output)
			return
		}
		messages, listErr := messageLog.List()
		t.Fatalf("worker did not report a finding through the bridge; promptErr=%v stopReason=%q output=%q messages=%#v listErr=%v",
			outcome.err, outcome.result.StopReason, outcome.result.Output, messages, listErr)
	}
	if !resolved.MCPHTTP {
		t.Fatalf("resolved capabilities did not keep the HTTP bridge: %#v", resolved)
	}
	// The bridge contract has two calls: context (task contract handshake) and
	// report (finding). A finding alone does not prove the context action worked.
	contextDeadline := time.Now().Add(2 * time.Second)
	for observation.contextCalls.Load() == 0 && time.Now().Before(contextDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if observation.contextCalls.Load() == 0 {
		t.Fatalf("worker reported a finding without calling worker_mgr(context): requests=%d reports=%d output=%q",
			observation.requests.Load(), observation.reportCalls.Load(), outcome.result.Output)
	}
	if reported.Kind != WorkerMessageFinding || !strings.Contains(reported.Body, "interop-ok") {
		t.Fatalf("bridge finding = %#v", reported)
	}
	if outcome.err != nil {
		t.Logf("%s prompt did not finish before the bridge assertion (ok): %v", selected.name, outcome.err)
	}
	t.Logf("%s %s real ACP bridge interop ok: command=%v contextCalls=%d reportCalls=%d finding=%q stopReason=%q output=%q",
		selected.name, outcome.result.AgentInfo.Version, selected.command,
		observation.contextCalls.Load(), observation.reportCalls.Load(),
		reported.Body, outcome.result.StopReason, outcome.result.Output)
}
