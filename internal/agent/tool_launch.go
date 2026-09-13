package agent

import "sync"

// ToolLaunchOrder keeps the *start* of one parallel tool-call batch in the
// declared provider order without serializing or blocking the batch: calls still
// run concurrently, still wait for approvals and durable claims independently,
// and may finish in any order, but call i may not report its start before call
// i-1 has reported its own. Argument sizes, worker scheduling, and per-call
// waits in front of the tool can therefore no longer reorder the observable
// start of a batch.
//
// The order is a chain of one-shot handoffs. Call i closes the signal that
// releases call i+1 only after its own start has been reported, so the
// predecessor's start happens before the successor's. A call that leaves before
// reporting its start (invalid arguments, unknown tool, replayed result)
// releases its successor through Release, so a failed call never parks the rest
// of the batch. The only wait is that handoff: it is never held across a tool
// body, an approval prompt, or a durable claim.
type ToolLaunchOrder struct {
	started []chan struct{}
}

// NewToolLaunchOrder prepares the start-order chain for one batch of calls. The
// first call is always free to start. A non-positive call count returns nil,
// which yields nil-safe handles.
func NewToolLaunchOrder(calls int) *ToolLaunchOrder {
	if calls <= 0 {
		return nil
	}
	order := &ToolLaunchOrder{started: make([]chan struct{}, calls)}
	for i := range order.started {
		order.started[i] = make(chan struct{})
	}
	close(order.started[0])
	return order
}

// Handle returns the ordering handle of one call in the batch. Handles are
// single-use, single-goroutine, and nil-safe, so sequential and single-call
// paths may pass nil.
func (o *ToolLaunchOrder) Handle(index int) *ToolLaunchHandle {
	if o == nil {
		return nil
	}
	return &ToolLaunchHandle{order: o, index: index}
}

// ToolLaunchHandle owns one call's start checkpoint in a ToolLaunchOrder batch.
// Every method is safe on a nil handle.
type ToolLaunchHandle struct {
	order *ToolLaunchOrder
	index int

	once sync.Once
}

// waitStart blocks until every earlier call of the batch reported its start.
func (h *ToolLaunchHandle) waitStart() {
	if h == nil || h.order == nil {
		return
	}
	<-h.order.started[h.index]
}

// markStarted releases the next call's start. Callers invoke it after the start
// event has been sent so the event stream keeps the declared order.
func (h *ToolLaunchHandle) markStarted() {
	if h == nil || h.order == nil {
		return
	}
	h.once.Do(func() {
		if h.index+1 < len(h.order.started) {
			close(h.order.started[h.index+1])
		}
	})
}

// Release reports the start checkpoint for a call that never reached it, so the
// calls queued behind it can begin. It is idempotent and is normally deferred by
// the code path that executes the call.
func (h *ToolLaunchHandle) Release() {
	h.markStarted()
}
