package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/imageproc"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

// contextKey is an unexported type for context keys defined in this package.
type contextKey int

const (
	defaultToolExecutionTimeout = 5 * time.Minute
)

const (
	// agentIDKey is the context key for the current agent's ID.
	agentIDKey contextKey = iota
	// agentEventChanKey is the context key for the current agent's event channel.
	agentEventChanKey
	// agentEventSinkKey is the context key for the run's race-free event sink.
	agentEventSinkKey
	// parentRunContextKey carries the parent agent run context through tool timeouts.
	parentRunContextKey
	// parentModeKey carries the parent agent's execution mode (plan/agent/yolo/os) for sub-agent inheritance.
	parentModeKey
)

// ContextWithAgentID returns a new context with the agent ID attached.
func ContextWithAgentID(ctx context.Context, id agentpkg.AgentID) context.Context {
	return context.WithValue(ctx, agentIDKey, id)
}

// AgentIDFromContext extracts the agent ID from the context.
func AgentIDFromContext(ctx context.Context) (agentpkg.AgentID, bool) {
	id, ok := ctx.Value(agentIDKey).(agentpkg.AgentID)
	return id, ok
}

// ContextWithEventChan returns a new context with the event channel attached.
func ContextWithEventChan(ctx context.Context, ch chan<- Event) context.Context {
	return context.WithValue(ctx, agentEventChanKey, ch)
}

// EventChanFromContext extracts the event channel from the context.
func EventChanFromContext(ctx context.Context) (chan<- Event, bool) {
	ch, ok := ctx.Value(agentEventChanKey).(chan<- Event)
	return ch, ok
}

// eventSink mediates event delivery into a run's event channel. The run
// goroutine owns and closes the channel when it finishes, but asynchronously
// spawned child agents may still be forwarding events at that moment; a bare
// send on a concurrently closed channel is a data race (and panics without a
// recover). Sends go through the sink so they can never race with the close:
// seal wakes blocked senders before waiting for in-flight ones, after which
// the underlying channel can be closed safely.
type eventSink struct {
	mu     sync.RWMutex
	ch     chan Event
	sealCh chan struct{}
	closed bool
}

func newEventSink(ch chan Event) *eventSink {
	return &eventSink{ch: ch, sealCh: make(chan struct{})}
}

// send forwards ev unless the sink is sealed, ctx is done, or sealing begins.
func (s *eventSink) send(ctx context.Context, ev Event) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	case <-ctx.Done():
		return false
	case <-s.sealCh:
		return false
	}
}

// seal stops accepting sends: blocked senders are woken first, then seal waits
// for in-flight sends to complete. Once seal returns, the caller may safely
// close the underlying channel.
func (s *eventSink) seal() {
	close(s.sealCh)
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// contextWithEventSink attaches the run event sink to a context so child-agent
// forwarding can use the race-free send path.
func contextWithEventSink(ctx context.Context, s *eventSink) context.Context {
	return context.WithValue(ctx, agentEventSinkKey, s)
}

// eventSinkFromContext extracts the run event sink from a context.
func eventSinkFromContext(ctx context.Context) (*eventSink, bool) {
	s, ok := ctx.Value(agentEventSinkKey).(*eventSink)
	return s, ok
}

// ContextWithParentRunContext attaches the parent agent run context to a tool context.
func ContextWithParentRunContext(ctx context.Context, parent context.Context) context.Context {
	return context.WithValue(ctx, parentRunContextKey, parent)
}

// ParentRunContextFromContext extracts the parent agent run context.
func ParentRunContextFromContext(ctx context.Context) (context.Context, bool) {
	parent, ok := ctx.Value(parentRunContextKey).(context.Context)
	return parent, ok
}

// ContextWithParentMode attaches the parent agent's execution mode to the context.
func ContextWithParentMode(ctx context.Context, mode string) context.Context {
	return context.WithValue(ctx, parentModeKey, mode)
}

// ParentModeFromContext extracts the parent agent's execution mode.
func ParentModeFromContext(ctx context.Context) (string, bool) {
	mode, ok := ctx.Value(parentModeKey).(string)
	return mode, ok
}

// Config holds the agent configuration.
type Config struct {
	ID               agentpkg.AgentID
	ParentID         agentpkg.AgentID
	Provider         provider.Provider
	Vendor           string // user-configured provider/vendor name (e.g. "longcat", "openai")
	Model            *provider.Model
	Mode             string // "plan", "agent", "yolo", "os"
	ThinkingLevel    provider.ThinkingLevel
	MaxTokens        int
	MaxTokensUserSet bool
	SandboxMgr       *sandbox.Manager
	Settings         *config.Settings
	Allow            *config.AllowConfig // auto-approval (allow.json): autoEdit, editPaths, bash rules
	Session          *session.Manager
	ExtraContext     string // extra context from files and skills
	RuleContent      string // content of .mothx/rule.md (project rules)
	// ExpertIdentity is the runtime-injected expert persona overlay and
	// ExpertRoster the team roster + dispatch rules section. Both are
	// authoritative runtime content (empty when no expert is bound) rendered
	// between project rules and project context.
	ExpertIdentity     string
	ExpertRoster       string
	CompactionSettings ctxpkg.CompactionSettings
	ApprovalHandler    func(toolCallID, toolName string, args map[string]any) bool
	// ApprovalDecisionLookup supplies a durable decision for a previously
	// requested tool approval. It is used by recovery paths before creating a
	// new interactive approval request.
	ApprovalDecisionLookup func(toolCallID, toolName string, args map[string]any) (approved bool, found bool)
	MultiAgent             bool // Decision 8: multi-agent mode
	DelegateMode           bool // blocking single sub-agent delegation mode
	Workflows              bool // dynamic workflow orchestration mode
	ConversationTurnID     string
	IntentID               string
	RunID                  string
	ConversationTurn       bool
	RuntimeOwnsTurnEnd     bool
	RuntimeOwnsUserEntry   bool
	UserEntryID            string
}

// AgentLoopConfig extends Config with loop-specific settings.
type AgentLoopConfig struct {
	Config

	// ForcedMode is an already-resolved Runtime invariant inherited by managed
	// children. Agent Core does not interpret its source.
	ForcedMode string

	// ToolExecutionMode determines how tool calls are executed.
	// "sequential": execute one by one
	// "parallel": execute concurrently (default)
	ToolExecutionMode string

	// MaxToolConcurrency bounds the number of local tool calls that may be in
	// flight for one tool-call batch. Non-positive values use the shared
	// default from config.
	MaxToolConcurrency int

	// MaxIterations is the safety limit for agent loop iterations.
	MaxIterations int

	// GetSteeringMessages returns messages to inject mid-run.
	GetSteeringMessages func() []provider.Message

	// GetFollowUpMessages returns messages to process after the agent would stop.
	// It may block while adapter-owned work is still running (for example a team
	// lead must wait for its members); a non-empty result keeps the run open and
	// injects the messages instead of finishing.
	GetFollowUpMessages func(ctx context.Context) []provider.Message

	// ShouldStopAfterTurn is called after each turn to check if we should stop.
	ShouldStopAfterTurn func(ctx ShouldStopAfterTurnContext) bool

	// PrepareNextTurn is called before the next turn to update context/model.
	PrepareNextTurn func(ctx PrepareNextTurnContext) *TurnUpdate

	// BeforeToolCall is called before a tool is executed.
	BeforeToolCall func(ctx BeforeToolCallContext) *ToolCallBlockResult

	// BeforeToolExecute is called after approval and durable execution claim,
	// immediately before the tool receives its context. Runtime uses this late
	// fence to revalidate cross-process execution ownership.
	BeforeToolExecute func(ctx BeforeToolExecuteContext) *ToolCallBlockResult

	// AfterToolCall is called after a tool finishes executing.
	AfterToolCall func(ctx AfterToolCallContext) *ToolCallResult

	// ContextPressureThreshold is the context usage percentage (0-1) that triggers EventContextPressure.
	// 0 means disabled. Default: 0.55 (55%).
	ContextPressureThreshold float64

	// BudgetPressureThreshold is the remaining iteration ratio (0-1) that triggers EventBudgetPressure.
	// 0 means disabled. Default: 0.20 (remaining 20%).
	BudgetPressureThreshold float64

	// MaxConsecutiveNoText is the max tool-only turns before a stuck-detection warning.
	// 0 means default (95).
	MaxConsecutiveNoText int
}

// ShouldStopAfterTurnContext is passed to ShouldStopAfterTurn.
type ShouldStopAfterTurnContext struct {
	Message     provider.Message
	ToolResults []provider.Message
	Context     *AgentContext
	NewMessages []provider.Message
}

// PrepareNextTurnContext is passed to PrepareNextTurn.
type PrepareNextTurnContext struct {
	ShouldStopAfterTurnContext
}

// TurnUpdate is returned from PrepareNextTurn.
type TurnUpdate struct {
	Context       *AgentContext
	Model         *provider.Model
	ThinkingLevel provider.ThinkingLevel
}

// BeforeToolCallContext is passed to BeforeToolCall.
type BeforeToolCallContext struct {
	AssistantMessage provider.Message
	ToolCall         provider.ToolCallBlock
	Args             any
	Context          *AgentContext
}

// BeforeToolExecuteContext is passed to BeforeToolExecute after any approval
// wait and durable idempotency claim have completed.
type BeforeToolExecuteContext struct {
	ToolCall         provider.ToolCallBlock
	Args             any
	Context          *AgentContext
	ExecutionContext context.Context
	RunID            string
	ExecutionKey     string
	SideEffecting    bool
}

// ToolCallBlockResult is returned from BeforeToolCall.
type ToolCallBlockResult struct {
	Block  bool
	Reason string
}

// AfterToolCallContext is passed to AfterToolCall.
type AfterToolCallContext struct {
	AssistantMessage provider.Message
	ToolCall         provider.ToolCallBlock
	Args             any
	Result           ToolCallResult
	IsError          bool
	Context          *AgentContext
}

// ToolCallResult represents the result of a tool call.
type ToolCallResult struct {
	Content   string
	IsError   bool
	Terminate bool
}

// AgentContext holds the current agent context.
type AgentContext struct {
	SystemPrompt string
	Messages     []provider.Message
	Tools        []provider.ToolDefinition
}

func cloneAgentContext(ctx *AgentContext) *AgentContext {
	if ctx == nil {
		return nil
	}
	return &AgentContext{
		SystemPrompt: ctx.SystemPrompt,
		Messages:     cloneMessages(ctx.Messages),
		Tools:        append([]provider.ToolDefinition(nil), ctx.Tools...),
	}
}

func cloneMessages(messages []provider.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]provider.Message, len(messages))
	for i, msg := range messages {
		cloned[i] = cloneMessage(msg)
	}
	return cloned
}

func cloneMessagesWithoutUsage(messages []provider.Message) []provider.Message {
	cloned := cloneMessages(messages)
	for i := range cloned {
		cloned[i].Usage = nil
	}
	return cloned
}

func cloneMessage(msg provider.Message) provider.Message {
	cloned := msg
	if len(msg.Contents) > 0 {
		cloned.Contents = make([]provider.ContentBlock, len(msg.Contents))
		for i, block := range msg.Contents {
			cloned.Contents[i] = cloneContentBlock(block)
		}
	}
	if msg.Usage != nil {
		usage := *msg.Usage
		cloned.Usage = &usage
	}
	return cloned
}

func cloneContentBlock(block provider.ContentBlock) provider.ContentBlock {
	cloned := block
	if block.Image != nil {
		image := *block.Image
		cloned.Image = &image
	}
	if block.ToolCall != nil {
		toolCall := *block.ToolCall
		toolCall.Arguments = append([]byte(nil), block.ToolCall.Arguments...)
		cloned.ToolCall = &toolCall
	}
	if block.CacheControl != nil {
		cacheControl := *block.CacheControl
		cloned.CacheControl = &cacheControl
	}
	return cloned
}

func normalizeToolCallArguments(tc *provider.ToolCallBlock) (map[string]any, error) {
	if tc == nil || len(tc.Arguments) == 0 {
		return nil, nil
	}
	var args map[string]any
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		if tc.InvalidArguments == "" {
			tc.InvalidArguments = string(tc.Arguments)
		}
		tc.Arguments = json.RawMessage(`{}`)
		return nil, err
	}
	return args, nil
}

