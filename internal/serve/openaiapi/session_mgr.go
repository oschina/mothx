package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/provider"
	openaiprovider "github.com/oschina/mothx/internal/provider/openai"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/util"
)

// APISession holds state for a single API session.
type APISession struct {
	// Runtime owns the front-end-neutral session resources. The duplicated
	// fields below are temporary adapter aliases retained for API compatibility
	// while Channel, ACP and TUI migrate to agentruntime.SessionRuntime.
	Runtime *agentruntime.SessionRuntime

	ID           string
	WorkDir      string
	Manager      *session.Manager
	Registry     *tools.Registry
	SandboxMgr   *sandbox.Manager
	AgentMgr     *agent.AgentManager // nil unless sub-agents/delegate/workflows enabled
	SkillsMgr    *skills.Manager
	ActiveSkills map[string]bool
	ExtraContext string
	RuleContent  string
	Mode         string // session-level mode override
	DisplayMode  string // session-level transcript display mode
	DelegateMode bool   // session-level delegation mode
	Workflows    bool   // session-level workflow mode
	WebSearch    bool   // session-level hosted web search toggle
	Browser      bool   // session-level browser tool toggle
	A2AMaster    bool   // session-level A2A dispatch tool toggle
	MultiAgent   bool   // session-level sub-agent tools toggle
	MCPClients   []*mcp.Client
	LastUsed     time.Time
	mu           sync.Mutex // serializes requests within this session
	lastUsedMu   sync.RWMutex
	runMu        sync.RWMutex
	running      bool
	uses         int

	// ForceCompact is a legacy/session flag consumed by the next agent run.
	ForceCompact bool

	Execution   *agentruntime.ExecutionRuntime
	Decisions   *agentruntime.DecisionService
	executionMu sync.RWMutex
	durableRuns map[string]struct{}

	approvalMu       sync.Mutex
	pendingApprovals map[string]pendingSessionApproval
	pendingQuestions map[string]pendingSessionQuestion
	activeRunID      string
	activeRunStatus  string
	activeRunAgent   *agent.Agent
	runCancel        context.CancelFunc
}

// pendingSessionApproval retains the protocol payload for one pending WebUI approval.
type pendingSessionApproval struct {
	Request SessionApprovalRequest
}

type pendingSessionQuestion struct {
	Request SessionQuestionRequest
}

// SessionApprovalResponse is a WebUI decision for one pending approval.
type SessionApprovalResponse struct {
	Action string `json:"action"`
}

// SessionApprovalResolution is the server-confirmed terminal approval state.
type SessionApprovalResolution struct {
	ApprovalID string `json:"approvalId"`
	SessionID  string `json:"sessionId"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// ActiveSessionInfo is the management API view of an active API session.
type ActiveSessionInfo struct {
	ID              string                                 `json:"id"`
	WorkDir         string                                 `json:"workDir"`
	Mode            string                                 `json:"mode,omitempty"`
	DelegateMode    bool                                   `json:"delegateMode,omitempty"`
	Workflows       bool                                   `json:"workflows,omitempty"`
	WebSearch       bool                                   `json:"webSearch,omitempty"`
	Browser         bool                                   `json:"browser,omitempty"`
	A2AMaster       bool                                   `json:"a2aMaster,omitempty"`
	MultiAgent      bool                                   `json:"multiAgent,omitempty"`
	Active          bool                                   `json:"active"`
	Running         bool                                   `json:"running,omitempty"`
	Execution       *agentruntime.SessionExecutionSnapshot `json:"execution,omitempty"`
	LastUsed        time.Time                              `json:"lastUsed"`
	MessageCount    int                                    `json:"messageCount"`
	Preview         string                                 `json:"preview,omitempty"`
	Title           string                                 `json:"title,omitempty"`
	ProjectID       string                                 `json:"projectId,omitempty"`
	Pinned          bool                                   `json:"pinned,omitempty"`
	ChannelType     string                                 `json:"channelType,omitempty"`
	ChannelID       string                                 `json:"channelId,omitempty"`
	ChannelLabel    string                                 `json:"channelLabel,omitempty"`
	Bound           bool                                   `json:"bound,omitempty"`
	ParentSessionID string                                 `json:"parentSessionId,omitempty"`
	ForkBoundarySeq int64                                  `json:"forkBoundarySeq,omitempty"`
	SeedLength      int64                                  `json:"seedLength,omitempty"`
	ForkKind        string                                 `json:"forkKind,omitempty"`
}

// SessionMessageEntry is a simplified message for the WebUI.
type SessionMessageEntry struct {
	ID                string                  `json:"id,omitempty"`
	Seq               int64                   `json:"seq,omitempty"`
	Role              string                  `json:"role"`
	Content           string                  `json:"content,omitempty"`
	Contents          []provider.ContentBlock `json:"contents,omitempty"`
	AgentID           string                  `json:"agentId,omitempty"`
	MemberID          string                  `json:"memberId,omitempty"`
	ExpertID          string                  `json:"expertId,omitempty"`
	MemberDisplayName string                  `json:"memberDisplayName,omitempty"`
	MemberEmoji       string                  `json:"memberEmoji,omitempty"`
	MemberRole        string                  `json:"memberRole,omitempty"`
	ToolCallID        string                  `json:"toolCallId,omitempty"`
	ToolName          string                  `json:"toolName,omitempty"`
	Arguments         json.RawMessage         `json:"arguments,omitempty"`
	InvalidArgs       string                  `json:"invalidArguments,omitempty"`
	Plan              *SessionTaskPlan        `json:"plan,omitempty"`
	IsError           bool                    `json:"isError,omitempty"`
	Summary           string                  `json:"summary,omitempty"`
	HasDetail         bool                    `json:"hasDetail,omitempty"`
	Attachments       []provider.Attachment   `json:"attachments,omitempty"`
}

// SessionToolResultDetail contains the full persisted result for one tool call.
type SessionToolResultDetail struct {
	ToolCallID string                  `json:"toolCallId"`
	ToolName   string                  `json:"toolName,omitempty"`
	Content    string                  `json:"content,omitempty"`
	Contents   []provider.ContentBlock `json:"contents,omitempty"`
	IsError    bool                    `json:"isError,omitempty"`
}

// SessionSubAgentInfo is the WebUI view of a managed sub-agent.
type SessionSubAgentInfo struct {
	ID                string `json:"id"`
	ParentID          string `json:"parentId,omitempty"`
	MemberID          string `json:"memberId,omitempty"`
	ExpertID          string `json:"expertId,omitempty"`
	MemberDisplayName string `json:"memberDisplayName,omitempty"`
	MemberEmoji       string `json:"memberEmoji,omitempty"`
	MemberRole        string `json:"memberRole,omitempty"`
	Status            string `json:"status"`
	Active            bool   `json:"active"`
	MessageCount      int    `json:"messageCount"`
	LastResponse      string `json:"lastResponse,omitempty"`
	Error             string `json:"error,omitempty"`
	StartedAt         string `json:"startedAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

// SessionTaskPlan is the WebUI view of a plan tool call.
type SessionTaskPlan struct {
	Title string            `json:"title,omitempty"`
	Steps []SessionPlanStep `json:"steps,omitempty"`
	Note  string            `json:"note,omitempty"`
}

// SessionPlanStep is one todo item in a plan tool call.
type SessionPlanStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ErrActiveSessionIDAmbiguous is returned when a session ID matches multiple active workdirs.
var ErrActiveSessionIDAmbiguous = errors.New("active session ID is ambiguous")

// ErrSessionToolResultNotFound is returned when a persisted tool result cannot be found.
var ErrSessionToolResultNotFound = errors.New("session tool result not found")

// ErrSessionNotFound is returned when a session cannot be found in memory or persistence.
var ErrSessionNotFound = errors.New("session not found")

// ErrSubAgentNotFound is returned when a sub-agent cannot be found in an active session.
var ErrSubAgentNotFound = errors.New("sub-agent not found")

// ErrInvalidCapability is returned when a capability patch contains an invalid value.
var ErrInvalidCapability = errors.New("invalid capability value")

// Lock acquires the session lock (one request at a time per session).
func (s *APISession) Lock() { s.mu.Lock() }

// TryLock acquires the session lock without waiting.
func (s *APISession) TryLock() bool { return s != nil && s.mu.TryLock() }

// Unlock releases the session lock.
func (s *APISession) Unlock() { s.mu.Unlock() }

// Touch updates the last-used timestamp.
func (s *APISession) Touch() {
	if s == nil {
		return
	}
	s.lastUsedMu.Lock()
	s.LastUsed = time.Now()
	s.lastUsedMu.Unlock()
}

func (s *APISession) lastUsedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.lastUsedMu.RLock()
	defer s.lastUsedMu.RUnlock()
	return s.LastUsed
}

func (s *APISession) pin() {
	s.runMu.Lock()
	s.uses++
	s.runMu.Unlock()
}

func (s *APISession) unpin() {
	s.runMu.Lock()
	if s.uses > 0 {
		s.uses--
	}
	s.runMu.Unlock()
}

func (s *APISession) isInUse() bool {
	if s == nil {
		return false
	}
	s.runMu.RLock()
	defer s.runMu.RUnlock()
	return s.uses > 0
}

// SetRunning records whether a chat run is currently active for this session.
func (s *APISession) SetRunning(running bool) {
	if s == nil {
		return
	}
	s.runMu.Lock()
	s.running = running
	s.runMu.Unlock()
}

// IsRunning reports whether a chat run is currently active for this session.
func (s *APISession) IsRunning() bool {
	if s == nil {
		return false
	}
	s.runMu.RLock()
	defer s.runMu.RUnlock()
	return s.running
}

