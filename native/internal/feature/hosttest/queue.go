package hosttest

import (
	"sync"
	"testing"
	"time"
)

// Queue is the UI-thread stand-in for lifecycle tests. Worker goroutines post
// their closures here and the test runs them on its own goroutine, so state
// is only ever touched from one thread, exactly as in the running app.
type Queue struct {
	mu   sync.Mutex
	fns  []func()
	wake chan struct{}
}

func NewQueue() *Queue {
	return &Queue{wake: make(chan struct{}, 1)}
}

func (q *Queue) Post(fn func()) {
	q.mu.Lock()
	q.fns = append(q.fns, fn)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Drain runs everything posted so far.
func (q *Queue) Drain() {
	for {
		q.mu.Lock()
		if len(q.fns) == 0 {
			q.mu.Unlock()
			return
		}
		fn := q.fns[0]
		q.fns = q.fns[1:]
		q.mu.Unlock()
		fn()
	}
}

// RunUntil drains posted work until done reports true or the deadline passes.
// It reports whether done became true.
func (q *Queue) RunUntil(t *testing.T, timeout time.Duration, done func() bool) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		q.Drain()
		if done() {
			return true
		}
		select {
		case <-q.wake:
		case <-deadline:
			q.Drain()
			return done()
		}
	}
}
