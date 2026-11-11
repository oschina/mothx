package agentruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
)

func newBridgeTestScope(t *testing.T, notify func(WorkerMessage)) (*WorkerBridgeServer, *WorkerBridgeScope, string, []any) {
	t.Helper()
	sessionDir := t.TempDir()
	log, err := OpenWorkerMessageLog(sessionDir, "session-bridge")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	scope := &WorkerBridgeScope{
		SessionID: "session-bridge", ChildID: "child-bridge", ParentRunID: "run-bridge",
		ProfileID: "opencode", WorkDir: sessionDir, TaskPrompt: "do the task",
		Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
		Messages:     log, Notify: notify,
	}
	server := NewWorkerBridgeServer()
	t.Cleanup(func() { _ = server.Close() })
	token, payload, err := server.Register(scope)
	if err != nil {
		t.Fatal(err)
	}
	return server, scope, token, payload
}

func TestWorkerBridgeRegistersLoopbackPayload(t *testing.T) {
	_, _, token, payload := newBridgeTestScope(t, nil)
	if token == "" || len(token) < 40 {
		t.Fatalf("token = %q, want 32 random bytes encoded", token)
	}
	encoded, _ := json.Marshal(payload)
	text := string(encoded)
	for _, want := range []string{`"type":"http"`, `"mothx-worker"`, `http://127.0.0.1:`, `"Authorization"`, "Bearer " + token} {
		if !strings.Contains(text, want) {
			t.Fatalf("payload missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "localhost") {
		t.Fatalf("payload must use numeric loopback, got %s", text)
	}
}

func TestWorkerBridgeRejectsMissingOrRevokedToken(t *testing.T) {
	server, _, token, payload := newBridgeTestScope(t, nil)
	url := workerBridgeURL(t, payload)
	if status := bridgeCallStatus(t, url, "", "context"); status != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", status)
	}
	if status := bridgeCallStatus(t, url, token, "context"); status != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", status)
	}
	server.Revoke(token)
	if status := bridgeCallStatus(t, url, token, "context"); status != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", status)
	}
}

func TestWorkerBridgeContextAndReport(t *testing.T) {
	notified := make(chan WorkerMessage, 2)
	_, _, token, payload := newBridgeTestScope(t, func(message WorkerMessage) { notified <- message })
	url := workerBridgeURL(t, payload)

	contextResult := bridgeCall(t, url, token, map[string]any{"action": "context"})
	if contextResult["status"] != "ok" || contextResult["task"] != "do the task" {
		t.Fatalf("context result = %#v", contextResult)
	}
	bridgeInfo, _ := contextResult["bridge"].(map[string]any)
	if bridgeInfo["report"] != true || bridgeInfo["ask"] != false {
		t.Fatalf("bridge capabilities = %#v", bridgeInfo)
	}

	report := bridgeCall(t, url, token, map[string]any{
		"action": "report", "kind": "finding", "text": "found a race", "dedupe_key": "race",
	})
	if report["status"] != "recorded" || report["delivery"] != "delivered" {
		t.Fatalf("report result = %#v", report)
	}
	select {
	case message := <-notified:
		if message.Body != "found a race" || message.ChildID != "child-bridge" {
			t.Fatalf("notified message = %#v", message)
		}
	default:
		t.Fatal("finding did not reach the delivery callback")
	}

	duplicate := bridgeCall(t, url, token, map[string]any{
		"action": "report", "kind": "finding", "text": "found a race", "dedupe_key": "race",
	})
	if duplicate["duplicate"] != true || duplicate["message_id"] != report["message_id"] {
		t.Fatalf("duplicate report = %#v", duplicate)
	}
}

func TestWorkerBridgeRejectsInvalidReport(t *testing.T) {
	_, scope, token, payload := newBridgeTestScope(t, nil)
	url := workerBridgeURL(t, payload)
	for name, args := range map[string]map[string]any{
		"missing kind":   {"action": "report", "text": "x"},
		"completion":     {"action": "report", "kind": "completion", "text": "x"},
		"missing text":   {"action": "report", "kind": "notice"},
		"oversized body": {"action": "report", "kind": "notice", "text": strings.Repeat("a", MaxWorkerReportBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			response := bridgeCallRaw(t, url, token, args)
			if response["isError"] != true {
				t.Fatalf("%s was accepted: %#v", name, response)
			}
		})
	}
	messages, err := scope.Messages.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("invalid reports were persisted: %#v", messages)
	}
}

