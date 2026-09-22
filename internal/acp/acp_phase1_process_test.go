package acp

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// --- shared Phase 1 wire-test infrastructure ----------------------------------

type acpProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
}

func startACPPhase1Process(t *testing.T, configDir string, extraEnv ...string) *acpProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStdioProcessHelper$")
	cmd.Env = append(os.Environ(), "MOTHX_ACP_PROCESS_HELPER=1", "MOTHX_DIR="+configDir)
	cmd.Env = append(cmd.Env, extraEnv...)
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
	return &acpProcess{cmd: cmd, stdin: stdin, reader: bufio.NewReader(stdout)}
}

func (p *acpProcess) send(t *testing.T, value any) {
	t.Helper()
	sendACPRequest(t, p.stdin, value)
}

func (p *acpProcess) respond(t *testing.T, id float64) map[string]any {
	t.Helper()
	return assertACPResponseID(t, p.reader, id)
}

// respondCollecting reads until the response of id arrives and returns it
// together with every notification observed on the way.
func (p *acpProcess) respondCollecting(t *testing.T, id float64) (map[string]any, []map[string]any) {
	t.Helper()
	var notifications []map[string]any
	for {
		message := p.readMessage(t)
		if got, ok := message["id"].(float64); ok && got == id {
			return message, notifications
		}
		notifications = append(notifications, message)
	}
}

// readUntilMethod reads until a message with the given wire method arrives.
func (p *acpProcess) readUntilMethod(t *testing.T, method string) (map[string]any, []map[string]any) {
	t.Helper()
	var collected []map[string]any
	for {
		message := p.readMessage(t)
		if message["method"] == method {
			return message, collected
		}
		collected = append(collected, message)
	}
}

func (p *acpProcess) readMessage(t *testing.T) map[string]any {
	t.Helper()
	line, err := p.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read ACP message: %v (partial line: %q)", err, line)
	}
	var message map[string]any
	if err := json.Unmarshal(line, &message); err != nil {
		t.Fatalf("parse ACP message %q: %v", line, err)
	}
	return message
}

func (p *acpProcess) closeAndWait(t *testing.T) {
	t.Helper()
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waitForACPProcess(t, p.cmd)
}

func writeACPPhase1Settings(t *testing.T, configDir, providerName, modelID, baseURL string, vision bool) {
	t.Helper()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = providerName
	settings.DefaultModel = modelID
	settings.DefaultMode = "yolo"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	model := config.ModelConfig{ID: modelID, Name: "Phase1 Model", ContextWindow: 32768, MaxTokens: 1024}
	if vision {
		model.Input = []string{"text", "image"}
	}
	settings.Providers = map[string]*config.ProviderConfig{
		providerName: {APIKey: "test-key", BaseURL: baseURL, API: "openai-chat", Models: []config.ModelConfig{model}},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
}

func acpSSEToolCall(w http.ResponseWriter, chunkID, callID, toolName, argsJSON string) {
	w.Header().Set("Content-Type", "text/event-stream")
	toolCall, _ := json.Marshal(map[string]any{
		"id": chunkID, "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": callID, "type": "function",
				"function": map[string]any{"name": toolName, "arguments": argsJSON},
			}},
		}}},
	})
	toolFinish, _ := json.Marshal(map[string]any{
		"id": chunkID, "object": "chat.completion.chunk",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
	})
	fmt.Fprintf(w, "data: %s\n\n", toolCall)
	fmt.Fprintf(w, "data: %s\n\n", toolFinish)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func acpSSEText(w http.ResponseWriter, chunkID, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	content, _ := json.Marshal(text)
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%s},\"finish_reason\":null}]}\n\n", chunkID, content)
	fmt.Fprintf(w, "data: {\"id\":%q,\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", chunkID)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func findACPSessionEvents(messages []map[string]any, event string) []map[string]any {
	var matches []map[string]any
	for _, message := range messages {
		if message["method"] != "_mothx/session_event" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		if params["event"] == event {
			matches = append(matches, params)
		}
	}
	return matches
}

func findACPUpdates(messages []map[string]any, updateType string) []map[string]any {
	var matches []map[string]any
	for _, message := range messages {
		if message["method"] != "session/update" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		update, _ := params["update"].(map[string]any)
		if update["sessionUpdate"] == updateType {
			matches = append(matches, update)
		}
	}
	return matches
}

func acpListedSessionByMeta(t *testing.T, response map[string]any, sessionID string) map[string]any {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("session/list response = %#v", response)
	}
	sessions, _ := result["sessions"].([]any)
	for _, entry := range sessions {
		listed, _ := entry.(map[string]any)
		if listed["sessionId"] == sessionID {
			meta, _ := listed["_meta"].(map[string]any)
			if meta == nil {
				t.Fatalf("listed session %s has no _meta: %#v", sessionID, listed)
			}
			return meta
		}
	}
	t.Fatalf("session %s missing from session/list: %#v", sessionID, sessions)
	return nil
}

