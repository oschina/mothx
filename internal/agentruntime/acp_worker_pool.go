package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

// acpConnectionPool owns reusable ACP processes for one SessionRuntime. ACP
// Agents commonly host multiple sessions in one process, so a new child task
// should not pay process startup and initialization again. Sessions remain
// isolated by remote session ID; Runtime still owns admission, policy, events,
// and cancellation.
type acpConnectionPool struct {
	rootCtx context.Context
	cancel  context.CancelFunc

	mu     sync.Mutex
	closed bool
	conns  map[string]*acpWorkerConnection
}

type acpWorkerConnection struct {
	key     string
	profile config.ACPAgentProfile
	client  *acpclient.Client
	init    acpclient.InitializeResult
	ready   chan struct{}
	initErr error

	mu sync.Mutex
	// sessions is registered only after session/new returns. A compliant Agent
	// may emit an update immediately after that response, before the caller has
	// observed it, so early notifications are retained per session ID and handed
	// to the session when it is registered. Bounds prevent an untrusted Agent
	// from growing this buffer without limit.
	sessions             map[string]*acpWorkerSession
	earlyNotifications   map[string][]acpclient.Message
	earlyNotificationIDs int
}

type acpWorkerSession struct {
	connection     *acpWorkerConnection
	id             string
	handleRequest  acpclient.RequestHandler
	onNotification func(acpclient.Message)

	// configOptions and models are the Agent-advertised session configuration
	// from session/new/load/resume. They are immutable for the session lifetime
	// and only used to apply the profile's configured model.
	configOptions []acpclient.SessionConfigOption
	models        *acpclient.SessionModelState

	done chan struct{}

	mu   sync.Mutex
	cond *sync.Cond
	// pending holds notifications this session has not projected yet. It is a
	// slice rather than a channel because the ACP client delivers
	// notifications on the shared connection's read loop: a blocking send
	// would stall every other session hosted by the same pooled process.
	pending []acpclient.Message
	started bool
	closed  bool
	// accepted counts notifications this session took ownership of, and
	// handled counts the ones that were projected (or dropped by the bound).
	accepted uint64
	handled  uint64
	dropped  uint64
}

func newACPConnectionPool() *acpConnectionPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &acpConnectionPool{
		rootCtx: ctx,
		cancel:  cancel,
		conns:   make(map[string]*acpWorkerConnection),
	}
}

func (r *SessionRuntime) acpConnectionPool() (*acpConnectionPool, error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("agent runtime is closed")
	}
	if r.acpConnections == nil {
		r.acpConnections = newACPConnectionPool()
	}
	return r.acpConnections, nil
}

// acquire returns a ready connection, starting and initializing one process at
// most once per key even when several child Runs race.
func (p *acpConnectionPool) acquire(ctx context.Context, invocation ACPWorkerInvocation, env []string) (*acpWorkerConnection, error) {
	if p == nil {
		return nil, fmt.Errorf("ACP connection pool is nil")
	}
	key := acpConnectionKey(invocation, env)
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, fmt.Errorf("ACP connection pool is closed")
		}
		existing := p.conns[key]
		if existing != nil {
			p.mu.Unlock()
			select {
			case <-existing.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-p.rootCtx.Done():
				return nil, fmt.Errorf("ACP connection pool is closed")
			}
			if existing.initErr != nil {
				p.forget(key, existing)
				return nil, existing.initErr
			}
			select {
			case <-existing.client.Done():
				p.forget(key, existing)
				_ = existing.client.Close()
				continue
			default:
				return existing, nil
			}
		}

		conn := &acpWorkerConnection{
			key:      key,
			profile:  invocation.Profile,
			ready:    make(chan struct{}),
			sessions: make(map[string]*acpWorkerSession),
		}
		p.conns[key] = conn
		p.mu.Unlock()
		conn.start(p.rootCtx, invocation, env)
		select {
		case <-conn.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.rootCtx.Done():
			return nil, fmt.Errorf("ACP connection pool is closed")
		}
		if conn.initErr != nil {
			p.forget(key, conn)
			return nil, conn.initErr
		}
		return conn, nil
	}
}

