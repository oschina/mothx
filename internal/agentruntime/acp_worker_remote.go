package agentruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
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
	session    *acpWorkerSession
	owned      bool
	handler    acpclient.RequestHandler
	turn       *acpWorkerTurn
	dead       bool
	closed     bool
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
		owned:      owned,
		handler:    invocation.HandleRequest,
	}
	notify := worker.handleNotification
	handler := worker.handleRequest
	additionalDirectories := acpSessionAdditionalDirectories(conn.init.AgentCapabilities, invocation.AdditionalDirectories)

	var session *acpWorkerSession
	if resumeID := strings.TrimSpace(invocation.ResumeSessionID); resumeID != "" {
		switch {
		case supportsSessionResume(conn.init.AgentCapabilities):
			session, err = conn.resumeSession(ctx, acpclient.ResumeSessionParams{
				SessionID:             resumeID,
				Cwd:                   invocation.WorkDir,
				AdditionalDirectories: additionalDirectories,
			}, handler, notify)
		case supportsSessionLoad(conn.init.AgentCapabilities):
			session, err = conn.loadSession(ctx, acpclient.LoadSessionParams{
				SessionID:             resumeID,
				Cwd:                   invocation.WorkDir,
				AdditionalDirectories: additionalDirectories,
			}, handler, notify)
		default:
			if owned {
				_ = conn.client.Close()
			}
			return nil, fmt.Errorf("ACP worker profile %q does not support session/resume or session/load for resume", invocation.Profile.ID)
		}
		if err != nil {
			if owned {
				_ = conn.client.Close()
			}
			return nil, fmt.Errorf("resume ACP worker session %q: %w", resumeID, err)
		}
	} else {
		session, err = conn.newSession(ctx, acpclient.NewSessionParams{
			Cwd:                   invocation.WorkDir,
			AdditionalDirectories: additionalDirectories,
		}, handler, notify)
		if err != nil {
			if owned {
				_ = conn.client.Close()
			}
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
	if strings.TrimSpace(invocation.ResumeSessionID) != "" && !supportsSessionResume(conn.init.AgentCapabilities) {
		session.start()
		// A dropped replay update means the loaded history is incomplete. The
		// turn still proceeds: the Runtime reports it as truncated evidence
		// rather than refusing to resume a session the Agent can serve.
		session.waitForPending()
	}
	return worker, nil
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
	w.session = nil
	w.mu.Unlock()

	var closeErr error
	if conn != nil && session != nil {
		closeCtx := context.Background()
		if ctx != nil {
			closeCtx = context.WithoutCancel(ctx)
		}
		var cancel context.CancelFunc
		closeCtx, cancel = context.WithTimeout(closeCtx, 2*time.Second)
		closeRemote := !dead && supportsSessionClose(conn.init.AgentCapabilities)
		closeErr = conn.closeSession(closeCtx, session, closeRemote)
		cancel()
	}
	if owned && conn != nil && conn.client != nil {
		_ = conn.client.Close()
	}
	return closeErr
}

func supportsSessionLoad(capabilities map[string]any) bool {
	if value, ok := capabilities["loadSession"].(bool); ok {
		return value
	}
	sessionCaps, ok := capabilities["sessionCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = sessionCaps["load"]
	return ok
}

func supportsSessionResume(capabilities map[string]any) bool {
	sessionCaps, ok := capabilities["sessionCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = sessionCaps["resume"]
	return ok
}

func supportsAdditionalDirectories(capabilities map[string]any) bool {
	sessionCaps, ok := capabilities["sessionCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = sessionCaps["additionalDirectories"]
	return ok
}

// acpSessionAdditionalDirectories returns caller-authorized roots only when
// the Agent advertised the matching session capability. ACP requires clients
// not to send this field otherwise; the Runtime filesystem bridge remains
// separately authorized by the session Registry.
func acpSessionAdditionalDirectories(capabilities map[string]any, directories []string) []string {
	if !supportsAdditionalDirectories(capabilities) || len(directories) == 0 {
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
