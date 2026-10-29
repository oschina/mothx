package terminal

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/platform"
)

var (
	ErrUnsupported = errors.New("terminal is not supported on this platform")
	ErrNotFound    = errors.New("terminal not found")
	ErrLimit       = errors.New("terminal limit reached")
	ErrExited      = errors.New("terminal has exited")
	ErrClosed      = errors.New("terminal manager is closed")
	ErrInvalidSize = errors.New("invalid terminal size")
)

// State is the lifecycle state of one PTY.
type State string

const (
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateDetached State = "detached"
	StateExited   State = "exited"
	StateKilled   State = "killed"
)

// Config controls the manager and newly created terminals.
type Config struct {
	Shell                string
	MaxSessions          int
	MaxPerSession        int
	ScrollbackBytes      int
	MaxOutputBytesPerSec int
}

func (c Config) withDefaults() Config {
	if c.MaxSessions <= 0 {
		c.MaxSessions = 8
	}
	if c.MaxPerSession <= 0 {
		c.MaxPerSession = 2
	}
	if c.ScrollbackBytes <= 0 {
		c.ScrollbackBytes = 1 << 20
	}
	if c.MaxOutputBytesPerSec <= 0 {
		c.MaxOutputBytesPerSec = 1 << 20
	}
	if c.Shell == "" {
		c.Shell = platform.DefaultShell()
	}
	return c
}

// Manager owns all live PTYs for one serve process.
type Manager struct {
	mu        sync.Mutex
	cfg       Config
	closed    bool
	byID      map[string]*Terminal
	bySession map[string]map[string]*Terminal
	logf      func(format string, args ...any)
}

func New(cfg Config) *Manager {
	return &Manager{
		cfg:       cfg.withDefaults(),
		byID:      make(map[string]*Terminal),
		bySession: make(map[string]map[string]*Terminal),
	}
}

// SetLogger installs the lifecycle audit sink. Only terminal metadata is
// logged; user input and PTY output must never be logged. The logger is copied
// into each terminal at creation so a later configuration change cannot make
// the audit sink disappear for a live shell.
func (m *Manager) SetLogger(logf func(format string, args ...any)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.logf = logf
	m.mu.Unlock()
}

func (m *Manager) audit(format string, args ...any) {
	if m == nil {
		return
	}
	m.mu.Lock()
	logf := m.logf
	m.mu.Unlock()
	if logf != nil {
		logf(format, args...)
	}
}

func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// UpdateConfig applies limits and shell to future terminals. Existing
// terminals keep their process and shell; the new output/backpressure limit is
// applied to their read loops as well.
func (m *Manager) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	m.mu.Lock()
	m.cfg = cfg
	terminals := make([]*Terminal, 0, len(m.byID))
	for _, t := range m.byID {
		terminals = append(terminals, t)
	}
	m.mu.Unlock()
	for _, t := range terminals {
		t.updateConfig(cfg)
	}
}

