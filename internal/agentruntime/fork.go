package agentruntime

import (
	"context"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// ForkOptions is the front-end-neutral request for a Session prefix fork.
// RequestID is mandatory so retries can reconcile the original child.
type ForkOptions = session.ForkOptions

type ForkResult = session.ForkResult

// Fork performs the canonical Session fork operation. The data layer owns the
// SQLite snapshot/copy transaction; this Runtime boundary keeps adapters from
// implementing their own copy or Agent lifecycle.
func Fork(ctx context.Context, sessionDir string, options ForkOptions) (ForkResult, error) {
	return session.ForkSession(ctx, sessionDir, options)
}

// ForkWithExpert is the Runtime-owned expert switch operation. It preserves
// the source session's identity and history while applying expertID only to the
// child branch; an empty expertID deliberately creates an unbound child.
func ForkWithExpert(ctx context.Context, sessionDir string, options ForkOptions, expertID string) (ForkResult, error) {
	expertID = strings.TrimSpace(expertID)
	options.ExpertID = &expertID
	return Fork(ctx, sessionDir, options)
}
