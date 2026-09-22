package channels

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func newWatchdogTestDispatcher(t *testing.T, cfg *Config) (*Dispatcher, string) {
	t.Helper()
	if cfg == nil {
		cfg = DefaultConfig()
	}
	sessionDir := t.TempDir()
	d := &Dispatcher{
		cfg:        cfg,
		sessionDir: sessionDir,
		sessions:   make(map[string]*ChannelSession),
	}
	return d, sessionDir
}

func registerRunningSession(t *testing.T, d *Dispatcher, sessionDir, sessionID string, startedAt, lastEventAt time.Time) (*ChannelSession, context.Context, *agent.Agent) {
	t.Helper()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "run-1", SessionID: sessionID, Status: "running",
		StartedAt: startedAt, UpdatedAt: startedAt,
	}); err != nil {
		t.Fatalf("save run: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runningAgent := agent.New(agent.Config{Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	sess := &ChannelSession{ID: sessionID, Platform: "wechat", UserID: "u1"}
	sess.runStateMu.Lock()
	sess.runID = "run-1"
	sess.runCancel = cancel
	sess.runAgent = runningAgent
	sess.runStartedAt = startedAt
	sess.lastEventAt = lastEventAt
	sess.runStateMu.Unlock()
	d.sessions[sessionKey("wechat", "u1")] = sess
	return sess, ctx, runningAgent
}

func assertAgentAborted(t *testing.T, a *agent.Agent) {
	t.Helper()
	ch := make(chan agent.Event, 1)
	if answer := a.RequestQuestion(context.Background(), ch, "q", []string{"a"}, ""); answer != "" {
		t.Fatal("expected aborted agent to unblock question waits with an empty answer")
	}
}

func TestWatchdogForceStopsStaleRun(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	now := time.Now()
	sess, ctx, runningAgent := registerRunningSession(t, d, sessionDir, "sess-stale", now.Add(-time.Hour), now.Add(-time.Hour))

	d.checkStalledRuns(now)

	select {
	case <-ctx.Done():
	default:
		t.Fatal("watchdog did not cancel the stalled run context")
	}
	assertAgentAborted(t, runningAgent)

	run, err := session.GetSessionRun(sessionDir, "run-1")
	if err != nil || run == nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "cancelling" || !strings.Contains(run.Error, "watchdog") {
		t.Fatalf("run status = %q error = %q, want cancelling with watchdog message", run.Status, run.Error)
	}
	events, err := session.ListSessionRunEvents(sessionDir, sess.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var watchdogEvents int
	for _, ev := range events {
		if ev.Source == "channel:watchdog" {
			watchdogEvents++
		}
	}
	if watchdogEvents != 1 {
		t.Fatalf("watchdog events = %d, want 1", watchdogEvents)
	}

	// A run that ignores abort must not be spammed on every tick.
	d.checkStalledRuns(now.Add(2 * time.Hour))
	events, _ = session.ListSessionRunEvents(sessionDir, sess.ID)
	watchdogEvents = 0
	for _, ev := range events {
		if ev.Source == "channel:watchdog" {
			watchdogEvents++
		}
	}
	if watchdogEvents != 1 {
		t.Fatalf("watchdog refired: events = %d, want 1", watchdogEvents)
	}
}

func TestWatchdogSkipsActiveRun(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	now := time.Now()
	_, ctx, _ := registerRunningSession(t, d, sessionDir, "sess-active", now.Add(-time.Minute), now)

	d.checkStalledRuns(now)

	select {
	case <-ctx.Done():
		t.Fatal("watchdog cancelled a run that is still making progress")
	default:
	}
	run, _ := session.GetSessionRun(sessionDir, "run-1")
	if run == nil || run.Status != "running" {
		t.Fatalf("run status = %#v, want running", run)
	}
}

func TestWatchdogForceStopsOverlongRun(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agent.RunMaxDurationSecs = 60
	d, sessionDir := newWatchdogTestDispatcher(t, cfg)
	now := time.Now()
	// Recent heartbeat: only the total-duration cap can fire.
	_, ctx, _ := registerRunningSession(t, d, sessionDir, "sess-long", now.Add(-2*time.Hour), now)

	d.checkStalledRuns(now)

	select {
	case <-ctx.Done():
	default:
		t.Fatal("watchdog did not cancel an overlong run")
	}
	run, _ := session.GetSessionRun(sessionDir, "run-1")
	if run == nil || run.Status != "cancelling" || !strings.Contains(run.Error, "max duration") {
		t.Fatalf("run = %#v, want cancelling with max duration reason", run)
	}
}

func TestAcquireRuntimeForRotateBusyWithoutForce(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("sess-busy"); err != nil {
		t.Fatal(err)
	}
	release := session.LockRuntime(sessionDir, "sess-busy")
	defer release()

	if _, err := d.AcquireRuntimeForRotate(context.Background(), sessionDir, "sess-busy", false); err != ErrSessionRunBusy {
		t.Fatalf("err = %v, want ErrSessionRunBusy", err)
	}
}

func TestAcquireRuntimeForRotateForceCancelsAndAcquires(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("sess-force"); err != nil {
		t.Fatal(err)
	}
	release := session.LockRuntime(sessionDir, "sess-force")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sess := &ChannelSession{ID: "sess-force", Platform: "wechat", UserID: "u1"}
	sess.runStateMu.Lock()
	sess.runID = "run-force"
	sess.runCancel = cancel
	sess.runStateMu.Unlock()
	d.sessions[sessionKey("wechat", "u1")] = sess

	// Release the lock once the forced cancellation lands.
	go func() {
		<-ctx.Done()
		release()
	}()

	acquired, err := d.AcquireRuntimeForRotate(context.Background(), sessionDir, "sess-force", true)
	if err != nil {
		t.Fatalf("force acquire: %v", err)
	}
	acquired()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("forced rotation did not cancel the active run")
	}
}

func TestHandleCommandStop(t *testing.T) {
	d, _ := newWatchdogTestDispatcher(t, nil)
	runningAgent := agent.New(agent.Config{Mode: "yolo"}, tools.NewRegistry(t.TempDir(), nil))
	sess := &ChannelSession{ID: "sess-stop", Platform: "wechat", UserID: "u1"}
	d.sessions[sessionKey("wechat", "u1")] = sess
	ctx := beginChannelStopTestRun(t, d, sess, "run-stop", runningAgent)

	reply, err := d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u1", Text: "/stop"})
	if err != nil {
		t.Fatalf("handleCommand(/stop): %v", err)
	}
	if !strings.Contains(reply, "Stop requested") {
		t.Fatalf("reply = %q", reply)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("/stop did not cancel the run context")
	}
	assertAgentAborted(t, runningAgent)

	reply, _ = d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u2", Text: "/stop"})
	if reply != "No active session." {
		t.Fatalf("reply for unknown session = %q", reply)
	}
}

func TestHandleCommandStatusShowsRunState(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sess := &ChannelSession{
		ID: "sess-status", Platform: "wechat", UserID: "u1",
		Manager: mgr, Mode: "yolo",
	}
	sess.runStateMu.Lock()
	sess.runID = "run-status"
	sess.runStartedAt = time.Now().Add(-time.Minute)
	sess.lastEventAt = time.Now().Add(-5 * time.Second)
	sess.runStateMu.Unlock()
	d.sessions[sessionKey("wechat", "u1")] = sess

	reply, err := d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u1", Text: "/status"})
	if err != nil {
		t.Fatalf("handleCommand(/status): %v", err)
	}
	if !strings.Contains(reply, "Run: run-status") || !strings.Contains(reply, "last event") {
		t.Fatalf("status reply missing run state: %q", reply)
	}

	sess.runStateMu.Lock()
	sess.runID = ""
	sess.runStateMu.Unlock()
	reply, _ = d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u1", Text: "/status"})
	if !strings.Contains(reply, "Run: idle") {
		t.Fatalf("status reply missing idle state: %q", reply)
	}
}

func TestHandleCommandNewBusyHintAndForceRotate(t *testing.T) {
	d, sessionDir := newWatchdogTestDispatcher(t, nil)
	old, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "u1")
	if err != nil {
		t.Fatalf("create bound session: %v", err)
	}
	release := session.LockRuntime(sessionDir, old.GetHeader().ID)

	reply, err := d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u1", Text: "/new"})
	if err != nil {
		t.Fatalf("handleCommand(/new): %v", err)
	}
	if !strings.Contains(reply, "/new force") {
		t.Fatalf("busy reply = %q, want hint about /new force", reply)
	}
	binding, _ := session.FindBinding(sessionDir, "wechat", "u1")
	if binding == nil || binding.SessionID != old.GetHeader().ID {
		t.Fatal("non-forced /new must not rotate a busy session")
	}

	// Let the forced rotation acquire the lock shortly after it starts waiting.
	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()
	reply, err = d.handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "u1", Text: "/new force"})
	if err != nil {
		t.Fatalf("handleCommand(/new force): %v", err)
	}
	if !strings.Contains(reply, "New session created") {
		t.Fatalf("force reply = %q", reply)
	}
	binding, _ = session.FindBinding(sessionDir, "wechat", "u1")
	if binding == nil || binding.SessionID == old.GetHeader().ID {
		t.Fatal("forced /new did not rotate the binding")
	}
}
