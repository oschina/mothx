package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
)

// errMockMemberFailure drives the spawn error-terminal mailbox test.
var errMockMemberFailure = errors.New("mock member failure")

// newMemberTestManager builds an AgentManager backed by a mock provider whose
// stream completes with the given assistant text, and installs the expert-team
// member context (roster + mailbox + expert id).
func newMemberTestManager(t testing.TB, defs []*MemberDef, responses []provider.StreamEvent) (*AgentManager, *MemberMailbox) {
	t.Helper()
	if responses == nil {
		responses = []provider.StreamEvent{
			{Type: provider.StreamStart},
			{Type: provider.StreamTextDelta, TextDelta: "member final result"},
			{Type: provider.StreamDone, StopReason: "stop"},
		}
	}
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{
		{ID: "model1", Name: "Model 1"},
	}, responses)
	sandboxMgr := sandbox.NewManager(t.TempDir())
	sandboxMgr.SetLevel(sandbox.LevelNone)
	settings := &config.Settings{SessionDir: t.TempDir()}
	factory := NewAgentFactory(
		mockProvider,
		mockProvider.Models()[0],
		settings,
		sandboxMgr,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{},
		nil,
	)
	mgr := NewAgentManager(factory)
	mbox := NewMemberMailbox()
	mgr.SetMemberContext(NewMemberDefRegistry(defs), mbox, "software-company")
	return mgr, mbox
}

func spawnHandleFromResult(t testing.TB, resultText string) string {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(resultText), &parsed); err != nil {
		t.Fatalf("parse spawn result %q: %v", resultText, err)
	}
	handle, _ := parsed["handle"].(string)
	if handle == "" {
		t.Fatalf("spawn result has no handle: %q", resultText)
	}
	return handle
}

func engineerDefs() []*MemberDef {
	return []*MemberDef{{
		ID:            "engineer",
		DisplayName:   "工程师",
		Emoji:         "🛠️",
		Role:          "member",
		Description:   "批量编码",
		Prompt:        "ENGINEER-PERSONA",
		Mode:          "plan",
		Tools:         []string{"read", "grep"},
		MaxIterations: 7,
	}}
}