// inspectExecution is the compatibility boundary for callers that need a
// session management projection. Durable state is always preferred; the
// process-local fallback is used only by embedded fixtures that have no shared
// session root yet.
func (s *APISession) inspectExecution() (agentruntime.SessionExecutionSnapshot, error) {
	if s == nil {
		return agentruntime.SessionExecutionSnapshot{State: agentruntime.SessionExecutionUnknown, Busy: true, DisplayOwnerScope: "unknown", LinkageState: "none", RecoveryAction: "none"}, fmt.Errorf("session is nil")
	}
	sessionDir := ""
	if s.Manager != nil {
		sessionDir = s.Manager.GetSessionDir()
	}
	if strings.TrimSpace(sessionDir) != "" && s.ID != "" {
		return agentruntime.InspectSessionExecution(sessionDir, s.ID)
	}
	if execution := s.executionRuntime(); execution != nil {
		if runID, active := execution.Active(); active {
			return agentruntime.SessionExecutionSnapshot{
				SessionID: s.ID, SessionExists: true, State: agentruntime.SessionExecutionLocal,
				Phase: "executing", Running: true, Busy: true, CanSubmit: false,
				CanCancelLocal: true, ActiveRun: &agentruntime.SessionRunSummary{ID: runID, Status: string(execution.State())},
				LinkageState: "legacy_unbound", RecoveryAction: "none", DisplayOwnerScope: "local",
			}, nil
		}
	}
	// Legacy embedded sessions may have only the old running bit. Keep that
	// conservative projection until they acquire a durable session root.
	s.runMu.RLock()
	legacyRunning := s.running
	s.runMu.RUnlock()
	if legacyRunning {
		return agentruntime.SessionExecutionSnapshot{SessionID: s.ID, SessionExists: true, State: agentruntime.SessionExecutionUnknown, Phase: "legacy", Busy: true, DisplayOwnerScope: "unknown", LinkageState: "legacy_unbound", RecoveryAction: "none"}, nil
	}
	return agentruntime.SessionExecutionSnapshot{SessionID: s.ID, SessionExists: s.ID != "", State: agentruntime.SessionExecutionIdle, Phase: "idle", CanSubmit: true, DisplayOwnerScope: "none", LinkageState: "none", RecoveryAction: "none"}, nil
}

// RequestSessionStop is the Serve projection of the Runtime-owned stop
// operation. It supplies provider and Decision hooks but does not decide Run
// ownership from APISession or RunManager memory.
func (s *Server) RequestSessionStop(ctx context.Context, id string) (agentruntime.SessionStopResult, error) {
	return s.requestSessionStop(ctx, id, "")
}

// requestSessionStop is the Serve projection of Runtime stop with an optional
// target Run identity. The target is used by Run API cancellation to prevent a
// stale request from cancelling a newer Run in the same Session.
func (s *Server) requestSessionStop(ctx context.Context, id, expectedRunID string) (agentruntime.SessionStopResult, error) {
	if s == nil || s.settings == nil || id == "" {
		return agentruntime.SessionStopResult{}, ErrSessionNotFound
	}
	result, err := agentruntime.RequestSessionStop(ctx, s.settings.GetSessionDir(), id, agentruntime.SessionStopOptions{
		ExpectedRunID: expectedRunID,
		RemoteCancel: func(ctx context.Context, request agentruntime.RemoteStopRequest) error {
			s.mu.RLock()
			driver := s.responsesRuns
			currentProvider := s.provider
			s.mu.RUnlock()
			if driver == nil || currentProvider == nil || !strings.EqualFold(currentProvider.Name(), request.Provider) {
				return agentruntime.ErrRemoteStopUnsupported
			}
			return driver.Cancel(ctx, request.SessionID, request.RemoteRunID)
		},
	})

	if result.Code == agentruntime.SessionStopAccepted && result.Execution.ActiveRun != nil && s.pool != nil {
		if sess, getErr := s.pool.getExact(id); getErr == nil && sess != nil {
			runID := result.Execution.ActiveRun.ID
			sess.approvalMu.Lock()
			if sess.activeRunID == runID {
				sess.activeRunStatus = string(agentruntime.RunStateCancelling)
			}
			sess.approvalMu.Unlock()
			s.clearSessionApprovalsForRun(sess, runID, "cancelled", "run cancelled by user")
			s.publishSessionRuntime(sess)
		}
	} else if result.Execution.SessionExists {
		s.PublishSessionRuntime(id)
	}
	return result, err
}

// CancelSessionRun remains as a compatibility wrapper for in-process callers.
// New protocol handlers should inspect the structured RequestSessionStop result.
func (s *Server) CancelSessionRun(id string) error {
	result, err := s.RequestSessionStop(context.Background(), id)
	if err != nil {
		return err
	}
	switch result.Code {
	case agentruntime.SessionStopAccepted, agentruntime.SessionStopRemoteAccepted, agentruntime.SessionStopRecoveryStarted:
		return nil
	case agentruntime.SessionStopNoActiveRun:
		return ErrSessionNotFound
	default:
		return fmt.Errorf("session stop rejected: %s", result.Code)
	}
}

func (s *Server) publishRuntimeSnapshot(sessionID string, snapshot *SessionRuntimeSnapshot) {
	if s == nil || sessionID == "" || snapshot == nil {
		return
	}
	runID := ""
	if snapshot.ActiveRun != nil {
		runID = snapshot.ActiveRun.RunID
	}
	if broker := s.getEventBroker(); broker != nil {
		broker.PublishRuntimeEvent(sessionID, runID, snapshot)
	}
	s.publishSessionStreamEvent(sessionID, "runtime_event", snapshot)
}

// PublishSessionRuntime publishes the canonical runtime state for an external
// execution source such as the WeChat or Feishu channel dispatcher.
func (s *Server) PublishSessionRuntime(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	snapshot, err := s.GetSessionRuntime(sessionID)
	if err != nil {
		return
	}
	s.publishRuntimeSnapshot(sessionID, snapshot)
}

func (s *Server) publishSessionRuntime(sess *APISession) {
	if s == nil || sess == nil {
		return
	}
	caps := s.capabilitiesFromSession(sess, true, sess.Manager != nil)
	s.publishRuntimeSnapshot(sess.ID, s.runtimeSnapshotFromCapabilities(&caps))
}

func (s *APISession) beginRun(runID string) {
	execution := s.ensureExecution()
	_, _ = execution.Begin(context.Background(), runID)
	s.beginRunBookkeeping(runID)
}

// ensureExecution lazily creates the session execution owner. Background
// tool progress can arrive concurrently, so initialization must be serialized
// even when the request that admitted the run has already returned.
func (s *APISession) ensureExecution() *agentruntime.ExecutionRuntime {
	if s == nil {
		return nil
	}
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.Execution == nil {
		s.Execution = &agentruntime.ExecutionRuntime{}
	}
	// A foreground finalizer normally clears the adapter projection after a
	// successful terminal commit. If that commit outlives the request context,
	// Runtime-owned retry completion must perform the same cleanup asynchronously.
	s.Execution.SetTerminalObserver(func(runID string, _ agentruntime.RunState) {
		s.finishRun(runID)
		s.clearDurableRun(runID)
	})
	return s.Execution
}

func (s *APISession) executionRuntime() *agentruntime.ExecutionRuntime {
	if s == nil {
		return nil
	}
	s.executionMu.RLock()
	execution := s.Execution
	s.executionMu.RUnlock()
	return execution
}

func (s *APISession) markDurableRun(runID string) {
	if s == nil || runID == "" {
		return
	}
	s.approvalMu.Lock()
	if s.durableRuns == nil {
		s.durableRuns = make(map[string]struct{})
	}
	s.durableRuns[runID] = struct{}{}
	s.approvalMu.Unlock()
}

func (s *APISession) isDurableRun(runID string) bool {
	if s == nil || runID == "" {
		return false
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	_, ok := s.durableRuns[runID]
	return ok
}

func (s *APISession) clearDurableRun(runID string) {
	if s == nil || runID == "" {
		return
	}
	s.approvalMu.Lock()
	delete(s.durableRuns, runID)
	s.approvalMu.Unlock()
}
func (s *APISession) beginRunBookkeeping(runID string) {
	s.approvalMu.Lock()
	s.activeRunID = runID
	s.activeRunStatus = "running"
	s.activeRunAgent = nil
	s.runCancel = nil
	s.approvalMu.Unlock()
	s.SetRunning(true)
}

func (s *APISession) attachRunAgent(runID string, a *agent.Agent, cancel context.CancelFunc) bool {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if s.activeRunID != runID || s.activeRunStatus != "running" {
		return false
	}
	s.activeRunAgent = a
	s.runCancel = cancel
	if execution := s.executionRuntime(); execution != nil {
		execution.SetAgent(a)
	}
	return true
}

func (s *APISession) markRunTerminalizing(runID string) {
	s.approvalMu.Lock()
	if s.activeRunID == runID && s.activeRunStatus == "running" {
		s.activeRunStatus = "terminalizing"
	}
	s.approvalMu.Unlock()
}

func (s *APISession) finishRun(runID string) {
	s.approvalMu.Lock()
	if s.activeRunID == runID {
		s.activeRunID = ""
		s.activeRunStatus = ""
		s.activeRunAgent = nil
		s.runCancel = nil
	}
	s.approvalMu.Unlock()
	s.SetRunning(false)
}

// ActiveRunID returns the current run identifier, if any.
func (s *APISession) ActiveRunID() string {
	if s == nil {
		return ""
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	return s.activeRunID
}

// SessionPool manages multiple concurrent API sessions.
type SessionPool struct {
	mu           sync.RWMutex
	sessions     map[string]*APISession
	maxSess      int
	idleTTL      time.Duration
	stopCh       chan struct{}
	stopOnce     sync.Once
	stopped      bool
	backgroundWG sync.WaitGroup
}

func sessionPoolKey(workDir, id string) string {
	return workDir + "\x00" + id
}

// NewSessionPool creates a session pool.
func NewSessionPool(maxSessions int, idleTimeout time.Duration) *SessionPool {
	p := &SessionPool{
		sessions: make(map[string]*APISession),
		maxSess:  maxSessions,
		idleTTL:  idleTimeout,
		stopCh:   make(chan struct{}),
	}
	if idleTimeout > 0 {
		go p.cleanupLoop()
	}
	return p
}

func (p *SessionPool) Snapshot() []*APISession {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*APISession, 0, len(p.sessions))
	for _, sess := range p.sessions {
		result = append(result, sess)
	}
	return result
}

// Go runs a short-lived pool-owned background task. Tracking these tasks keeps
// best-effort work such as title generation from touching a test or server's
// session database after Shutdown has returned.
func (p *SessionPool) Go(fn func()) bool {
	if p == nil || fn == nil {
		return false
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return false
	}
	p.backgroundWG.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.backgroundWG.Done()
		fn()
	}()
	return true
}

