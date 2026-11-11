package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

// ManagedAgentStatus captures scheduling state for an agent managed by AgentManager.
type ManagedAgentStatus struct {
	ID                agentpkg.AgentID
	ParentID          agentpkg.AgentID
	MemberID          string
	ExpertID          string
	MemberDisplayName string
	MemberEmoji       string
	MemberRole        string
	State             string
	Result            string
	Error             string
	// Metadata is an opaque, bounded child-backend projection (for example the
	// Router profile and declared evidence class). It never carries remote
	// message bodies or configuration secrets.
	Metadata  map[string]any
	StartedAt time.Time
	UpdatedAt time.Time
}

// AgentStatusListener observes lifecycle transitions of managed agents. It is
// invoked after the manager lock is released, so listeners may call back into
// the manager (Parent, Status, ...) safely.
type AgentStatusListener func(ManagedAgentStatus)

// AgentManager manages the lifecycle of all agent instances.
type AgentManager struct {
	mu        sync.RWMutex
	agents    map[agentpkg.AgentID]agentpkg.Agent
	parentOf  map[agentpkg.AgentID]agentpkg.AgentID
	children  map[agentpkg.AgentID][]agentpkg.AgentID
	statuses  map[agentpkg.AgentID]ManagedAgentStatus
	cancels   map[agentpkg.AgentID]context.CancelFunc
	listeners []AgentStatusListener
	factory   *AgentFactory
	counter   int64

	// Expert-team member context, installed once by the runtime assembly
	// layer via SetMemberContext before any run starts and treated as
	// read-only afterwards. All three are nil/empty when no expert team is
	// bound to the session.
	Members  *MemberDefRegistry
	Mailbox  *MemberMailbox
	ExpertID string

	// worktreeProvider materializes an isolated worktree for a child agent on
	// demand, and worktreePerChild makes every sub-agent get one. The provider
	// is installed by the runtime assembly layer; internal/agent never runs git.
	worktreeProvider WorktreeProvider
	worktreePerChild bool

	// childBackend is an optional Runtime-installed child constructor. Agent
	// Core never imports the external protocol; it only knows whether the
	// backend handled this validated request.
	childBackend           ChildBackend
	childRunRecorder       ChildRunRecorder
	changeSetIntegrator    ChangeSetIntegrator
	workerQuestionResolver WorkerQuestionResolver
	sessionID              string
}

// ChildBackend constructs a child after AgentManager has validated the parent,
// mode, and sub-agent policy. It is installed by the Runtime during assembly.
//
// handled=false asks the manager to keep the existing local AgentFactory path.
// A backend must return a fully functional agentpkg.Agent and must not bypass
// the manager's status, cancellation, event-forwarding, or terminal contracts.
type ChildBackend interface {
	CreateChild(opts AgentOptions) (agentpkg.Agent, bool, error)
}

// ChildWorktreePlanner lets a Runtime-installed backend request an isolated
// worktree before construction. Agent Core resolves it through the Runtime-owned
// WorktreeProvider; the backend never runs git itself.
type ChildWorktreePlanner interface {
	ChildWorktree(opts AgentOptions) *WorktreeSpec
}

// ChildBackendRuntimeConfig is an optional extension for an external child
// backend whose child constructor captures a configuration snapshot at
// assembly time. AgentManager refreshes that snapshot when the adapter updates
// runtime config, so the next child observes edited settings (for example an
// ACP worker profile's default model) without discarding live child bookkeeping.
type ChildBackendRuntimeConfig interface {
	UpdateChildBackendRuntimeConfig(settings *config.Settings)
}

