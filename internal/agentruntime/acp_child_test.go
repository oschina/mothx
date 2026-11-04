package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

// leadStub is a minimal parent agent used to satisfy the manager's parent
// validation without creating a local AgentFactory child.
type leadStub struct{ id agentpkg.AgentID }

func (a *leadStub) ID() agentpkg.AgentID       { return a.id }
func (a *leadStub) ParentID() agentpkg.AgentID { return "" }
func (a *leadStub) Run(ctx context.Context, msg string) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *leadStub) RunWithMessages(ctx context.Context, msgs []agentpkg.Message) <-chan agentpkg.Event {
	return a.Run(ctx, "")
}
func (a *leadStub) Abort()                                          {}
func (a *leadStub) GetMessages() []agentpkg.Message                 { return nil }
func (a *leadStub) SetMessages(msgs []agentpkg.Message)             {}
func (a *leadStub) GetContext() *agentpkg.AgentContext              { return nil }
func (a *leadStub) SetContext(ctx *agentpkg.AgentContext)           {}
func (a *leadStub) GetContextUsage() *agentpkg.ContextUsage         { return nil }
func (a *leadStub) LoadHistoryMessages(msgs []agentpkg.Message)     {}
func (a *leadStub) HandleApprovalResponse(id string, approved bool) {}

func testExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestACPChildBackendRunsThroughAgentManager(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("router-worker-session"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "helper", Command: []string{testExecutable(t), "-test.run=TestACPWorkerBridgeHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_BRIDGE_HELPER"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	runtime := &SessionRuntime{Manager: manager, ID: "router-worker-session", Source: SourceTUI, WorkDir: workDir}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "helper"}); err != nil {
		t.Fatalf("ActivateRouter: %v", err)
	}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings})
	manager2.Register(&leadStub{id: "lead"})

	child, err := manager2.Create(agent.AgentOptions{
		// A model-supplied work_dir must not move the external worker.
		ParentID: "lead", WorkDir: t.TempDir(),
		ExternalAgent: "helper", ExternalTask: "inspect the retry path",
		ExternalRole: "investigate", ExternalDeliverable: []string{"findings"},
	})
	if err != nil {
		t.Fatalf("Create external child: %v", err)
	}
	if proxy, ok := child.(*acpChildProxy); ok && proxy.workDir != workDir {
		t.Fatalf("external worker workdir = %q, want the Runtime session directory %q", proxy.workDir, workDir)
	}
	var terminal []agentpkg.Event
	for event := range child.Run(t.Context(), "") {
		if event.AgentID != child.ID() {
			t.Fatalf("event %v has AgentID %q, want %q", event.Type, event.AgentID, child.ID())
		}
		if event.Type == agentpkg.EventRunFinished {
			terminal = append(terminal, event)
		}
	}
	if len(terminal) != 1 {
		t.Fatalf("terminal events = %d, want exactly one", len(terminal))
	}
	if terminal[0].Status != agentpkg.TaskSuccess {
		t.Fatalf("terminal status = %q, error = %v", terminal[0].Status, terminal[0].Error)
	}
	messages := child.GetMessages()
	if len(messages) == 0 {
		t.Fatal("child transcript is empty")
	}
	last := messages[len(messages)-1]
	text := last.Content
	for _, block := range last.Contents {
		text += block.Text
	}
	if !strings.Contains(text, "bridge output") {
		t.Fatalf("child transcript = %#v", messages)
	}
	status, ok := manager2.Status(child.ID())
	if !ok || status.State != "ready" {
		t.Fatalf("manager status before the tool monitor = %+v", status)
	}
	if status.Metadata["profileId"] != "helper" || status.Metadata["evidence"] != "worker-reported" {
		t.Fatalf("child metadata = %#v", status.Metadata)
	}
	provider, ok := child.(agent.ChildCompletionProvider)
	if !ok {
		t.Fatal("child does not expose a bounded completion envelope")
	}
	completion := provider.CompletionMetadata()
	if completion["role"] != "investigate" || completion["outputTruncated"] != false {
		t.Fatalf("completion metadata = %#v", completion)
	}

	// A spawned external worker keeps its remote session alive for follow-up
	// turns; subagent_send drives the second Run on the same session.
	var secondTerminal agentpkg.Event
	for event := range child.Run(t.Context(), "continue with the next check") {
		if event.Type == agentpkg.EventRunFinished {
			secondTerminal = event
		}
	}
	if secondTerminal.Status != agentpkg.TaskSuccess || secondTerminal.Error != nil {
		t.Fatalf("second external run = %#v", secondTerminal)
	}
	assistantMessages := 0
	for _, message := range child.GetMessages() {
		if message.Role == agentpkg.RoleAssistant {
			assistantMessages++
		}
	}
	if assistantMessages < 2 {
		t.Fatalf("child transcript did not retain both turns: %#v", child.GetMessages())
	}
	if marker, ok := child.(interface{ LongRunningFollowUp() bool }); !ok || !marker.LongRunningFollowUp() {
		t.Fatal("external child follow-up must not inherit the local wall-clock budget")
	}
	record, found, err := session.LoadChildRun(sessionDir, "router-worker-session", string(child.ID()))
	if err != nil || !found {
		t.Fatalf("load durable child history: found=%t err=%v", found, err)
	}
	if record.RemoteSessionID == "" || record.Status != "success" ||
		!strings.Contains(record.TranscriptJSON, "bridge output") {
		t.Fatalf("durable child history = %#v", record)
	}
}

