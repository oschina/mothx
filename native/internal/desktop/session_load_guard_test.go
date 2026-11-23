package desktop

import (
	"errors"
	"strings"
	"testing"
)

// A session/load response that belongs to an older selection must never mutate
// the session the user switched to. These tests pin both the success and failure
// projections.
func TestSessionLoadGuardIgnoresStaleSuccess(t *testing.T) {
	a := testApp()
	a.currentSession = "s2"
	a.loadingSession = "s2"
	a.sessionLoadSeq = 2

	a.finishSessionLoad("s1", sessionResult{SessionID: "s1"}, "stale", 1, a.reloadSeq)

	if a.currentSession != "s2" || a.loadingSession != "s2" {
		t.Fatalf("stale success mutated the visible session: current=%q loading=%q", a.currentSession, a.loadingSession)
	}
}

func TestSessionLoadGuardIgnoresStaleFailure(t *testing.T) {
	a := testApp()
	a.currentSession = "s2"
	a.sessionLoadSeq = 2

	a.failSessionLoad("s1", errors.New("load failed"), 1, a.reloadSeq)

	if a.currentSession != "s2" {
		t.Fatalf("stale failure cleared the visible session: %q", a.currentSession)
	}
}

func TestSessionLoadGuardIgnoresFailureAfterReconnect(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.loadingSession = "s1"
	a.sessionLoadSeq = 1
	a.reloadSeq = 7

	a.failSessionLoad("s1", errors.New("load failed"), 1, 6)

	if a.currentSession != "s1" || a.loadingSession != "s1" {
		t.Fatalf("failure from a replaced connection mutated state: current=%q loading=%q", a.currentSession, a.loadingSession)
	}
}

// A genuine failure for the visible session resets the shell and retires the
// stale cached transcript so a following new task cannot inherit it.
func TestSessionLoadFailureClearsCachedTranscript(t *testing.T) {
	a := testApp()
	a.sessionStates = map[string]sessionSnapshot{
		"s1": {ID: "s1", Messages: []*chatMessage{{ID: "m1", Kind: messageUser, Text: "old"}}},
	}
	a.currentSession = "s1"
	a.loadingSession = "s1"
	a.sessionLoadSeq = 1

	a.failSessionLoad("s1", errors.New("load failed"), 1, a.reloadSeq)

	if a.currentSession != "" || a.loadingSession != "" {
		t.Fatalf("failed load did not return to the idle shell: current=%q loading=%q", a.currentSession, a.loadingSession)
	}
	if _, ok := a.sessionStates["s1"]; ok {
		t.Fatal("failed load kept the stale cached transcript")
	}
	if len(a.messages) == 0 || !strings.Contains(a.messages[len(a.messages)-1].Text, "载入会话失败") {
		t.Fatalf("failed load must keep its error visible: %+v", a.messages)
	}
}