// ChildRunObservation is the lifecycle fact a child backend reports for
// durable history. The Runtime owns where it is stored; Agent Core only
// supplies canonical identity, status, and the child transcript.
type ChildRunObservation struct {
	ChildID         string
	SessionID       string
	ParentRunID     string
	Backend         string
	ProfileID       string
	RemoteSessionID string
	WorkDir         string
	WorktreeID      string
	BaseRevision    string
	ChangeSetJSON   string
	ChangeSetError  string
	Status          string
	ContractJSON    string
	TranscriptJSON  string
	// ResolvedCapabilitiesJSON is the typed capability projection negotiated at
	// initialize. It is diagnostic child metadata, never prompt content.
	ResolvedCapabilitiesJSON   string
	CapabilityDegradationsJSON string
	// MCPBridge is the compact bridge state: available, unavailable, disabled,
	// or unreachable.
	MCPBridge string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ChildRunRecorder persists child history through the Runtime-owned session
// boundary. It is optional so standalone Agent Core tests do not require a DB.
type ChildRunRecorder interface {
	RecordChildRun(observation ChildRunObservation) error
}

// ChangeSetSelection identifies one immutable child change-set snapshot the
// Router lead wants the Runtime integration owner to apply.
type ChangeSetSelection struct {
	Handle string `json:"handle"`
	Digest string `json:"digest"`
}

// ChangeSetIntegrationChild is the per-child integration outcome.
type ChangeSetIntegrationChild struct {
	Handle        string   `json:"handle"`
	Digest        string   `json:"digest"`
	Status        string   `json:"status"`
	Error         string   `json:"error,omitempty"`
	EvidenceError string   `json:"evidenceError,omitempty"`
	AppliedFiles  []string `json:"appliedFiles,omitempty"`
}

// ChangeSetIntegrationResult is the Runtime-owned integration evidence projected
// to the lead. It never treats a worker's prose as proof of a workspace change.
type ChangeSetIntegrationResult struct {
	IntegrationID string                      `json:"integrationId,omitempty"`
	Status        string                      `json:"status"`
	Children      []ChangeSetIntegrationChild `json:"children,omitempty"`
	AppliedFiles  []string                    `json:"appliedFiles,omitempty"`
	Error         string                      `json:"error,omitempty"`
	EvidenceError string                      `json:"evidenceError,omitempty"`
}

// WorkerQuestionResolver resolves a Router worker's blocking question through
// the Runtime-owned decision service. Agent Core only forwards the opaque
// question ID and the lead's answer; it never owns the durable decision record.
// It is installed by Runtime during assembly, at the same layer as
// ChangeSetIntegrator, so internal/agent never imports agentruntime.
type WorkerQuestionResolver interface {
	ResolveWorkerQuestion(ctx context.Context, questionID, answer string) error
}

// SetWorkerQuestionResolver installs the Runtime-owned worker-question
// resolution service. It must be set during assembly, before Router tools are
// exposed.
func (m *AgentManager) SetWorkerQuestionResolver(resolver WorkerQuestionResolver) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workerQuestionResolver = resolver
	m.mu.Unlock()
}

// ResolveWorkerQuestion delegates to the Runtime-owned decision service. A nil
// resolver fails closed: reporting success would let the lead believe a blocked
// worker was unblocked when it was not.
func (m *AgentManager) ResolveWorkerQuestion(ctx context.Context, questionID, answer string) error {
	if m == nil {
		return fmt.Errorf("agent manager is not initialized")
	}
	m.mu.RLock()
	resolver := m.workerQuestionResolver
	m.mu.RUnlock()
	if resolver == nil {
		return fmt.Errorf("worker question resolution is not available for this session")
	}
	return resolver.ResolveWorkerQuestion(ctx, questionID, answer)
}

// ChangeSetIntegrator applies selected immutable snapshots through the one
// Runtime-owned integration lane. Agent Core only validates the tool shape.
type ChangeSetIntegrator interface {
	IntegrateChangeSets(ctx context.Context, selections []ChangeSetSelection) (ChangeSetIntegrationResult, error)
}

// ChildMetadataProvider is an optional interface on an external child. Its
// output is bounded display metadata, never a policy input.
type ChildMetadataProvider interface {
	ChildMetadata() map[string]any
}

// ChildCompletionProvider is an optional interface for the bounded envelope a
// blocking delegation may merge into its result.
type ChildCompletionProvider interface {
	CompletionMetadata() map[string]any
}

// WorktreeProvider materializes (or reuses) an isolated worktree for baseCwd
// and returns its directory. The Runtime implements it; internal/agent only
// requests.
type WorktreeProvider interface {
	ResolveWorktree(baseCwd string, spec WorktreeSpec) (string, error)
}

