package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func TestSubAgentWaitToolMetadata(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentWaitTool(mgr)
	if tool.Name() != "subagent_wait" {
		t.Fatalf("name = %q, want subagent_wait", tool.Name())
	}
	if tool.Description() == "" || tool.PromptSnippet() == "" {
		t.Fatal("expected description and prompt snippet")
	}
	if len(tool.PromptGuidelines()) == 0 {
		t.Fatal("expected wait guidelines")
	}
	joined := strings.Join(tool.PromptGuidelines(), " ")
	if !strings.Contains(joined, "reflexive") {
		t.Fatalf("guidelines must forbid reflexive waiting: %q", joined)
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["timeout_ms"]; !ok {
		t.Fatal("expected optional timeout_ms parameter")
	}
	if required, ok := schema["required"]; ok && required != nil {
		t.Fatalf("subagent_wait must not have required params, got %v", required)
	}
}

func TestResolveSubAgentWaitTimeoutMS(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   int
	}{
		{"default when absent", map[string]any{}, subAgentWaitDefaultTimeoutMS},
		{"clamp below min", map[string]any{"timeout_ms": float64(10)}, subAgentWaitMinTimeoutMS},
		{"clamp zero", map[string]any{"timeout_ms": float64(0)}, subAgentWaitMinTimeoutMS},
		{"clamp negative", map[string]any{"timeout_ms": float64(-500)}, subAgentWaitMinTimeoutMS},
		{"clamp above max", map[string]any{"timeout_ms": float64(1000000)}, subAgentWaitMaxTimeoutMS},
		{"in range", map[string]any{"timeout_ms": float64(5000)}, 5000},
		{"int in range", map[string]any{"timeout_ms": 60000}, 60000},
		{"int below min", map[string]any{"timeout_ms": 100}, subAgentWaitMinTimeoutMS},
		{"wrong type falls back to default", map[string]any{"timeout_ms": "soon"}, subAgentWaitDefaultTimeoutMS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveSubAgentWaitTimeoutMS(tt.params); got != tt.want {
				t.Fatalf("resolveSubAgentWaitTimeoutMS(%v) = %d, want %d", tt.params, got, tt.want)
			}
		})
	}
	if subAgentWaitMinTimeoutMS != 2500 || subAgentWaitMaxTimeoutMS != 120000 || subAgentWaitDefaultTimeoutMS != 30000 {
		t.Fatalf("wait window constants = %d/%d/%d, want 2500/120000/30000",
			subAgentWaitMinTimeoutMS, subAgentWaitMaxTimeoutMS, subAgentWaitDefaultTimeoutMS)
	}
}

type subAgentWaitParsed struct {
	Message  string `json:"message"`
	TimedOut bool   `json:"timed_out"`
	Pending  []struct {
		Member      string `json:"member"`
		Status      string `json:"status"`
		DisplayName string `json:"display_name"`
	} `json:"pending"`
}

func parseSubAgentWaitResult(t testing.TB, result tools.ToolResult) subAgentWaitParsed {
	t.Helper()
	var parsed subAgentWaitParsed
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse wait result %q: %v", result.Text, err)
	}
	return parsed
}

func TestSubAgentWaitToolNilMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentWaitTool(mgr)

	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed := parseSubAgentWaitResult(t, result)
	if parsed.Message != "no member mailbox is bound to this session" {
		t.Fatalf("message = %q", parsed.Message)
	}
	if parsed.TimedOut {
		t.Fatal("nil mailbox must not report timeout")
	}
	if len(parsed.Pending) != 0 {
		t.Fatalf("nil mailbox must not report pending, got %v", parsed.Pending)
	}
	if strings.Contains(result.Text, "pending") {
		t.Fatalf("nil mailbox result must omit the pending field: %q", result.Text)
	}
}

