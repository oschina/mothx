package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/util"
)

// eventStoreDisplayState is the list-facing subset of an EventStore session:
// the first user-message preview, the latest title, and the persisted binding
// metadata. It is derived projection data, never replay authority.
type eventStoreDisplayState struct {
	Preview  string
	Title    string
	Bindings SessionBindings
}

type eventStoreDisplayCacheEntry struct {
	fingerprint string
	state       eventStoreDisplayState
}

var eventStoreDisplays sync.Map     // map[string]eventStoreDisplayCacheEntry
var eventStoreDisplayLocks sync.Map // map[string]*sync.Mutex, one load per session

func eventStoreDisplayKey(sessionDir, sessionID string) string {
	return sessionDir + "\x00" + sessionID
}

// eventStorePhysicalFingerprint identifies the physical EventStore layout
// (segment names, sizes, and modification times) without scanning it. Appends
// and segment rotation both change it, so a cached list projection can never
// outlive the bytes it was derived from, and an open can tell whether the log
// it converged against is still the one on disk. A session with no event bytes
// returns "".
func eventStorePhysicalFingerprint(sessionDir, sessionID string) (string, error) {
	store, err := NewEventStore(sessionDir, sessionID)
	if err != nil {
		return "", err
	}
	layout, err := store.scanLayout()
	if err != nil {
		return "", err
	}
	if len(layout.paths) == 0 {
		return "", nil
	}
	var builder strings.Builder
	for _, path := range layout.paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "%s:%d:%d|", filepath.Base(path), info.Size(), info.ModTime().UnixNano())
	}
	return builder.String(), nil
}

// loadEventStoreDisplay returns cached list metadata for one EventStore
// session, loading the canonical session once when the fingerprint changed.
// Concurrent list requests for the same session share a single load. It
// reports false when the session has no EventStore bytes, leaving legacy
// SQLite sessions on their existing path.
func loadEventStoreDisplay(sessionDir, sessionID string) (eventStoreDisplayState, bool) {
	if state, ok := cachedEventStoreDisplay(sessionDir, sessionID); ok {
		return state, true
	}
	key := eventStoreDisplayKey(sessionDir, sessionID)
	lock := eventStoreDisplayLock(key)
	lock.Lock()
	defer lock.Unlock()
	if state, ok := cachedEventStoreDisplay(sessionDir, sessionID); ok {
		return state, true
	}
	fingerprint, err := eventStorePhysicalFingerprint(sessionDir, sessionID)
	if err != nil || fingerprint == "" {
		return eventStoreDisplayState{}, false
	}
	manager, err := loadEventStoreDisplayManager(sessionDir, sessionID)
	if err != nil || manager == nil {
		return eventStoreDisplayState{}, false
	}
	state := eventStoreDisplayFromManager(manager)
	// The store may have been appended to while the Manager loaded. Cache
	// against the fingerprint observed after loading; a concurrent append
	// simply makes the next list recompute.
	if current, err := eventStorePhysicalFingerprint(sessionDir, sessionID); err == nil && current != "" {
		eventStoreDisplays.Store(key, eventStoreDisplayCacheEntry{fingerprint: current, state: state})
	}
	return state, true
}

func cachedEventStoreDisplay(sessionDir, sessionID string) (eventStoreDisplayState, bool) {
	fingerprint, err := eventStorePhysicalFingerprint(sessionDir, sessionID)
	if err != nil || fingerprint == "" {
		return eventStoreDisplayState{}, false
	}
	value, ok := eventStoreDisplays.Load(eventStoreDisplayKey(sessionDir, sessionID))
	if !ok {
		return eventStoreDisplayState{}, false
	}
	entry := value.(eventStoreDisplayCacheEntry)
	if entry.fingerprint != fingerprint {
		return eventStoreDisplayState{}, false
	}
	return entry.state, true
}

func eventStoreDisplayLock(key string) *sync.Mutex {
	lock, _ := eventStoreDisplayLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// loadEventStoreDisplayManager reads committed EventStore batches for a
// read-only list projection. It deliberately does not run open-time EventStore
// convergence: listing sessions must not repair turns, delete legacy rows, or
// otherwise acquire canonical-write side effects just to render a preview.
func loadEventStoreDisplayManager(sessionDir, sessionID string) (*Manager, error) {
	manager := &Manager{sessionDir: sessionDir, eventStoreMode: true}
	entries, useEventStore, err := manager.loadCommittedEventEntries(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	if !useEventStore || len(entries) == 0 {
		return nil, nil
	}
	manager.entries = entries
	return manager, nil
}

func eventStoreDisplayFromManager(manager *Manager) eventStoreDisplayState {
	state := eventStoreDisplayState{}
	for _, message := range manager.GetMessages() {
		if message.Role != "user" {
			continue
		}
		text := message.Content
		if text == "" {
			for _, block := range message.Contents {
				if block.Type == "text" && block.Text != "" {
					text = block.Text
					break
				}
			}
		}
		if text != "" {
			state.Preview = util.TruncateWithSuffix(text, 60, "...")
		}
		break
	}
	if info, ok := manager.GetLatestSessionInfo(); ok {
		state.Title = info.Name
	}
	if entry, ok := manager.GetLatestModelChange(); ok {
		state.Bindings.ModelChange, state.Bindings.HasModelChange = entry, true
	}
	if entry, ok := manager.GetLatestModeChange(); ok {
		state.Bindings.ModeChange, state.Bindings.HasModeChange = entry, true
	}
	if entry, ok := manager.GetLatestThinkingLevelChange(); ok {
		state.Bindings.ThinkingLevelChange, state.Bindings.HasThinkingLevelChange = entry, true
	}
	if entry, ok := manager.GetLatestAdditionalDirectories(); ok {
		state.Bindings.AdditionalDirectories, state.Bindings.HasAdditionalDirectories = entry, true
	}
	return state
}