// WorktreeMetadataProvider is the richer optional contract. AgentManager uses
// it when available so external child backends receive the worktree identity
// and base revision, not just a directory.
type WorktreeMetadataProvider interface {
	ResolveWorktreeMetadata(baseCwd string, spec WorktreeSpec) (WorktreeResolution, error)
}

// NewAgentManager creates a new agent manager.
func NewAgentManager(factory *AgentFactory) *AgentManager {
	manager := &AgentManager{
		agents:   make(map[agentpkg.AgentID]agentpkg.Agent),
		parentOf: make(map[agentpkg.AgentID]agentpkg.AgentID),
		children: make(map[agentpkg.AgentID][]agentpkg.AgentID),
		statuses: make(map[agentpkg.AgentID]ManagedAgentStatus),
		cancels:  make(map[agentpkg.AgentID]context.CancelFunc),
		factory:  factory,
	}
	if factory != nil {
		factory.manager = manager
	}
	return manager
}

// SetMemberContext installs the expert-team member context: the roster
// registry, the session member mailbox, and the bound expert id. Passing
// nil/empty values clears a previous binding. Callers must set the context
// during assembly, before any agent run starts; sub-agent tools read these
// fields directly and rely on that happens-before ordering.
func (m *AgentManager) SetMemberContext(members *MemberDefRegistry, mailbox *MemberMailbox, expertID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Members = members
	m.Mailbox = mailbox
	m.ExpertID = expertID
	if mailbox != nil {
		// The lead's follow-up hook waits for members through the mailbox; the
		// manager is the only owner of "is a child still running".
		mailbox.SetRunningPredicate(m.HasRunningChildren)
	}
	if m.factory != nil {
		m.factory.memberMailbox = mailbox
	}
}

// SetChildBackend installs the Runtime-owned external child constructor. It
// must be set during assembly, before any sub-agent is created.
func (m *AgentManager) SetChildBackend(backend ChildBackend) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.childBackend = backend
	m.mu.Unlock()
}

// SetChildRunRecorder installs durable child-history persistence and the
// canonical session identity. It must be called during Runtime assembly,
// before child agents start.
func (m *AgentManager) SetChildRunRecorder(recorder ChildRunRecorder, sessionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.childRunRecorder = recorder
	m.sessionID = sessionID
	m.mu.Unlock()
}

// SetChangeSetIntegrator installs the Runtime-owned change-set application
// service. It must be set during assembly, before Router tools are exposed.
func (m *AgentManager) SetChangeSetIntegrator(integrator ChangeSetIntegrator) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.changeSetIntegrator = integrator
	m.mu.Unlock()
}

// IntegrateChangeSets delegates to the Runtime-owned integration service.
func (m *AgentManager) IntegrateChangeSets(ctx context.Context, selections []ChangeSetSelection) (ChangeSetIntegrationResult, error) {
	if m == nil {
		return ChangeSetIntegrationResult{}, fmt.Errorf("agent manager is not initialized")
	}
	m.mu.RLock()
	integrator := m.changeSetIntegrator
	m.mu.RUnlock()
	if integrator == nil {
		return ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration is not available for this session")
	}
	return integrator.IntegrateChangeSets(ctx, selections)
}

type childWorkDirProvider interface {
	WorkDir() string
}

// RecordLocalChildRun persists one completed local child through the
// Runtime-installed recorder. External backends persist their own remote
// session identity and must not be double-recorded here.
func (m *AgentManager) RecordLocalChildRun(a agentpkg.Agent, parentRunID, status string) error {
	if m == nil || a == nil {
		return nil
	}
	m.mu.RLock()
	recorder := m.childRunRecorder
	sessionID := m.sessionID
	m.mu.RUnlock()
	if recorder == nil || sessionID == "" {
		return nil
	}
	transcript, err := json.Marshal(a.GetMessages())
	if err != nil {
		return fmt.Errorf("encode child transcript: %w", err)
	}
	workDir := ""
	if provider, ok := a.(childWorkDirProvider); ok {
		workDir = provider.WorkDir()
	}
	startedAt := time.Now()
	if current, ok := m.Status(a.ID()); ok && !current.StartedAt.IsZero() {
		startedAt = current.StartedAt
	}
	now := time.Now()
	return recorder.RecordChildRun(ChildRunObservation{
		ChildID: string(a.ID()), SessionID: sessionID, ParentRunID: parentRunID,
		Backend: "local", WorkDir: workDir, Status: status,
		ContractJSON: "{}", TranscriptJSON: string(transcript),
		CreatedAt: startedAt, UpdatedAt: now,
	})
}

