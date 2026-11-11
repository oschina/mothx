package agentruntime

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/config"
)

// acpRemoteWorker owns one remote ACP session inside an optionally pooled
// process. It is transport/session state only: Runtime constructs it from an
// already-authorized invocation and the child proxy owns the transcript and
// terminal event.
type acpRemoteWorker struct {
	mu         sync.Mutex
	invocation ACPWorkerInvocation
	conn       *acpWorkerConnection
	init       acpclient.InitializeResult
	caps       workerdriver.WorkerCapabilities
	session    *acpWorkerSession
	owned      bool
	handler    acpclient.RequestHandler
	turn       *acpWorkerTurn
	// revokeBridge releases this worker's bridge token. It is called on close
	// and on every Open failure after registration.
	revokeBridge func()
	dead         bool
	closed       bool
}

type acpWorkerTurn struct {
	mu        sync.Mutex
	emit      func(agentpkg.Event)
	output    strings.Builder
	truncated bool
	toolCalls int
}

func newACPWorkerTurn(emit func(agentpkg.Event)) *acpWorkerTurn {
	return &acpWorkerTurn{emit: emit}
}

// Open validates the invocation, obtains a pooled connection, then creates or
// loads exactly one remote session. A resume request never falls back to a new
// session: silently losing the remote transcript would be a policy change.
func (ACPWorkerBridge) Open(ctx context.Context, invocation ACPWorkerInvocation) (*acpRemoteWorker, error) {
	if err := validateACPWorkerInvocation(invocation); err != nil {
		return nil, err
	}
	var env []string
	if !invocation.Profile.IsRemote() {
		var err error
		env, err = acpWorkerEnvironment(invocation.Profile)
		if err != nil {
			return nil, err
		}
	}
	conn, owned, err := acquireACPWorkerConnection(ctx, invocation, env)
	if err != nil {
		return nil, err
	}
	worker := &acpRemoteWorker{
		invocation: invocation,
		conn:       conn,
		init:       conn.init,
		caps: resolveWorkerCapabilities(
			invocation.Profile, conn.init.AgentCapabilities, workerCapabilityPolicyFromInvocation(invocation),
		),
		owned:   owned,
		handler: invocation.HandleRequest,
	}
	notify := worker.handleNotification
	handler := worker.handleRequest
	additionalDirectories := acpSessionAdditionalDirectories(worker.caps, invocation.AdditionalDirectories)
	mcpServers := worker.prepareMCPServers(invocation)

	var session *acpWorkerSession
	if resumeID := strings.TrimSpace(invocation.ResumeSessionID); resumeID != "" {
		switch {
		case worker.caps.SessionResume:
			session, err = conn.resumeSession(ctx, acpclient.ResumeSessionParams{
				SessionID:             resumeID,
				Cwd:                   invocation.WorkDir,
				MCPServers:            mcpServers,
				AdditionalDirectories: additionalDirectories,
			}, handler, notify)
		case worker.caps.SessionLoad:
			session, err = conn.loadSession(ctx, acpclient.LoadSessionParams{
				SessionID:             resumeID,
				Cwd:                   invocation.WorkDir,
				MCPServers:            mcpServers,
				AdditionalDirectories: additionalDirectories,
			}, handler, notify)
		default:
			if owned {
				_ = conn.client.Close()
			}
			worker.revokeWorkerBridge()
			return nil, fmt.Errorf("ACP worker profile %q does not support session/resume or session/load for resume", invocation.Profile.ID)
		}
		if err != nil {
			if owned {
				_ = conn.client.Close()
			}
			worker.revokeWorkerBridge()
			return nil, fmt.Errorf("resume ACP worker session %q: %w", resumeID, err)
		}
	} else {
		session, err = conn.newSession(ctx, acpclient.NewSessionParams{
			Cwd:                   invocation.WorkDir,
			MCPServers:            mcpServers,
			AdditionalDirectories: additionalDirectories,
		}, handler, notify)
		if err != nil {
			if owned {
				_ = conn.client.Close()
			}
			worker.revokeWorkerBridge()
			if len(conn.init.AuthMethods) > 0 {
				return nil, fmt.Errorf("ACP worker profile %q requires local authentication handled by its own client; MothX does not relay credentials: %w", invocation.Profile.ID, err)
			}
			return nil, fmt.Errorf("create ACP worker session: %w", err)
		}
	}
	worker.session = session
	// A new session starts its projector lazily in Prompt, after the turn is
	// installed. A loaded session must drain the protocol-mandated history
	// replay now; otherwise those updates would enter the first new turn.
	if strings.TrimSpace(invocation.ResumeSessionID) != "" && !worker.caps.SessionResume {
		session.start()
		// A dropped replay update means the loaded history is incomplete. The
		// turn still proceeds: the Runtime reports it as truncated evidence
		// rather than refusing to resume a session the Agent can serve.
		session.waitForPending()
	}
	// Apply the administrator-configured model once the remote session exists.
	// An empty profile value means the external Agent keeps its own default.
	if err := worker.applyConfiguredModel(ctx); err != nil {
		_ = worker.Close(ctx)
		return nil, err
	}
	return worker, nil
}

