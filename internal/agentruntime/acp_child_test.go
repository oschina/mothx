package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
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
	handler := runtime.newACPRequestHandler("run-parent", "agent-child", "helper", config.ACPPermissionAutoAllowOnce, nil)
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

	rejectHandler := runtime.newACPRequestHandler("run-parent", "agent-child-2", "helper", config.ACPPermissionAutoReject, nil)
	rejectMessage := message
	rejectMessage.ID = json.RawMessage("8")
	rejectResult, _ := rejectHandler(t.Context(), rejectMessage)
	rejected, ok := rejectResult.(acpclient.PermissionResult)
	if !ok || rejected.Outcome.OptionID != "" || rejected.Outcome.Outcome != "cancelled" {
		t.Fatalf("reject without an offered reject_once = %#v, want cancelled", rejectResult)
	}

	unknown, rpcErr := handler(t.Context(), acpclient.Message{JSONRPC: "2.0", ID: json.RawMessage("9"), Method: "fs/read_text_file"})
	if rpcErr == nil || unknown != nil || rpcErr.Code != -32601 {
		t.Fatalf("unknown reverse request = %#v, %#v", unknown, rpcErr)
	}
}