func TestACPChildBackendRejectsUnactivatedProfile(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("router-worker-unbound"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "helper", Command: []string{"/opt/helper", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	runtime := &SessionRuntime{Manager: manager, ID: "router-worker-unbound", Source: SourceTUI}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings})
	manager2.Register(&leadStub{id: "lead"})
	if _, err := manager2.Create(agent.AgentOptions{
		ParentID: "lead", WorkDir: workDir, ExternalAgent: "helper", ExternalTask: "inspect",
	}); err == nil {
		t.Fatal("external child was created without Router activation")
	}

	// A matching activation must still reject a different target before any
	// child is registered.
	settings.ACPAgents = append(settings.ACPAgents, config.ACPAgentProfile{
		ID: "other", Command: []string{"/opt/other", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	})
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "helper"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager2.Create(agent.AgentOptions{
		ParentID: "lead", WorkDir: workDir, ExternalAgent: "other", ExternalTask: "inspect",
	}); err == nil {
		t.Fatal("mismatched external target was accepted")
	}
}

func TestACPChildBackendSelectsAnyBoundProfile(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("router-worker-multi"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{
		{
			ID: "primary", Command: []string{"/opt/primary", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
		},
		{
			ID: "reviewer", Command: []string{"/opt/reviewer", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoReject,
			Roles:            []config.ACPAgentRole{config.ACPRoleReview},
		},
	}
	runtime := &SessionRuntime{Manager: manager, ID: "router-worker-multi", Source: SourceTUI, WorkDir: workDir}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{
		ACPProfileID: "primary",
		Profiles: []RouterProfileBinding{
			{ProfileID: "primary"}, {ProfileID: "reviewer"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings})
	manager2.Register(&leadStub{id: "lead"})
	child, err := manager2.Create(agent.AgentOptions{
		ParentID: "lead", ExternalAgent: "reviewer", ExternalTask: "review the change",
		ExternalRole: "review", ExternalDeliverable: []string{"review"},
	})
	if err != nil {
		t.Fatalf("Create reviewer child: %v", err)
	}
	proxy, ok := child.(*acpChildProxy)
	if !ok {
		t.Fatalf("child type = %T", child)
	}
	if proxy.profile.ID != "reviewer" || proxy.contract.TargetProfile != "reviewer" {
		t.Fatalf("selected profile = %q contract target = %q", proxy.profile.ID, proxy.contract.TargetProfile)
	}
	if proxy.profile.EffectivePermissionPolicy() != config.ACPPermissionAutoReject {
		t.Fatalf("profile strategy = %q", proxy.profile.EffectivePermissionPolicy())
	}
	if err := manager2.Destroy(child.ID()); err != nil {
		t.Fatalf("destroy reviewer child: %v", err)
	}
}

func TestACPChildRetryWithoutSessionResendsContract(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	t.Setenv("MOTHX_ACP_BRIDGE_FAIL_FIRST_NEW", "1")
	t.Setenv("MOTHX_ACP_BRIDGE_ECHO_PROMPT", "1")
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("router-worker-retry"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "helper", Command: []string{testExecutable(t), "-test.run=TestACPWorkerBridgeHelperProcess", "--"},
		Enabled: true,
		Environment: []string{
			"MOTHX_ACP_BRIDGE_HELPER", "MOTHX_ACP_BRIDGE_FAIL_FIRST_NEW", "MOTHX_ACP_BRIDGE_ECHO_PROMPT",
		},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	runtime := &SessionRuntime{Manager: manager, ID: "router-worker-retry", Source: SourceTUI, WorkDir: workDir}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "helper"}); err != nil {
		t.Fatalf("ActivateRouter: %v", err)
	}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings})
	manager2.Register(&leadStub{id: "lead"})
	child, err := manager2.Create(agent.AgentOptions{
		ParentID: "lead", ExternalAgent: "helper", ExternalTask: "inspect the retry path",
		ExternalRole: "investigate", ExternalDeliverable: []string{"findings"},
	})
	if err != nil {
		t.Fatalf("Create external child: %v", err)
	}
	if terminator, ok := child.(agent.ExternalChildTerminator); ok {
		defer terminator.TerminateExternalChild()
	}

	var firstTerminal agentpkg.Event
	for event := range child.Run(t.Context(), "") {
		if event.Type == agentpkg.EventRunFinished {
			firstTerminal = event
		}
	}
	if firstTerminal.Status != agentpkg.TaskError {
		t.Fatalf("first run terminal = %#v, want a failed session setup", firstTerminal)
	}
	proxy := child.(*acpChildProxy)
	proxy.mu.Lock()
	if proxy.remoteSessionID != "" {
		t.Fatalf("failed session setup retained remote session ID %q", proxy.remoteSessionID)
	}
	proxy.mu.Unlock()

	var secondTerminal agentpkg.Event
	for event := range child.Run(t.Context(), "please retry now") {
		if event.Type == agentpkg.EventRunFinished {
			secondTerminal = event
		}
	}
	if secondTerminal.Status != agentpkg.TaskSuccess {
		t.Fatalf("retry terminal = %#v", secondTerminal)
	}
	var assistant strings.Builder
	for _, message := range child.GetMessages() {
		if message.Role != agentpkg.RoleAssistant {
			continue
		}
		assistant.WriteString(message.Content)
		for _, block := range message.Contents {
			assistant.WriteString(block.Text)
		}
	}
	if !strings.Contains(assistant.String(), "inspect the retry path") ||
		!strings.Contains(assistant.String(), "please retry now") {
		t.Fatalf("retry prompt did not preserve the contract and follow-up: %q", assistant.String())
	}
}

func TestACPChildBackendReservesPermitAtCreation(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("router-worker-permit"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "helper", Command: []string{"/opt/helper", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}
	runtime := &SessionRuntime{Manager: manager, ID: "router-worker-permit", Source: SourceTUI, WorkDir: workDir}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "helper"}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings})
	manager2.Register(&leadStub{id: "lead"})
	opts := agent.AgentOptions{
		ParentID: "lead", WorkDir: workDir, ExternalAgent: "helper", ExternalTask: "inspect",
	}
	first, err := manager2.Create(opts)
	if err != nil {
		t.Fatalf("first external child: %v", err)
	}
	if _, err := manager2.Create(opts); err == nil ||
		!strings.Contains(err.Error(), "already running for this session") {
		t.Fatalf("second external child error = %v, want synchronous permit refusal", err)
	}

	// Destroy before Run releases the construction-time permit, so a later task
	// can start without waiting for a process that was never launched.
	if err := manager2.Destroy(first.ID()); err != nil {
		t.Fatalf("destroy reserved child: %v", err)
	}
	third, err := manager2.Create(opts)
	if err != nil {
		t.Fatalf("permit was not released by Destroy: %v", err)
	}
	third.Abort()
}

