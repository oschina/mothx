package acp

import (
	"time"

	"github.com/oschina/mothx/internal/session"
)

// databaseIntegrityMethod is the additive notification that carries the outcome
// of a startup integrity check which finished after the Runtime had already
// started. The startup check of a large session database runs on in the
// background (see database.StartupCheckMode), so a desktop shell cannot learn
// its verdict from the handshake: a Runtime that starts on a damaged database
// reports it here instead of staying silent.
//
// It is advertised by the "databaseIntegrity" feature key. The verdict and the
// repair belong to the Runtime and have already been settled when this is sent:
// a stale index that could be rebuilt was rebuilt, and damage is never touched.
// The client only tells the user.
const databaseIntegrityMethod = "mothx/database/integrity"

// databaseIntegrityNotification is the payload of mothx/database/integrity. It
// is a projection of session.DatabaseIntegrityFinding and carries no state the
// client is expected to keep.
type databaseIntegrityNotification struct {
	// Kind is what the check concluded: "index_repaired" (a stale secondary
	// index was rebuilt, no row changed), "index_unrepaired" (a stale index
	// stays stale until a later start rebuilds it), "corrupt" (structural
	// damage; the database was left untouched), or "check_failed" (the check
	// itself could not run, which is not a corruption verdict).
	Kind string `json:"kind"`
	// Path is the canonical database file that was checked.
	Path string `json:"path"`
	// Detail is the one-line operator-facing summary.
	Detail string `json:"detail"`
	// Report is the quick_check output behind the finding. It is empty when the
	// check itself failed.
	Report string `json:"report,omitempty"`
	// At is when the check finished, as RFC 3339 in UTC.
	At string `json:"at,omitempty"`
}

// databaseIntegrityNotificationFor projects one finding onto the wire.
func databaseIntegrityNotificationFor(finding session.DatabaseIntegrityFinding) databaseIntegrityNotification {
	notification := databaseIntegrityNotification{
		Kind:   string(finding.Kind),
		Path:   finding.Path,
		Detail: finding.Describe(),
		Report: finding.Report,
	}
	if !finding.At.IsZero() {
		notification.At = finding.At.UTC().Format(time.RFC3339Nano)
	}
	return notification
}

// notifyDatabaseIntegrity projects a startup integrity finding as the additive
// mothx/database/integrity notification. It runs on the check's goroutine; like
// the worktree status notification it only writes a message, so it is safe to
// call from there.
func (s *server) notifyDatabaseIntegrity(finding session.DatabaseIntegrityFinding) {
	if s == nil || finding.Kind == "" {
		return
	}
	_ = s.notifyExtension(databaseIntegrityMethod, databaseIntegrityNotificationFor(finding))
}
