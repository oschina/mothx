package agentruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/startvibecoding/mothx/internal/session"
)

// CreateSessionOptions describes persisted session identity without coupling it
// to a front-end protocol. Channel binding remains adapter policy input.
type CreateSessionOptions struct {
	WorkDir     string
	SessionDir  string
	ID          string
	ChannelType string
	ChannelID   string
}

// CreateSession initializes a persisted local or bound channel session.
func CreateSession(opts CreateSessionOptions) (*session.Manager, error) {
	if strings.TrimSpace(opts.WorkDir) == "" {
		return nil, fmt.Errorf("session work directory is required")
	}
	mgr := session.New(opts.WorkDir, opts.SessionDir)
	channelType := strings.TrimSpace(opts.ChannelType)
	if channelType != "" && channelType != "local" {
		if err := mgr.InitWithIDAndBinding(opts.ID, channelType, opts.ChannelID); err != nil {
			return nil, err
		}
		return mgr, nil
	}
	if err := mgr.InitWithID(opts.ID); err != nil {
		return nil, err
	}
	return mgr, nil
}

// OpenSession opens a persisted session by exact ID regardless of its workdir.
func OpenSession(sessionDir, id string) (*session.Manager, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	return session.OpenByIDExact(sessionDir, id)
}

// DeleteSession removes a persisted session by ID when it is not active.
func DeleteSession(sessionDir, id string) error {
	mgr, err := OpenSession(sessionDir, id)
	if err != nil {
		return err
	}
	guard, err := AcquireSessionMutation(context.Background(), sessionDir, id, ExecutionAdmissionOptions{})
	if err != nil {
		return fmt.Errorf("acquire deletion lease for session %s: %w", id, err)
	}
	defer guard.Release()
	return session.DeleteSessionWithMutation(mgr.GetFile(), sessionDir, guard)
}

// DeleteSessionWithMutation removes a session while the caller already holds
// its shared mutation lease. It is the multi-session counterpart of
// DeleteSession and keeps cascade deletion on the same Runtime-owned lifecycle
// without reacquiring a process-local lock.
func DeleteSessionWithMutation(sessionDir, id string, guard *session.RuntimeLeaseGuard) error {
	mgr, err := OpenSession(sessionDir, id)
	if err != nil {
		return err
	}
	return session.DeleteSessionWithMutation(mgr.GetFile(), sessionDir, guard)
}

// OpenSessionForWorkDir opens a persisted session scoped to its working directory.
func OpenSessionForWorkDir(workDir, sessionDir, id string) (*session.Manager, error) {
	if strings.TrimSpace(workDir) == "" || strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("session work directory and ID are required")
	}
	return session.OpenByID(workDir, sessionDir, id)
}