func (c *acpWorkerConnection) start(ctx context.Context, invocation ACPWorkerInvocation, env []string) {
	clientInfo := invocation.ClientInfo
	if clientInfo.Name == "" {
		clientInfo = acpclient.Implementation{Name: "mothx", Title: "MothX Router", Version: "dev"}
	}
	client, err := startACPClient(ctx, invocation, env, acpclient.RemoteConfig{
		MaxMessageBytes:     maxACPWorkerMessageBytes,
		HandleRequest:       c.handleRequest,
		NotificationHandler: c.handleNotification,
	})
	if err != nil {
		c.initErr = fmt.Errorf("start ACP worker: %w", err)
		close(c.ready)
		return
	}
	readTextFile, writeTextFile, terminalAccess := acpWorkerPoolCapabilities(invocation)
	initialized, err := client.Initialize(ctx, acpclient.InitializeParams{
		ClientInfo:         clientInfo,
		ClientCapabilities: acpWorkerClientCapabilities(readTextFile, writeTextFile, terminalAccess),
	})
	if err != nil {
		_ = client.Close()
		c.initErr = fmt.Errorf("initialize ACP worker: %w", err)
		close(c.ready)
		return
	}
	c.client = client
	c.init = initialized
	close(c.ready)
}

// acpWorkerPoolCapabilities computes the capability union a reusable
// connection must advertise. ACP negotiates client capabilities once per
// connection, not per session, while write authorization is per task. A
// Registry-backed connection therefore advertises the write bridge regardless
// of which task opens it first; each session's request handler still rejects
// writes when its own role or write scope does not allow them.
func acpWorkerPoolCapabilities(invocation ACPWorkerInvocation) (readTextFile, writeTextFile, terminalAccess bool) {
	readTextFile = invocation.ReadTextFile
	terminalAccess = invocation.TerminalAccess
	writeTextFile = invocation.WriteTextFile || readTextFile
	return readTextFile, writeTextFile, terminalAccess
}

// startACPClient selects the transport declared by the validated profile.
// stdio still owns a contained child process; remote transports never call the
// local launcher.
func startACPClient(ctx context.Context, invocation ACPWorkerInvocation, env []string, remote acpclient.RemoteConfig) (*acpclient.Client, error) {
	switch invocation.Profile.EffectiveTransport() {
	case config.ACPAgentTransportWebSocket:
		remote.URL = invocation.Profile.URL
		return acpclient.StartWebSocket(ctx, remote)
	case config.ACPAgentTransportHTTP:
		remote.URL = invocation.Profile.URL
		return acpclient.StartHTTP(ctx, remote)
	default:
		return acpclient.Start(ctx, acpclient.Config{
			Command:             invocation.Profile.Command,
			Dir:                 invocation.WorkDir,
			Env:                 env,
			MaxMessageBytes:     remote.MaxMessageBytes,
			Launcher:            invocation.Launcher,
			Stderr:              &boundedACPStderr{limit: 2048},
			HandleRequest:       remote.HandleRequest,
			NotificationHandler: remote.NotificationHandler,
		})
	}
}

func acpWorkerClientCapabilities(readTextFile, writeTextFile, terminalAccess bool) map[string]any {
	return map[string]any{
		"fs":       map[string]bool{"readTextFile": readTextFile, "writeTextFile": writeTextFile},
		"terminal": terminalAccess,
		"auth":     map[string]bool{"terminal": false},
	}
}

func (c *acpWorkerConnection) handleNotification(message acpclient.Message) {
	notification, ok := acpclient.DecodeSessionNotification(message)
	if !ok {
		return
	}
	c.mu.Lock()
	session := c.sessions[notification.SessionID]
	if session == nil {
		c.retainEarlyNotificationLocked(notification.SessionID, message)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	session.enqueue(message)
}

const (
	maxEarlyACPSessions      = 32
	maxEarlyACPNotifications = 128
)

// maxACPWorkerPendingNotifications bounds the per-session notification
// backlog. The ACP client delivers notifications on the pooled connection's
// read loop, so this buffer must stay bounded: an untrusted Agent that emits
// updates faster than they can be projected must not be able to stall the
// other sessions hosted by the same process.
const maxACPWorkerPendingNotifications = 512

func (c *acpWorkerConnection) retainEarlyNotificationLocked(sessionID string, message acpclient.Message) {
	if sessionID == "" {
		return
	}
	if c.earlyNotifications == nil {
		c.earlyNotifications = make(map[string][]acpclient.Message)
	}
	queue := c.earlyNotifications[sessionID]
	if queue == nil {
		if c.earlyNotificationIDs >= maxEarlyACPSessions {
			return
		}
		c.earlyNotificationIDs++
	}
	if len(queue) >= maxEarlyACPNotifications {
		return
	}
	c.earlyNotifications[sessionID] = append(queue, message)
}

func (c *acpWorkerConnection) handleRequest(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
	sessionID := acpclient.DecodeSessionID(message.Params)
	session := c.session(sessionID)
	if session == nil || session.handleRequest == nil {
		return nil, &acpclient.RPCError{Code: -32601, Message: "session method not found"}
	}
	return session.handleRequest(ctx, message)
}

func (c *acpWorkerConnection) session(id string) *acpWorkerSession {
	if c == nil || id == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[id]
}

func (c *acpWorkerConnection) newSession(ctx context.Context, params acpclient.NewSessionParams, handler acpclient.RequestHandler, onNotification func(acpclient.Message)) (*acpWorkerSession, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("ACP connection is not initialized")
	}
	result, err := c.client.NewSession(ctx, params)
	if err != nil {
		return nil, err
	}
	return c.registerSession(result.SessionID, result.ConfigOptions, result.Models, handler, onNotification)
}