func acpNewSessionID(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("session response = %#v", response)
	}
	sessionID, _ := result["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session response = %#v, missing sessionId", response)
	}
	return sessionID
}

// --- §4.1 + §4.9: run status projection, run_status events, feature discovery --

func TestACPStdioProcessRunStatusProjectionAndEvents(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRun := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRun)
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		if call == 1 {
			// Block so the run is observably active while session/list runs.
			<-release
			acpSSEText(w, "chatcmpl-runstatus", "finished now")
			return
		}
		acpSSEText(w, "chatcmpl-runstatus-extra", "extra")
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "runstatus-test", "runstatus-model", providerServer.URL+"/v1", false)

	process := startACPPhase1Process(t, configDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	initialize := process.respond(t, 1)
	meta := initialize["result"].(map[string]any)["_meta"].(map[string]any)[mothxExtensionNamespace].(map[string]any)
	rawFeatures, _ := meta["features"].([]any)
	features := map[string]bool{}
	for _, feature := range rawFeatures {
		if name, ok := feature.(string); ok {
			features[name] = true
		}
	}
	for _, want := range []string{"runStatus", "sessionMeta", "projects", "workspaceExtend", "decisionDeadline", "subagentEvents", "toolResultImages", "attachmentList"} {
		if !features[want] {
			t.Fatalf("initialize features = %#v, want %q", rawFeatures, want)
		}
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionA := acpNewSessionID(t, process.respond(t, 2))
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionB := acpNewSessionID(t, process.respond(t, 3))

	// Start a run that blocks in the provider, then list while it is active.
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionA, "prompt": []map[string]any{{"type": "text", "text": "run now"}}},
	})
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	listActive, activeNotifications := process.respondCollecting(t, 5)

	metaA := acpListedSessionByMeta(t, listActive, sessionA)
	lastRun, ok := metaA["lastRun"].(map[string]any)
	if !ok {
		t.Fatalf("active session _meta = %#v, want a lastRun projection", metaA)
	}
	runID, _ := lastRun["runId"].(string)
	if !strings.HasPrefix(runID, "acp_") {
		t.Fatalf("lastRun = %#v, want the canonical ACP run identity", lastRun)
	}
	if lastRun["status"] != "running" || lastRun["active"] != true {
		t.Fatalf("active lastRun = %#v, want running/active", lastRun)
	}
	if startedAt, _ := lastRun["startedAt"].(string); startedAt == "" {
		t.Fatalf("active lastRun = %#v, want a startedAt timestamp", lastRun)
	}
	if lastRun["finishedAt"] != nil {
		t.Fatalf("active lastRun = %#v, want a null finishedAt", lastRun)
	}
	metaB := acpListedSessionByMeta(t, listActive, sessionB)
	if _, hasLastRun := metaB["lastRun"]; hasLastRun {
		t.Fatalf("session without runs _meta = %#v, want no lastRun key", metaB)
	}
	if metaB["pinned"] != false || metaB["projectId"] != nil {
		t.Fatalf("default metadata projection = %#v, want pinned=false projectId=null", metaB)
	}
	beginEvents := findACPSessionEvents(activeNotifications, "run_status")
	if len(beginEvents) != 1 || beginEvents[0]["status"] != "running" || beginEvents[0]["runId"] != runID || beginEvents[0]["sessionId"] != sessionA {
		t.Fatalf("run_status begin events = %#v, want exactly one running projection for %s", beginEvents, runID)
	}

	// Finish the run and collect the terminal projections. The finish
	// run_status event is emitted by the run finalizer right after the
	// prompt response and before the admission lock is released, so read
	// past the response until it arrives.
	releaseRun()
	promptResponse, promptNotifications := process.respondCollecting(t, 4)
	stopReason, _ := promptResponse["result"].(map[string]any)["stopReason"].(string)
	if stopReason != "end_turn" {
		t.Fatalf("prompt response = %#v, want end_turn", promptResponse)
	}
	finishEvents := findACPSessionEvents(promptNotifications, "run_status")
	for attempt := 0; attempt < 10 && len(finishEvents) == 0; attempt++ {
		message := process.readMessage(t)
		promptNotifications = append(promptNotifications, message)
		finishEvents = findACPSessionEvents(promptNotifications, "run_status")
	}
	if len(finishEvents) != 1 || finishEvents[0]["status"] != "completed" || finishEvents[0]["runId"] != runID {
		t.Fatalf("run_status finish events = %#v, want exactly one completed projection", finishEvents)
	}
	terminalEvents := findACPSessionEvents(promptNotifications, "terminal")
	if len(terminalEvents) != 1 || terminalEvents[0]["status"] != "completed" {
		t.Fatalf("terminal events = %#v, want the pre-existing terminal projection to survive", terminalEvents)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	listFinished := process.respond(t, 6)
	metaFinished := acpListedSessionByMeta(t, listFinished, sessionA)
	finishedRun, ok := metaFinished["lastRun"].(map[string]any)
	if !ok {
		t.Fatalf("finished session _meta = %#v, want a lastRun projection", metaFinished)
	}
	if finishedRun["runId"] != runID || finishedRun["status"] != "completed" || finishedRun["active"] != false {
		t.Fatalf("finished lastRun = %#v, want completed/inactive", finishedRun)
	}
	if finishedAt, _ := finishedRun["finishedAt"].(string); finishedAt == "" {
		t.Fatalf("finished lastRun = %#v, want a finishedAt timestamp", finishedRun)
	}
}

