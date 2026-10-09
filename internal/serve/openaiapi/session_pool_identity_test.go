package openaiapi

import "testing"

// TestGetOrCreateSessionKeepsOneIdentityPerSessionID pins the invariant behind
// the session pool's "<workDir>\x00<id>" key: because a session ID is unique in
// the session database (sessions.id is the primary key), a session ID must
// resolve to exactly one in-memory APISession. A request for the same ID with a
// different work directory is rejected instead of creating a divergent second
// instance that would split in-memory run/decision state.
func TestGetOrCreateSessionKeepsOneIdentityPerSessionID(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	dirA := t.TempDir()
	dirB := t.TempDir()
	const sessionID = "identity-session"

	first, err := srv.getOrCreateSession(sessionID, dirA)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if first.WorkDir != dirA {
		t.Fatalf("work dir = %q, want %q", first.WorkDir, dirA)
	}

	if _, err := srv.getOrCreateSession(sessionID, dirB); err == nil {
		t.Fatal("getOrCreateSession accepted a conflicting work directory for the same id")
	}

	if got := srv.pool.Get(sessionID); got != first {
		t.Fatalf("pool lookup resolved to %p, want the original session %p", got, first)
	}

	again, err := srv.getOrCreateSession(sessionID, dirA)
	if err != nil {
		t.Fatalf("re-open session: %v", err)
	}
	if again != first {
		t.Fatalf("same id + same work dir returned a new instance %p, want %p", again, first)
	}
}
