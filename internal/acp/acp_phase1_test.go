package acp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agentruntime"
)

// syncedBuffer is a thread-safe output sink for fixture servers. Decision
// deadline reminders and other projections are written from server
// goroutines while the test goroutine asserts on the output.
type syncedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// parseACPMessages splits newline-delimited wire output of a fixture server
// into JSON messages.
func parseACPMessages(t *testing.T, output string) []map[string]any {
	t.Helper()
	var messages []map[string]any
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("parse ACP message %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func acpSessionEventParams(messages []map[string]any, event string) []map[string]any {
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

func assertACPRPCErrorCode(t *testing.T, message map[string]any, code string) {
	t.Helper()
	errObj, ok := message["error"].(map[string]any)
	if !ok {
		t.Fatalf("response = %#v, want a structured RPC error", message)
	}
	data, _ := errObj["data"].(map[string]any)
	if data == nil || data["code"] != code {
		t.Fatalf("RPC error = %#v, want structured code %q", errObj, code)
	}
	if msg, _ := errObj["message"].(string); strings.TrimSpace(msg) == "" {
		t.Fatalf("RPC error = %#v, want a human-readable message", errObj)
	}
}

func newPhase1FixtureServer(output *syncedBuffer) *server {
	return &server{
		w:          output,
		pending:    make(map[string]chan json.RawMessage),
		sessions:   make(map[string]*sessionRuntime),
		subagents:  make(map[string]*subagentProjection),
		toolTitles: make(map[string]string),
	}
}

// --- §4.1 run status projection ----------------------------------------------

func TestACPRunStatusProjectionMapsCanonicalStatuses(t *testing.T) {
	cases := map[string]string{
		"running":              "running",
		"created":              "running",
		"queued":               "running",
		"waiting_for_approval": "running",
		"waiting_for_question": "running",
		"cancelling":           "running",
		"terminalizing":        "running",
		"completed":            "completed",
		"incomplete":           "incomplete",
		"failed":               "failed",
		"timed_out":            "failed",
		"expired":              "failed",
		"cancelled":            "cancelled",
		"canceled":             "cancelled",
		"unknown-terminal":     "failed",
	}
	for status, want := range cases {
		if got := acpRunStatus(status); got != want {
			t.Fatalf("acpRunStatus(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestACPNotifyRunStatusProjectsSessionEvent(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.notifyRunStatus("session-1", "run-1", "running")
	messages := parseACPMessages(t, output.String())
	events := acpSessionEventParams(messages, "run_status")
	if len(events) != 1 {
		t.Fatalf("run_status events = %#v, want exactly one", events)
	}
	event := events[0]
	if event["sessionId"] != "session-1" || event["runId"] != "run-1" || event["status"] != "running" {
		t.Fatalf("run_status event = %#v", event)
	}
}

// --- §4.2 session metadata and projects --------------------------------------

func TestACPOptionalProjectIDShapes(t *testing.T) {
	present, value, err := acpOptionalProjectID(nil)
	if err != nil || present || value != "" {
		t.Fatalf("absent projectId = %v, %q, %v, want keep-current", present, value, err)
	}
	present, value, err = acpOptionalProjectID(json.RawMessage(`null`))
	if err != nil || !present || value != "" {
		t.Fatalf("null projectId = %v, %q, %v, want explicit clear", present, value, err)
	}
	present, value, err = acpOptionalProjectID(json.RawMessage(`" project-9 "`))
	if err != nil || !present || value != "project-9" {
		t.Fatalf("string projectId = %v, %q, %v, want trimmed assignment", present, value, err)
	}
	if _, _, err := acpOptionalProjectID(json.RawMessage(`42`)); err == nil {
		t.Fatal("numeric projectId must be rejected")
	}
}

func TestACPSetSessionMetaValidatesParamsBeforeStorage(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)

	s.handleSetSessionMeta(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/session/setMeta", Params: json.RawMessage(`{}`)})
	s.handleSetSessionMeta(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/session/setMeta", Params: json.RawMessage(`{"sessionId":"s"}`)})
	s.handleSetSessionMeta(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "mothx/session/setMeta", Params: json.RawMessage(`{"sessionId":"s","projectId":42}`)})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 3 {
		t.Fatalf("messages = %#v, want three structured errors", messages)
	}
	for _, message := range messages {
		assertACPRPCErrorCode(t, message, "invalid_params")
	}
}

func TestACPProjectsHandlersReturnStructuredErrorsWithoutSettings(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.handleProjectsList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/projects/list"})
	s.handleProjectsCreate(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/projects/create", Params: json.RawMessage(`{"name":"x"}`)})
	s.handleProjectsCreate(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "mothx/projects/create", Params: json.RawMessage(`{}`)})
	s.handleProjectsRename(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`4`), Method: "mothx/projects/rename", Params: json.RawMessage(`{"id":"p"}`)})
	s.handleProjectsDelete(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`5`), Method: "mothx/projects/delete", Params: json.RawMessage(`{}`)})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 5 {
		t.Fatalf("messages = %#v, want five responses", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "projects_unavailable")
	assertACPRPCErrorCode(t, messages[1], "projects_unavailable")
	assertACPRPCErrorCode(t, messages[2], "invalid_params")
	assertACPRPCErrorCode(t, messages[3], "invalid_params")
	assertACPRPCErrorCode(t, messages[4], "invalid_params")
}

// --- §4.3 workspace extension ------------------------------------------------

func TestACPWorkspaceExtendValidationMergeAndLimit(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.cwd = t.TempDir()
	existing := t.TempDir()
	added := t.TempDir()

	// Relative paths are rejected with a structured error.
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/workspace/extend", Params: json.RawMessage(`{"additionalDirectories":["relative/dir"]}`)})
	// Missing directories are rejected.
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/workspace/extend", Params: json.RawMessage(fmt.Sprintf(`{"additionalDirectories":[%q]}`, filepath.Join(added, "missing")))})
	// Empty input is rejected.
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "mothx/workspace/extend", Params: json.RawMessage(`{"additionalDirectories":[]}`)})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 3 {
		t.Fatalf("messages = %#v, want three structured errors", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "workspace_directory_invalid")
	assertACPRPCErrorCode(t, messages[1], "workspace_directory_unavailable")
	assertACPRPCErrorCode(t, messages[2], "invalid_params")
	if s.workspaceAdditionalDirectories != nil {
		t.Fatalf("rejected extends mutated the window: %#v", s.workspaceAdditionalDirectories)
	}

	// A valid extend grows the window, dedupes, and responds with cwd + dirs.
	s.workspaceAdditionalDirectories = []string{existing}
	output.Reset()
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`4`), Method: "mothx/workspace/extend", Params: json.RawMessage(fmt.Sprintf(`{"additionalDirectories":[%q,%q]}`, added, added))})
	if len(s.workspaceAdditionalDirectories) != 2 {
		t.Fatalf("window = %#v, want the existing plus the deduped addition", s.workspaceAdditionalDirectories)
	}
	responses := parseACPMessages(t, output.String())
	var result map[string]any
	for _, message := range responses {
		if id, ok := message["id"].(float64); ok && id == 4 {
			result, _ = message["result"].(map[string]any)
		}
	}
	if result == nil {
		t.Fatalf("extend response missing: %#v", responses)
	}
	if result["cwd"] != s.cwd {
		t.Fatalf("extend cwd = %#v, want the immutable %q", result["cwd"], s.cwd)
	}
	dirs, _ := result["additionalDirectories"].([]any)
	if len(dirs) != 2 {
		t.Fatalf("extend additionalDirectories = %#v, want both roots", dirs)
	}
	events := acpSessionEventParams(responses, "workspace")
	if len(events) != 1 {
		t.Fatalf("workspace events = %#v, want exactly one", events)
	}
	if events[0]["cwd"] != s.cwd {
		t.Fatalf("workspace event = %#v", events[0])
	}

	// The window is capped at 16 additional directories.
	many := make([]string, 0, 15)
	for index := 0; index < 15; index++ {
		many = append(many, fmt.Sprintf("%q", t.TempDir()))
	}
	output.Reset()
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`5`), Method: "mothx/workspace/extend", Params: json.RawMessage(fmt.Sprintf(`{"additionalDirectories":[%s]}`, strings.Join(many, ",")))})
	limited := parseACPMessages(t, output.String())
	if len(limited) != 1 {
		t.Fatalf("limit messages = %#v, want one structured error", limited)
	}
	assertACPRPCErrorCode(t, limited[0], "workspace_limit_exceeded")
	if len(s.workspaceAdditionalDirectories) != 2 {
		t.Fatalf("rejected limit extend mutated the window: %#v", s.workspaceAdditionalDirectories)
	}
}

func TestACPWorkspaceExtendNormalizesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.cwd = t.TempDir()
	s.handleWorkspaceExtend(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/workspace/extend", Params: json.RawMessage(fmt.Sprintf(`{"additionalDirectories":[%q]}`, link))})
	if len(s.workspaceAdditionalDirectories) != 1 || s.workspaceAdditionalDirectories[0] != resolvedTarget {
		t.Fatalf("window = %#v, want the EvalSymlinks-normalized target %q", s.workspaceAdditionalDirectories, resolvedTarget)
	}
}

// --- §4.4 decision deadline reminders ----------------------------------------

func TestACPScheduleDecisionDeadlineEmitsBothMarksAndStopsCleanly(t *testing.T) {
	originalCap, originalFinal := decisionDeadlineFirstNoticeCap, decisionDeadlineFinalNotice
	decisionDeadlineFirstNoticeCap = time.Hour
	decisionDeadlineFinalNotice = 30 * time.Millisecond
	defer func() {
		decisionDeadlineFirstNoticeCap = originalCap
		decisionDeadlineFinalNotice = originalFinal
	}()

	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	timeout := 100 * time.Millisecond
	deadline := time.Now().Add(timeout)
	// first = min(1h, 50ms) = 50ms; second = 100ms-30ms = 70ms > first.
	stop := s.scheduleDecisionDeadline("session-1", "req-1", agentruntime.DecisionQuestion, timeout, deadline)
	waitForACPOutputCount(t, output, 2, 3*time.Second)
	stop()
	time.Sleep(120 * time.Millisecond)
	events := acpSessionEventParams(parseACPMessages(t, output.String()), "decision_deadline")
	if len(events) != 2 {
		t.Fatalf("decision_deadline events = %#v, want exactly the two marks and none after stop", events)
	}
	for _, event := range events {
		if event["requestId"] != "req-1" || event["kind"] != "question" || event["sessionId"] != "session-1" {
			t.Fatalf("decision_deadline event = %#v", event)
		}
		if _, ok := event["deadline"].(string); !ok {
			t.Fatalf("decision_deadline deadline = %#v, want an RFC3339 timestamp", event)
		}
		if remaining, ok := event["remainingMs"].(float64); !ok || remaining < 0 {
			t.Fatalf("decision_deadline remainingMs = %#v", event["remainingMs"])
		}
	}
}

