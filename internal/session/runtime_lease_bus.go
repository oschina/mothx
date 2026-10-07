package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeLeaseNotification is a local-process wake-up signal and an ownership
// synchronization hint. SQLite leases and durable Run rows remain the sole
// authority for admission, cancellation, and ownership: receivers re-read the
// database whenever the synchronized in-memory view is missing, stale, or
// uncertain, and never authorize, cancel, or terminalize from a notification.
//
// Threat model: the UDP bus is unauthenticated. Any process on the same host
// can send spoofed notifications (only the loopback source address is
// checked). This is acceptable because a notification can at most refresh or
// clear a process-local ownership hint and schedule a database re-read; it can
// never grant ownership, cancel a run, or change content. A forged
// database_rebuilt notice can retire a cached connection this process actually
// holds for the named file and log a warning; notices for databases it never
// opened are ignored. The bus is deliberately host-only: a directed broadcast
// on the loopback network, so it never reaches another machine.
type RuntimeLeaseNotification struct {
	Version   int    `json:"version"`
	MessageID string `json:"messageId"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	// DatabaseIdentity names the canonical session database this notification is
	// scoped to (see RuntimeDatabaseIdentity). Global-scope ownership
	// synchronization keys its in-memory view by (DatabaseIdentity, SessionID),
	// so producers that know the session directory must stamp it. Notifications
	// published without one are still valid; receivers then fall back to SQLite.
	DatabaseIdentity string `json:"databaseIdentity,omitempty"`
	// Path names the database file for the database_rebuilt wake-up; lease
	// notifications leave it empty because they are session-scoped.
	Path             string `json:"path,omitempty"`
	Origin           string `json:"origin,omitempty"`
	OriginInstanceID string `json:"originInstanceId"`
	OwnerInstanceID  string `json:"ownerInstanceId,omitempty"`
	OwnerPID         int    `json:"ownerPid,omitempty"`
	RunID            string `json:"runId,omitempty"`
	Epoch            int64  `json:"epoch,omitempty"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
	// Seq is a per-OriginInstanceID monotonic sequence. Receivers use it to drop
	// out-of-order duplicates and to detect a gap (a jump means a lost datagram),
	// which schedules a coalesced SQLite re-read. It never authorizes anything.
	Seq uint64 `json:"seq,omitempty"`
	// RunStatus and Phase are optional projection hints carried by state_changed
	// events; receivers still re-read the durable Run before projecting it.
	RunStatus string `json:"runStatus,omitempty"`
	Phase     string `json:"phase,omitempty"`
}

const (
	// A directed broadcast on the loopback /8 network. It never leaves the
	// host, unlike a LAN broadcast such as 255.255.255.255.
	runtimeLeaseBusDefaultPort = "49371"
	runtimeLeaseBusVersion     = 3
	runtimeLeaseBusDedupeTTL   = 10 * time.Second
	runtimeLeaseBusDedupeLimit = 4096
	// runtimeLeaseBusPayloadLimit bounds one datagram. It is larger than the
	// session-scoped lease events so a bounded ownership snapshot batch fits;
	// larger payloads are dropped rather than fragmented (batches, not UDP
	// fragmentation, keep multi-session sync within the limit).
	runtimeLeaseBusPayloadLimit = 4096
)

var runtimeLeaseBus = struct {
	sync.Mutex
	started   bool
	listening bool
	conn      net.PacketConn
	handlers  map[uint64]func(RuntimeLeaseNotification)
	nextID    uint64
	seen      map[string]time.Time
}{handlers: make(map[uint64]func(RuntimeLeaseNotification)), seen: make(map[string]time.Time)}

var runtimeLeaseBusMessageSequence atomic.Uint64

// runtimeLeaseBusSeq is the per-process OriginInstanceID sequence stamped on
// every published ownership notification. Receivers drop older/duplicate
// messages and detect gaps with it; it is never an authorization token.
var runtimeLeaseBusSeq atomic.Uint64

var runtimeLeaseBusLogs = struct {
	sync.Mutex
	nextID uint64
	sinks  map[uint64]func(string)
}{sinks: make(map[uint64]func(string))}

