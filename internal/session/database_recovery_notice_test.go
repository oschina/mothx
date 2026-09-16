package session

import (
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestRuntimeLeaseBusDatabaseRebuiltValidation pins the wire contract of the
// rebuild notice: it is database-scoped (no session), carries the replaced file,
// and rejects a notice without one.
func TestRuntimeLeaseBusDatabaseRebuiltValidation(t *testing.T) {
	base := RuntimeLeaseNotification{
		Version:          runtimeLeaseBusVersion,
		MessageID:        "message-1",
		Type:             runtimeLeaseBusDatabaseRebuilt,
		OriginInstanceID: "instance-1",
	}
	withPath := base
	withPath.Path = filepath.Join("/tmp", "sessions", "sessions.db")
	if !validRuntimeLeaseNotification(withPath) {
		t.Fatal("a database rebuild notice with a path must be valid without a session ID")
	}
	withoutPath := base
	if validRuntimeLeaseNotification(withoutPath) {
		t.Fatal("a database rebuild notice without a path must be rejected")
	}
	lease := RuntimeLeaseNotification{
		Version:          runtimeLeaseBusVersion,
		MessageID:        "message-2",
		Type:             "state_changed",
		OriginInstanceID: "instance-1",
	}
	if validRuntimeLeaseNotification(lease) {
		t.Fatal("a lease notification without a session ID must stay invalid")
	}
}

// TestRuntimeLeaseBusBroadcastStaysOnLoopback locks the notification scope: the
// bus is a directed broadcast on the loopback /8 network, so it reaches the other
// mothx processes on this host and never another machine - even when the session
// directory is shared over a network share, and even if a caller tries to widen
// it with an environment variable.
func TestRuntimeLeaseBusBroadcastStaysOnLoopback(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(probe.LocalAddr().(*net.UDPAddr).Port)
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTHX_RUNTIME_BUS_PORT", port)

	for _, scope := range []string{"", "host", "lan", "255.255.255.255"} {
		t.Setenv("MOTHX_RUNTIME_BUS_SCOPE", scope)
		listen, broadcast := runtimeLeaseBusAddresses()
		if broadcast != "127.255.255.255:"+port {
			t.Fatalf("broadcast address for scope %q = %q, want the loopback directed broadcast", scope, broadcast)
		}
		if listen != ":"+port {
			t.Fatalf("listen address for scope %q = %q, want the wildcard bind", scope, listen)
		}
	}
}

// TestPeerDatabaseRebuildRetiresCachedConnection covers the reaction a peer
// process must have: another process replaced the database file, so this
// process's cached handle must go and the notice must be reported once.
func TestPeerDatabaseRebuildRetiresCachedConnection(t *testing.T) {
	TakeDatabaseRecoveries()
	sessionDir := t.TempDir()
	path := RootDatabasePath(sessionDir)
	cached, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := CloseDatabases(); err != nil {
			t.Fatal(err)
		}
	}()

	notices := make(chan DatabaseRecovery, 1)
	handlePeerDatabaseRebuilt(filepath.Join(sessionDir, ".", "sessions.db"), func(recovery DatabaseRecovery) {
		notices <- recovery
	})

	reopened, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened == cached {
		t.Fatal("the cached connection was not retired after a peer rebuild")
	}

	select {
	case recovery := <-notices:
		if !recovery.Peer || recovery.Path != filepath.Clean(path) {
			t.Fatalf("on-notice recovery = %#v, want a peer notice for %s", recovery, path)
		}
	case <-time.After(time.Second):
		t.Fatal("the peer rebuild notice was not delivered to the caller")
	}

	drained := TakeDatabaseRecoveries()
	if len(drained) != 1 || !drained[0].Peer {
		t.Fatalf("drained recoveries = %#v, want the single peer notice", drained)
	}
	if remaining := TakeDatabaseRecoveries(); len(remaining) != 0 {
		t.Fatalf("the peer notice survived the drain: %#v", remaining)
	}
}

// TestPeerDatabaseRebuildIgnoresEmptyPath keeps a malformed datagram harmless.
func TestPeerDatabaseRebuildIgnoresEmptyPath(t *testing.T) {
	TakeDatabaseRecoveries()
	handlePeerDatabaseRebuilt("", nil)
	if notices := TakeDatabaseRecoveries(); len(notices) != 0 {
		t.Fatalf("an empty path produced a notice: %#v", notices)
	}
}
