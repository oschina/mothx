package serve

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/oschina/mothx/internal/config"
	openaiapi "github.com/oschina/mothx/internal/serve/openaiapi"
)

// handleMCPConfig manages the global MCP configuration shared by all runtimes.
// It intentionally uses the same mcp.json schema and path as the TUI.
func (rt *channelRuntime) handleMCPConfig(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rt.handleMCPConfigAtPath(w, r, config.GlobalMCPPath(), sessions)
	}
}

func (rt *channelRuntime) handleMCPConfigAtPath(w http.ResponseWriter, r *http.Request, path string, sessions activeSessionManager) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := loadMCPConfig(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		var cfg config.MCPConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid MCP config: " + err.Error()})
			return
		}
		config.NormalizeMCPConfig(&cfg)
		if err := config.SaveMCPConfig(path, &cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		// Reconnect the affected sessions in place so the new configuration
		// takes effect without restarting the server or the client.
		reloaded := reloadMCPForSessions(sessions, path)
		if len(reloaded) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"mcpServers":       cfg.MCPServers,
				"reloadedSessions": reloaded,
			})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func loadMCPConfig(path string) (*config.MCPConfig, error) {
	cfg, err := config.LoadMCPConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return &config.MCPConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	config.NormalizeMCPConfig(cfg)
	return cfg, nil
}

func (rt *channelRuntime) handleSessionMCPConfig(w http.ResponseWriter, r *http.Request, sessions activeSessionManager, id string) {
	if sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
		return
	}
	workDir, err := sessions.GetSessionWorkDir(id)
	if err != nil {
		if errors.Is(err, openaiapi.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	rt.handleMCPConfigAtPath(w, r, filepath.Join(workDir, config.ProjectMCPPath()), sessions)
}

// reloadMCPForSessions reconnects the sessions affected by an MCP
// configuration write. A global write affects every active session; a project
// write affects only the sessions whose working directory owns that project
// configuration. The reload is Runtime-owned and never fails the write: a
// server that cannot be reconnected is reported through the session's
// canonical warning surface.
func reloadMCPForSessions(sessions activeSessionManager, path string) []string {
	reloader, ok := sessions.(interface {
		ReloadAllSessionMCP() []string
		ReloadSessionMCP(string) error
	})
	if !ok || reloader == nil {
		return nil
	}
	if path == config.GlobalMCPPath() {
		return reloader.ReloadAllSessionMCP()
	}
	// A project configuration belongs to one working directory. Only the
	// sessions whose work dir matches it are reloaded.
	workDir := filepath.Dir(path)
	var reloaded []string
	for _, item := range sessions.ListActivePoolSessions() {
		if item.WorkDir != workDir {
			continue
		}
		if err := reloader.ReloadSessionMCP(item.ID); err != nil {
			continue
		}
		reloaded = append(reloaded, item.ID)
	}
	return reloaded
}
