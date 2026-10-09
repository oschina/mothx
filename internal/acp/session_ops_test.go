package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// waitForCondition polls cond until it holds or the deadline elapses. It keeps
// the lane tests deterministic without depending on a fixed sleep.
func waitForCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestSessionOpLanesSerializesPerKeyAndRunsKeysConcurrently pins the core
// guarantee: work for one session is FIFO, work for different sessions is not
// blocked behind it.
func TestSessionOpLanesSerializesPerKeyAndRunsKeysConcurrently(t *testing.T) {
	lanes := newSessionOpLanes()

	release := make(chan struct{})
	firstStarted := make(chan struct{})
	otherRan := make(chan struct{})

	var mu sync.Mutex
	var order []string
	record := func(v string) {
		mu.Lock()
		order = append(order, v)
		mu.Unlock()
	}

	if !lanes.dispatch("session-a", func() {
		record("a1-start")
		close(firstStarted)
		<-release
		record("a1-end")
	}) {
		t.Fatal("dispatch a1 returned false")
	}
	// A second job for the same session must queue behind the first.
	if !lanes.dispatch("session-a", func() { record("a2") }) {
		t.Fatal("dispatch a2 returned false")
	}
	// A different session must run without waiting for a1.
	if !lanes.dispatch("session-b", func() {
		record("b1")
		close(otherRan)
	}) {
		t.Fatal("dispatch b1 returned false")
	}

	<-firstStarted
	<-otherRan
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	// Different keys run concurrently, so a1-start and b1 may appear in either
	// order; the queued same-key job must not have run yet.
	seen := map[string]bool{}
	for _, v := range got {
		seen[v] = true
	}
	if !seen["a1-start"] || !seen["b1"] || seen["a2"] || seen["a1-end"] {
		t.Fatalf("order before release = %v, want a1-start and b1 only", got)
	}

	close(release)
	waitForCondition(t, "queued a2 to run after a1", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 4
	})
	mu.Lock()
	defer mu.Unlock()
	if order[len(order)-1] != "a2" {
		t.Fatalf("queued same-key job must run last: %v", order)
	}
	endIdx := -1
	for i, v := range order {
		if v == "a1-end" {
			endIdx = i
		}
	}
	if endIdx < 0 || endIdx >= len(order)-1 {
		t.Fatalf("a1 must end before a2 runs: %v", order)
	}
}

func TestSessionOpLanesBusyTracksRunningAndQueued(t *testing.T) {
	if (*sessionOpLanes)(nil).busy("k") {
		t.Fatal("nil lanes reported busy")
	}
	lanes := newSessionOpLanes()
	release := make(chan struct{})
	started := make(chan struct{})
	lanes.dispatch("k", func() {
		close(started)
		<-release
	})
	<-started
	if !lanes.busy("k") {
		t.Fatal("running lane not reported busy")
	}
	if lanes.busy("other") {
		t.Fatal("unrelated key reported busy")
	}
	lanes.dispatch("k", func() {})
	if !lanes.busy("k") {
		t.Fatal("queued lane not reported busy")
	}
	close(release)
	waitForCondition(t, "lane to retire", func() bool { return !lanes.busy("k") })
}

func TestSessionOpLanesShutdownDrainsAndRejects(t *testing.T) {
	lanes := newSessionOpLanes()
	ran := make(chan struct{})
	lanes.dispatch("k", func() { close(ran) })
	lanes.shutdown()
	<-ran
	if lanes.dispatch("k", func() { t.Error("job ran after shutdown") }) {
		t.Fatal("dispatch after shutdown returned true")
	}
}

