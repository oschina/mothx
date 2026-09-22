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
func (rt *channelRuntime) handleMCPConfig(w http.ResponseWriter, r *http.Request) {
	rt.handleMCPConfigAtPath(w, r, config.GlobalMCPPath())
}

func (rt *channelRuntime) handleMCPConfigAtPath(w http.ResponseWriter, r *http.Request, path string) {
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
	workDir, err := sessionWorkDir(sessions.ListActiveSessions(), id)
	if err != nil {
		if errors.Is(err, openaiapi.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	rt.handleMCPConfigAtPath(w, r, filepath.Join(workDir, config.ProjectMCPPath()))
}

func sessionWorkDir(items []openaiapi.ActiveSessionInfo, id string) (string, error) {
	var workDir string
	for _, item := range items {
		if item.ID != id {
			continue
		}
		if workDir != "" && workDir != item.WorkDir {
			return "", openaiapi.ErrActiveSessionIDAmbiguous
		}
		workDir = item.WorkDir
	}
	if workDir == "" {
		return "", openaiapi.ErrSessionNotFound
	}
	return workDir, nil
}
