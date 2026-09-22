package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

func TestACPStdioProcessInitializeNewPromptClose(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	knowledgeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(knowledgeDir, "runtime.md"), []byte("# Runtime\n\nThe Runtime owns durable Runs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	providerRequests := make(chan string, 1)
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read provider request: %v", err)
		}
		providerRequests <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-process\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ACP smoke response\"},\"finish_reason\":null}]}\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-process\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer providerServer.Close()

	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "process-test"
	settings.DefaultModel = "process-model"
	settings.DefaultMode = "yolo"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"process-test": {
			APIKey:  "test-key",
			BaseURL: providerServer.URL + "/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "process-model", Name: "Process Model", ContextWindow: 32768, MaxTokens: 1024}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStdioProcessHelper$")
	cmd.Env = append(os.Environ(), "MOTHX_ACP_PROCESS_HELPER=1", "MOTHX_DIR="+configDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	assertACPResponseID(t, reader, 1)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	newSession := assertACPResponseID(t, reader, 2)
	result, ok := newSession["result"].(map[string]any)
	if !ok {
		t.Fatalf("session/new result = %#v", newSession["result"])
	}
	sessionID, ok := result["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session/new response = %#v, missing sessionId", newSession)
	}

	// Desktop inserts /expert-creater from ACP's available command projection.
	// The command must activate the same built-in Skill before the next prompt
	// reaches the current Agent.
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 90, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "/expert-creater"}}},
	})
	activation := assertACPResponseID(t, reader, 90)
	activationResult, _ := activation["result"].(map[string]any)
	if activationResult["stopReason"] != "end_turn" {
		t.Fatalf("expert creator activation = %#v", activation)
	}

	// Knowledge-base management is an ACP capability, not a Desktop-only
	// operation.  Create and index a deterministic base before the generic ACP
	// prompt references it.
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "mothx/manage/knowledge-bases/create",
		"params": map[string]any{"knowledgeBase": map[string]any{
			"name": "Runtime docs", "rootDir": knowledgeDir, "preprocessProfile": "documents",
			"mode": "yolo", "schedule": "manual", "enabled": true,
		}},
	})
	created := assertACPResponseID(t, reader, 3)
	createdResult, ok := created["result"].(map[string]any)
	if !ok {
		t.Fatalf("knowledge base create result = %#v", created["result"])
	}
	base, _ := createdResult["knowledgeBase"].(map[string]any)
	baseID, _ := base["id"].(string)
	if baseID == "" {
		t.Fatalf("knowledge base create result = %#v, missing id", createdResult)
	}
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "mothx/manage/knowledge-bases/scan", "params": map[string]any{"id": baseID},
	})
	assertACPResponseID(t, reader, 4)

	// The scan RPC only admits a background index job now. Poll the projected
	// status until the snapshot commits before the prompt requires the base.
	for attempt := 0; ; attempt++ {
		if attempt > 200 {
			t.Fatal("knowledge base index did not complete in time")
		}
		pollID := 40 + attempt
		sendACPRequest(t, stdin, map[string]any{
			"jsonrpc": "2.0", "id": pollID, "method": "mothx/manage/knowledge-bases/get", "params": map[string]any{"id": baseID},
		})
		polled := assertACPResponseID(t, reader, float64(pollID))
		polledResult, _ := polled["result"].(map[string]any)
		if polledResult != nil && polledResult["status"] == "completed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId":         sessionID,
			"prompt":            []map[string]any{{"type": "text", "text": "Who owns durable Runs?"}},
			"knowledgeBaseRefs": []map[string]any{{"knowledgeBaseId": baseID, "required": true}},
		},
	})
	notifications := assertACPResponseIDCollecting(t, reader, 5)
	if !containsACPNotification(notifications, "session/update", "agent_message_chunk", "ACP smoke response") {
		t.Fatalf("prompt notifications missing assistant message: %#v", notifications)
	}
	select {
	case request := <-providerRequests:
		if !strings.Contains(request, "Runtime-managed knowledge-base references") || !strings.Contains(request, "runtime.md") {
			t.Fatalf("generic ACP prompt did not include the Runtime knowledge context: %s", request)
		}
		if !strings.Contains(request, "## Active Skill: expert-creater") || !strings.Contains(request, "expert.json") {
			t.Fatalf("generic ACP prompt did not include the activated expert creator Skill: %s", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not receive the generic ACP prompt")
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "session/close", "params": map[string]any{"sessionId": sessionID}})
	assertACPResponseID(t, reader, 6)
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waitForACPProcess(t, cmd)
}

func containsACPNotification(messages []map[string]any, method, update, text string) bool {
	for _, message := range messages {
		if message["method"] != method {
			continue
		}
		params, _ := message["params"].(map[string]any)
		updatePayload, _ := params["update"].(map[string]any)
		if updatePayload["sessionUpdate"] != update {
			continue
		}
		content, _ := updatePayload["content"].(map[string]any)
		if content["text"] == text {
			return true
		}
	}
	return false
}

func waitForACPProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("ACP process did not exit after stdin EOF")
	}
}

func assertACPResponseIDCollecting(t *testing.T, reader *bufio.Reader, id float64) []map[string]any {
	t.Helper()
	var notifications []map[string]any
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal(err)
		}
		if got, ok := message["id"].(float64); ok && got == id {
			if rpcErr := message["error"]; rpcErr != nil {
				t.Fatalf("ACP response %v error: %#v", id, rpcErr)
			}
			return notifications
		}
		notifications = append(notifications, message)
	}
}
