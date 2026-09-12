package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
)

const acpTestOnePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl8P6sAAAAASUVORK5CYII="

func TestACPInitializeDeclaresPromptCapabilitiesMatchingImplementation(t *testing.T) {
	var output bytes.Buffer
	s := &server{w: &output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime)}
	s.handleInitialize(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"protocolVersion":1}`)})
	var response struct {
		Result struct {
			AgentCapabilities struct {
				PromptCapabilities promptCaps `json:"promptCapabilities"`
			} `json:"agentCapabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("parse initialize response %q: %v", output.String(), err)
	}
	caps := response.Result.AgentCapabilities.PromptCapabilities
	if !caps.Image || !caps.Audio || !caps.EmbeddedContext {
		t.Fatalf("promptCapabilities = %#v, want image/audio/embeddedContext all true", caps)
	}

	// The declaration must match the implementation: every declared content
	// block type is accepted by promptToIngresses and normalized into the
	// Runtime input contract instead of prompt text.
	workspace := t.TempDir()
	pngBytes, err := base64.StdEncoding.DecodeString(acpTestOnePixelPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(workspace, "pixel.png")
	if err := os.WriteFile(pngPath, pngBytes, 0600); err != nil {
		t.Fatal(err)
	}
	text, ingresses, err := promptToIngresses([]contentBlock{
		{Type: "text", Text: "describe"},
		{Type: "image", MimeType: "image/png", Data: acpTestOnePixelPNGBase64},
		{Type: "audio", MimeType: "audio/wav", Data: base64.StdEncoding.EncodeToString([]byte("RIFFdata"))},
		{Type: "resource", Name: "notes.md", MimeType: "text/markdown", Data: base64.StdEncoding.EncodeToString([]byte("# notes"))},
		{Type: "resource_link", Name: "pixel", URI: "file://" + pngPath, MimeType: "image/png"},
	}, workspace, nil, "acp:caps")
	if err != nil {
		t.Fatalf("promptToIngresses must accept every declared capability: %v", err)
	}
	if text != "describe" {
		t.Fatalf("prompt text = %q, want only the text block", text)
	}
	if len(ingresses) != 4 {
		t.Fatalf("ingresses = %#v, want image/audio/resource/resource_link materialization", ingresses)
	}
	wantKinds := []agentruntime.AttachmentKind{
		agentruntime.AttachmentImage, agentruntime.AttachmentAudio,
		agentruntime.AttachmentFile, agentruntime.AttachmentImage,
	}
	for index, want := range wantKinds {
		if ingresses[index].Kind != want {
			t.Fatalf("ingress %d kind = %q, want %q", index, ingresses[index].Kind, want)
		}
	}
}

