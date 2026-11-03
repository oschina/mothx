package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	agentcore "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
)

// ACPChildBackend is the bridge between the shared AgentManager child graph and
// the ACP Worker Bridge. Agent Core only sees an extra agentpkg.Agent; it never
// imports ACP policy, profiles, or transport.
type ACPChildBackend struct {
	Runtime  *SessionRuntime
	Settings *config.Settings
}

// CreateChild reports handled=false when the request is a normal local child.
// A requested external target always reports handled=true, including errors, so
// Agent Core can never fall back to a local child for a Router delegation.
func (b ACPChildBackend) CreateChild(opts agentcore.AgentOptions) (agentpkg.Agent, bool, error) {
	target := strings.TrimSpace(opts.ExternalAgent)
	if target == "" {
		return nil, false, nil
	}
	if b.Runtime == nil || b.Settings == nil {
		return nil, true, fmt.Errorf("external ACP delegation requires an attached Runtime")
	}
	profile, binding, err := b.Runtime.ResolveRouterACPProfile(b.Settings)
	if err != nil {
		return nil, true, fmt.Errorf("resolve Router ACP worker: %w", err)
	}
	if profile.ID != target {
		return nil, true, fmt.Errorf("external target %q is not the activated Router worker %q", target, profile.ID)
	}
	if opts.Worktree != nil {
		return nil, true, fmt.Errorf("external ACP workers do not support per-child worktrees in this release")
	}
	// The external worker always runs in the Runtime-resolved session directory.
	// A model-supplied work_dir is intentionally ignored: it is not a workspace
	// authorization mechanism and would also move relative write roots.
	workDir := strings.TrimSpace(b.Runtime.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(opts.WorkDir)
	}
	if workDir == "" || !filepath.IsAbs(workDir) {
		return nil, true, fmt.Errorf("external ACP worker requires an absolute Runtime-resolved working directory")
	}
	contract, err := subagentContractFromOptions(profile, binding, opts)
	if err != nil {
		return nil, true, err
	}
	parentRunID := ""
	if b.Runtime.Execution != nil {
		if runID, ok := b.Runtime.Execution.Active(); ok {
			parentRunID = runID
		}
	}
	return &acpChildProxy{
		runtime:     b.Runtime,
		id:          opts.ID,
		parent:      opts.ParentID,
		mode:        opts.Mode,
		profile:     profile,
		contract:    contract,
		workDir:     workDir,
		parentRunID: parentRunID,
		launcher:    acpProcessLauncher{},
	}, true, nil
}

func subagentContractFromOptions(profile config.ACPAgentProfile, binding RouterBinding, opts agentcore.AgentOptions) (SubagentTaskContract, error) {
	role := SubagentRole(strings.TrimSpace(opts.ExternalRole))
	if role == "" {
		role = SubagentRoleInvestigate
	}
	deliverables := make([]SubagentDeliverable, 0, len(opts.ExternalDeliverable))
	for _, value := range opts.ExternalDeliverable {
		deliverables = append(deliverables, SubagentDeliverable(strings.TrimSpace(value)))
	}
	if len(deliverables) == 0 {
		deliverables = defaultSubagentDeliverables(role)
	}
	contract := SubagentTaskContract{
		Objective:            opts.ExternalTask,
		Role:                 role,
		ReadScope:            append([]string(nil), opts.ExternalReadScope...),
		WriteScope:           append([]string(nil), opts.ExternalWriteScope...),
		ExpectedDeliverables: deliverables,
		Verification:         opts.ExternalVerification,
		StopConditions:       append([]string(nil), opts.ExternalStopConditions...),
		TargetProfile:        binding.ACPProfileID,
		SystemPromptExtra:    opts.SystemPromptExtra,
	}
	if err := contract.Validate(profile); err != nil {
		return SubagentTaskContract{}, fmt.Errorf("Router task contract: %w", err)
	}
	return contract, nil
}