func TestSubAgentWaitToolPendingSummaryExcludesPayload(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(NewMemberDefRegistry(nil), mbox, "software-company")
	mbox.Enqueue(MemberCompletion{
		MemberID:    "engineer",
		DisplayName: "工程师",
		Status:      MemberStatusDone,
		Payload:     "SECRET-PAYLOAD-CONTENT",
	})

	tool := NewSubAgentWaitTool(mgr)
	start := time.Now()
	result, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("wait with pending completions must return immediately, took %v", elapsed)
	}
	if strings.Contains(result.Text, "SECRET-PAYLOAD-CONTENT") {
		t.Fatalf("wait summary must not include payload content: %q", result.Text)
	}
	parsed := parseSubAgentWaitResult(t, result)
	if parsed.Message != "Wait completed." || parsed.TimedOut {
		t.Fatalf("message = %q timed_out = %v", parsed.Message, parsed.TimedOut)
	}
	if len(parsed.Pending) != 1 {
		t.Fatalf("pending = %v, want one entry", parsed.Pending)
	}
	entry := parsed.Pending[0]
	if entry.Member != "engineer" || entry.Status != "done" || entry.DisplayName != "工程师" {
		t.Fatalf("pending entry = %#v", entry)
	}
	// The summary is read-only: delivery stays with the mailbox drain.
	if !mbox.HasPending() {
		t.Fatal("wait must not drain the mailbox")
	}
}

func TestSubAgentWaitToolReturnsOnActivity(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mbox, "")
	go func() {
		time.Sleep(30 * time.Millisecond)
		mbox.Enqueue(MemberCompletion{MemberID: "qa", Status: MemberStatusDone, Payload: "passed"})
	}()

	tool := NewSubAgentWaitTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{"timeout_ms": float64(10000)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parsed := parseSubAgentWaitResult(t, result)
	if parsed.Message != "Wait completed." || parsed.TimedOut {
		t.Fatalf("message = %q timed_out = %v, want activity return", parsed.Message, parsed.TimedOut)
	}
	if len(parsed.Pending) != 1 || parsed.Pending[0].Member != "qa" || parsed.Pending[0].Status != "done" {
		t.Fatalf("pending = %#v", parsed.Pending)
	}
}

func TestSubAgentWaitToolTimeout(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mbox, "")

	tool := NewSubAgentWaitTool(mgr)
	start := time.Now()
	// timeout_ms below the minimum must be clamped up to 2500ms.
	result, err := tool.Execute(context.Background(), map[string]any{"timeout_ms": float64(1)})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 2400*time.Millisecond {
		t.Fatalf("wait returned after %v, want >= ~2.5s (min clamp)", elapsed)
	}
	parsed := parseSubAgentWaitResult(t, result)
	if parsed.Message != "Wait timed out." || !parsed.TimedOut {
		t.Fatalf("message = %q timed_out = %v, want timeout", parsed.Message, parsed.TimedOut)
	}
	if len(parsed.Pending) != 0 {
		t.Fatalf("pending = %v, want empty", parsed.Pending)
	}
}

func TestSubAgentWaitToolContextCanceled(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mbox, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewSubAgentWaitTool(mgr)
	_, err := tool.Execute(ctx, map[string]any{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want wrapped context.Canceled", err)
	}
}

func TestRegisterSubAgentToolsIncludesWait(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	RegisterSubAgentTools(registry, mgr)

	wait, ok := registry.Get("subagent_wait")
	if !ok {
		t.Fatal("expected subagent_wait to be registered")
	}
	if wait.Name() != "subagent_wait" {
		t.Fatalf("registered tool name = %q", wait.Name())
	}
	for _, name := range []string{"subagent_spawn", "subagent_status", "subagent_send", "subagent_destroy"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected %s to stay registered", name)
		}
	}
}

func TestSubAgentFactoryRemovesWaitToolFromChildren(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(AgentOptions{ID: "child", ParentID: parent.ID()})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	adapter, ok := child.(*AgentAdapter)
	if !ok {
		t.Fatalf("expected AgentAdapter, got %T", child)
	}
	// Decision 5: sub-agents cannot nest; the child registry must not expose
	// any subagent_* tool, including subagent_wait.
	if _, ok := adapter.inner.registry.Get("subagent_wait"); ok {
		t.Fatal("child registry must not contain subagent_wait")
	}
	for _, name := range []string{"subagent_spawn", "subagent_status", "subagent_send", "subagent_destroy", "delegate_subagent"} {
		if _, ok := adapter.inner.registry.Get(name); ok {
			t.Fatalf("child registry must not contain %s", name)
		}
	}
}