// SubscribeRuntimeLeaseLogs receives UDP diagnostics without writing them to
// the process-wide logger. Serve uses this to expose the messages in WebUI;
// TUI, CLI, and channel transports must not receive protocol diagnostics.
func SubscribeRuntimeLeaseLogs(sink func(string)) func() {
	if sink == nil {
		return func() {}
	}
	runtimeLeaseBusLogs.Lock()
	runtimeLeaseBusLogs.nextID++
	id := runtimeLeaseBusLogs.nextID
	runtimeLeaseBusLogs.sinks[id] = sink
	runtimeLeaseBusLogs.Unlock()
	return func() {
		runtimeLeaseBusLogs.Lock()
		delete(runtimeLeaseBusLogs.sinks, id)
		runtimeLeaseBusLogs.Unlock()
	}
}

func runtimeLeaseBusLogf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	runtimeLeaseBusLogs.Lock()
	sinks := make([]func(string), 0, len(runtimeLeaseBusLogs.sinks))
	for _, sink := range runtimeLeaseBusLogs.sinks {
		sinks = append(sinks, sink)
	}
	runtimeLeaseBusLogs.Unlock()
	for _, sink := range sinks {
		sink(message)
	}
}

// SubscribeRuntimeLeaseNotifications receives best-effort notifications from
// other local processes. It is deliberately optional: a bind failure simply
// leaves durable SQLite replay as the synchronization path.
func SubscribeRuntimeLeaseNotifications(handler func(RuntimeLeaseNotification)) func() {
	if handler == nil {
		return func() {}
	}
	runtimeLeaseBus.Lock()
	runtimeLeaseBus.nextID++
	id := runtimeLeaseBus.nextID
	runtimeLeaseBus.handlers[id] = handler
	if !runtimeLeaseBus.started {
		runtimeLeaseBus.started = true
		go runRuntimeLeaseBus()
	}
	runtimeLeaseBus.Unlock()
	return func() {
		runtimeLeaseBus.Lock()
		delete(runtimeLeaseBus.handlers, id)
		// Stop the listener once every handler unsubscribed so the bus
		// goroutine does not outlive its last subscriber.
		if len(runtimeLeaseBus.handlers) == 0 && runtimeLeaseBus.conn != nil {
			_ = runtimeLeaseBus.conn.Close()
		}
		runtimeLeaseBus.Unlock()
	}
}

func runtimeLeaseBusAddresses() (listen, broadcast string) {
	port := strings.TrimSpace(os.Getenv("MOTHX_RUNTIME_BUS_PORT"))
	if port == "" {
		port = runtimeLeaseBusDefaultPort
	}
	// A wildcard bind is required to receive the directed broadcast. Every
	// listener verifies the packet source is loopback before processing it, and
	// the send address is always the loopback directed broadcast: the bus is
	// host-only by design, so no environment variable widens it to a LAN
	// broadcast.
	return net.JoinHostPort("", port), net.JoinHostPort("127.255.255.255", port)
}