func TestACPScheduleDecisionDeadlineStopsBeforeFirstMark(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	stop := s.scheduleDecisionDeadline("session-1", "req-2", agentruntime.DecisionApproval, 40*time.Millisecond, time.Now().Add(40*time.Millisecond))
	stop()
	time.Sleep(120 * time.Millisecond)
	if events := acpSessionEventParams(parseACPMessages(t, output.String()), "decision_deadline"); len(events) != 0 {
		t.Fatalf("decision_deadline events after immediate stop = %#v, want none", events)
	}
	// Double stop must be safe.
	stop()
}

func TestACPScheduleDecisionDeadlineSkipsFinalNoticeForShortTimeouts(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	// timeout/2 = 10ms fires; timeout-60s is negative and must not schedule.
	stop := s.scheduleDecisionDeadline("session-1", "req-3", agentruntime.DecisionApproval, 20*time.Millisecond, time.Now().Add(20*time.Millisecond))
	defer stop()
	waitForACPOutputCount(t, output, 1, 3*time.Second)
	time.Sleep(80 * time.Millisecond)
	events := acpSessionEventParams(parseACPMessages(t, output.String()), "decision_deadline")
	if len(events) != 1 {
		t.Fatalf("decision_deadline events = %#v, want only the first mark", events)
	}
}

// waitForACPOutputCount blocks until the fixture output contains at least
// want complete JSON lines mentioning decision_deadline, or fails on timeout.
func waitForACPOutputCount(t *testing.T, output *syncedBuffer, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count := 0
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.Contains(line, `"decision_deadline"`) {
				count++
			}
		}
		if count >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d decision_deadline events: %s", want, output.String())
}