// --- §4.2: pinned/project metadata projection, projects CRUD, fork inheritance --

func TestACPStdioProcessSessionMetaProjectsAndFork(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acpSSEText(w, "chatcmpl-meta", "ok")
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "meta-test", "meta-model", providerServer.URL+"/v1", false)

	process := startACPPhase1Process(t, configDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.respond(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionA := acpNewSessionID(t, process.respond(t, 2))
	// Forking requires a completed conversation turn in the source session.
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionA, "prompt": []map[string]any{{"type": "text", "text": "hello"}}},
	})
	process.respondCollecting(t, 3)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "mothx/projects/create", "params": map[string]any{"name": "Phase1 Project"}})
	created := process.respond(t, 4)["result"].(map[string]any)
	projectID, _ := created["id"].(string)
	if projectID == "" || created["name"] != "Phase1 Project" {
		t.Fatalf("projects/create result = %#v", created)
	}
	if createdAt, _ := created["createdAt"].(string); createdAt == "" {
		t.Fatalf("projects/create result = %#v, want a createdAt timestamp", created)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "mothx/projects/list", "params": map[string]any{}})
	listed := process.respond(t, 5)["result"].(map[string]any)["projects"].([]any)
	if len(listed) != 1 {
		t.Fatalf("projects/list = %#v, want exactly the created project", listed)
	}
	project := listed[0].(map[string]any)
	if project["id"] != projectID || project["sessionCount"] != float64(0) {
		t.Fatalf("projects/list entry = %#v", project)
	}

	// setMeta assigns the pin and the project, responds with the stored state,
	// and broadcasts session_info_update with the additive _meta keys.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "mothx/session/setMeta", "params": map[string]any{
		"sessionId": sessionA, "pinned": true, "projectId": projectID,
	}})
	setMetaResponse, setMetaNotifications := process.respondCollecting(t, 6)
	setMeta := setMetaResponse["result"].(map[string]any)
	if setMeta["pinned"] != true || setMeta["projectId"] != projectID {
		t.Fatalf("setMeta result = %#v", setMeta)
	}
	if updatedAt, _ := setMeta["updatedAt"].(string); updatedAt == "" {
		t.Fatalf("setMeta result = %#v, want an updatedAt timestamp", setMeta)
	}
	infoUpdates := findACPUpdates(setMetaNotifications, "session_info_update")
	if len(infoUpdates) != 1 {
		t.Fatalf("session_info_update notifications = %#v, want exactly one", infoUpdates)
	}
	infoMeta, _ := infoUpdates[0]["_meta"].(map[string]any)
	if infoMeta == nil || infoMeta["pinned"] != true || infoMeta["projectId"] != projectID {
		t.Fatalf("session_info_update _meta = %#v, want pinned/projectId", infoUpdates[0])
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	metaA := acpListedSessionByMeta(t, process.respond(t, 7), sessionA)
	if metaA["pinned"] != true || metaA["projectId"] != projectID {
		t.Fatalf("listed _meta after setMeta = %#v", metaA)
	}

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "mothx/projects/rename", "params": map[string]any{"id": projectID, "name": "Renamed"}})
	renamed := process.respond(t, 8)["result"].(map[string]any)
	if renamed["id"] != projectID || renamed["name"] != "Renamed" {
		t.Fatalf("projects/rename result = %#v", renamed)
	}

	// Forks inherit the project assignment but never the pin (existing DAO
	// behavior projected through session/list).
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "session/fork", "params": map[string]any{
		"sessionId": sessionA, "cwd": workDir, "requestId": "fork-phase1-1",
	}})
	forked := process.respond(t, 9)
	sessionB := acpNewSessionID(t, forked)
	if parent, _ := forked["result"].(map[string]any)["parentSessionId"].(string); parent != sessionA {
		t.Fatalf("fork result = %#v, want the parent lineage", forked)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	listAfterFork := process.respond(t, 10)
	metaB := acpListedSessionByMeta(t, listAfterFork, sessionB)
	if metaB["pinned"] != false {
		t.Fatalf("forked _meta = %#v, want pinned=false", metaB)
	}
	if metaB["projectId"] != projectID {
		t.Fatalf("forked _meta = %#v, want the inherited project assignment", metaB)
	}

	// Deleting the project clears the assignment of every session (declared
	// ON DELETE SET NULL semantics).
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "mothx/projects/delete", "params": map[string]any{"id": projectID}})
	process.respond(t, 11)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 12, "method": "session/list", "params": map[string]any{"cwd": workDir}})
	listAfterDelete := process.respond(t, 12)
	if deletedMeta := acpListedSessionByMeta(t, listAfterDelete, sessionA); deletedMeta["projectId"] != nil {
		t.Fatalf("session _meta after project delete = %#v, want projectId=null", deletedMeta)
	}
	if deletedMeta := acpListedSessionByMeta(t, listAfterDelete, sessionB); deletedMeta["projectId"] != nil {
		t.Fatalf("forked _meta after project delete = %#v, want projectId=null", deletedMeta)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 13, "method": "mothx/projects/list", "params": map[string]any{}})
	if remaining := process.respond(t, 13)["result"].(map[string]any)["projects"].([]any); len(remaining) != 0 {
		t.Fatalf("projects/list after delete = %#v, want empty", remaining)
	}

	// Structured errors: unknown project and unknown session.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 14, "method": "mothx/session/setMeta", "params": map[string]any{"sessionId": sessionA, "projectId": "missing-project"}})
	assertACPStructuredErrorCode(t, readACPMessageUntilID(t, process.reader, 14), "project_not_found")
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 15, "method": "mothx/session/setMeta", "params": map[string]any{"sessionId": "unknown-session", "pinned": true}})
	assertACPStructuredErrorCode(t, readACPMessageUntilID(t, process.reader, 15), "session_not_found")

	// An explicit null clears the project while an absent field keeps state.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 16, "method": "mothx/projects/create", "params": map[string]any{"name": "Second"}})
	secondID := process.respond(t, 16)["result"].(map[string]any)["id"].(string)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 17, "method": "mothx/session/setMeta", "params": map[string]any{"sessionId": sessionA, "projectId": secondID}})
	process.respond(t, 17)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 18, "method": "mothx/session/setMeta", "params": map[string]any{"sessionId": sessionA, "projectId": nil}})
	clearedResponse, clearedNotifications := process.respondCollecting(t, 18)
	cleared := clearedResponse["result"].(map[string]any)
	if cleared["projectId"] != nil || cleared["pinned"] != true {
		t.Fatalf("setMeta clear result = %#v, want projectId=null with the pin preserved", cleared)
	}
	// Drain the finalizer run_status event of the seeded prompt so process
	// shutdown does not race an unread notification.
	_ = clearedNotifications
}