func TestACPInitializeProjectsArtifactSwitchDefaultAndOverride(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%v", enabled), func(t *testing.T) {
			var output bytes.Buffer
			s := &server{
				artifact: enabled,
				w:        &output,
				pending:  make(map[string]chan json.RawMessage),
				sessions: make(map[string]*sessionRuntime),
			}
			s.handleInitialize(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"protocolVersion":1}`)})

			var response map[string]any
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatalf("parse initialize response %q: %v", output.String(), err)
			}
			result, _ := response["result"].(map[string]any)
			capabilities, _ := result["agentCapabilities"].(map[string]any)
			meta, _ := capabilities["_meta"].(map[string]any)
			mothx, _ := meta[mothxExtensionNamespace].(map[string]any)
			if got, ok := mothx["artifactEnabled"].(bool); !ok || got != enabled {
				t.Fatalf("artifactEnabled = %#v, want %v", mothx["artifactEnabled"], enabled)
			}
		})
	}
}

func TestACPDecisionTimeoutFallsBackToDefaultsAndHonorsConfiguration(t *testing.T) {
	if defaultPermissionTimeout != 5*time.Minute || defaultQuestionTimeout != 5*time.Minute {
		t.Fatalf("decision timeout defaults = %v / %v, want 5m / 5m (approval aligned with question)", defaultPermissionTimeout, defaultQuestionTimeout)
	}
	zero := &server{}
	if got := zero.effectivePermissionTimeout(); got != defaultPermissionTimeout {
		t.Fatalf("default permission timeout = %v, want 5m", got)
	}
	if got := zero.effectiveQuestionTimeout(); got != defaultQuestionTimeout {
		t.Fatalf("default question timeout = %v, want 5m", got)
	}
	configured := &server{permissionTimeout: 30 * time.Minute, questionTimeout: 45 * time.Minute}
	if got := configured.effectivePermissionTimeout(); got != 30*time.Minute {
		t.Fatalf("configured permission timeout = %v, want 30m", got)
	}
	if got := configured.effectiveQuestionTimeout(); got != 45*time.Minute {
		t.Fatalf("configured question timeout = %v, want 45m", got)
	}
	negative := &server{permissionTimeout: -time.Second, questionTimeout: -time.Minute}
	if got := negative.effectivePermissionTimeout(); got != defaultPermissionTimeout {
		t.Fatalf("negative permission timeout = %v, want default fallback", got)
	}
	if got := negative.effectiveQuestionTimeout(); got != defaultQuestionTimeout {
		t.Fatalf("negative question timeout = %v, want default fallback", got)
	}
}

func TestACPRequestPermissionHonorsConfiguredTimeout(t *testing.T) {
	output := &syncedBuffer{}
	s := &server{w: output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime), permissionTimeout: 40 * time.Millisecond}
	start := time.Now()
	if s.requestPermissionContext(context.Background(), "session-timeout", "call-1", "bash", map[string]any{"command": "true"}) {
		t.Fatal("timed-out approval unexpectedly allowed the call")
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("permission timeout elapsed = %v, want the configured 40ms instead of the 5m default", elapsed)
	}
	if !strings.Contains(output.String(), `"method":"session/request_permission"`) {
		t.Fatalf("permission request was not projected: %s", output.String())
	}
	// The additive decision_deadline reminder fires at min(60s, timeout/2).
	deadline := findACPDeadlineEvent(t, output.String(), "approval")
	if deadline == nil {
		t.Fatalf("no decision_deadline reminder was projected: %s", output.String())
	}
}

// findACPDeadlineEvent scans newline-delimited wire output for a
// decision_deadline session event of the given kind and returns its params.
func findACPDeadlineEvent(t *testing.T, output, kind string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var message map[string]any
		if json.Unmarshal([]byte(line), &message) != nil {
			continue
		}
		if message["method"] != "_mothx/session_event" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		if params["event"] == "decision_deadline" && params["kind"] == kind {
			return params
		}
	}
	return nil
}

func TestACPRequestQuestionHonorsConfiguredTimeout(t *testing.T) {
	output := &syncedBuffer{}
	s := &server{w: output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime), questionTimeout: 40 * time.Millisecond}
	start := time.Now()
	if answer := s.requestQuestion(context.Background(), "session-timeout", "continue?", []string{"yes"}, ""); answer != "" {
		t.Fatalf("timed-out question answered %q", answer)
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("question timeout elapsed = %v, want the configured 40ms instead of the 5m default", elapsed)
	}
	var notification struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params questionRequest `json:"params"`
	}
	found := false
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"_mothx/request_question"`) {
			continue
		}
		if err := json.Unmarshal([]byte(line), &notification); err != nil {
			t.Fatalf("parse question request %q: %v", line, err)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("question request was not projected: %s", output.String())
	}
	if notification.Method != "_mothx/request_question" {
		t.Fatalf("question method = %q", notification.Method)
	}
	if notification.Params.TimeoutMs != 40 {
		t.Fatalf("question timeoutMs = %d, want the configured 40ms deadline", notification.Params.TimeoutMs)
	}
	// The additive decision_deadline reminder fires before the timeout and
	// carries the request identity so clients can render a countdown.
	deadline := findACPDeadlineEvent(t, output.String(), "question")
	if deadline == nil {
		t.Fatalf("no decision_deadline reminder was projected: %s", output.String())
	}
	if deadline["requestId"] != notification.ID {
		t.Fatalf("decision_deadline requestId = %#v, want %q", deadline["requestId"], notification.ID)
	}
	if _, ok := deadline["deadline"].(string); !ok {
		t.Fatalf("decision_deadline deadline = %#v, want an RFC3339 timestamp", deadline["deadline"])
	}
	if _, ok := deadline["remainingMs"].(float64); !ok {
		t.Fatalf("decision_deadline remainingMs = %#v, want a number", deadline["remainingMs"])
	}
}