// --- §4.6 sub-agent lifecycle events -----------------------------------------

func TestACPObserveSubagentEventProjectsStartedAndSingleTerminal(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)

	// Parent-scoped events never produce subagent projections.
	s.observeSubagentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: "parent"})
	if len(acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")) != 0 {
		t.Fatalf("parent event projected a subagent event: %s", output.String())
	}

	// First child event projects "started"; later activity does not repeat it.
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-1", Type: agentpkg.EventTextDelta, TextDelta: "child", MemberID: "engineer", ExpertID: "software-company", MemberDisplayName: "工程师", MemberEmoji: "🛠️", MemberRole: "member"})
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-1", Type: agentpkg.EventToolExecutionEnd, ToolCallID: "call-1"})
	events := acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 1 || events[0]["status"] != "started" || events[0]["agentId"] != "child-1" || events[0]["sessionId"] != "session-1" {
		t.Fatalf("subagent events = %#v, want exactly one started projection", events)
	}
	if events[0]["memberId"] != "engineer" || events[0]["expertId"] != "software-company" || events[0]["memberDisplayName"] != "工程师" || events[0]["memberEmoji"] != "🛠️" || events[0]["memberRole"] != "member" {
		t.Fatalf("started member metadata = %#v", events[0])
	}

	// The canonical terminal projects exactly one "completed".
	output.Reset()
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-1", Type: agentpkg.EventRunFinished, Status: agentpkg.TaskSuccess})
	// Legacy terminals after the canonical one must not duplicate.
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-1", Type: agentpkg.EventDone, Done: true})
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 1 || events[0]["status"] != "completed" {
		t.Fatalf("terminal events = %#v, want exactly one completed projection", events)
	}
	if events[0]["memberDisplayName"] != "工程师" || events[0]["memberEmoji"] != "🛠️" || events[0]["memberRole"] != "member" {
		t.Fatalf("terminal member metadata = %#v", events[0])
	}

	// A failing child maps to "failed".
	output.Reset()
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-2", Type: agentpkg.EventRunFinished, Status: agentpkg.TaskFailed})
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 2 || events[0]["status"] != "started" || events[1]["status"] != "failed" {
		t.Fatalf("failed child events = %#v, want started+failed", events)
	}

	// A cancelled child also maps to the terminal "failed" vocabulary and a
	// legacy EventError afterwards must not add another terminal.
	output.Reset()
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-3", Type: agentpkg.EventRunFinished, Status: agentpkg.TaskCanceled})
	s.observeSubagentEvent("session-1", agentpkg.Event{AgentID: "child-3", Type: agentpkg.EventError})
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 2 || events[0]["status"] != "started" || events[1]["status"] != "failed" {
		t.Fatalf("cancelled child events = %#v, want started+failed", events)
	}

	// Legacy-only streams still pair started with completed.
	output.Reset()
	s.observeSubagentEvent("session-2", agentpkg.Event{AgentID: "child-4", Type: agentpkg.EventDone, Done: true})
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 2 || events[0]["status"] != "started" || events[1]["status"] != "completed" || events[0]["sessionId"] != "session-2" {
		t.Fatalf("legacy child events = %#v, want started+completed", events)
	}

	// Shutdown clears the projection state so a reused agent ID starts fresh.
	s.clearSubagentProjections("session-2")
	output.Reset()
	s.observeSubagentEvent("session-2", agentpkg.Event{AgentID: "child-4", Type: agentpkg.EventTextDelta})
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "subagent")
	if len(events) != 1 || events[0]["status"] != "started" {
		t.Fatalf("events after clear = %#v, want a fresh started projection", events)
	}
}

// --- §4.7 tool result images -------------------------------------------------