// Get returns an existing session by ID, or nil.
func (p *SessionPool) Get(id string) *APISession {
	return p.GetForWorkDir("", id)
}

// GetForWorkDir returns a session by workDir and ID, or nil.
func (p *SessionPool) GetForWorkDir(workDir, id string) *APISession {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if workDir != "" {
		return p.sessions[sessionPoolKey(workDir, id)]
	}
	var found *APISession
	for _, s := range p.sessions {
		if s.ID != id {
			continue
		}
		if found != nil {
			return nil
		}
		found = s
	}
	return found
}

// Pin keeps a session resident while a caller is about to use it. The pool
// lock closes the gap between lookup and session locking, so idle eviction
// cannot replace a live session with a second instance.
func (p *SessionPool) Pin(s *APISession) bool {
	if p == nil || s == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions[sessionPoolKey(s.WorkDir, s.ID)] != s {
		return false
	}
	s.pin()
	return true
}

// Unpin releases a residency reference acquired by Pin.
func (p *SessionPool) Unpin(s *APISession) {
	if s != nil {
		s.unpin()
	}
}

// Put adds a session to the pool. Returns an error if the pool is at capacity.
func (p *SessionPool) Put(s *APISession) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := sessionPoolKey(s.WorkDir, s.ID)
	if p.maxSess > 0 && len(p.sessions) >= p.maxSess {
		// Check if we have an existing entry (replace is OK)
		if _, exists := p.sessions[key]; !exists {
			return &PoolFullError{Max: p.maxSess}
		}
	}
	s.Touch()
	p.sessions[key] = s
	return nil
}

// Remove removes a session by ID.
func (p *SessionPool) Remove(id string) {
	p.RemoveByWorkDir("", id)
}

// RemoveByWorkDir removes a session by workDir and ID.
func (p *SessionPool) RemoveByWorkDir(workDir, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if workDir != "" {
		delete(p.sessions, sessionPoolKey(workDir, id))
		return
	}
	var key string
	var found bool
	for k, s := range p.sessions {
		if s.ID != id {
			continue
		}
		if found {
			return
		}
		key = k
		found = true
	}
	if found {
		delete(p.sessions, key)
	}
}

// Replace swaps an existing session entry for a new one.
func (p *SessionPool) Replace(oldID string, s *APISession) {
	p.ReplaceByWorkDir("", oldID, s)
}

// ReplaceByWorkDir swaps an existing session entry for a new one.
func (p *SessionPool) ReplaceByWorkDir(workDir, oldID string, s *APISession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if oldID != "" {
		if workDir != "" {
			delete(p.sessions, sessionPoolKey(workDir, oldID))
		} else {
			for k, sess := range p.sessions {
				if sess.ID == oldID {
					delete(p.sessions, k)
					break
				}
			}
		}
	}
	if s != nil {
		s.Touch()
		key := sessionPoolKey(s.WorkDir, s.ID)
		if _, exists := p.sessions[key]; !exists && p.maxSess > 0 && len(p.sessions) >= p.maxSess {
			for k, sess := range p.sessions {
				if sess.ID == s.ID && sess.WorkDir == s.WorkDir {
					key = k
					break
				}
			}
		}
		p.sessions[key] = s
	}
}

// Count returns the number of active sessions.
func (p *SessionPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}

// List returns all session IDs.
func (p *SessionPool) List() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0, len(p.sessions))
	for id := range p.sessions {
		ids = append(ids, id)
	}
	return ids
}

// ListForWorkDir returns all session IDs for a specific workDir.
func (p *SessionPool) ListForWorkDir(workDir string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0)
	for _, s := range p.sessions {
		if s.WorkDir == workDir {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

func (p *SessionPool) listDetails() []ActiveSessionInfo {
	p.mu.RLock()
	active := make([]*APISession, 0, len(p.sessions))
	for _, s := range p.sessions {
		active = append(active, s)
	}
	p.mu.RUnlock()

	sessions := make([]ActiveSessionInfo, 0, len(active))
	for _, s := range active {
		lastUsed := s.lastUsedAt()
		messageCount := 0
		if s.Manager != nil {
			messageCount = len(s.Manager.GetMessages())
		}
		execution, executionErr := s.inspectExecution()
		if executionErr != nil {
			execution.State = agentruntime.SessionExecutionUnknown
			execution.Busy = true
			execution.CanSubmit = false
		}
		sessions = append(sessions, ActiveSessionInfo{
			ID:           s.ID,
			WorkDir:      s.WorkDir,
			Mode:         s.Mode,
			DelegateMode: s.DelegateMode,
			Workflows:    s.Workflows,
			WebSearch:    s.WebSearch,
			Browser:      s.Browser,
			A2AMaster:    s.A2AMaster,
			MultiAgent:   s.MultiAgent,
			Active:       true,
			Running:      execution.Running,
			Execution:    &execution,
			LastUsed:     lastUsed,
			MessageCount: messageCount,
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastUsed.Equal(sessions[j].LastUsed) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].LastUsed.After(sessions[j].LastUsed)
	})
	return sessions
}

func (p *SessionPool) getExact(id string) (*APISession, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var found *APISession
	for _, s := range p.sessions {
		if s.ID != id {
			continue
		}
		if found != nil {
			return nil, ErrActiveSessionIDAmbiguous
		}
		found = s
	}
	return found, nil
}

func (s *Server) findSessionWorkDir(id string) (string, bool, error) {
	if id == "" || s == nil {
		return "", false, nil
	}
	if s.pool != nil {
		sess, err := s.pool.getExact(id)
		if err != nil {
			return "", false, err
		}
		if sess != nil {
			return sess.WorkDir, true, nil
		}
	}
	if s.settings == nil {
		return "", false, nil
	}
	mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), id)
	if err != nil {
		return "", false, nil
	}
	if header := mgr.GetHeader(); header != nil {
		return header.Cwd, true, nil
	}
	return "", true, nil
}

// Shutdown stops eviction and closes every session through the shared
// SessionRuntime boundary. The context bounds agent cancellation and MCP
// cleanup; the pool is emptied only after each runtime has been offered a
// chance to terminalize its active run.
func (p *SessionPool) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		close(p.stopCh)
	})
	p.backgroundWG.Wait()
	sessions := p.Snapshot()
	var firstErr error
	closed := make(map[*APISession]struct{}, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		if sess.Runtime != nil {
			if err := sess.Runtime.Shutdown(ctx); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("shutdown session %s: %w", sess.ID, err)
				}
			} else {
				closed[sess] = struct{}{}
				sess.MCPClients = nil
			}
		} else {
			mcp.CloseClients(sess.MCPClients)
			sess.MCPClients = nil
		}
	}
	p.mu.Lock()
	for key, sess := range p.sessions {
		if _, ok := closed[sess]; ok {
			delete(p.sessions, key)
		}
	}
	p.mu.Unlock()
	return firstErr
}

// Stop is the compatibility helper used by tests and embedders that do not
// have a request context. Production server shutdown should call Shutdown.
func (p *SessionPool) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.Shutdown(ctx)
}

// cleanupLoop periodically removes idle sessions.
func (p *SessionPool) cleanupLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.evictIdle()
		}
	}
}

func (p *SessionPool) evictIdle() {
	if p.idleTTL <= 0 {
		return
	}
	now := time.Now()
	p.mu.Lock()
	candidates := make([]*APISession, 0, len(p.sessions))
	for _, s := range p.sessions {
		if s.isInUse() {
			continue
		}
		candidates = append(candidates, s)
	}
	p.mu.Unlock()
	var evicted []*APISession
	// Inspect outside the pool lock; a durable external owner must keep the
	// session resident even when this process has no local runner.
	for _, s := range candidates {
		if execution, err := s.inspectExecution(); err != nil || execution.Busy {
			continue
		}
		if now.Sub(s.lastUsedAt()) > p.idleTTL {
			evicted = append(evicted, s)
		}
	}
	for _, sess := range evicted {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownOK := true
		if sess.Runtime != nil {
			if err := sess.Runtime.Shutdown(ctx); err != nil {
				shutdownOK = false
			} else {
				sess.MCPClients = nil
			}
		} else {
			mcp.CloseClients(sess.MCPClients)
			sess.MCPClients = nil
		}
		cancel()
		if shutdownOK {
			p.mu.Lock()
			for key, current := range p.sessions {
				if current == sess {
					delete(p.sessions, key)
				}
			}
			p.mu.Unlock()
		}
	}
}

// PoolFullError is returned when the session pool is at capacity.
type PoolFullError struct {
	Max int
}

func (e *PoolFullError) Error() string {
	return "session pool is at capacity"
}