// --- §4.3: dynamic workspace extension ----------------------------------------

func TestACPStdioProcessWorkspaceExtend(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	dir2 := t.TempDir()
	resolvedDir2, err := filepath.EvalSymlinks(dir2)
	if err != nil {
		t.Fatal(err)
	}
	notesContent := []byte("extended workspace notes")
	notesPath := filepath.Join(resolvedDir2, "notes.txt")
	if err := os.WriteFile(notesPath, notesContent, 0600); err != nil {
		t.Fatal(err)
	}
	var firstBody atomic.Value
	var parentCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call := parentCalls.Add(1)
		if call == 1 {
			firstBody.Store(string(body))
			args, _ := json.Marshal(map[string]string{"path": notesPath})
			acpSSEToolCall(w, "chatcmpl-extend", "call_read_notes", "read", string(args))
			return
		}
		acpSSEText(w, "chatcmpl-extend-final", "read the extended file")
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "extend-test", "extend-model", providerServer.URL+"/v1", false)

	process := startACPPhase1Process(t, configDir)
	defer process.closeAndWait(t)

	// Negotiate a workspace window at initialize so the extension is observable.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": 1,
		"_meta":           map[string]any{"mothx": map[string]any{"workspace": map[string]any{"cwd": workDir}}},
	}})
	process.respond(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionID := acpNewSessionID(t, process.respond(t, 2))

	// Relative paths are rejected with a structured error.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "mothx/workspace/extend", "params": map[string]any{"additionalDirectories": []string{"relative/dir"}}})
	assertACPStructuredErrorCode(t, readACPMessageUntilID(t, process.reader, 3), "workspace_directory_invalid")

	promptWithLink := func(id int) map[string]any {
		return map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "session/prompt",
			"params": map[string]any{
				"sessionId": sessionID,
				"prompt": []map[string]any{
					{"type": "text", "text": "read the linked notes"},
					{"type": "resource_link", "name": "notes.txt", "uri": "file://" + notesPath, "mimeType": "text/plain"},
				},
				"_meta": map[string]any{"mothx": map[string]any{"workspace": map[string]any{
					"cwd": workDir, "additionalDirectories": []string{resolvedDir2},
				}}},
			},
		}
	}

	// Before the extension the new root is outside the negotiated window.
	process.send(t, promptWithLink(4))
	denied := readACPMessageUntilID(t, process.reader, 4)
	errObj, _ := denied["error"].(map[string]any)
	if errObj == nil || !strings.Contains(fmt.Sprint(errObj["message"]), "outside the negotiated workspace") {
		t.Fatalf("prompt before extend = %#v, want a workspace rejection", denied)
	}

	// Extend the window; the response carries the immutable cwd and the grown
	// directory list and a workspace session event is broadcast.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "mothx/workspace/extend", "params": map[string]any{"additionalDirectories": []string{dir2}}})
	extendResponse, extendNotifications := process.respondCollecting(t, 5)
	extendResult := extendResponse["result"].(map[string]any)
	resolvedCwd, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if extendResult["cwd"] != resolvedCwd && extendResult["cwd"] != workDir {
		t.Fatalf("extend cwd = %#v, want the immutable workspace cwd", extendResult["cwd"])
	}
	dirs, _ := extendResult["additionalDirectories"].([]any)
	if len(dirs) != 1 || dirs[0] != resolvedDir2 {
		t.Fatalf("extend additionalDirectories = %#v, want the normalized %q", dirs, resolvedDir2)
	}
	workspaceEvents := findACPSessionEvents(extendNotifications, "workspace")
	if len(workspaceEvents) != 1 || workspaceEvents[0]["additionalDirectories"] == nil {
		t.Fatalf("workspace events = %#v, want exactly one projection", workspaceEvents)
	}

	// After the extension the same prompt is admitted, the linked file is
	// materialized, and the open session's tools can read the new directory.
	process.send(t, promptWithLink(6))
	promptResponse, promptNotifications := process.respondCollecting(t, 6)
	if stopReason, _ := promptResponse["result"].(map[string]any)["stopReason"].(string); stopReason != "end_turn" {
		t.Fatalf("prompt after extend = %#v, want end_turn", promptResponse)
	}
	body, _ := firstBody.Load().(string)
	if !strings.Contains(body, "notes.txt") {
		t.Fatalf("provider request has no materialized resource_link from the extended directory: %s", body)
	}
	var toolUpdate map[string]any
	for _, update := range findACPUpdates(promptNotifications, "tool_call_update") {
		if update["toolCallId"] == "call_read_notes" && update["status"] == "completed" {
			toolUpdate = update
		}
	}
	if toolUpdate == nil {
		t.Fatalf("no completed read tool_call_update in %#v", promptNotifications)
	}
	encoded, _ := json.Marshal(toolUpdate)
	if !strings.Contains(string(encoded), string(notesContent)) {
		t.Fatalf("read tool result = %s, want the extended directory file content", encoded)
	}
}