func TestACPStdioProcessArtifactProjectionFetchAndLoadReplay(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	artifactContent := []byte("artifact wire content")
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), artifactContent, 0600); err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		var payloads []string
		switch call {
		case 1:
			payloads = []string{
				`{"id":"chatcmpl-artifact","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_publish","type":"function","function":{"name":"publish_artifact","arguments":"{\"path\":\"report.txt\"}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-artifact","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
		default:
			payloads = []string{
				`{"id":"chatcmpl-artifact-final","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Published the report."},"finish_reason":null}]}`,
				`{"id":"chatcmpl-artifact-final","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
		}
		for _, payload := range payloads {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer providerServer.Close()

	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "artifact-test"
	settings.DefaultModel = "artifact-model"
	settings.DefaultMode = "yolo"
	settings.EnableACPArtifact = config.BoolPtr(true)
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"artifact-test": {
			APIKey:  "test-key",
			BaseURL: providerServer.URL + "/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "artifact-model", Name: "Artifact Model", ContextWindow: 32768, MaxTokens: 1024}},
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
	initialize := assertACPResponseID(t, reader, 1)
	caps := initialize["result"].(map[string]any)["agentCapabilities"].(map[string]any)["promptCapabilities"].(map[string]any)
	if caps["image"] != true || caps["audio"] != true || caps["embeddedContext"] != true {
		t.Fatalf("wire promptCapabilities = %#v, want all true", caps)
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	newSession := assertACPResponseID(t, reader, 2)
	sessionID, ok := newSession["result"].(map[string]any)["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session/new response = %#v, missing sessionId", newSession)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": "Publish report.txt"}},
		},
	})
	promptNotifications := assertACPResponseIDCollecting(t, reader, 3)
	artifact := findACPArtifactUpdate(t, promptNotifications)
	attachmentID, _ := artifact["artifactId"].(string)
	if attachmentID == "" {
		t.Fatalf("artifact update = %#v, missing artifactId", artifact)
	}
	if artifact["filename"] != "report.txt" || artifact["kind"] != "file" || artifact["status"] != "generated" {
		t.Fatalf("artifact update = %#v", artifact)
	}
	if artifact["mediaType"] != "text/plain; charset=utf-8" {
		t.Fatalf("artifact mediaType = %#v", artifact["mediaType"])
	}
	if size, _ := artifact["size"].(float64); int(size) != len(artifactContent) {
		t.Fatalf("artifact size = %#v, want %d", artifact["size"], len(artifactContent))
	}
	if runID, _ := artifact["runId"].(string); !strings.HasPrefix(runID, "acp_") {
		t.Fatalf("artifact runId = %#v, want the canonical ACP run identity", artifact["runId"])
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "mothx/attachment/fetch",
		"params": map[string]any{"sessionId": sessionID, "attachmentId": attachmentID},
	})
	fetch := assertACPResponseID(t, reader, 4)
	result, ok := fetch["result"].(map[string]any)
	if !ok {
		t.Fatalf("fetch response = %#v", fetch)
	}
	if result["filename"] != "report.txt" || result["mediaType"] != "text/plain; charset=utf-8" {
		t.Fatalf("fetch result = %#v", result)
	}
	if size, _ := result["size"].(float64); int64(size) != int64(len(artifactContent)) {
		t.Fatalf("fetch size = %#v, want %d", result["size"], len(artifactContent))
	}
	contentBase64, _ := result["contentBase64"].(string)
	content, err := base64.StdEncoding.DecodeString(contentBase64)
	if err != nil || !bytes.Equal(content, artifactContent) {
		t.Fatalf("fetch content = %q, %v, want %q", content, err, artifactContent)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 5, "method": "mothx/attachment/fetch",
		"params": map[string]any{"sessionId": sessionID, "attachmentId": "missing"},
	})
	missing := readACPMessageUntilID(t, reader, 5)
	assertACPStructuredErrorCode(t, missing, "attachment_not_found")

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 6, "method": "mothx/attachment/fetch",
		"params": map[string]any{"sessionId": "foreign-session", "attachmentId": attachmentID},
	})
	foreign := readACPMessageUntilID(t, reader, 6)
	assertACPStructuredErrorCode(t, foreign, "attachment_not_found")

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": "mothx/attachment/fetch",
		"params": map[string]any{"sessionId": sessionID},
	})
	invalid := readACPMessageUntilID(t, reader, 7)
	if errObj, _ := invalid["error"].(map[string]any); errObj == nil || errObj["code"] != float64(-32602) {
		t.Fatalf("invalid fetch params response = %#v, want -32602", invalid)
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "session/close", "params": map[string]any{"sessionId": sessionID}})
	assertACPResponseID(t, reader, 8)

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "session/load", "params": map[string]any{"sessionId": sessionID, "cwd": workDir}})
	loadNotifications := assertACPResponseIDCollecting(t, reader, 9)
	replayed := findACPArtifactUpdate(t, loadNotifications)
	if replayed["artifactId"] != attachmentID || replayed["status"] != "generated" || replayed["filename"] != "report.txt" {
		t.Fatalf("replayed artifact = %#v, want the persisted artifact %s", replayed, attachmentID)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waitForACPProcess(t, cmd)
}

