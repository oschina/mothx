package agentruntime

import (
	"fmt"

	agentcore "github.com/oschina/mothx/internal/agent"
	sessionpkg "github.com/oschina/mothx/internal/session"
)

// localChildRunRecorder adapts Agent Core's lifecycle observation to the
// session/DAO-owned child history record. It is installed only by the shared
// Runtime; Agent Core never imports the database or session package for it.
type localChildRunRecorder struct {
	runtime *SessionRuntime
}

func (r localChildRunRecorder) RecordChildRun(observation agentcore.ChildRunObservation) error {
	if r.runtime == nil || observation.SessionID == "" || observation.ChildID == "" {
		return nil
	}
	r.runtime.mu.RLock()
	manager := r.runtime.Manager
	closed := r.runtime.closed
	r.runtime.mu.RUnlock()
	if closed || manager == nil {
		return nil
	}
	sessionDir := manager.GetSessionDir()
	if sessionDir == "" {
		return fmt.Errorf("session directory is not available for child history")
	}
	return sessionpkg.SaveChildRun(sessionDir, sessionpkg.ChildRun{
		ChildID: observation.ChildID, SessionID: observation.SessionID,
		ParentRunID: observation.ParentRunID, Backend: observation.Backend,
		ProfileID: observation.ProfileID, RemoteSessionID: observation.RemoteSessionID,
		WorkDir: observation.WorkDir, WorktreeID: observation.WorktreeID,
		BaseRevision: observation.BaseRevision, ChangeSetJSON: observation.ChangeSetJSON,
		ChangeSetError: observation.ChangeSetError, Status: observation.Status,
		ContractJSON: observation.ContractJSON, TranscriptJSON: observation.TranscriptJSON,
		CreatedAt: observation.CreatedAt, UpdatedAt: observation.UpdatedAt,
	})
}
