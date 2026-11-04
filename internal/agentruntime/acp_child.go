package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	agentcore "github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	sessionpkg "github.com/oschina/mothx/internal/session"
	worktreepkg "github.com/oschina/mothx/internal/worktree"
)

const maxACPFollowUpMessageBytes = 32 << 10

const acpWorkerTruncatedMarker = "\n\n[worker output truncated]"

// ACPChildBackend is the bridge between the shared AgentManager child graph and
// the ACP Worker Bridge. Agent Core only sees an extra agentpkg.Agent; it never
// imports ACP policy, profiles, or transport.
type ACPChildBackend struct {
	Runtime  *SessionRuntime
	Settings *config.Settings
	// Worktrees is the Runtime-owned worktree manager used for child isolation
	// and immutable change-set capture. It may be nil for headless tests.
	Worktrees *WorktreeManager
}

// ChildWorktree requests a managed worktree for an implementation worker before
// the AgentManager resolves it. Integration writes the base workspace directly,
// and read-only/test/review workers keep sharing the stable session workspace.
func (b ACPChildBackend) ChildWorktree(opts agentcore.AgentOptions) *agentcore.WorktreeSpec {
	if SubagentRole(strings.TrimSpace(opts.ExternalRole)) == SubagentRoleImplement {
		return &agentcore.WorktreeSpec{}
	}
	return nil
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
	profile, binding, err := b.Runtime.ResolveRouterACPProfileByID(b.Settings, target)
	if err != nil {
		return nil, true, fmt.Errorf("resolve Router ACP worker: %w", err)
	}
	// A model-supplied work_dir is intentionally ignored. Only a worktree
	// resolved by AgentManager from a Runtime-owned provider is trusted enough
	// to move the external worker's workspace.
	workDir := strings.TrimSpace(b.Runtime.WorkDir)
	resolution := opts.WorktreeResolved
	if resolution != nil && strings.TrimSpace(resolution.Directory) != "" {
		workDir = strings.TrimSpace(resolution.Directory)
	} else if opts.Worktree != nil {
		// Older providers may resolve the directory without metadata. The
		// manager has already replaced WorkDir, and the explicit worktree bit is
		// the trust marker that distinguishes it from a model-supplied path.
		workDir = strings.TrimSpace(opts.WorkDir)
	}
	if workDir == "" || !filepath.IsAbs(workDir) {
		return nil, true, fmt.Errorf("external ACP worker requires an absolute Runtime-resolved working directory")
	}
	if resolution == nil && opts.Worktree != nil {
		resolution = &agentcore.WorktreeResolution{Directory: workDir}
	}
	if resolution != nil {
		if resolution.ID == "" && b.Worktrees != nil {
			sessionDir := ""
			if b.Runtime.Manager != nil {
				sessionDir = b.Runtime.Manager.GetSessionDir()
			}
			if sessionDir != "" {
				if wt, lookupErr := sessionpkg.GetWorktreeByDirectory(sessionDir, workDir); lookupErr == nil && wt.ID != "" {
					resolution.ID = wt.ID
					resolution.RepositoryRoot = wt.RepositoryRoot
				}
			}
		}
		if resolution.BaseRevision == "" && b.Worktrees != nil {
			ctx, cancel := context.WithTimeout(ctxOrBackground(opts.RuntimeContext), 10*time.Minute)
			revision, revisionErr := b.Worktrees.Revision(ctx, workDir)
			cancel()
			if revisionErr == nil {
				resolution.BaseRevision = revision
			}
		}
	}
	workspace, err := b.Runtime.acpWorkspaceFor(workDir)
	if err != nil {
		return nil, true, err
	}
	attachments, err := b.resolveSubagentAttachments(ctxOrBackground(opts.RuntimeContext), opts.ExternalAttachmentIDs)
	if err != nil {
		return nil, true, err
	}
	contract, err := subagentContractFromOptions(profile, binding, opts, attachments)
	if err != nil {
		return nil, true, err
	}
	// Reserve the live-worker slot at construction, not when the asynchronous
	// Run goroutine happens to start. subagent_spawn must return a synchronous
	// error for a second worker instead of a handle whose status later fails.
	isolatedWrite := contract.Role == SubagentRoleImplement &&
		acpWorkDirIsIsolated(b.Runtime.WorkDir, workDir, resolution)
	release, err := b.Runtime.acquireACPWorkerPermitForWorkspace(
		profile.EffectiveEnforcedChildren(), contract.Role, isolatedWrite,
		profile.EffectiveAllowParallelBaseWorkspaceWrites(),
		contract.WriteScope,
	)
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
		runtime:       b.Runtime,
		id:            opts.ID,
		parent:        opts.ParentID,
		mode:          opts.Mode,
		profile:       profile,
		contract:      contract,
		workDir:       workDir,
		workspace:     workspace,
		worktrees:     b.Worktrees,
		worktreeID:    resolutionID(resolution),
		baseRevision:  resolutionBaseRevision(resolution),
		parentRunID:   parentRunID,
		launcher:      acpProcessLauncher{},
		permitRelease: release,
		createdAt:     time.Now().UTC(),
	}, true, nil
}