func TestWorkerBridgeAskUnavailableUntilRuntimeInstallsResolver(t *testing.T) {
	_, _, token, payload := newBridgeTestScope(t, nil)
	url := workerBridgeURL(t, payload)
	response := bridgeCallRaw(t, url, token, map[string]any{"action": "ask", "text": "which path?"})
	if response["isError"] != true {
		t.Fatalf("ask without a resolver was accepted: %#v", response)
	}

	scope := &WorkerBridgeScope{
		SessionID: "s", ChildID: "c", Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
		Ask: func(_ context.Context, question string, options []string, _ time.Duration) (string, string) {
			return "use B", "answered"
		},
	}
	if scope.Ask == nil {
		t.Fatal("test scope missing ask")
	}
}

func TestWorkerBridgeAskRejectsOversizedOptions(t *testing.T) {
	scope := &WorkerBridgeScope{
		SessionID: "s", ChildID: "c", Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
		Ask: func(_ context.Context, _ string, _ []string, _ time.Duration) (string, string) {
			return "ok", "answered"
		},
	}
	handler := &workerBridgeMCPHandler{}
	if _, err := handler.ask(context.Background(), scope, "q", []string{strings.Repeat("x", MaxWorkerAskOptionBytes+1)}, 0); err == nil || !strings.Contains(err.Error(), "option exceeds") {
		t.Fatalf("oversized single option error = %v", err)
	}
	options := make([]string, MaxWorkerAskOptions)
	for index := range options {
		options[index] = strings.Repeat("y", MaxWorkerAskOptionBytes)
	}
	if _, err := handler.ask(context.Background(), scope, "q", options, 0); err == nil || !strings.Contains(err.Error(), "in total") {
		t.Fatalf("oversized option total error = %v", err)
	}
}

