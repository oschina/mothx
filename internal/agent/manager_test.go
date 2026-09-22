package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

// --- AgentManager tests ---

func newTestManager() *AgentManager {
	factory := &AgentFactory{}
	return NewAgentManager(factory)
}

func TestAgentManagerCreate(t *testing.T) {
	m := newTestManager()

	a, err := m.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.ID() != "main" {
		t.Errorf("expected ID 'main', got %q", a.ID())
	}
}

func TestAgentManagerCreateAutoID(t *testing.T) {
	m := newTestManager()

	a, err := m.Create(AgentOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ID() == "" {
		t.Error("expected non-empty auto-generated ID")
	}
}

func TestAgentManagerUpdateRuntimeConfigAffectsFutureAgents(t *testing.T) {
	oldModel := &provider.Model{ID: "old-model", Name: "Old", Provider: "old-provider"}
	oldProvider := provider.NewMockProvider("old-provider", []*provider.Model{oldModel}, nil)
	newModel := &provider.Model{ID: "new-model", Name: "New", Provider: "new-provider"}
	newProvider := provider.NewMockProvider("new-provider", []*provider.Model{newModel}, nil)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "new-provider"
	settings.DefaultModel = "new-model"

	m := NewAgentManager(NewAgentFactory(oldProvider, oldModel, config.DefaultSettings(), nil, "", "", nil, compactionSettings(), nil))
	m.UpdateRuntimeConfig(newProvider, "new-provider", newModel, settings, nil)

	a, err := m.Create(AgentOptions{ID: "future"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg, ok := runtimeConfigOfManagedAgent(a)
	if !ok {
		t.Fatal("future agent does not expose runtime config")
	}
	if cfg.Provider != newProvider {
		t.Fatalf("Provider = %#v, want new provider", cfg.Provider)
	}
	if cfg.Model == nil || cfg.Model.ID != "new-model" {
		t.Fatalf("Model = %#v, want new-model", cfg.Model)
	}
	if cfg.Settings == nil || cfg.Settings.DefaultProvider != "new-provider" || cfg.Settings.DefaultModel != "new-model" {
		t.Fatalf("Settings defaults = %#v, want new-provider/new-model", cfg.Settings)
	}
}

func TestManagerCreatedLeadReceivesTeamToolsAndMailboxSteering(t *testing.T) {
	model := &provider.Model{ID: "m1", Name: "M1"}
	p := provider.NewMockProvider("mock", []*provider.Model{model}, nil)
	factory := NewAgentFactoryWithOptions(p, model, config.DefaultSettings(), nil, "", "", nil, compactionSettings(), nil, AgentFactoryOptions{MultiAgentEnabled: true})
	manager := NewAgentManager(factory)
	mailbox := NewMemberMailbox()
	manager.SetMemberContext(NewMemberDefRegistry([]*MemberDef{{ID: "engineer"}}), mailbox, "team")
	yes := true
	created, err := manager.Create(AgentOptions{ID: "esm-worker", MultiAgent: &yes})
	if err != nil {
		t.Fatalf("create ESM lead: %v", err)
	}
	adapter, ok := created.(*AgentAdapter)
	if !ok || adapter.inner == nil {
		t.Fatalf("created agent = %#v, want AgentAdapter", created)
	}
	if _, ok := adapter.inner.registry.Get("subagent_spawn"); !ok {
		t.Fatal("manager-created team lead missing subagent_spawn")
	}
	if adapter.inner.config.GetSteeringMessages == nil {
		t.Fatal("manager-created team lead missing mailbox steering")
	}
	mailbox.Enqueue(MemberCompletion{MemberID: "engineer", Status: "done", Payload: "completed work"})
	if messages := adapter.inner.config.GetSteeringMessages(); len(messages) != 1 || !messages[0].SystemInjected {
		t.Fatalf("mailbox steering = %#v, want one system-injected completion", messages)
	}

	no := false
	critic, err := manager.Create(AgentOptions{ID: "esm-critic", MultiAgent: &no, Tools: []string{"read"}})
	if err != nil {
		t.Fatalf("create critic: %v", err)
	}
	criticAdapter := critic.(*AgentAdapter)
	if _, ok := criticAdapter.inner.registry.Get("subagent_spawn"); ok {
		t.Fatal("isolated critic unexpectedly received subagent_spawn")
	}
}

func TestAgentManagerCreateWithParent(t *testing.T) {
	m := newTestManager()

	parent, _ := m.Create(AgentOptions{ID: "main"})
	child, err := m.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if child.ParentID() != "main" {
		t.Errorf("expected parent 'main', got %q", child.ParentID())
	}

	children := m.Children("main")
	if len(children) != 1 || children[0] != "sub-1" {
		t.Errorf("expected [sub-1], got %v", children)
	}

	pid, ok := m.Parent("sub-1")
	if !ok || pid != "main" {
		t.Errorf("expected parent 'main', got %q (ok=%v)", pid, ok)
	}

	_ = parent
}

func TestAgentManagerCreateNestedSubAgentRejected(t *testing.T) {
	m := newTestManager()

	// Create a sub-agent
	m.Create(AgentOptions{ID: "main"})
	m.Create(AgentOptions{ID: "sub-1", ParentID: "main"})

	// Try to create a sub-sub-agent (should fail - Decision 5)
	_, err := m.Create(AgentOptions{ID: "sub-sub-1", ParentID: "sub-1"})
	if err == nil {
		t.Fatal("expected error for nested sub-agent, got nil")
	}
}

func TestAgentManagerCreateMissingParent(t *testing.T) {
	m := newTestManager()

	_, err := m.Create(AgentOptions{ID: "orphan", ParentID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing parent, got nil")
	}
}

func TestAgentManagerGet(t *testing.T) {
	m := newTestManager()
	m.Create(AgentOptions{ID: "main"})

	a, ok := m.Get("main")
	if !ok || a == nil {
		t.Fatal("expected to find agent 'main'")
	}

	_, ok = m.Get("nonexistent")
	if ok {
		t.Error("expected not to find agent 'nonexistent'")
	}
}

func TestAgentManagerDestroy(t *testing.T) {
	m := newTestManager()
	m.Create(AgentOptions{ID: "main"})
	m.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	m.Create(AgentOptions{ID: "sub-2", ParentID: "main"})

	if m.Count() != 3 {
		t.Errorf("expected 3 agents, got %d", m.Count())
	}

	err := m.Destroy("main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All should be destroyed (children recursively)
	if m.Count() != 0 {
		t.Errorf("expected 0 agents after destroy, got %d", m.Count())
	}
}

func TestAgentManagerDestroyChild(t *testing.T) {
	m := newTestManager()
	m.Create(AgentOptions{ID: "main"})
	m.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	m.Create(AgentOptions{ID: "sub-2", ParentID: "main"})

	err := m.Destroy("sub-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parent should still exist with one child
	if m.Count() != 2 {
		t.Errorf("expected 2 agents, got %d", m.Count())
	}
	children := m.Children("main")
	if len(children) != 1 || children[0] != "sub-2" {
		t.Errorf("expected [sub-2], got %v", children)
	}
}

func TestAgentManagerFinishCancelsChildrenAndRetainsStatus(t *testing.T) {
	m := newTestManager()
	parent, err := m.Create(AgentOptions{ID: "main"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	m.Create(AgentOptions{ID: "sub-1", ParentID: "main"})
	m.MarkRunning("sub-1")

	cancelled := false
	m.SetCancel("sub-1", func() {
		cancelled = true
	})
	m.Finish("main", errors.New("network error"))

	if !cancelled {
		t.Fatal("expected child cancel func to be called")
	}
	if m.Count() != 0 {
		t.Fatalf("expected no active agents, got %d", m.Count())
	}
	if _, ok := m.Status("main"); ok {
		t.Fatal("expected finished parent status to be removed")
	}
	st, ok := m.Status("sub-1")
	if !ok {
		t.Fatal("expected child status to be retained")
	}
	if st.State != "error" {
		t.Fatalf("expected child state error, got %q", st.State)
	}
	if st.Error != "network error" {
		t.Fatalf("expected child error to preserve cause, got %q", st.Error)
	}

	if adapter, ok := parent.(*AgentAdapter); ok {
		select {
		case <-adapter.inner.abort:
			t.Fatal("Finish aborted the completed parent agent")
		default:
		}
	}
}

func TestAgentManagerFinishSuccessKeepsAsyncChildren(t *testing.T) {
	m := newTestManager()
	if _, err := m.Create(AgentOptions{ID: "main"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := m.Create(AgentOptions{ID: "sub-1", ParentID: "main"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	m.MarkRunning("sub-1")

	cancelled := false
	m.SetCancel("sub-1", func() {
		cancelled = true
	})

	m.Finish("main", nil)

	if cancelled {
		t.Fatal("successful parent finish cancelled running async child")
	}
	if _, ok := m.Get("main"); ok {
		t.Fatal("expected finished parent to be removed")
	}
	if _, ok := m.Get("sub-1"); !ok {
		t.Fatal("expected async child to remain active")
	}
	st, ok := m.Status("sub-1")
	if !ok {
		t.Fatal("expected child status to remain available")
	}
	if st.State != "running" {
		t.Fatalf("child state = %q, want running", st.State)
	}
}

func TestAgentManagerDestroyNotFound(t *testing.T) {
	m := newTestManager()
	err := m.Destroy("nonexistent")
	if err == nil {
		t.Fatal("expected error for destroying nonexistent agent")
	}
}

func TestAgentManagerList(t *testing.T) {
	m := newTestManager()
	if _, err := m.Create(AgentOptions{ID: "a"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := m.Create(AgentOptions{ID: "b"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := m.Create(AgentOptions{ID: "c"}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	ids := m.List()
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
	want := []agentpkg.AgentID{"a", "b", "c"}
	for i, id := range want {
		if ids[i] != id {
			t.Fatalf("ids[%d] = %s, want %s; full list: %v", i, ids[i], id, ids)
		}
	}
	for i := 0; i < 20; i++ {
		got := m.List()
		for j, id := range want {
			if got[j] != id {
				t.Fatalf("list order changed on iteration %d: got %v, want %v", i, got, want)
			}
		}
	}
}

func TestAgentManagerChildrenEmpty(t *testing.T) {
	m := newTestManager()
	m.Create(AgentOptions{ID: "main"})

	children := m.Children("main")
	if children != nil {
		t.Errorf("expected nil children, got %v", children)
	}
}

func TestAgentManagerParentNotFound(t *testing.T) {
	m := newTestManager()
	_, ok := m.Parent("nonexistent")
	if ok {
		t.Error("expected false for nonexistent agent")
	}
}

func TestAgentManagerConcurrent(t *testing.T) {
	m := newTestManager()
	m.Create(AgentOptions{ID: "main"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Create(AgentOptions{ID: agentpkg.AgentID("sub"), ParentID: "main"})
		}()
	}
	wg.Wait()

	// Some will fail due to duplicate IDs, but no panic
	if m.Count() < 2 {
		t.Errorf("expected at least 2 agents, got %d", m.Count())
	}
}

// --- EventRouter tests ---

func TestEventRouterDispatch(t *testing.T) {
	r := NewEventRouter()

	var received []agentpkg.Event
	r.RegisterAgent("agent-1", RouterEventHandlerFunc(func(e agentpkg.Event) error {
		received = append(received, e)
		return nil
	}))

	r.Dispatch(agentpkg.Event{AgentID: "agent-1", Type: agentpkg.EventTextDelta, TextDelta: "hello"})
	r.Dispatch(agentpkg.Event{AgentID: "agent-2", Type: agentpkg.EventTextDelta, TextDelta: "world"})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].TextDelta != "hello" {
		t.Errorf("expected 'hello', got %q", received[0].TextDelta)
	}
}

func TestEventRouterGlobal(t *testing.T) {
	r := NewEventRouter()

	var received []agentpkg.Event
	r.RegisterGlobal(RouterEventHandlerFunc(func(e agentpkg.Event) error {
		received = append(received, e)
		return nil
	}))

	r.Dispatch(agentpkg.Event{AgentID: "a1", Type: agentpkg.EventDone})
	r.Dispatch(agentpkg.Event{AgentID: "a2", Type: agentpkg.EventDone})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func TestEventRouterUnregisterAgent(t *testing.T) {
	r := NewEventRouter()

	count := 0
	r.RegisterAgent("a1", RouterEventHandlerFunc(func(e agentpkg.Event) error {
		count++
		return nil
	}))

	r.Dispatch(agentpkg.Event{AgentID: "a1"})
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}

	r.UnregisterAgent("a1")
	r.Dispatch(agentpkg.Event{AgentID: "a1"})
	if count != 1 {
		t.Errorf("expected still 1 after unregister, got %d", count)
	}
}

func TestEventRouterError(t *testing.T) {
	r := NewEventRouter()
	testErr := errors.New("test error")

	r.RegisterAgent("a1", RouterEventHandlerFunc(func(e agentpkg.Event) error {
		return testErr
	}))

	err := r.Dispatch(agentpkg.Event{AgentID: "a1"})
	if err != testErr {
		t.Errorf("expected test error, got %v", err)
	}
}

func TestEventRouterHandlerCount(t *testing.T) {
	r := NewEventRouter()
	r.RegisterAgent("a1", RouterEventHandlerFunc(func(e agentpkg.Event) error { return nil }))
	r.RegisterAgent("a1", RouterEventHandlerFunc(func(e agentpkg.Event) error { return nil }))
	r.RegisterGlobal(RouterEventHandlerFunc(func(e agentpkg.Event) error { return nil }))

	if r.HandlerCount("a1") != 2 {
		t.Errorf("expected 2 handlers for a1, got %d", r.HandlerCount("a1"))
	}
	if r.HandlerCount("a2") != 0 {
		t.Errorf("expected 0 handlers for a2, got %d", r.HandlerCount("a2"))
	}
	if r.GlobalHandlerCount() != 1 {
		t.Errorf("expected 1 global handler, got %d", r.GlobalHandlerCount())
	}
}

func TestEventRouterMultipleAgents(t *testing.T) {
	r := NewEventRouter()

	var mu sync.Mutex
	received := map[agentpkg.AgentID][]string{}

	r.RegisterGlobal(RouterEventHandlerFunc(func(e agentpkg.Event) error {
		mu.Lock()
		received[e.AgentID] = append(received[e.AgentID], e.TextDelta)
		mu.Unlock()
		return nil
	}))

	r.Dispatch(agentpkg.Event{AgentID: "a1", TextDelta: "from-a1"})
	r.Dispatch(agentpkg.Event{AgentID: "a2", TextDelta: "from-a2"})
	r.Dispatch(agentpkg.Event{AgentID: "a1", TextDelta: "from-a1-again"})

	if len(received["a1"]) != 2 {
		t.Errorf("expected 2 events for a1, got %d", len(received["a1"]))
	}
	if len(received["a2"]) != 1 {
		t.Errorf("expected 1 event for a2, got %d", len(received["a2"]))
	}
}

// --- AgentAdapter tests ---

func TestAgentAdapterImplementsInterface(t *testing.T) {
	// Verify AgentAdapter satisfies agent.Agent interface at compile time
	var _ agentpkg.Agent = (*AgentAdapter)(nil)
}

func TestEventToPublic(t *testing.T) {
	e := Event{
		AgentID:                   "test-agent",
		Type:                      EventTextDelta,
		MemberID:                  "engineer",
		ExpertID:                  "software-company",
		MemberDisplayName:         "工程师",
		MemberEmoji:               "🛠️",
		MemberRole:                "member",
		TextDelta:                 "hello",
		ToolCallID:                "tc1",
		ToolName:                  "bash",
		ToolArgs:                  map[string]any{"cmd": "ls"},
		StatusMessage:             "running",
		ResponseStateFailureClass: "expired",
		Done:                      true,
		StopReason:                "end_turn",
		Error:                     context.Canceled,
		ApprovalID:                "ap1",
		ApprovalTool:              "write",
		ApprovalResult:            true,
		Attachments:               []provider.Attachment{{Kind: "citation", Name: "docs", URL: "https://example.test", Metadata: map[string]any{"source": "test"}}},
	}

	pub := EventToPublic(e)
	if pub.AgentID != "test-agent" {
		t.Errorf("expected agent ID 'test-agent', got %q", pub.AgentID)
	}
	if pub.Type != agentpkg.EventTextDelta {
		t.Errorf("expected EventTextDelta, got %d", pub.Type)
	}
	if pub.MemberID != "engineer" || pub.ExpertID != "software-company" || pub.MemberDisplayName != "工程师" || pub.MemberEmoji != "🛠️" || pub.MemberRole != "member" {
		t.Errorf("expert member metadata = %#v", pub)
	}
	if pub.TextDelta != "hello" {
		t.Errorf("expected 'hello', got %q", pub.TextDelta)
	}
	if pub.ResponseStateFailureClass != "expired" {
		t.Errorf("expected response state failure class, got %q", pub.ResponseStateFailureClass)
	}
	if pub.Error != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", pub.Error)
	}
	if !pub.ApprovalResult {
		t.Error("expected ApprovalResult=true")
	}
	if len(pub.Attachments) != 1 || pub.Attachments[0].URL != "https://example.test" || pub.Attachments[0].Metadata["source"] != "test" {
		t.Errorf("attachments = %#v", pub.Attachments)
	}
}

func TestStreamEventAttachmentRoundTrip(t *testing.T) {
	internal := provider.StreamEvent{Type: provider.StreamDone, Attachments: []provider.Attachment{{
		Kind: "file", Name: "report.csv", ProviderRef: "file_1",
	}}}
	public := StreamEventToPublic(internal)
	back := StreamEventFromPublic(public)
	if len(back.Attachments) != 1 || back.Attachments[0].ProviderRef != "file_1" {
		t.Fatalf("attachment round trip = %#v", back.Attachments)
	}
}

func TestStreamEventHostedItemRoundTrip(t *testing.T) {
	internal := provider.StreamEvent{Type: provider.StreamHostedItem, HostedItem: &provider.HostedItem{
		ID: "search_1", Type: "web_search_call", Status: "completed", OutputIndex: 2,
	}}
	public := StreamEventToPublic(internal)
	if public.Type != agentpkg.StreamHostedItem || public.HostedItem == nil || public.HostedItem.ID != "search_1" || public.HostedItem.OutputIndex != 2 {
		t.Fatalf("hosted item public event = %#v", public)
	}
	back := StreamEventFromPublic(public)
	if back.Type != provider.StreamHostedItem || back.HostedItem == nil || back.HostedItem.Status != "completed" {
		t.Fatalf("hosted item round trip = %#v", back)
	}
}

func TestStreamEventRetryRoundTrip(t *testing.T) {
	internal := provider.StreamEvent{
		Type:         provider.StreamRetry,
		RetryAttempt: 2,
		RetryMax:     4, // Legacy producers remain supported by the public bridge.
		RetryAfterMS: 1250,
	}
	public := StreamEventToPublic(internal)
	if public.Type != agentpkg.StreamRetry || public.RetryAttempt != 2 || public.RetryMaxAttempts != 4 || public.RetryAfterMS != 1250 {
		t.Fatalf("public retry event = %#v", public)
	}
	back := StreamEventFromPublic(public)
	if back.Type != provider.StreamRetry || back.RetryAttempt != 2 || back.RetryMax != 4 || back.RetryMaxAttempts != 4 || back.RetryAfterMS != 1250 {
		t.Fatalf("round-tripped retry event = %#v", back)
	}
}

func TestEventToPublicPreservesRetryMetadata(t *testing.T) {
	public := EventToPublic(Event{
		Type:             EventRetry,
		RetryStatus:      true,
		RetryAttempt:     2,
		RetryMaxAttempts: 4,
		RetryAfterMS:     1250,
		RetryMaxTokens:   4096,
		RetryReason:      "service unavailable",
		RetryContinue:    true,
	})
	if public.Type != agentpkg.EventRetry || !public.RetryStatus || public.RetryAttempt != 2 || public.RetryMaxAttempts != 4 || public.RetryAfterMS != 1250 || public.RetryMaxTokens != 4096 || public.RetryReason != "service unavailable" || !public.RetryContinue {
		t.Fatalf("public retry event = %#v", public)
	}
}

func TestMessageRoundTrip(t *testing.T) {
	original := agentpkg.Message{
		Role:    agentpkg.RoleAssistant,
		Content: "test content",
		Contents: []agentpkg.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "toolCall", ToolCall: &agentpkg.ToolCallBlock{ID: "tc1", Name: "bash"}},
			{Type: "file", File: &agentpkg.FileContent{ID: "file_123", Filename: "report.csv"}},
		},
		Usage: &agentpkg.Usage{InputTokens: 100, OutputTokens: 50},
		Attachments: []agentpkg.Attachment{{
			Kind: "citation", Name: "docs", URL: "https://example.test/docs", ProviderRef: "source_1",
		}},
	}

	internal := MessageFromPublic(original)
	back := MessageToPublic(internal)

	if back.Role != original.Role {
		t.Errorf("role mismatch: %q vs %q", back.Role, original.Role)
	}
	if back.Content != original.Content {
		t.Errorf("content mismatch: %q vs %q", back.Content, original.Content)
	}
	if len(back.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(back.Contents))
	}
	if back.Contents[1].ToolCall.Name != "bash" {
		t.Errorf("tool call name mismatch: %q", back.Contents[1].ToolCall.Name)
	}
	if back.Contents[2].File == nil || back.Contents[2].File.ID != "file_123" {
		t.Errorf("file block mismatch: %#v", back.Contents[2].File)
	}
	if back.Usage.InputTokens != 100 {
		t.Errorf("usage mismatch: %d", back.Usage.InputTokens)
	}
	if len(back.Attachments) != 1 || back.Attachments[0].ProviderRef != "source_1" {
		t.Errorf("attachments mismatch: %#v", back.Attachments)
	}
}

func TestContextUsageToPublicNil(t *testing.T) {
	if ContextUsageToPublic(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestWrapEventChan(t *testing.T) {
	in := make(chan Event, 2)
	in <- Event{AgentID: "a1", Type: EventTextDelta, TextDelta: "hi"}
	in <- Event{AgentID: "a1", Type: EventDone}
	close(in)

	out := WrapEventChan(in)
	var events []agentpkg.Event
	for e := range out {
		events = append(events, e)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].TextDelta != "hi" {
		t.Errorf("expected 'hi', got %q", events[0].TextDelta)
	}
}

func TestAgentManagerStatusListenerTerminalTransitions(t *testing.T) {
	m := newTestManager()
	var mu sync.Mutex
	var got []ManagedAgentStatus
	m.AddStatusListener(func(st ManagedAgentStatus) {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
	})

	if _, err := m.Create(AgentOptions{ID: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(AgentOptions{ID: "sub-1", ParentID: "main"}); err != nil {
		t.Fatal(err)
	}

	// Non-terminal transitions must not fire.
	m.MarkRunning("sub-1")
	// Terminal transition fires exactly once; repeats are suppressed.
	m.MarkDone("sub-1", "result")
	m.MarkDone("sub-1", "result")

	mu.Lock()
	if len(got) != 1 || got[0].ID != "sub-1" || got[0].State != "done" || got[0].ParentID != "main" {
		t.Fatalf("listener events = %#v, want one done for sub-1", got)
	}
	mu.Unlock()

	// Finish with a cause transitions remaining children to error and fires,
	// while already-done children stay done and silent.
	if _, err := m.Create(AgentOptions{ID: "sub-2", ParentID: "main"}); err != nil {
		t.Fatal(err)
	}
	m.Finish("main", errors.New("parent failed"))

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("listener events = %#v, want done + error", got)
	}
	last := got[1]
	if last.ID != "sub-2" || last.State != "error" || last.Error != "parent failed" || last.ParentID != "main" {
		t.Fatalf("error transition = %#v", last)
	}
}
