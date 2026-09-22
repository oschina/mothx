package esm

import (
	"context"
	"fmt"
	"sync"

	"github.com/oschina/mothx/internal/provider"
)

// SteeringSource projects the latest active ESM objective into an already
// running Agent at its normal steering boundaries. It never starts a run: the
// adapter remains responsible for idle continuation scheduling.
//
// A source is intentionally scoped to one Agent run. It is safe for the agent
// loop to call Next repeatedly and emits each persisted objective version at
// most once.
type SteeringSource struct {
	store     *Store
	sessionID string

	mu          sync.Mutex
	lastVersion string
}

// NewSteeringSource creates a run-scoped source for one session. A nil Store
// or empty session ID is accepted so adapters can use the source uniformly in
// partially initialized or compatibility paths; Next then returns no message.
func NewSteeringSource(store *Store, sessionID string) *SteeringSource {
	return &SteeringSource{store: store, sessionID: sessionID}
}

// Next returns a single system-injected steering message when an active ESM
// objective is first observed or has changed since the prior call. Paused,
// blocked, limited, completed, and missing objectives deliberately produce no
// steering instruction.
func (s *SteeringSource) Next() []provider.Message {
	if s == nil || s.store == nil || s.sessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Serialize the read together with the version update. Agent loops normally
	// call this serially, but this also prevents two concurrent callers from
	// observing objective versions in reverse order and re-emitting one.
	obj, err := s.store.Get(context.Background(), s.sessionID)
	if err != nil || obj == nil {
		return nil
	}
	version := steeringVersion(obj)
	if version == s.lastVersion {
		return nil
	}
	s.lastVersion = version
	if obj.Status != StatusActive {
		return nil
	}
	return []provider.Message{SteeringMessage(obj)}
}

func steeringVersion(obj *Objective) string {
	if obj == nil {
		return ""
	}
	// UpdatedAt is the persisted optimistic-concurrency version. Include the
	// value fields as a defensive fallback for tests or imported legacy rows
	// whose timestamp was stored at lower precision.
	return fmt.Sprintf("%s|%s|%s|%s|%s", obj.ESMID, obj.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), obj.Status, obj.Phase, obj.Objective)
}