func subagentContractFromOptions(profile config.ACPAgentProfile, binding RouterBinding, opts agentcore.AgentOptions, attachments []SubagentAttachment) (SubagentTaskContract, error) {
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
		TargetProfile:        profile.ID,
		SystemPromptExtra:    opts.SystemPromptExtra,
		Attachments:          attachments,
	}
	if err := contract.Validate(profile); err != nil {
		return SubagentTaskContract{}, fmt.Errorf("Router task contract: %w", err)
	}
	return contract, nil
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// resolveSubagentAttachments uses the Runtime input owner; Agent Core and the
// model only ever pass opaque resource IDs.
func (b ACPChildBackend) resolveSubagentAttachments(ctx context.Context, ids []string) ([]SubagentAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if b.Runtime == nil {
		return nil, fmt.Errorf("external ACP attachments require an attached Runtime")
	}
	b.Runtime.mu.RLock()
	inputs := b.Runtime.Inputs
	sessionID := b.Runtime.ID
	b.Runtime.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return nil, fmt.Errorf("Runtime input resources are not bound to a session")
	}
	attachments := make([]SubagentAttachment, 0, len(ids))
	for _, id := range ids {
		record, data, err := inputs.Read(ctx, sessionID, strings.TrimSpace(id))
		if err != nil {
			return nil, fmt.Errorf("resolve external worker attachment %q: %w", id, err)
		}
		attachments = append(attachments, SubagentAttachment{
			ID: record.ID, Filename: record.Filename, MediaType: record.MediaType,
			Kind: record.Kind, RelativePath: record.RelativePath, Bytes: record.Bytes, Data: data,
		})
	}
	return attachments, nil
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

func resolutionID(resolution *agentcore.WorktreeResolution) string {
	if resolution == nil {
		return ""
	}
	return resolution.ID
}

func resolutionBaseRevision(resolution *agentcore.WorktreeResolution) string {
	if resolution == nil {
		return ""
	}
	return resolution.BaseRevision
}

