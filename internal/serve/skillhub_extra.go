package serve

import (
	"errors"
	"net/http"
	"strings"

	"github.com/oschina/mothx/internal/serve/openaiapi"
	"github.com/oschina/mothx/internal/skillhub"
)

func (rt *channelRuntime) handleSkillHubUninstall(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	var request skillHubUninstallRequest
	if err := decodeSkillHubJSON(r, &request); err != nil {
		writeSkillHubError(w, err)
		return
	}
	if request.Market == "" || strings.TrimSpace(request.ID) == "" {
		writeSkillHubError(w, errors.New("market and skill id are required"))
		return
	}
	service, _, err := skillHubServiceForRequest(server, request.SessionID, request.WorkDir)
	if err == nil {
		err = service.Uninstall(request.Market, request.ID, request.Scope)
	}
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	state, err := server.RefreshSkillHubSession(request.SessionID, request.WorkDir, "")
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uninstalled": true, "session": state})
}

func (rt *channelRuntime) handleSkillHubSkillSet(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	var request skillHubSkillSetRequest
	if err := decodeSkillHubJSON(r, &request); err != nil {
		writeSkillHubError(w, err)
		return
	}
	if len(request.Skills) == 0 {
		writeSkillHubError(w, errors.New("skillset must contain skills"))
		return
	}
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.TargetDir) == "" {
		writeSkillHubError(w, errors.New("sessionId and targetDir are required for installation"))
		return
	}
	service, runtime, err := skillHubServiceForRequest(server, request.SessionID, request.WorkDir)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	installs := make([]skillhub.InstallRequest, 0, len(request.Skills))
	for _, item := range request.Skills {
		market, marketErr := skillHubMarket(string(item.Market), runtime.DefaultMarket)
		if marketErr != nil {
			writeSkillHubError(w, marketErr)
			return
		}
		scope := item.Scope
		if scope == "" {
			scope = request.Scope
		}
		if scope == "" {
			scope = runtime.DefaultScope
		}
		installs = append(installs, skillhub.InstallRequest{Market: market, ID: item.ID, Version: item.Version, Scope: scope, TargetDir: request.TargetDir, Overwrite: item.Overwrite})
	}
	results, err := service.InstallSkillSet(r.Context(), installs)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	activeNames := make([]string, 0, len(results))
	if request.Activate {
		for _, result := range results {
			activeNames = append(activeNames, activationName(true, result.Name))
		}
	}
	var state *openaiapi.SkillHubSessionState
	if request.Activate {
		state, err = server.RefreshSkillHubSessionMany(request.SessionID, request.WorkDir, activeNames)
	} else {
		state, err = server.RefreshSkillHubSession(request.SessionID, request.WorkDir, "")
	}
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installs": results, "session": state})
}

func (rt *channelRuntime) handleSkillHubShowcase(w http.ResponseWriter, r *http.Request, server *openaiapi.Server, kind string) {
	service, runtime, err := skillHubServiceForRequest(server, r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	market, err := skillHubMarket(r.URL.Query().Get("market"), runtime.DefaultMarket)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	result, err := service.Showcase(r.Context(), market, kind, skillhub.SearchQuery{Limit: 20})
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (rt *channelRuntime) handleSkillHubContent(w http.ResponseWriter, r *http.Request, server *openaiapi.Server, path string) {
	market, id, err := parseSkillHubPath(path)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeSkillHubError(w, errors.New("path is required"))
		return
	}
	service, _, err := skillHubServiceForRequest(server, r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	content, err := service.FileContent(r.Context(), market, id, r.URL.Query().Get("version"), filePath)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (rt *channelRuntime) handleSkillHubSetActive(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	var request skillHubActiveSkillsRequest
	if err := decodeSkillHubJSON(r, &request); err != nil {
		writeSkillHubError(w, err)
		return
	}
	state, err := server.SetActiveSkillsForSession(request.SessionID, request.WorkDir, request.Names)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": state})
}
