package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

func TestACPWorkerBridgeRunsOneTextTurnOverStdio(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	notifications := make(chan acpclient.Message, 1)
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: config.ACPAgentProfile{
			ID: "helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerBridgeHelperProcess", "--"}, Enabled: true,
			Environment: []string{"MOTHX_ACP_BRIDGE_HELPER"}, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		WorkDir:  t.TempDir(),
		Launcher: acpProcessLauncher{},
		Contract: SubagentTaskContract{
			Objective:            "inspect this task",
			Role:                 SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "helper",
		},
		OnNotification: func(message acpclient.Message) {
			notifications <- message
		},
	})
	if err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	if result.SessionID != "bridge-session" || result.StopReason != "end_turn" || result.AgentInfo.Name != "bridge-helper" {
		t.Fatalf("result = %#v", result)
	}
	if result.Output != "bridge output" {
		t.Fatalf("bridge output = %q, want the streamed worker text", result.Output)
	}
	select {
	case message := <-notifications:
		if message.Method != "session/update" {
			t.Fatalf("notification = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not forward session/update")
	}
}

func TestBoundACPTextDoesNotSplitRunes(t *testing.T) {
	text := strings.Repeat("a", maxACPProjectionTextBytes-2) + "界" + "tail"
	bounded, truncated := boundACPText(text)
	if !truncated {
		t.Fatal("long text was not marked truncated")
	}
	if !utf8.ValidString(bounded) {
		t.Fatalf("bounded text is not valid UTF-8: %q", bounded)
	}
	if len(bounded) != maxACPProjectionTextBytes-2 {
		t.Fatalf("bounded length = %d, want the last full rune boundary %d", len(bounded), maxACPProjectionTextBytes-2)
	}
}

func TestACPWorkerTurnStreamsTextBeyondBoundedResult(t *testing.T) {
	const chunkSize = 8 << 10
	chunk := strings.Repeat("x", chunkSize)
	streamed := 0
	turn := newACPWorkerTurn(func(event agentpkg.Event) {
		if event.Type == agentpkg.EventTextDelta {
			streamed += len(event.TextDelta)
		}
	})
	for range 4 {
		turn.apply(acpclient.SessionUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       &acpclient.ContentBlock{Type: "text", Text: chunk},
		})
	}
	result := turn.result(acpclient.Implementation{}, "session")
	if streamed != 4*chunkSize {
		t.Fatalf("streamed %d bytes, want every text delta (%d)", streamed, 4*chunkSize)
	}
	if len(result.Output) != maxSubagentResultBytes {
		t.Fatalf("bounded result = %d bytes, want %d", len(result.Output), maxSubagentResultBytes)
	}
	if !result.Truncated {
		t.Fatal("bounded result was not marked truncated")
	}
}

