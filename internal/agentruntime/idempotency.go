package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// ErrIdempotencyKeyConflict means a submission key was reused for a different
// request or admission scope. Callers must not silently start another Run.
var ErrIdempotencyKeyConflict = session.ErrRuntimeSubmissionConflict

// ErrIdempotencyRunMissing means a durable started event matched a submission
// key but its canonical Run row is unavailable for reconciliation.
var ErrIdempotencyRunMissing = errors.New("idempotency started event has no durable run")

// IdempotencyKeyFingerprint keeps a client/platform key out of durable event
// data while retaining a stable equality token for reconciliation.
func IdempotencyKeyFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("sha256:%x", digest[:])
}

// FindIdempotentRun reconciles a submission key against canonical started
// events. It is intentionally a read-only compatibility bridge until the
// Runtime-owned submission table is migrated; callers must invoke it again
// after acquiring their session/runtime admission locks.
func FindIdempotentRun(ctx context.Context, sessionDir, sessionID, key, fingerprint, scope string) (*session.SessionRun, error) {
	key = strings.TrimSpace(key)
	if sessionDir == "" || sessionID == "" || key == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if scope == "" {
		scope = "submit"
	}
	keyFingerprint := IdempotencyKeyFingerprint(key)
	if submission, err := session.GetRuntimeSubmission(ctx, sessionDir, sessionID, scope, keyFingerprint); err != nil {
		return nil, err
	} else if submission != nil {
		if submission.RequestFingerprint != "" && fingerprint != "" && submission.RequestFingerprint != fingerprint {
			return nil, ErrIdempotencyKeyConflict
		}
		run, err := GetDurableRun(ctx, sessionDir, submission.RunID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, ErrIdempotencyRunMissing
		}
		return run, nil
	}
	// Named legacy bridge for Runs admitted before runtime_submissions. Remove
	// this event scan after all supported databases have crossed schema 33.
	events, err := session.ListSessionRunEventsContext(ctx, sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != "started" || len(event.Data) == 0 {
			continue
		}
		var data struct {
			// IdempotencyKey is readable only for pre-migration history. New
			// events persist the hash and never the caller's raw key.
			IdempotencyKey     string `json:"idempotencyKey"`
			IdempotencyKeyHash string `json:"idempotencyKeyHash"`
			IdempotencyScope   string `json:"idempotencyScope"`
			Fingerprint        string `json:"requestFingerprint"`
		}
		if json.Unmarshal(event.Data, &data) != nil {
			continue
		}
		if data.IdempotencyKeyHash != "" {
			if data.IdempotencyKeyHash != keyFingerprint {
				continue
			}
		} else if data.IdempotencyKey != key {
			continue
		}
		if data.IdempotencyScope != "" && data.IdempotencyScope != scope {
			return nil, ErrIdempotencyKeyConflict
		}
		if data.IdempotencyScope == "" && scope != "submit" {
			return nil, ErrIdempotencyKeyConflict
		}
		if data.Fingerprint != "" && fingerprint != "" && data.Fingerprint != fingerprint {
			return nil, ErrIdempotencyKeyConflict
		}
		run, err := GetDurableRun(ctx, sessionDir, event.RunID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, ErrIdempotencyRunMissing
		}
		return run, nil
	}
	return nil, nil
}
