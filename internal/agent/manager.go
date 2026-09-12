package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
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
	StartedAt         time.Time
	UpdatedAt         time.Time
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
// existing managed agents untouched.
func (m *AgentManager) UpdateRuntimeConfig(p provider.Provider, providerName string, model *provider.Model, settings *config.Settings, allow *config.AllowConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.factory == nil {
		return
	}
	m.factory = m.factory.withRuntimeConfig(p, providerName, model, settings, allow)
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

	a := factory.Create(opts)
	m.agents[opts.ID] = a
	if opts.ParentID != "" {
		m.parentOf[opts.ID] = opts.ParentID
		m.children[opts.ParentID] = append(m.children[opts.ParentID], opts.ID)
	}
	now := time.Now()
	m.statuses[opts.ID] = ManagedAgentStatus{
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

	return a, nil
}

func modeOfManagedAgent(a agentpkg.Agent) string {
	if cfg, ok := runtimeConfigOfManagedAgent(a); ok {
		return cfg.Mode
	}
	return ""
}

func runtimeConfigOfManagedAgent(a agentpkg.Agent) (AgentLoopConfig, bool) {
	if adapter, ok := a.(*AgentAdapter); ok && adapter != nil && adapter.inner != nil {
		return adapter.inner.config, true
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
	return st, ok
}

// Statuses returns a sorted copy of all tracked agent statuses.
func (m *AgentManager) Statuses() []ManagedAgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ManagedAgentStatus, 0, len(m.statuses))
	for _, st := range m.statuses {
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
