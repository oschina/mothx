package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/tools"
)

// noopTool keeps the loop going (a turn with a tool call never ends the run) so
// a test can drive the loop toward its iteration limit.
type noopTool struct{}

func (noopTool) Name() string               { return "noop" }
func (noopTool) Description() string        { return "no-op" }
func (noopTool) PromptSnippet() string      { return "no-op" }
func (noopTool) PromptGuidelines() []string { return nil }
func (noopTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (noopTool) Execute(context.Context, map[string]any) (tools.ToolResult, error) {
	return tools.NewTextToolResult("ok"), nil
}

func TestIterationBudgetPolicyNormalize(t *testing.T) {
	p := IterationBudgetPolicy{}.Normalize(90)
	if p.Soft != 90 || p.Hard != 180 {
		t.Fatalf("soft/hard = %d/%d, want 90/180", p.Soft, p.Hard)
	}
	if p.RenewFactor != defaultRenewFactor || p.MaxRenewals != defaultMaxRenewals {
		t.Fatalf("renew factor/renewals = %v/%d", p.RenewFactor, p.MaxRenewals)
	}
	if p.MinInterval != 9 {
		t.Fatalf("min interval = %d, want 9 (soft/10)", p.MinInterval)
	}
	if p.MaxWallClock != DefaultIterationBudgetWallClock {
		t.Fatalf("wall clock = %v, want %v", p.MaxWallClock, DefaultIterationBudgetWallClock)
	}
	if !p.Enabled() {
		t.Fatal("normalized policy must be enabled")
	}
	if (IterationBudgetPolicy{}).Enabled() {
		t.Fatal("zero policy must be disabled")
	}
}

func TestIterationBudgetRequestClamp(t *testing.T) {
	b := newIterationBudget(IterationBudgetPolicy{
		Soft: 10, Hard: 20, RenewFactor: 0.5, MaxRenewals: 2, MinInterval: 3, MaxWallClock: time.Hour,
	}, 10)

	if _, _, _, err := b.Request(0, "   "); err == nil {
		t.Fatal("an empty reason must be rejected")
	}

	b.setTurn(0)
	granted, remaining, renewals, err := b.Request(0, "unfinished")
	if err != nil || granted != 5 || renewals != 1 {
		t.Fatalf("first request = granted %d renewals %d err %v, want 5/1/nil", granted, renewals, err)
	}
	if b.Limit() != 15 || remaining != 15 {
		t.Fatalf("limit/remaining = %d/%d, want 15/15", b.Limit(), remaining)
	}

	// Too soon: only one turn elapsed since the last grant.
	b.setTurn(1)
	if _, _, _, err := b.Request(0, "again"); err == nil {
		t.Fatal("a request inside the minimum interval must be rejected")
	}

	b.setTurn(4)
	if _, _, _, err := b.Request(100, "more"); err != nil {
		t.Fatalf("second request errored: %v", err)
	}
	if b.Limit() != 20 {
		t.Fatalf("limit = %d, want the hard ceiling 20", b.Limit())
	}

	b.setTurn(9)
	if _, _, _, err := b.Request(0, "one more"); err == nil {
		t.Fatal("a request past MaxRenewals must be rejected")
	}
}

// TestIterationBudgetCanRenew pins the gate that decides whether the model-facing
// budget notice still points at the renewal tool.
func TestIterationBudgetCanRenew(t *testing.T) {
	var absent *iterationBudget
	if absent.CanRenew() {
		t.Fatal("a nil budget can never renew")
	}

	b := newIterationBudget(IterationBudgetPolicy{
		Soft: 10, Hard: 20, RenewFactor: 0.5, MaxRenewals: 2, MinInterval: 3, MaxWallClock: time.Hour,
	}, 10)
	if !b.CanRenew() {
		t.Fatal("a fresh budget below the ceiling may renew")
	}

	// Spend every renewal: no more grants are possible even below the ceiling.
	b.setTurn(0)
	if _, _, _, err := b.Request(0, "first"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	b.setTurn(4)
	if _, _, _, err := b.Request(0, "second"); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if b.CanRenew() {
		t.Fatal("a budget with no renewals left must not advertise renewal")
	}

	// A single-renewal budget that lands exactly on the hard ceiling is done too.
	capped := newIterationBudget(IterationBudgetPolicy{
		Soft: 10, Hard: 15, RenewFactor: 0.5, MaxRenewals: 5, MinInterval: 1, MaxWallClock: time.Hour,
	}, 10)
	capped.setTurn(0)
	if _, _, _, err := capped.Request(100, "to the ceiling"); err != nil {
		t.Fatalf("ceiling request: %v", err)
	}
	if capped.Limit() != 15 {
		t.Fatalf("limit = %d, want the hard ceiling 15", capped.Limit())
	}
	if capped.CanRenew() {
		t.Fatal("a budget at its hard ceiling must not advertise renewal")
	}
}

func TestIterationBudgetContextRoundTrip(t *testing.T) {
	if _, ok := iterationBudgetFromContext(context.Background()); ok {
		t.Fatal("no budget must be found in a bare context")
	}
	b := newIterationBudget(IterationBudgetPolicy{Soft: 4, Hard: 8}, 4)
	ctx := contextWithIterationBudget(context.Background(), b)
	got, ok := iterationBudgetFromContext(ctx)
	if !ok || got != b {
		t.Fatal("budget handle did not round-trip through the context")
	}
}

func TestExtendBudgetTool(t *testing.T) {
	tool := NewExtendBudgetTool()
	if tool.Name() != IterationBudgetToolName {
		t.Fatalf("tool name = %q, want %q", tool.Name(), IterationBudgetToolName)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"reason": "x"}); err == nil {
		t.Fatal("a run without a budget handle must reject the tool")
	}

	b := newIterationBudget(IterationBudgetPolicy{Soft: 4, Hard: 12, RenewFactor: 0.5, MaxRenewals: 2, MinInterval: 1}, 4)
	ctx := contextWithIterationBudget(context.Background(), b)
	if _, err := tool.Execute(ctx, map[string]any{}); err == nil {
		t.Fatal("a missing reason must be rejected")
	}
	res, err := tool.Execute(ctx, map[string]any{"reason": "still working"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Text, "Granted 2 additional turns") {
		t.Fatalf("unexpected tool result %q", res.Text)
	}
	if b.Limit() != 6 {
		t.Fatalf("limit = %d, want 6", b.Limit())
	}
}

// TestLoopInjectsBudgetNoticeOnce drives the loop to its limit and verifies the
// model-visible budget notice is a single transient system-injected message.
func TestLoopInjectsBudgetNoticeOnce(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(noopTool{})
	scripted := newScriptedProvider([]provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "c1", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		{Type: provider.StreamDone, StopReason: "tool_use"},
	})

	a := NewWithLoopConfig(AgentLoopConfig{
		Config:        Config{ID: "lead", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		MaxIterations: 10, BudgetPressureThreshold: 0.6,
		IterationBudget: IterationBudgetPolicy{Soft: 10, Hard: 20, RenewFactor: 0.5, MaxRenewals: 2, MinInterval: 5, MaxWallClock: time.Hour},
	}, registry)

	status, reason, _ := collectTerminalEvents(t, a.Run(context.Background(), "go"))
	if status != TaskIncomplete || reason != "max_iterations" {
		t.Fatalf("terminal = %q/%q, want incomplete/max_iterations", status, reason)
	}

	notices := 0
	for _, msg := range a.messages {
		if msg.SystemInjected && strings.Contains(msg.Content, "[Budget Pressure]") {
			notices++
			if !strings.Contains(msg.Content, IterationBudgetToolName) {
				t.Fatalf("notice must point the model at %s: %q", IterationBudgetToolName, msg.Content)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("budget notices = %d, want exactly 1", notices)
	}
}

// TestLoopRenewsBudgetViaTool proves a model-initiated renewal keeps the run
// alive past its soft limit and that the run then finishes normally.
func TestLoopRenewsBudgetViaTool(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	registry.Register(NewExtendBudgetTool())
	scripted := newScriptedProvider(
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "c1", Name: IterationBudgetToolName, Arguments: json.RawMessage(`{"reason":"unfinished work"}`)}},
			{Type: provider.StreamDone, StopReason: "tool_use"},
		},
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "c2", Name: IterationBudgetToolName, Arguments: json.RawMessage(`{"reason":"still unfinished"}`)}},
			{Type: provider.StreamDone, StopReason: "tool_use"},
		},
		[]provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "done"},
			{Type: provider.StreamDone, StopReason: "stop"},
		},
	)

	a := NewWithLoopConfig(AgentLoopConfig{
		Config:        Config{ID: "lead", Provider: scripted, Model: scripted.models[0], Mode: "yolo"},
		MaxIterations: 2,
		IterationBudget: IterationBudgetPolicy{
			Soft: 2, Hard: 6, RenewFactor: 1.0, MaxRenewals: 2, MinInterval: 1, MaxWallClock: time.Hour,
		},
	}, registry)

	status, reason, errorEvent := collectTerminalEvents(t, a.Run(context.Background(), "go"))
	if status != TaskSuccess || errorEvent {
		t.Fatalf("terminal = %q reason = %q errorEvent = %v, want a successful run past the soft limit", status, reason, errorEvent)
	}
	if scripted.calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (the run must continue past the soft limit of 2)", scripted.calls)
	}
}

// TestCacheMarkersUnaffectedByBudgetNotice pins the cache invariant: a transient
// system-injected notice appended at the tail never moves the cache breakpoints
// off the newest real messages.
func TestCacheMarkersUnaffectedByBudgetNotice(t *testing.T) {
	base := []provider.Message{
		{Role: "user", Content: "[session context]", SystemInjected: true},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	before := selectCacheMarkers(base)
	withNotice := append(append([]provider.Message{}, base...), provider.NewSystemInjectedUserMessage("[Budget Pressure] 1/10 turns remaining"))
	after := selectCacheMarkers(withNotice)
	if before != after {
		t.Fatalf("cache markers moved from %v to %v after a tail notice", before, after)
	}
}
