package openaiapi

import (
	"errors"
	"testing"
)

// TestDeferredSessionLookupsProjectEmpty pins the deferred-WebUI-session
// contract: a server-allocated id that has not been persisted yet must project
// an empty result from every session-scoped lookup, never a 404, so opening a
// brand-new WebUI session before its first run does not surface
// session_not_found. Only an arbitrary unknown id is a genuine 404.
func TestDeferredSessionLookupsProjectEmpty(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	id, err := srv.AllocateSessionID()
	if err != nil {
		t.Fatalf("allocate session id: %v", err)
	}
	if srv.isAllocatedSessionID(id) != true {
		t.Fatalf("allocated id %q is not tracked as deferred", id)
	}

	if _, err := srv.GetSessionTrajectory(id, "", 10); err != nil {
		t.Fatalf("trajectory for deferred session: %v", err)
	}
	if _, err := srv.GetSessionMessages(id); err != nil {
		t.Fatalf("messages for deferred session: %v", err)
	}
	if _, err := srv.GetSessionCapabilities(id); err != nil {
		t.Fatalf("capabilities for deferred session: %v", err)
	}
	if _, err := srv.GetSessionRuntime(id); err != nil {
		t.Fatalf("runtime for deferred session: %v", err)
	}
	if events, err := srv.GetSessionRunEvents(id); err != nil {
		t.Fatalf("run events for deferred session: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("run events for deferred session = %#v, want none", events)
	}
	if events, err := srv.GetSessionCapabilityEvents(id); err != nil {
		t.Fatalf("capability events for deferred session: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("capability events for deferred session = %#v, want none", events)
	}
	if agents, err := srv.GetSessionSubAgents(id); err != nil {
		t.Fatalf("sub-agents for deferred session: %v", err)
	} else if len(agents) != 0 {
		t.Fatalf("sub-agents for deferred session = %#v, want none", agents)
	}

	const unknown = "not-an-allocated-session"
	lookups := map[string]func() error{
		"trajectory":        func() error { _, err := srv.GetSessionTrajectory(unknown, "", 10); return err },
		"run events":        func() error { _, err := srv.GetSessionRunEvents(unknown); return err },
		"capability events": func() error { _, err := srv.GetSessionCapabilityEvents(unknown); return err },
		"sub-agents":        func() error { _, err := srv.GetSessionSubAgents(unknown); return err },
	}
	for name, lookup := range lookups {
		if err := lookup(); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("%s for unknown id = %v, want ErrSessionNotFound", name, err)
		}
	}
}
