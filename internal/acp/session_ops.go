package acp

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/mcp"
)

// sessionOpLanes serializes session-scoped request handling FIFO per session
// while letting different sessions proceed concurrently. The ACP read loop
// dispatches session-scoped work here so a slow lifecycle operation — session
// load/fork/new assemble Runtime resources and connect MCP servers — can never
// head-of-line block the read loop. $/cancel_request and the client's responses
// to reverse approval/question requests are read on that loop and must stay
// reachable while a load is in flight.
//
// A nil *sessionOpLanes runs every job inline, which keeps direct handler
// invocations (unit tests, embedded use) synchronous and unchanged.
type sessionOpLanes struct {
	mu       sync.Mutex
	pending  map[string][]func()
	running  map[string]bool
	draining bool
	wg       sync.WaitGroup
}

func newSessionOpLanes() *sessionOpLanes {
	return &sessionOpLanes{pending: make(map[string][]func()), running: make(map[string]bool)}
}

// dispatch enqueues job on the lane for key. It reports false only when the
// lanes have already shut down and the job was dropped.
func (l *sessionOpLanes) dispatch(key string, job func()) bool {
	if l == nil {
		job()
		return true
	}
	if job == nil {
		return true
	}
	l.mu.Lock()
	if l.draining {
		l.mu.Unlock()
		return false
	}
	l.pending[key] = append(l.pending[key], job)
	if l.running[key] {
		l.mu.Unlock()
		return true
	}
	l.running[key] = true
	l.wg.Add(1)
	l.mu.Unlock()
	go l.drain(key)
	return true
}

// drain runs the queued jobs for one key in order, then retires the lane so an
// idle session does not keep a goroutine or map entry alive.
func (l *sessionOpLanes) drain(key string) {
	defer l.wg.Done()
	for {
		l.mu.Lock()
		queue := l.pending[key]
		if len(queue) == 0 {
			delete(l.pending, key)
			delete(l.running, key)
			l.mu.Unlock()
			return
		}
		job := queue[0]
		l.pending[key] = queue[1:]
		l.mu.Unlock()
		job()
	}
}

// busy reports whether a job is queued or running for key. The read loop is the
// only dispatcher, so this is authoritative for deciding whether a
// session-scoped request must be serialized behind pending work.
func (l *sessionOpLanes) busy(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running[key] || len(l.pending[key]) > 0
}

// shutdown stops accepting new jobs and waits for in-flight jobs to finish so
// the process-boundary runtime shutdown never races a lane job that is still
// assembling or installing a session runtime.
func (l *sessionOpLanes) shutdown() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.draining = true
	l.mu.Unlock()
	l.wg.Wait()
}

// sessionOpKey extracts the session identity a request is scoped to. It is used
// only to pick the serialization lane, never as authorization: handlers still
// resolve the Runtime-owned session themselves.
func sessionOpKey(params json.RawMessage) string {
	var probe struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &probe)
	key := strings.TrimSpace(probe.SessionID)
	if key == "" {
		// A session-scoped request without a session id cannot be serialized
		// against other work; a dedicated lane still keeps it off the read loop.
		return "\x00unkeyed"
	}
	return key
}

// dispatchSessionOp always runs job off the read loop on the lane for key. It is
// used for operations that can block for a long time while they assemble state
// (session load/fork/new) or whose ordering against such an operation must be
// preserved (close/delete).
func (s *server) dispatchSessionOp(req rpcRequest, key string, job func()) {
	if s.ops.dispatch(key, job) {
		return
	}
	if len(req.ID) > 0 {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "server is shutting down"})
	}
}

// runSessionOp keeps a session-scoped method synchronous unless a lifecycle
// operation is already queued or running for the same session. In that case the
// work is serialized behind it on the lane so it observes the installed runtime
// instead of racing a half-built one, and the read loop stays reachable. The
// uncontended path is unchanged from today.
func (s *server) runSessionOp(req rpcRequest, key string, job func()) {
	if s.ops.busy(key) {
		s.dispatchSessionOp(req, key, job)
		return
	}
	job()
}