// ListActiveSessions returns persisted sessions from sessions.db, merged with
// currently active API runtime state.
func (s *Server) ListActiveSessions() []ActiveSessionInfo {
	if s == nil || s.pool == nil {
		return nil
	}
	active := s.pool.listDetails()
	if s.settings == nil || s.cfg == nil {
		return active
	}
	details, err := session.ListAllDetailed(s.settings.GetSessionDir(), session.WithMessagesOnly())
	if err != nil {
		provider.DebugLogf("list persisted sessions: %v", err)
		return active
	}
	byID := make(map[string]ActiveSessionInfo, len(active)+len(details))
	for _, item := range details {
		item := ActiveSessionInfo{
			ID:              item.ID,
			WorkDir:         item.Cwd,
			LastUsed:        item.ModTime,
			MessageCount:    item.MessageCount,
			Preview:         item.Preview,
			Title:           item.Name,
			ChannelType:     item.ChannelType,
			ChannelID:       item.ChannelID,
			ChannelLabel:    channelLabel(item.ChannelType, item.ChannelID),
			Bound:           item.ChannelType == "wechat" || item.ChannelType == "feishu",
			ParentSessionID: item.ParentSession, ForkBoundarySeq: item.ForkBoundarySeq, SeedLength: item.SeedLength, ForkKind: item.ForkKind,
		}
		if item.ChannelType == "" {
			item.ChannelType = "local"
			item.ChannelLabel = channelLabel(item.ChannelType, item.ChannelID)
		}
		if metadata, err := session.GetSessionMetadata(s.settings.GetSessionDir(), item.ID); err == nil {
			item.ProjectID, item.Pinned = metadata.ProjectID, metadata.Pinned
		}
		byID[item.ID] = item
	}
	for _, item := range active {
		persisted := byID[item.ID]
		if persisted.ID == "" {
			if metadata, err := session.GetSessionMetadata(s.settings.GetSessionDir(), item.ID); err == nil {
				item.ProjectID, item.Pinned = metadata.ProjectID, metadata.Pinned
			}
			byID[item.ID] = item
			continue
		}
		item.MessageCount = persisted.MessageCount
		if item.WorkDir == "" {
			item.WorkDir = persisted.WorkDir
		}
		if item.Preview == "" {
			item.Preview = persisted.Preview
		}
		if persisted.Title != "" {
			// The persisted session_info entry is authoritative: an active
			// runtime can still hold the title from before a user rename.
			item.Title = persisted.Title
		}
		item.ProjectID, item.Pinned = persisted.ProjectID, persisted.Pinned
		if item.ChannelType == "" {
			item.ChannelType = persisted.ChannelType
		}
		if item.ChannelID == "" {
			item.ChannelID = persisted.ChannelID
		}
		if item.ChannelLabel == "" {
			item.ChannelLabel = persisted.ChannelLabel
		}
		item.Bound = persisted.Bound || item.Bound
		byID[item.ID] = item
	}
	sessions := make([]ActiveSessionInfo, 0, len(byID))
	for _, item := range byID {
		execution, err := agentruntime.InspectSessionExecution(s.settings.GetSessionDir(), item.ID)
		if err != nil {
			provider.DebugLogf("inspect execution for session %q: %v", item.ID, err)
		}
		item.Execution = &execution
		item.Running = execution.Running
		sessions = append(sessions, item)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].LastUsed.Equal(sessions[j].LastUsed) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].LastUsed.After(sessions[j].LastUsed)
	})
	return sessions
}

// CapabilityOverview returns serve-level capability defaults and availability.
func (s *Server) CapabilityOverview() CapabilityOverview {
	defaults := s.defaultSessionCapabilities("", false, false)
	overview := CapabilityOverview{
		Modes: []string{"plan", "agent", "yolo", "os"},
		Features: map[string]CapabilityFeature{
			"delegate":   {Available: true, Default: defaults.DelegateMode},
			"multiAgent": {Available: true, Default: defaults.MultiAgent},
			"workflows":  {Available: true, Default: defaults.Workflows},
			"webSearch":  {Available: true, Default: defaults.WebSearch},
			"browser":    {Available: true, Default: defaults.Browser},
			"a2aMaster":  {Available: true, Default: defaults.A2AMaster},
			"sandbox":    {Available: true, Default: s != nil && s.cfg != nil && s.cfg.Sandbox.Enabled},
		},
		Defaults: defaults,
	}
	s.mu.RLock()
	activeProvider := s.provider
	model := s.model
	s.mu.RUnlock()
	_, basicResolver := activeProvider.(provider.AttachmentResolver)
	_, metadataResolver := activeProvider.(provider.AttachmentMetadataResolver)
	overview.AttachmentDownload = basicResolver || metadataResolver
	if p, ok := activeProvider.(*openaiprovider.Provider); ok && model != nil && p.API() == "openai-responses" {
		report := p.ResponsesCapabilityReport(model.ID)
		overview.Responses = &ResponsesCapabilityOverview{
			ModelID: report.ModelID, Provider: report.Provider, API: report.API,
			SupportsResponses:        report.SupportsResponses,
			SupportsPreviousResponse: report.SupportsPreviousResponse, SupportsConversation: report.SupportsConversation,
			SupportsBackground: report.SupportsBackground, SupportsStructuredOutput: report.SupportsStructuredOutput,
			SupportsServiceTier: report.SupportsServiceTier, SupportsParallelTools: report.SupportsParallelTools,
			SupportsToolChoice: report.SupportsToolChoice, SupportsAttachmentDownload: report.SupportsAttachmentDownload, HostedTools: report.HostedTools, HostedPolicies: report.HostedPolicies,
			SupportedInclude: report.SupportedInclude, SupportedEvents: report.SupportedEvents,
			SupportedItems: report.SupportedItems, AttachmentKinds: report.AttachmentKinds,
			SupportedAnnotations: report.SupportedAnnotations,
		}
	}
	return overview
}

// GetSessionCapabilities returns runtime capabilities for an active or persisted session.
func (s *Server) GetSessionCapabilities(id string) (*SessionCapabilities, error) {
	if id == "" {
		return nil, ErrSessionNotFound
	}
	if s != nil && s.pool != nil {
		sess, err := s.pool.getExact(id)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			caps := s.capabilitiesFromSession(sess, true, sess.Manager != nil)
			return &caps, nil
		}
	}
	if s == nil || s.settings == nil {
		return nil, ErrSessionNotFound
	}
	mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), id)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	workDir := ""
	if header := mgr.GetHeader(); header != nil {
		workDir = header.Cwd
	}
	caps := s.defaultSessionCapabilities(workDir, false, true)
	caps.ID = id
	if stored, ok, err := s.loadStoredCapabilities(id); err != nil {
		return nil, err
	} else if ok {
		applyStoredCapabilitiesToResponse(&caps, stored)
	}
	mode, err := s.resolveSessionMode(&APISession{ID: id, Manager: mgr, Mode: caps.Mode}, "")
	if err != nil {
		return nil, err
	}
	caps.Mode = mode
	return &caps, nil
}

func (s *Server) runtimeSnapshotFromCapabilities(caps *SessionCapabilities) *SessionRuntimeSnapshot {
	if caps == nil {
		return &SessionRuntimeSnapshot{Capabilities: map[string]SessionCapabilityState{}}
	}
	snapshot := &SessionRuntimeSnapshot{
		SessionID:        caps.ID,
		Mode:             caps.Mode,
		DisplayMode:      normalizedDisplayMode(caps.DisplayMode),
		Model:            caps.Model,
		ThinkingLevel:    caps.ThinkingLevel,
		WorkDir:          caps.WorkDir,
		Capabilities:     make(map[string]SessionCapabilityState, 6),
		PendingApprovals: []SessionApprovalRequest{},
		PendingQuestions: []SessionQuestionRequest{},
	}
	if snapshot.Mode == "" {
		snapshot.Mode = "yolo"
	}
	state := func(available, enabled bool, unavailableReason string) SessionCapabilityState {
		reason := ""
		if !available {
			reason = unavailableReason
		}
		if available && !enabled {
			if reason == "" {
				reason = "disabled for this session"
			}
		}
		return SessionCapabilityState{
			Available:      available,
			Enabled:        enabled,
			Effective:      available && enabled,
			DisabledReason: reason,
		}
	}
	available := func(name string) bool { return s.runtimeCapabilityAvailable(name) }
	snapshot.Capabilities["browser"] = state(available("browser"), caps.Browser, "disabled by serve config")
	snapshot.Capabilities["delegate"] = state(available("delegate"), caps.DelegateMode, "disabled by serve config")
	snapshot.Capabilities["multiAgent"] = state(available("multiAgent"), caps.MultiAgent, "disabled by serve config")
	snapshot.Capabilities["workflows"] = state(available("workflows"), caps.Workflows, "disabled by serve config")
	snapshot.Capabilities["webSearch"] = state(available("webSearch"), caps.WebSearch, "disabled by serve config")
	snapshot.Capabilities["a2aMaster"] = state(available("a2aMaster"), caps.A2AMaster, "disabled by serve config")
	if s != nil && s.settings != nil && caps.ID != "" {
		execution, _ := agentruntime.InspectSessionExecution(s.settings.GetSessionDir(), caps.ID)
		snapshot.Execution = &execution
		if execution.ActiveRun != nil {
			run := execution.ActiveRun
			snapshot.ActiveRun = &SessionActiveRun{
				RunID: run.ID, Status: run.Status, Source: run.Source, Model: run.Model,
				Mode: run.Mode, StartedAt: run.StartedAt, UpdatedAt: run.UpdatedAt,
			}
		}
	}
	if s.settings != nil && caps.ID != "" {
		if runs, err := session.ListResponseRuns(s.settings.GetSessionDir(), caps.ID, 50); err == nil {
			for i := len(runs) - 1; i >= 0; i-- {
				if isTerminalResponsesRunState(runs[i].State) {
					continue
				}
				snapshot.ResponsesRun = &SessionResponsesRun{
					LocalRunID:      runs[i].LocalRunID,
					ResponseID:      runs[i].ResponseID,
					State:           runs[i].State,
					CancelRequested: runs[i].CancelRequested,
				}
				break
			}
		}
		if caps.ID != "" {
			if esmSnapshot, err := s.GetESM(caps.ID); err == nil {
				snapshot.ESM = esmSnapshot
			}
		}
	}
	// Pending approvals are tracked in-memory and keyed by session+run.
	if s != nil && s.pool != nil && caps.ID != "" {
		if sess, err := s.pool.getExact(caps.ID); err == nil && sess != nil {
			sess.approvalMu.Lock()
			runID := sess.activeRunID
			decisionIDs := s.pendingDecisionIDsForRun(sess, runID)
			for approvalID, pending := range sess.pendingApprovals {
				if pending.Request.RunID == runID && (len(decisionIDs) == 0 || decisionIDs[approvalID] == agentruntime.DecisionApproval) {
					snapshot.PendingApprovals = append(snapshot.PendingApprovals, pending.Request)
				}
			}
			for questionID, pending := range sess.pendingQuestions {
				if pending.Request.RunID == runID && (len(decisionIDs) == 0 || decisionIDs[questionID] == agentruntime.DecisionQuestion) {
					snapshot.PendingQuestions = append(snapshot.PendingQuestions, pending.Request)
				}
			}
			sess.approvalMu.Unlock()
			if len(snapshot.PendingQuestions) == 0 {
				snapshot.PendingQuestions = append(snapshot.PendingQuestions, s.recoveredPendingQuestions(caps.ID, runID)...)
			}
		}
	}
	return snapshot
}