// SetWorktreeProvider installs the Runtime-owned worktree resolver. It must be
// set during assembly, before any sub-agent is created.
func (m *AgentManager) SetWorktreeProvider(provider WorktreeProvider) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.worktreeProvider = provider
	m.mu.Unlock()
}

// SetWorktreePerChild makes every sub-agent run in its own isolated worktree.
// It is the policy-driven trigger; explicit requests (tool parameter or member
// declaration) work regardless of this setting.
func (m *AgentManager) SetWorktreePerChild(enabled bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.worktreePerChild = enabled
	m.mu.Unlock()
}

// SetMemberWaitEnabled records whether this manager's lead may hold its run open
// for still-running members. It is true only for a bound expert team: other
// sessions still deliver member questions and completions through the mailbox
// steering drain, but a run may end while members are running so unattended
// entry points never inherit the team's bounded member wait. Callers must set
// this during assembly, before any run starts.
func (m *AgentManager) SetMemberWaitEnabled(enabled bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.factory != nil {
		m.factory.memberWaitEnabled = enabled
	}
}

// NotifyMemberQuestion queues a member's blocking question for the lead. The
// mailbox is the only wake path: a blocked subagent_wait returns on the activity
// signal, and the question is injected as a steering message at the lead's next
// iteration boundary so the lead can answer it with subagent_answer instead of
// the member blocking on an answer that can never arrive.
func (m *AgentManager) NotifyMemberQuestion(memberID, displayName, questionID, question string, options []string) {
	if m == nil {
		return
	}
	m.Mailbox.Enqueue(MemberCompletion{
		Kind:        MemberItemQuestion,
		MemberID:    memberID,
		DisplayName: displayName,
		Status:      MemberStatusQuestion,
		Payload:     question,
		QuestionID:  questionID,
		Options:     append([]string(nil), options...),
		Wake:        true,
	})
}

// HasRunningChildren reports whether any managed child (a spawned member or a
// delegated task) is still running. Team leads use it to avoid ending their run
// while member results are still in flight.
func (m *AgentManager) HasRunningChildren() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id := range m.parentOf {
		status, ok := m.statuses[id]
		if !ok {
			continue
		}
		if status.State == "ready" || status.State == "running" {
			return true
		}
	}
	return false
}

// AddStatusListener registers a listener for terminal lifecycle transitions
// (an agent entering the done or error state). Parent event streams can close
// before an asynchronously spawned child finishes, so observers that need
// reliable terminal states must subscribe here instead of relying on forwarded
// stream events alone.
func (m *AgentManager) AddStatusListener(l AgentStatusListener) {
	if m == nil || l == nil {
		return
	}
	m.mu.Lock()
	m.listeners = append(m.listeners, l)
	m.mu.Unlock()
}

// fireTerminalStatuses invokes listeners for agents that transitioned into a
// terminal state. It must be called without holding the manager lock.
func (m *AgentManager) fireTerminalStatuses(statuses []ManagedAgentStatus) {
	if len(statuses) == 0 {
		return
	}
	m.mu.RLock()
	listeners := append([]AgentStatusListener(nil), m.listeners...)
	m.mu.RUnlock()
	for _, st := range statuses {
		for _, l := range listeners {
			l(st)
		}
	}
}

func isTerminalManagedState(state string) bool {
	return state == "done" || state == "incomplete" || state == "error" || state == "canceled"
}

// UpdateRuntimeConfig updates the factory used for future agents while keeping
// existing managed agents untouched. A child backend that captured its own
// settings snapshot is refreshed too, so the next external delegation observes
// the same runtime config as a newly constructed local child.
func (m *AgentManager) UpdateRuntimeConfig(p provider.Provider, providerName string, model *provider.Model, settings *config.Settings, allow *config.AllowConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.factory != nil {
		m.factory = m.factory.withRuntimeConfig(p, providerName, model, settings, allow)
	}
	backend := m.childBackend
	m.mu.Unlock()
	if updater, ok := backend.(ChildBackendRuntimeConfig); ok {
		// Call outside the manager lock: the backend owns its own synchronization
		// and must not create a lock-order dependency with Runtime state.
		updater.UpdateChildBackendRuntimeConfig(settings)
	}
}