func (m *Manager) Create(sessionID, cwd string, cols, rows uint16) (*Terminal, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if sessionID == "" || cwd == "" {
		return nil, fmt.Errorf("session and cwd are required")
	}
	if cols < 1 || rows < 1 {
		return nil, ErrInvalidSize
	}
	cols, rows = clampSize(cols, rows)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if len(m.byID) >= m.cfg.MaxSessions {
		m.mu.Unlock()
		return nil, ErrLimit
	}
	if len(m.bySession[sessionID]) >= m.cfg.MaxPerSession {
		m.mu.Unlock()
		return nil, ErrLimit
	}
	cfg := m.cfg
	logf := m.logf
	// Keep the slot reserved while starting the process so concurrent creates
	// cannot race past MaxSessions.
	id, err := randomID("t")
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	token, err := randomID("tt")
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	t := &Terminal{
		ID:        id,
		SessionID: sessionID,
		Cwd:       cwd,
		Shell:     cfg.Shell,
		Cols:      cols,
		Rows:      rows,
		Token:     token,
		cfg:       cfg,
		logf:      logf,
		ring:      newRing(cfg.ScrollbackBytes),
		done:      make(chan struct{}),
		exitDone:  make(chan struct{}),
		limiter:   newOutputLimiter(cfg.MaxOutputBytesPerSec),
		cmd:       exec.Command(cfg.Shell),
	}
	t.cmd.Dir = cwd
	t.cmd.Env = shellEnv()
	t.state = StateStarting
	if err := t.start(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	t.state = StateRunning
	m.byID[id] = t
	if m.bySession[sessionID] == nil {
		m.bySession[sessionID] = make(map[string]*Terminal)
	}
	m.bySession[sessionID][id] = t
	m.mu.Unlock()

	pty := t.pty
	go t.readLoop(pty)
	go t.waitLoop(pty)
	m.audit("terminal_created id=%s session=%s cwd=%s shell=%s cols=%d rows=%d", t.ID, t.SessionID, t.Cwd, t.Shell, cols, rows)
	return t, nil
}

func (m *Manager) Get(sessionID, id string) (*Terminal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.byID[id]
	if t == nil || t.SessionID != sessionID {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *Manager) GetByID(id string) (*Terminal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.byID[id]
	if t == nil {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *Manager) Authenticate(id, token string) (*Terminal, error) {
	t, err := m.GetByID(id)
	if err != nil {
		return nil, err
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(t.Token)) != 1 {
		return nil, ErrNotFound
	}
	return t, nil
}

func (m *Manager) List(sessionID string) []*Terminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Terminal
	if sessionID != "" {
		for _, t := range m.bySession[sessionID] {
			out = append(out, t)
		}
		return out
	}
	for _, t := range m.byID {
		out = append(out, t)
	}
	return out
}

func (m *Manager) Close(sessionID, id string) error {
	m.mu.Lock()
	t := m.byID[id]
	if t == nil || t.SessionID != sessionID {
		m.mu.Unlock()
		return ErrNotFound
	}
	m.removeLocked(t)
	m.mu.Unlock()
	return t.Close()
}

func (m *Manager) CloseByID(id string) error {
	m.mu.Lock()
	t := m.byID[id]
	if t == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	m.removeLocked(t)
	m.mu.Unlock()
	return t.Close()
}

func (m *Manager) CloseSession(sessionID string) {
	m.mu.Lock()
	var terminals []*Terminal
	for _, t := range m.bySession[sessionID] {
		terminals = append(terminals, t)
	}
	for _, t := range terminals {
		m.removeLocked(t)
	}
	m.mu.Unlock()
	for _, t := range terminals {
		_ = t.Close()
	}
}

// CloseAll terminates every live terminal while keeping the manager usable.
// Config updates use it when the terminal feature becomes unavailable, so a
// previously open shell cannot outlive an explicit disable or an auth removal.
func (m *Manager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	terminals := make([]*Terminal, 0, len(m.byID))
	for _, t := range m.byID {
		terminals = append(terminals, t)
	}
	m.byID = make(map[string]*Terminal)
	m.bySession = make(map[string]map[string]*Terminal)
	m.mu.Unlock()
	for _, t := range terminals {
		_ = t.Close()
	}
}

func (m *Manager) Shutdown() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	terminals := make([]*Terminal, 0, len(m.byID))
	for _, t := range m.byID {
		terminals = append(terminals, t)
	}
	m.byID = make(map[string]*Terminal)
	m.bySession = make(map[string]map[string]*Terminal)
	m.mu.Unlock()
	for _, t := range terminals {
		_ = t.Close()
	}
}

func (m *Manager) removeLocked(t *Terminal) {
	delete(m.byID, t.ID)
	if group := m.bySession[t.SessionID]; group != nil {
		delete(group, t.ID)
		if len(group) == 0 {
			delete(m.bySession, t.SessionID)
		}
	}
}

func randomID(prefix string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Terminal is one interactive shell process attached to a PTY.
type Terminal struct {
	ID        string
	SessionID string
	Cwd       string
	Shell     string
	Token     string
	Cols      uint16
	Rows      uint16

	mu         sync.Mutex
	state      State
	exitCode   int
	throttled  bool
	cmd        *exec.Cmd
	pty        PTY
	ring       *ringBuffer
	sub        *subscription
	cfg        Config
	limiter    *outputLimiter
	writeMu    sync.Mutex
	done       chan struct{}
	exitDone   chan struct{}
	finishOnce sync.Once
	logf       func(format string, args ...any)
}

func (t *Terminal) start() error {
	configureProcAttr(t.cmd)
	pty, err := startPTY(t.cmd, t.Cols, t.Rows)
	if err != nil {
		return err
	}
	t.pty = pty
	return nil
}

func (t *Terminal) updateConfig(cfg Config) {
	t.mu.Lock()
	t.cfg = cfg
	t.ring.resize(cfg.ScrollbackBytes)
	t.mu.Unlock()
	if t.limiter != nil {
		t.limiter.setRate(cfg.MaxOutputBytesPerSec)
	}
}

func (t *Terminal) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Size reports the last applied geometry. Resize and Attach run concurrently
// with HTTP listings, so callers must not read the Cols/Rows fields directly.
func (t *Terminal) Size() (uint16, uint16) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Cols, t.Rows
}

func (t *Terminal) ExitCode() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exitCode
}

