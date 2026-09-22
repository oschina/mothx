package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/oschina/mothx/internal/serve/openaiapi"
	"github.com/oschina/mothx/internal/skillhub"
	"github.com/oschina/mothx/internal/skills"
)

type skillHubInstallRequest struct {
	Market    skillhub.Market `json:"market"`
	ID        string          `json:"id"`
	Version   string          `json:"version,omitempty"`
	Scope     string          `json:"scope,omitempty"`
	TargetDir string          `json:"targetDir,omitempty"`
	WorkDir   string          `json:"workDir,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Overwrite bool            `json:"overwrite,omitempty"`
	Activate  bool            `json:"activate,omitempty"`
}

type skillHubTarget struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Label string `json:"label"`
}

type skillHubSkillSetRequest struct {
	Skills    []skillHubInstallRequest `json:"skills"`
	Scope     string                   `json:"scope,omitempty"`
	TargetDir string                   `json:"targetDir,omitempty"`
	WorkDir   string                   `json:"workDir,omitempty"`
	SessionID string                   `json:"sessionId,omitempty"`
	Activate  bool                     `json:"activate,omitempty"`
}

type skillHubUninstallRequest struct {
	Market    skillhub.Market `json:"market"`
	ID        string          `json:"id"`
	Scope     string          `json:"scope,omitempty"`
	WorkDir   string          `json:"workDir,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}
type skillHubActivateRequest struct {
	Name      string `json:"name"`
	WorkDir   string `json:"workDir,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type skillHubActiveSkillsRequest struct {
	Names     []string `json:"names"`
	WorkDir   string   `json:"workDir,omitempty"`
	SessionID string   `json:"sessionId,omitempty"`
}

func (rt *channelRuntime) handleSkillHub(server *openaiapi.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if server == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
			return
		}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/skillhub/"), "/")
		switch {
		case path == "markets" && r.Method == http.MethodGet:
			service, _, err := skillHubServiceForRequest(server, r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
			if err != nil {
				writeSkillHubError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"markets": service.Markets()})
		case path == "categories" && r.Method == http.MethodGet:
			rt.handleSkillHubCategories(w, r, server)
		case path == "official" && r.Method == http.MethodGet:
			rt.handleSkillHubOfficial(w, r, server)
		case path == "search" && r.Method == http.MethodGet:
			rt.handleSkillHubSearch(w, r, server)
		case strings.HasPrefix(path, "skills/") && r.Method == http.MethodGet:
			rt.handleSkillHubDetail(w, r, server, strings.TrimPrefix(path, "skills/"))
		case path == "targets" && r.Method == http.MethodGet:
			rt.handleSkillHubTargets(w, r, server)
		case path == "installed" && r.Method == http.MethodGet:
			rt.handleSkillHubInstalled(w, r, server)
		case path == "install" && r.Method == http.MethodPost:
			rt.handleSkillHubInstall(w, r, server)
		case path == "activate" && r.Method == http.MethodPost:
			rt.handleSkillHubActivate(w, r, server)
		case path == "set-active" && r.Method == http.MethodPost:
			rt.handleSkillHubSetActive(w, r, server)
		case path == "skillset" && r.Method == http.MethodPost:
			rt.handleSkillHubSkillSet(w, r, server)
		case path == "uninstall" && r.Method == http.MethodPost:
			rt.handleSkillHubUninstall(w, r, server)
		case strings.HasPrefix(path, "showcase/") && r.Method == http.MethodGet:
			rt.handleSkillHubShowcase(w, r, server, strings.TrimPrefix(path, "showcase/"))
		case strings.HasPrefix(path, "content/") && r.Method == http.MethodGet:
			rt.handleSkillHubContent(w, r, server, strings.TrimPrefix(path, "content/"))
		case path == "targets" || path == "markets" || path == "categories" || path == "official" || path == "search" || path == "installed" || path == "install" || path == "activate" || path == "set-active" || path == "skillset" || path == "uninstall" || strings.HasPrefix(path, "skills/") || strings.HasPrefix(path, "showcase/") || strings.HasPrefix(path, "content/"):
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "SkillHub endpoint not found"})
		}
	}
}

func (rt *channelRuntime) handleSkillHubCategories(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
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
	categories, err := service.Categories(r.Context(), market)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func (rt *channelRuntime) handleSkillHubOfficial(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	service, _, err := skillHubServiceForRequest(server, r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	market, err := skillHubMarket(r.URL.Query().Get("market"), string(skillhub.MarketSkillHub))
	if err != nil || market != skillhub.MarketSkillHub {
		writeSkillHubError(w, errors.New("official recommendations are available on SkillHub.cn only"))
		return
	}
	limit, err := skillHubQueryInt(r.URL.Query(), "limit", 20)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	page, err := skillHubQueryInt(r.URL.Query(), "page", 1)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	result, err := service.Official(r.Context(), skillhub.UserSkillsQuery{Query: r.URL.Query().Get("q"), Limit: limit, Page: page})
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (rt *channelRuntime) handleSkillHubSearch(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
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
	limit, err := skillHubQueryInt(r.URL.Query(), "limit", 20)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	page, err := skillHubQueryInt(r.URL.Query(), "page", 1)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	result, err := service.Search(r.Context(), market, skillhub.SearchQuery{
		Query: r.URL.Query().Get("q"), Limit: limit, Page: page, Cursor: r.URL.Query().Get("cursor"),
		Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order"), Category: r.URL.Query().Get("category"),
	})
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (rt *channelRuntime) handleSkillHubDetail(w http.ResponseWriter, r *http.Request, server *openaiapi.Server, path string) {
	filesOnly := strings.HasSuffix(path, "/files")
	if filesOnly {
		path = strings.TrimSuffix(path, "/files")
	}
	market, id, err := parseSkillHubPath(path)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	service, _, err := skillHubServiceForRequest(server, r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	detail, err := service.Detail(r.Context(), market, id)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	if filesOnly {
		writeJSON(w, http.StatusOK, map[string]any{"files": detail.Files})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (rt *channelRuntime) handleSkillHubTargets(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	if sessionID == "" {
		writeSkillHubError(w, errors.New("sessionId is required"))
		return
	}
	workDir, err := server.ResolveSkillHubWorkDir(sessionID, r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	runtime := server.SkillHubRuntime()
	labels := []string{"MothX project skills", "Project skills", "Agents skills", "Generic project skills"}
	targets := make([]skillHubTarget, 0, len(skills.ProjectSkillDirs(workDir))+1)
	for i, dir := range skills.ProjectSkillDirs(workDir) {
		label := "Project skills"
		if i < len(labels) {
			label = labels[i]
		}
		targets = append(targets, skillHubTarget{Path: dir, Scope: "project", Label: label})
	}
	if runtime.GlobalSkillsDir != "" {
		targets = append(targets, skillHubTarget{Path: runtime.GlobalSkillsDir, Scope: "global", Label: "Global skills"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "workDir": workDir, "targets": targets})
}

func (rt *channelRuntime) handleSkillHubInstalled(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	runtime := server.SkillHubRuntime()
	workDir, err := server.ResolveSkillHubWorkDir(r.URL.Query().Get("sessionId"), r.URL.Query().Get("workDir"))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	index, err := skillhub.NewLocalIndex(runtime.GlobalSkillsDir, skills.ProjectSkillDirs(workDir))
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	response := map[string]any{"installed": index.List(), "workDir": workDir}
	state, err := server.InspectSkillHubSession(r.URL.Query().Get("sessionId"), workDir)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	response["session"] = state
	writeJSON(w, http.StatusOK, response)
}

func (rt *channelRuntime) handleSkillHubInstall(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	var request skillHubInstallRequest
	if err := decodeSkillHubJSON(r, &request); err != nil {
		writeSkillHubError(w, err)
		return
	}
	runtime := server.SkillHubRuntime()
	market, err := skillHubMarket(string(request.Market), runtime.DefaultMarket)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	if strings.TrimSpace(request.SessionID) == "" {
		writeSkillHubError(w, errors.New("sessionId is required for installation"))
		return
	}
	if strings.TrimSpace(request.ID) == "" {
		writeSkillHubError(w, errors.New("skill id is required"))
		return
	}
	if strings.TrimSpace(request.TargetDir) == "" {
		writeSkillHubError(w, errors.New("targetDir is required for installation"))
		return
	}
	scope := request.Scope
	if scope == "" {
		scope = runtime.DefaultScope
	}
	if scope != "project" && scope != "global" {
		writeSkillHubError(w, errors.New("scope must be project or global"))
		return
	}
	service, _, err := skillHubServiceForRequest(server, request.SessionID, request.WorkDir)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	result, err := service.Install(r.Context(), skillhub.InstallRequest{Market: market, ID: request.ID, Version: request.Version, Scope: scope, TargetDir: request.TargetDir, Overwrite: request.Overwrite})
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	state, err := server.RefreshSkillHubSession(request.SessionID, request.WorkDir, activationName(request.Activate, result.Name))
	if err != nil {
		writeSkillHubError(w, fmt.Errorf("installed, but failed to refresh session: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"install": result, "activated": request.Activate, "session": state})
}

func (rt *channelRuntime) handleSkillHubActivate(w http.ResponseWriter, r *http.Request, server *openaiapi.Server) {
	var request skillHubActivateRequest
	if err := decodeSkillHubJSON(r, &request); err != nil {
		writeSkillHubError(w, err)
		return
	}
	if strings.TrimSpace(request.Name) == "" {
		writeSkillHubError(w, errors.New("skill name is required"))
		return
	}
	state, err := server.RefreshSkillHubSession(request.SessionID, request.WorkDir, request.Name)
	if err != nil {
		writeSkillHubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activated": true, "session": state})
}

func skillHubServiceForRequest(server *openaiapi.Server, sessionID, requestedWorkDir string) (*skillhub.Service, openaiapi.SkillHubRuntime, error) {
	runtime := server.SkillHubRuntime()
	workDir, err := server.ResolveSkillHubWorkDir(sessionID, requestedWorkDir)
	if err != nil {
		return nil, runtime, err
	}
	return skillhub.NewServiceForWorkDir(runtime.GlobalSkillsDir, workDir, runtime.OfficialHandles, skillhub.ClientsForSettings(server.SettingsSkillHub())...), runtime, nil
}

func parseSkillHubPath(path string) (skillhub.Market, string, error) {
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", errors.New("market and skill id are required")
	}
	market, err := skillHubMarket(parts[0], "")
	if err != nil {
		return "", "", err
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil || strings.TrimSpace(id) == "" {
		return "", "", errors.New("invalid skill id")
	}
	return market, id, nil
}

func skillHubMarket(value, fallback string) (skillhub.Market, error) {
	if value == "" {
		value = fallback
	}
	market := skillhub.Market(value)
	if market != skillhub.MarketSkillHub && market != skillhub.MarketClawHub {
		return "", fmt.Errorf("unsupported marketplace %q", value)
	}
	return market, nil
}

func skillHubQueryInt(values url.Values, key string, fallback int) (int, error) {
	value := values.Get(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func decodeSkillHubJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeSkillHubError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	message := err.Error()
	if strings.Contains(message, "not in allowedWorkDirs") || strings.Contains(message, "overrides are disabled") {
		status = http.StatusForbidden
	} else if strings.Contains(message, "not found") {
		status = http.StatusNotFound
	} else if strings.Contains(message, "failed to refresh session") {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func activationName(activate bool, name string) string {
	if activate {
		return filepath.Base(name)
	}
	return ""
}