// acpWorkDirIsIsolated reports whether an implementation worker's directory is
// genuinely outside the session work directory. A trusted worktree resolution
// is necessary but not sufficient: the same directory (or a subdirectory) must
// not be admitted as an isolated writer, or it could race readers on the base
// workspace.
func acpWorkDirIsIsolated(baseWorkDir, workDir string, resolution *agentcore.WorktreeResolution) bool {
	if resolution == nil {
		return false
	}
	base := strings.TrimSpace(baseWorkDir)
	candidate := strings.TrimSpace(workDir)
	if base == "" || candidate == "" {
		return false
	}
	base = filepath.Clean(base)
	candidate = filepath.Clean(candidate)
	// Compare physical locations as well as lexical paths: a managed worktree
	// reached through a symlink must not be mistaken for an isolated writer.
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	if candidate == base {
		return false
	}
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		// Different volumes cannot be nested under the base workspace.
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// acpChildProxy adapts one external ACP worker turn to the existing
// agentpkg.Agent contract. It owns input/text projection and the single
// terminal event; it is not an Agent loop and creates no local session,
// provider, registry, Run, or decision store.
type acpChildProxy struct {
	runtime      *SessionRuntime
	id           agentpkg.AgentID
	parent       agentpkg.AgentID
	mode         string
	profile      config.ACPAgentProfile
	contract     SubagentTaskContract
	workDir      string
	workspace    *acpWorkerWorkspace
	worktrees    *WorktreeManager
	worktreeID   string
	baseRevision string

	parentRunID string
	launcher    acpclient.Launcher

	mu       sync.Mutex
	messages []agentpkg.Message
	context  *agentpkg.AgentContext
	cancel   context.CancelFunc
	remote   *acpRemoteWorker
	// remoteSessionID survives a dead transport so a later turn can use
	// session/load instead of silently starting a fresh remote conversation.
	remoteSessionID string
	running         bool
	started         bool
	aborted         bool
	createdAt       time.Time

	permitRelease func()

	truncated          bool
	stopReason         string
	droppedUpdates     uint64
	changeSetJSON      string
	changeSetDigest    string
	changeSetError     string
	changeSetFiles     int
	changeSetTruncated bool
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
	userMsg = strings.TrimSpace(userMsg)
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		cancel()
		return p.immediateError(fmt.Errorf("external worker is already running"))
	}
	if p.aborted {
		p.mu.Unlock()
		cancel()
		return p.immediateError(fmt.Errorf("external worker was canceled before it started"))
	}
	// A session only exists once Open has returned a remote session ID. If the
	// very first attempt fails before that point (launch error, initialize
	// error, or session/new error), a later subagent_send retry must still send
	// the full task contract. p.started alone would incorrectly turn that retry
	// into a follow-up with no objective.
	firstTurn := p.remote == nil && p.remoteSessionID == ""
	retryWithoutSession := firstTurn && p.started
	if !firstTurn {
		if userMsg == "" {
			p.mu.Unlock()
			cancel()
			return p.immediateError(fmt.Errorf("a follow-up message is required"))
		}
		if len(userMsg) > maxACPFollowUpMessageBytes {
			p.mu.Unlock()
			cancel()
			return p.immediateError(fmt.Errorf("follow-up message exceeds %d bytes", maxACPFollowUpMessageBytes))
		}
		p.messages = append(p.messages, agentpkg.NewUserMessage(userMsg))
	} else {
		if !retryWithoutSession {
			p.messages = append(p.messages, agentpkg.NewUserMessage(p.contract.Objective))
		} else if userMsg != "" {
			p.messages = append(p.messages, agentpkg.NewUserMessage(userMsg))
		}
	}
	p.started = true
	p.running = true
	p.cancel = cancel
	p.mu.Unlock()

	events := make(chan agentpkg.Event, 64)
	go func() {
		defer close(events)
		defer cancel()
		defer func() {
			p.mu.Lock()
			p.running = false
			p.cancel = nil
			aborted := p.aborted
			p.mu.Unlock()
			if aborted {
				p.closeRemote()
				p.releasePermit()
			}
		}()
		emit := func(event agentpkg.Event) {
			event.AgentID = p.id
			events <- event
		}
		remote, cleanup, remoteErr := p.ensureRemote(runCtx, emit)
		if remoteErr != nil {
			p.finishRun(emit, agentpkg.TaskError, remoteErr, "")
			return
		}
		defer cleanup()
		prompt := p.promptContent(remote, firstTurn, retryWithoutSession, userMsg)
		result, runErr := remote.Prompt(runCtx, prompt, emit)
		// Preserve the safe partial transcript on error or cancellation. The
		// worker may already have produced side effects or useful text; replaying
		// the prompt is forbidden, but discarding what arrived is not required.
		output := result.Output
		if result.Truncated {
			keep := maxSubagentResultBytes - len(acpWorkerTruncatedMarker)
			output, _ = truncateACPText(output, keep)
			output += acpWorkerTruncatedMarker
		}
		p.appendAssistant(output)
		p.mu.Lock()
		p.truncated = result.Truncated
		p.stopReason = result.StopReason
		p.droppedUpdates = result.DroppedUpdates
		if remote.Dead() {
			p.remote = nil
		}
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
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != agentpkg.RoleUser {
			continue
		}
		var text strings.Builder
		if messages[i].Content != "" {
			text.WriteString(messages[i].Content)
		}
		for _, content := range messages[i].Contents {
			if content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
		if trimmed := strings.TrimSpace(text.String()); trimmed != "" {
			return p.Run(ctx, trimmed)
		}
	}
	return p.Run(ctx, "")
}

func (p *acpChildProxy) Abort() {
	p.mu.Lock()
	p.aborted = true
	cancel := p.cancel
	running := p.running
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !running {
		p.closeRemote()
		p.releasePermit()
	}
}

func (p *acpChildProxy) releasePermit() {
	p.mu.Lock()
	release := p.permitRelease
	p.permitRelease = nil
	p.mu.Unlock()
	if release != nil {
		release()
	}
}

// TerminateExternalChild is the delegate-path cleanup hook: a blocking
// delegate has no follow-up handle, so it closes the remote session and frees
// the worker permit immediately after its one turn.
func (p *acpChildProxy) TerminateExternalChild() {
	p.mu.Lock()
	p.aborted = true
	cancel := p.cancel
	running := p.running
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !running {
		p.closeRemote()
		p.releasePermit()
	}
}

// LongRunningFollowUp tells the shared sub-agent tool that a follow-up turn is
// not subject to the local per-agent wall-clock budget.
func (p *acpChildProxy) LongRunningFollowUp() bool { return true }

func (p *acpChildProxy) ensureRemote(ctx context.Context, emit func(agentpkg.Event)) (*acpRemoteWorker, func(), error) {
	handler, cleanup, terminalAccess := p.newTurnHandler(emit)
	p.mu.Lock()
	remote := p.remote
	resumeSessionID := p.remoteSessionID
	p.mu.Unlock()

	if remote != nil && !remote.Dead() {
		remote.SetRequestHandler(handler)
		return remote, cleanup, nil
	}
	if remote != nil {
		_ = remote.Close(context.Background())
		p.mu.Lock()
		if p.remote == remote {
			p.remote = nil
		}
		p.mu.Unlock()
	}

	pool, poolErr := p.runtime.acpConnectionPool()
	if poolErr != nil {
		cleanup()
		return nil, nil, poolErr
	}
	worker, err := (ACPWorkerBridge{}).Open(ctx, ACPWorkerInvocation{
		Profile:               p.profile,
		Contract:              p.contract,
		WorkDir:               p.workDir,
		AdditionalDirectories: p.workspace.additionalDirectories(p.runtime.AdditionalDirectoriesSnapshot()),
		ResumeSessionID:       resumeSessionID,
		Launcher:              p.launcher,
		HandleRequest:         handler,
		ReadTextFile:          p.runtime.acpReadTextFileAvailableIn(p.workspace),
		WriteTextFile:         p.runtime.acpWriteTextFileAvailableIn(p.workspace, p.profile, p.contract),
		TerminalAccess:        terminalAccess,
		pool:                  pool,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	p.mu.Lock()
	p.remote = worker
	p.remoteSessionID = worker.SessionID()
	p.mu.Unlock()
	return worker, cleanup, nil
}

func (p *acpChildProxy) newTurnHandler(emit func(agentpkg.Event)) (acpclient.RequestHandler, func(), bool) {
	baseHandler := p.runtime.newACPRequestHandler(acpWorkerRequestScope{
		ParentRunID: p.parentRunID,
		ChildID:     string(p.id),
		Profile:     p.profile,
		Contract:    p.contract,
		WorkDir:     p.workDir,
		Strategy:    p.profile.EffectivePermissionPolicy(),
		Workspace:   p.workspace,
	}, emit)
	if !p.profile.TerminalAccess {
		return baseHandler, func() {}, false
	}
	env, err := acpWorkerEnvironment(p.profile)
	if err != nil {
		return baseHandler, func() {}, false
	}
	terminals := newACPTerminalManager(p.workspace.registryOr(p.runtime.Registry), p.workDir, env)
	if terminals == nil {
		return baseHandler, func() {}, false
	}
	handler := func(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
		if isACPTerminalMethod(message.Method) {
			return terminals.Handle(ctx, message)
		}
		return baseHandler(ctx, message)
	}
	return handler, terminals.Close, true
}

func (p *acpChildProxy) promptContent(remote *acpRemoteWorker, firstTurn, retryWithoutSession bool, userMsg string) []acpclient.ContentBlock {
	if firstTurn {
		content := acpPromptContent(remote.Capabilities(), p.contract, p.workDir)
		if instructions := strings.TrimSpace(userMsg); retryWithoutSession && instructions != "" {
			content = append(content, acpclient.ContentBlock{
				Type: "text",
				Text: "## Retry instructions\n\n" + instructions,
			})
		}
		return content
	}
	return []acpclient.ContentBlock{{Type: "text", Text: userMsg}}
}

func (p *acpChildProxy) closeRemote() {
	p.mu.Lock()
	remote := p.remote
	p.remote = nil
	p.mu.Unlock()
	if remote != nil {
		_ = remote.Close(context.Background())
	}
}

func (p *acpChildProxy) immediateError(err error) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event, 2)
	ch <- agentpkg.Event{Type: agentpkg.EventRunFinished, AgentID: p.id, Status: agentpkg.TaskError, Error: err}
	ch <- agentpkg.Event{Type: agentpkg.EventError, AgentID: p.id, Error: err}
	close(ch)
	return ch
}

func (p *acpChildProxy) finishRun(emit func(agentpkg.Event), status agentpkg.TaskStatus, err error, stopReason string) {
	p.captureChangeSet()
	p.persistChildRun(status, err)
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

// captureChangeSet snapshots a managed worktree before the durable child row is
// written. The snapshot is auxiliary evidence, so a capture failure is
// recorded rather than upgrading a completed worker into an error.
func (p *acpChildProxy) captureChangeSet() {
	if p == nil || p.worktrees == nil || strings.TrimSpace(p.worktreeID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changeSet, err := p.worktrees.CaptureChangeSet(ctx, p.worktreeID, p.baseRevision, worktreepkg.DefaultChangeSetPatchBytes)
	if err != nil {
		p.mu.Lock()
		p.changeSetJSON = ""
		p.changeSetDigest = ""
		p.changeSetError = err.Error()
		p.changeSetFiles = 0
		p.changeSetTruncated = false
		p.mu.Unlock()
		return
	}
	encoded, err := json.Marshal(changeSet)
	if err != nil {
		p.mu.Lock()
		p.changeSetError = fmt.Sprintf("encode change set: %v", err)
		p.mu.Unlock()
		return
	}
	p.mu.Lock()
	p.changeSetJSON = string(encoded)
	p.changeSetDigest = changeSet.Digest
	p.changeSetError = ""
	p.changeSetFiles = len(changeSet.Files) + len(changeSet.UntrackedFiles)
	p.changeSetTruncated = changeSet.PatchTruncated || changeSet.UntrackedTruncated
	p.mu.Unlock()
}

// persistChildRun records the child transcript, remote session identity, and
// terminal status in the session database. It is deliberately best-effort:
// losing a history row must not turn an otherwise completed child into an
// execution failure, but the event log names the persistence error.
func (p *acpChildProxy) persistChildRun(status agentpkg.TaskStatus, runErr error) {
	if p == nil || p.runtime == nil {
		return
	}
	p.runtime.mu.RLock()
	manager := p.runtime.Manager
	sessionID := p.runtime.ID
	p.runtime.mu.RUnlock()
	if manager == nil {
		return
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = p.runtime.sessionIDForDecisions()
	}
	sessionDir := manager.GetSessionDir()
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(sessionDir) == "" {
		return
	}
	p.mu.Lock()
	transcript := append([]agentpkg.Message(nil), p.messages...)
	remoteSessionID := p.remoteSessionID
	worktreeID := p.worktreeID
	baseRevision := p.baseRevision
	changeSetJSON := p.changeSetJSON
	changeSetError := p.changeSetError
	p.mu.Unlock()
	transcriptJSON, err := json.Marshal(transcript)
	if err != nil {
		log.Printf("[agentruntime] persist child %s transcript: %v", p.id, err)
		return
	}
	contractJSON, err := json.Marshal(p.contract)
	if err != nil {
		log.Printf("[agentruntime] persist child %s contract: %v", p.id, err)
		return
	}
	statusText := string(status)
	if runErr != nil && statusText == "" {
		statusText = "error"
	}
	if err := sessionpkg.SaveChildRun(sessionDir, sessionpkg.ChildRun{
		ChildID: string(p.id), SessionID: sessionID, ParentRunID: p.parentRunID,
		Backend: "acp", ProfileID: p.profile.ID, RemoteSessionID: remoteSessionID,
		WorkDir: p.workDir, WorktreeID: worktreeID, BaseRevision: baseRevision,
		ChangeSetJSON: changeSetJSON, ChangeSetError: changeSetError, Status: statusText,
		ContractJSON: string(contractJSON), TranscriptJSON: string(transcriptJSON),
		CreatedAt: p.createdAt, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("[agentruntime] persist child %s run: %v", p.id, err)
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
	metadata := map[string]any{
		"external": true, "backend": "acp", "profileId": p.profile.ID,
		"role": string(p.contract.Role), "deliverables": deliverables,
		"evidence": "worker-reported",
	}
	p.mu.Lock()
	worktreeID, changeSetDigest, changeSetError := p.worktreeID, p.changeSetDigest, p.changeSetError
	changeSetFiles, changeSetTruncated := p.changeSetFiles, p.changeSetTruncated
	p.mu.Unlock()
	if worktreeID != "" {
		metadata["worktreeId"] = worktreeID
	}
	if changeSetDigest != "" {
		metadata["changeSetDigest"] = changeSetDigest
		metadata["changeSetFiles"] = changeSetFiles
		metadata["changeSetTruncated"] = changeSetTruncated
	}
	if changeSetError != "" {
		metadata["changeSetError"] = changeSetError
	}
	return metadata
}

// CompletionMetadata is the bounded envelope merged into the blocking delegate
// result. It labels the worker's claims as untrusted evidence.
func (p *acpChildProxy) CompletionMetadata() map[string]any {
	p.mu.Lock()
	truncated, stopReason := p.truncated, p.stopReason
	droppedUpdates := p.droppedUpdates
	p.mu.Unlock()
	metadata := p.ChildMetadata()
	metadata["outputTruncated"] = truncated
	metadata["stopReason"] = stopReason
	if droppedUpdates > 0 {
		metadata["droppedUpdates"] = droppedUpdates
	}
	return metadata
}