func runRuntimeLeaseBus() {
	listenAddress, _ := runtimeLeaseBusAddresses()
	listener := net.ListenConfig{Control: runtimeLeaseBusListenerControl}
	conn, err := listener.ListenPacket(context.Background(), "udp4", listenAddress)
	if err != nil {
		runtimeLeaseBus.Lock()
		runtimeLeaseBus.started = false
		runtimeLeaseBus.Unlock()
		runtimeLeaseBusLogf("[udp] listener unavailable address=%s error=%v", listenAddress, err)
		return
	}
	runtimeLeaseBus.Lock()
	runtimeLeaseBus.listening = true
	runtimeLeaseBus.conn = conn
	// The last handler may have unsubscribed while the socket was still
	// binding, before conn was stored where the unsubscribe path could close
	// it. Close now so the goroutine exits instead of reading forever with no
	// subscribers. This return happens before the deferred cleanup is installed,
	// so reset every field here too: leaving started=true (with a closed conn and
	// listening still true) would make a later subscriber skip starting a new
	// goroutine and silently disable the bus for the rest of the process.
	if len(runtimeLeaseBus.handlers) == 0 {
		runtimeLeaseBus.listening = false
		runtimeLeaseBus.conn = nil
		runtimeLeaseBus.started = false
		runtimeLeaseBus.Unlock()
		_ = conn.Close()
		return
	}
	runtimeLeaseBus.Unlock()
	snapshotStop := make(chan struct{})
	go runtimeOwnershipSnapshotLoop(snapshotStop)
	defer func() {
		close(snapshotStop)
		_ = conn.Close()
		runtimeLeaseBus.Lock()
		runtimeLeaseBus.listening = false
		runtimeLeaseBus.conn = nil
		// A handler may have subscribed between the close trigger and this
		// cleanup; restart instead of leaving it without a listener.
		restart := len(runtimeLeaseBus.handlers) > 0
		runtimeLeaseBus.started = restart
		runtimeLeaseBus.Unlock()
		if restart {
			go runRuntimeLeaseBus()
		}
	}()
	runtimeLeaseBusLogf("[udp] listener started address=%s", listenAddress)
	buf := make([]byte, runtimeLeaseBusPayloadLimit)
	for {
		n, source, readErr := conn.ReadFrom(buf)
		if readErr != nil {
			return
		}
		sourceAddr, ok := source.(*net.UDPAddr)
		if !ok || !sourceAddr.IP.IsLoopback() {
			continue
		}
		var notification RuntimeLeaseNotification
		if json.Unmarshal(buf[:n], &notification) != nil || !validRuntimeLeaseNotification(notification) {
			runtimeLeaseMetrics.invalid.Add(1)
			continue
		}
		if notification.OriginInstanceID == runtimeOwnerID() {
			// This process has already published the corresponding canonical
			// event directly. Ignoring its loopback copy avoids duplicate WebUI
			// projections while other processes still receive the broadcast.
			runtimeLeaseMetrics.selfSkipped.Add(1)
			continue
		}
		runtimeLeaseBus.Lock()
		if !rememberRuntimeLeaseMessageLocked(notification.MessageID, time.Now()) {
			runtimeLeaseMetrics.duplicates.Add(1)
			runtimeLeaseBus.Unlock()
			continue
		}
		handlers := make([]func(RuntimeLeaseNotification), 0, len(runtimeLeaseBus.handlers))
		for _, handler := range runtimeLeaseBus.handlers {
			handlers = append(handlers, handler)
		}
		runtimeLeaseBus.Unlock()
		runtimeLeaseMetrics.received.Add(1)
		// Update the process-wide ownership view for every accepted notification,
		// independent of which adapters are subscribed: the view is global-scope.
		ingestRuntimeOwnership(notification)
		runtimeLeaseBusLogf("[udp] received type=%s session=%q origin=%s origin_instance=%s message=%s epoch=%d source=%s", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, notification.Epoch, source)
		for _, handler := range handlers {
			handler(notification)
		}
	}
}

func rememberRuntimeLeaseMessageLocked(messageID string, now time.Time) bool {
	if _, exists := runtimeLeaseBus.seen[messageID]; exists {
		return false
	}
	for id, expiresAt := range runtimeLeaseBus.seen {
		if !expiresAt.After(now) {
			delete(runtimeLeaseBus.seen, id)
		}
	}
	if len(runtimeLeaseBus.seen) >= runtimeLeaseBusDedupeLimit {
		for id := range runtimeLeaseBus.seen {
			delete(runtimeLeaseBus.seen, id)
			break
		}
	}
	runtimeLeaseBus.seen[messageID] = now.Add(runtimeLeaseBusDedupeTTL)
	return true
}

func validRuntimeLeaseNotification(notification RuntimeLeaseNotification) bool {
	if notification.Version != runtimeLeaseBusVersion || strings.TrimSpace(notification.MessageID) == "" || len(notification.MessageID) > 256 || strings.TrimSpace(notification.OriginInstanceID) == "" || len(notification.OriginInstanceID) > 256 || len(notification.Origin) > 128 {
		return false
	}
	if notification.Type == runtimeLeaseBusDatabaseRebuilt {
		// A rebuild notice is database-scoped: it carries the replaced file and
		// no session, and carries no content at all.
		return strings.TrimSpace(notification.Path) != "" && len(notification.Path) <= 4096
	}
	if strings.TrimSpace(notification.SessionID) == "" || len(notification.SessionID) > 256 {
		return false
	}
	switch notification.Type {
	// "renewed" is deliberately absent: heartbeat renewals must never be
	// broadcast (that would be one packet per session every 3s); only ownership
	// transitions and durable run state changes wake peers.
	case "acquired", "released", "lost", "state_changed", runtimeLeaseBusOwnershipSnapshot:
		return true
	default:
		return false
	}
}

