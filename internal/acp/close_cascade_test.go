package acp

import (
	"sync"
	"testing"
)

// TestRunSessionLaneJobSerializesOnTargetLane pins the primitive that lets a
// cascade close reach a descendant: the descendant's shutdown is enqueued on the
// descendant's own lane, so it cannot interleave with work already queued or
// running there (for example a descendant prompt).
func TestRunSessionLaneJobSerializesOnTargetLane(t *testing.T) {
	srv := &server{ops: newSessionOpLanes()}

	release := make(chan struct{})
	started := make(chan struct{})
	if !srv.ops.dispatch("child", func() {
		close(started)
		<-release
	}) {
		t.Fatal("dispatch returned false")
	}
	<-started

	var mu sync.Mutex
	ran := false
	done := make(chan struct{})
	go func() {
		srv.runSessionLaneJob("child", func() {
			mu.Lock()
			ran = true
			mu.Unlock()
		})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("runSessionLaneJob returned before the target lane drained")
	default:
	}

	close(release)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if !ran {
		t.Fatal("runSessionLaneJob never ran the job")
	}
}

// TestRunSessionLaneJobRunsInlineWithoutLanes pins the nil-lane fallback: direct
// handler fixtures (no lane scheduler) must keep running synchronously.
func TestRunSessionLaneJobRunsInlineWithoutLanes(t *testing.T) {
	var srv server
	ran := false
	srv.runSessionLaneJob("child", func() { ran = true })
	if !ran {
		t.Fatal("runSessionLaneJob did not run inline without lanes")
	}
}