// --- §4.4: decision deadline reminders on the wire -----------------------------

func TestACPStdioProcessDecisionDeadlineReminders(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	var parentCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := parentCalls.Add(1)
		if call == 1 {
			args, _ := json.Marshal(map[string]any{"question": "continue?", "options": []string{"yes", "no"}})
			acpSSEToolCall(w, "chatcmpl-question", "call_question", "question", string(args))
			return
		}
		acpSSEText(w, "chatcmpl-question-final", "thanks for answering")
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "deadline-test", "deadline-model", providerServer.URL+"/v1", false)

	process := startACPPhase1Process(t, configDir, "MOTHX_ACP_HELPER_QUESTION_TIMEOUT=4s")
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.respond(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionID := acpNewSessionID(t, process.respond(t, 2))
	// The interactive question tool is advertised only outside unattended modes
	// (yolo), so switch this session to agent mode before prompting. The
	// execution-side registration check then permits the question call.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 20, "method": "session/set_mode", "params": map[string]any{"sessionId": sessionID, "modeId": "agent"}})
	process.respond(t, 20)
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "ask me"}}},
	})

	// Wait for the reverse question request; with the 4s timeout the first
	// reminder fires at min(60s, timeout/2) = 2s.
	questionMessage, beforeQuestion := process.readUntilMethod(t, "mothx/requestQuestion")
	requestID, _ := questionMessage["id"].(string)
	if requestID == "" {
		t.Fatalf("question request = %#v, want a string request id", questionMessage)
	}
	params, _ := questionMessage["params"].(map[string]any)
	if params["prompt"] != "continue?" {
		t.Fatalf("question params = %#v, want the question prompt payload", params)
	}
	if events := findACPSessionEvents(beforeQuestion, "decision_deadline"); len(events) != 0 {
		t.Fatalf("decision_deadline events before the first mark = %#v", events)
	}

	// The reminder arrives while the decision is still pending.
	var deadlineEvent map[string]any
	var collected []map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for deadlineEvent == nil {
		if time.Now().After(deadline) {
			t.Fatalf("no decision_deadline reminder within 5s; collected %#v", collected)
		}
		message := process.readMessage(t)
		collected = append(collected, message)
		for _, event := range findACPSessionEvents([]map[string]any{message}, "decision_deadline") {
			deadlineEvent = event
		}
	}
	if deadlineEvent["requestId"] != requestID || deadlineEvent["kind"] != "question" || deadlineEvent["sessionId"] != sessionID {
		t.Fatalf("decision_deadline event = %#v", deadlineEvent)
	}
	if eventDeadline, _ := deadlineEvent["deadline"].(string); eventDeadline == "" {
		t.Fatalf("decision_deadline event = %#v, want an RFC3339 deadline", deadlineEvent)
	}
	remaining, _ := deadlineEvent["remainingMs"].(float64)
	if remaining <= 0 || remaining > 4000 {
		t.Fatalf("decision_deadline remainingMs = %#v, want (0, 4000]", deadlineEvent)
	}

	// Answering resolves the decision; no further reminder may arrive.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": requestID, "result": map[string]any{"answer": "yes"}})
	all := append(collected, []map[string]any{}...)
	promptResponse, promptNotifications := process.respondCollecting(t, 3)
	all = append(all, promptNotifications...)
	if stopReason, _ := promptResponse["result"].(map[string]any)["stopReason"].(string); stopReason != "end_turn" {
		t.Fatalf("prompt response = %#v, want end_turn", promptResponse)
	}
	events := findACPSessionEvents(all, "decision_deadline")
	if len(events) != 1 {
		t.Fatalf("decision_deadline events over the whole run = %#v, want exactly the single first mark", events)
	}
	if !containsACPNotification(promptNotifications, "session/update", "agent_message_chunk", "thanks for answering") {
		t.Fatalf("prompt notifications missing the post-answer assistant message: %#v", promptNotifications)
	}
}