// applyConfiguredModel selects the profile's model on the just-created remote
// session. It prefers the standard session config option and falls back to the
// deprecated session/set_model wire only when the Agent advertises no modern
// model option. A resume of an already-configured session may return no catalog;
// in that case the Agent's persisted session configuration stays in effect.
func (w *acpRemoteWorker) applyConfiguredModel(ctx context.Context) error {
	model := strings.TrimSpace(w.invocation.Profile.EffectiveModel())
	if model == "" {
		return nil
	}
	w.mu.Lock()
	conn := w.conn
	session := w.session
	resumed := strings.TrimSpace(w.invocation.ResumeSessionID) != ""
	w.mu.Unlock()
	if conn == nil || conn.client == nil || session == nil {
		return fmt.Errorf("ACP worker profile %q cannot apply model %q without an open session", w.invocation.Profile.ID, model)
	}
	if option, ok := acpModelConfigOption(session.configOptions); ok {
		return w.applyModelConfigOption(ctx, conn, session, option, model)
	}
	if session.models != nil {
		return w.applyLegacySessionModel(ctx, conn, session, session.models, model)
	}
	if resumed {
		// session/resume may return an empty result. The remote session still
		// owns the model chosen when it was created; do not fail a follow-up.
		return nil
	}
	return fmt.Errorf("ACP worker profile %q configures model %q, but the worker does not advertise a model config option", w.invocation.Profile.ID, model)
}

func (w *acpRemoteWorker) applyModelConfigOption(ctx context.Context, conn *acpWorkerConnection, session *acpWorkerSession, option acpclient.SessionConfigOption, model string) error {
	if acpConfigCurrentValue(option) == model {
		return nil
	}
	if choices := acpConfigOptionValues(option); len(choices) > 0 && !acpModelChoiceContains(choices, model) {
		return fmt.Errorf("ACP worker profile %q model %q is not offered by the worker (available: %s)", w.invocation.Profile.ID, model, strings.Join(choices, ", "))
	}
	result, err := conn.client.SetSessionConfigOption(ctx, acpclient.SetSessionConfigOptionParams{
		SessionID: session.id,
		ConfigID:  option.ID,
		Value:     model,
	})
	if err != nil {
		return fmt.Errorf("ACP worker profile %q select model %q: %w", w.invocation.Profile.ID, model, err)
	}
	if len(result.ConfigOptions) > 0 {
		session.configOptions = append([]acpclient.SessionConfigOption(nil), result.ConfigOptions...)
		session.models = nil
	}
	return nil
}

func (w *acpRemoteWorker) applyLegacySessionModel(ctx context.Context, conn *acpWorkerConnection, session *acpWorkerSession, state *acpclient.SessionModelState, model string) error {
	if strings.TrimSpace(state.CurrentModelID) == model {
		return nil
	}
	available := make([]string, 0, len(state.AvailableModels))
	for _, candidate := range state.AvailableModels {
		if value := strings.TrimSpace(candidate.ModelID); value != "" {
			available = append(available, value)
		}
	}
	if len(available) > 0 && !acpModelChoiceContains(available, model) {
		return fmt.Errorf("ACP worker profile %q model %q is not offered by the worker (available: %s)", w.invocation.Profile.ID, model, strings.Join(available, ", "))
	}
	if err := conn.client.SetSessionModel(ctx, acpclient.SetSessionModelParams{SessionID: session.id, ModelID: model}); err != nil {
		return fmt.Errorf("ACP worker profile %q select model %q: %w", w.invocation.Profile.ID, model, err)
	}
	state.CurrentModelID = model
	return nil
}

