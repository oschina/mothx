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

// RuntimeLeaseNotification is a best-effort local-process wake-up signal.
// SQLite leases and durable Run rows remain the sole authority: receivers must
// always re-read the database before projecting any state to a client.
//
// Threat model: the UDP bus is unauthenticated. Any process on the same host
// can send spoofed notifications (only the loopback source address is
// checked). This is acceptable because notifications are advisory only; every
// receiver re-validates against the authoritative SQLite lease and Run rows
// before acting, so a forged packet can at most trigger a redundant database
// re-read (or, for a rebuild notice, retire a cached connection and log a
// warning), never an ownership or content change. The bus is deliberately
// host-only: it is a directed broadcast on the loopback network, so it never
// reaches another machine.
type RuntimeLeaseNotification struct {
	Version   int    `json:"version"`
	MessageID string `json:"messageId"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	// Path names the database file for the database_rebuilt wake-up; lease
	// notifications leave it empty because they are session-scoped.
	Path             string `json:"path,omitempty"`
	Origin           string `json:"origin,omitempty"`
	OriginInstanceID string `json:"originInstanceId"`
	OwnerInstanceID  string `json:"ownerInstanceId,omitempty"`
	Epoch            int64  `json:"epoch,omitempty"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"`
}

const (
	// A directed broadcast on the loopback /8 network. It never leaves the
	// host, unlike a LAN broadcast such as 255.255.255.255.
	runtimeLeaseBusDefaultPort = "49371"
	runtimeLeaseBusVersion     = 2
	runtimeLeaseBusDedupeTTL   = 10 * time.Second
	runtimeLeaseBusDedupeLimit = 4096
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
	// subscribers; the deferred cleanup resets state and restarts if a handler
	// subscribed in the meantime.
	if len(runtimeLeaseBus.handlers) == 0 {
		runtimeLeaseBus.Unlock()
		_ = conn.Close()
		return
	}
	runtimeLeaseBus.Unlock()
	defer func() {
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
	buf := make([]byte, 1024)
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
			continue
		}
		if notification.OriginInstanceID == runtimeOwnerID() {
			// This process has already published the corresponding canonical
			// event directly. Ignoring its loopback copy avoids duplicate WebUI
			// projections while other processes still receive the broadcast.
			continue
		}
		runtimeLeaseBus.Lock()
		if !rememberRuntimeLeaseMessageLocked(notification.MessageID, time.Now()) {
			runtimeLeaseBus.Unlock()
			continue
		}
		handlers := make([]func(RuntimeLeaseNotification), 0, len(runtimeLeaseBus.handlers))
		for _, handler := range runtimeLeaseBus.handlers {
			handlers = append(handlers, handler)
		}
		runtimeLeaseBus.Unlock()
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
	case "acquired", "renewed", "released", "lost", "state_changed":
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
	if strings.TrimSpace(notification.Origin) == "" {
		notification.Origin = "runtime"
	}
	if notification.MessageID == "" {
		notification.MessageID = fmt.Sprintf("%s-%d-%d", notification.OriginInstanceID, time.Now().UnixNano(), runtimeLeaseBusMessageSequence.Add(1))
	}
	payload, err := json.Marshal(notification)
	if err != nil || len(payload) > 1024 {
		return
	}
	dialer := net.Dialer{Timeout: 200 * time.Millisecond, Control: runtimeLeaseBusSenderControl}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, broadcastAddress := runtimeLeaseBusAddresses()
	conn, err := dialer.DialContext(ctx, "udp4", broadcastAddress)
	if err != nil {
		runtimeLeaseBusLogf("[udp] send failed type=%s session=%q origin=%s origin_instance=%s message=%s error=%v", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write(payload); err != nil {
		runtimeLeaseBusLogf("[udp] send failed type=%s session=%q origin=%s origin_instance=%s message=%s error=%v", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, err)
		return
	}
	runtimeLeaseBusLogf("[udp] sent type=%s session=%q origin=%s origin_instance=%s message=%s epoch=%d", notification.Type, notification.SessionID, notification.Origin, notification.OriginInstanceID, notification.MessageID, notification.Epoch)
}

// NotifyRuntimeStateChanged wakes local observers after a durable Run state
// transition. It never carries Run content and never changes ownership.
func NotifyRuntimeStateChanged(sessionID, origin string) {
	publishRuntimeLeaseNotification(RuntimeLeaseNotification{Type: "state_changed", SessionID: sessionID, Origin: origin})
}

// runtimeLeaseBusDatabaseRebuilt is the advisory wake-up published after a
// database was backed up and rebuilt because its schema could not be migrated.
// Receivers must not treat it as state: it only tells them the file they may
// still hold open was replaced, so they retire their cached connection and warn
// the user. The reason and the backup path stay with the recovering process.
const runtimeLeaseBusDatabaseRebuilt = "database_rebuilt"