func publishRuntimeLeaseNotification(notification RuntimeLeaseNotification) {
	if notification.SessionID == "" && notification.Type != runtimeLeaseBusDatabaseRebuilt {
		return
	}
	notification.Version = runtimeLeaseBusVersion
	notification.OriginInstanceID = runtimeOwnerID()
	if notification.Seq == 0 {
		// A caller that batches several entries under one generation stamp sets
		// Seq itself; otherwise every notification gets the next per-origin value.
		notification.Seq = runtimeLeaseBusSeq.Add(1)
	}
	if strings.TrimSpace(notification.Origin) == "" {
		notification.Origin = "runtime"
	}
	if notification.MessageID == "" {
		notification.MessageID = fmt.Sprintf("%s-%d-%d", notification.OriginInstanceID, time.Now().UnixNano(), runtimeLeaseBusMessageSequence.Add(1))
	}
	payload, err := json.Marshal(notification)
	if err != nil || len(payload) > runtimeLeaseBusPayloadLimit {
		runtimeLeaseMetrics.sendFailures.Add(1)
		return
	}
	_, broadcastAddress := runtimeLeaseBusAddresses()
	conn, err := runtimeLeaseBusSenderConn(broadcastAddress)
	if err != nil {
		runtimeLeaseMetrics.sendFailures.Add(1)
		runtimeLeaseBusLogf("[udp] send failed type=%s session=%q origin=%s origin_instance=%s message=%s error=%v", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, err)
		return
	}
	// A write deadline keeps a saturated socket buffer from stalling the
	// publisher's hot path (lease release, run terminalization); loopback UDP
	// normally never blocks because the kernel drops instead.
	_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := conn.Write(payload); err != nil {
		runtimeLeaseMetrics.sendFailures.Add(1)
		dropRuntimeLeaseBusSenderConn(conn)
		runtimeLeaseBusLogf("[udp] send failed type=%s session=%q origin=%s origin_instance=%s message=%s error=%v", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, err)
		return
	}
	runtimeLeaseMetrics.published.Add(1)
	runtimeLeaseBusLogf("[udp] sent type=%s session=%q origin=%s origin_instance=%s message=%s epoch=%d", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, notification.Epoch)
}

// runtimeLeaseBusSender caches one connected UDP socket for the loopback
// directed broadcast address. Publishing stays synchronous (a process exiting
// right after a lease release must not lose the datagram), but the per-message
// dial cost and its pathological 200ms ceiling are paid at most once per
// address change; after a dial failure further attempts back off briefly so a
// host without usable loopback routing degrades to near-free skips instead of
// stalling every lease event.
var runtimeLeaseBusSender = struct {
	sync.Mutex
	addr       string
	conn       net.Conn
	nextDialAt time.Time
}{}

const runtimeLeaseBusDialBackoff = 5 * time.Second

func runtimeLeaseBusSenderConn(address string) (net.Conn, error) {
	runtimeLeaseBusSender.Lock()
	defer runtimeLeaseBusSender.Unlock()
	if runtimeLeaseBusSender.conn != nil && runtimeLeaseBusSender.addr == address {
		return runtimeLeaseBusSender.conn, nil
	}
	if runtimeLeaseBusSender.conn != nil {
		_ = runtimeLeaseBusSender.conn.Close()
		runtimeLeaseBusSender.conn = nil
		runtimeLeaseBusSender.addr = ""
	}
	if now := time.Now(); now.Before(runtimeLeaseBusSender.nextDialAt) {
		return nil, fmt.Errorf("runtime lease bus sender is backing off until %s", runtimeLeaseBusSender.nextDialAt.Format(time.RFC3339))
	}
	dialer := net.Dialer{Timeout: 200 * time.Millisecond, Control: runtimeLeaseBusSenderControl}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "udp4", address)
	if err != nil {
		runtimeLeaseBusSender.nextDialAt = time.Now().Add(runtimeLeaseBusDialBackoff)
		return nil, err
	}
	runtimeLeaseBusSender.addr = address
	runtimeLeaseBusSender.conn = conn
	return conn, nil
}