// Register adds an already-created top-level agent to the manager.
func (m *AgentManager) Register(a agentpkg.Agent) {
	if a == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	id := a.ID()
	m.agents[id] = a
	if a.ParentID() != "" {
		m.parentOf[id] = a.ParentID()
		m.children[a.ParentID()] = appendUniqueAgentID(m.children[a.ParentID()], id)
	}
	now := time.Now()
	m.statuses[id] = ManagedAgentStatus{
		ID:        id,
		ParentID:  a.ParentID(),
		State:     "ready",
		StartedAt: now,
		UpdatedAt: now,
	}
}

// Create creates a new agent and registers it.
// If opts.ParentID is set, validates the parent exists and is a top-level agent.
func (m *AgentManager) Create(opts AgentOptions) (agentpkg.Agent, error) {
	if opts.ParentID != "" && opts.Worktree == nil && opts.ExternalAgent != "" {
		m.mu.RLock()
		backend := m.childBackend
		m.mu.RUnlock()
		if planner, ok := backend.(ChildWorktreePlanner); ok {
			opts.Worktree = planner.ChildWorktree(opts)
		}
	}
	// Resolve an on-demand worktree before taking the manager lock: it may
	// block on git and must not stall other manager operations.
	if opts.Worktree != nil || opts.ParentID != "" {
		m.mu.RLock()
		provider := m.worktreeProvider
		perChild := m.worktreePerChild
		m.mu.RUnlock()
		// An explicit request (tool parameter or member declaration) resolves for
		// any child, including an ESM role with no parent. The per-child policy
		// applies only to spawned sub-agents.
		if opts.Worktree != nil || (perChild && opts.ParentID != "") {
			if provider == nil {
				return nil, fmt.Errorf("an isolated worktree was requested but no worktree provider is configured")
			}
			spec := WorktreeSpec{}
			if opts.Worktree != nil {
				spec = *opts.Worktree
			}
			resolved := WorktreeResolution{}
			var err error
			if metadataProvider, ok := provider.(WorktreeMetadataProvider); ok {
				resolved, err = metadataProvider.ResolveWorktreeMetadata(opts.WorkDir, spec)
			} else {
				resolved.Directory, err = provider.ResolveWorktree(opts.WorkDir, spec)
			}
			if err != nil {
				if !spec.Optional {
					return nil, fmt.Errorf("resolve sub-agent worktree: %w", err)
				}
				// Optional: keep the inherited workspace and continue.
			} else {
				opts.WorkDir = resolved.Directory
				opts.WorktreeResolved = &resolved
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	factory := m.factory

	// Generate ID if not provided
	if opts.ID == "" {
		opts.ID = agentpkg.AgentID(fmt.Sprintf("agent-%d", atomic.AddInt64(&m.counter, 1)))
	}
	// Validate parent
	if opts.ParentID != "" {
		parent, ok := m.agents[opts.ParentID]
		if !ok {
			return nil, fmt.Errorf("parent agent %s not found", opts.ParentID)
		}
		if parentCfg, ok := runtimeConfigOfManagedAgent(parent); ok {
			factory = m.factory.withParentRuntimeConfig(parentCfg)
		}
		if opts.Mode == "" {
			opts.Mode = modeOfManagedAgent(parent)
		}
		if opts.Mode == "" {
			opts.Mode = "yolo"
		}
		// Decision 5: sub-agents cannot nest (only top-level agents can spawn)
		if parent.ParentID() != "" {
			return nil, fmt.Errorf("parent agent %s is itself a sub-agent; nesting is not allowed", opts.ParentID)
		}
	}
	if opts.Mode == "" {
		opts.Mode = "yolo"
	}
	// An external child is driven by the Runtime backend's own role/profile
	// policy. Agent Core only needs a neutral bookkeeping mode so parent
	// inheritance (including Router) cannot reject a request the backend owns.
	if opts.ExternalAgent != "" {
		opts.Mode = "agent"
	}
	resolvedMode, err := factory.resolveAgentMode(opts.Session, opts.Mode)
	if err != nil {
		return nil, err
	}
	opts.Mode = resolvedMode
	if opts.ParentID != "" {
		policy := DefaultSubAgentPolicy()
		if err := policy.Validate(string(opts.ParentID), opts.Mode, len(m.children[opts.ParentID])); err != nil {
			return nil, err
		}
	}

	var a agentpkg.Agent
	handled := false
	if opts.ExternalAgent != "" && m.childBackend == nil {
		return nil, fmt.Errorf("external child %q requires a Runtime-installed child backend", opts.ExternalAgent)
	}
	if m.childBackend != nil {
		created, backendHandled, err := m.childBackend.CreateChild(opts)
		if err != nil {
			return nil, err
		}
		if backendHandled {
			if created == nil {
				return nil, fmt.Errorf("external child backend returned no agent")
			}
			a, handled = created, true
		}
	}
	if !handled {
		a = factory.Create(opts)
	}
	m.agents[opts.ID] = a
	if opts.ParentID != "" {
		m.parentOf[opts.ID] = opts.ParentID
		m.children[opts.ParentID] = append(m.children[opts.ParentID], opts.ID)
	}
	now := time.Now()
	status := ManagedAgentStatus{
		ID:                opts.ID,
		ParentID:          opts.ParentID,
		MemberID:          opts.MemberID,
		ExpertID:          opts.ExpertID,
		MemberDisplayName: opts.MemberDisplayName,
		MemberEmoji:       opts.MemberEmoji,
		MemberRole:        opts.MemberRole,
		State:             "ready",
		StartedAt:         now,
		UpdatedAt:         now,
	}
	if provider, ok := a.(ChildMetadataProvider); ok {
		status.Metadata = provider.ChildMetadata()
	}
	m.statuses[opts.ID] = status

	return a, nil
}

func modeOfManagedAgent(a agentpkg.Agent) string {
	if cfg, ok := runtimeConfigOfManagedAgent(a); ok {
		return cfg.Mode
	}
	return ""
}

// runtimeConfigProvider is implemented by external child proxies that carry a
// neutral mode/config snapshot without embedding a local AgentLoopConfig.
type runtimeConfigProvider interface {
	RuntimeConfig() (AgentLoopConfig, bool)
}

func runtimeConfigOfManagedAgent(a agentpkg.Agent) (AgentLoopConfig, bool) {
	if adapter, ok := a.(*AgentAdapter); ok && adapter != nil && adapter.inner != nil {
		return adapter.inner.config, true
	}
	if provider, ok := a.(runtimeConfigProvider); ok {
		return provider.RuntimeConfig()
	}
	return AgentLoopConfig{}, false
}

// SetCancel records the active run cancel function for an agent.
func (m *AgentManager) SetCancel(id agentpkg.AgentID, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel == nil {
		delete(m.cancels, id)
		return
	}
	m.cancels[id] = cancel
}

// Get returns an agent by ID.
func (m *AgentManager) Get(id agentpkg.AgentID) (agentpkg.Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[id]
	return a, ok
}

// Destroy stops and removes an agent and all its children.
func (m *AgentManager) Destroy(id agentpkg.AgentID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.agents[id]
	if !ok {
		return fmt.Errorf("agent %s not found", id)
	}

	// Recursively destroy children first
	children := m.children[id]
	for _, childID := range children {
		m.destroyLocked(childID)
	}

	// Abort the agent
	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}
	a.Abort()

	// Remove from parent's children list
	if parentID, hasParent := m.parentOf[id]; hasParent {
		siblings := m.children[parentID]
		filtered := make([]agentpkg.AgentID, 0, len(siblings))
		for _, sid := range siblings {
			if sid != id {
				filtered = append(filtered, sid)
			}
		}
		m.children[parentID] = filtered
	}

	// Remove self
	delete(m.agents, id)
	delete(m.parentOf, id)
	delete(m.children, id)
	delete(m.statuses, id)
	delete(m.cancels, id)

	return nil
}

// DetachChild removes a child from its parent's active child list while
// retaining the child agent, parent link, and status for later inspection.
func (m *AgentManager) DetachChild(id agentpkg.AgentID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if parentID, hasParent := m.parentOf[id]; hasParent {
		m.children[parentID] = removeAgentID(m.children[parentID], id)
	}
}

// Finish unregisters a completed top-level agent and cancels any remaining children.
// Child statuses are retained so callers can inspect why a delegated task stopped.
// Finish must not abort the completed agent itself: interactive frontends may keep
// the same agent instance for the next turn, and Abort is a one-way signal.
func (m *AgentManager) Finish(id agentpkg.AgentID, cause error) {
	var terminal []ManagedAgentStatus
	m.mu.Lock()

	if cause != nil {
		for _, childID := range m.children[id] {
			terminal = m.finishChildLocked(childID, cause, terminal)
		}
	}
	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}
	if parentID, hasParent := m.parentOf[id]; hasParent {
		m.children[parentID] = removeAgentID(m.children[parentID], id)
	}
	delete(m.agents, id)
	delete(m.parentOf, id)
	if cause != nil {
		delete(m.children, id)
	}
	delete(m.statuses, id)
	m.mu.Unlock()
	m.fireTerminalStatuses(terminal)
}

// destroyLocked destroys an agent without locking (caller must hold lock).
func (m *AgentManager) destroyLocked(id agentpkg.AgentID) {
	// Destroy children recursively
	for _, childID := range m.children[id] {
		m.destroyLocked(childID)
	}
	if a, ok := m.agents[id]; ok {
		if cancel, ok := m.cancels[id]; ok {
			cancel()
			delete(m.cancels, id)
		}
		a.Abort()
	}
	delete(m.agents, id)
	delete(m.parentOf, id)
	delete(m.children, id)
	delete(m.statuses, id)
	delete(m.cancels, id)
}

func (m *AgentManager) finishChildLocked(id agentpkg.AgentID, cause error, terminal []ManagedAgentStatus) []ManagedAgentStatus {
	for _, childID := range m.children[id] {
		terminal = m.finishChildLocked(childID, cause, terminal)
	}
	if cancel, ok := m.cancels[id]; ok {
		cancel()
		delete(m.cancels, id)
	}
	if a, ok := m.agents[id]; ok {
		a.Abort()
	}
	st := m.statuses[id]
	st.ID = id
	if st.StartedAt.IsZero() {
		st.StartedAt = time.Now()
	}
	if parentID, ok := m.parentOf[id]; ok {
		st.ParentID = parentID
	}
	if !isTerminalManagedState(st.State) {
		previous := st.State
		st.State = "error"
		if cause != nil {
			st.Error = cause.Error()
		} else if st.Error == "" {
			st.Error = "parent agent finished"
		}
		if previous != "error" {
			st.UpdatedAt = time.Now()
			terminal = append(terminal, st)
		}
	}
	st.UpdatedAt = time.Now()
	m.statuses[id] = st

	delete(m.agents, id)
	delete(m.parentOf, id)
	delete(m.children, id)
	return terminal
}

// MarkRunning records that an agent has started processing a task.
func (m *AgentManager) MarkRunning(id agentpkg.AgentID) {
	m.updateStatus(id, "running", "", "")
}

// BeginFollowUp starts a new turn for an already-managed child. Terminal state
// is sticky within one turn so a late compatibility EventDone/EventError cannot
// overwrite the canonical EventRunFinished outcome. A follow-up is a new turn,
// so it must explicitly clear that terminal state before its events arrive.
func (m *AgentManager) BeginFollowUp(id agentpkg.AgentID) {
	if m == nil {
		return
	}
	m.mu.Lock()
	st, ok := m.statuses[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if st.StartedAt.IsZero() {
		st.StartedAt = time.Now()
	}
	st.State = "running"
	st.Result = ""
	st.Error = ""
	st.UpdatedAt = time.Now()
	m.statuses[id] = st
	m.mu.Unlock()
}

// MarkDone records successful completion and the last reported result.
func (m *AgentManager) MarkDone(id agentpkg.AgentID, result string) {
	m.updateStatus(id, "done", result, "")
}

// MarkIncomplete records that an agent stopped before completing its objective.
func (m *AgentManager) MarkIncomplete(id agentpkg.AgentID, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.updateStatus(id, "incomplete", "", msg)
}

// MarkError records an agent failure.
func (m *AgentManager) MarkError(id agentpkg.AgentID, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.updateStatus(id, "error", "", msg)
}

// MarkCanceled records that an agent's run was canceled (user abort, timeout,
// or context cancellation). Canceled is a terminal state distinct from error.
func (m *AgentManager) MarkCanceled(id agentpkg.AgentID, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.updateStatus(id, "canceled", "", msg)
}

func (m *AgentManager) updateStatus(id agentpkg.AgentID, state, result, errMsg string) {
	m.mu.Lock()
	st := m.statuses[id]
	st.ID = id
	if st.StartedAt.IsZero() {
		st.StartedAt = time.Now()
	}
	if parentID, ok := m.parentOf[id]; ok {
		st.ParentID = parentID
	}
	previous := st.State
	// Terminal state is sticky. Canonical EventRunFinished is followed by
	// legacy EventDone/EventError for compatibility; those events must never
	// overwrite incomplete, canceled, error, or success with another outcome.
	if isTerminalManagedState(previous) && previous != state {
		m.mu.Unlock()
		return
	}
	st.State = state
	if result != "" {
		st.Result = result
	}
	if errMsg != "" {
		st.Error = errMsg
	}
	// Backend-owned metadata can change after construction (for example an
	// external child's terminal change-set digest). Refresh it on every status
	// transition so subagent_status/subagent_wait never project stale facts.
	if provider, ok := m.agents[id].(ChildMetadataProvider); ok {
		st.Metadata = provider.ChildMetadata()
	}
	st.UpdatedAt = time.Now()
	m.statuses[id] = st
	m.mu.Unlock()
	if isTerminalManagedState(state) && previous != state {
		m.fireTerminalStatuses([]ManagedAgentStatus{st})
	}
}

// Status returns a copy of the tracked status for an agent.
func (m *AgentManager) Status(id agentpkg.AgentID) (ManagedAgentStatus, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.statuses[id]
	if !ok {
		return st, false
	}
	st.Metadata = cloneStatusMetadata(st.Metadata)
	return st, true
}

// Statuses returns a sorted copy of all tracked agent statuses.
func cloneStatusMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func (m *AgentManager) Statuses() []ManagedAgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ManagedAgentStatus, 0, len(m.statuses))
	for _, st := range m.statuses {
		st.Metadata = cloneStatusMetadata(st.Metadata)
		statuses = append(statuses, st)
	}
	sort.Slice(statuses, func(i, j int) bool {
		if !statuses[i].StartedAt.Equal(statuses[j].StartedAt) {
			return statuses[i].StartedAt.Before(statuses[j].StartedAt)
		}
		return statuses[i].ID < statuses[j].ID
	})
	return statuses
}

