package channels

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
)

// watchdogTick is how often the dispatcher scans channel runs for stalls.
const watchdogTick = 15 * time.Second

// startWatchdog launches the run watchdog. It stops when the dispatcher run
// root context is canceled (Dispatcher.Close).
func (d *Dispatcher) startWatchdog() {
	if d == nil || d.runRootCtx == nil {
		return
	}
	go d.runWatchdog()
}

func (d *Dispatcher) runWatchdog() {
	ticker := time.NewTicker(watchdogTick)
	defer ticker.Stop()
	for {
		select {
		case <-d.runRootCtx.Done():
			return
		case now := <-ticker.C:
			d.checkStalledRuns(now)
		}
	}
}

// checkStalledRuns force-stops channel runs that stopped making progress.
// A run is stalled when no agent event arrived within the configured stale
// timeout, or when it exceeds the configured total duration cap. Force-stopping
// aborts the agent (unblocking waits that ignore context cancellation), cancels
// the run context and marks the persisted run as cancelling so /new, /status
// and the WebUI converge on reality instead of reporting a phantom active run.
func (d *Dispatcher) checkStalledRuns(now time.Time) {
	runtime := d.runtimeSnapshot()
	cfg := runtime.cfg
	if cfg == nil {
		cfg = DefaultConfig()
	}
	stale := cfg.Agent.GetRunStaleTimeout()
	maxDuration := cfg.Agent.GetRunMaxDuration()
	if stale <= 0 && maxDuration <= 0 {
		return
	}

	d.mu.RLock()
	sessions := make([]*ChannelSession, 0, len(d.sessions))
	for _, sess := range d.sessions {
		sessions = append(sessions, sess)
	}
	d.mu.RUnlock()

	activeRuns := make(map[string]struct{})
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		sess.runStateMu.Lock()
		runID := sess.runID
		cancel := sess.runCancel
		runningAgent := sess.runAgent
		startedAt := sess.runStartedAt
		lastEventAt := sess.lastEventAt
		sess.runStateMu.Unlock()
		if runID == "" {
			continue
		}
		activeRuns[runID] = struct{}{}

		var reason string
		switch {
		case maxDuration > 0 && !startedAt.IsZero() && now.Sub(startedAt) > maxDuration:
			reason = fmt.Sprintf("run exceeded max duration %s", maxDuration.Round(time.Second))
		case stale > 0 && !lastEventAt.IsZero() && now.Sub(lastEventAt) > stale:
			reason = fmt.Sprintf("no agent events for %s", stale.Round(time.Second))
		}
		if reason == "" {
			continue
		}
		if d.watchdogAlreadyFired(runID) {
			continue
		}
		d.forceStopRun(sess, runID, reason, cancel, runningAgent)
	}
	d.pruneWatchdogFired(activeRuns)
}

func (d *Dispatcher) forceStopRun(sess *ChannelSession, runID, reason string, cancel func(), runningAgent *agent.Agent) {
	sessionID := sess.ID
	log.Printf("[channels] watchdog forcing stop of run %s (session %s): %s", runID, sessionID, reason)
	message := "watchdog: " + reason
	cancelled := false
	if sess.Execution != nil {
		sess.Execution.SetRunStore(agentruntime.RunStore{SessionDir: d.sessionDir})
		sess.Execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: d.sessionDir})
		var err error
		cancelled, err = sess.Execution.CancelDurable(message)
		if err != nil {
			log.Printf("[channels] watchdog persist cancellation for run %s: %v", runID, err)
		}
	}
	if !cancelled {
		if runningAgent != nil {
			runningAgent.Abort()
		}
		if cancel != nil {
			cancel()
		}
		if err := agentruntime.UpdateDurableRun(d.sessionDir, runID, agentruntime.RunStateCancelling, message); err != nil {
			log.Printf("[channels] watchdog update run %s: %v", runID, err)
		}
	}
	eventData, _ := json.Marshal(map[string]string{"error": message})
	event := agentruntime.RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "canceled",
		// Keep the watchdog marker distinct from the originating adapter source;
		// the run row and normal lifecycle events retain the resolved source.
		Source: "channel:watchdog", Status: "cancelling", Data: eventData,
	}
	var eventErr error
	if sess.Execution != nil {
		_, eventErr = sess.Execution.RecordEvent(event)
	} else {
		_, eventErr = (agentruntime.SessionRunEventSink{SessionDir: d.sessionDir}).Record(event)
	}
	if eventErr != nil {
		log.Printf("[channels] watchdog save run event %s: %v", runID, eventErr)
	}
	d.notifyRunObserver(sessionID)
}

// watchdogAlreadyFired records that a run was already force-stopped so a run
// that ignores abort does not get spammed with repeated stop requests. Entries
// are pruned once the run leaves the active set.
func (d *Dispatcher) watchdogAlreadyFired(runID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.watchdogFired == nil {
		d.watchdogFired = make(map[string]struct{})
	}
	if _, ok := d.watchdogFired[runID]; ok {
		return true
	}
	d.watchdogFired[runID] = struct{}{}
	return false
}

func (d *Dispatcher) pruneWatchdogFired(activeRuns map[string]struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for runID := range d.watchdogFired {
		if _, ok := activeRuns[runID]; !ok {
			delete(d.watchdogFired, runID)
		}
	}
}