// Agent is the core agent loop.
type Agent struct {
	id       agentpkg.AgentID
	parentID agentpkg.AgentID
	config   AgentLoopConfig
	registry *tools.Registry
	mu       sync.RWMutex
	context  *AgentContext
	// runCtx is the context of the run currently producing events. It lets
	// event sends stop as soon as a run is cancelled instead of parking on a
	// full channel whose consumer already stopped reading. It is stored without
	// a.mu because sendEvent is called while the loop already holds the message
	// lock (steering injection), and a re-entrant RLock would deadlock.
	runCtx atomic.Pointer[context.Context]
	// droppedEvents counts events skipped because the run context finished while
	// the consumer was no longer reading. Reported once when the run ends.
	droppedEvents        atomic.Int64
	abort                chan struct{}
	abortOnce            sync.Once
	messages             []provider.Message
	messageIDs           []string
	isStreaming          bool
	conversationTurnID   string
	conversationTurnOpen bool
	// The final assistant message is staged here when Runtime owns turn end;
	// terminal persistence commits it with the Run/turn boundary.
	lastAssistantEntryID string
	lastAssistantMessage provider.Message

	// Frozen system prompt and tools (built once, never change during session)
	// This is critical for prompt cache optimization - see LLM_Agent_Cache.md
	frozenSystemPrompt string
	frozenToolDefs     []provider.ToolDefinition
	frozenToolNames    []string

	// Approval mechanism for agent mode
	pendingApprovals map[string]chan bool // approvalID -> response channel
	approvalMu       sync.Mutex
	approvalCounter  int64

	// Question mechanism for plan mode
	pendingQuestions map[string]chan string // questionID -> response channel
	questionMu       sync.Mutex
	questionCounter  int64

	// Force compaction flag, consumed before the next request is built.
	forceCompact int32 // atomic: 0=false, 1=true
}

type conversationTurnStore interface {
	StartConversationTurn(turnID, intentID, runID string) error
	EndConversationTurn(turnID, status, stopReason string) error
}

// SetConversationTurn binds a reusable Agent instance to the durable Run it
// is about to execute. TUI keeps one Agent across prompts, while other
// adapters usually provide these values at construction time.
func (a *Agent) SetConversationTurn(turnID, intentID, runID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.config.ConversationTurnID = turnID
	a.config.IntentID = intentID
	a.config.RunID = runID
	a.config.RuntimeOwnsTurnEnd = true
	a.config.RuntimeOwnsUserEntry = true
	a.config.UserEntryID = session.RunUserEntryID(runID)
	a.conversationTurnID = ""
	a.conversationTurnOpen = false
	a.lastAssistantEntryID = ""
	a.lastAssistantMessage = provider.Message{}
	a.mu.Unlock()
}

// buildFrozenPrompt builds the system prompt and tools once at construction time.
// These values are frozen for the entire session lifetime to maximize prompt cache hits.
// This implements Rule R2.1 from LLM_Agent_Cache.md: System prompt must be built once and never modified.
func (a *Agent) buildFrozenPrompt() {
	if a.registry == nil {
		a.frozenSystemPrompt = ""
		a.frozenToolDefs = nil
		a.frozenToolNames = nil
		return
	}
	toolDefs := a.registry.ModeTools(a.config.Mode)
	if t, ok := configuredWebSearchToolDefinition(a.config.Settings); ok {
		toolDefs = append(toolDefs, t)
	}
	if t, ok := openAIResponsesWebSearchToolDefinition(a.config.Provider); ok {
		toolDefs = append(toolDefs, t)
	}
	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		if t.Kind == "hosted" {
			continue
		}
		toolNames = append(toolNames, t.Name)
	}
	toolSnippets := a.registry.ToolSnippets(toolNames)
	toolGuidelines := a.registry.ToolGuidelines(toolNames)
	a.frozenSystemPrompt = BuildSystemPromptWithOptions(
		a.config.Mode,
		toolNames,
		a.registry.GetWorkDir(),
		a.config.RuleContent,
		a.config.ExtraContext,
		toolSnippets,
		toolGuidelines,
		a.config.MultiAgent,
		a.config.DelegateMode,
		a.config.Workflows,
		SystemPromptOptions{
			ToolExecutionMode:  a.config.ToolExecutionMode,
			MaxToolConcurrency: a.config.MaxToolConcurrency,
			Authored:           a.config.Settings != nil && a.config.Settings.Authored,
			ExpertIdentity:     a.config.ExpertIdentity,
			ExpertRoster:       a.config.ExpertRoster,
		},
	)
	a.frozenToolDefs = toolDefs
	a.frozenToolNames = toolNames
}

func imageGenerationToolDefinition(settings *config.Settings, providerName string) (provider.ToolDefinition, bool) {
	if settings == nil {
		return provider.ToolDefinition{}, false
	}
	if providerName == "" {
		providerName = settings.DefaultProvider
	}
	if providerName == "" {
		providerName = "openai"
	}
	pc := settings.GetProviderConfig(providerName)
	if pc == nil {
		pc = config.DefaultProviderConfig(providerName)
	}
	if pc == nil {
		return provider.ToolDefinition{}, false
	}
	resolved := provider.ResolveAdapterConfig(pc)
	if resolved.API != "responses" && resolved.API != "openai-responses" {
		return provider.ToolDefinition{}, false
	}
	return provider.ToolDefinition{
		Name:         provider.HostedToolImageGeneration,
		Kind:         "hosted",
		Provider:     providerName,
		ProviderType: resolved.API,
	}, true
}

func configuredWebSearchToolDefinition(settings *config.Settings) (provider.ToolDefinition, bool) {
	if settings == nil || !settings.IsWebSearchEnabled() {
		return provider.ToolDefinition{}, false
	}
	cfg := settings.WebSearch
	providerName := cfg.Provider
	if providerName == "" {
		providerName = settings.DefaultProvider
	}
	if providerName == "" {
		providerName = "openai"
	}

	resolved := provider.AdapterConfig{}
	if pc := settings.GetProviderConfig(providerName); pc != nil {
		resolved = provider.ResolveAdapterConfig(pc)
	} else {
		resolved = provider.ResolveAdapterConfig(&config.ProviderConfig{API: "openai-chat"})
		switch providerName {
		case "anthropic":
			resolved.API = "anthropic-messages"
		case "openai":
			resolved.API = "openai-responses"
		}
	}

	providerType := cfg.ProviderType
	if providerType == "" {
		providerType = resolved.API
	}
	switch providerType {
	case "responses":
		providerType = "openai-responses"
	case "messages":
		providerType = "anthropic-messages"
	}

	return provider.ToolDefinition{
		Name:         provider.HostedToolWebSearch,
		Kind:         "hosted",
		Provider:     providerName,
		ProviderType: providerType,
		Model:        cfg.Model,
	}, true
}

func openAIResponsesWebSearchToolDefinition(p provider.Provider) (provider.ToolDefinition, bool) {
	if p == nil || (p.API() != "responses" && p.API() != "openai-responses") {
		return provider.ToolDefinition{}, false
	}
	return provider.ToolDefinition{
		Name:         provider.HostedToolOpenAIResponsesWebSearch,
		Kind:         "hosted",
		Provider:     p.Name(),
		ProviderType: p.API(),
	}, true
}

// New creates a new agent.
func New(cfg Config, registry *tools.Registry) *Agent {
	cfg.CompactionSettings = ctxpkg.NormalizeCompactionSettings(cfg.CompactionSettings)
	configureRegistryImageHint(cfg, registry)
	toolExecutionMode := "parallel"
	maxToolConcurrency := config.DefaultToolExecutionMaxConcurrency
	if cfg.Settings != nil {
		toolExecutionMode = cfg.Settings.ToolExecution.EffectiveMode()
		maxToolConcurrency = cfg.Settings.ToolExecution.EffectiveMaxConcurrency()
	}
	loopConfig := AgentLoopConfig{
		Config:             cfg,
		ToolExecutionMode:  toolExecutionMode,
		MaxToolConcurrency: maxToolConcurrency,
		MaxIterations:      200,
	}

	id := cfg.ID
	if id == "" {
		id = agentpkg.AgentID(fmt.Sprintf("agent-%d", time.Now().UnixNano()))
	}

	agent := &Agent{
		id:               id,
		parentID:         cfg.ParentID,
		config:           loopConfig,
		registry:         registry,
		abort:            make(chan struct{}),
		pendingApprovals: make(map[string]chan bool),
		pendingQuestions: make(map[string]chan string),
		context: &AgentContext{
			Messages: make([]provider.Message, 0),
		},
	}
	// Build frozen system prompt once at construction time (R2.1)
	agent.buildFrozenPrompt()
	agent.context.SystemPrompt = agent.frozenSystemPrompt
	agent.context.Tools = agent.frozenToolDefs
	return agent
}

// NewWithLoopConfig creates a new agent with custom loop configuration.
func NewWithLoopConfig(cfg AgentLoopConfig, registry *tools.Registry) *Agent {
	// ForcedMode is resolved by the front-end-neutral Runtime and inherited by
	// managed children. Normalize Config.Mode before building the frozen prompt
	// so mode-dependent tools and system instructions cannot use a downgraded
	// adapter request.
	if forced := strings.TrimSpace(cfg.ForcedMode); forced != "" {
		cfg.Mode = forced
	}
	cfg.CompactionSettings = ctxpkg.NormalizeCompactionSettings(cfg.CompactionSettings)
	configureRegistryImageHint(cfg.Config, registry)
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 200
	}
	if cfg.ToolExecutionMode == "" {
		if cfg.Settings != nil {
			cfg.ToolExecutionMode = cfg.Settings.ToolExecution.EffectiveMode()
		} else {
			cfg.ToolExecutionMode = "parallel"
		}
	}
	if cfg.MaxToolConcurrency <= 0 {
		if cfg.Settings != nil {
			cfg.MaxToolConcurrency = cfg.Settings.ToolExecution.EffectiveMaxConcurrency()
		}
		if cfg.MaxToolConcurrency <= 0 {
			cfg.MaxToolConcurrency = config.DefaultToolExecutionMaxConcurrency
		}
	}

	id := cfg.ID
	if id == "" {
		id = agentpkg.AgentID(fmt.Sprintf("agent-%d", time.Now().UnixNano()))
	}

	agent := &Agent{
		id:               id,
		parentID:         cfg.ParentID,
		config:           cfg,
		registry:         registry,
		abort:            make(chan struct{}),
		pendingApprovals: make(map[string]chan bool),
		pendingQuestions: make(map[string]chan string),
		context: &AgentContext{
			Messages: make([]provider.Message, 0),
		},
	}
	// Build frozen system prompt once at construction time (R2.1)
	agent.buildFrozenPrompt()
	agent.context.SystemPrompt = agent.frozenSystemPrompt
	agent.context.Tools = agent.frozenToolDefs
	return agent
}

// MaxToolConcurrency returns the normalized per-batch local tool limit.
func (a *Agent) MaxToolConcurrency() int {
	if a == nil || a.config.MaxToolConcurrency <= 0 {
		return config.DefaultToolExecutionMaxConcurrency
	}
	return a.config.MaxToolConcurrency
}

func configureRegistryImageHint(cfg Config, registry *tools.Registry) {
	if registry == nil {
		return
	}
	hint := imageproc.Hint{
		ProviderID: cfg.Vendor,
	}
	if cfg.Provider != nil {
		hint.ProviderName = cfg.Provider.Name()
		hint.API = cfg.Provider.API()
		if hint.ProviderID == "" {
			hint.ProviderID = cfg.Provider.Name()
		}
	}
	if cfg.Model != nil {
		hint.ModelID = cfg.Model.ID
		if hint.ProviderID == "" {
			hint.ProviderID = cfg.Model.Provider
		}
	}
	if cfg.Settings != nil {
		providerKey := hint.ProviderID
		if providerKey == "" {
			providerKey = cfg.Settings.DefaultProvider
		}
		if pc := cfg.Settings.GetProviderConfig(providerKey); pc != nil {
			hint.Vendor = pc.Vendor
			hint.BaseURL = pc.BaseURL
			if hint.API == "" {
				hint.API = pc.API
			}
		}
	}
	registry.SetImageHint(hint)
}

// LoadHistoryMessages loads historical messages from session into agent context.
func (a *Agent) LoadHistoryMessages(messages []provider.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loadHistoryStateLocked(messages, nil)
}

// LoadHistoryState loads historical messages plus their session entry IDs.
func (a *Agent) LoadHistoryState(messages []provider.Message, entryIDs []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.loadHistoryStateLocked(messages, entryIDs)
}

func (a *Agent) loadHistoryStateLocked(messages []provider.Message, entryIDs []string) {
	a.messages = append(a.messages, messages...)
	a.context.Messages = append(a.context.Messages, messages...)
	if len(entryIDs) == len(messages) {
		a.messageIDs = append(a.messageIDs, append([]string(nil), entryIDs...)...)
		return
	}
	a.messageIDs = append(a.messageIDs, make([]string, len(messages))...)
}

// Abort signals the agent to stop processing.
// Satisfies both internal and public agent.Agent interface.
func (a *Agent) Abort() {
	a.abortOnce.Do(func() {
		close(a.abort)
	})
}

// Aborted reports whether Abort has been called on this Agent instance. Abort
// is one-shot and permanent: once the abort channel is closed the instance can
// never start another run. Callers that cache an Agent (for example a
// front-end reusing it across prompts) must discard it after an abort instead
// of reusing it, otherwise every later run would be canceled immediately.
func (a *Agent) Aborted() bool {
	select {
	case <-a.abort:
		return true
	default:
		return false
	}
}