// --- §4.5 + §4.8: resume artifact replay and attachment metadata listing ------

func TestACPStdioProcessResumeArtifactReplayAndAttachmentList(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	artifactContent := []byte("resume artifact content")
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), artifactContent, 0600); err != nil {
		t.Fatal(err)
	}
	var providerCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		if call == 1 {
			acpSSEToolCall(w, "chatcmpl-resume", "call_publish", "publish_artifact", `{"path":"report.txt"}`)
			return
		}
		acpSSEText(w, "chatcmpl-resume-final", "published")
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "resume-test", "resume-model", providerServer.URL+"/v1", false)
	if err := config.SaveGlobalSettingsPatch(map[string]any{"enableACPArtifact": true}); err != nil {
		t.Fatalf("enable ACP artifacts for fixture: %v", err)
	}

	process := startACPPhase1Process(t, configDir)
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.respond(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionID := acpNewSessionID(t, process.respond(t, 2))
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "publish report.txt"}}},
	})
	_, promptNotifications := process.respondCollecting(t, 3)
	artifactUpdates := findACPUpdates(promptNotifications, "artifact")
	if len(artifactUpdates) != 1 {
		t.Fatalf("artifact updates = %#v, want exactly one", artifactUpdates)
	}
	attachmentID, _ := artifactUpdates[0]["artifactId"].(string)
	if attachmentID == "" {
		t.Fatalf("artifact update = %#v, missing artifactId", artifactUpdates[0])
	}

	// close → resume (reopen branch) replays the historical generated artifact.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session/close", "params": map[string]any{"sessionId": sessionID}})
	process.respond(t, 4)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 5, "method": "session/resume", "params": map[string]any{"sessionId": sessionID, "cwd": workDir}})
	_, resumeNotifications := process.respondCollecting(t, 5)
	replayed := findACPUpdates(resumeNotifications, "artifact")
	if len(replayed) != 1 || replayed[0]["artifactId"] != attachmentID || replayed[0]["status"] != "generated" {
		t.Fatalf("resume replay = %#v, want the persisted artifact %s", replayed, attachmentID)
	}

	// resume again on the already-open session (existing branch) replays too.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 6, "method": "session/resume", "params": map[string]any{"sessionId": sessionID, "cwd": workDir}})
	_, secondResumeNotifications := process.respondCollecting(t, 6)
	replayedAgain := findACPUpdates(secondResumeNotifications, "artifact")
	if len(replayedAgain) != 1 || replayedAgain[0]["artifactId"] != attachmentID {
		t.Fatalf("second resume replay = %#v, want the same artifact", replayedAgain)
	}

	// attachment/list projects metadata only, in creation order, with the
	// canonical fields and no content payload.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "mothx/attachment/list", "params": map[string]any{"sessionId": sessionID}})
	listResult := process.respond(t, 7)["result"].(map[string]any)
	attachments, _ := listResult["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("attachment/list = %#v, want exactly the generated artifact", attachments)
	}
	entry := attachments[0].(map[string]any)
	if entry["attachmentId"] != attachmentID || entry["filename"] != "report.txt" || entry["kind"] != "file" || entry["status"] != "generated" {
		t.Fatalf("attachment entry = %#v", entry)
	}
	if entry["mediaType"] != "text/plain; charset=utf-8" {
		t.Fatalf("attachment mediaType = %#v", entry["mediaType"])
	}
	if size, _ := entry["size"].(float64); int64(size) != int64(len(artifactContent)) {
		t.Fatalf("attachment size = %#v, want %d", entry["size"], len(artifactContent))
	}
	if runID, _ := entry["runId"].(string); !strings.HasPrefix(runID, "acp_") {
		t.Fatalf("attachment runId = %#v", entry["runId"])
	}
	if createdAt, _ := entry["createdAt"].(string); createdAt == "" {
		t.Fatalf("attachment createdAt = %#v", entry["createdAt"])
	}
	if _, hasContent := entry["contentBase64"]; hasContent {
		t.Fatalf("attachment entry leaks content: %#v", entry)
	}

	// The generated filter matches, the input filter is empty for this flow.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "mothx/attachment/list", "params": map[string]any{"sessionId": sessionID, "status": "generated"}})
	generated := process.respond(t, 8)["result"].(map[string]any)["attachments"].([]any)
	if len(generated) != 1 || generated[0].(map[string]any)["attachmentId"] != attachmentID {
		t.Fatalf("generated listing = %#v", generated)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "mothx/attachment/list", "params": map[string]any{"sessionId": sessionID, "status": "input"}})
	inputs := process.respond(t, 9)["result"].(map[string]any)["attachments"].([]any)
	if len(inputs) != 0 {
		t.Fatalf("input listing = %#v, want empty", inputs)
	}

	// Another session lists empty and an unknown session lists empty too:
	// rows never leak across sessions.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 10, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	otherSession := acpNewSessionID(t, process.respond(t, 10))
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 11, "method": "mothx/attachment/list", "params": map[string]any{"sessionId": otherSession}})
	if other := process.respond(t, 11)["result"].(map[string]any)["attachments"].([]any); len(other) != 0 {
		t.Fatalf("foreign session listing = %#v, want empty", other)
	}
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 12, "method": "mothx/attachment/list", "params": map[string]any{"sessionId": "unknown-session"}})
	if unknown := process.respond(t, 12)["result"].(map[string]any)["attachments"].([]any); len(unknown) != 0 {
		t.Fatalf("unknown session listing = %#v, want empty", unknown)
	}

	// Every listed id is fetchable with byte-identical content: the list and
	// fetch id sets agree.
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 13, "method": "mothx/attachment/fetch", "params": map[string]any{"sessionId": sessionID, "attachmentId": attachmentID}})
	fetched := process.respond(t, 13)["result"].(map[string]any)
	content, err := base64.StdEncoding.DecodeString(fetched["contentBase64"].(string))
	if err != nil || string(content) != string(artifactContent) {
		t.Fatalf("fetched content = %q, %v, want %q", content, err, artifactContent)
	}
}

