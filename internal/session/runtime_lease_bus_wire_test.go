package session

import (
	"encoding/json"
	"testing"
)

// TestRuntimeLeaseNotificationV3WireFields pins the version-3 wire contract:
// global-scope synchronization needs DatabaseIdentity and Seq, the optional
// RunStatus/Phase hints must survive JSON, and a session-scoped event must fit
// the raised datagram limit.
func TestRuntimeLeaseNotificationV3WireFields(t *testing.T) {
	original := RuntimeLeaseNotification{
		Version:          runtimeLeaseBusVersion,
		MessageID:        "message-1",
		Type:             "state_changed",
		SessionID:        "session-1",
		DatabaseIdentity: "/tmp/mothx-sessions/sessions.db",
		Origin:           "tui",
		OriginInstanceID: "instance-1",
		OwnerInstanceID:  "instance-1",
		Epoch:            7,
		ExpiresAt:        1234567890,
		Seq:              42,
		RunStatus:        "running",
		Phase:            "executing",
	}
	if !validRuntimeLeaseNotification(original) {
		t.Fatal("a complete version-3 lease notification must be valid")
	}
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > runtimeLeaseBusPayloadLimit {
		t.Fatalf("a session-scoped lease notification (%d bytes) must fit the payload limit", len(payload))
	}
	var decoded RuntimeLeaseNotification
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DatabaseIdentity != original.DatabaseIdentity || decoded.Seq != original.Seq ||
		decoded.RunStatus != original.RunStatus || decoded.Phase != original.Phase {
		t.Fatalf("version-3 wire fields did not round-trip: %#v", decoded)
	}
}

// TestRuntimeLeaseNotificationAcceptsOwnershipSnapshot pins that the periodic
// anti-entropy type is a valid wire event.
func TestRuntimeLeaseNotificationAcceptsOwnershipSnapshot(t *testing.T) {
	snapshot := RuntimeLeaseNotification{
		Version: runtimeLeaseBusVersion, MessageID: "message-snapshot",
		Type: runtimeLeaseBusOwnershipSnapshot, SessionID: "session-1",
		DatabaseIdentity: "/tmp/mothx-sessions/sessions.db", OriginInstanceID: "instance-1",
	}
	if !validRuntimeLeaseNotification(snapshot) {
		t.Fatal("ownership_snapshot must be a valid wire event")
	}
}

// TestRuntimeLeaseBusVersionAndPayloadLimit pins the negotiated version and the
// datagram bound that the ownership snapshot batching must respect.
func TestRuntimeLeaseBusVersionAndPayloadLimit(t *testing.T) {
	if runtimeLeaseBusVersion != 3 {
		t.Fatalf("runtime lease bus version = %d, want 3", runtimeLeaseBusVersion)
	}
	if runtimeLeaseBusPayloadLimit != 4096 {
		t.Fatalf("runtime lease bus payload limit = %d, want 4096", runtimeLeaseBusPayloadLimit)
	}
}