func (a *Agent) callbackSnapshot() ([]provider.Message, *AgentContext) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneMessages(a.messages), cloneAgentContext(a.context)
}

func (a *Agent) agentEndEvent() Event {
	a.mu.RLock()
	defer a.mu.RUnlock()
	m := make([]provider.Message, len(a.messages))
	copy(m, a.messages)
	return Event{Type: EventAgentEnd, Messages: m}
}

// emitRunFinished sends the single canonical terminal event for this run. It
// must be emitted exactly once per run, immediately before EventAgentEnd, so
// consumers can classify the outcome from Status instead of inferring it from
// EventDone/EventError/channel-close combinations.
func (a *Agent) emitRunFinished(ch chan<- Event, status TaskStatus, reason string, runErr error, usage *provider.Usage, attachments []provider.Attachment) {
	a.mu.Lock()
	turnID := a.conversationTurnID
	turnOpen := a.conversationTurnOpen
	assistantEntryID := a.lastAssistantEntryID
	assistantMessage := cloneMessage(a.lastAssistantMessage)
	if turnOpen && !a.config.RuntimeOwnsTurnEnd {
		a.conversationTurnOpen = false
	}
	a.mu.Unlock()
	if turnOpen {
		if store, ok := any(a.config.Session).(conversationTurnStore); ok {
			turnStatus := "failed"
			switch status {
			case TaskSuccess:
				turnStatus = "completed"
			case TaskCanceled:
				turnStatus = "cancelled"
			case TaskIncomplete:
				turnStatus = "incomplete"
			}
			if err := store.EndConversationTurn(turnID, turnStatus, reason); err != nil {
				log.Printf("[agent] failed to close conversation turn %s: %v", turnID, err)
			}
		}
	}
	// The canonical terminal event is sent unconditionally: an adapter that is
	// still reading must always observe the run outcome, while every other send
	// stops once the run context is done (see sendEvent).
	ch <- Event{
		Type:             EventRunFinished,
		Done:             true,
		Status:           status,
		StopReason:       reason,
		Error:            runErr,
		AssistantEntryID: assistantEntryID,
		AssistantMessage: assistantMessage,
		Usage:            usage,
		Attachments:      attachments,
		ContextUsage:     a.GetContextUsage(),
	}
}

// emit sends an event with this agent's ID stamped on it.
func (a *Agent) emit(ch chan<- Event, event Event) {
	event.AgentID = a.id
	a.sendEvent(ch, event)
}

// --- Public agent.Agent interface methods ---

// ID returns the agent's unique identifier.
func (a *Agent) ID() agentpkg.AgentID { return a.id }

// ParentID returns the parent agent's ID, or empty if top-level.
func (a *Agent) ParentID() agentpkg.AgentID { return a.parentID }

// Run processes a user message and streams events back.
func (a *Agent) Run(ctx context.Context, userMsg string) <-chan Event {
	return a.RunWithUserMessage(ctx, provider.NewUserMessage(userMsg))
}

// RunWithUserMessage processes an already-built user message and streams events back.
func (a *Agent) RunWithUserMessage(ctx context.Context, msg provider.Message) <-chan Event {
	ch := make(chan Event, 100)
	sink := newEventSink(ch)

	go func() {
		defer func() {
			// Stop child-agent forwarding before closing the channel so a late
			// send can never race with the close.
			sink.seal()
			close(ch)
		}()
		a.setRunContext(ctx)
		defer a.setRunContext(nil)
		a.mu.Lock()
		a.lastAssistantEntryID = ""
		a.lastAssistantMessage = provider.Message{}
		a.mu.Unlock()
		if a.config.RuntimeOwnsUserEntry && a.config.Session != nil {
			// Durable admission appended the user entry outside the Manager's
			// in-memory snapshot. Refresh its leaf before assistant/tool entries
			// are persisted by the Agent loop.
			if err := a.config.Session.Reload(); err != nil {
				a.emitRunFinished(ch, TaskFailed, "session_reload", err, nil, nil)
				ch <- Event{Type: EventError, Error: fmt.Errorf("reload runtime-owned user entry: %w", err)}
				ch <- a.agentEndEvent()
				return
			}
		}
		turnStore, turnStarted := a.beginConversationTurn(msg)
		if turnStore != nil && !turnStarted {
			a.emitRunFinished(ch, TaskFailed, "turn_start", fmt.Errorf("failed to start conversation turn"), nil, nil)
			ch <- a.agentEndEvent()
			return
		}

		// Add user message to conversation
		if msg.Role == "" {
			msg.Role = "user"
		}
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}
		a.mu.Lock()
		msgIndex := len(a.messages)
		userEntryLoaded := a.config.RuntimeOwnsUserEntry && a.config.UserEntryID != "" &&
			msgIndex > 0 && len(a.messageIDs) == msgIndex && a.messageIDs[msgIndex-1] == a.config.UserEntryID
		if userEntryLoaded {
			msgIndex--
		} else {
			a.messages = append(a.messages, msg)
			entryID := ""
			if a.config.RuntimeOwnsUserEntry {
				entryID = a.config.UserEntryID
			}
			a.messageIDs = append(a.messageIDs, entryID)
			a.context.Messages = append(a.context.Messages, msg)
		}
		a.mu.Unlock()

		// Save to session
		if a.config.Session != nil && !a.config.RuntimeOwnsUserEntry {
			msgID, err := a.config.Session.AppendMessage(msg)
			if err != nil {
				a.emitRunFinished(ch, TaskFailed, "session_save", err, nil, nil)
				ch <- Event{Type: EventError, Error: fmt.Errorf("save user message to session: %w", err)}
				ch <- a.agentEndEvent()
				return
			}
			a.setMessageID(msgIndex, msgID)
		}

		// Run agent loop
		a.loop(contextWithEventSink(ctx, sink), ch)
		a.logDroppedEvents()
	}()

	return ch
}

// logDroppedEvents reports events that could not be delivered because the run
// ended while its consumer had stopped reading (see sendEvent). Every run entry
// point calls it after the loop returns so the drop is never silent.
func (a *Agent) logDroppedEvents() {
	if a == nil {
		return
	}
	if dropped := a.droppedEvents.Load(); dropped > 0 {
		log.Printf("[agent] run %s dropped %d event(s): the consumer stopped reading before the run ended", a.id, dropped)
	}
}

func (a *Agent) beginConversationTurn(msg provider.Message) (conversationTurnStore, bool) {
	if a == nil || a.config.Session == nil || !a.config.ConversationTurn || msg.SystemInjected {
		return nil, true
	}
	return a.beginConversationTurnForRun()
}

func (a *Agent) beginConversationTurnForRun() (conversationTurnStore, bool) {
	if a == nil || a.config.Session == nil || !a.config.ConversationTurn {
		return nil, true
	}
	store, ok := any(a.config.Session).(conversationTurnStore)
	if !ok {
		return nil, true
	}
	turnID := a.config.ConversationTurnID
	if turnID == "" {
		turnID = "turn-" + session.GenerateID()
	}
	if err := store.StartConversationTurn(turnID, string(a.config.IntentID), string(a.config.RunID)); err != nil {
		return store, false
	}
	a.mu.Lock()
	a.conversationTurnID = turnID
	a.conversationTurnOpen = true
	a.mu.Unlock()
	return store, true
}

// RunWithMessages processes with explicit message history.
// summarizeMessagesWithSubAgent runs a summarization request through a child
// Agent loop. This deliberately avoids constructing a provider request here: the
// child uses the same provider implementation and model compatibility logic as
// every other sub-agent.
func (a *Agent) summarizeMessagesWithSubAgent(ctx context.Context, messages []provider.Message, maxTokens int) (string, error) {
	registry := tools.NewRegistry(workDirForAgent(a), sandbox.NewNoneSandbox())
	model := a.config.Model
	if model != nil {
		modelCopy := *model
		// The summarizer intentionally receives the oversized history. Let the
		// provider enforce its real limit instead of the normal turn guard, whose
		// reserve is designed for an interactive response.
		modelCopy.ContextWindow = 0
		model = &modelCopy
	}
	child := NewWithLoopConfig(AgentLoopConfig{Config: Config{
		Provider:           a.config.Provider,
		Vendor:             a.config.Vendor,
		Model:              model,
		Mode:               a.config.Mode,
		ThinkingLevel:      a.config.ThinkingLevel,
		MaxTokens:          maxTokens,
		CompactionSettings: ctxpkg.CompactionSettings{Enabled: false},
	}, MaxIterations: 1, ToolExecutionMode: "sequential"}, registry)
	child.frozenSystemPrompt = a.frozenSystemPrompt
	child.frozenToolDefs = nil
	child.context.SystemPrompt = a.frozenSystemPrompt
	child.context.Tools = nil

	var summary strings.Builder
	for event := range child.RunWithMessages(ctx, messages) {
		switch event.Type {
		case EventTextDelta:
			summary.WriteString(event.TextDelta)
		case EventError:
			if event.Error != nil {
				return "", event.Error
			}
		}
	}
	result := strings.TrimSpace(summary.String())
	if result == "" {
		return "", fmt.Errorf("tool result summarization returned empty result")
	}
	return result, nil
}

func workDirForAgent(a *Agent) string {
	if a != nil && a.registry != nil {
		return a.registry.GetWorkDir()
	}
	return ""
}

// RunWithMessages processes with explicit message history.
func (a *Agent) RunWithMessages(ctx context.Context, messages []provider.Message) <-chan Event {
	ch := make(chan Event, 100)
	sink := newEventSink(ch)

	go func() {
		defer func() {
			sink.seal()
			close(ch)
		}()
		a.mu.Lock()
		a.messages = messages
		a.messageIDs = make([]string, len(messages))
		a.context.Messages = messages
		a.mu.Unlock()
		a.loop(contextWithEventSink(ctx, sink), ch)
		a.logDroppedEvents()
	}()

	return ch
}

// RunWithLoadedHistory continues an Agent after the shared Runtime has loaded
// a persisted session history. It deliberately does not append another user
// message, which is required for a linked retry of an already-accepted intent.
func (a *Agent) RunWithLoadedHistory(ctx context.Context) <-chan Event {
	ch := make(chan Event, 100)
	sink := newEventSink(ch)

	go func() {
		defer func() {
			sink.seal()
			close(ch)
		}()
		turnStore, turnStarted := a.beginConversationTurnForRun()
		if turnStore != nil && !turnStarted {
			a.emitRunFinished(ch, TaskFailed, "turn_start", fmt.Errorf("failed to start conversation turn"), nil, nil)
			ch <- a.agentEndEvent()
			return
		}
		a.loop(contextWithEventSink(ctx, sink), ch)
		a.logDroppedEvents()
	}()

	return ch
}

func retryCompatibilityStatus(attempt, maxAttempts, retryAfterMS int) string {
	if attempt > 0 && maxAttempts > 0 {
		message := fmt.Sprintf("Retrying (attempt %d/%d)", attempt, maxAttempts)
		if retryAfterMS > 0 {
			message += fmt.Sprintf("; waiting %s", time.Duration(retryAfterMS)*time.Millisecond)
		}
		return message + "..."
	}
	return "Retrying..."
}

// BuildBackgroundChatParams creates one durable Responses background request
// without entering the Agent loop. The caller owns the remote response
// lifecycle and must not use this as a substitute for RunWithUserMessage.
func (a *Agent) BuildBackgroundChatParams(localTurnID string, msg provider.Message) (provider.ChatParams, error) {
	return a.buildBackgroundChatParams(localTurnID, &msg)
}

// BuildBackgroundContinuationParams builds a durable Responses continuation
// request from already-loaded local history. It does not append a synthetic
// user message, which is important when a process resumes a remote run.
func (a *Agent) BuildBackgroundContinuationParams(localTurnID string) (provider.ChatParams, error) {
	return a.buildBackgroundChatParams(localTurnID, nil)
}

// BuildBackgroundReplayParams builds a background request from the local
// Responses archive, explicitly dropping remote lineage. It is used when a
// continuation proves that the remote response/conversation state is no
// longer available.
func (a *Agent) BuildBackgroundReplayParams(localTurnID string) (provider.ChatParams, error) {
	params, err := a.buildBackgroundChatParams(localTurnID, nil)
	if err != nil {
		return provider.ChatParams{}, err
	}
	if a.config.Session == nil || params.ResponseOptions == nil {
		return params, nil
	}
	// The background coordinator appends tool calls/results to the durable
	// session manager while the detached agent remains unchanged. Read the
	// latest session messages so replay includes the failed continuation.
	if state := a.config.Session.GetReplayState(); len(state.Messages) > 0 {
		params.Messages = append([]provider.Message(nil), state.Messages...)
	}
	items, err := a.nativeResponsesReplayItems(params.Messages)
	if err != nil {
		return provider.ChatParams{}, fmt.Errorf("load Responses background replay state: %w", err)
	}
	params.ResponseOptions.PreviousResponseID = ""
	params.ResponseOptions.ReplayItems = items
	params.ResponseOptions.SuppressConversation = true
	return params, nil
}