func (t *Terminal) Done() <-chan struct{} { return t.done }

func (t *Terminal) Write(p []byte) error {
	t.mu.Lock()
	pty, state := t.pty, t.state
	t.mu.Unlock()
	if pty == nil || state == StateExited || state == StateKilled {
		return ErrExited
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := pty.Write(p)
	return err
}

// MaxCols and MaxRows bound the PTY geometry accepted from any caller. The
// HTTP API validates the same range, but WebSocket resize frames are binary and
// must not be able to request an absurd terminal buffer.
const (
	MaxCols = 500
	MaxRows = 300
)

func clampSize(cols, rows uint16) (uint16, uint16) {
	if cols > MaxCols {
		cols = MaxCols
	}
	if rows > MaxRows {
		rows = MaxRows
	}
	return cols, rows
}

func (t *Terminal) Resize(cols, rows uint16) error {
	if cols < 1 || rows < 1 {
		return ErrInvalidSize
	}
	cols, rows = clampSize(cols, rows)
	t.mu.Lock()
	pty, state := t.pty, t.state
	if pty != nil && state != StateExited && state != StateKilled {
		t.Cols, t.Rows = cols, rows
	}
	t.mu.Unlock()
	if pty == nil || state == StateExited || state == StateKilled {
		return ErrExited
	}
	return pty.Resize(cols, rows)
}

func (t *Terminal) Close() error { return t.CloseWithReason("closed") }

// closeGracePeriod bounds how long a shell may ignore the polite termination
// request before the process group is killed. CloseWithReason waits on the
// terminal's completion channel, so a normal shell (the common case) closes as
// soon as the process exits instead of always sleeping this long.
const closeGracePeriod = 2 * time.Second

func (t *Terminal) CloseWithReason(reason string) error {
	t.mu.Lock()
	if t.state == StateExited || t.state == StateKilled {
		t.mu.Unlock()
		return nil
	}
	t.state = StateKilled
	pty := t.pty
	t.mu.Unlock()
	if pty != nil {
		requestStop(pty)
		select {
		case <-t.done:
		case <-time.After(closeGracePeriod):
			forceStop(pty)
		}
		_ = pty.Close()
	}
	t.finish(StateKilled, reason)
	if t.logf != nil {
		t.logf("terminal_killed id=%s session=%s cwd=%s exit=%d reason=%s", t.ID, t.SessionID, t.Cwd, t.ExitCode(), reason)
	}
	return nil
}

// ControlMessage reports a non-output condition to the attached client. It is
// intentionally transport-neutral; the serve WebSocket handler maps it to a
// canonical control frame.
type ControlMessage struct {
	Type   string
	Active bool
}

// subscription is a single attached WebSocket/consumer. A new Attach takes
// over the previous subscription.
type subscription struct {
	out      chan []byte
	control  chan ControlMessage
	stopCh   chan struct{}
	stopOnce sync.Once
	reasonMu sync.Mutex
	reason   string
}

func newSubscription() *subscription {
	return &subscription{
		out:     make(chan []byte, 64),
		control: make(chan ControlMessage, 4),
		stopCh:  make(chan struct{}),
	}
}

func (s *subscription) stop(reason string) {
	s.stopOnce.Do(func() {
		s.reasonMu.Lock()
		s.reason = reason
		s.reasonMu.Unlock()
		close(s.stopCh)
	})
}

func (s *subscription) Reason() string {
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	return s.reason
}

// Subscription is the consumer-facing view of one attachment.
type Subscription struct {
	out       <-chan []byte
	control   <-chan ControlMessage
	stop      <-chan struct{}
	replay    []byte
	throttled bool
	source    *subscription
}

func (s *Subscription) Output() <-chan []byte { return s.out }

func (s *Subscription) Control() <-chan ControlMessage {
	if s == nil {
		return nil
	}
	return s.control
}

func (s *Subscription) Stop() <-chan struct{} { return s.stop }
func (s *Subscription) Throttled() bool {
	return s != nil && s.throttled
}
func (s *Subscription) Replay() []byte { return s.replay }
func (s *Subscription) Reason() string {
	if s == nil || s.source == nil {
		return ""
	}
	return s.source.Reason()
}

func (t *Terminal) Attach() (*Subscription, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state == StateExited || t.state == StateKilled {
		return nil, ErrExited
	}
	if t.sub != nil {
		t.sub.stop("takeover")
	}
	sub := newSubscription()
	t.sub = sub
	t.state = StateRunning
	return &Subscription{
		out:       sub.out,
		control:   sub.control,
		stop:      sub.stopCh,
		replay:    t.ring.snapshot(),
		throttled: t.throttled,
		source:    sub,
	}, nil
}

func (t *Terminal) Detach(s *Subscription) {
	if s == nil {
		return
	}
	// Stop before taking the terminal lock so a blocked reader can release it.
	s.source.stop("detached")
	t.mu.Lock()
	if t.sub == s.source {
		t.sub = nil
		if t.state == StateRunning {
			t.state = StateDetached
		}
	}
	t.mu.Unlock()
}

func (t *Terminal) readLoop(pty PTY) {
	if pty == nil {
		t.finish(StateExited, "exited")
		return
	}
	buf := make([]byte, 32*1024)
	wasThrottled := false
	for {
		n, err := pty.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			sub := t.sub
			t.mu.Unlock()
			waited := t.limiter.wait(len(data))
			previousThrottled := wasThrottled
			if waited != wasThrottled {
				t.mu.Lock()
				t.throttled = waited
				t.mu.Unlock()
				wasThrottled = waited
			}
			if sub != nil {
				// Refresh "active" on every throttled chunk so the client can
				// clear the status after a quiet period without missing a
				// sustained throttle.
				if waited {
					notifyControl(sub, ControlMessage{Type: "backpressure", Active: true})
				} else if previousThrottled {
					notifyControl(sub, ControlMessage{Type: "backpressure", Active: false})
				}
			}
			t.mu.Lock()
			t.ring.write(data)
			current := t.sub
			t.mu.Unlock()
			if current != nil {
				select {
				case current.out <- data:
				case <-current.stopCh:
				}
			}
		}
		if err != nil {
			break
		}
	}
	// A PTY read commonly fails with EIO when the child exits, slightly before
	// cmd.Wait records the status. Wait for the exit code briefly so the
	// terminal/detached SSE consumer does not observe a false exit code 0.
	select {
	case <-t.exitDone:
	case <-time.After(500 * time.Millisecond):
	}
	t.finish(StateExited, "exited")
}

func (t *Terminal) waitLoop(pty PTY) {
	if pty == nil {
		t.finish(StateExited, "exited")
		return
	}
	code, _ := pty.Wait()
	t.mu.Lock()
	t.exitCode = code
	t.mu.Unlock()
	close(t.exitDone)
	t.finish(StateExited, "exited")
	if t.State() == StateExited && t.logf != nil {
		t.logf("terminal_exited id=%s session=%s cwd=%s exit=%d", t.ID, t.SessionID, t.Cwd, code)
	}
}

func notifyControl(sub *subscription, message ControlMessage) {
	if sub == nil {
		return
	}
	select {
	case sub.control <- message:
	default:
	}
}

func (t *Terminal) finish(state State, reason string) {
	t.finishOnce.Do(func() {
		t.mu.Lock()
		if t.state != StateKilled {
			t.state = state
		}
		sub := t.sub
		t.sub = nil
		pty := t.pty
		t.pty = nil
		t.mu.Unlock()
		if pty != nil {
			_ = pty.Close()
		}
		if sub != nil {
			sub.stop(reason)
		}
		close(t.done)
	})
}