func TestACPToolImageContentsProjectsWithinLimits(t *testing.T) {
	small := base64.StdEncoding.EncodeToString([]byte("pixel"))
	contents := acpToolImageContents([]agentpkg.ToolImage{
		{MimeType: "image/png", Data: small},
		{MimeType: "", Data: small},          // empty mime falls back to image/png
		{MimeType: "image/png", Data: "   "}, // empty payload is skipped
	})
	if len(contents) != 2 {
		t.Fatalf("contents = %#v, want two image blocks", contents)
	}
	for _, content := range contents {
		if content.Type != "content" || content.Content == nil || content.Content.Type != "image" {
			t.Fatalf("content = %#v, want an image content block", content)
		}
		if content.Content.Data != small {
			t.Fatalf("image data = %q, want the identical base64 payload", content.Content.Data)
		}
		if content.Content.MimeType != "image/png" {
			t.Fatalf("image mime = %q, want image/png", content.Content.MimeType)
		}
	}
	if got := acpToolImageContents(nil); got != nil {
		t.Fatalf("nil images = %#v, want nil", got)
	}
}

func TestACPToolImageContentsDegradesOversizedAndExcessImages(t *testing.T) {
	// One decoded byte over the 2MB limit.
	oversized := base64.StdEncoding.EncodeToString(make([]byte, acpToolImageMaxBytes+1))
	small := base64.StdEncoding.EncodeToString([]byte("pixel"))
	images := []agentpkg.ToolImage{{MimeType: "image/png", Data: oversized}}
	for index := 0; index < acpToolImageMaxCount+2; index++ {
		images = append(images, agentpkg.ToolImage{MimeType: "image/jpeg", Data: small})
	}
	contents := acpToolImageContents(images)
	imagesSeen, notesSeen := 0, 0
	for _, content := range contents {
		if content.Content == nil {
			continue
		}
		switch content.Content.Type {
		case "image":
			imagesSeen++
		case "text":
			notesSeen++
			if !strings.Contains(content.Content.Text, "not projected") {
				t.Fatalf("degraded note = %q, want an explanatory text", content.Content.Text)
			}
		}
	}
	if imagesSeen != acpToolImageMaxCount {
		t.Fatalf("projected images = %d, want the %d-image cap", imagesSeen, acpToolImageMaxCount)
	}
	if notesSeen != 3 {
		t.Fatalf("degraded notes = %d, want one oversized plus two excess-image notes", notesSeen)
	}
}

// --- §4.8 attachment list validation -----------------------------------------

func TestACPAttachmentListValidatesParamsBeforeStorage(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.handleAttachmentList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "mothx/attachment/list", Params: json.RawMessage(`{}`)})
	s.handleAttachmentList(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "mothx/attachment/list", Params: json.RawMessage(`{"sessionId":"s","status":"expired"}`)})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 2 {
		t.Fatalf("messages = %#v, want two structured errors", messages)
	}
	assertACPRPCErrorCode(t, messages[0], "invalid_params")
	assertACPRPCErrorCode(t, messages[1], "attachment_list_invalid_status")
}

// --- §4.9 capability discovery ------------------------------------------------

func TestACPInitializeDeclaresPhaseOneFeatureKeys(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.handleInitialize(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"protocolVersion":1}`)})
	var response struct {
		Result struct {
			Meta map[string]any `json:"_meta"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.String())), &response); err != nil {
		t.Fatalf("parse initialize response: %v", err)
	}
	namespace, _ := response.Result.Meta[mothxExtensionNamespace].(map[string]any)
	rawFeatures, _ := namespace["features"].([]any)
	features := make(map[string]bool, len(rawFeatures))
	for _, feature := range rawFeatures {
		if name, ok := feature.(string); ok {
			features[name] = true
		}
	}
	for _, want := range []string{
		"runStatus", "sessionMeta", "projects", "workspaceExtend",
		"decisionDeadline", "subagentEvents", "toolResultImages", "attachmentList",
		"sessionListAll", "sessionWorkDir",
		// Phase 0 keys must survive the additive change.
		"artifactProjection", "attachmentFetch",
	} {
		if !features[want] {
			t.Fatalf("features = %#v, want %q", features, want)
		}
	}
}

// --- agent event projection integration ---------------------------------------