// ResponsesStateFallbackError delegates remote state classification to the
// configured provider without exposing provider-specific types to callers.
func (a *Agent) ResponsesStateFallbackError(err error) bool {
	if a == nil || a.config.Provider == nil {
		return false
	}
	fallback, ok := a.config.Provider.(provider.ResponseStateFallbackProvider)
	return ok && fallback.ResponseStateFallbackError(err)
}

func (a *Agent) buildBackgroundChatParams(localTurnID string, newMessage *provider.Message) (provider.ChatParams, error) {
	if a == nil || a.config.Provider == nil || a.config.Model == nil {
		return provider.ChatParams{}, fmt.Errorf("agent provider and model are required")
	}

	a.mu.RLock()
	messages := append([]provider.Message(nil), a.messages...)
	systemPrompt := a.frozenSystemPrompt
	tools := append([]provider.ToolDefinition(nil), a.frozenToolDefs...)
	a.mu.RUnlock()
	if newMessage != nil {
		msg := *newMessage
		if msg.Role == "" {
			msg.Role = "user"
		}
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}
		messages = append(messages, msg)
	}
	messages = append(messages, a.buildSessionContextMessage())
	messages = applyCacheMarkers(messages, selectCacheMarkers(messages))

	params := provider.ChatParams{
		Messages:      messages,
		Tools:         tools,
		SystemPrompt:  systemPrompt,
		ThinkingLevel: provider.NormalizeThinkingLevel(a.config.ThinkingLevel),
		MaxTokens:     a.maxTokensForRequest(messages),
		Temperature:   config.NormalizeSamplingPtr(a.config.Model.Temperature),
		TopP:          config.NormalizeSamplingPtr(a.config.Model.TopP),
		ModelID:       a.config.Model.ID,
		Abort:         a.abort,
	}
	if a.config.Session == nil || a.config.Provider.API() != "openai-responses" {
		return params, nil
	}
	state, err := a.prepareResponsesState(localTurnID, messages, false)
	if err != nil {
		return provider.ChatParams{}, fmt.Errorf("prepare Responses background state: %w", err)
	}
	params.ResponseOptions = &provider.ResponseOptions{
		PreviousResponseID:   state.previousResponseID,
		ReplayItems:          state.replayItems,
		SuppressConversation: state.suppressConversation,
	}
	return params, nil
}

// ExecuteBackgroundToolCall runs one local function tool through the same
// approval, sandbox and execution-record path as the normal agent loop. The
// returned event stream is owned by the caller, which may publish approval and
// progress events while waiting for the result.
func (a *Agent) ExecuteBackgroundToolCall(ctx context.Context, tc provider.ToolCallBlock, localTurnID string) <-chan Event {
	return a.executeBackgroundToolCall(ctx, tc, localTurnID, false, nil)
}

// ExecuteBackgroundToolCallRecovering reopens only known read-only tool
// records left in an interrupted state. Side-effecting records stay guarded by
// the normal idempotency path and are never retried automatically.
func (a *Agent) ExecuteBackgroundToolCallRecovering(ctx context.Context, tc provider.ToolCallBlock, localTurnID string) <-chan Event {
	return a.executeBackgroundToolCall(ctx, tc, localTurnID, true, nil)
}

// ExecuteBackgroundToolCallOrdered runs one call of a background batch that
// reports its starts in the declared provider order. allowReadOnlyRecovery keeps
// the normal and recovering entry points available to batch callers, and launch
// carries the batch position (nil executes unordered, as before).
func (a *Agent) ExecuteBackgroundToolCallOrdered(ctx context.Context, tc provider.ToolCallBlock, localTurnID string, allowReadOnlyRecovery bool, launch *ToolLaunchHandle) <-chan Event {
	return a.executeBackgroundToolCall(ctx, tc, localTurnID, allowReadOnlyRecovery, launch)
}

func (a *Agent) executeBackgroundToolCall(ctx context.Context, tc provider.ToolCallBlock, localTurnID string, allowReadOnlyRecovery bool, launch *ToolLaunchHandle) <-chan Event {
	ch := make(chan Event, 100)
	sink := newEventSink(ch)
	go func() {
		defer func() {
			sink.seal()
			close(ch)
		}()
		_ = a.executeSingleToolCallWithRecovery(contextWithEventSink(ctx, sink), tc, localTurnID, ch, allowReadOnlyRecovery, launch)
	}()
	return ch
}

const (
	defaultOutputMaxTokens       = 8192
	escalatedOutputMaxTokens     = 65536
	maxOutputRecoveryAttempts    = 3
	outputRecoveryTailCharacters = 1200
)

func buildOutputRecoveryMessage(partial string) string {
	runes := []rune(partial)
	if len(runes) > outputRecoveryTailCharacters {
		runes = runes[len(runes)-outputRecoveryTailCharacters:]
	}
	tail := string(runes)
	return "Output token limit hit. Resume directly — no apology, no recap of what you were doing. Pick up mid-thought if that is where the cut happened. Break remaining work into smaller pieces.\n\n" +
		"The previous assistant response ended with this exact suffix. Do not repeat any line, table row, code line, or prose that already appears in it; output only text that comes after this suffix:\n\n<previous_response_suffix>\n" + tail + "\n</previous_response_suffix>"
}
func isOutputTruncationReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max-tokens", "length", "max_output_tokens", "token_limit":
		return true
	default:
		return false
	}
}

func (a *Agent) escalatedMaxTokens(current int) int {
	limit := escalatedOutputMaxTokens
	// Never exceed a known model's native output limit. For an explicit model
	// limit, retry is disabled by the caller; this guard also protects callers
	// that invoke the helper directly.
	if a.config.Model != nil && a.config.Model.MaxTokens > 0 {
		// A known model's native limit is the escalation ceiling. This keeps
		// small models below their API limit while allowing 128K/384K models
		// to use their full native output ceiling after the conservative default.
		limit = a.config.Model.MaxTokens
	}
	if a.config.Model != nil && a.config.Model.ContextWindow > 0 && limit > a.config.Model.ContextWindow {
		limit = a.config.Model.ContextWindow
	}
	if current >= limit {
		return 0
	}
	return limit
}