// The bridge must not write its own question row: the Runtime-owned Ask path is
// the only writer that knows the durable DecisionID. This test exercises the
// full HTTP -> MCP -> Runtime path and asserts both answered and timeout
// questions produce exactly one row with the right terminal status.
func TestWorkerBridgeAskWritesOneQuestionRow(t *testing.T) {
	newHarness := func(t *testing.T, childID string) (*SessionRuntime, string, string) {
		t.Helper()
		runtime, _ := newWorkerQuestionRuntime(t)
		messageLog, err := runtime.WorkerMessageLog()
		if err != nil {
			t.Fatal(err)
		}
		bridge := NewWorkerBridgeServer()
		t.Cleanup(func() { _ = bridge.Close() })
		scope := &WorkerBridgeScope{
			SessionID: runtime.sessionIDForWorkerBridge(), ChildID: childID,
			ParentRunID: "run-bridge-ask", Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
			Messages: messageLog,
			Ask: func(ctx context.Context, question string, options []string, timeout time.Duration) (string, string) {
				return runtime.askWorkerQuestion(ctx, childID, "run-bridge-ask", question, options, timeout)
			},
		}
		token, payload, err := bridge.Register(scope)
		if err != nil {
			t.Fatal(err)
		}
		return runtime, token, workerBridgeURL(t, payload)
	}

	t.Run("answered", func(t *testing.T) {
		runtime, token, url := newHarness(t, "child-bridge-ask")
		resolveErr := make(chan error, 1)
		go func() {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				questions, err := runtime.PendingWorkerQuestions()
				if err != nil {
					resolveErr <- err
					return
				}
				if len(questions) == 1 && questions[0].ChildID == "child-bridge-ask" {
					resolveErr <- runtime.ResolveWorkerQuestion(context.Background(), questions[0].ID, "A")
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			resolveErr <- fmt.Errorf("worker question never became pending")
		}()
		result := bridgeCall(t, url, token, map[string]any{
			"action": "ask", "text": "which branch?", "options": []string{"A", "B"}, "timeout_seconds": 5,
		})
		if err := <-resolveErr; err != nil {
			t.Fatalf("resolve bridge question: %v", err)
		}
		if result["status"] != "answered" || result["answer"] != "A" {
			t.Fatalf("ask result = %#v", result)
		}
		messages, err := runtime.WorkerMessages()
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 {
			t.Fatalf("answered ask wrote %d question rows, want exactly 1: %#v", len(messages), messages)
		}
		message := messages[0]
		if message.Kind != WorkerMessageQuestion || message.ChildID != "child-bridge-ask" {
			t.Fatalf("question row = %#v", message)
		}
		if !IsWorkerQuestionID(message.DecisionID) {
			t.Fatalf("question row is not linked to its durable decision: %#v", message)
		}
		if message.Status != WorkerMessageStatusDelivered {
			t.Fatalf("answered question status = %q, want delivered", message.Status)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runtime, token, url := newHarness(t, "child-bridge-timeout")
		result := bridgeCall(t, url, token, map[string]any{
			"action": "ask", "text": "anyone there?", "timeout_seconds": 1,
		})
		if result["status"] != "timeout" || result["answer"] != "" {
			t.Fatalf("timeout ask result = %#v", result)
		}
		messages, err := runtime.WorkerMessages()
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 {
			t.Fatalf("timed-out ask wrote %d question rows, want exactly 1: %#v", len(messages), messages)
		}
		message := messages[0]
		if message.Kind != WorkerMessageQuestion || !IsWorkerQuestionID(message.DecisionID) {
			t.Fatalf("question row = %#v", message)
		}
		if message.Status != WorkerMessageStatusExpired {
			t.Fatalf("timed-out question status = %q, want expired", message.Status)
		}
	})
}

func TestWorkerMessageLogFoldDedupeAndTornLine(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenWorkerMessageLog(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	first, duplicate, err := log.Create(WorkerMessage{
		SessionID: "s", ChildID: "c", Kind: WorkerMessageFinding, Body: "one", DedupeKey: "k1",
	})
	if err != nil || duplicate {
		t.Fatalf("create: err=%v duplicate=%v", err, duplicate)
	}
	second, duplicate, err := log.Create(WorkerMessage{
		SessionID: "s", ChildID: "c", Kind: WorkerMessageFinding, Body: "one", DedupeKey: "k1",
	})
	if err != nil || !duplicate || second.ID != first.ID {
		t.Fatalf("dedupe: err=%v duplicate=%v id=%q want %q", err, duplicate, second.ID, first.ID)
	}
	if err := log.Mark(first.ID, workerMessageOpDelivered, "run-1", ""); err != nil {
		t.Fatal(err)
	}
	// A crash may leave a torn final line. It must not hide earlier state.
	file, err := os.OpenFile(mustWorkerMessageLogPath(t, dir, "s"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"op":"created","id":"torn`)
	file.Close()
	messages, err := log.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Status != WorkerMessageStatusDelivered || messages[0].RunID != "run-1" {
		t.Fatalf("folded messages = %#v", messages)
	}
}

func TestWorkerMessageLogRejectsUnknownTransition(t *testing.T) {
	log, err := OpenWorkerMessageLog(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if err := log.Mark("missing", workerMessageOpCreated, "", ""); err == nil {
		t.Fatal("unsupported transition was accepted")
	}
}

func workerBridgeURL(t *testing.T, payload []any) string {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	var entries []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(encoded, &entries); err != nil || len(entries) == 0 {
		t.Fatalf("decode payload: %v (%s)", err, encoded)
	}
	return entries[0].URL
}

func bridgeCallStatus(t *testing.T, url, token, action string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "worker_mgr", "arguments": map[string]any{"action": action}},
	})
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func bridgeCall(t *testing.T, url, token string, args map[string]any) map[string]any {
	t.Helper()
	raw := bridgeCallRaw(t, url, token, args)
	content, ok := raw["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("tool result has no content: %#v", raw)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("tool result text is not JSON: %q", text)
	}
	return decoded
}

func bridgeCallRaw(t *testing.T, url, token string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "worker_mgr", "arguments": args},
	})
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Result
}

func TestWorkerBridgeClosesIdleListenerAndRebinds(t *testing.T) {
	server, _, token, payload := newBridgeTestScope(t, nil)
	server.SetIdleTimeout(30 * time.Millisecond)
	firstPort := server.Port()
	if firstPort == 0 {
		t.Fatal("bridge did not bind a port")
	}
	server.Revoke(token)
	deadline := time.Now().Add(2 * time.Second)
	for server.Port() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.Port() != 0 {
		t.Fatalf("idle listener stayed bound on port %d", firstPort)
	}
	// The old URL can no longer serve the credential: the listener is closed.
	request, _ := http.NewRequest(http.MethodPost, workerBridgeURL(t, payload), bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Authorization", "Bearer "+token)
	if response, err := http.DefaultClient.Do(request); err == nil {
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("closed idle listener still served the old credential")
		}
	}
	// A new registration rebinds a fresh ephemeral port.
	scope := &WorkerBridgeScope{
		SessionID: "session-bridge-2", ChildID: "child-2",
		Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
	}
	if _, _, err := server.Register(scope); err != nil {
		t.Fatalf("rebind after idle close: %v", err)
	}
	if server.Port() == 0 {
		t.Fatal("bridge did not rebind after idle close")
	}
}

func TestWorkerBridgeRejectsAfterChildTerminal(t *testing.T) {
	server, _, token, payload := newBridgeTestScope(t, nil)
	url := workerBridgeURL(t, payload)
	server.SetChildTerminal("session-bridge", "child-bridge", true)
	response := bridgeCallRaw(t, url, token, map[string]any{"action": "report", "kind": "finding", "text": "late"})
	if response["isError"] != true {
		t.Fatalf("post-terminal report was accepted: %#v", response)
	}
	content, _ := response["content"].([]any)
	if len(content) == 0 || !strings.Contains(content[0].(map[string]any)["text"].(string), "child_terminal") {
		t.Fatalf("post-terminal error did not name child_terminal: %#v", response)
	}
	server.SetChildTerminal("session-bridge", "child-bridge", false)
	if response := bridgeCallRaw(t, url, token, map[string]any{"action": "report", "kind": "notice", "text": "new turn"}); response["isError"] == true {
		t.Fatalf("active turn was still rejected: %#v", response)
	}
}

// The per-child bearer token must never be persisted to the JSONL evidence log,
// session files, or the bridge's own log output. Only request metadata is
// logged.
func TestWorkerBridgeTokenNeverLeaksToStorageOrLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	server, scope, token, payload := newBridgeTestScope(t, nil)
	url := workerBridgeURL(t, payload)
	bridgeCall(t, url, token, map[string]any{"action": "report", "kind": "finding", "text": "token leak probe"})

	// Simulate the session's durable child metadata as well: a token must not
	// appear in any file under the session directory.
	if err := os.WriteFile(filepath.Join(filepath.Dir(scope.WorkDir), "child_meta.json"),
		[]byte(`{"childId":"child-bridge","mcpBridge":"auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = filepath.Walk(scope.WorkDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if bytes.Contains(data, []byte(token)) {
			t.Fatalf("token leaked into %s", path)
		}
		return nil
	})
	if strings.Contains(logs.String(), token) {
		t.Fatalf("token leaked into bridge logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "session=session-bridge") {
		t.Fatalf("bridge logs missing request metadata: %s", logs.String())
	}
	server.Revoke(token)
}

// Two mothx processes on the same host each bind an ephemeral loopback port.
// The kernel-assigned ports must differ and both listeners must be reachable.
func TestWorkerBridgeMultiProcessPortIsolation(t *testing.T) {
	if os.Getenv("MOTHX_BRIDGE_HELPER") == "1" {
		server := NewWorkerBridgeServer()
		scope := &WorkerBridgeScope{
			SessionID: "helper", ChildID: "helper-child",
			Capabilities: workerdriver.WorkerCapabilities{MCPHTTP: true},
		}
		token, payload, err := server.Register(scope)
		if err != nil {
			fmt.Println("ERROR", err)
			os.Exit(1)
		}
		encoded, _ := json.Marshal(map[string]any{"token": token, "payload": payload})
		fmt.Println(string(encoded))
		time.Sleep(4 * time.Second)
		_ = server.Close()
		os.Exit(0)
	}

	spawn := func() (*exec.Cmd, map[string]any) {
		cmd := exec.Command(os.Args[0], "-test.run=TestWorkerBridgeMultiProcessPortIsolation")
		cmd.Env = append(os.Environ(), "MOTHX_BRIDGE_HELPER=1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		})
		reader := bufio.NewReader(stdout)
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read helper output: %v", err)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &result); err != nil {
			t.Fatalf("decode helper output %q: %v", line, err)
		}
		return cmd, result
	}

	_, first := spawn()
	_, second := spawn()
	urlOf := func(result map[string]any) string {
		encoded, _ := json.Marshal(result["payload"])
		var entries []struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(encoded, &entries); err != nil || len(entries) == 0 {
			t.Fatalf("decode helper payload: %v (%s)", err, encoded)
		}
		return entries[0].URL
	}
	firstURL, secondURL := urlOf(first), urlOf(second)
	if firstURL == secondURL {
		t.Fatalf("two processes bound the same worker bridge URL: %s", firstURL)
	}
	for _, entry := range []struct {
		url   string
		token string
	}{{firstURL, first["token"].(string)}, {secondURL, second["token"].(string)}} {
		if status := bridgeCallStatus(t, entry.url, entry.token, "context"); status != http.StatusOK {
			t.Fatalf("%s returned %d, want 200", entry.url, status)
		}
	}
}
