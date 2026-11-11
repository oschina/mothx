package agentruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/agentruntime/workerdriver"
	"github.com/oschina/mothx/internal/mcp"
)

// workerMgrToolName is the single MCP tool a Router worker may call.
const workerMgrToolName = "worker_mgr"

// Worker bridge limits.
const (
	workerBridgeTokenBytes = 32
	workerBridgeRateLimit  = 20
	workerBridgeRateWindow = time.Minute
	// DefaultWorkerAskTimeout bounds one blocking worker question. Runtime policy
	// may lower or raise it within MaxWorkerAskTimeout.
	DefaultWorkerAskTimeout = 10 * time.Minute
	MaxWorkerAskTimeout     = 30 * time.Minute
)

// WorkerAskFunc resolves one worker question. Runtime installs the concrete
// DecisionService-backed implementation; the bridge only blocks on it.
type WorkerAskFunc func(ctx context.Context, question string, options []string, timeout time.Duration) (answer string, status string)

// WorkerBridgeScope is the per-child authorization and state bound to one MCP
// token. It contains no enumerable session or child parameter: the token is the
// only way a request can name a scope.
type WorkerBridgeScope struct {
	SessionID    string
	ChildID      string
	ParentRunID  string
	ProfileID    string
	WorkDir      string
	TaskPrompt   string
	Capabilities workerdriver.WorkerCapabilities
	Messages     *WorkerMessageLog
	// Notify receives a finding that should enter lead context. It must not
	// block; the bridge handler already owns request serialization.
	Notify func(WorkerMessage)
	// Ask, when installed by Runtime, resolves a blocking worker question.
	Ask WorkerAskFunc

	rateMu    sync.Mutex
	rateStart time.Time
	rateCount int
	// askMu bounds concurrent blocking questions. The proposal default is one
	// pending ask per child; a second concurrent ask is rejected instead of
	// queueing behind a possibly long decision.
	askMu      sync.Mutex
	activeAsks int
	terminal   bool
}

