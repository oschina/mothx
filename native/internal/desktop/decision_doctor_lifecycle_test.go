package desktop

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

// A finished run has no live decision left: the Runtime terminalized them with
// the run. Their cards must close, or a late click reports a choice that
// nothing will act on.
func TestRunEndClosesPendingDecisions(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	pending := &decision{
		ID:      json.RawMessage(`7`),
		Kind:    "permission",
		Title:   "rm -rf build",
		Options: []decisionOption{{ID: "allow_once", Label: "允许一次", Kind: "allow_once"}},
	}
	answered := &decision{ID: json.RawMessage(`8`), Kind: "permission", Resolved: true, ResolvedValue: "已选择：允许一次"}
	a.appendMessage(&chatMessage{ID: "decision:7", Kind: messageDecision, Decision: pending})
	a.appendMessage(&chatMessage{ID: "decision:8", Kind: messageDecision, Decision: answered})

	a.markRunDone("cancelled")

	if !pending.Resolved || pending.ResolvedValue != T("已取消") {
		t.Fatalf("a pending decision outlived its run: resolved=%v value=%q", pending.Resolved, pending.ResolvedValue)
	}
	if answered.ResolvedValue != "已选择：允许一次" {
		t.Fatalf("an answered decision was rewritten to %q", answered.ResolvedValue)
	}
}

// Reconnecting while a diagnosis is running must not leave the button busy
// until the app restarts, and the old run must not publish into the panel of
// the new connection.
func TestReconnectDuringDoctorRunKeepsTheButtonUsable(t *testing.T) {
	a, queue := connectTestApp(t)
	got, release := hosttest.SignalFiles(t)
	t.Setenv(hosttest.EnvHold, "mothx/doctor")
	setLaunchBinaryOverride(acpChildShim(t, "hold"))
	go a.connect()
	if !queue.runUntil(t, 20*time.Second, func() bool {
		return a.conn == connReady && connectAttemptsIdle(a) && a.client() != nil
	}) {
		t.Fatalf("the runtime never connected: conn=%d err=%q", a.conn, a.connErr)
	}
	a.setFeatures(map[string]bool{"doctor": true})

	a.runDoctor()
	if !a.runtime.DoctorLoading {
		t.Fatal("the diagnosis must be in flight")
	}
	hosttest.WaitForFile(t, got)

	// The person reconnects while the diagnosis is still running.
	a.invalidateDoctorPanel()
	if a.runtime.DoctorLoading {
		t.Fatal("reconnecting must release the busy flag")
	}

	// The old run is answered only now; it belongs to the previous connection.
	hosttest.Release(t, release)
	hosttest.WaitForFile(t, got+".done")
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		queue.drain()
		time.Sleep(10 * time.Millisecond)
	}
	if a.runtime.DoctorLoading {
		t.Fatal("a stale diagnosis left the button busy")
	}
	if a.runtime.Doctor != nil || a.runtime.DoctorStatus != "" {
		t.Fatalf("a stale diagnosis published into the new panel: %+v", a.runtime)
	}

	// The button works again.
	if err := os.Remove(got + ".done"); err != nil {
		t.Fatal(err)
	}
	a.runDoctor()
	if !a.runtime.DoctorLoading {
		t.Fatal("a new diagnosis must be able to start")
	}
	hosttest.WaitForFile(t, got+".done")
	if !queue.runUntil(t, 20*time.Second, func() bool { return !a.runtime.DoctorLoading }) {
		t.Fatal("the new diagnosis never finished")
	}
	if a.runtime.Doctor == nil {
		t.Fatal("the new diagnosis must publish its result")
	}
}