// List returns all agent IDs.
func (m *AgentManager) List() []agentpkg.AgentID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]agentpkg.AgentID, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := m.statuses[ids[i]]
		right := m.statuses[ids[j]]
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.Before(right.StartedAt)
		}
		return ids[i] < ids[j]
	})
	return ids
}

func appendUniqueAgentID(ids []agentpkg.AgentID, id agentpkg.AgentID) []agentpkg.AgentID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeAgentID(ids []agentpkg.AgentID, id agentpkg.AgentID) []agentpkg.AgentID {
	if len(ids) == 0 {
		return nil
	}
	filtered := make([]agentpkg.AgentID, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

// Children returns the children of an agent.
func (m *AgentManager) Children(id agentpkg.AgentID) []agentpkg.AgentID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	children := m.children[id]
	if children == nil {
		return nil
	}
	result := make([]agentpkg.AgentID, len(children))
	copy(result, children)
	return result
}

// Parent returns the parent ID of an agent.
func (m *AgentManager) Parent(id agentpkg.AgentID) (agentpkg.AgentID, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pid, ok := m.parentOf[id]
	return pid, ok
}

// Count returns the number of active agents.
func (m *AgentManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}

// HasRunning reports whether any managed agent is currently executing or ready
// to execute work. Completed retained agents are history, not active blockers.
func (m *AgentManager) HasRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id := range m.agents {
		state := m.statuses[id].State
		if state == "" || state == "ready" || state == "running" {
			return true
		}
	}
	return false
}