func (s *WorkerBridgeScope) beginAsk() bool {
	if s == nil {
		return false
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.activeAsks >= 1 {
		return false
	}
	s.activeAsks++
	return true
}

func (s *WorkerBridgeScope) endAsk() {
	if s == nil {
		return
	}
	s.askMu.Lock()
	if s.activeAsks > 0 {
		s.activeAsks--
	}
	s.askMu.Unlock()
}

func (s *WorkerBridgeScope) isTerminal() bool {
	if s == nil {
		return false
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()
	return s.terminal
}

func (s *WorkerBridgeScope) setTerminal(terminal bool) {
	if s == nil {
		return
	}
	s.askMu.Lock()
	s.terminal = terminal
	s.askMu.Unlock()
}

func (s *WorkerBridgeScope) allowRate() bool {
	if s == nil {
		return false
	}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := time.Now()
	if s.rateStart.IsZero() || now.Sub(s.rateStart) >= workerBridgeRateWindow {
		s.rateStart = now
		s.rateCount = 0
	}
	if s.rateCount >= workerBridgeRateLimit {
		return false
	}
	s.rateCount++
	return true
}

// WorkerBridgeServer owns the process-wide loopback MCP listener and the token
// registry. It is created by top-level assembly and shared by every session in
// the process; a session only registers child scopes and revokes them on
// shutdown.
type WorkerBridgeServer struct {
	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	port     int
	tokens   map[string]*WorkerBridgeScope
	byChild  map[string]string
	closed   bool
	// idleTimeout closes the listener after the last worker token is released.
	// The next registration binds a fresh ephemeral port.
	idleTimeout time.Duration
	idleTimer   *time.Timer
}

// NewWorkerBridgeServer creates a bridge server. The listener is lazy: the first
// worker registration binds a tcp4 loopback port selected by the kernel.
func NewWorkerBridgeServer() *WorkerBridgeServer {
	return &WorkerBridgeServer{
		tokens: map[string]*WorkerBridgeScope{}, byChild: map[string]string{},
		idleTimeout: workerBridgeIdleTimeout,
	}
}

// workerBridgeIdleTimeout is how long an unused listener stays bound. A new
// worker registration after the timeout rebinds a fresh ephemeral port.
const workerBridgeIdleTimeout = 5 * time.Minute

// SetIdleTimeout overrides the idle shutdown window. It is intended for tests
// and diagnostics; a non-positive value restores the default.
func (s *WorkerBridgeServer) SetIdleTimeout(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if timeout <= 0 {
		timeout = workerBridgeIdleTimeout
	}
	s.idleTimeout = timeout
}

// EnsureStarted binds 127.0.0.1:0 on demand. Using an ephemeral tcp4 port means
// several mothx processes (TUI, serve, ACP Desktop, tests) never collide.
func (s *WorkerBridgeServer) EnsureStarted() (int, error) {
	if s == nil {
		return 0, fmt.Errorf("worker bridge is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureStartedLocked()
}

// ensureStartedLocked binds the listener if needed. Callers hold s.mu, which
// lets Register install its token atomically with the listener start so a firing
// idle timer cannot close the port between the two steps.
func (s *WorkerBridgeServer) ensureStartedLocked() (int, error) {
	if s.closed {
		return 0, fmt.Errorf("worker bridge is closed")
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	if s.listener != nil {
		return s.port, nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("start worker bridge listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{
		Handler:           s.authHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		// A worker report is a short request; a long ask is still bounded by the
		// question deadline. Keep the server-side read/write windows generous
		// enough for a blocked ask but finite.
		ReadTimeout:  35 * time.Minute,
		WriteTimeout: 35 * time.Minute,
	}
	s.listener = listener
	s.server = server
	s.port = port
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("[agentruntime] worker bridge listener stopped: %v", serveErr)
		}
	}()
	return port, nil
}

// Available reports whether this process can expose a bridge. A nil bridge is
// not available; a created-but-not-yet-started bridge can still be started on
// demand, so it reports true.
func (s *WorkerBridgeServer) Available() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// Port returns the bound loopback port, or zero before the first start.
func (s *WorkerBridgeServer) Port() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Register issues an opaque per-child token and returns the authorized
// session/new mcpServers payload. A previous token for the same child is
// revoked: one child has exactly one live bridge credential.
func (s *WorkerBridgeServer) Register(scope *WorkerBridgeScope) (string, []any, error) {
	if s == nil || scope == nil {
		return "", nil, fmt.Errorf("worker bridge scope is required")
	}
	if strings.TrimSpace(scope.ChildID) == "" || strings.TrimSpace(scope.SessionID) == "" {
		return "", nil, fmt.Errorf("worker bridge scope requires session and child ids")
	}
	if !scope.Capabilities.MCPBridge() {
		return "", nil, fmt.Errorf("worker capabilities do not allow the MCP bridge")
	}
	s.mu.Lock()
	port, err := s.ensureStartedLocked()
	if err != nil {
		s.mu.Unlock()
		return "", nil, err
	}
	token, err := newWorkerBridgeToken()
	if err != nil {
		// A listener may have been bound and its idle timer cleared:
		// schedule idle shutdown so a failed registration does not retain it.
		s.scheduleIdleCloseLocked()
		s.mu.Unlock()
		return "", nil, err
	}
	// The mutex has been held continuously since ensureStartedLocked cleared
	// the idle timer, so no idle close can race the token installation below.
	childKey := workerBridgeChildKey(scope.SessionID, scope.ChildID)
	if previous := s.byChild[childKey]; previous != "" {
		delete(s.tokens, previous)
	}
	s.tokens[token] = scope
	s.byChild[childKey] = token
	s.mu.Unlock()
	payload := []any{map[string]any{
		"type": "http",
		"name": "mothx-worker",
		"url":  fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
		"headers": []any{
			map[string]any{"name": "Authorization", "value": "Bearer " + token},
		},
	}}
	return token, payload, nil
}

// Revoke removes one token. It is safe on an unknown or already-revoked token.
func (s *WorkerBridgeServer) Revoke(token string) {
	if s == nil || token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scope := s.tokens[token]
	delete(s.tokens, token)
	if scope != nil {
		childKey := workerBridgeChildKey(scope.SessionID, scope.ChildID)
		if s.byChild[childKey] == token {
			delete(s.byChild, childKey)
		}
	}
	if len(s.tokens) == 0 {
		s.scheduleIdleCloseLocked()
	}
}

// scheduleIdleCloseLocked stops the listener after the idle window when no
// worker token remains. The next EnsureStarted binds a fresh port; this is why
// a long-lived process never keeps an unused listener open indefinitely.
func (s *WorkerBridgeServer) scheduleIdleCloseLocked() {
	if s.closed || s.listener == nil || s.idleTimer != nil {
		return
	}
	timeout := s.idleTimeout
	if timeout <= 0 {
		timeout = workerBridgeIdleTimeout
	}
	s.idleTimer = time.AfterFunc(timeout, func() {
		s.mu.Lock()
		s.idleTimer = nil
		if s.closed || len(s.tokens) > 0 || s.listener == nil {
			s.mu.Unlock()
			return
		}
		server := s.server
		listener := s.listener
		port := s.port
		s.server = nil
		s.listener = nil
		s.mu.Unlock()
		// Shutdown outside the lock: it waits for in-flight requests, whose
		// authentication path needs the same mutex.
		if server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
		}
		if listener != nil {
			_ = listener.Close()
		}
		// Publish port 0 only after the listener is actually closed, and only if
		// a concurrent registration has not already bound a replacement.
		s.mu.Lock()
		if s.listener == nil && s.server == nil && s.port == port {
			s.port = 0
		}
		s.mu.Unlock()
	})
}

// RevokeChild removes one session-scoped child's live bridge credential.
func (s *WorkerBridgeServer) RevokeChild(sessionID, childID string) {
	if s == nil || sessionID == "" || childID == "" {
		return
	}
	s.mu.Lock()
	token := s.byChild[workerBridgeChildKey(sessionID, childID)]
	s.mu.Unlock()
	s.Revoke(token)
}

func workerBridgeChildKey(sessionID, childID string) string {
	return sessionID + "\x00" + childID
}

// SetChildTerminal records whether the child's current turn has reached a
// terminal state. A call after the terminal event is rejected as
// child_terminal; a follow-up turn marks the scope active again.
func (s *WorkerBridgeServer) SetChildTerminal(sessionID, childID string, terminal bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	scope := s.tokens[s.byChild[workerBridgeChildKey(sessionID, childID)]]
	s.mu.Unlock()
	scope.setTerminal(terminal)
}

// RevokeSession removes every credential belonging to one session. Session
// shutdown calls this; the shared listener stays up for other sessions.
func (s *WorkerBridgeServer) RevokeSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	tokens := make([]string, 0)
	for token, scope := range s.tokens {
		if scope.SessionID == sessionID {
			tokens = append(tokens, token)
		}
	}
	s.mu.Unlock()
	for _, token := range tokens {
		s.Revoke(token)
	}
}

// Close stops the listener and drops every token. It is idempotent and owned by
// top-level process shutdown, not by a session.
func (s *WorkerBridgeServer) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	server := s.server
	listener := s.listener
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.tokens = map[string]*WorkerBridgeScope{}
	s.byChild = map[string]string{}
	s.mu.Unlock()
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	if listener != nil {
		_ = listener.Close()
	}
	return nil
}

func newWorkerBridgeToken() (string, error) {
	raw := make([]byte, workerBridgeTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate worker bridge token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

type workerScopeContextKey struct{}

func (s *WorkerBridgeServer) authHandler() http.Handler {
	mcpHandler := mcp.NewHTTPHandler(&workerBridgeMCPHandler{})
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !workerBridgeLoopbackRequest(request) {
			// The bridge is loopback-only by construction; reject a proxied or
			// forwarded request before it can present a valid bearer token.
			http.Error(response, "forbidden", http.StatusForbidden)
			log.Printf("[agentruntime] worker bridge request rejected: reason=non_loopback")
			return
		}
		token := bearerToken(request.Header.Get("Authorization"))
		scope := s.scope(token)
		if scope == nil {
			// Never echo the presented token.
			response.Header().Set("WWW-Authenticate", `Bearer realm="mothx-worker"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			log.Printf("[agentruntime] worker bridge request rejected: reason=unauthorized")
			return
		}
		tool, bytesRead := peekWorkerTool(request)
		mcpHandler.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), workerScopeContextKey{}, scope)))
		log.Printf("[agentruntime] worker bridge request: session=%s child=%s tool=%s bytes=%d", scope.SessionID, scope.ChildID, tool, bytesRead)
	})
}

func (s *WorkerBridgeServer) scope(token string) *WorkerBridgeScope {
	if s == nil || token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[token]
}

func bearerToken(header string) string {
	value := strings.TrimSpace(header)
	const prefix = "bearer "
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(value[len(prefix):])
}

// workerBridgeLoopbackRequest accepts only a direct loopback peer. The listener
// is already tcp4 on 127.0.0.1, but an explicit RemoteAddr check keeps a
// misconfigured proxy or test harness from exercising the worker surface.
func workerBridgeLoopbackRequest(request *http.Request) bool {
	if request == nil {
		return false
	}
	host := request.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

// peekWorkerTool extracts only the tool name and body size for bounded logging.
// It never logs report or question text.
func peekWorkerTool(request *http.Request) (string, int64) {
	if request.Body == nil || request.ContentLength <= 0 || request.ContentLength > 1<<20 {
		return request.Method, request.ContentLength
	}
	// The MCP handler reads the body once; a peek would consume it. Log the
	// method only for requests that carry a body.
	return "tools/call", request.ContentLength
}

// workerBridgeMCPHandler is the MCP ServerHandler for the bridge. It exposes
// exactly one tool; the token scope supplies identity.
type workerBridgeMCPHandler struct{}

func (h *workerBridgeMCPHandler) ServerName() string { return "mothx-worker" }

func (h *workerBridgeMCPHandler) ListTools(context.Context) ([]mcp.ServerTool, error) {
	return []mcp.ServerTool{{
		Name:        workerMgrToolName,
		Description: "Report progress or findings to the lead, ask the lead a question, or read the task contract. You cannot spawn, destroy, or message other workers.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["context", "report", "ask"]},
				"kind": {"type": "string", "enum": ["notice", "progress", "finding"]},
				"text": {"type": "string"},
				"dedupe_key": {"type": "string"},
				"options": {"type": "array", "items": {"type": "string"}},
				"timeout_seconds": {"type": "integer"}
			},
			"required": ["action"]
		}`),
	}}, nil
}

func (h *workerBridgeMCPHandler) CallTool(ctx context.Context, name string, arguments json.RawMessage) (mcp.ServerToolResult, error) {
	if name != workerMgrToolName {
		return mcp.ServerToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
	scope, ok := ctx.Value(workerScopeContextKey{}).(*WorkerBridgeScope)
	if !ok || scope == nil {
		return mcp.ServerToolResult{}, fmt.Errorf("worker bridge scope is missing")
	}
	if scope.isTerminal() {
		return mcp.ServerToolResult{}, fmt.Errorf("child_terminal: the worker turn has ended; no message was accepted")
	}
	var params struct {
		Action         string   `json:"action"`
		Kind           string   `json:"kind"`
		Text           string   `json:"text"`
		DedupeKey      string   `json:"dedupe_key"`
		Options        []string `json:"options"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &params); err != nil {
			return mcp.ServerToolResult{}, fmt.Errorf("decode worker_mgr arguments: %w", err)
		}
	}
	switch strings.TrimSpace(params.Action) {
	case "context":
		return workerBridgeJSON(map[string]any{
			"action": "context", "status": "ok",
			"task": scope.TaskPrompt, "work_dir": scope.WorkDir,
			"bridge": map[string]any{"report": true, "ask": scope.Ask != nil, "wake": "finding"},
			"capabilities": map[string]any{
				"sessionResume": scope.Capabilities.SessionResume,
				"sessionLoad":   scope.Capabilities.SessionLoad,
				"promptImage":   scope.Capabilities.PromptImage,
				"fsWriteText":   scope.Capabilities.FSWriteText,
				"terminal":      scope.Capabilities.Terminal,
			},
		})
	case "report":
		return h.report(scope, params.Kind, params.Text, params.DedupeKey)
	case "ask":
		return h.ask(ctx, scope, params.Text, params.Options, params.TimeoutSeconds)
	default:
		return mcp.ServerToolResult{}, fmt.Errorf("unknown action %q; use context, report, or ask", params.Action)
	}
}

func (h *workerBridgeMCPHandler) report(scope *WorkerBridgeScope, kind, text, dedupeKey string) (mcp.ServerToolResult, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case WorkerMessageNotice, WorkerMessageProgress, WorkerMessageFinding:
	default:
		return mcp.ServerToolResult{}, fmt.Errorf("report kind must be notice, progress, or finding")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return mcp.ServerToolResult{}, fmt.Errorf("report text is required")
	}
	if len(text) > MaxWorkerReportBytes {
		return mcp.ServerToolResult{}, fmt.Errorf("report text exceeds %d bytes", MaxWorkerReportBytes)
	}
	if scope.Messages == nil {
		return mcp.ServerToolResult{}, fmt.Errorf("worker message log is not available")
	}
	if !scope.allowRate() {
		return mcp.ServerToolResult{}, fmt.Errorf("worker message rate limit exceeded (%d per %s)", workerBridgeRateLimit, workerBridgeRateWindow)
	}
	message, duplicate, err := scope.Messages.Create(WorkerMessage{
		SessionID: scope.SessionID, ChildID: scope.ChildID, ParentRunID: scope.ParentRunID,
		Kind: kind, Body: text, DedupeKey: dedupeKey,
	})
	if err != nil {
		return mcp.ServerToolResult{}, err
	}
	delivery := "stored_only"
	if kind == WorkerMessageFinding && !duplicate && scope.Notify != nil {
		scope.Notify(message)
		delivery = "delivered"
	}
	return workerBridgeJSON(map[string]any{
		"action": "report", "status": "recorded", "message_id": message.ID,
		"kind": kind, "delivery": delivery, "duplicate": duplicate,
	})
}

func (h *workerBridgeMCPHandler) ask(ctx context.Context, scope *WorkerBridgeScope, text string, options []string, timeoutSeconds int) (mcp.ServerToolResult, error) {
	if scope.Ask == nil {
		return mcp.ServerToolResult{}, fmt.Errorf("ask is not available for this worker")
	}
	if !scope.beginAsk() {
		return mcp.ServerToolResult{}, fmt.Errorf("only one pending question is allowed per worker")
	}
	defer scope.endAsk()
	text = strings.TrimSpace(text)
	if text == "" {
		return mcp.ServerToolResult{}, fmt.Errorf("ask text is required")
	}
	if len(text) > MaxWorkerAskBytes {
		return mcp.ServerToolResult{}, fmt.Errorf("ask text exceeds %d bytes", MaxWorkerAskBytes)
	}
	if len(options) > MaxWorkerAskOptions {
		return mcp.ServerToolResult{}, fmt.Errorf("ask options exceed %d", MaxWorkerAskOptions)
	}
	optionBytes := 0
	for _, option := range options {
		if len(option) > MaxWorkerAskOptionBytes {
			return mcp.ServerToolResult{}, fmt.Errorf("ask option exceeds %d bytes", MaxWorkerAskOptionBytes)
		}
		optionBytes += len(option)
	}
	if optionBytes > MaxWorkerAskOptionsBytes {
		return mcp.ServerToolResult{}, fmt.Errorf("ask options exceed %d bytes in total", MaxWorkerAskOptionsBytes)
	}
	if !scope.allowRate() {
		return mcp.ServerToolResult{}, fmt.Errorf("worker message rate limit exceeded (%d per %s)", workerBridgeRateLimit, workerBridgeRateWindow)
	}
	timeout := DefaultWorkerAskTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	if timeout > MaxWorkerAskTimeout {
		timeout = MaxWorkerAskTimeout
	}
	// The JSONL question row is owned by the Runtime-installed Ask implementation
	// (askWorkerQuestion): it is the only place that knows the durable DecisionID,
	// so writing a second row here would leave one message permanently "created".
	answer, status := scope.Ask(ctx, text, append([]string(nil), options...), timeout)
	return workerBridgeJSON(map[string]any{
		"action": "ask", "status": status, "answer": answer,
	})
}

func workerBridgeJSON(value map[string]any) (mcp.ServerToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return mcp.ServerToolResult{}, err
	}
	return mcp.ServerToolResult{Content: []mcp.ServerContent{{Type: "text", Text: string(encoded)}}}, nil
}