// acpModelConfigOption finds the Agent's model option by its stable category
// first, then by the conventional "model" id for older payloads that omit the
// category.
func acpModelConfigOption(options []acpclient.SessionConfigOption) (acpclient.SessionConfigOption, bool) {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Category), "model") && strings.TrimSpace(option.ID) != "" {
			return option, true
		}
	}
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.ID), "model") {
			return option, true
		}
	}
	return acpclient.SessionConfigOption{}, false
}

// acpConfigCurrentValue projects a session option's current value. Only a
// select's string value is meaningful for model selection; boolean options may
// share the same response and must not affect it.
func acpConfigCurrentValue(option acpclient.SessionConfigOption) string {
	value, _ := option.CurrentValue.(string)
	return strings.TrimSpace(value)
}

func acpConfigOptionValues(option acpclient.SessionConfigOption) []string {
	values := make([]string, 0, len(option.Options))
	for _, choice := range option.Options {
		if value := strings.TrimSpace(choice.Value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func acpModelChoiceContains(values []string, model string) bool {
	for _, value := range values {
		if value == model {
			return true
		}
	}
	return false
}

func validateACPWorkerInvocation(invocation ACPWorkerInvocation) error {
	if err := invocation.Profile.Validate(); err != nil {
		return err
	}
	if !invocation.Profile.Enabled {
		return fmt.Errorf("ACP worker profile %q is disabled", invocation.Profile.ID)
	}
	if strings.TrimSpace(invocation.WorkDir) == "" || !filepath.IsAbs(invocation.WorkDir) {
		return fmt.Errorf("ACP worker requires an absolute work directory")
	}
	if err := invocation.Contract.Validate(invocation.Profile); err != nil {
		return err
	}
	if invocation.Profile.EffectiveTransport() == config.ACPAgentTransportStdio && invocation.Launcher == nil {
		return fmt.Errorf("ACP worker requires a Runtime process launcher")
	}
	return nil
}

func (w *acpRemoteWorker) SessionID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.session == nil {
		return ""
	}
	return w.session.id
}

func (w *acpRemoteWorker) Capabilities() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.init.AgentCapabilities
}

// WorkerCapabilities returns the resolved capability set. Callers must read
// this instead of re-probing the raw ACP advertisement.
func (w *acpRemoteWorker) WorkerCapabilities() workerdriver.WorkerCapabilities {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.caps
}

// prepareMCPServers asks the Runtime for an authorized loopback bridge payload
// after initialize resolved the capability set. A failure is a degradation, not
// a task failure: the worker simply runs without the bridge, and the resolved
// capability/metadata projection records mcpBridge=unavailable.
func (w *acpRemoteWorker) prepareMCPServers(invocation ACPWorkerInvocation) []any {
	w.mu.Lock()
	caps := w.caps
	w.mu.Unlock()
	if invocation.PrepareMCPServers == nil || !caps.MCPBridge() {
		return nil
	}
	servers, revoke, err := invocation.PrepareMCPServers(caps)
	if err != nil {
		log.Printf("[agentruntime] ACP worker bridge unavailable: %v", err)
		w.degradeMCPBridge(err)
		return nil
	}
	if revoke != nil {
		w.mu.Lock()
		w.revokeBridge = revoke
		w.mu.Unlock()
	}
	return servers
}

// degradeMCPBridge tightens the resolved capability set after bridge
// registration or listener startup failed. Child metadata and the model-facing
// summary are derived from this value, so they cannot keep advertising an
// available bridge. The prompt itself is unaffected.
func (w *acpRemoteWorker) degradeMCPBridge(cause error) {
	reason := "mcpHttp: worker bridge registration failed"
	if cause != nil {
		message := strings.TrimSpace(cause.Error())
		if len(message) > 200 {
			message = truncateUTF8Bytes(message, 200)
		}
		if message != "" {
			reason += ": " + message
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.caps.MCPHTTP || w.caps.MCPSSE || w.caps.MCPStdio {
		w.caps.Degradations = append(w.caps.Degradations, reason)
	}
	w.caps.MCPHTTP = false
	w.caps.MCPSSE = false
	w.caps.MCPStdio = false
	w.caps.Source = "degraded"
}

func (w *acpRemoteWorker) revokeWorkerBridge() {
	w.mu.Lock()
	revoke := w.revokeBridge
	w.revokeBridge = nil
	w.mu.Unlock()
	if revoke != nil {
		revoke()
	}
}

func (w *acpRemoteWorker) AgentInfo() acpclient.Implementation {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.init.AgentInfo
}

func (w *acpRemoteWorker) Dead() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dead || w.closed || w.session == nil
}

func (w *acpRemoteWorker) SetRequestHandler(handler acpclient.RequestHandler) {
	w.mu.Lock()
	w.handler = handler
	w.mu.Unlock()
}

func (w *acpRemoteWorker) handleRequest(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
	w.mu.Lock()
	handler := w.handler
	w.mu.Unlock()
	if handler == nil {
		return nil, &acpclient.RPCError{Code: -32601, Message: "session method not found"}
	}
	return handler(ctx, message)
}

func (w *acpRemoteWorker) handleNotification(message acpclient.Message) {
	notification, ok := acpclient.DecodeSessionNotification(message)
	if !ok {
		return
	}
	w.mu.Lock()
	sessionID := ""
	if w.session != nil {
		sessionID = w.session.id
	}
	turn := w.turn
	onNotification := w.invocation.OnNotification
	w.mu.Unlock()
	if sessionID == "" || notification.SessionID != sessionID {
		return
	}
	if onNotification != nil {
		onNotification(message)
	}
	if turn != nil {
		turn.apply(notification.Update)
	}
}

// Prompt sends exactly one prompt to the existing remote session. Notifications
// are applied synchronously by the client reader before the prompt response, so
// the returned turn state includes every update that preceded completion.
func (w *acpRemoteWorker) Prompt(ctx context.Context, prompt []acpclient.ContentBlock, emit func(agentpkg.Event)) (ACPWorkerResult, error) {
	w.mu.Lock()
	if w.closed || w.dead || w.session == nil {
		w.mu.Unlock()
		return ACPWorkerResult{}, fmt.Errorf("ACP worker session is not available")
	}
	conn := w.conn
	session := w.session
	agentInfo := w.init.AgentInfo
	turn := newACPWorkerTurn(emit)
	w.turn = turn
	w.mu.Unlock()
	session.start()
	defer func() {
		w.mu.Lock()
		if w.turn == turn {
			w.turn = nil
		}
		w.mu.Unlock()
	}()

	cancelDone := make(chan struct{})
	defer close(cancelDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.client.Cancel(session.id)
		case <-cancelDone:
		}
	}()
	response, err := conn.client.Prompt(ctx, acpclient.PromptParams{
		SessionID: session.id,
		Prompt:    prompt,
	})
	dropped := session.waitForPending()
	result := turn.result(agentInfo, session.id)
	if dropped > 0 {
		// The oldest notifications were discarded because the Agent outran the
		// projection. The transcript is missing its beginning, so the turn is
		// incomplete rather than a shorter successful answer.
		result.Truncated = true
		result.DroppedUpdates = dropped
	}
	if err != nil {
		if ctx.Err() == nil {
			w.mu.Lock()
			w.dead = true
			w.mu.Unlock()
		}
		result.StopReason = "error"
		return result, fmt.Errorf("run ACP worker prompt: %w", err)
	}
	result.StopReason = response.StopReason
	return result, nil
}

func (w *acpRemoteWorker) Close(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	conn := w.conn
	session := w.session
	owned := w.owned
	dead := w.dead
	revokeBridge := w.revokeBridge
	w.revokeBridge = nil
	w.session = nil
	w.mu.Unlock()
	if revokeBridge != nil {
		revokeBridge()
	}

	var closeErr error
	if conn != nil && session != nil {
		closeCtx := context.Background()
		if ctx != nil {
			closeCtx = context.WithoutCancel(ctx)
		}
		var cancel context.CancelFunc
		closeCtx, cancel = context.WithTimeout(closeCtx, 2*time.Second)
		closeRemote := !dead && w.caps.SessionClose
		closeErr = conn.closeSession(closeCtx, session, closeRemote)
		cancel()
	}
	if owned && conn != nil && conn.client != nil {
		_ = conn.client.Close()
	}
	return closeErr
}

// acpSessionAdditionalDirectories returns caller-authorized roots only when
// the resolved capability allows the field. ACP requires clients not to send
// this field otherwise; the Runtime filesystem bridge remains separately
// authorized by the session Registry.
func acpSessionAdditionalDirectories(caps workerdriver.WorkerCapabilities, directories []string) []string {
	if !caps.AdditionalDirectories || len(directories) == 0 {
		return nil
	}
	return append([]string(nil), directories...)
}

func (t *acpWorkerTurn) apply(update acpclient.SessionUpdate) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch update.SessionUpdate {
	case "agent_message_chunk":
		if update.Content == nil || update.Content.Text == "" {
			return
		}
		text := update.Content.Text
		// The parent completion envelope is bounded, but the child event
		// projection is not: consumers rendering a live worker transcript must
		// still receive text after the bounded result is full. Keep appending
		// only the remaining result bytes so Output stays <= maxSubagentResultBytes.
		if remaining := maxSubagentResultBytes - t.output.Len(); remaining > 0 {
			piece, pieceTruncated := truncateACPText(text, remaining)
			t.output.WriteString(piece)
			if pieceTruncated {
				t.truncated = true
			}
		} else {
			t.truncated = true
		}
		t.emitEvent(agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: text})
	case "agent_thought_chunk":
		if update.Content != nil && update.Content.Text != "" {
			text, _ := boundACPText(update.Content.Text)
			t.emitEvent(agentpkg.Event{Type: agentpkg.EventThinkDelta, ThinkDelta: text})
		}
	case "tool_call":
		t.toolCalls++
		title, _ := boundACPText(update.Title)
		t.emitEvent(agentpkg.Event{
			Type:       agentpkg.EventToolCall,
			ToolCallID: acpRemoteToolCallID(update.ToolCallID),
			ToolName:   title,
			ToolArgs:   decodeACPToolInput(update.RawInput),
			ToolCall: &agentpkg.ToolCallBlock{
				ID: acpRemoteToolCallID(update.ToolCallID), Name: title,
				Kind: update.Kind, Input: string(update.RawInput),
			},
		})
	case "tool_call_update":
		eventType := agentpkg.EventToolExecutionUpdate
		if update.Status == "completed" || update.Status == "failed" {
			eventType = agentpkg.EventToolExecutionEnd
		}
		title, _ := boundACPText(update.Title)
		result, _ := boundACPText(string(update.RawOutput))
		event := agentpkg.Event{
			Type:       eventType,
			ToolCallID: acpRemoteToolCallID(update.ToolCallID),
			ToolName:   title,
			ToolResult: result,
		}
		if update.Status == "failed" {
			event.ToolError = fmt.Errorf("remote tool call failed")
		}
		t.emitEvent(event)
	case "plan":
		steps := make([]agentpkg.PlanStep, 0, len(update.Entries))
		for _, entry := range update.Entries {
			content, _ := boundACPText(entry.Content)
			steps = append(steps, agentpkg.PlanStep{Title: content, Status: entry.Status})
		}
		t.emitEvent(agentpkg.Event{Type: agentpkg.EventPlanUpdate, Plan: &agentpkg.TaskPlan{Steps: steps}})
	case "usage_update":
		t.emitEvent(agentpkg.Event{
			Type:         agentpkg.EventUsage,
			ContextUsage: &agentpkg.ContextUsage{TotalTokens: update.Used, ContextWindow: update.Size},
		})
	}
}

func (t *acpWorkerTurn) emitEvent(event agentpkg.Event) {
	if t.emit != nil {
		t.emit(event)
	}
}

func (t *acpWorkerTurn) result(agentInfo acpclient.Implementation, sessionID string) ACPWorkerResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return ACPWorkerResult{
		AgentInfo: agentInfo, SessionID: sessionID,
		Output: t.output.String(), Truncated: t.truncated, ToolCalls: t.toolCalls,
	}
}
