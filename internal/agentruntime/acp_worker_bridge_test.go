package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	acpclient "github.com/oschina/mothx/internal/acp/client"
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
		WorkDir: t.TempDir(),
		Task:    "inspect this task",
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
	select {
	case message := <-notifications:
		if message.Method != "session/update" {
			t.Fatalf("notification = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("bridge did not forward session/update")
	}
}

func TestACPWorkerBridgeHelperProcess(t *testing.T) {
	if os.Getenv("MOTHX_ACP_BRIDGE_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
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
			result = acpclient.NewSessionResult{SessionID: "bridge-session"}
		case "session/prompt":
			_, _ = writer.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"bridge-session","update":{"sessionUpdate":"agent_message_chunk"}}}` + "\n")
			_ = writer.Flush()
			result = acpclient.PromptResult{StopReason: "end_turn"}
		case "session/close":
			result = map[string]any{}
		default:
			result = map[string]any{}
		}
		encodedResult, _ := json.Marshal(result)
		response, _ := json.Marshal(acpclient.Message{JSONRPC: "2.0", ID: request.ID, Result: encodedResult})
		_, _ = writer.Write(append(response, '\n'))
		_ = writer.Flush()
		if request.Method == "session/close" {
			os.Exit(0)
		}
	}
}