func defaultSubagentDeliverables(role SubagentRole) []SubagentDeliverable {
	switch role {
	case SubagentRoleImplement, SubagentRoleIntegrate:
		return []SubagentDeliverable{SubagentDeliverableChangedFiles, SubagentDeliverableTestEvidence}
	case SubagentRoleTest:
		return []SubagentDeliverable{SubagentDeliverableTestEvidence}
	case SubagentRoleReview:
		return []SubagentDeliverable{SubagentDeliverableReview}
	default:
		return []SubagentDeliverable{SubagentDeliverableFindings}
	}
}

// acpChildProxy adapts one external ACP worker turn to the existing
// agentpkg.Agent contract. It owns input/text projection and the single
// terminal event; it is not an Agent loop and creates no local session,
// provider, registry, Run, or decision store.
type acpChildProxy struct {
	runtime  *SessionRuntime
	id       agentpkg.AgentID
	parent   agentpkg.AgentID
	mode     string
	profile  config.ACPAgentProfile
	contract SubagentTaskContract
	workDir  string

	parentRunID string
	launcher    acpclient.Launcher

	mu       sync.Mutex
	messages []agentpkg.Message
	context  *agentpkg.AgentContext
	cancel   context.CancelFunc
	running  bool

	truncated  bool
	stopReason string
}

func (p *acpChildProxy) ID() agentpkg.AgentID       { return p.id }
func (p *acpChildProxy) ParentID() agentpkg.AgentID { return p.parent }

// RuntimeConfig exposes only the neutral bookkeeping mode to AgentManager. It
// never carries provider, tools, or Router policy.
func (p *acpChildProxy) RuntimeConfig() (agentcore.AgentLoopConfig, bool) {
	return agentcore.AgentLoopConfig{Mode: p.mode}, true
}

func (p *acpChildProxy) Run(ctx context.Context, userMsg string) <-chan agentpkg.Event {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		cancel()
		ch := make(chan agentpkg.Event, 1)
		ch <- agentpkg.Event{Type: agentpkg.EventRunFinished, AgentID: p.id, Status: agentpkg.TaskError, Error: fmt.Errorf("external worker is already running")}
		close(ch)
		return ch
	}
	p.running = true
	p.cancel = cancel
	p.messages = append(p.messages, agentpkg.NewUserMessage(p.contract.Objective))
	p.mu.Unlock()

	events := make(chan agentpkg.Event, 64)
	go func() {
		defer close(events)
		defer cancel()
		defer func() {
			p.mu.Lock()
			p.running = false
			p.cancel = nil
			p.mu.Unlock()
		}()
		emit := func(event agentpkg.Event) {
			event.AgentID = p.id
			events <- event
		}

		release, err := p.runtime.acquireACPWorkerPermit(p.profile.EffectiveEnforcedChildren())
		if err != nil {
			p.finishRun(emit, agentpkg.TaskError, err, "")
			return
		}
		defer release()

		handler := p.runtime.newACPRequestHandler(p.parentRunID, string(p.id), p.profile.ID, p.profile.EffectivePermissionPolicy(), emit)
		result, runErr := (ACPWorkerBridge{}).Run(runCtx, ACPWorkerInvocation{
			Profile: p.profile, Contract: p.contract, WorkDir: p.workDir,
			Launcher: p.launcher, HandleRequest: handler, OnEvent: emit,
		})
		// Preserve the safe partial transcript on error or cancellation. The
		// worker may already have produced side effects or useful text; replaying
		// the prompt is forbidden, but discarding what arrived is not required.
		output := result.Output
		if result.Truncated {
			output += "\n\n[worker output truncated]"
		}
		p.appendAssistant(output)
		p.mu.Lock()
		p.truncated = result.Truncated
		p.stopReason = result.StopReason
		p.mu.Unlock()
		if runErr != nil {
			if runCtx.Err() != nil || result.StopReason == "cancelled" {
				p.finishRun(emit, agentpkg.TaskCanceled, runErr, result.StopReason)
				return
			}
			p.finishRun(emit, agentpkg.TaskError, runErr, result.StopReason)
			return
		}
		status := agentpkg.TaskSuccess
		if result.StopReason == "cancelled" {
			status = agentpkg.TaskCanceled
		} else if result.StopReason != "" && result.StopReason != "end_turn" {
			status = agentpkg.TaskIncomplete
		}
		p.finishRun(emit, status, nil, result.StopReason)
	}()
	return events
}