func dropRuntimeLeaseBusSenderConn(conn net.Conn) {
	runtimeLeaseBusSender.Lock()
	defer runtimeLeaseBusSender.Unlock()
	if runtimeLeaseBusSender.conn == conn {
		runtimeLeaseBusSender.conn = nil
		runtimeLeaseBusSender.addr = ""
	}
	_ = conn.Close()
}

// runtimeLeaseBusOwnershipSnapshot re-announces a held lease. It is the
// repair/refresh path: receivers treat it like an acquire, and it keeps an
// otherwise-silent owner (heartbeat renewals are not broadcast) fresh within
// runtimeOwnershipLivenessWindow.
const runtimeLeaseBusOwnershipSnapshot = "ownership_snapshot"

// runtimeOwnershipSnapshotInterval is shorter than runtimeOwnershipLivenessWindow
// so a live owner keeps peers' views fresh and repairs a lost acquire event.
const runtimeOwnershipSnapshotInterval = 10 * time.Second

// runtimeOwnershipSnapshotIntervalValue returns the effective anti-entropy
// interval, honoring a test override. Production always uses the constant.
func runtimeOwnershipSnapshotIntervalValue() time.Duration {
	if value := strings.TrimSpace(os.Getenv("MOTHX_RUNTIME_SNAPSHOT_INTERVAL")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return runtimeOwnershipSnapshotInterval
}

// publishOwnershipSnapshots re-announces every lease this process still holds.
// Each lease is its own datagram so a multi-session process stays inside the
// payload limit without UDP fragmentation.
func publishOwnershipSnapshots() {
	activeRuntimeLeases.Lock()
	leases := make([]*runtimeLease, 0, len(activeRuntimeLeases.leases))
	for _, lease := range activeRuntimeLeases.leases {
		leases = append(leases, lease)
	}
	activeRuntimeLeases.Unlock()
	for _, lease := range leases {
		if lease == nil {
			continue
		}
		lease.bindingMu.RLock()
		if lease.released {
			lease.bindingMu.RUnlock()
			continue
		}
		notification := RuntimeLeaseNotification{
			Type: runtimeLeaseBusOwnershipSnapshot, SessionID: lease.sessionID,
			DatabaseIdentity: runtimeDatabaseIdentity(lease.sessionDir), Origin: lease.purpose,
			OwnerInstanceID: lease.ownerID, OwnerPID: os.Getpid(), RunID: lease.runID, Epoch: lease.epoch,
			RunStatus: runtimeLeaseRunStatusHint(lease.purpose),
		}
		if !lease.expiresAt.IsZero() {
			notification.ExpiresAt = lease.expiresAt.Unix()
		}
		lease.bindingMu.RUnlock()
		publishRuntimeLeaseNotification(notification)
	}
}

func runtimeOwnershipSnapshotLoop(stop <-chan struct{}) {
	// Announce immediately so a process that just started (or a peer that just
	// subscribed) converges without waiting a full interval.
	publishOwnershipSnapshots()
	ticker := time.NewTicker(runtimeOwnershipSnapshotIntervalValue())
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			publishOwnershipSnapshots()
		}
	}
}

// NotifyRuntimeStateChanged wakes local observers after a durable Run state
// transition. It never carries Run content and never changes ownership. An
// empty sessionDir publishes an unscoped notification (receivers then fall back
// to SQLite); a non-empty one scopes it to the canonical database identity.
func NotifyRuntimeStateChanged(sessionDir, sessionID, origin string) {
	notification := RuntimeLeaseNotification{Type: "state_changed", SessionID: sessionID, Origin: origin}
	if strings.TrimSpace(sessionDir) != "" {
		notification.DatabaseIdentity = runtimeDatabaseIdentity(sessionDir)
	}
	publishRuntimeLeaseNotification(notification)
}

// runtimeLeaseBusDatabaseRebuilt is the advisory wake-up published after a
// database was backed up and rebuilt because its schema could not be migrated.
// Receivers must not treat it as state: it only tells them the file they may
// still hold open was replaced, so they retire their cached connection and warn
// the user. The reason and the backup path stay with the recovering process.
const runtimeLeaseBusDatabaseRebuilt = "database_rebuilt"