func TestACPChildUsesResolvedWorktreeAndPersistsChangeSet(t *testing.T) {
	t.Setenv("MOTHX_ACP_BRIDGE_HELPER", "1")
	repo := initWorktreeTestRepo(t)
	sessionDir := t.TempDir()
	worktrees, _ := newTestWorktreeManager(t, sessionDir)
	manager := session.New(repo, sessionDir)
	if err := manager.InitWithID("router-worker-worktree"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "helper", Command: []string{testExecutable(t), "-test.run=TestACPWorkerBridgeHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_BRIDGE_HELPER"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
	}}
	runtime := &SessionRuntime{
		Manager: manager, ID: "router-worker-worktree", Source: SourceTUI, WorkDir: repo,
		Registry: tools.NewRegistry(repo, nil),
	}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "helper"}); err != nil {
		t.Fatalf("ActivateRouter: %v", err)
	}
	defer runtime.Close()

	manager2 := agent.NewAgentManager(nil)
	manager2.SetChildBackend(ACPChildBackend{Runtime: runtime, Settings: settings, Worktrees: worktrees})
	manager2.SetWorktreeProvider(NewWorktreeProviderAdapter(worktrees))
	manager2.SetWorktreePerChild(true)
	manager2.Register(&leadStub{id: "lead"})

	child, err := manager2.Create(agent.AgentOptions{
		ParentID: "lead", WorkDir: repo, ExternalAgent: "helper",
		ExternalTask: "inspect the isolated worktree", ExternalRole: "investigate",
		ExternalDeliverable: []string{"findings"},
	})
	if err != nil {
		t.Fatalf("Create external child: %v", err)
	}
	proxy, ok := child.(*acpChildProxy)
	if !ok {
		t.Fatalf("child type = %T", child)
	}
	if proxy.worktreeID == "" || proxy.baseRevision == "" {
		t.Fatalf("worktree metadata = id %q base %q", proxy.worktreeID, proxy.baseRevision)
	}
	if proxy.workDir == repo || !strings.HasPrefix(proxy.workDir, worktrees.Root()) {
		t.Fatalf("external worker workdir = %q, want a managed worktree under %q", proxy.workDir, worktrees.Root())
	}
	if proxy.workspace == nil || !proxy.workspace.isolated ||
		proxy.workspace.registry.GetWorkDir() != proxy.workDir ||
		len(proxy.workspace.registry.GetAdditionalDirectories()) != 0 {
		t.Fatalf("isolated workspace projection = %#v", proxy.workspace)
	}

	if err := os.WriteFile(filepath.Join(proxy.workDir, "README.md"), []byte("hello\nworker change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proxy.workDir, "worker.txt"), []byte("worker output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var terminal agentpkg.Event
	for event := range child.Run(t.Context(), "") {
		if event.Type == agentpkg.EventRunFinished {
			terminal = event
		}
	}
	if terminal.Status != agentpkg.TaskSuccess {
		t.Fatalf("terminal = %#v", terminal)
	}
	// The shared spawn/delegate path marks the managed child terminal after it
	// observes EventRunFinished; that transition refreshes backend metadata.
	manager2.MarkDone(child.ID(), "")

	record, found, err := session.LoadChildRun(sessionDir, "router-worker-worktree", string(child.ID()))
	if err != nil || !found {
		t.Fatalf("load durable child history: found=%t err=%v", found, err)
	}
	if record.WorktreeID != proxy.worktreeID || record.BaseRevision != proxy.baseRevision {
		t.Fatalf("child history worktree = %q base = %q", record.WorktreeID, record.BaseRevision)
	}
	if record.ChangeSetError != "" || !strings.Contains(record.ChangeSetJSON, "README.md") ||
		!strings.Contains(record.ChangeSetJSON, "worker.txt") {
		t.Fatalf("child history change set = %q error=%q", record.ChangeSetJSON, record.ChangeSetError)
	}
	status, ok := manager2.Status(child.ID())
	if ok {
		if status.Metadata["worktreeId"] != proxy.worktreeID {
			t.Fatalf("child metadata = %#v", status.Metadata)
		}
		if status.Metadata["changeSetDigest"] == nil {
			t.Fatalf("terminal child metadata is missing the change-set digest: %#v", status.Metadata)
		}
	}
	completion := child.(agent.ChildCompletionProvider).CompletionMetadata()
	if completion["changeSetDigest"] == nil || completion["changeSetTruncated"] != false {
		t.Fatalf("completion change-set metadata = %#v", completion)
	}
}

func TestACPWorkDirIsIsolated(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "workspace")
	child := filepath.Join(base, "nested")
	sibling := filepath.Join(root, "workspace-worker")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	resolution := &agent.WorktreeResolution{Directory: sibling, ID: "wt-1"}

	cases := []struct {
		name       string
		base       string
		candidate  string
		resolution *agent.WorktreeResolution
		want       bool
	}{
		{name: "no resolution", base: base, candidate: sibling, want: false},
		{name: "same directory", base: base, candidate: base, resolution: resolution, want: false},
		{name: "nested directory", base: base, candidate: child, resolution: resolution, want: false},
		{name: "sibling directory", base: base, candidate: sibling, resolution: resolution, want: true},
		{name: "parent directory", base: base, candidate: root, resolution: resolution, want: true},
		{name: "sibling prefix is not nesting", base: base, candidate: base + "-other", resolution: resolution, want: true},
		{name: "empty base", base: "", candidate: sibling, resolution: resolution, want: false},
		{name: "empty candidate", base: base, candidate: "", resolution: resolution, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acpWorkDirIsIsolated(tc.base, tc.candidate, tc.resolution); got != tc.want {
				t.Fatalf("acpWorkDirIsIsolated(%q, %q) = %t, want %t", tc.base, tc.candidate, got, tc.want)
			}
		})
	}
}

func TestACPWorkDirIsIsolatedResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "workspace-link")
	if err := os.Symlink(base, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolution := &agent.WorktreeResolution{Directory: link, ID: "wt-link"}
	if acpWorkDirIsIsolated(base, link, resolution) {
		t.Fatal("a symlink to the base workspace was admitted as an isolated writer")
	}
	if acpWorkDirIsIsolated(base, filepath.Join(link, "nested"), resolution) {
		t.Fatal("a symlinked subdirectory of the base workspace was admitted as an isolated writer")
	}
}

func TestSelectACPPermissionOptionNeverChoosesAlways(t *testing.T) {
	options := []acpclient.PermissionOption{
		{OptionID: "allow-always", Kind: "allow_always"},
		{OptionID: "allow-once", Kind: "allow_once"},
		{OptionID: "reject-always", Kind: "reject_always"},
		{OptionID: "reject-once", Kind: "reject_once"},
	}
	if option, ok := selectACPPermissionOption(options, config.ACPPermissionAutoAllowOnce); !ok || option.OptionID != "allow-once" {
		t.Fatalf("auto allow selected %#v, ok=%v", option, ok)
	}
	if option, ok := selectACPPermissionOption(options, config.ACPPermissionAutoReject); !ok || option.OptionID != "reject-once" {
		t.Fatalf("auto reject selected %#v, ok=%v", option, ok)
	}
	if _, ok := selectACPPermissionOption([]acpclient.PermissionOption{{OptionID: "allow-always", Kind: "allow_always"}}, config.ACPPermissionAutoAllowOnce); ok {
		t.Fatal("auto allow accepted allow_always when no one-time option was offered")
	}
	if _, ok := selectACPPermissionOption(options, config.ACPPermissionInteractive); ok {
		t.Fatal("interactive strategy auto-selected an option")
	}
}