// loadSession restores one existing remote session. Capability and resume
// policy are decided by the Runtime before this transport helper is called.
func (c *acpWorkerConnection) loadSession(ctx context.Context, params acpclient.LoadSessionParams, handler acpclient.RequestHandler, onNotification func(acpclient.Message)) (*acpWorkerSession, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("ACP connection is not initialized")
	}
	result, err := c.client.LoadSession(ctx, params)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(result.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(params.SessionID)
	}
	return c.registerSession(sessionID, result.ConfigOptions, result.Models, handler, onNotification)
}

// resumeSession reconnects without replay. The caller has already gated the
// request on sessionCapabilities.resume.
func (c *acpWorkerConnection) resumeSession(ctx context.Context, params acpclient.ResumeSessionParams, handler acpclient.RequestHandler, onNotification func(acpclient.Message)) (*acpWorkerSession, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("ACP connection is not initialized")
	}
	result, err := c.client.ResumeSession(ctx, params)
	if err != nil {
		return nil, err
	}
	return c.registerSession(strings.TrimSpace(params.SessionID), result.ConfigOptions, result.Models, handler, onNotification)
}

func (c *acpWorkerConnection) registerSession(sessionID string, configOptions []acpclient.SessionConfigOption, models *acpclient.SessionModelState, handler acpclient.RequestHandler, onNotification func(acpclient.Message)) (*acpWorkerSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("ACP session is missing an id")
	}
	c.mu.Lock()
	_, duplicate := c.sessions[sessionID]
	c.mu.Unlock()
	if duplicate {
		return nil, fmt.Errorf("ACP returned duplicate session id %q", sessionID)
	}
	if err := c.client.SessionOpened(context.Background(), sessionID); err != nil {
		return nil, fmt.Errorf("open ACP session transport: %w", err)
	}
	session := &acpWorkerSession{
		connection:     c,
		id:             sessionID,
		handleRequest:  handler,
		onNotification: onNotification,
		configOptions:  append([]acpclient.SessionConfigOption(nil), configOptions...),
		models:         models,
		done:           make(chan struct{}),
	}
	session.cond = sync.NewCond(&session.mu)
	c.mu.Lock()
	if _, exists := c.sessions[session.id]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("ACP returned duplicate session id %q", session.id)
	}
	early := c.earlyNotifications[session.id]
	if early != nil {
		for _, message := range early {
			session.enqueue(message)
		}
		delete(c.earlyNotifications, session.id)
		c.earlyNotificationIDs--
	}
	c.sessions[session.id] = session
	c.mu.Unlock()
	return session, nil
}

func (c *acpWorkerConnection) closeSession(ctx context.Context, session *acpWorkerSession, closeRemote bool) error {
	if session == nil {
		return nil
	}
	var closeErr error
	if closeRemote {
		closeErr = c.client.CloseSession(ctx, session.id)
	}
	c.mu.Lock()
	if c.sessions[session.id] == session {
		delete(c.sessions, session.id)
	}
	c.mu.Unlock()
	session.stop()
	c.client.SessionClosed(session.id)
	return closeErr
}

