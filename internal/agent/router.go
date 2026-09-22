package agent

import (
	"sync"

	agentpkg "github.com/oschina/mothx/agent"
)

// RouterEventHandler receives agent events for routing purposes.
type RouterEventHandler interface {
	HandleRouterEvent(event agentpkg.Event) error
}

// RouterEventHandlerFunc adapts a function to RouterEventHandler.
type RouterEventHandlerFunc func(event agentpkg.Event) error

// HandleRouterEvent implements RouterEventHandler.
func (f RouterEventHandlerFunc) HandleRouterEvent(event agentpkg.Event) error {
	return f(event)
}

// EventRouter routes events from agents to consumers (UI, parent agents).
type EventRouter struct {
	mu       sync.RWMutex
	handlers map[agentpkg.AgentID][]RouterEventHandler
	global   []RouterEventHandler
}

// NewEventRouter creates a new event router.
func NewEventRouter() *EventRouter {
	return &EventRouter{
		handlers: make(map[agentpkg.AgentID][]RouterEventHandler),
	}
}

// RegisterAgent registers an event handler for a specific agent.
func (r *EventRouter) RegisterAgent(id agentpkg.AgentID, handler RouterEventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[id] = append(r.handlers[id], handler)
}

// UnregisterAgent removes all handlers for a specific agent.
func (r *EventRouter) UnregisterAgent(id agentpkg.AgentID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handlers, id)
}

// RegisterGlobal registers a handler that receives events from all agents.
func (r *EventRouter) RegisterGlobal(handler RouterEventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.global = append(r.global, handler)
}

// Dispatch sends an event to the appropriate handlers.
// Returns the first error from any handler, or nil.
func (r *EventRouter) Dispatch(event agentpkg.Event) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Route to agent-specific handlers
	for _, h := range r.handlers[event.AgentID] {
		if err := h.HandleRouterEvent(event); err != nil {
			return err
		}
	}

	// Route to global handlers
	for _, h := range r.global {
		if err := h.HandleRouterEvent(event); err != nil {
			return err
		}
	}

	return nil
}

// HandlerCount returns the number of handlers for a given agent (for testing).
func (r *EventRouter) HandlerCount(id agentpkg.AgentID) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers[id])
}

// GlobalHandlerCount returns the number of global handlers (for testing).
func (r *EventRouter) GlobalHandlerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.global)
}