// resolveOrphanedQuestions records cancellation resolutions for questions whose
// local Agent run cannot survive a process restart. This keeps durable question
// state consistent with RunManager's orphan recovery policy.
func (s *Server) resolveOrphanedDecisions(run session.SessionRun) error {
	if s == nil || s.settings == nil || run.ID == "" || run.SessionID == "" {
		return nil
	}
	records, err := agentruntime.LoadRunDecisionRecords(s.settings.GetSessionDir(), run.SessionID, run.ID)
	if err != nil {
		return err
	}
	approvals := make(map[string]SessionApprovalRequest)
	questions := make(map[string]SessionQuestionRequest)
	for _, record := range records {
		switch record.Kind {
		case agentruntime.DecisionApproval:
			var request SessionApprovalRequest
			if len(record.Payload) == 0 || json.Unmarshal(record.Payload, &request) != nil || request.ApprovalID == "" {
				continue
			}
			approvals[record.ID] = request
		case agentruntime.DecisionQuestion:
			var request SessionQuestionRequest
			if len(record.Payload) == 0 || json.Unmarshal(record.Payload, &request) != nil || request.QuestionID == "" {
				continue
			}
			questions[record.ID] = request
		}
	}
	for id, record := range agentruntime.ReplayDecisions(records) {
		switch record.Kind {
		case agentruntime.DecisionApproval:
			request := approvals[id]
			if request.ApprovalID == "" {
				continue
			}
			resolution := &SessionApprovalResolution{ApprovalID: id, SessionID: run.SessionID, Action: "deny_once", Status: "cancelled", Message: "run ended when the server restarted"}
			decision := agentruntime.DecisionRequest{ID: id, SessionID: run.SessionID, RunID: run.ID, Kind: agentruntime.DecisionApproval}
			result := agentruntime.DecisionResolution{ID: id, Kind: agentruntime.DecisionApproval, Status: resolution.Status, Value: resolution.Action}
			record, err := agentruntime.NewDecisionResolutionRecord(decision, result, map[string]any{"approval": request, "resolution": resolution})
			if err != nil {
				return err
			}
			fields := agentruntime.DecisionEventEnvelope(record)
			fields["approval"] = request
			fields["resolution"] = resolution
			data, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			if _, err = (agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()}).Record(agentruntime.RunEvent{SessionID: run.SessionID, RunID: run.ID, EventType: "approval_resolved", Status: resolution.Status, Source: run.Source, Model: run.Model, Mode: run.Mode, Timestamp: time.Now(), Data: data}); err != nil {
				return err
			}
		case agentruntime.DecisionQuestion:
			request := questions[id]
			if request.QuestionID == "" {
				continue
			}
			resolution := &SessionQuestionResolution{QuestionID: id, SessionID: run.SessionID, RunID: run.ID, Status: "cancelled", Message: "run ended when the server restarted"}
			if err := s.recordSessionQuestionResolutionForRun(run, request, resolution); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveOrphanedQuestions is retained for focused compatibility tests.
func (s *Server) resolveOrphanedQuestions(run session.SessionRun) error {
	return s.resolveOrphanedDecisions(run)
}

func (s *Server) recordSessionQuestionResolutionForRun(run session.SessionRun, request SessionQuestionRequest, resolution *SessionQuestionResolution) error {
	if s == nil || s.settings == nil || resolution == nil {
		return nil
	}
	decision := agentruntime.DecisionRequest{ID: request.QuestionID, SessionID: request.SessionID, RunID: request.RunID, Kind: agentruntime.DecisionQuestion}
	result := agentruntime.DecisionResolution{ID: request.QuestionID, Kind: agentruntime.DecisionQuestion, Status: resolution.Status, Value: resolution.Answer}
	record, err := agentruntime.NewDecisionResolutionRecord(decision, result, map[string]any{"question": request, "resolution": resolution})
	if err != nil {
		return err
	}
	fields := agentruntime.DecisionEventEnvelope(record)
	fields["question"] = request
	fields["resolution"] = resolution
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = (agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()}).Record(agentruntime.RunEvent{
		SessionID: run.SessionID,
		RunID:     run.ID,
		EventType: "question_resolved",
		Status:    resolution.Status,
		Source:    run.Source,
		Model:     run.Model,
		Mode:      run.Mode,
		Timestamp: time.Now(),
		Data:      data,
	})
	return err
}
func (s *Server) recoveredPendingQuestions(sessionID, runID string) []SessionQuestionRequest {
	if s == nil || s.settings == nil || sessionID == "" || runID == "" {
		return nil
	}
	records, err := agentruntime.LoadRunDecisionRecords(s.settings.GetSessionDir(), sessionID, runID)
	if err != nil {
		return nil
	}
	questions := make(map[string]SessionQuestionRequest)
	for _, record := range records {
		if record.Kind != agentruntime.DecisionQuestion || len(record.Payload) == 0 {
			continue
		}
		var request SessionQuestionRequest
		if json.Unmarshal(record.Payload, &request) != nil || request.QuestionID == "" {
			continue
		}
		questions[record.ID] = request
	}
	pending := agentruntime.ReplayDecisions(records)
	result := make([]SessionQuestionRequest, 0, len(pending))
	for id, record := range pending {
		if record.Kind != agentruntime.DecisionQuestion {
			continue
		}
		if request, ok := questions[id]; ok {
			result = append(result, request)
		}
	}
	return result
}

func isTerminalResponsesRunState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed", "failed", "incomplete", "cancelled", "canceled", "expired":
		return true
	default:
		return false
	}
}

func (s *Server) runtimeCapabilityAvailable(name string) bool {
	if s == nil || s.cfg == nil {
		return false
	}
	switch name {
	case "delegate":
		return s.cfg.EnableDelegate
	case "multiAgent":
		return s.cfg.EnableSubAgents
	case "workflows":
		return s.cfg.EnableWorkflows
	case "webSearch":
		return s.IsWebSearchAvailable()
	case "browser":
		return s.cfg.EnableBrowser
	case "a2aMaster":
		return s.cfg.EnableA2AMaster
	default:
		return false
	}
}

// ListSessionRuns returns persisted runs for a session.
// SetSessionMetadata updates a persisted session's project assignment and pin state.
func (s *Server) SetSessionMetadata(id string, metadata session.SessionMetadata) (*ActiveSessionInfo, error) {
	if s == nil || s.settings == nil {
		return nil, ErrSessionNotFound
	}
	if _, err := session.OpenByIDExact(s.settings.GetSessionDir(), id); err != nil {
		return nil, ErrSessionNotFound
	}
	if err := session.SetSessionMetadata(s.settings.GetSessionDir(), id, metadata); err != nil {
		return nil, err
	}
	for _, item := range s.ListActiveSessions() {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, ErrSessionNotFound
}

// SetSessionTitle records a user-provided title without changing project metadata.
func (s *Server) SetSessionTitle(id, title string) (*ActiveSessionInfo, error) {
	if s == nil || s.settings == nil {
		return nil, ErrSessionNotFound
	}
	mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), id)
	if err != nil {
		return nil, err
	}
	if _, err := mgr.AppendSessionTitle(title, "manual"); err != nil {
		return nil, err
	}
	for _, item := range s.ListActiveSessions() {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, ErrSessionNotFound
}

func (s *Server) ListSessionRuns(id string, limit int) ([]session.SessionRun, error) {
	if s == nil || s.settings == nil || id == "" {
		return nil, ErrSessionNotFound
	}
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSessionNotFound
	}
	return session.ListSessionRuns(s.settings.GetSessionDir(), id, limit)
}

// GetSessionRuntime returns a structured runtime snapshot for WebUI.
func (s *Server) GetSessionRuntime(id string) (*SessionRuntimeSnapshot, error) {
	if id == "" {
		return nil, ErrSessionNotFound
	}
	caps, err := s.GetSessionCapabilities(id)
	if err != nil {
		return nil, err
	}
	return s.runtimeSnapshotFromCapabilities(caps), nil
}

func (s *Server) PatchSessionRuntime(id string, patch SessionRuntimePatch) (*SessionRuntimeSnapshot, error) {
	if id == "" {
		return nil, ErrSessionNotFound
	}
	capPatch := SessionCapabilityPatch{}
	if patch.DisplayMode != nil {
		mode := strings.TrimSpace(*patch.DisplayMode)
		if mode != "work" && mode != "code" {
			return nil, fmt.Errorf("%w: displayMode must be work or code", ErrInvalidCapability)
		}
		capPatch.DisplayMode = &mode
	}
	if patch.Mode != nil {
		capPatch.Mode = patch.Mode
	}
	if patch.Tools != nil {
		capPatch.WebSearch = patch.Tools.WebSearch
		capPatch.Browser = patch.Tools.Browser
		capPatch.A2AMaster = patch.Tools.A2AMaster
		capPatch.DelegateMode = patch.Tools.Delegate
		capPatch.MultiAgent = patch.Tools.MultiAgent
		capPatch.Workflows = patch.Tools.Workflows
	}
	for name, enabled := range patch.Capabilities {
		value := enabled
		switch name {
		case "browser":
			capPatch.Browser = &value
		case "delegate":
			capPatch.DelegateMode = &value
		case "multiAgent":
			capPatch.MultiAgent = &value
		case "workflows":
			capPatch.Workflows = &value
		case "webSearch":
			capPatch.WebSearch = &value
		case "a2aMaster":
			capPatch.A2AMaster = &value
		default:
			return nil, ErrInvalidCapability
		}
	}
	updated, err := s.PatchSessionCapabilities(id, capPatch)
	if err != nil {
		return nil, err
	}
	snapshot := s.runtimeSnapshotFromCapabilities(updated)
	snapshot.DisplayMode = updated.DisplayMode
	s.publishSessionStreamEvent(id, "runtime_event", snapshot)
	return snapshot, nil
}

