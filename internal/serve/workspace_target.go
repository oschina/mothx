package serve

// Shared target resolution for every read-only workspace projection
// (git changes, files listing/preview). One owner for the "which directory is
// this panel about?" decision: session tabs resolve through the canonical
// activeSessionManager (the same GetSessionWorkDir contract as the terminal
// API), and draft tabs without a session row may supply an explicit path.
// Adapters must not grow per-panel fallbacks; new projections call these
// functions only. This file is a migration bridge: when ACP/Desktop gain the
// equivalent mothx/files/* projections, the resolver moves into
// internal/agentruntime alongside the Runtime-owned session identity and the
// allowlist entry here is removed.

import (
	"net/http"
	"path/filepath"
	"strings"
)

func sessionWorkDirTarget(sessions activeSessionManager, r *http.Request) (string, string) {
	q := r.URL.Query()
	return sessionWorkDirTargetFrom(q.Get("sessionId"), q.Get("path"), sessions)
}

func sessionWorkDirTargetFrom(rawSessionID, rawPath string, sessions activeSessionManager) (string, string) {
	if id := strings.TrimSpace(rawSessionID); id != "" {
		if sessions == nil {
			return "", "session not found"
		}
		if cwd, err := sessions.GetSessionWorkDir(id); err == nil && cwd != "" {
			return cwd, ""
		}
		for _, info := range sessions.ListActiveSessions() {
			if info.ID == id && info.WorkDir != "" {
				return info.WorkDir, ""
			}
		}
		return "", "session not found"
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", "path or sessionId is required"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err.Error()
	}
	return abs, ""
}
