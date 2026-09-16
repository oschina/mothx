package session

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	database "github.com/startvibecoding/mothx/internal/db"
)

// A database that internal/db had to back up and rebuild after a schema
// migration failure is announced over the same advisory UDP bus as runtime
// lease changes, so every other mothx process on this host that shares the
// session directory learns that the file it may still hold open was replaced.
// The notice carries no content: receivers only retire their cached connection,
// warn the user, and reopen the rebuilt file on the next access (see
// docs/proposal/cross-process-session-execution-ownership-proposal.md for the
// durable-authority rules the bus must never bypass).

var databaseRecoveryHookOnce sync.Once

// ensureDatabaseRecoveryHook wires the recovery announcement into internal/db.
// Every session database open runs EnsureCurrentSchema, so registering the hook
// there covers exactly the processes that can perform a recovery without a
// package-level init side effect.
func ensureDatabaseRecoveryHook() {
	databaseRecoveryHookOnce.Do(func() {
		database.SetMigrationRecoveryNotifier(notifyDatabaseRebuilt)
	})
}

// notifyDatabaseRebuilt announces one recovery over the advisory bus. It never
// carries the migration error, the backup path, or any content: peers only need
// to know which database file was replaced.
func notifyDatabaseRebuilt(recovery database.MigrationRecovery) {
	path := strings.TrimSpace(recovery.Path)
	if path == "" || recovery.Peer {
		return
	}
	publishRuntimeLeaseNotification(RuntimeLeaseNotification{
		Type:   runtimeLeaseBusDatabaseRebuilt,
		Path:   filepath.Clean(path),
		Origin: "db",
	})
}

// peerDatabaseRebuilds records rebuilds other processes announced, so a
// front-end drains local and peer notices through one API and reports them
// exactly once.
var peerDatabaseRebuilds = struct {
	sync.Mutex
	entries []DatabaseRecovery
}{}

// WatchDatabaseRebuilds retires this process's cached connection when another
// process on this host rebuilds a database after a failed migration. Without it,
// a long-running process keeps reading and writing the replaced file through its
// open handle (a deleted inode on Unix) until it restarts.
//
// onNotice, when non-nil, receives each peer notice for UI rendering and is the
// only user-facing channel used (the notice is always recorded for
// TakeDatabaseRecoveries); without it the notice is logged. The returned
// function unsubscribes. The watcher is advisory: a missed datagram only means
// the stale handle is retired later, after a restart.
func WatchDatabaseRebuilds(onNotice func(DatabaseRecovery)) func() {
	return SubscribeRuntimeLeaseNotifications(func(notification RuntimeLeaseNotification) {
		if notification.Type != runtimeLeaseBusDatabaseRebuilt {
			return
		}
		// Closing a connection checkpoints the database, so keep it off the bus
		// reader: a slow close must not delay lease wake-ups behind it.
		go handlePeerDatabaseRebuilt(notification.Path, onNotice)
	})
}

func handlePeerDatabaseRebuilt(path string, onNotice func(DatabaseRecovery)) {
	if path == "" {
		return
	}
	recovery := DatabaseRecovery{Path: path, Peer: true, At: time.Now()}
	// Retire the cached handle first so the notice is only reported once the
	// connection is actually gone; a close failure is reported instead of
	// pretending the process converged.
	if err := database.Close(path); err != nil {
		log.Printf("[db] another MothX process rebuilt %s; closing the cached connection: %v", path, err)
	}
	peerDatabaseRebuilds.Lock()
	peerDatabaseRebuilds.entries = append(peerDatabaseRebuilds.entries, recovery)
	peerDatabaseRebuilds.Unlock()
	if onNotice != nil {
		onNotice(recovery)
		return
	}
	// No UI channel: log it, so headless entry points (serve, ACP, channels) still
	// report that their connection was replaced.
	log.Printf("[db] %s; reopening it on the next access", recovery.Describe())
}

// takePeerDatabaseRebuilds returns and clears the peer notices recorded since
// the last call.
func takePeerDatabaseRebuilds() []DatabaseRecovery {
	peerDatabaseRebuilds.Lock()
	defer peerDatabaseRebuilds.Unlock()
	entries := peerDatabaseRebuilds.entries
	peerDatabaseRebuilds.entries = nil
	return entries
}