func TestACPWorkerBridgeHelperProcess(t *testing.T) {
	if os.Getenv("MOTHX_ACP_BRIDGE_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	newSessionAttempts := 0
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var request acpclient.Message
		if err := json.Unmarshal(line, &request); err != nil {
			os.Exit(2)
		}
		var result any
		var rpcErr *acpclient.RPCError
		switch request.Method {
		case "initialize":
			result = acpclient.InitializeResult{
				ProtocolVersion: acpclient.ProtocolVersion,
				AgentInfo:       acpclient.Implementation{Name: "bridge-helper", Version: "1"},
				AgentCapabilities: map[string]any{
					"sessionCapabilities": map[string]any{"close": map[string]any{}},
				},
			}
		case "session/new":
			newSessionAttempts++
			if os.Getenv("MOTHX_ACP_BRIDGE_FAIL_FIRST_NEW") == "1" && newSessionAttempts == 1 {
				rpcErr = &acpclient.RPCError{Code: -32000, Message: "transient session failure"}
				break
			}
			sessionID := "bridge-session"
			if os.Getenv("MOTHX_ACP_BRIDGE_ECHO_MCP") == "1" {
				var params struct {
					MCPServers []any `json:"mcpServers"`
				}
				if unmarshalErr := json.Unmarshal(request.Params, &params); unmarshalErr != nil || len(params.MCPServers) == 0 {
					sessionID = "bridge-session-no-mcp"
				} else {
					sessionID = "bridge-session-mcp"
				}
			}
			result = acpclient.NewSessionResult{SessionID: sessionID}
		case "session/prompt":
			text := "bridge output"
			if os.Getenv("MOTHX_ACP_BRIDGE_PARTIAL") == "1" {
				text = "partial output"
			}
			if os.Getenv("MOTHX_ACP_BRIDGE_ECHO_PROMPT") == "1" {
				var params acpclient.PromptParams
				if err := json.Unmarshal(request.Params, &params); err == nil {
					parts := make([]string, 0, len(params.Prompt))
					for _, block := range params.Prompt {
						if block.Type == "text" {
							parts = append(parts, block.Text)
						}
					}
					text = strings.Join(parts, "\n")
				}
			}
			encodedText, _ := json.Marshal(text)
			_, _ = writer.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"bridge-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":` + string(encodedText) + `}}}}` + "\n")
			_ = writer.Flush()
			if os.Getenv("MOTHX_ACP_BRIDGE_PARTIAL") == "1" {
				os.Exit(1)
			}
			result = acpclient.PromptResult{StopReason: "end_turn"}
		case "session/close":
			result = map[string]any{}
		default:
			result = map[string]any{}
		}
		encodedResult, _ := json.Marshal(result)
		response := acpclient.Message{JSONRPC: "2.0", ID: request.ID}
		if rpcErr != nil {
			response.Error = rpcErr
		} else {
			response.Result = encodedResult
		}
		encodedResponse, _ := json.Marshal(response)
		_, _ = writer.Write(append(encodedResponse, '\n'))
		_ = writer.Flush()
		if request.Method == "session/close" {
			os.Exit(0)
		}
	}
}

func TestACPWorkerBridgeKeepsSafePartialOutputOnFailure(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	t.Setenv("MOTHX_ACP_BRIDGE_PARTIAL", "1")
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: config.ACPAgentProfile{
			ID: "helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerBridgeHelperProcess", "--"}, Enabled: true,
			Environment: []string{"MOTHX_ACP_BRIDGE_HELPER", "MOTHX_ACP_BRIDGE_PARTIAL"}, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		WorkDir:  t.TempDir(),
		Launcher: acpProcessLauncher{},
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings}, TargetProfile: "helper",
		},
	})
	if err == nil {
		t.Fatal("worker exit before the prompt response was treated as success")
	}
	if result.Output != "partial output" {
		t.Fatalf("partial output = %q, want the safe streamed text", result.Output)
	}
}

func TestACPWorkerBridgeInjectsAuthorizedMCPServers(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	t.Setenv("MOTHX_ACP_BRIDGE_ECHO_MCP", "1")
	prepareCalls := 0
	revoked := 0
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: config.ACPAgentProfile{
			ID: "helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerBridgeHelperProcess", "--"}, Enabled: true,
			Environment: []string{"MOTHX_ACP_BRIDGE_HELPER", "MOTHX_ACP_BRIDGE_ECHO_MCP"}, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		WorkDir:            t.TempDir(),
		Launcher:           acpProcessLauncher{},
		MCPBridgeAvailable: true,
		PrepareMCPServers: func(caps workerdriver.WorkerCapabilities) ([]any, func(), error) {
			prepareCalls++
			if !caps.MCPHTTP {
				t.Error("PrepareMCPServers called without a resolved MCPHTTP capability")
			}
			return []any{map[string]any{"type": "http", "name": "mothx-worker", "url": "http://127.0.0.1:1/mcp"}},
				func() { revoked++ }, nil
		},
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings}, TargetProfile: "helper",
		},
	})
	if err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	if prepareCalls != 1 {
		t.Fatalf("PrepareMCPServers calls = %d, want 1", prepareCalls)
	}
	if result.SessionID != "bridge-session-mcp" {
		t.Fatalf("session/new did not receive mcpServers: session = %q", result.SessionID)
	}
	if revoked != 1 {
		t.Fatalf("bridge revoke calls = %d, want 1 after Close", revoked)
	}
}

func TestACPWorkerBridgeSkipsMCPServersWithoutBridgePolicy(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	t.Setenv("MOTHX_ACP_BRIDGE_ECHO_MCP", "1")
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: config.ACPAgentProfile{
			ID: "helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerBridgeHelperProcess", "--"}, Enabled: true,
			Environment: []string{"MOTHX_ACP_BRIDGE_HELPER", "MOTHX_ACP_BRIDGE_ECHO_MCP"}, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		WorkDir:  t.TempDir(),
		Launcher: acpProcessLauncher{},
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings}, TargetProfile: "helper",
		},
	})
	if err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	if result.SessionID != "bridge-session-no-mcp" {
		t.Fatalf("session/new received an unauthorized bridge payload: session = %q", result.SessionID)
	}
}
