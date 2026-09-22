package acp

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// This is a wire-level Go ACP client. It intentionally uses only the standard
// library so Harbor compatibility remains an external/manual check rather
// than a dependency of the default test or CI graph.
func TestACPStdioProcessSessionModelOptions(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	extraDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "process-test"
	settings.DefaultModel = "process-model-1"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"process-test": {
			APIKey:  "test-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "process-model-1", Name: "Process Model 1", ContextWindow: 32768, MaxTokens: 1024},
				{ID: "process-model-2", Name: "Process Model 2", ContextWindow: 65536, MaxTokens: 2048},
			},
		},
		"process-alt": {
			APIKey:  "test-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "alt-model", Name: "Alt Model", ContextWindow: 32768, MaxTokens: 1024},
			},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPConfigProcessHelper$")
	cmd.Env = append(os.Environ(),
		"MOTHX_ACP_CONFIG_PROCESS_HELPER=1",
		"MOTHX_DIR="+configDir,
		"HARBOR_ACP_REQUESTED_MODEL=process-test/process-model-2",
	)
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

	if _, err := stdin.Write([]byte(`{"jsonrpc":"2.0"` + "\n")); err != nil {
		t.Fatal(err)
	}
	parseError := readACPResponseWithNullID(t, reader)
	if code := parseError["error"].(map[string]any)["code"]; code != float64(-32700) || parseError["id"] != nil {
		t.Fatalf("parse error response = %#v, want -32700 with null id", parseError)
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	assertACPResponseID(t, reader, 1)

	newSession := func(id float64, additionalDirectories []string) (string, []any) {
		t.Helper()
		sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/new", "params": map[string]any{"cwd": workDir, "additionalDirectories": additionalDirectories}})
		response := assertACPResponseID(t, reader, id)
		result := response["result"].(map[string]any)
		sessionID, ok := result["sessionId"].(string)
		if !ok || sessionID == "" {
			t.Fatalf("session/new response = %#v", response)
		}
		options, _ := result["configOptions"].([]any)
		return sessionID, options
	}

	first, firstOptions := newSession(2, []string{extraDir})
	if got := configOptionCurrent(firstOptions, "model"); got != "process-test/process-model-2" {
		t.Fatalf("first session model = %q, want requested model process-test/process-model-2", got)
	}
	if got := configOptionCurrent(firstOptions, "provider"); got != "process-test" {
		t.Fatalf("first session provider = %q, want process-test", got)
	}
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 35, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "provider", "value": "process-alt"},
	})
	altResponse := assertACPResponseID(t, reader, 35)
	altOptions := altResponse["result"].(map[string]any)["configOptions"].([]any)
	if got := configOptionCurrent(altOptions, "provider"); got != "process-alt" || configOptionCurrent(altOptions, "model") != "process-alt/alt-model" {
		t.Fatalf("provider switch options = %#v", altOptions)
	}
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 36, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "provider", "value": "process-test"},
	})
	assertACPResponseID(t, reader, 36)
	second, _ := newSession(3, nil)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 31, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	listed := assertACPResponseID(t, reader, 31)["result"].(map[string]any)["sessions"].([]any)
	foundAdditional := false
	for _, raw := range listed {
		entry := raw.(map[string]any)
		if entry["sessionId"] == first {
			roots, _ := entry["additionalDirectories"].([]any)
			foundAdditional = len(roots) == 1 && roots[0] == extraDir
		}
	}
	if !foundAdditional {
		t.Fatalf("session/list did not report additional directory for %s: %#v", first, listed)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "model", "value": "process-test/process-model-1"},
	})
	setResponse := assertACPResponseID(t, reader, 4)
	if options := setResponse["result"].(map[string]any)["configOptions"]; options == nil {
		t.Fatalf("set_config_option response missing configOptions: %#v", setResponse)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "model", "value": "process-test/does-not-exist"},
	})
	invalid := readACPResponse(t, reader, 5)
	if code := invalid["error"].(map[string]any)["code"]; code != float64(-32602) {
		t.Fatalf("invalid model error code = %#v, want -32602", code)
	}

	// Expert selection is a Runtime config option: binding a fresh session is
	// in-place, while replacing that non-empty identity must fork a child.
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 37, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "expert", "value": "software-company"},
	})
	expertBound := assertACPResponseID(t, reader, 37)
	if got := configOptionCurrent(expertBound["result"].(map[string]any)["configOptions"].([]any), "expert"); got != "software-company" {
		t.Fatalf("bound expert option = %q, want software-company", got)
	}
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 38, "method": "session/set_config_option",
		"params": map[string]any{"sessionId": first, "configId": "expert", "value": "frontend-developer"},
	})
	switchRejected := readACPResponse(t, reader, 38)
	if code := switchRejected["error"].(map[string]any)["code"]; code != float64(-32602) {
		t.Fatalf("in-place expert switch code = %#v, want -32602", code)
	}
	// Forking deliberately requires a completed conversation turn. Seed the
	// parent through the canonical session API so this process test exercises
	// the ACP expertId fork projection rather than a rejected empty branch.
	// First release ACP's idle runtime lease; the persisted parent remains
	// available for a later fork exactly as it would after an app restart.
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 381, "method": "session/close", "params": map[string]any{"sessionId": first}})
	assertACPResponseID(t, reader, 381)
	parent, err := session.OpenByIDExact(settings.SessionDir, first)
	if err != nil {
		t.Fatalf("open fork parent: %v", err)
	}
	guard, err := session.AcquireExecutionAdmission(settings.SessionDir, first)
	if err != nil {
		t.Fatalf("acquire fork parent admission: %v", err)
	}
	const forkTurnID = "expert-config-fork-turn"
	if err := session.StartConversationTurn(settings.SessionDir, session.ConversationTurn{ID: forkTurnID, SessionID: first, IntentID: "expert-config-fork-intent", RunID: "expert-config-fork-run"}); err != nil {
		guard.Release()
		t.Fatalf("start fork parent turn: %v", err)
	}
	if err := parent.Reload(); err != nil {
		guard.Release()
		t.Fatalf("reload fork parent: %v", err)
	}
	if _, err := parent.AppendMessage(provider.NewUserMessage("fork expert identity")); err != nil {
		guard.Release()
		t.Fatalf("append fork parent message: %v", err)
	}
	if err := session.EndConversationTurn(settings.SessionDir, first, forkTurnID, "completed", "end_turn", time.Now()); err != nil {
		guard.Release()
		t.Fatalf("finish fork parent turn: %v", err)
	}
	guard.Release()
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 39, "method": "session/fork",
		"params": map[string]any{"sessionId": first, "cwd": workDir, "expertId": "frontend-developer", "_meta": map[string]any{"mothx": map[string]any{"workspace": map[string]any{"cwd": workDir}, "parentSessionId": first}}},
	})
	forked := assertACPResponseID(t, reader, 39)["result"].(map[string]any)
	forkedID, _ := forked["sessionId"].(string)
	if forkedID == "" || forkedID == first {
		t.Fatalf("expert fork result = %#v", forked)
	}
	if got := configOptionCurrent(forked["configOptions"].([]any), "expert"); got != "frontend-developer" {
		t.Fatalf("forked expert option = %q, want frontend-developer", got)
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "session/resume", "params": map[string]any{"sessionId": second, "cwd": workDir, "additionalDirectories": []string{extraDir}}})
	secondResume := assertACPResponseID(t, reader, 6)
	options := secondResume["result"].(map[string]any)["configOptions"].([]any)
	if got := configOptionCurrent(options, "model"); got != "process-test/process-model-2" {
		t.Fatalf("second session model = %q, want requested model process-test/process-model-2", got)
	}

	for id, sessionID := range map[float64]string{8: second, 40: forkedID} {
		sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/close", "params": map[string]any{"sessionId": sessionID}})
		assertACPResponseID(t, reader, id)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("ACP config process did not exit after stdin EOF")
	}
}

func TestACPConfigProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_ACP_CONFIG_PROCESS_HELPER") != "1" {
		return
	}
	if err := Run(RunOptions{}); err != nil {
		t.Fatal(err)
	}
}

func readACPResponse(t *testing.T, reader *bufio.Reader, id float64) map[string]any {
	t.Helper()
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
			return message
		}
	}
}

func readACPResponseWithNullID(t *testing.T, reader *bufio.Reader) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(line, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func configOptionCurrent(options []any, id string) string {
	for _, raw := range options {
		option, _ := raw.(map[string]any)
		if option["id"] == id {
			value, _ := option["currentValue"].(string)
			return value
		}
	}
	return ""
}