func (a *Agent) loop(ctx context.Context, ch chan<- Event) {
	a.sendEvent(ch, Event{Type: EventAgentStart})

	// Propagate Abort() into a cancellable context so an interrupt (e.g. Esc in
	// the TUI) also stops in-flight tool execution. Every tool call must still
	// record a tool result message — otherwise the persisted history keeps an
	// assistant tool_call without a response and strict tool APIs (Kimi/OpenAI)
	// reject the next request with a 400 error.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	a.setRunContext(runCtx)
	defer a.setRunContext(nil)
	go func() {
		select {
		case <-a.abort:
			cancelRun()
		case <-runCtx.Done():
		}
	}()

	// Track consecutive iterations without text output for loop detection
	consecutiveNoText := 0
	maxConsecutiveNoText := a.config.MaxConsecutiveNoText
	if maxConsecutiveNoText <= 0 {
		maxConsecutiveNoText = 95 // default threshold
	}
	const maxConsecutiveNoTextAfterWarning = 5 // After warning, allow 5 more turns before stopping
	warningIssued := false

	// Pressure tracking — fire events once per threshold crossing
	contextPressureFired := false
	budgetPressureFired := false

	escalated := false
	recoveryAttempts := 0

	// Stream timeout retry: when the provider stalls (idle/response timeout)
	// before emitting any visible output, retry the turn a limited number of
	// times instead of failing the whole run. Retrying is safe here because no
	// tool has been executed yet and nothing has been persisted for this turn.
	streamTimeoutRetries := 0
	const maxStreamTimeoutRetries = 2

	// Empty-response detection: a provider may return an effectively empty
	// turn (no text/thinking/toolCall + stub usage) on transient errors (e.g.
	// some OpenAI-compatible gateways return usage {1,1,2} with HTTP 200). Such
	// a turn must be retried instead of silently ending the session via the
	// "no tool call => done" path. See provider.ClassifyTurn.
	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 2

	// Context-overflow recovery: if the provider rejects a request for
	// exceeding the context window (possible when the token estimate
	// underestimates real usage, e.g. Chinese-heavy channel chats), compact
	// and retry once instead of failing the session permanently.
	contextOverflowRetried := false
	responsesReplayFallback := false
	for i := 0; i < a.config.MaxIterations; i++ {
		select {
		case <-runCtx.Done():
			a.emitRunFinished(ch, TaskCanceled, "aborted", runCtx.Err(), nil, nil)
			ch <- Event{Type: EventError, Error: runCtx.Err(), StopReason: "aborted"}
			ch <- a.agentEndEvent()
			return
		default:
		}

		a.sendEvent(ch, Event{Type: EventTurnStart})

		// A turn whose answer was cut off by the output limit is not a success
		// unless escalation or a continuation actually recovered it. The flag is
		// scoped to this turn: a later turn that completes normally (for example
		// after a follow-up injection kept the run open) must not inherit it.
		truncated := false

		// Process pending steering messages
		if a.config.GetSteeringMessages != nil {
			steeringMessages := a.config.GetSteeringMessages()
			if len(steeringMessages) > 0 {
				a.mu.Lock()
				for _, msg := range steeringMessages {
					a.sendEvent(ch, Event{Type: EventMessageStart, Message: msg})
					a.sendEvent(ch, Event{Type: EventMessageEnd, Message: msg})
					a.messages = append(a.messages, msg)
					a.messageIDs = append(a.messageIDs, "")
					a.context.Messages = append(a.context.Messages, msg)
				}
				a.mu.Unlock()
			}
		}

		// Use frozen system prompt and tools (R2.1: built once, never change during session)
		a.context.SystemPrompt = a.frozenSystemPrompt
		a.context.Tools = a.frozenToolDefs

		// Compact before building the next request so plain text turns and
		// forced compactions cannot miss the trigger point.
		a.compactIfNeeded(runCtx, ch)

		// Build session context message with dynamic info (R2.3)
		sessionContextMsg := a.buildSessionContextMessage()

		// Build and guard message list before sending. Session context is
		// system_injected, so cache markers skip it.
		allMessages, err := a.prepareRequestMessages(sessionContextMsg, ch)
		if err != nil {
			// The estimated request no longer fits the context budget. Recover
			// once via compaction/truncation instead of failing permanently.
			if a.tryRecoverContextOverflow(runCtx, ch, &contextOverflowRetried, err) {
				continue
			}
			a.emitRunFinished(ch, TaskIncomplete, "context_limit", err, nil, nil)
			ch <- Event{Type: EventError, Error: err, StopReason: "context_limit"}
			ch <- a.agentEndEvent()
			return
		}

		// Select cache markers (dual-marker rolling buffer, R3.1-R3.3)
		markers := selectCacheMarkers(allMessages)
		messagesWithMarkers := applyCacheMarkers(allMessages, markers)

		// Chat request with frozen system prompt and cache markers
		params := provider.ChatParams{
			Messages:      messagesWithMarkers,
			Tools:         a.frozenToolDefs,
			SystemPrompt:  a.frozenSystemPrompt,
			ThinkingLevel: provider.NormalizeThinkingLevel(a.config.ThinkingLevel),
			MaxTokens:     a.maxTokensForRequest(messagesWithMarkers),
			Temperature:   config.NormalizeSamplingPtr(a.config.Model.Temperature),
			TopP:          config.NormalizeSamplingPtr(a.config.Model.TopP),
			ModelID:       a.config.Model.ID,
			Abort:         a.abort,
		}
		if err := a.validateImageRequestBudget(allMessages); err != nil {
			a.emitRunFinished(ch, TaskIncomplete, "image_request_limit", err, nil, nil)
			ch <- Event{Type: EventError, Error: err, StopReason: "image_request_limit"}
			ch <- a.agentEndEvent()
			return
		}

		var responseState responsesStateSnapshot
		var responseTurnID string
		if a.config.Session != nil && a.config.Provider.API() == "openai-responses" {
			responseTurnID = session.GenerateID()
			state, err := a.prepareResponsesState(responseTurnID, allMessages, responsesReplayFallback)
			if err != nil {
				a.emitRunFinished(ch, TaskFailed, "error", err, nil, nil)
				ch <- Event{Type: EventError, Error: err, StopReason: "error"}
				ch <- a.agentEndEvent()
				return
			}
			responseState = state
			params.ResponseOptions = &provider.ResponseOptions{
				PreviousResponseID:   state.previousResponseID,
				ReplayItems:          state.replayItems,
				SuppressConversation: state.suppressConversation,
				ResponseArchive:      a.responseArchiveSink(responseTurnID, state.version),
			}
		}
		streamStart := time.Now()
		streamCh := a.config.Provider.Chat(runCtx, params)

		var (
			textContent    string
			thinkContent   string
			thinkSignature string
			toolCalls      []provider.ToolCallBlock
			toolCallIDs    = make(map[string]struct{})
			usage          *provider.Usage
			attachments    []provider.Attachment
			stopReason     string
			streamErr      error
		)

		// Process stream events
		for event := range streamCh {
			switch event.Type {
			case provider.StreamStart:
				// Stream started
			case provider.StreamTextDelta:
				textContent += event.TextDelta
				a.sendEvent(ch, Event{Type: EventTextDelta, TextDelta: event.TextDelta})
			case provider.StreamThinkDelta:
				thinkContent += event.ThinkDelta
				a.sendEvent(ch, Event{Type: EventThinkDelta, ThinkDelta: event.ThinkDelta})
			case provider.StreamThinkSignature:
				thinkSignature = event.ThinkSignature
			case provider.StreamHostedItem:
				if event.HostedItem != nil {
					a.sendEvent(ch, Event{Type: EventHostedItem, HostedItem: event.HostedItem})
				}
			case provider.StreamToolCall:
				if event.ToolCall != nil {
					if event.ToolCall.ID == "" {
						event.ToolCall.ID = provider.NextToolCallFallbackID("agent_toolcall")
					}
					// A replayed Responses item can be surfaced twice by a
					// gateway. The provider call id is the execution identity;
					// keep only the first occurrence in this turn.
					if _, duplicate := toolCallIDs[event.ToolCall.ID]; duplicate {
						continue
					}
					toolCallIDs[event.ToolCall.ID] = struct{}{}
					// Parse arguments for the event
					args, err := normalizeToolCallArguments(event.ToolCall)
					if err != nil {
						// Log parse error but continue - tool execution will handle invalid args.
						a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: fmt.Sprintf("Warning: failed to parse tool arguments: %v", err)})
					}
					toolCalls = append(toolCalls, *event.ToolCall)
					a.sendEvent(ch, Event{Type: EventToolCall, ToolCall: event.ToolCall, ToolArgs: args})
				}
			case provider.StreamUsage:
				usage = event.Usage
			case provider.StreamDone:
				stopReason = event.StopReason
				attachments = append([]provider.Attachment(nil), event.Attachments...)
			case provider.StreamError:
				streamErr = event.Error
				stopReason = event.StopReason
			case provider.StreamRetry:
				retryMaxAttempts := event.RetryMaxAttempts
				if retryMaxAttempts == 0 {
					retryMaxAttempts = event.RetryMax
				}
				// Preserve one status event for older consumers. This compatibility
				// projection stays sanitized: provider diagnostics ride on the
				// marked EventRetry below instead of user-facing status text.
				a.sendEvent(ch, Event{
					Type: EventStatus, StatusMessage: retryCompatibilityStatus(event.RetryAttempt, retryMaxAttempts, event.RetryAfterMS), RetryStatus: true,
					RetryAttempt: event.RetryAttempt, RetryMaxAttempts: retryMaxAttempts, RetryAfterMS: event.RetryAfterMS,
				})
				// StatusMessage carries the sanitized, bounded RetryDetail for
				// adapters that opt into showing it. Retry scheduling and state
				// never depend on this text.
				a.sendEvent(ch, Event{
					Type:             EventRetry,
					StatusMessage:    event.RetryDetail,
					RetryAttempt:     event.RetryAttempt,
					RetryMaxAttempts: retryMaxAttempts,
					RetryAfterMS:     event.RetryAfterMS,
					RetryReason:      "provider",
				})
			}
		}

		if streamErr != nil {
			failureClass := provider.ResponseStateFailureRequestFailed
			if responseTurnID != "" && responseState.remoteStateActive {
				failureClass = a.recordResponsesStateFailure(responseTurnID, responseState, streamErr)
			}
			if !responsesReplayFallback && responseState.remoteStateActive {
				if fallbackProvider, ok := a.config.Provider.(provider.ResponseStateFallbackProvider); ok && fallbackProvider.ResponseStateFallbackError(streamErr) {
					responsesReplayFallback = true
					a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: retryCompatibilityStatus(1, 1, 0), RetryStatus: true, ResponseStateFailureClass: string(failureClass), RetryAttempt: 1, RetryMaxAttempts: 1})
					a.sendEvent(ch, Event{Type: EventRetry, RetryAttempt: 1, RetryMaxAttempts: 1, RetryReason: "response_state"})
					continue
				}
			}
			if provider.IsContextOverflowError(streamErr) && a.tryRecoverContextOverflow(runCtx, ch, &contextOverflowRetried, streamErr) {
				continue
			}
			if a.tryRetryStreamTimeout(runCtx, ch, &streamTimeoutRetries, maxStreamTimeoutRetries, textContent, thinkContent, streamErr) {
				continue
			}
			if provider.IsStreamTimeoutError(streamErr) {
				streamErr = fmt.Errorf("供应商响应超时，已自动重试 %d 次仍未恢复，请稍后重试或检查网络/供应商状态", streamTimeoutRetries)
			}
			a.emitRunFinished(ch, TaskFailed, stopReason, streamErr, usage, nil)
			ch <- Event{Type: EventError, Error: streamErr, StopReason: stopReason, ResponseStateFailureClass: func() string {
				if responseState.remoteStateActive {
					return string(failureClass)
				}
				return ""
			}()}
			ch <- a.agentEndEvent()
			return
		}
		// A successful replay has re-established the remote lineage for this
		// turn; subsequent turns may use the newly archived response id again.
		responsesReplayFallback = false

		if isOutputTruncationReason(stopReason) {
			if !escalated && !a.config.MaxTokensUserSet && (a.config.Model == nil || !a.config.Model.MaxTokensSet) {
				nextMax := a.escalatedMaxTokens(params.MaxTokens)
				if nextMax > params.MaxTokens {
					escalated = true
					a.config.MaxTokens = nextMax
					a.sendEvent(ch, Event{Type: EventRetry, RetryAttempt: 1, RetryMaxAttempts: 1, RetryMaxTokens: nextMax, RetryReason: "output_limit"})
					continue
				}
			}
			if escalated && len(toolCalls) == 0 && recoveryAttempts < maxOutputRecoveryAttempts {
				recoveryAttempts++
				if textContent != "" || thinkContent != "" {
					var partialContents []provider.ContentBlock
					if thinkContent != "" {
						partialContents = append(partialContents, provider.ContentBlock{Type: "thinking", Thinking: thinkContent, Signature: thinkSignature})
					}
					if textContent != "" {
						partialContents = append(partialContents, provider.ContentBlock{Type: "text", Text: textContent})
					}
					partial := provider.NewAssistantMessage(partialContents)
					a.mu.Lock()
					a.messages = append(a.messages, partial)
					a.messageIDs = append(a.messageIDs, "")
					a.context.Messages = append(a.context.Messages, partial)
					a.mu.Unlock()
				}
				recovery := provider.NewSystemInjectedUserMessage(buildOutputRecoveryMessage(textContent))
				a.mu.Lock()
				a.messages = append(a.messages, recovery)
				a.messageIDs = append(a.messageIDs, "")
				a.context.Messages = append(a.context.Messages, recovery)
				a.mu.Unlock()
				a.sendEvent(ch, Event{Type: EventRetry, RetryAttempt: recoveryAttempts + 1, RetryMaxAttempts: maxOutputRecoveryAttempts + 1, RetryMaxTokens: params.MaxTokens, RetryReason: "continuation", RetryContinue: true})
				continue
			}
			if len(toolCalls) == 0 {
				// Escalation and continuation are exhausted: the turn stays truncated.
				truncated = true
			}
		}

		// Empty-response guard (provider.ClassifyTurn is vendor-agnostic). This
		// runs before the assistant message is built/saved so a retried turn
		// does not leave an empty assistant message in history. Only the
		// "effectively empty + stub usage + no explicit stop" case is retried;
		// a model that produced content or explicitly signalled stop is honoured.
		if provider.ClassifyTurn(textContent, thinkContent, toolCalls, usage, stopReason) == provider.TurnEmpty {
			emptyResponseRetries++
			if emptyResponseRetries <= maxEmptyResponseRetries {
				a.sendEvent(ch, Event{Type: EventStatus, StatusMessage: retryCompatibilityStatus(emptyResponseRetries, maxEmptyResponseRetries, 0), RetryStatus: true, RetryAttempt: emptyResponseRetries, RetryMaxAttempts: maxEmptyResponseRetries})
				a.sendEvent(ch, Event{Type: EventRetry, RetryAttempt: emptyResponseRetries, RetryMaxAttempts: maxEmptyResponseRetries, RetryReason: "empty_response"})
				continue
			}
			a.emitRunFinished(ch, TaskFailed, "empty_response", fmt.Errorf("provider returned an empty response %d times in a row", emptyResponseRetries), usage, nil)
			ch <- Event{Type: EventError, Error: fmt.Errorf("provider returned an empty response %d times in a row; last usage: %s, stopReason: %q", emptyResponseRetries, provider.FormatUsage(usage), stopReason), StopReason: "empty_response"}
			ch <- a.agentEndEvent()
			return
		}
		emptyResponseRetries = 0

		// Build assistant message
		var contents []provider.ContentBlock
		if thinkContent != "" {
			contents = append(contents, provider.ContentBlock{
				Type:      "thinking",
				Thinking:  thinkContent,
				Signature: thinkSignature,
			})
		}
		if textContent != "" {
			contents = append(contents, provider.ContentBlock{
				Type: "text",
				Text: textContent,
			})
		}
		for _, tc := range toolCalls {
			tc := tc
			contents = append(contents, provider.ContentBlock{
				Type:     "toolCall",
				ToolCall: &tc,
			})
		}

		assistantMsg := provider.NewAssistantMessage(contents)
		estimator := ctxpkg.ResolveTokenEstimator(a.config.CompactionSettings, a.config.Model)
		estimatedUsage := estimateProviderUsage(a.frozenSystemPrompt, messagesWithMarkers, a.frozenToolDefs, assistantMsg, estimator)
		usage = completeProviderUsage(usage, estimatedUsage)
		// Store usage in the message for context tracking
		assistantMsg.Usage = usage
		deferAssistantEntry := a.config.RuntimeOwnsTurnEnd && a.config.Session != nil && len(toolCalls) == 0
		assistantEntryID := ""
		if deferAssistantEntry {
			assistantEntryID = session.RunAssistantEntryID(a.config.RunID)
		}
		a.mu.Lock()
		assistantIndex := len(a.messages)
		a.messages = append(a.messages, assistantMsg)
		a.messageIDs = append(a.messageIDs, assistantEntryID)
		a.context.Messages = append(a.context.Messages, assistantMsg)
		a.mu.Unlock()

		// Save to session
		if a.config.Session != nil && !deferAssistantEntry {
			msgID, err := a.config.Session.AppendMessage(assistantMsg)
			if err != nil {
				a.emitRunFinished(ch, TaskFailed, "session_save", err, usage, nil)
				ch <- Event{Type: EventError, Error: fmt.Errorf("save assistant message to session: %w", err)}
				ch <- a.agentEndEvent()
				return
			}
			assistantEntryID = msgID
			a.setMessageID(assistantIndex, msgID)
		}
		a.mu.Lock()
		a.lastAssistantEntryID = assistantEntryID
		a.lastAssistantMessage = cloneMessage(assistantMsg)
		a.mu.Unlock()

		// Calculate cost
		if usage != nil && a.config.Model != nil {
			usage.CalculateCost(a.config.Model)
		}
		a.sendEvent(ch, Event{Type: EventUsage, Usage: usage, ContextUsage: a.GetContextUsage()})

		// Record usage stats
		if a.config.Session != nil && usage != nil {
			vendor := usageStatsProviderName(a.config.Config)
			protocol := a.config.Provider.API()
			modelID := a.config.Model.ID
			durationMs := int(time.Since(streamStart).Milliseconds())
			if err := a.config.Session.RecordUsageFromProviderUsage(vendor, protocol, modelID, usage, durationMs); err != nil {
				log.Printf("[agent] failed to record usage stats: %v", err)
			}
		}

		// Track progress for loop detection. Tool-only warnings are injected
		// after tool results are recorded so provider message ordering stays valid.
		if textContent != "" {
			consecutiveNoText = 0
			warningIssued = false // AI responded with text, reset warning state
		}

		// If no tool calls, the turn would end the run. Adapters may still have
		// work to hand over (for example a team lead whose members finished after
		// the last iteration): inject it and keep the run open instead of
		// finishing, so the lead learns about the result and decides whether to
		// continue or end the turn.
		if len(toolCalls) == 0 {
			if a.injectFollowUpMessages(runCtx, ch) {
				continue
			}
			if err := runCtx.Err(); err != nil {
				// The run was cancelled while this turn was in flight; the follow-up
				// hook returns as soon as the run context is done. Terminalize here
				// like the loop-entry cancellation path: reporting success would make
				// adapters that derive their state from the terminal event (ACP) turn
				// a user cancellation into a normal completion.
				a.emitRunFinished(ch, TaskCanceled, "aborted", err, nil, nil)
				ch <- Event{Type: EventError, Error: err, StopReason: "aborted"}
				ch <- a.agentEndEvent()
				return
			}
			contextUsage := a.GetContextUsage()
			a.sendEvent(ch, Event{Type: EventTurnEnd, TurnMessage: assistantMsg, ContextUsage: contextUsage})
			if truncated {
				// The provider cut the answer off and neither escalation nor
				// continuation could recover it: the run is incomplete, not a success.
				a.emitRunFinished(ch, TaskIncomplete, "output_limit", fmt.Errorf("provider output was truncated and could not be continued"), usage, attachments)
				ch <- Event{Type: EventError, Error: fmt.Errorf("provider output truncated (stop reason %q)", stopReason), StopReason: "output_limit"}
				ch <- a.agentEndEvent()
				return
			}
			a.emitRunFinished(ch, TaskSuccess, stopReason, nil, usage, attachments)
			ch <- Event{Type: EventDone, StopReason: stopReason, Usage: usage, Attachments: attachments, ContextUsage: contextUsage}
			ch <- a.agentEndEvent()
			return
		}

		// Execute tool calls
		var toolResults []provider.Message
		toolTurnID := ""
		if a.config.Session != nil {
			// The assistant message position is stable across replay; use it as
			// the local turn identity so a reconnected tool call can reuse its
			// execution record instead of receiving a fresh random key.
			toolTurnID = fmt.Sprintf("assistant-turn-%d", assistantIndex)
		}
		if a.config.ToolExecutionMode == "sequential" {
			toolResults = a.executeToolCallsSequential(runCtx, toolCalls, toolTurnID, ch)
		} else {
			toolResults = a.executeToolCallsParallel(runCtx, toolCalls, toolTurnID, ch)
		}

		// Add tool results to context
		a.mu.Lock()
		for _, result := range toolResults {
			a.messages = append(a.messages, result)
			a.messageIDs = append(a.messageIDs, "")
			a.context.Messages = append(a.context.Messages, result)
		}
		baseIndex := len(a.messages) - len(toolResults)
		a.mu.Unlock()
		for i, result := range toolResults {
			if a.config.Session != nil {
				msgID, err := a.config.Session.AppendMessage(result)
				if err != nil {
					a.emitRunFinished(ch, TaskFailed, "session_save", err, usage, nil)
					ch <- Event{Type: EventError, Error: fmt.Errorf("save tool result to session: %w", err)}
					ch <- a.agentEndEvent()
					return
				}
				a.setMessageID(baseIndex+i, msgID)
			}
		}

		if textContent == "" {
			consecutiveNoText++
			threshold := maxConsecutiveNoText
			if warningIssued {
				threshold = maxConsecutiveNoTextAfterWarning
			}
			if consecutiveNoText >= threshold {
				if !warningIssued {
					// Inject a warning message to let the AI explain itself.
					warningMsg := provider.NewUserMessage("[System] You have been making tool calls for " + fmt.Sprintf("%d", consecutiveNoText) + " consecutive turns without any text response. Please explain what you are doing and whether you are stuck. If you are making progress, briefly describe your current task and continue. If you are truly stuck, please stop and explain the issue.")
					a.sendEvent(ch, Event{Type: EventMessageStart, Message: warningMsg})
					a.sendEvent(ch, Event{Type: EventMessageEnd, Message: warningMsg})
					a.mu.Lock()
					warningIndex := len(a.messages)
					a.messages = append(a.messages, warningMsg)
					a.messageIDs = append(a.messageIDs, "")
					a.context.Messages = append(a.context.Messages, warningMsg)
					a.mu.Unlock()
					if a.config.Session != nil {
						msgID, err := a.config.Session.AppendMessage(warningMsg)
						if err != nil {
							a.emitRunFinished(ch, TaskFailed, "session_save", err, usage, nil)
							ch <- Event{Type: EventError, Error: fmt.Errorf("save warning message to session: %w", err)}
							ch <- a.agentEndEvent()
							return
						}
						a.setMessageID(warningIndex, msgID)
					}
					warningIssued = true
					consecutiveNoText = 0 // Reset counter for post-warning phase
				} else {
					// Already warned, now truly stuck. Tool results have already been
					// appended, so the saved transcript remains provider-valid.
					a.emitRunFinished(ch, TaskIncomplete, "stuck", nil, usage, nil)
					ch <- Event{Type: EventError, Error: fmt.Errorf("agent appears stuck: %d consecutive turns without text output after warning", consecutiveNoText), StopReason: "stuck"}
					ch <- a.agentEndEvent()
					return
				}
			}
		}

		contextUsage := a.GetContextUsage()
		a.sendEvent(ch, Event{Type: EventTurnEnd, TurnMessage: assistantMsg, TurnToolResults: toolResults, ContextUsage: contextUsage})

		// --- Pressure checks (fire once per threshold crossing) ---

		// Context Pressure: fire EventContextPressure once when usage exceeds threshold
		if !contextPressureFired {
			threshold := a.config.ContextPressureThreshold
			if threshold <= 0 {
				threshold = 0.55 // default 55%
			}
			if ctx := contextUsage; ctx != nil && ctx.Percent != nil {
				if *ctx.Percent >= threshold*100 {
					contextPressureFired = true
					warnMsg := fmt.Sprintf(
						"[Context Pressure] %.0f%% of context window used (%d/%d tokens). "+
							"Compaction will trigger soon. Consider saving important context to memory.md and wrapping up the current task.",
						*ctx.Percent, ctx.Tokens, ctx.ContextWindow)
					a.sendEvent(ch, Event{
						Type:            EventContextPressure,
						PressureMessage: warnMsg,
						PressureType:    "context",
						PressurePercent: *ctx.Percent,
						ContextUsage:    ctx,
					})
				}
			}
		}

		// Budget Pressure: fire EventBudgetPressure once when remaining iterations reach threshold
		if !budgetPressureFired {
			threshold := a.config.BudgetPressureThreshold
			if threshold <= 0 {
				threshold = 0.20 // default 20%
			}
			remaining := float64(a.config.MaxIterations-i) / float64(a.config.MaxIterations)
			if remaining <= threshold {
				budgetPressureFired = true
				remainingTurns := a.config.MaxIterations - i
				warnMsg := fmt.Sprintf(
					"[Budget Pressure] %d/%d turns remaining (%.0f%%). "+
						"Complete the current task and summarize progress.",
					remainingTurns, a.config.MaxIterations, remaining*100)
				a.sendEvent(ch, Event{
					Type:            EventBudgetPressure,
					PressureMessage: warnMsg,
					PressureType:    "budget",
					PressurePercent: remaining * 100,
				})
			}
		}

		// Check if we should stop after this turn
		if a.config.ShouldStopAfterTurn != nil {
			messagesSnapshot, contextSnapshot := a.callbackSnapshot()
			stopCtx := ShouldStopAfterTurnContext{
				Message:     assistantMsg,
				ToolResults: cloneMessages(toolResults),
				Context:     contextSnapshot,
				NewMessages: messagesSnapshot,
			}
			if a.config.ShouldStopAfterTurn(stopCtx) {
				a.emitRunFinished(ch, TaskSuccess, "should_stop", nil, usage, nil)
				ch <- Event{Type: EventDone, StopReason: "should_stop", Usage: usage, ContextUsage: contextUsage}
				ch <- a.agentEndEvent()
				return
			}
		}

		// Prepare next turn
		if a.config.PrepareNextTurn != nil {
			messagesSnapshot, contextSnapshot := a.callbackSnapshot()
			prepCtx := PrepareNextTurnContext{
				ShouldStopAfterTurnContext: ShouldStopAfterTurnContext{
					Message:     assistantMsg,
					ToolResults: cloneMessages(toolResults),
					Context:     contextSnapshot,
					NewMessages: messagesSnapshot,
				},
			}
			update := a.config.PrepareNextTurn(prepCtx)
			if update != nil {
				if update.Context != nil {
					a.context = update.Context
				}
				if update.Model != nil {
					a.config.Model = update.Model
				}
				if update.ThinkingLevel != "" {
					a.config.ThinkingLevel = update.ThinkingLevel
				}
			}
		}

		// Check for steering messages (for mid-run injection)
		if a.config.GetSteeringMessages != nil {
			steeringMessages := a.config.GetSteeringMessages()
			if len(steeringMessages) > 0 {
				for _, msg := range steeringMessages {
					a.sendEvent(ch, Event{Type: EventMessageStart, Message: msg})
					a.sendEvent(ch, Event{Type: EventMessageEnd, Message: msg})
					a.mu.Lock()
					a.messages = append(a.messages, msg)
					a.messageIDs = append(a.messageIDs, "")
					a.context.Messages = append(a.context.Messages, msg)
					a.mu.Unlock()
				}
			}
		}

		// Continue loop - LLM will see tool results and decide next action
		// The loop will only exit when LLM returns a response without tool calls
		continue
	}

	a.emitRunFinished(ch, TaskIncomplete, "max_iterations", nil, nil, nil)
	ch <- Event{Type: EventError, Error: fmt.Errorf("max iterations (%d) exceeded", a.config.MaxIterations), StopReason: "max_iterations"}
	ch <- a.agentEndEvent()
}