func (p *acpChildProxy) RunWithMessages(ctx context.Context, messages []agentpkg.Message) <-chan agentpkg.Event {
	return p.Run(ctx, "")
}

func (p *acpChildProxy) Abort() {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *acpChildProxy) finishRun(emit func(agentpkg.Event), status agentpkg.TaskStatus, err error, stopReason string) {
	terminal := agentpkg.Event{Type: agentpkg.EventRunFinished, Status: status, StopReason: stopReason, Error: err}
	emit(terminal)
	switch status {
	case agentpkg.TaskError:
		emit(agentpkg.Event{Type: agentpkg.EventError, Error: err, StopReason: stopReason})
	case agentpkg.TaskCanceled:
		emit(agentpkg.Event{Type: agentpkg.EventDone, Done: true, StopReason: stopReason, Error: err})
	default:
		emit(agentpkg.Event{Type: agentpkg.EventDone, Done: true, StopReason: stopReason})
	}
}

func (p *acpChildProxy) appendAssistant(text string) {
	if text == "" {
		return
	}
	p.mu.Lock()
	p.messages = append(p.messages, agentpkg.NewAssistantMessage([]agentpkg.ContentBlock{{Type: "text", Text: text}}))
	p.mu.Unlock()
}

func (p *acpChildProxy) GetMessages() []agentpkg.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agentpkg.Message(nil), p.messages...)
}

func (p *acpChildProxy) SetMessages(messages []agentpkg.Message) {
	p.mu.Lock()
	p.messages = append([]agentpkg.Message(nil), messages...)
	p.mu.Unlock()
}

func (p *acpChildProxy) GetContext() *agentpkg.AgentContext {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.context == nil {
		return &agentpkg.AgentContext{Messages: append([]agentpkg.Message(nil), p.messages...)}
	}
	clone := *p.context
	clone.Messages = append([]agentpkg.Message(nil), p.context.Messages...)
	clone.Tools = append([]agentpkg.ToolDefinition(nil), p.context.Tools...)
	return &clone
}

func (p *acpChildProxy) SetContext(agentContext *agentpkg.AgentContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if agentContext == nil {
		p.context = nil
		return
	}
	clone := *agentContext
	clone.Messages = append([]agentpkg.Message(nil), agentContext.Messages...)
	clone.Tools = append([]agentpkg.ToolDefinition(nil), agentContext.Tools...)
	p.context = &clone
}

func (p *acpChildProxy) GetContextUsage() *agentpkg.ContextUsage { return nil }

func (p *acpChildProxy) LoadHistoryMessages(messages []agentpkg.Message) {
	if len(messages) == 0 {
		return
	}
	p.mu.Lock()
	p.messages = append(append([]agentpkg.Message(nil), messages...), p.messages...)
	p.mu.Unlock()
}

// HandleApprovalResponse is intentionally a no-op: Router's external approvals
// are resolved by the non-blocking DecisionService strategy, never by a UI
// callback racing the remote Agent.
func (p *acpChildProxy) HandleApprovalResponse(approvalID string, approved bool) {}

// ChildMetadata is bounded display metadata for the manager/graph projection.
func (p *acpChildProxy) ChildMetadata() map[string]any {
	deliverables := make([]string, 0, len(p.contract.ExpectedDeliverables))
	for _, deliverable := range p.contract.ExpectedDeliverables {
		deliverables = append(deliverables, string(deliverable))
	}
	return map[string]any{
		"external": true, "backend": "acp", "profileId": p.profile.ID,
		"role": string(p.contract.Role), "deliverables": deliverables,
		"evidence": "worker-reported",
	}
}

// CompletionMetadata is the bounded envelope merged into the blocking delegate
// result. It labels the worker's claims as untrusted evidence.
func (p *acpChildProxy) CompletionMetadata() map[string]any {
	p.mu.Lock()
	truncated, stopReason := p.truncated, p.stopReason
	p.mu.Unlock()
	metadata := p.ChildMetadata()
	metadata["outputTruncated"] = truncated
	metadata["stopReason"] = stopReason
	return metadata
}