// --- §4.6 + §4.7: sub-agent lifecycle events and tool result images ------------

func TestACPStdioProcessSubagentEventsAndToolResultImages(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	pngBytes, err := base64.StdEncoding.DecodeString(acpTestOnePixelPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	pngPath := filepath.Join(workDir, "pixel.png")
	if err := os.WriteFile(pngPath, pngBytes, 0600); err != nil {
		t.Fatal(err)
	}
	var parentCalls atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Delegated task:") {
			acpSSEText(w, "chatcmpl-child", "sub-agent-result")
			return
		}
		switch parentCalls.Add(1) {
		case 1:
			args, _ := json.Marshal(map[string]any{"task": "reply with hello"})
			acpSSEToolCall(w, "chatcmpl-parent-delegate", "call_delegate", "delegate_subagent", string(args))
		case 2:
			args, _ := json.Marshal(map[string]string{"path": pngPath})
			acpSSEToolCall(w, "chatcmpl-parent-read", "call_read_png", "read", string(args))
		default:
			acpSSEText(w, "chatcmpl-parent-final", "all done")
		}
	}))
	defer providerServer.Close()
	writeACPPhase1Settings(t, configDir, "subagent-test", "subagent-model", providerServer.URL+"/v1", true)

	process := startACPPhase1Process(t, configDir, "MOTHX_ACP_HELPER_MULTI_AGENT=1")
	defer process.closeAndWait(t)

	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	process.respond(t, 1)
	process.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": workDir}})
	sessionID := acpNewSessionID(t, process.respond(t, 2))
	process.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{"sessionId": sessionID, "prompt": []map[string]any{{"type": "text", "text": "delegate and read the image"}}},
	})
	promptResponse, notifications := process.respondCollecting(t, 3)
	if stopReason, _ := promptResponse["result"].(map[string]any)["stopReason"].(string); stopReason != "end_turn" {
		t.Fatalf("prompt response = %#v, want end_turn", promptResponse)
	}

	// Sub-agent lifecycle: exactly one started/completed pair for one agentId,
	// in order, scoped to the parent session.
	subagentEvents := findACPSessionEvents(notifications, "subagent")
	var started, completed []map[string]any
	for _, event := range subagentEvents {
		if event["sessionId"] != sessionID {
			t.Fatalf("subagent event = %#v, want the parent session id", event)
		}
		switch event["status"] {
		case "started":
			started = append(started, event)
		case "completed":
			completed = append(completed, event)
		}
	}
	if len(started) != 1 || len(completed) != 1 {
		t.Fatalf("subagent events = %#v, want one started/completed pair", subagentEvents)
	}
	agentID, _ := started[0]["agentId"].(string)
	if agentID == "" || completed[0]["agentId"] != agentID {
		t.Fatalf("subagent pair = %#v / %#v, want the same agentId", started[0], completed[0])
	}
	startedIndex, completedIndex := -1, -1
	for index, event := range subagentEvents {
		if event["agentId"] != agentID {
			continue
		}
		if event["status"] == "started" && startedIndex < 0 {
			startedIndex = index
		}
		if event["status"] == "completed" && completedIndex < 0 {
			completedIndex = index
		}
	}
	if startedIndex < 0 || startedIndex >= completedIndex {
		t.Fatalf("subagent event order = %#v, want started before completed", subagentEvents)
	}

	// The child terminal event must not produce a parent terminal projection:
	// exactly one terminal event exists and the run completed successfully.
	terminalEvents := findACPSessionEvents(notifications, "terminal")
	if len(terminalEvents) != 1 || terminalEvents[0]["status"] != "completed" || terminalEvents[0]["sessionId"] != sessionID {
		t.Fatalf("terminal events = %#v, want exactly one completed parent projection", terminalEvents)
	}

	// Child text stays on the parent session stream (single stream).
	if !containsACPNotification(notifications, "session/update", "agent_message_chunk", "sub-agent-result") {
		t.Fatalf("child text was not projected on the parent stream: %#v", notifications)
	}
	if !containsACPNotification(notifications, "session/update", "agent_message_chunk", "all done") {
		t.Fatalf("parent final message missing: %#v", notifications)
	}

	// Tool result images: the read tool's image payload is projected as an
	// additive image content block with byte-identical base64 data.
	var imageUpdate map[string]any
	for _, update := range findACPUpdates(notifications, "tool_call_update") {
		if update["toolCallId"] == "call_read_png" && update["status"] == "completed" {
			imageUpdate = update
		}
	}
	if imageUpdate == nil {
		t.Fatalf("no completed read tool_call_update in %#v", notifications)
	}
	contents, _ := imageUpdate["content"].([]any)
	var imageBlock map[string]any
	for _, item := range contents {
		content, _ := item.(map[string]any)
		block, _ := content["content"].(map[string]any)
		if content["type"] == "content" && block != nil && block["type"] == "image" {
			imageBlock = block
		}
	}
	if imageBlock == nil {
		t.Fatalf("read tool_call_update content = %#v, want an image block", contents)
	}
	if imageBlock["mimeType"] != "image/png" {
		t.Fatalf("image block = %#v, want image/png", imageBlock)
	}
	data, _ := imageBlock["data"].(string)
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("image block data is not valid base64: %v", err)
	}
	if string(decoded) != string(pngBytes) {
		t.Fatalf("image block payload differs from the file bytes (%d vs %d)", len(decoded), len(pngBytes))
	}
	if data != acpTestOnePixelPNGBase64 {
		t.Fatalf("image block data = %q, want the passthrough base64 payload", data)
	}
}