// PatchSessionCapabilities activates a session if needed and updates mutable runtime capabilities.
func (s *Server) PatchSessionCapabilities(id string, patch SessionCapabilityPatch) (*SessionCapabilities, error) {
	if id == "" {
		return nil, ErrSessionNotFound
	}
	workDir, found, err := s.findSessionWorkDir(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	sess, err := s.getOrCreateSession(id, workDir)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrSessionNotFound
	}
	if !s.pool.Pin(sess) {
		return nil, ErrSessionNotFound
	}
	defer s.pool.Unpin(sess)
	sess.Lock()
	defer sess.Unlock()

	before := capabilitySnapshotFromSession(sess)
	refreshContext := false
	registryChanged := false
	if patch.Mode != nil {
		mode := strings.TrimSpace(*patch.Mode)
		if err := validateCapabilityMode(mode); err != nil {
			return nil, err
		}
		resolved, err := s.resolveSessionMode(sess, mode)
		if err != nil {
			return nil, err
		}
		sess.Mode = resolved
	}
	if patch.DisplayMode != nil {
		sess.DisplayMode = normalizedDisplayMode(*patch.DisplayMode)
	}
	if applyBoolOption(&sess.WebSearch, patch.WebSearch) {
		// Web search affects hosted tool injection at next agent construction.
	}
	if applyBoolOption(&sess.Browser, patch.Browser) {
		refreshContext = true
		registryChanged = true
	}
	if applyBoolOption(&sess.A2AMaster, patch.A2AMaster) {
		registryChanged = true
	}
	delegate := patch.DelegateMode
	if delegate == nil {
		delegate = patch.Delegate
	}
	if applyBoolOption(&sess.DelegateMode, delegate) {
		registryChanged = true
	}
	if applyBoolOption(&sess.MultiAgent, patch.MultiAgent) {
		registryChanged = true
	}
	if applyBoolOption(&sess.Workflows, patch.Workflows) {
		refreshContext = true
		registryChanged = true
	}
	// Mode and webSearch only affect agent construction/configuration. They do
	// not own registry state, so a mode-only WebUI PATCH must not re-register
	// session tools (or reinitialize optional integrations such as A2A).
	if registryChanged {
		if err := s.syncSessionTools(sess, refreshContext); err != nil {
			return nil, err
		}
	}
	if err := s.persistSessionCapabilitiesWithEvents(sess, before, "api_patch", "webui", "", map[string]any{
		"source": "session_capabilities_patch",
	}); err != nil {
		return nil, err
	}
	sess.Touch()

	caps := s.capabilitiesFromSession(sess, true, sess.Manager != nil)
	caps.RuntimeOnly = false
	caps.PersistenceNote = ""
	return &caps, nil
}

func (s *Server) loadStoredCapabilities(id string) (*session.SessionCapabilities, bool, error) {
	if s == nil || s.settings == nil || id == "" {
		return nil, false, nil
	}
	return session.LoadSessionCapabilities(s.settings.GetSessionDir(), id)
}

func (s *Server) applyStoredSessionCapabilities(sess *APISession) error {
	if sess == nil {
		return nil
	}
	stored, ok, err := s.loadStoredCapabilities(sess.ID)
	if err != nil {
		return err
	}
	oldBrowser := sess.Browser
	oldWorkflows := sess.Workflows
	if ok {
		if err := applyStoredCapabilitiesToSession(sess, stored); err != nil {
			return err
		}
	}
	mode, err := s.resolveSessionMode(sess, "")
	if err != nil {
		return err
	}
	sess.Mode = mode
	return s.syncSessionTools(sess, oldBrowser != sess.Browser || oldWorkflows != sess.Workflows)
}

func applyStoredCapabilitiesToSession(sess *APISession, stored *session.SessionCapabilities) error {
	if sess == nil || stored == nil {
		return nil
	}
	if err := validateCapabilityMode(stored.Mode); err != nil {
		return err
	}
	sess.Mode = stored.Mode
	sess.DisplayMode = normalizedDisplayMode(stored.DisplayMode)
	sess.DelegateMode = stored.DelegateMode
	sess.MultiAgent = stored.MultiAgent
	sess.Workflows = stored.Workflows
	sess.WebSearch = stored.WebSearch
	sess.Browser = stored.Browser
	sess.A2AMaster = stored.A2AMaster
	return nil
}

func applyStoredCapabilitiesToResponse(caps *SessionCapabilities, stored *session.SessionCapabilities) {
	if caps == nil || stored == nil {
		return
	}
	caps.Mode = stored.Mode
	if caps.Mode == "" {
		caps.Mode = "yolo"
	}
	caps.DelegateMode = stored.DelegateMode
	caps.Delegate = stored.DelegateMode
	caps.MultiAgent = stored.MultiAgent
	caps.Workflows = stored.Workflows
	caps.WebSearch = stored.WebSearch
	caps.Browser = stored.Browser
	caps.A2AMaster = stored.A2AMaster
	caps.DisplayMode = stored.DisplayMode
	if caps.DisplayMode != "code" {
		caps.DisplayMode = "work"
	}
	caps.RuntimeOnly = false
	caps.PersistenceNote = ""
}

func (s *Server) persistSessionCapabilities(sess *APISession) error {
	if s == nil || s.settings == nil || sess == nil || sess.ID == "" {
		return nil
	}
	return session.SaveSessionCapabilities(s.settings.GetSessionDir(), session.SessionCapabilities{
		SessionID:    sess.ID,
		Mode:         sess.Mode,
		DisplayMode:  normalizedDisplayMode(sess.DisplayMode),
		DelegateMode: sess.DelegateMode,
		MultiAgent:   sess.MultiAgent,
		Workflows:    sess.Workflows,
		WebSearch:    sess.WebSearch,
		Browser:      sess.Browser,
		A2AMaster:    sess.A2AMaster,
		UpdatedAt:    time.Now(),
	})
}

func normalizedDisplayMode(mode string) string {
	if strings.TrimSpace(mode) == "code" {
		return "code"
	}
	return "work"
}

// resolveSessionMode resolves one effective mode for session display, execution,
// records, and approvals. Bound WeChat and Feishu sessions cannot be downgraded.
func (s *Server) resolveSessionMode(sess *APISession, requestedMode string) (string, error) {
	_, mode, err := s.resolveSessionPolicy(sess, requestedMode)
	return mode, err
}

func (s *Server) resolveSessionPolicy(sess *APISession, requestedMode string) (agentruntime.SourceResolution, string, error) {
	if sess == nil {
		return agentruntime.SourceResolution{}, "", ErrSessionNotFound
	}
	var header *session.Header
	if sess.Manager != nil {
		header = sess.Manager.GetHeader()
	}
	defaultMode := ""
	if s != nil && s.cfg != nil {
		defaultMode = s.cfg.DefaultMode
	}
	if sess.Runtime != nil {
		return sess.Runtime.ResolvePolicy(sess.Mode, requestedMode, defaultMode)
	}
	var binding *session.Binding
	if s != nil && s.settings != nil && sess.ID != "" {
		var err error
		binding, err = session.FindBindingBySessionID(s.settings.GetSessionDir(), sess.ID)
		if err != nil {
			return agentruntime.SourceResolution{}, "", err
		}
	}
	return agentruntime.ResolvePolicy(agentruntime.SourceResolutionInput{
		Binding: binding, SessionHeader: header, Requested: agentruntime.SourceWebUI,
	}, sess.Mode, requestedMode, defaultMode)
}

func (s *Server) resolveSessionModeFromHeader(header *session.Header, sessionMode, requestedMode string) (string, error) {
	defaultMode := ""
	if s != nil && s.cfg != nil {
		defaultMode = s.cfg.DefaultMode
	}
	_, mode, err := agentruntime.ResolvePolicy(agentruntime.SourceResolutionInput{
		SessionHeader: header, Requested: agentruntime.SourceWebUI,
	}, sessionMode, requestedMode, defaultMode)
	return mode, err
}

func validateCapabilityMode(mode string) error {
	switch mode {
	case "", "plan", "agent", "yolo", "os":
		return nil
	default:
		return fmt.Errorf("%w: mode must be plan, agent, yolo, os, or empty string", ErrInvalidCapability)
	}
}

func (s *Server) defaultSessionCapabilities(workDir string, active bool, persisted bool) SessionCapabilities {
	mode := ""
	delegateMode := false
	workflows := false
	webSearch := false
	browser := false
	a2aMaster := false
	multiAgent := false
	if s != nil && s.cfg != nil {
		mode = s.cfg.DefaultMode
		delegateMode = s.cfg.EnableDelegate
		workflows = s.cfg.EnableWorkflows
		webSearch = s.IsWebSearchAvailable()
		browser = s.cfg.EnableBrowser
		a2aMaster = s.cfg.EnableA2AMaster
		multiAgent = s.cfg.EnableSubAgents
	}
	if mode == "" {
		mode = "yolo"
	}
	return SessionCapabilities{
		WorkDir:         workDir,
		Active:          active,
		Mode:            mode,
		DelegateMode:    delegateMode,
		Delegate:        delegateMode,
		MultiAgent:      multiAgent,
		Workflows:       workflows,
		WebSearch:       webSearch,
		Browser:         browser,
		A2AMaster:       a2aMaster,
		Model:           s.currentModelID(),
		ThinkingLevel:   s.currentThinkingLevel(),
		Persisted:       persisted,
		RuntimeOnly:     true,
		PersistenceNote: "capability changes are runtime-only until session capability persistence is implemented",
	}
}