func TestACPHandleAgentEventProjectsToolResultImages(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	payload := base64.StdEncoding.EncodeToString([]byte("screenshot-bytes"))
	s.handleAgentEvent("session-1", agentpkg.Event{
		Type:       agentpkg.EventToolExecutionEnd,
		ToolCallID: "call-read",
		ToolName:   "read",
		ToolResult: "image attached",
		ToolImages: []agentpkg.ToolImage{{MimeType: "image/png", Data: payload}},
	})
	messages := parseACPMessages(t, output.String())
	if len(messages) != 1 || messages[0]["method"] != "session/update" {
		t.Fatalf("messages = %#v, want one tool_call_update", messages)
	}
	params, _ := messages[0]["params"].(map[string]any)
	update, _ := params["update"].(map[string]any)
	if update["sessionUpdate"] != "tool_call_update" || update["status"] != "completed" {
		t.Fatalf("update = %#v", update)
	}
	contents, _ := update["content"].([]any)
	if len(contents) != 2 {
		t.Fatalf("content = %#v, want the text and image blocks", contents)
	}
	imageContent, _ := contents[1].(map[string]any)
	block, _ := imageContent["content"].(map[string]any)
	if imageContent["type"] != "content" || block["type"] != "image" || block["mimeType"] != "image/png" || block["data"] != payload {
		t.Fatalf("image block = %#v, want the identical base64 payload", imageContent)
	}
	// The raw output stays text-only: base64 payloads never duplicate there.
	rawOutput, _ := update["rawOutput"].(map[string]any)
	if raw, _ := rawOutput["content"].(string); raw != "image attached" {
		t.Fatalf("rawOutput content = %#v", rawOutput)
	}
}

func TestACPToolBoundaryStartsNewAssistantMessage(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.sessions["session-1"] = &sessionRuntime{
		id:               "session-1",
		promptID:         "prompt-1",
		messageID:        acpStreamMessageID("session-1", "prompt-1", "message", 0),
		thoughtMessageID: acpStreamMessageID("session-1", "prompt-1", "thought", 0),
	}

	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: "before tool"})
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventToolCall, ToolCall: &agentpkg.ToolCallBlock{ID: "call-1", Name: "read"}})
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventToolExecutionEnd, ToolCallID: "call-1", ToolName: "read", ToolResult: "done"})
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTurnStart})
	s.handleAgentEvent("session-1", agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: "final answer"})

	messages := parseACPMessages(t, output.String())
	updates := make([]map[string]any, 0, 4)
	for _, message := range messages {
		if message["method"] != "session/update" {
			continue
		}
		params, _ := message["params"].(map[string]any)
		updates = append(updates, params["update"].(map[string]any))
	}
	if len(updates) != 4 {
		t.Fatalf("updates = %#v, want pre-tool text, tool call, tool result, and final text", updates)
	}
	if updates[0]["sessionUpdate"] != "agent_message_chunk" || updates[1]["sessionUpdate"] != "tool_call" || updates[2]["sessionUpdate"] != "tool_call_update" || updates[3]["sessionUpdate"] != "agent_message_chunk" {
		t.Fatalf("update order = %#v, want text, tool call, tool result, final text", updates)
	}
	if updates[0]["messageId"] == updates[3]["messageId"] {
		t.Fatalf("pre-tool and final message IDs = %q, want distinct model-turn IDs", updates[0]["messageId"])
	}
}

func TestACPDecisionRequestProjectsDeadlineReminders(t *testing.T) {
	output := &syncedBuffer{}
	s := newPhase1FixtureServer(output)
	s.permissionTimeout = 40 * time.Millisecond
	if s.requestPermissionContext(context.Background(), "session-1", "call-1", "bash", map[string]any{"command": "true"}) {
		t.Fatal("timed-out approval unexpectedly allowed the call")
	}
	events := acpSessionEventParams(parseACPMessages(t, output.String()), "decision_deadline")
	if len(events) != 1 {
		t.Fatalf("decision_deadline events = %#v, want exactly the first mark before the timeout", events)
	}
	if events[0]["kind"] != "approval" || events[0]["sessionId"] != "session-1" {
		t.Fatalf("decision_deadline event = %#v", events[0])
	}
	// Resolution stopped the timer: waiting past the deadline adds nothing.
	time.Sleep(80 * time.Millisecond)
	events = acpSessionEventParams(parseACPMessages(t, output.String()), "decision_deadline")
	if len(events) != 1 {
		t.Fatalf("decision_deadline events after resolution = %#v, want no additional reminders", events)
	}
}