// setRunContext records the context of the run currently producing events.
func (a *Agent) setRunContext(ctx context.Context) {
	if a == nil {
		return
	}
	if ctx == nil {
		a.runCtx.Store(nil)
		return
	}
	a.droppedEvents.Store(0)
	a.runCtx.Store(&ctx)
}

// sendEvent delivers ev unless the run context is done. Consumers may stop
// reading when a run is aborted (the TUI retires the stream, serve returns on
// cancellation); a bare send would then park the loop on a full channel forever
// and the run could never finish its terminal bookkeeping. Terminal events
// (EventRunFinished/EventDone/EventAgentEnd) keep their unconditional send so a
// still-reading adapter always observes them.
func (a *Agent) sendEvent(ch chan<- Event, ev Event) bool {
	if ch == nil {
		return false
	}
	var ctx context.Context
	if stored := a.runCtx.Load(); stored != nil {
		ctx = *stored
	}
	if ctx == nil {
		ch <- ev
		return true
	}
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		// The consumer stopped reading (aborted run): the event is no longer
		// deliverable, so report the drop instead of blocking the loop. The count
		// is logged once when the run ends.
		a.droppedEvents.Add(1)
		return false
	}
}

// injectFollowUpMessages appends adapter-supplied follow-up messages (drained
// through GetFollowUpMessages) and reports whether the loop must continue.
// Messages follow the same in-memory delivery contract as mid-run steering:
// they are injected as system-provided context and forwarded as message events.
func (a *Agent) injectFollowUpMessages(ctx context.Context, ch chan<- Event) bool {
	if a.config.GetFollowUpMessages == nil {
		return false
	}
	messages := a.config.GetFollowUpMessages(ctx)
	if len(messages) == 0 {
		return false
	}
	for _, msg := range messages {
		a.sendEvent(ch, Event{Type: EventMessageStart, Message: msg})
		a.sendEvent(ch, Event{Type: EventMessageEnd, Message: msg})
		a.mu.Lock()
		a.messages = append(a.messages, msg)
		a.messageIDs = append(a.messageIDs, "")
		a.context.Messages = append(a.context.Messages, msg)
		a.mu.Unlock()
	}
	return true
}

type responsesStateSnapshot struct {
	previousResponseID   string
	replayItems          []json.RawMessage
	suppressConversation bool
	remoteStateActive    bool
	version              int64
}

func (a *Agent) prepareResponsesState(localTurnID string, messages []provider.Message, forceReplay bool) (responsesStateSnapshot, error) {
	if a.config.Session == nil {
		return responsesStateSnapshot{}, nil
	}
	header := a.config.Session.GetHeader()
	if header == nil || header.ID == "" {
		return responsesStateSnapshot{}, fmt.Errorf("Responses archive requires an initialized session")
	}
	modeProvider, ok := a.config.Provider.(provider.ResponseStateModeProvider)
	if !ok {
		return responsesStateSnapshot{}, nil
	}
	if forceReplay || modeProvider.ResponseStateMode() == "replay" {
		items, err := a.nativeResponsesReplayItems(messages)
		if err != nil {
			return responsesStateSnapshot{}, fmt.Errorf("load Responses replay items: %w", err)
		}
		return responsesStateSnapshot{
			replayItems:          items,
			suppressConversation: forceReplay && modeProvider.ResponseStateMode() == "conversation",
		}, nil
	}
	if modeProvider.ResponseStateMode() == "conversation" {
		return responsesStateSnapshot{remoteStateActive: true}, nil
	}
	if modeProvider.ResponseStateMode() != "previous_response_id" {
		return responsesStateSnapshot{}, nil
	}
	state, err := session.GetResponseSessionState(a.config.Session.GetSessionDir(), header.ID)
	if err != nil {
		return responsesStateSnapshot{}, fmt.Errorf("load Responses state: %w", err)
	}
	if state == nil || state.PreviousResponseID == "" {
		// A new session has no remote lineage yet. Its first request is a normal
		// replay turn; the archive callback creates the lineage after completion.
		return responsesStateSnapshot{}, nil
	}
	return responsesStateSnapshot{previousResponseID: state.PreviousResponseID, remoteStateActive: true, version: state.Version}, nil
}