func (s *Server) capabilitiesFromSession(sess *APISession, active bool, persisted bool) SessionCapabilities {
	if sess == nil {
		return s.defaultSessionCapabilities("", active, persisted)
	}
	caps := s.defaultSessionCapabilities(sess.WorkDir, active, persisted)
	caps.ID = sess.ID
	mode, err := s.resolveSessionMode(sess, "")
	if err == nil {
		caps.Mode = mode
	} else if sess.Mode != "" {
		caps.Mode = sess.Mode
	}
	caps.DisplayMode = normalizedDisplayMode(sess.DisplayMode)
	caps.DelegateMode = sess.DelegateMode
	caps.Delegate = sess.DelegateMode
	caps.MultiAgent = sess.MultiAgent
	caps.Workflows = sess.Workflows
	caps.WebSearch = sess.WebSearch
	caps.Browser = sess.Browser
	caps.A2AMaster = sess.A2AMaster
	if _, ok, err := s.loadStoredCapabilities(sess.ID); err == nil && ok {
		caps.RuntimeOnly = false
		caps.PersistenceNote = ""
	}
	return caps
}

func (s *Server) currentModelID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.model == nil {
		return ""
	}
	return s.model.ID
}

func (s *Server) currentThinkingLevel() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if s.cfg.DefaultThinkingLevel != "" {
		return s.cfg.DefaultThinkingLevel
	}
	if s.settings != nil {
		return s.settings.DefaultThinkingLevel
	}
	return ""
}

// DeleteActiveSession deletes one active session from persistence and the runtime pool.
func (s *Server) DeleteActiveSession(id string) (bool, error) {
	if s == nil || s.pool == nil {
		return false, nil
	}
	sess, err := s.pool.getExact(id)
	if err != nil {
		return false, err
	}
	if sess == nil {
		if s.settings == nil {
			return false, nil
		}
		_, err := session.OpenByIDExact(s.settings.GetSessionDir(), id)
		if err != nil {
			return false, nil
		}
		if err := agentruntime.DeleteSession(s.settings.GetSessionDir(), id); err != nil {
			return false, err
		}
		return true, nil
	}
	if sess.Runtime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := sess.Runtime.Shutdown(ctx); err != nil {
			cancel()
			return false, fmt.Errorf("shutdown session runtime: %w", err)
		}
		cancel()
	} else {
		mcp.CloseClients(sess.MCPClients)
		sess.MCPClients = nil
	}
	if sess.Manager != nil && sess.Manager.GetFile() != "" && s.settings != nil {
		if err := agentruntime.DeleteSession(s.settings.GetSessionDir(), sess.ID); err != nil {
			return false, err
		}
	}
	if sess.Runtime != nil {
		sess.MCPClients = nil // legacy alias is released by Runtime.
	}
	s.pool.RemoveByWorkDir(sess.WorkDir, sess.ID)

	s.mu.Lock()
	if s.defaultSessionIDs != nil {
		for workDir, defaultID := range s.defaultSessionIDs {
			if defaultID == sess.ID {
				delete(s.defaultSessionIDs, workDir)
			}
		}
	}
	s.mu.Unlock()

	return true, nil
}

// GetSessionMessages returns the message history for a persisted session.
func (s *Server) GetSessionMessages(id string) ([]SessionMessageEntry, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if s.settings != nil && id != "" {
		if _, found, err := s.findSessionWorkDir(id); err != nil {
			return nil, err
		} else if found {
			messages, err := session.ListSessionMessagesWithSeq(s.settings.GetSessionDir(), id)
			if err != nil {
				return nil, err
			}
			return sequencedMessagesToEntries(messages), nil
		}
	}
	messages, err := s.sessionMessages(id)
	if err != nil {
		return nil, err
	}
	return sessionMessagesToEntries(messages), nil
}

// GetSessionMessagesLatest returns the latest N messages for a session,
// converted to WebUI entries. hasMore reports whether older messages may remain.
func (s *Server) GetSessionMessagesLatest(id string, limit int) ([]SessionMessageEntry, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if s.settings != nil && id != "" {
		if _, found, err := s.findSessionWorkDir(id); err != nil {
			return nil, false, err
		} else if found {
			messages, err := session.ListSessionMessagesLatest(s.settings.GetSessionDir(), id, limit)
			if err != nil {
				return nil, false, err
			}
			return sequencedMessagesToEntries(messages), len(messages) >= limit, nil
		}
	}
	messages, err := s.sessionMessages(id)
	if err != nil {
		return nil, false, err
	}
	return sessionMessagesToEntries(messages), false, nil
}

// GetSessionMessagesBefore returns up to N messages with seq < beforeSeq,
// converted to WebUI entries. hasMore reports whether older messages may remain.
func (s *Server) GetSessionMessagesBefore(id string, beforeSeq int64, limit int) ([]SessionMessageEntry, bool, error) {
	if s == nil || s.pool == nil {
		return nil, false, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if s.settings != nil && id != "" {
		if _, found, err := s.findSessionWorkDir(id); err != nil {
			return nil, false, err
		} else if found {
			messages, err := session.ListSessionMessagesBefore(s.settings.GetSessionDir(), id, beforeSeq, limit)
			if err != nil {
				return nil, false, err
			}
			return sequencedMessagesToEntries(messages), len(messages) >= limit, nil
		}
	}
	// In-memory sessions have no seq cursors; nothing older to page.
	return []SessionMessageEntry{}, false, nil
}

// GetSessionToolResult returns the full persisted result for a tool call.
func (s *Server) GetSessionToolResult(id, toolCallID string) (*SessionToolResultDetail, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if toolCallID == "" {
		return nil, ErrSessionToolResultNotFound
	}
	messages, err := s.sessionMessages(id)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		if msg.SystemInjected || msg.Role != "toolResult" || msg.ToolCallID != toolCallID {
			continue
		}
		detail := &SessionToolResultDetail{
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
			Content:    toolResultText(msg),
			IsError:    msg.IsError,
		}
		if len(msg.Contents) > 0 {
			detail.Contents = cloneContentBlocks(msg.Contents)
		}
		return detail, nil
	}
	return nil, ErrSessionToolResultNotFound
}

// GetSessionSubAgents returns sub-agent statuses for an active session.
// Sessions that exist in the session DB but are not currently loaded have no
// live sub-agents, so they return an empty list instead of ErrSessionNotFound.
func (s *Server) GetSessionSubAgents(id string) ([]SessionSubAgentInfo, error) {
	if s == nil || s.pool == nil {
		return nil, ErrSessionNotFound
	}
	history := s.externalSubAgentHistoryFor(id)
	external := []SessionSubAgentInfo(nil)
	if history != nil {
		external = history.list()
	}
	sess, err := s.pool.getExact(id)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		if len(external) > 0 {
			return external, nil
		}
		if _, found, err := s.findSessionWorkDir(id); err != nil {
			return nil, err
		} else if found {
			return external, nil
		}
		return nil, ErrSessionNotFound
	}
	if sess.AgentMgr == nil {
		return external, nil
	}

	statuses := sess.AgentMgr.Statuses()
	out := make([]SessionSubAgentInfo, 0, len(statuses))
	for _, st := range statuses {
		if st.ParentID == "" {
			continue
		}
		info := SessionSubAgentInfo{
			ID:                string(st.ID),
			ParentID:          string(st.ParentID),
			MemberID:          st.MemberID,
			ExpertID:          st.ExpertID,
			MemberDisplayName: st.MemberDisplayName,
			MemberEmoji:       st.MemberEmoji,
			MemberRole:        st.MemberRole,
			Status:            st.State,
			LastResponse:      st.Result,
			Error:             st.Error,
		}
		if info.Status == "" {
			info.Status = "unknown"
		}
		if !st.StartedAt.IsZero() {
			info.StartedAt = st.StartedAt.Format(time.RFC3339)
		}
		if !st.UpdatedAt.IsZero() {
			info.UpdatedAt = st.UpdatedAt.Format(time.RFC3339)
		}
		if a, ok := sess.AgentMgr.Get(st.ID); ok {
			info.Active = true
			info.MessageCount = len(a.GetMessages())
		}
		out = append(out, info)
	}
	seen := make(map[string]struct{}, len(out))
	for _, info := range out {
		seen[info.ID] = struct{}{}
	}
	for _, info := range external {
		if _, exists := seen[info.ID]; !exists {
			out = append(out, info)
		}
	}
	return out, nil
}

// GetSessionSubAgentMessages returns the in-memory transcript for a sub-agent.
// Sessions that exist in the session DB but are not currently loaded have no
// live sub-agent transcripts, so they return an empty list instead of
// ErrSessionNotFound; the WebUI replays persisted sub-agent messages instead.
func (s *Server) GetSessionSubAgentMessages(id, agentID string) ([]SessionMessageEntry, error) {
	if s == nil || s.pool == nil {
		return nil, ErrSessionNotFound
	}
	if history := s.externalSubAgentHistoryFor(id); history != nil {
		if entries, ok := history.transcript(agentID); ok {
			return entries, nil
		}
	}
	sess, err := s.pool.getExact(id)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		if _, found, err := s.findSessionWorkDir(id); err != nil {
			return nil, err
		} else if found {
			return []SessionMessageEntry{}, nil
		}
		return nil, ErrSessionNotFound
	}
	if sess.AgentMgr == nil || agentID == "" {
		return nil, ErrSubAgentNotFound
	}
	a, ok := sess.AgentMgr.Get(agentpkg.AgentID(agentID))
	if !ok {
		if _, statusOK := sess.AgentMgr.Status(agentpkg.AgentID(agentID)); statusOK {
			return []SessionMessageEntry{}, nil
		}
		return nil, ErrSubAgentNotFound
	}
	entries := sessionMessagesToEntries(agent.MessagesFromPublic(a.GetMessages()))
	for i := range entries {
		entries[i].AgentID = agentID
		if entries[i].ID == "" {
			entries[i].ID = fmt.Sprintf("%s:%d", agentID, i)
		}
	}
	return entries, nil
}

// GetSessionRunEvents returns persisted run lifecycle events for a session.
func (s *Server) GetSessionRunEvents(id string) ([]SessionRunEventEntry, error) {
	if s == nil || s.settings == nil || id == "" {
		return nil, ErrSessionNotFound
	}
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSessionNotFound
	}
	events, err := session.ListSessionRunEventsWithSeq(s.settings.GetSessionDir(), id)
	if err != nil {
		return nil, err
	}
	out := make([]SessionRunEventEntry, 0, len(events))
	for _, item := range events {
		out = append(out, sessionRunEventToEntry(item.Event, item.Seq))
	}
	return out, nil
}