func TestSubAgentSpawnToolMemberWithoutBinding(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSpawnTool(mgr)
	_, err := tool.Execute(context.Background(), map[string]any{
		"task":   "implement the feature",
		"member": "engineer",
	})
	if err == nil {
		t.Fatal("expected tool error when no expert team is bound")
	}
	if err.Error() != "no expert team is bound to this session" {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestSubAgentSpawnToolUnknownMember(t *testing.T) {
	mgr, _ := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	_, err = tool.Execute(ctx, map[string]any{
		"task":   "implement the feature",
		"member": "ghost",
	})
	if err == nil {
		t.Fatal("expected tool error for unknown member")
	}
	if !strings.Contains(err.Error(), `unknown member "ghost"`) {
		t.Fatalf("err = %q, want unknown member", err.Error())
	}
	if !strings.Contains(err.Error(), "known members: [engineer]") {
		t.Fatalf("err = %q, want known member ids listed", err.Error())
	}
	if mgr.Count() != 1 {
		t.Fatalf("failed member spawn must not create a child, count = %d", mgr.Count())
	}
}

func TestSubAgentSpawnToolMemberDefOverrides(t *testing.T) {
	workDir := t.TempDir()
	defs := engineerDefs()
	defs[0].WorkDir = workDir
	mgr, _ := newMemberTestManager(t, defs, nil)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "yolo"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithParentMode(ctx, "yolo")

	result, err := tool.Execute(ctx, map[string]any{
		"task":   "implement the feature",
		"member": "engineer",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatalf("expected spawned child %q", handle)
	}
	loopCfg, ok := runtimeConfigOfManagedAgent(child)
	if !ok {
		t.Fatal("expected child runtime config")
	}
	// MemberDef mode wins over parent-mode inheritance; existing validation unchanged.
	if loopCfg.Config.Mode != "plan" {
		t.Fatalf("child mode = %q, want def mode plan", loopCfg.Config.Mode)
	}
	if loopCfg.MaxIterations != 7 {
		t.Fatalf("child max iterations = %d, want def value 7", loopCfg.MaxIterations)
	}
	// Persona prompt is injected as SystemPromptExtra.
	if !strings.Contains(loopCfg.Config.ExtraContext, "ENGINEER-PERSONA") {
		t.Fatalf("child extra context = %q, want persona injection", loopCfg.Config.ExtraContext)
	}
	adapter := child.(*AgentAdapter)
	childTools := adapter.inner.GetContext().Tools
	if !toolNamesContain(childTools, "read") || !toolNamesContain(childTools, "grep") {
		t.Fatalf("child tools = %v, want def tools read+grep", toolNames(childTools))
	}
	if toolNamesContain(childTools, "write") {
		t.Fatal("def tools must narrow the child registry")
	}
	if got := workDirForAgent(adapter.inner); got != workDir {
		t.Fatalf("child work dir = %q, want def work dir %q", got, workDir)
	}

	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolExplicitParamsOnlyNarrowMemberDef(t *testing.T) {
	explicitDir := t.TempDir()
	mgr, _ := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())

	result, err := tool.Execute(ctx, map[string]any{
		"task":                "implement the feature",
		"member":              "engineer",
		"mode":                "plan",
		"tools":               []any{"write", "read"},
		"max_iterations":      float64(3),
		"work_dir":            explicitDir,
		"system_prompt_extra": "CALLER-EXTRA",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatalf("expected spawned child %q", handle)
	}
	loopCfg, _ := runtimeConfigOfManagedAgent(child)
	if loopCfg.Config.Mode != "plan" {
		t.Fatalf("child mode = %q, want explicit plan within def ceiling", loopCfg.Config.Mode)
	}
	if loopCfg.MaxIterations != 3 {
		t.Fatalf("child max iterations = %d, want explicit 3 over def 7", loopCfg.MaxIterations)
	}
	// Persona injection concatenates: def.Prompt + "\n\n" + caller extra.
	if !strings.Contains(loopCfg.Config.ExtraContext, "ENGINEER-PERSONA\n\nCALLER-EXTRA") {
		t.Fatalf("child extra context = %q, want persona before caller extra", loopCfg.Config.ExtraContext)
	}
	adapter := child.(*AgentAdapter)
	childTools := adapter.inner.GetContext().Tools
	if !toolNamesContain(childTools, "read") {
		t.Fatalf("child tools = %v, want narrowed explicit read", toolNames(childTools))
	}
	if toolNamesContain(childTools, "write") || toolNamesContain(childTools, "grep") {
		t.Fatalf("explicit tools must be intersected with def tools, got %v", toolNames(childTools))
	}
	if got := workDirForAgent(adapter.inner); got != explicitDir {
		t.Fatalf("child work dir = %q, want explicit %q", got, explicitDir)
	}

	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolMemberCannotEscalateModeOrWorkDir(t *testing.T) {
	memberDir := t.TempDir()
	defs := engineerDefs()
	defs[0].WorkDir = memberDir
	mgr, _ := newMemberTestManager(t, defs, nil)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "plan"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithParentMode(ctx, "plan")

	_, err = tool.Execute(ctx, map[string]any{"task": "implement", "member": "engineer", "mode": "yolo"})
	if err == nil || !strings.Contains(err.Error(), "exceeds member/session capability") {
		t.Fatalf("yolo escalation error = %v", err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("mode escalation created a child, count = %d", mgr.Count())
	}

	_, err = tool.Execute(ctx, map[string]any{"task": "implement", "member": "engineer", "work_dir": t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "work_dir is fixed") {
		t.Fatalf("work_dir escalation error = %v", err)
	}
}

func TestSubAgentSpawnToolMemberModeIsClampedByPlanParent(t *testing.T) {
	defs := engineerDefs()
	defs[0].Mode = "yolo"
	mgr, _ := newMemberTestManager(t, defs, nil)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "plan"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithParentMode(ctx, "plan")

	result, err := tool.Execute(ctx, map[string]any{"task": "review", "member": "engineer"})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatalf("expected spawned child %q", handle)
	}
	loopCfg, _ := runtimeConfigOfManagedAgent(child)
	if loopCfg.Config.Mode != "plan" {
		t.Fatalf("child mode = %q, want plan parent ceiling", loopCfg.Config.Mode)
	}
	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
}

func TestSubAgentSpawnToolWithoutMemberKeepsInheritance(t *testing.T) {
	mgr, _ := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main", Mode: "yolo"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithParentMode(ctx, "yolo")

	result, err := tool.Execute(ctx, map[string]any{"task": "investigate"})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)
	child, ok := mgr.Get(agentpkg.AgentID(handle))
	if !ok {
		t.Fatalf("expected spawned child %q", handle)
	}
	loopCfg, _ := runtimeConfigOfManagedAgent(child)
	if loopCfg.Config.Mode != "yolo" {
		t.Fatalf("child mode = %q, want inherited yolo", loopCfg.Config.Mode)
	}
	if loopCfg.MaxIterations != 50 {
		t.Fatalf("child max iterations = %d, want spawn default 50", loopCfg.MaxIterations)
	}
	if strings.Contains(loopCfg.Config.ExtraContext, "ENGINEER-PERSONA") {
		t.Fatal("spawn without member must not inject any persona")
	}
	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolMemberCompletionEntersMailbox(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())

	result, err := tool.Execute(ctx, map[string]any{
		"task":   "implement the feature",
		"member": "engineer",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	timedOut, err := mbox.WaitForActivity(waitCtx, 10*time.Second)
	if err != nil {
		t.Fatalf("wait for mailbox activity: %v", err)
	}
	if timedOut {
		t.Fatal("timed out waiting for the member completion")
	}
	pending := mbox.PendingSummary()
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want exactly one completion", pending)
	}
	c := pending[0]
	if c.MemberID != "engineer" || c.DisplayName != "工程师" {
		t.Fatalf("completion identity = %#v", c)
	}
	if c.Status != MemberStatusDone {
		t.Fatalf("completion status = %q, want done", c.Status)
	}
	if !strings.Contains(c.Payload, "member final result") {
		t.Fatalf("completion payload = %q, want final response", c.Payload)
	}
	// Exactly one notification per run: the legacy EventDone after the
	// canonical EventRunFinished must not enqueue a duplicate.
	time.Sleep(50 * time.Millisecond)
	if got := mbox.PendingSummary(); len(got) != 1 {
		t.Fatalf("pending after settle = %d, want no duplicate enqueue", len(got))
	}
	msgs := mbox.DrainSteering()
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Content, "[MEMBER_COMPLETION]") {
		t.Fatalf("drained messages = %#v", msgs)
	}

	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolGenericSpawnNotifiesBoundMailbox(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())

	result, err := tool.Execute(ctx, map[string]any{"task": "investigate"})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if timedOut, err := mbox.WaitForActivity(waitCtx, 10*time.Second); err != nil || timedOut {
		t.Fatalf("wait for mailbox activity: timedOut=%v err=%v", timedOut, err)
	}
	pending := mbox.PendingSummary()
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want one completion", pending)
	}
	if pending[0].MemberID != "" || pending[0].Status != MemberStatusDone {
		t.Fatalf("generic spawn completion = %#v, want empty member id + done", pending[0])
	}

	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolWithoutMailboxDoesNotFail(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSpawnTool(mgr)
	result, err := tool.Execute(context.Background(), map[string]any{"task": "investigate"})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)
	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestDelegateSubAgentToolDoesNotEnqueueMailbox(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, engineerDefs(), nil)
	if _, err := mgr.Create(AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewDelegateSubAgentTool(mgr)
	ctx := ContextWithAgentID(context.Background(), "main")

	result, err := tool.Execute(ctx, map[string]any{"task": "summarize"})
	if err != nil {
		t.Fatalf("execute delegate: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Text), &parsed); err != nil {
		t.Fatalf("parse delegate result: %v", err)
	}
	if parsed["status"] != "done" {
		t.Fatalf("delegate status = %v, want done", parsed["status"])
	}
	// The blocking delegate path returns results synchronously: it must not
	// enqueue a mailbox completion.
	if mbox.HasPending() {
		t.Fatalf("delegate path must not enqueue, pending = %#v", mbox.PendingSummary())
	}
}

func TestForwardChildAgentEventCarriesMemberMeta(t *testing.T) {
	ch := make(chan Event, 4)
	e := agentpkg.Event{Type: agentpkg.EventStatus, StatusMessage: "working"}

	if !ForwardChildAgentEvent(context.Background(), ch, "agent-1", e, ChildEventMeta{
		MemberID:          "engineer",
		ExpertID:          "software-company",
		MemberDisplayName: "工程师",
		MemberEmoji:       "🛠️",
		MemberRole:        "member",
	}) {
		t.Fatal("expected forward to succeed")
	}
	ev := <-ch
	if ev.MemberID != "engineer" || ev.ExpertID != "software-company" || ev.MemberDisplayName != "工程师" || ev.MemberEmoji != "🛠️" || ev.MemberRole != "member" {
		t.Fatalf("forwarded event = %#v, want complete member metadata", ev)
	}
	if ev.AgentID != "agent-1" || ev.StatusMessage != "working" {
		t.Fatalf("forwarded event lost identity: %#v", ev)
	}

	// Without meta the fields stay empty: existing call sites are unchanged.
	if !ForwardChildAgentEvent(context.Background(), ch, "agent-2", e) {
		t.Fatal("expected forward to succeed")
	}
	ev = <-ch
	if ev.MemberID != "" || ev.ExpertID != "" || ev.MemberDisplayName != "" || ev.MemberEmoji != "" || ev.MemberRole != "" {
		t.Fatalf("forwarded event = %#v, want empty member metadata", ev)
	}
}

func TestSubAgentSpawnToolForwardsMemberMeta(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, engineerDefs(), nil)
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	eventCh := make(chan Event, 200)
	ctx := ContextWithAgentID(context.Background(), parent.ID())
	ctx = ContextWithEventChan(ctx, eventCh)

	tool := NewSubAgentSpawnTool(mgr)
	result, err := tool.Execute(ctx, map[string]any{
		"task":   "implement the feature",
		"member": "engineer",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := agentpkg.AgentID(spawnHandleFromResult(t, result.Text))
	status, ok := mgr.Status(handle)
	if !ok {
		t.Fatalf("managed member status not found for %q", handle)
	}
	if status.MemberID != "engineer" || status.ExpertID != "software-company" || status.MemberDisplayName != "工程师" || status.MemberEmoji != "🛠️" || status.MemberRole != "member" {
		t.Fatalf("managed member status lost metadata: %#v", status)
	}

	found := false
	deadline := time.After(10 * time.Second)
	for !found {
		select {
		case ev := <-eventCh:
			if ev.AgentID == handle && ev.MemberID == "engineer" && ev.ExpertID == "software-company" && ev.MemberDisplayName == "工程师" && ev.MemberEmoji == "🛠️" && ev.MemberRole == "member" {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for a member-tagged forwarded child event")
		}
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := mbox.WaitForActivity(waitCtx, 10*time.Second); err != nil {
		t.Fatalf("wait for mailbox activity: %v", err)
	}
	waitForManagedAgentToStop(t, mgr, handle)
	if err := mgr.Destroy(handle); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolMemberErrorEntersMailbox(t *testing.T) {
	mgr, mbox := newMemberTestManager(t, engineerDefs(), []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamError, Error: errMockMemberFailure},
	})
	parent, err := mgr.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := NewSubAgentSpawnTool(mgr)
	ctx := ContextWithAgentID(context.Background(), parent.ID())

	result, err := tool.Execute(ctx, map[string]any{
		"task":   "implement the feature",
		"member": "engineer",
	})
	if err != nil {
		t.Fatalf("execute spawn: %v", err)
	}
	handle := spawnHandleFromResult(t, result.Text)

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if timedOut, err := mbox.WaitForActivity(waitCtx, 10*time.Second); err != nil || timedOut {
		t.Fatalf("wait for mailbox activity: timedOut=%v err=%v", timedOut, err)
	}
	pending := mbox.PendingSummary()
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want one completion", pending)
	}
	if pending[0].Status != MemberStatusError {
		t.Fatalf("completion status = %q, want error", pending[0].Status)
	}
	if !strings.Contains(pending[0].Payload, "mock member failure") {
		t.Fatalf("completion payload = %q, want error text", pending[0].Payload)
	}
	// The error steering message must carry the next-step re-dispatch hint.
	msgs := mbox.DrainSteering()
	if len(msgs) != 1 {
		t.Fatalf("drained %d messages, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, `subagent_spawn(member:"engineer", task:…)`) {
		t.Fatalf("error steering message missing next-step hint: %q", msgs[0].Content)
	}

	waitForManagedAgentToStop(t, mgr, agentpkg.AgentID(handle))
	if err := mgr.Destroy(agentpkg.AgentID(handle)); err != nil {
		t.Fatalf("destroy spawned agent: %v", err)
	}
}

func TestSubAgentSpawnToolParametersExposeOptionalMember(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	tool := NewSubAgentSpawnTool(mgr)

	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	member, ok := props["member"].(map[string]any)
	if !ok {
		t.Fatal("expected member property in spawn parameters")
	}
	if member["type"] != "string" {
		t.Fatalf("member type = %v, want string", member["type"])
	}
	required, _ := schema["required"].([]any)
	for _, r := range required {
		if r == "member" {
			t.Fatal("member must be optional")
		}
	}

	wantGuideline := "When an expert team roster is present in the system prompt, dispatch members by their id via the member parameter instead of restating personas in the task"
	found := false
	for _, g := range tool.PromptGuidelines() {
		if g == wantGuideline {
			found = true
		}
	}
	if !found {
		t.Fatalf("spawn guidelines missing roster dispatch entry: %v", tool.PromptGuidelines())
	}
}

func toolNames(defs []provider.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}