// nativeResponsesReplayItems interleaves canonical response output with the
// local user and tool-result transcript. It deliberately declines to replay
// when the archive and transcript cannot be proven to align one-to-one.
func (a *Agent) nativeResponsesReplayItems(messages []provider.Message) ([]json.RawMessage, error) {
	// Responses providers reject a single native replay item at 128 KiB. The
	// session store enforces this for new archives, but older sessions and
	// externally supplied archives may still contain larger items. Fall back to
	// ordinary transcript conversion instead of making the whole turn fail.
	const maxNativeReplayItemBytes = 128 * 1024
	if a.config.Session == nil {
		return nil, nil
	}
	header := a.config.Session.GetHeader()
	if header == nil || header.ID == "" {
		return nil, nil
	}
	turns, err := session.ListResponseReplayTurns(a.config.Session.GetSessionDir(), header.ID, 500)
	if err != nil || len(turns) == 0 {
		return nil, err
	}
	assistantCount := 0
	for _, message := range messages {
		if message.Role == "assistant" {
			assistantCount++
		}
	}
	if assistantCount != len(turns) {
		return nil, nil
	}
	items := make([]json.RawMessage, 0, len(messages)+assistantCount)
	turnIndex := 0
	for _, message := range messages {
		switch message.Role {
		case "assistant":
			for _, item := range turns[turnIndex].Items {
				if len(item) > maxNativeReplayItemBytes {
					return nil, nil
				}
				items = append(items, item)
			}
			turnIndex++
		case "toolResult":
			output, ok := replayTextContent(message)
			if !ok || message.ToolCallID == "" {
				return nil, nil
			}
			raw, err := json.Marshal(map[string]any{
				"type": "function_call_output", "call_id": message.ToolCallID, "output": output,
			})
			if err != nil {
				return nil, err
			}
			if len(raw) > maxNativeReplayItemBytes {
				return nil, nil
			}
			items = append(items, raw)
		default:
			text, ok := replayTextContent(message)
			if !ok {
				return nil, nil
			}
			role := message.Role
			if role == "" {
				role = "user"
			}
			raw, err := json.Marshal(map[string]any{
				"type": "message", "role": role,
				"content": []map[string]string{{"type": "input_text", "text": text}},
			})
			if err != nil {
				return nil, err
			}
			if len(raw) > maxNativeReplayItemBytes {
				return nil, nil
			}
			items = append(items, raw)
		}
	}
	return items, nil
}

func replayTextContent(message provider.Message) (string, bool) {
	if len(message.Contents) == 0 {
		return message.Content, true
	}
	var parts []string
	for _, content := range message.Contents {
		if content.Type != "text" {
			return "", false
		}
		parts = append(parts, content.Text)
	}
	if len(parts) == 0 {
		return message.Content, message.Content != ""
	}
	return strings.Join(parts, "\n"), true
}

func (a *Agent) responseArchiveSink(localTurnID string, expectedStateVersion int64) func(provider.ResponseArchive) {
	return func(archive provider.ResponseArchive) {
		if a.config.Session == nil {
			return
		}
		header := a.config.Session.GetHeader()
		if header == nil || header.ID == "" {
			return
		}
		now := time.Now()
		responseSummaryFields := map[string]any{
			"responseId": archive.ResponseID, "status": archive.Status, "itemCount": len(archive.Items),
			"incompleteReason": archive.IncompleteReason, "attachments": archive.Attachments,
		}
		if len(archive.UnknownEventTypes) > 0 {
			responseSummaryFields["unknownEventTypes"] = append([]string(nil), archive.UnknownEventTypes...)
		}
		if archive.Status == "completed" && archive.ResponseID != "" && archive.StateMode == "previous_response_id" {
			responseSummaryFields["lineageExpectedVersion"] = expectedStateVersion
			advanced, err := session.CompareAndSwapResponseSessionState(a.config.Session.GetSessionDir(), session.ResponseSessionState{
				SessionID: header.ID, StateMode: archive.StateMode, PreviousResponseID: archive.ResponseID,
				ConversationID: archive.ConversationID, Provider: a.config.Provider.Name(), API: a.config.Provider.API(), Model: a.config.Model.ID,
			}, expectedStateVersion)
			switch {
			case err != nil:
				responseSummaryFields["lineageUpdate"] = "error"
				log.Printf("[agent] advance Responses lineage: %v", err)
			case !advanced:
				// Another completed turn already advanced this session's remote
				// chain. Keep this response archived, but mark it as a branch so
				// recovery and support tooling never mistake it for the active tip.
				responseSummaryFields["lineageUpdate"] = "conflict"
				log.Printf("[agent] Responses lineage conflict for session %s turn %s", header.ID, localTurnID)
			default:
				responseSummaryFields["lineageUpdate"] = "advanced"
			}
		}
		responseSummary, _ := json.Marshal(responseSummaryFields)
		turn := session.ResponseTurn{
			SessionID: header.ID, LocalTurnID: localTurnID, ResponseID: archive.ResponseID,
			PreviousResponseID: archive.PreviousResponseID, ConversationID: archive.ConversationID,
			Provider: a.config.Provider.Name(), API: a.config.Provider.API(), Model: a.config.Model.ID,
			StateMode: archive.StateMode, Status: archive.Status, IncompleteReason: archive.IncompleteReason,
			ResponseSummary: responseSummary, CreatedAt: now, CompletedAt: &now,
		}
		if turn.StateMode == "" {
			turn.StateMode = "replay"
		}
		if turn.Status == "" {
			turn.Status = "unknown"
		}
		if err := session.SaveResponseTurn(a.config.Session.GetSessionDir(), turn); err != nil {
			log.Printf("[agent] archive Responses turn: %v", err)
			return
		}
		for _, item := range archive.Items {
			if err := session.SaveResponseItem(a.config.Session.GetSessionDir(), session.ResponseItemArchive{
				SessionID: header.ID, LocalTurnID: localTurnID, ResponseID: archive.ResponseID,
				ItemID: item.ID, OutputIndex: item.OutputIndex, ItemType: item.Type, ItemStatus: item.Status,
				SanitizedJSON: item.Canonical,
			}); err != nil {
				log.Printf("[agent] archive Responses item: %v", err)
			}
		}
	}
}

func (a *Agent) recordResponsesStateFailure(localTurnID string, state responsesStateSnapshot, streamErr error) provider.ResponseStateFailureClass {
	if a.config.Session == nil || a.config.Provider == nil || localTurnID == "" || streamErr == nil {
		return provider.ResponseStateFailureRequestFailed
	}
	header := a.config.Session.GetHeader()
	if header == nil || header.ID == "" {
		return provider.ResponseStateFailureRequestFailed
	}
	failureClass := provider.ResponseStateFailureRequestFailed
	if classifier, ok := a.config.Provider.(provider.ResponseStateFailureClassifier); ok {
		failureClass = classifier.ResponseStateFailureClass(streamErr)
	}
	modelID := ""
	if a.config.Model != nil {
		modelID = a.config.Model.ID
	}
	stateMode := "previous_response_id"
	if modeProvider, ok := a.config.Provider.(provider.ResponseStateModeProvider); ok && modeProvider.ResponseStateMode() != "" {
		stateMode = modeProvider.ResponseStateMode()
	}
	summary, err := json.Marshal(map[string]any{
		"status": "failed", "stateFailureClass": string(failureClass),
		"remoteStateActive": true, "lineageExpectedVersion": state.version,
	})
	if err != nil {
		return failureClass
	}
	now := time.Now()
	if err := session.SaveResponseTurn(a.config.Session.GetSessionDir(), session.ResponseTurn{
		SessionID: header.ID, LocalTurnID: localTurnID, PreviousResponseID: state.previousResponseID,
		Provider: a.config.Provider.Name(), API: a.config.Provider.API(), Model: modelID,
		StateMode: stateMode, Status: "failed", IncompleteReason: string(failureClass),
		ResponseSummary: summary, CreatedAt: now, CompletedAt: &now,
	}); err != nil {
		log.Printf("[agent] archive Responses state failure: %v", err)
	}
	return failureClass
}

func usageStatsProviderName(cfg Config) string {
	if cfg.Vendor != "" {
		return cfg.Vendor
	}
	if cfg.Provider != nil {
		return cfg.Provider.Name()
	}
	return ""
}

// executeToolCallsSequential executes tool calls one by one.
func (a *Agent) executeToolCallsSequential(ctx context.Context, toolCalls []provider.ToolCallBlock, localTurnID string, ch chan<- Event) []provider.Message {
	var results []provider.Message

	for _, tc := range toolCalls {
		result := a.executeSingleToolCall(ctx, tc, localTurnID, ch, nil)
		results = append(results, result)

		// Check for early termination
		if result.IsError {
			// Continue with other tools even if one fails
		}
	}

	return results
}

// executeToolCallsParallel executes tool calls concurrently. Calls report their
// starts in the declared provider order (see ToolLaunchOrder); nothing is held
// across a tool body, an approval, or a durable claim, so the calls still
// overlap and may finish in any order. Results stay aligned with the declared
// order.
func (a *Agent) executeToolCallsParallel(ctx context.Context, toolCalls []provider.ToolCallBlock, localTurnID string, ch chan<- Event) []provider.Message {
	order := NewToolLaunchOrder(len(toolCalls))
	indexes := make([]int, len(toolCalls))
	for i := range indexes {
		indexes[i] = i
	}
	return BoundedParallel(a.MaxToolConcurrency(), indexes, func(index int) provider.Message {
		return a.executeSingleToolCall(ctx, toolCalls[index], localTurnID, ch, order.Handle(index))
	})
}

// executeSingleToolCall executes a single tool call.
func (a *Agent) executeSingleToolCall(ctx context.Context, tc provider.ToolCallBlock, localTurnID string, ch chan<- Event, launch *ToolLaunchHandle) provider.Message {
	return a.executeSingleToolCallWithRecovery(ctx, tc, localTurnID, ch, false, launch)
}