func TestSubagentTaskContractRenderPromptIsBoundedAndWorkerOnly(t *testing.T) {
	contract := validContract()
	prompt := contract.RenderPrompt("/work")
	for _, want := range []string{"Objective", "Add a focused regression test", "implement", "/work", "src/retry_test.go", "go test ./...", "worker rules", "untrusted"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
	}
	if len(prompt) > maxSubagentContractBytes+2048 {
		t.Fatalf("rendered prompt is not bounded: %d bytes", len(prompt))
	}
}

func TestACPRequestHandlerResolvesPermissionThroughDecisions(t *testing.T) {
	runtime := &SessionRuntime{ID: "decision-session"}
	runtime.SetDecisions(&DecisionService{})
	handler := runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: "run-parent", ChildID: "agent-child",
		Profile: config.ACPAgentProfile{ID: "helper"}, Strategy: config.ACPPermissionAutoAllowOnce,
	}, nil)
	message := acpclient.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage("7"),
		Method:  "session/request_permission",
		Params: json.RawMessage(`{
			"sessionId":"remote",
			"toolCall":{"toolCallId":"tool-1","title":"edit","kind":"edit","rawInput":{"path":"a.go"}},
			"options":[
				{"optionId":"allow-always","kind":"allow_always"},
				{"optionId":"allow-once","kind":"allow_once"}
			]
		}`),
	}
	result, rpcErr := handler(t.Context(), message)
	if rpcErr != nil {
		t.Fatalf("handler: %v", rpcErr)
	}
	selected, ok := result.(acpclient.PermissionResult)
	if !ok || selected.Outcome.Outcome != "selected" || selected.Outcome.OptionID != "allow-once" {
		t.Fatalf("permission result = %#v", result)
	}
	if pending := runtime.Decisions.Pending(); len(pending) != 0 {
		t.Fatalf("decision left pending: %#v", pending)
	}

	rejectHandler := runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: "run-parent", ChildID: "agent-child-2",
		Profile: config.ACPAgentProfile{ID: "helper"}, Strategy: config.ACPPermissionAutoReject,
	}, nil)
	rejectMessage := message
	rejectMessage.ID = json.RawMessage("8")
	rejectResult, _ := rejectHandler(t.Context(), rejectMessage)
	rejected, ok := rejectResult.(acpclient.PermissionResult)
	if !ok || rejected.Outcome.OptionID != "" || rejected.Outcome.Outcome != "cancelled" {
		t.Fatalf("reject without an offered reject_once = %#v, want cancelled", rejectResult)
	}

	unknown, rpcErr := handler(t.Context(), acpclient.Message{JSONRPC: "2.0", ID: json.RawMessage("9"), Method: "terminal/create"})
	if rpcErr == nil || unknown != nil || rpcErr.Code != -32601 {
		t.Fatalf("unknown reverse request = %#v, %#v", unknown, rpcErr)
	}
}
