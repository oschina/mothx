package session

import (
	"context"
	"encoding/json"
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
	Preview     string
	PreviewSeen bool
	Title       string
	Bindings    SessionBindings
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
	state, ok := loadEventStoreDisplayProjection(sessionDir, sessionID)
	if !ok {
		return eventStoreDisplayState{}, false
	}
	// The store may have been appended to while the projection was read. Cache
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

// loadEventStoreDisplayProjection reads the committed EventStore batches with
// a structural-only scan and decodes just the few entries a list row needs.
// It deliberately does not run open-time convergence: listing sessions must
// not repair turns, delete legacy rows, or otherwise acquire canonical-write
// side effects just to render a preview. Canonical session open still performs
// full digest verification and fails closed.
func loadEventStoreDisplayProjection(sessionDir, sessionID string) (eventStoreDisplayState, bool) {
	inspection, err := InspectEventStoreProjection(context.Background(), sessionDir, sessionID)
	if err != nil || (inspection.State != EventStoreHealthy && inspection.State != EventStoreReconciling) {
		return eventStoreDisplayState{}, false
	}
	batches := committedEventBatches(inspection)
	if len(batches) == 0 {
		return eventStoreDisplayState{}, false
	}
	return eventStoreDisplayFromBatches(batches), true
}

func eventStoreDisplayFromBatches(batches []EventBatch) eventStoreDisplayState {
	state := eventStoreDisplayState{}
	for _, batch := range batches {
		for _, event := range batch.Events {
			switch EntryType(event.Kind) {
			case EntryMessage:
				if state.PreviewSeen {
					continue
				}
				var entry MessageEntry
				if json.Unmarshal(event.Payload, &entry) != nil || entry.Message.Role != "user" {
					continue
				}
				text := entry.Message.Content
				if text == "" {
					for _, block := range entry.Message.Contents {
						if block.Type == "text" && block.Text != "" {
							text = block.Text
							break
						}
					}
				}
				if text != "" {
					state.Preview = util.TruncateWithSuffix(text, 60, "...")
				}
				state.PreviewSeen = true
			case EntrySessionInfo:
				var entry SessionInfoEntry
				if json.Unmarshal(event.Payload, &entry) == nil {
					state.Title = entry.Name
				}
			case EntryModelChange:
				var entry ModelChangeEntry
				if json.Unmarshal(event.Payload, &entry) == nil {
					state.Bindings.ModelChange, state.Bindings.HasModelChange = entry, true
				}
			case EntryModeChange:
				var entry ModeChangeEntry
				if json.Unmarshal(event.Payload, &entry) == nil {
					state.Bindings.ModeChange, state.Bindings.HasModeChange = entry, true
				}
			case EntryThinkingChange:
				var entry ThinkingLevelChangeEntry
				if json.Unmarshal(event.Payload, &entry) == nil {
					state.Bindings.ThinkingLevelChange, state.Bindings.HasThinkingLevelChange = entry, true
				}
			case EntryAdditionalDirectories:
				var entry AdditionalDirectoriesEntry
				if json.Unmarshal(event.Payload, &entry) == nil {
					state.Bindings.AdditionalDirectories, state.Bindings.HasAdditionalDirectories = entry, true
				}
			}
		}
	}
	return state
}