func TestSessionOpKeyExtractsSessionID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"present", `{"sessionId":"s-1"}`, "s-1"},
		{"trimmed", `{"sessionId":"  s-2  "}`, "s-2"},
		{"missing", `{"other":true}`, "\x00unkeyed"},
		{"invalid", `not-json`, "\x00unkeyed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionOpKey(json.RawMessage(tc.in)); got != tc.want {
				t.Fatalf("sessionOpKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunSessionOpSerializesBehindInFlightLifecycleWork proves the hybrid
// routing: a session-scoped request for a session with an in-flight lifecycle
// operation is queued (and returns immediately), while an unrelated session
// keeps its synchronous path.
func TestRunSessionOpSerializesBehindInFlightLifecycleWork(t *testing.T) {
	srv := &server{ops: newSessionOpLanes(), w: &bytes.Buffer{}}

	release := make(chan struct{})
	loadStarted := make(chan struct{})
	srv.dispatchSessionOp(rpcRequest{ID: json.RawMessage("1")}, "session-a", func() {
		close(loadStarted)
		<-release
	})
	<-loadStarted

	var mu sync.Mutex
	var ran []string
	runSessionOpDone := make(chan struct{})
	go func() {
		srv.runSessionOp(
			rpcRequest{ID: json.RawMessage("2"), Params: json.RawMessage(`{"sessionId":"session-a"}`)},
			sessionOpKey(json.RawMessage(`{"sessionId":"session-a"}`)),
			func() {
				mu.Lock()
				ran = append(ran, "a-prompt")
				mu.Unlock()
			},
		)
		close(runSessionOpDone)
	}()

	// The queued prompt must not run while the load holds the lane.
	<-runSessionOpDone
	mu.Lock()
	if len(ran) != 0 {
		mu.Unlock()
		t.Fatalf("queued prompt ran before the load released: %v", ran)
	}
	mu.Unlock()

	// A different session keeps the synchronous fast path: it runs inline, so by
	// the time runSessionOp returns the job has already executed.
	srv.runSessionOp(
		rpcRequest{ID: json.RawMessage("3"), Params: json.RawMessage(`{"sessionId":"session-b"}`)},
		sessionOpKey(json.RawMessage(`{"sessionId":"session-b"}`)),
		func() {
			mu.Lock()
			ran = append(ran, "b-prompt")
			mu.Unlock()
		},
	)

	close(release)
	waitForCondition(t, "queued prompt to run after the load", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, v := range ran {
			if v == "a-prompt" {
				return true
			}
		}
		return false
	})
}

func TestDispatchSessionOpReportsShutdown(t *testing.T) {
	srv := &server{ops: newSessionOpLanes(), w: &bytes.Buffer{}}
	srv.ops.shutdown()
	srv.dispatchSessionOp(rpcRequest{ID: json.RawMessage("9")}, "s", func() {
		t.Error("job ran after shutdown")
	})
	msgs := jsonLines(t, srv.w.(*bytes.Buffer))
	if len(msgs) != 1 {
		t.Fatalf("messages = %#v, want one shutdown error", msgs)
	}
	errObj, ok := msgs[0]["error"].(map[string]any)
	if !ok || errObj["code"] != float64(-32000) {
		t.Fatalf("shutdown response = %#v, want code -32000", msgs[0])
	}
}

// TestDispatchRequestKeepsCancelReachableDuringSlowLoad is the read-loop-level
// regression for the head-of-line problem: while a session's lane is busy with
// a slow operation, a session/load for that session is queued off the caller,
// and session/cancel is still answered instead of waiting behind it.
func TestDispatchRequestKeepsCancelReachableDuringSlowLoad(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	newTestSession(t, cwd, dir, "slow-load-session", 1)
	var out bytes.Buffer
	srv := testSessionServer(cwd, dir, &out)
	srv.ops = newSessionOpLanes()
	defer srv.shutdownAllSessionRuntimes()

	release := make(chan struct{})
	loadStarted := make(chan struct{})
	srv.dispatchSessionOp(rpcRequest{ID: json.RawMessage("100")}, "slow-load-session", func() {
		close(loadStarted)
		<-release
	})
	<-loadStarted

	// A session/load for the blocked session must be queued: the call returns
	// immediately even though the lane is busy.
	loadReturned := make(chan struct{})
	go func() {
		srv.dispatchRequest(rpcRequest{
			ID:     json.RawMessage("1"),
			Method: "session/load",
			Params: json.RawMessage(fmt.Sprintf(`{"sessionId":"slow-load-session","cwd":%q}`, cwd)),
		})
		close(loadReturned)
	}()
	waitForCondition(t, "session/load to be dispatched off the read loop", func() bool {
		select {
		case <-loadReturned:
			return true
		default:
			return false
		}
	})

	// session/cancel is handled on the read loop, not on the lane: it answers
	// immediately (unknown session, because the load has not installed one yet)
	// rather than waiting for the in-flight load to finish.
	srv.dispatchRequest(rpcRequest{
		ID:     json.RawMessage("2"),
		Method: "session/cancel",
		Params: json.RawMessage(`{"sessionId":"slow-load-session"}`),
	})
	answered := false
	for _, message := range jsonLines(t, &out) {
		if message["id"] == float64(2) {
			answered = true
		}
	}
	if !answered {
		t.Fatal("session/cancel was not answered while a load was in flight")
	}

	close(release)
	waitForCondition(t, "queued load to finish", func() bool { return !srv.ops.busy("slow-load-session") })
}