func (a *Agent) executeSingleToolCallWithRecovery(ctx context.Context, tc provider.ToolCallBlock, localTurnID string, ch chan<- Event, allowReadOnlyRecovery bool, launch *ToolLaunchHandle) provider.Message {
	// Every path out of the call must release the calls queued behind it in the
	// batch, otherwise a call that fails before reporting its start would park
	// the rest of the batch.
	defer launch.Release()
	toolResult := func(content string, contents []provider.ContentBlock, isError bool) provider.Message {
		message := provider.NewToolResultMessageWithContents(tc.ID, tc.Name, content, contents, isError)
		message.ToolKind = tc.Kind
		return message
	}
	// Parse arguments
	var params map[string]any
	argsRaw := tc.Arguments
	if tc.InvalidArguments != "" {
		argsRaw = json.RawMessage(tc.InvalidArguments)
	}
	if len(argsRaw) > 0 {
		if err := json.Unmarshal(argsRaw, &params); err != nil {
			errMsg := fmt.Sprintf("parse tool arguments: %v", err)
			a.sendEvent(ch, Event{
				Type:       EventToolExecutionEnd,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolResult: errMsg,
				ToolError:  err,
			})
			return toolResult(errMsg, nil, true)
		}
	}
	if params == nil {
		params = map[string]any{}
	}

	// A parallel batch starts in the declared provider order: a later call may
	// not report its start before every earlier call of the same batch did. The
	// handoff happens here, before any approval or durable claim, so ordering
	// never blocks another call's execution.
	launch.waitStart()
	a.sendEvent(ch, Event{
		Type:       EventToolExecutionStart,
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		ToolArgs:   params,
	})
	launch.markStarted()

	// OS mode intentionally exposes and permits only the bash tool. Keep this
	// guard in execution as well as in ModeTools so an unsolicited provider call
	// cannot reach another registered tool.
	if a.config.Mode == "os" && tc.Name != "bash" {
		errMsg := fmt.Sprintf("tool %q is unavailable in OS mode; only bash is registered", tc.Name)
		a.sendEvent(ch, Event{
			Type:       EventToolExecutionEnd,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ToolResult: errMsg,
			ToolError:  fmt.Errorf("%s", errMsg),
		})
		return toolResult(errMsg, nil, true)
	}

	// Find tool
	tool, ok := a.registry.Get(tc.Name)
	if !ok {
		errMsg := fmt.Sprintf("unknown tool: %s", tc.Name)
		a.sendEvent(ch, Event{
			Type:       EventToolExecutionEnd,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			ToolResult: errMsg,
			ToolError:  fmt.Errorf("%s", errMsg),
		})
		return toolResult(errMsg, nil, true)
	}

	// Check if tool call should be blocked
	if a.config.BeforeToolCall != nil {
		blockResult := a.config.BeforeToolCall(BeforeToolCallContext{
			ToolCall: tc,
			Args:     params,
			Context:  a.context,
		})
		if blockResult != nil && blockResult.Block {
			reason := blockResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked"
			}
			a.sendEvent(ch, Event{
				Type:       EventToolExecutionEnd,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolResult: reason,
				ToolError:  fmt.Errorf("%s", reason),
			})
			return toolResult(reason, nil, true)
		}
	}

	// Check if tool needs user approval based on mode. Git metadata has an
	// independent one-shot approval because it is protected by the sandbox.
	gitAccessApproved := false
	if tc.Name == "bash" && a.config.Mode != "yolo" && a.config.Mode != "os" && a.config.SandboxMgr != nil && a.config.SandboxMgr.GetActive().Level() != sandbox.LevelNone {
		if command, ok := bashCommandArg(params); ok && sandbox.GitAccessRequired(command, "") {
			request := map[string]any{"command": command, "reason": "This command may access protected .git metadata. Allow once?"}
			gitAccessApproved = a.resolveToolApproval(ctx, ch, tc.ID, "git_access", request)
			if !gitAccessApproved {
				reason := "Git metadata access denied; .git is protected by the sandbox"
				a.sendEvent(ch, Event{Type: EventToolExecutionEnd, ToolCallID: tc.ID, ToolName: tc.Name, ToolResult: reason, ToolError: fmt.Errorf("%s", reason)})
				return toolResult(reason, nil, true)
			}
		}
	}
	if a.NeedsApproval(tc.Name, params) {
		approved := a.resolveToolApproval(ctx, ch, tc.ID, tc.Name, params)
		if !approved {
			reason := "Tool execution denied by user"
			a.sendEvent(ch, Event{
				Type:       EventToolExecutionEnd,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				ToolResult: reason,
				ToolError:  fmt.Errorf("%s", reason),
			})
			return toolResult(reason, nil, true)
		}
	}

	// Execute tool with timeout
	toolCtx, cancel := toolExecutionContext(ctx, tool, params)
	defer cancel()

	// Inject agent ID, event channel, and mode into context for sub-agent tools
	toolCtx = ContextWithAgentID(toolCtx, a.id)
	toolCtx = ContextWithEventChan(toolCtx, ch)
	toolCtx = ContextWithParentRunContext(toolCtx, ctx)
	toolCtx = ContextWithParentMode(toolCtx, a.config.Mode)
	toolCtx = tools.ContextWithQuestionAsker(toolCtx, a)
	toolCtx = sandbox.ContextWithGitAccess(toolCtx, gitAccessApproved)

	claimed, reused, err := a.claimToolExecutionWithRecovery(localTurnID, tc, params, allowReadOnlyRecovery)
	if err != nil {
		errMsg := fmt.Sprintf("record tool execution: %v", err)
		a.sendEvent(ch, Event{Type: EventToolExecutionEnd, ToolCallID: tc.ID, ToolName: tc.Name, ToolResult: errMsg, ToolError: err})
		return toolResult(errMsg, nil, true)
	}
	if reused != nil {
		reusedResult := *reused
		var reusedErr error
		if reusedResult.Contents != nil {
			reusedResult.Content, reusedResult.Contents, reusedResult.IsError, reusedErr = a.gateToolResultImages(reusedResult.Content, reusedResult.Contents, reusedResult.IsError)
			if reusedErr != nil {
				reusedResult.ToolKind = tc.Kind
			}
		}
		executionState := "reused"
		if reusedResult.IsError {
			executionState = "interrupted"
		}
		a.sendEvent(ch, Event{Type: EventToolExecutionEnd, ToolCallID: tc.ID, ToolName: tc.Name, ToolResult: reusedResult.Content, ToolError: reusedErr, ToolExecutionState: executionState, ToolImages: toolResultImages(reusedResult.Contents)})
		a.sendEvent(ch, Event{Type: EventToolResult, ToolCallID: tc.ID, ToolName: tc.Name, ToolResult: reusedResult.Content, ToolError: reusedErr, ToolExecutionState: executionState})
		return reusedResult
	}
	if claimed != nil {
		toolCtx = tools.ContextWithOperationID(toolCtx, claimed.ExecutionKey)
	}
	if a.config.BeforeToolExecute != nil {
		sideEffecting := isSideEffectingToolName(tc.Name)
		executionKey := ""
		if claimed != nil {
			sideEffecting = claimed.SideEffecting
			executionKey = claimed.ExecutionKey
		}
		blockResult := a.config.BeforeToolExecute(BeforeToolExecuteContext{
			ToolCall:         tc,
			Args:             params,
			Context:          a.context,
			ExecutionContext: toolCtx,
			RunID:            a.config.RunID,
			ExecutionKey:     executionKey,
			SideEffecting:    sideEffecting,
		})
		if blockResult != nil && blockResult.Block {
			reason := blockResult.Reason
			if reason == "" {
				reason = "Tool execution was blocked before the side effect fence"
			}
			a.sendEvent(ch, Event{Type: EventToolExecutionEnd, ToolCallID: tc.ID, ToolName: tc.Name, ToolResult: reason, ToolError: fmt.Errorf("%s", reason), ToolExecutionState: "interrupted"})
			return toolResult(reason, nil, true)
		}
	}

	result, err := tool.Execute(toolCtx, params)
	isError := err != nil
	resultContent := result.Text
	resultContents := result.Contents
	resultDiff := result.Diff
	resultPlan := result.Plan
	if err != nil {
		resultContent = err.Error()
		resultContents = nil
		resultDiff = nil
		resultPlan = nil
	}

	// Apply after-tool-call hook
	if a.config.AfterToolCall != nil {
		afterResult := a.config.AfterToolCall(AfterToolCallContext{
			ToolCall: tc,
			Args:     params,
			Result: ToolCallResult{
				Content: resultContent,
				IsError: isError,
			},
			IsError: isError,
			Context: a.context,
		})
		if afterResult != nil {
			if afterResult.Content != "" {
				resultContent = afterResult.Content
			}
			isError = afterResult.IsError
			resultContents = nil
			resultPlan = nil
		}
	}
	resultContent, resultContents, isError, imageErr := a.gateToolResultImages(resultContent, resultContents, isError)
	if imageErr != nil {
		err = imageErr
	}
	if claimed != nil {
		completed := time.Now()
		claimed.ExecutionState = "completed"
		claimed.ResultSummary = toolExecutionResultSummary(resultContent, isError)
		claimed.CompletedAt = &completed
		if updateErr := session.UpdateToolExecutionRecord(a.config.Session.GetSessionDir(), *claimed); updateErr != nil {
			log.Printf("[agent] failed to persist tool execution result: %v", updateErr)
		}
	}

	if resultPlan != nil {
		a.sendEvent(ch, Event{
			Type:       EventPlanUpdate,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Plan:       resultPlan,
		})
	}

	a.sendEvent(ch, Event{
		Type:       EventToolExecutionEnd,
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		ToolResult: resultContent,
		ToolDiff:   resultDiff,
		ToolError:  err,
		ToolImages: toolResultImages(resultContents),
	})
	a.sendEvent(ch, Event{
		Type:       EventToolResult,
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
		ToolResult: resultContent,
		ToolDiff:   resultDiff,
		ToolError:  err,
	})

	return toolResult(resultContent, resultContents, isError)
}

// resolveToolApproval resolves one tool approval. ctx is the run-level context
// of the calling agent: a cancelled run must unblock the wait so the tool batch
// (and therefore the run loop) can reach its terminal event instead of parking
// forever inside an approval prompt that can no longer be answered.
func (a *Agent) resolveToolApproval(ctx context.Context, ch chan<- Event, toolCallID, toolName string, args map[string]any) bool {
	if a.config.ApprovalDecisionLookup != nil {
		if approved, found := a.config.ApprovalDecisionLookup(toolCallID, toolName, args); found {
			return approved
		}
	}
	if a.config.ApprovalHandler != nil {
		return a.config.ApprovalHandler(toolCallID, toolName, args)
	}
	return a.RequestToolApproval(ctx, ch, toolCallID, toolName, args)
}

// claimToolExecution establishes a durable idempotency boundary immediately
// before execution. The same local session, turn, provider call id, tool name
// and normalized arguments always map to the same record.
func (a *Agent) claimToolExecution(localTurnID string, tc provider.ToolCallBlock, params map[string]any) (*session.ToolExecutionRecord, *provider.Message, error) {
	return a.claimToolExecutionWithRecovery(localTurnID, tc, params, false)
}

func (a *Agent) claimToolExecutionWithRecovery(localTurnID string, tc provider.ToolCallBlock, params map[string]any, allowReadOnlyRecovery bool) (*session.ToolExecutionRecord, *provider.Message, error) {
	if a.config.Session == nil || localTurnID == "" || tc.ID == "" {
		return nil, nil, nil
	}
	// The plan tool only emits a local UI state update. Persisting its execution
	// can turn an interrupted Responses run into a permanently stale plan, even
	// though replaying it has no external side effect.
	if tc.Name == "plan" {
		return nil, nil, nil
	}
	header := a.config.Session.GetHeader()
	if header == nil || header.ID == "" {
		return nil, nil, nil
	}
	normalizedArgs, err := json.Marshal(params)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize tool arguments: %w", err)
	}
	argsHashBytes := sha256.Sum256(normalizedArgs)
	argsHash := fmt.Sprintf("%x", argsHashBytes[:])
	keyInput := header.ID + "\x00" + localTurnID + "\x00" + tc.ID + "\x00" + tc.Name + "\x00" + argsHash
	keyHash := sha256.Sum256([]byte(keyInput))
	record := session.ToolExecutionRecord{
		SessionID:      header.ID,
		LocalTurnID:    localTurnID,
		ExecutionKey:   fmt.Sprintf("tool:%x", keyHash[:]),
		Provider:       a.config.Provider.Name(),
		API:            a.config.Provider.API(),
		ProviderCallID: tc.ID,
		ToolKind:       tc.Kind,
		ToolName:       tc.Name,
		ArgsHash:       argsHash,
		ExecutionState: "running",
		SideEffecting:  isSideEffectingToolName(tc.Name),
	}
	stored, created, err := session.ClaimToolExecutionRecord(a.config.Session.GetSessionDir(), record)
	if err != nil {
		return nil, nil, err
	}
	if created {
		return stored, nil, nil
	}
	if stored.ExecutionState == "completed" {
		content, isError := parseToolExecutionResultSummary(stored.ResultSummary)
		message := provider.NewToolResultMessage(tc.ID, tc.Name, content, isError)
		message.ToolKind = tc.Kind
		return nil, &message, nil
	}
	canRecover := allowReadOnlyRecovery && ((isReadOnlyToolName(tc.Name) && !stored.SideEffecting) || stored.ExecutionState == "retry_requested")
	if canRecover {
		reclaimed, err := session.ReclaimInterruptedToolExecution(a.config.Session.GetSessionDir(), stored.ExecutionKey)
		if err != nil {
			return nil, nil, err
		}
		if reclaimed {
			stored.ExecutionState = "running"
			stored.ResultSummary = nil
			stored.CompletedAt = nil
			return stored, nil, nil
		}
	}
	messageText := "Tool execution is already in progress or was interrupted; it was not repeated."
	if stored.SideEffecting {
		messageText += " This side effect has no verified external idempotency guarantee, so exactly-once execution cannot be promised."
	}
	message := provider.NewToolResultMessage(tc.ID, tc.Name, messageText, true)
	message.ToolKind = tc.Kind
	return nil, &message, nil
}

func isReadOnlyToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "grep", "find", "ls", "jobs", "skill_ref", "question", "plan":
		return true
	default:
		return false
	}
}

func isSideEffectingToolName(name string) bool {
	return !isReadOnlyToolName(name)
}

func toolExecutionResultSummary(content string, isError bool) json.RawMessage {
	encoded, err := json.Marshal(map[string]any{"content": content, "isError": isError})
	if err != nil {
		return nil
	}
	return encoded
}

func parseToolExecutionResultSummary(raw json.RawMessage) (string, bool) {
	var value struct {
		Content string `json:"content"`
		IsError bool   `json:"isError"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Content == "" {
		return "A prior tool execution completed; its result is available in the session transcript.", false
	}
	return value.Content, value.IsError
}

func toolExecutionContext(ctx context.Context, tool tools.Tool, params map[string]any) (context.Context, context.CancelFunc) {
	timeout := defaultToolExecutionTimeout
	if provider, ok := tool.(tools.ExecutionTimeoutProvider); ok {
		if custom, override := provider.ExecutionTimeout(params); override {
			timeout = custom
		}
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// GetMessages returns a copy of the current message history.
