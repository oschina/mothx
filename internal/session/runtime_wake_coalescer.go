package session

import (
	"strings"
	"sync"
)

// SessionWakeCoalescer collapses bursts of advisory wake-ups for one session
// into a single in-flight handler invocation plus at most one trailing re-run.
// It bounds the receive side of the unauthenticated UDP lease bus: a spoofed or
// chatty flood for one session can never spawn more than one handler goroutine
// at a time, and the trailing re-run guarantees the handler re-reads the
// durable state after the last wake in a burst, so projections still converge.
//
// The handler must be idempotent and re-read authoritative state itself (the
// bus never carries state). Wakes for distinct session IDs remain independent;
// the residual amplification for a flood of unique random IDs is one cheap
// durable read per ID, which the loopback-only trust boundary accepts.
type SessionWakeCoalescer struct {
	mu      sync.Mutex
	handler func(sessionID string)
	running map[string]bool
	dirty   map[string]bool
}

// NewSessionWakeCoalescer creates a coalescer for one handler. A nil handler
// makes every wake a no-op.
func NewSessionWakeCoalescer(handler func(sessionID string)) *SessionWakeCoalescer {
	return &SessionWakeCoalescer{
		handler: handler,
		running: make(map[string]bool),
		dirty:   make(map[string]bool),
	}
}

// Wake schedules the handler for sessionID unless it is already in flight, in
// which case exactly one trailing re-run is remembered. It never blocks.
func (c *SessionWakeCoalescer) Wake(sessionID string) {
	if c == nil || c.handler == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	c.mu.Lock()
	if c.running[sessionID] {
		c.dirty[sessionID] = true
		c.mu.Unlock()
		return
	}
	c.running[sessionID] = true
	c.mu.Unlock()
	go c.drain(sessionID)
}

func (c *SessionWakeCoalescer) drain(sessionID string) {
	for {
		c.handler(sessionID)
		c.mu.Lock()
		if c.dirty[sessionID] {
			delete(c.dirty, sessionID)
			c.mu.Unlock()
			continue
		}
		delete(c.running, sessionID)
		c.mu.Unlock()
		return
	}
}
