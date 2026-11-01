package serve

// Thin WebUI/API projections of the shared workspace services:
// internal/workspacefiles (read-only listing/preview) and
// internal/workspaceopen (allowlisted external launch). This file owns no
// policy of its own: the root comes from the canonical session source, path
// containment is workspacefiles', launcher availability is workspaceopen',
// and the transport gate is exactly the terminal one (EffectiveTerminalEnabled),
// because launching local programs carries the same trust class as the PTY
// dock. ACP can later project the same two packages through mothx/* methods.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/oschina/mothx/internal/workspacefiles"
	"github.com/oschina/mothx/internal/workspaceopen"
)

func filesWriteError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func (rt *channelRuntime) handleFilesList(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		root, problem := sessionWorkDirTarget(sessions, r)
		if root == "" {
			filesWriteError(w, http.StatusBadRequest, "invalid_target", problem)
			return
		}
		result, err := workspacefiles.List(root, r.URL.Query().Get("dir"))
		if err != nil {
			if errors.Is(err, workspacefiles.ErrInvalidPath) {
				filesWriteError(w, http.StatusForbidden, "invalid_path", err.Error())
			} else {
				filesWriteError(w, http.StatusBadRequest, "list_failed", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"root":      root,
			"dir":       result.Dir,
			"entries":   result.Entries,
			"truncated": result.Truncated,
		})
	}
}

func (rt *channelRuntime) handleFilesContent(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		root, problem := sessionWorkDirTarget(sessions, r)
		if root == "" {
			filesWriteError(w, http.StatusBadRequest, "invalid_target", problem)
			return
		}
		result, err := workspacefiles.Read(root, r.URL.Query().Get("file"))
		if err != nil {
			switch {
			case errors.Is(err, workspacefiles.ErrInvalidPath):
				filesWriteError(w, http.StatusForbidden, "invalid_path", err.Error())
			case errors.Is(err, workspacefiles.ErrNotFound):
				filesWriteError(w, http.StatusNotFound, "file_not_found", err.Error())
			default:
				filesWriteError(w, http.StatusBadRequest, "read_failed", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// openersGate mirrors the terminal gate: external launches require the same
// listener/auth conditions and return the same codes when disabled. Tests may
// force-enable the gate with testOpenersEnabled.
func (rt *channelRuntime) openersGate(w http.ResponseWriter) bool {
	if rt.testOpenersEnabled {
		return true
	}
	enabled, reason := EffectiveTerminalEnabled(rt.configSnapshot())
	if enabled {
		return true
	}
	status, code := terminalUnavailableStatus(reason)
	filesWriteError(w, status, code, code)
	return false
}

func (rt *channelRuntime) handleFilesOpeners() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !rt.openersGate(w) {
			return
		}
		writeJSON(w, http.StatusOK, workspaceopen.ProbeOpeners())
	}
}

// serveOpenRootResolver adapts the canonical activeSessionManager into the
// shared workspaceopen.RootResolver contract. Draft tabs without a session
// are handled by the path query in the HTTP layer, not here.
type serveOpenRootResolver struct{ sessions activeSessionManager }

func (r serveOpenRootResolver) ResolveRoot(_ context.Context, sessionID string) (string, error) {
	if r.sessions == nil || sessionID == "" {
		return "", workspaceopen.ErrRootUnavailable
	}
	cwd, err := r.sessions.GetSessionWorkDir(sessionID)
	if err != nil || cwd == "" {
		return "", workspaceopen.ErrRootUnavailable
	}
	return cwd, nil
}

// serveOpenerPolicy implements workspaceopen.OpenerPolicy through the exact
// terminal enablement rule, so launching local programs can never outrun the
// PTY dock's transport gate.
type serveOpenerPolicy struct{ rt *channelRuntime }

func (p serveOpenerPolicy) TerminalEnabled() bool {
	if p.rt == nil {
		return false
	}
	if p.rt.testOpenersEnabled {
		return true
	}
	enabled, _ := EffectiveTerminalEnabled(p.rt.configSnapshot())
	return enabled
}

// draftOpenSessionSentinel keeps the shared Open flow uniform when a draft
// tab supplies its chosen directory instead of a session ID.
const draftOpenSessionSentinel = "__draft__"

// staticWorkDirSessions is the minimal activeSessionManager for one draft
// root. It exists only so the adapter can feed the canonical RootResolver
// contract without inventing a second resolution path.
type staticWorkDirSessions struct {
	activeSessionManager
	dir string
}

func (s *staticWorkDirSessions) GetSessionWorkDir(string) (string, error) {
	if s.dir == "" {
		return "", os.ErrNotExist
	}
	return s.dir, nil
}

func (rt *channelRuntime) handleFilesOpen(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !rt.openersGate(w) {
			return
		}
		var body struct {
			SessionID string `json:"sessionId"`
			Path      string `json:"path"`
			Target    string `json:"target"`
			Action    string `json:"action"`
			Editor    string `json:"editor"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&body); err != nil {
			filesWriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
			return
		}
		sessionID := strings.TrimSpace(body.SessionID)
		rootOverride := strings.TrimSpace(body.Path)
		// Draft tabs have no session row yet; their chosen directory is the
		// canonical root for this projection. Session tabs ALWAYS resolve
		// through the shared RootResolver (never a client-supplied path).
		resolver := serveOpenRootResolver{sessions}
		if sessionID == "" && rootOverride != "" {
			resolver = serveOpenRootResolver{sessions: &staticWorkDirSessions{dir: rootOverride}}
			sessionID = draftOpenSessionSentinel
		}
		svc := workspaceopen.New(resolver, serveOpenerPolicy{rt})
		resp, launchErr := svc.Open(r.Context(), workspaceopen.Request{
			SessionID: sessionID,
			Action:    workspaceopen.Action(strings.TrimSpace(body.Action)),
			Target:    body.Target,
			Editor:    workspaceopen.Editor(strings.TrimSpace(body.Editor)),
		})
		if launchErr != nil {
			switch {
			case errors.Is(launchErr, workspaceopen.ErrUnavailable):
				filesWriteError(w, http.StatusNotImplemented, "launcher_unavailable", "launcher unavailable")
			case errors.Is(launchErr, workspaceopen.ErrRootUnavailable):
				filesWriteError(w, http.StatusNotFound, "target_not_found", launchErr.Error())
			case errors.Is(launchErr, workspacefiles.ErrInvalidPath):
				filesWriteError(w, http.StatusForbidden, "invalid_path", launchErr.Error())
			default:
				filesWriteError(w, http.StatusBadGateway, "launch_failed", launchErr.Error())
			}
			return
		}
		rt.publishManagementEvent("workspace_open", map[string]any{
			"action": resp.Action, "editor": body.Editor,
		})
		writeJSON(w, http.StatusOK, map[string]any{"opened": resp.Opened, "action": resp.Action})
	}
}