func (s *acpWorkerSession) run() {
	defer close(s.done)
	for {
		message, ok := s.next()
		if !ok {
			return
		}
		if s.onNotification != nil {
			s.onNotification(message)
		}
		// The handled counter advances only after the projection completed.
		// waitForPending reads it to prove that every accepted notification
		// reached the turn, so counting earlier would let a caller observe a
		// result before its text was applied.
		s.mu.Lock()
		s.handled++
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// next waits for the next pending notification and returns it. The second
// return value is false once the session is closed and no pending work
// remains.
func (s *acpWorkerSession) next() (acpclient.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		// A closed session stops projecting immediately: the remaining
		// backlog belongs to a cancelled or already-terminal turn, and
		// draining it here would delay stop() behind the projection.
		if s.closed {
			s.pending = nil
			return acpclient.Message{}, false
		}
		if len(s.pending) > 0 {
			message := s.pending[0]
			s.pending[0] = acpclient.Message{}
			s.pending = s.pending[1:]
			return message, true
		}
		s.cond.Wait()
	}
}

// start launches the per-session notification projector. Open defers this for
// a new session until the first prompt has installed its turn, so an Agent
// that emits session/update immediately after session/new cannot race a nil
// worker session or a nil turn. A loaded session starts during Open so its
// replayed history is drained before the first new prompt.
func (s *acpWorkerSession) start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go s.run()
}

// enqueue takes ownership of one notification for this session. It never
// blocks: the caller runs on the pooled connection's read loop, where a stall
// would delay every other session sharing that process. When the bound is
// reached the oldest pending notification is dropped so the live stream stays
// the freshest available view, and the drop is counted as handled so
// waitForPending cannot deadlock on a notification that no longer exists.
func (s *acpWorkerSession) enqueue(message acpclient.Message) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.accepted++
	if len(s.pending) >= maxACPWorkerPendingNotifications {
		s.pending = s.pending[1:]
		s.dropped++
	}
	s.pending = append(s.pending, message)
	s.cond.Broadcast()
	s.mu.Unlock()
}

// waitForPending blocks until every notification accepted before this call has
// been projected or dropped. A snapshot count avoids the sync.WaitGroup
// Add/Wait race when an Agent emits updates after a prompt response. The
// returned count is the number of notifications dropped because the buffer
// was full: the oldest ones are discarded, so the projected transcript is
// missing its beginning and the caller must report the turn as incomplete.
func (s *acpWorkerSession) waitForPending() uint64 {
	s.mu.Lock()
	target := s.accepted
	for s.handled+s.dropped < target && !s.closed {
		s.cond.Wait()
	}
	dropped := s.dropped
	s.mu.Unlock()
	return dropped
}

func (s *acpWorkerSession) stop() {
	s.mu.Lock()
	if s.closed {
		started := s.started
		s.mu.Unlock()
		if started {
			<-s.done
		}
		return
	}
	s.closed = true
	started := s.started
	s.cond.Broadcast()
	s.mu.Unlock()
	if started {
		<-s.done
		return
	}
	close(s.done)
}

func (p *acpConnectionPool) forget(key string, conn *acpWorkerConnection) {
	if p == nil || conn == nil {
		return
	}
	p.mu.Lock()
	if p.conns[key] == conn {
		delete(p.conns, key)
	}
	p.mu.Unlock()
}

// Close terminates every pooled process. Active prompt calls observe transport
// termination and remain responsible for their single canonical child terminal.
func (p *acpConnectionPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	conns := make([]*acpWorkerConnection, 0, len(p.conns))
	for _, conn := range p.conns {
		conns = append(conns, conn)
	}
	p.conns = nil
	p.mu.Unlock()
	p.cancel()
	for _, conn := range conns {
		if conn.client != nil {
			_ = conn.client.Close()
		}
	}
}

func acpConnectionKey(invocation ACPWorkerInvocation, env []string) string {
	// Capability flags are deliberately excluded except for the ones that
	// cannot be restored after initialize (Registry availability and terminal
	// opt-in). A Registry-backed connection advertises the write bridge as part
	// of its union, so read-only and write tasks for the same profile/workdir
	// reuse one initialized process/connection; the per-session handler
	// enforces the actual task role and write scope.
	return strings.Join([]string{
		strings.TrimSpace(invocation.Profile.ID),
		strings.TrimSpace(invocation.WorkDir),
		strings.TrimSpace(string(invocation.Profile.EffectiveTransport())),
		strings.TrimSpace(invocation.Profile.URL),
		strings.Join(invocation.Profile.Command, "\x00"),
		strings.Join(env, "\x00"),
		fmt.Sprintf("%t", invocation.ReadTextFile),
		fmt.Sprintf("%t", invocation.TerminalAccess),
	}, "\x01")
}