func TestACPStdioProcessImagePromptReachesProviderAsImageContent(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	var providerCalls atomic.Int32
	var firstBody, secondBody atomic.Value
	manifestPath := regexp.MustCompile(`- path: ([^\\"]+)`)
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			firstBody.Store(string(body))
			// The declared image capability must be real: the image block is
			// accepted and materialized by the Runtime input contract, then
			// advertised to the model as a managed input file. Ask for it back
			// with the read tool so the canonical flow carries it to the
			// provider as native image content.
			match := manifestPath.FindStringSubmatch(string(body))
			if match == nil {
				t.Errorf("first provider request has no materialized input manifest: %s", body)
				match = []string{"", ".mothx/missing.png"}
			}
			args, _ := json.Marshal(map[string]string{"path": match[1]})
			toolCall, _ := json.Marshal(map[string]any{
				"id": "chatcmpl-image-read", "object": "chat.completion.chunk",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0, "id": "call_read_image", "type": "function",
						"function": map[string]any{"name": "read", "arguments": string(args)},
					}},
				}}},
			})
			toolFinish, _ := json.Marshal(map[string]any{
				"id": "chatcmpl-image-read", "object": "chat.completion.chunk",
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
			})
			fmt.Fprintf(w, "data: %s\n\n", toolCall)
			fmt.Fprintf(w, "data: %s\n\n", toolFinish)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		secondBody.Store(string(body))
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-image\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"looks like a pixel\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-image\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer providerServer.Close()

	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "image-test"
	settings.DefaultModel = "image-model"
	settings.DefaultMode = "yolo"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"image-test": {
			APIKey:  "test-key",
			BaseURL: providerServer.URL + "/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "image-model", Name: "Image Model", ContextWindow: 32768, MaxTokens: 1024, Input: []string{"text", "image"}}},
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
	sessionID, ok := newSession["result"].(map[string]any)["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session/new response = %#v, missing sessionId", newSession)
	}

	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt": []map[string]any{
				{"type": "text", "text": "describe this image"},
				{"type": "image", "mimeType": "image/png", "data": acpTestOnePixelPNGBase64},
			},
		},
	})
	notifications := assertACPResponseIDCollecting(t, reader, 3)
	if !containsACPNotification(notifications, "session/update", "agent_message_chunk", "looks like a pixel") {
		t.Fatalf("prompt notifications missing assistant message: %#v", notifications)
	}
	first, _ := firstBody.Load().(string)
	if first == "" {
		t.Fatal("provider never received the prompt request")
	}
	// The image block must normalize into the Runtime input contract instead
	// of being rejected or flattened into plain prompt text.
	if !strings.Contains(first, "Runtime-managed input files") || !strings.Contains(first, "mediaType: image/png") {
		t.Fatalf("first provider request has no materialized image resource: %s", first)
	}
	second, _ := secondBody.Load().(string)
	if second == "" {
		t.Fatal("provider never received the follow-up request with the image tool result")
	}
	// The materialized image must reach the provider as native image content
	// through the canonical tool-result projection.
	if !strings.Contains(second, `"type":"image_url"`) {
		t.Fatalf("follow-up provider request has no image content block: %s", second)
	}
	if !strings.Contains(second, "data:image/png;base64,"+acpTestOnePixelPNGBase64) {
		t.Fatalf("follow-up provider request does not carry the materialized PNG data: %s", second)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waitForACPProcess(t, cmd)
}

func findACPArtifactUpdate(t *testing.T, messages []map[string]any) map[string]any {
	t.Helper()
	for _, message := range messages {
		if message["method"] != "session/update" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		update, _ := params["update"].(map[string]any)
		if update["sessionUpdate"] == "artifact" {
			return update
		}
	}
	t.Fatalf("no artifact session/update in notifications: %#v", messages)
	return nil
}

func readACPMessageUntilID(t *testing.T, reader *bufio.Reader, id float64) map[string]any {
	t.Helper()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatalf("parse ACP message %q: %v", line, err)
		}
		if got, ok := message["id"].(float64); ok && got == id {
			return message
		}
	}
}

func assertACPStructuredErrorCode(t *testing.T, message map[string]any, code string) {
	t.Helper()
	errObj, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want a structured RPC error", message)
	}
	data, _ := errObj["data"].(map[string]any)
	if data == nil || data["code"] != code {
		t.Fatalf("RPC error = %#v, want structured code %q", errObj, code)
	}
}