// GetSessionCapabilityEvents returns persisted capability transition events for a session.
func (s *Server) GetSessionCapabilityEvents(id string) ([]SessionCapabilityEventEntry, error) {
	if s == nil || s.settings == nil || id == "" {
		return nil, ErrSessionNotFound
	}
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSessionNotFound
	}
	events, err := session.ListSessionCapabilityEventsWithSeq(s.settings.GetSessionDir(), id)
	if err != nil {
		return nil, err
	}
	out := make([]SessionCapabilityEventEntry, 0, len(events))
	for _, item := range events {
		out = append(out, sessionCapabilityEventToEntry(item.Event, item.Seq))
	}
	return out, nil
}

func (s *Server) sessionMessages(id string) ([]provider.Message, error) {
	if id == "" {
		workDir := s.cfg.GetWorkDir()
		s.mu.RLock()
		defaultID := s.defaultSessionIDs[workDir]
		s.mu.RUnlock()
		id = defaultID
	}
	if id == "" {
		return nil, nil
	}
	if s.settings != nil {
		mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), id)
		if err == nil {
			return mgr.GetMessages(), nil
		}
	}
	sess, err := s.pool.getExact(id)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.Manager == nil {
		return nil, nil
	}
	return sess.Manager.GetMessages(), nil
}

func formatEventTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339Nano)
}

func decodeEventData(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil || len(data) == 0 {
		return nil
	}
	return data
}

func sessionRunEventToEntry(ev session.SessionRunEvent, seq int64) SessionRunEventEntry {
	return SessionRunEventEntry{
		Seq:       seq,
		ID:        ev.ID,
		SessionID: ev.SessionID,
		RunID:     ev.RunID,
		EventType: ev.EventType,
		Source:    ev.Source,
		Status:    ev.Status,
		Model:     ev.Model,
		Mode:      ev.Mode,
		Timestamp: formatEventTimestamp(ev.Timestamp),
		Data:      decodeEventData(ev.Data),
	}
}

func sessionCapabilityEventToEntry(ev session.SessionCapabilityEvent, seq int64) SessionCapabilityEventEntry {
	return SessionCapabilityEventEntry{
		Seq:        seq,
		ID:         ev.ID,
		SessionID:  ev.SessionID,
		RunID:      ev.RunID,
		EventType:  ev.EventType,
		Source:     ev.Source,
		Actor:      ev.Actor,
		Capability: ev.Capability,
		OldValue:   ev.OldValue,
		NewValue:   ev.NewValue,
		Timestamp:  formatEventTimestamp(ev.Timestamp),
		Data:       decodeEventData(ev.Data),
	}
}

func sessionMessagesToEntries(msgs []provider.Message) []SessionMessageEntry {
	var entries []SessionMessageEntry
	for _, m := range msgs {
		entries = append(entries, providerMessageToSessionEntries(m, 0, "")...)
	}
	return entries
}

func sequencedMessagesToEntries(msgs []session.SequencedMessage) []SessionMessageEntry {
	var entries []SessionMessageEntry
	for _, item := range msgs {
		entries = append(entries, providerMessageToSessionEntries(item.Message, item.Seq, item.EntryID)...)
	}
	return entries
}

// providerMessageToSessionEntries projects one provider message into serve's
// API history entries. The canonical transcript keeps tool calls inside the
// single assistant entry (session.appendRunAssistantMessageTx); splitting them
// into Role:"toolCall" entries here is a serve rendering projection only, and
// replay consistency between the two shapes is repaired by the provider
// codecs' tool-result sequencing.
func providerMessageToSessionEntries(m provider.Message, seq int64, entryID string) []SessionMessageEntry {
	var entries []SessionMessageEntry
	if m.SystemInjected {
		return entries
	}
	entryIDFor := func(suffix string) string {
		if entryID == "" {
			return ""
		}
		if suffix == "" {
			return entryID
		}
		return entryID + ":" + suffix
	}
	withCursor := func(entry SessionMessageEntry, suffix string) SessionMessageEntry {
		entry.ID = entryIDFor(suffix)
		entry.Seq = seq
		return entry
	}
	switch m.Role {
	case "user":
		content := messageText(m)
		entry := SessionMessageEntry{Role: m.Role, Content: content}
		if len(m.Contents) > 0 {
			entry.Contents = cloneContentBlocks(m.Contents)
		}
		entries = append(entries, withCursor(entry, ""))
	case "assistant":
		content := messageText(m)
		hasThinking := false
		for _, block := range m.Contents {
			if block.Type == "thinking" && block.Thinking != "" {
				hasThinking = true
				break
			}
		}
		if content != "" || len(m.Attachments) > 0 || hasThinking {
			entries = append(entries, withCursor(SessionMessageEntry{
				Role:        m.Role,
				Content:     content,
				Contents:    cloneContentBlocks(m.Contents),
				Attachments: append([]provider.Attachment(nil), m.Attachments...),
			}, "assistant"))
		}
		for idx, block := range m.Contents {
			if block.ToolCall == nil {
				continue
			}
			suffix := fmt.Sprintf("tool:%d", idx)
			if block.ToolCall.ID != "" {
				suffix = "tool:" + block.ToolCall.ID
			}
			entries = append(entries, withCursor(SessionMessageEntry{
				Role:        "toolCall",
				ToolCallID:  block.ToolCall.ID,
				ToolName:    block.ToolCall.Name,
				Arguments:   validRawMessage(block.ToolCall.Arguments),
				InvalidArgs: block.ToolCall.InvalidArguments,
				Plan:        planFromToolCall(block.ToolCall.Name, block.ToolCall.Arguments),
			}, suffix))
		}
	case "toolResult":
		suffix := "toolResult"
		if m.ToolCallID != "" {
			suffix += ":" + m.ToolCallID
		}
		entries = append(entries, withCursor(SessionMessageEntry{
			Role:       "toolResult",
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			IsError:    m.IsError,
			Summary:    summarizeToolResult(m),
			HasDetail:  true,
		}, suffix))
	}
	return entries
}

func messageText(msg provider.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var content string
	for _, b := range msg.Contents {
		if b.Type == "text" && b.Text != "" {
			content += b.Text
		}
	}
	return content
}

func toolResultText(msg provider.Message) string {
	text := messageText(msg)
	if text != "" {
		return text
	}
	if len(msg.Contents) > 0 {
		return "(rich tool result)"
	}
	return ""
}

func summarizeToolResult(msg provider.Message) string {
	text := strings.TrimSpace(toolResultText(msg))
	if text == "" {
		text = "(empty result)"
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	return util.TruncateWithSuffix(text, 140, "...")
}

func planFromToolCall(toolName string, args json.RawMessage) *SessionTaskPlan {
	if toolName != "plan" || len(args) == 0 || !json.Valid(args) {
		return nil
	}
	var raw struct {
		Title string `json:"title"`
		Steps []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"steps"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(args, &raw); err != nil || len(raw.Steps) == 0 {
		return nil
	}
	plan := &SessionTaskPlan{
		Title: strings.TrimSpace(raw.Title),
		Note:  strings.TrimSpace(raw.Note),
		Steps: make([]SessionPlanStep, 0, len(raw.Steps)),
	}
	for _, step := range raw.Steps {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			continue
		}
		status := normalizeSessionPlanStatus(step.Status)
		if status == "" {
			status = "pending"
		}
		plan.Steps = append(plan.Steps, SessionPlanStep{Title: title, Status: status})
	}
	if len(plan.Steps) == 0 {
		return nil
	}
	return plan
}

func normalizeSessionPlanStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "running", "done", "failed":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func validRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneContentBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	cloned := make([]provider.ContentBlock, len(blocks))
	for i, block := range blocks {
		cloned[i] = block
		if block.Image != nil {
			image := *block.Image
			cloned[i].Image = &image
		}
		if block.Audio != nil {
			audio := *block.Audio
			cloned[i].Audio = &audio
		}
		if block.Video != nil {
			video := *block.Video
			cloned[i].Video = &video
		}
		if block.File != nil {
			file := *block.File
			cloned[i].File = &file
		}
		if block.ToolCall != nil {
			toolCall := *block.ToolCall
			cloned[i].ToolCall = &toolCall
		}
		if block.CacheControl != nil {
			cacheControl := *block.CacheControl
			cloned[i].CacheControl = &cacheControl
		}
	}
	return cloned
}

func channelLabel(channelType, channelID string) string {
	switch channelType {
	case "wechat":
		return "WeChat"
	case "feishu":
		return "Feishu"
	default:
		return "Local"
	}
}

// buildAgentOptionsForSession returns Runtime-owned Agent inputs for a session.
// The adapter may add per-request prompt or approval hooks, but it does not
// assemble an internal agent.Config.
func (s *Server) buildAgentOptionsForSession(sess *APISession, model *provider.Model, mode string) agentruntime.AgentBuildOptions {
	extraContext := sess.ExtraContext
	if extraContext == "" {
		extraContext = s.extraContext
	}
	runtimeSettings := s.settingsForSession(sess)

	thinkingLevel := provider.ThinkingLevel(s.cfg.DefaultThinkingLevel)
	if thinkingLevel == "" {
		thinkingLevel = provider.ThinkingLevel(s.settings.DefaultThinkingLevel)
	}

	return agentruntime.AgentBuildOptions{
		Provider: s.provider, ProviderName: s.providerName, Model: model,
		Mode: mode, ThinkingLevel: thinkingLevel, MaxTokens: agent.ResolveMaxTokens(model), MaxTokensSet: true,
		Settings: runtimeSettings, Allow: s.getAllow(), ExtraContext: extraContext,
		RuleContent: sess.RuleContent, MultiAgent: sess.MultiAgent,
		DelegateMode: sess.DelegateMode, Workflows: sess.Workflows,
		GetSteeringMessages: s.esmSteeringMessages(sess.ID),
	}
}
