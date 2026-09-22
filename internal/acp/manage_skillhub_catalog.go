package acp

// The SkillHub catalogue is intentionally an ACP projection of the shared
// skillhub.Service.  Desktop never discovers a market, writes a skill, or
// constructs a client itself; it can only render these capability-gated RPC
// responses and submit the user's explicit installation choices.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/skillhub"
	"github.com/oschina/mothx/internal/skills"
)

type manageSkillHubCatalogRequest struct {
	SessionID string `json:"sessionId,omitempty"`
	Market    string `json:"market,omitempty"`
	ID        string `json:"id,omitempty"`
	Version   string `json:"version,omitempty"`
	Scope     string `json:"scope,omitempty"`
	TargetDir string `json:"targetDir,omitempty"`
	Query     string `json:"query,omitempty"`
	Category  string `json:"category,omitempty"`
	Sort      string `json:"sort,omitempty"`
	Order     string `json:"order,omitempty"`
	Page      int    `json:"page,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
	Activate  bool   `json:"activate,omitempty"`
}

type manageSkillHubTarget struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Label string `json:"label"`
}

// manageSkillHubCatalog parses the common request envelope and resolves its
// session to the Runtime-owned working directory.  A catalogue request must
// name an open ACP session so a Desktop UI can never choose an arbitrary path
// for discovery or installation.
func (s *server) manageSkillHubCatalog(req rpcRequest) (manageSkillHubCatalogRequest, *sessionRuntime, *skillhub.Service, error) {
	var in manageSkillHubCatalogRequest
	if len(req.Params) > 0 && string(req.Params) != "null" {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			return in, nil, nil, fmt.Errorf("invalid params")
		}
	}
	in.SessionID = strings.TrimSpace(in.SessionID)
	if in.SessionID == "" {
		return in, nil, nil, fmt.Errorf("sessionId is required")
	}
	rt := s.sessionRuntime(in.SessionID)
	if rt == nil || rt.runtime == nil {
		return in, nil, nil, fmt.Errorf("unknown or inactive session")
	}
	settings, err := s.manageSettings()
	if err != nil {
		return in, nil, nil, fmt.Errorf("load settings: %w", err)
	}
	officialHandles := settings.SkillHub.OfficialHandles
	if len(officialHandles) == 0 {
		officialHandles = []string{config.DefaultSkillHubOfficialHandle}
	}
	service := skillhub.NewServiceForWorkDir(settings.GetGlobalSkillsDir(), rt.runtime.WorkDir,
		officialHandles, skillhub.ClientsForSettings(settings.SkillHub)...)
	return in, rt, service, nil
}

func manageSkillHubMarket(value, fallback string) (skillhub.Market, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	market := skillhub.Market(value)
	if market != skillhub.MarketSkillHub && market != skillhub.MarketClawHub {
		return "", fmt.Errorf("unsupported skill market %q", value)
	}
	return market, nil
}

func manageSkillHubLimit(value int) int {
	if value <= 0 {
		return 20
	}
	if value > 100 {
		return 100
	}
	return value
}

func (s *server) writeManageSkillHubCatalogError(req rpcRequest, err error) {
	message := "SkillHub request failed"
	code := "skillhub_unavailable"
	if err != nil {
		message = err.Error()
		if strings.Contains(message, "sessionId is required") || strings.Contains(message, "unknown or inactive session") || strings.Contains(message, "invalid params") || strings.Contains(message, "unsupported skill market") || strings.Contains(message, "must be") || strings.Contains(message, "required") {
			code = "skillhub_invalid_request"
		}
	}
	if settings, settingsErr := s.manageSettings(); settingsErr == nil {
		message = manageRedactSecrets(message, settings)
	}
	s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, code, message, nil))
}

func (s *server) handleManageSkillHubMarkets(req rpcRequest) {
	_, _, service, err := s.manageSkillHubCatalog(req)
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	result := map[string]any{"markets": service.Markets()}
	// Project the canonical default market so clients select SkillHub.cn (or the
	// configured default) instead of guessing from the alphabetical market order.
	if settings, settingsErr := s.manageSettings(); settingsErr == nil {
		result["defaultMarket"] = manageSkillHubDefaultMarket(settings)
	}
	s.writeResponse(req.ID, result, nil)
}

func (s *server) handleManageSkillHubCategories(req rpcRequest) {
	in, _, service, err := s.manageSkillHubCatalog(req)
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	settings, _ := s.manageSettings()
	market, err := manageSkillHubMarket(in.Market, manageSkillHubDefaultMarket(settings))
	if err == nil {
		var categories []skillhub.Category
		categories, err = service.Categories(context.Background(), market)
		if err == nil {
			s.writeResponse(req.ID, map[string]any{"categories": categories}, nil)
			return
		}
	}
	s.writeManageSkillHubCatalogError(req, err)
}

func (s *server) handleManageSkillHubOfficial(req rpcRequest) {
	in, _, service, err := s.manageSkillHubCatalog(req)
	if err == nil {
		if market, marketErr := manageSkillHubMarket(in.Market, string(skillhub.MarketSkillHub)); marketErr != nil || market != skillhub.MarketSkillHub {
			err = fmt.Errorf("official recommendations are available on SkillHub.cn only")
		}
	}
	if err == nil {
		result, queryErr := service.Official(context.Background(), skillhub.UserSkillsQuery{Query: in.Query, Limit: manageSkillHubLimit(in.Limit), Page: in.Page})
		if queryErr == nil {
			s.writeResponse(req.ID, result, nil)
			return
		}
		err = queryErr
	}
	s.writeManageSkillHubCatalogError(req, err)
}

func (s *server) handleManageSkillHubSearch(req rpcRequest) {
	in, _, service, err := s.manageSkillHubCatalog(req)
	if err == nil {
		settings, _ := s.manageSettings()
		market, marketErr := manageSkillHubMarket(in.Market, manageSkillHubDefaultMarket(settings))
		if marketErr != nil {
			err = marketErr
		} else {
			result, searchErr := service.Search(context.Background(), market, skillhub.SearchQuery{Query: in.Query, Limit: manageSkillHubLimit(in.Limit), Page: in.Page, Cursor: in.Cursor, Sort: in.Sort, Order: in.Order, Category: in.Category})
			if searchErr == nil {
				s.writeResponse(req.ID, result, nil)
				return
			}
			err = searchErr
		}
	}
	s.writeManageSkillHubCatalogError(req, err)
}

func (s *server) handleManageSkillHubDetail(req rpcRequest) {
	in, _, service, err := s.manageSkillHubCatalog(req)
	if err == nil && strings.TrimSpace(in.ID) == "" {
		err = fmt.Errorf("skill id is required")
	}
	if err == nil {
		settings, _ := s.manageSettings()
		market, marketErr := manageSkillHubMarket(in.Market, manageSkillHubDefaultMarket(settings))
		if marketErr != nil {
			err = marketErr
		} else {
			detail, detailErr := service.Detail(context.Background(), market, strings.TrimSpace(in.ID))
			if detailErr == nil {
				s.writeResponse(req.ID, detail, nil)
				return
			}
			err = detailErr
		}
	}
	s.writeManageSkillHubCatalogError(req, err)
}

func (s *server) handleManageSkillHubTargets(req rpcRequest) {
	in, rt, _, err := s.manageSkillHubCatalog(req)
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	settings, err := s.manageSettings()
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	labels := []string{"MothX project skills", "Project skills", "Agents skills", "Generic project skills"}
	targets := make([]manageSkillHubTarget, 0, len(skills.ProjectSkillDirs(rt.runtime.WorkDir))+1)
	for i, dir := range skills.ProjectSkillDirs(rt.runtime.WorkDir) {
		label := "Project skills"
		if i < len(labels) {
			label = labels[i]
		}
		targets = append(targets, manageSkillHubTarget{Path: dir, Scope: "project", Label: label})
	}
	if dir := settings.GetGlobalSkillsDir(); dir != "" {
		targets = append(targets, manageSkillHubTarget{Path: dir, Scope: "global", Label: "Global skills"})
	}
	s.writeResponse(req.ID, map[string]any{"sessionId": in.SessionID, "workDir": rt.runtime.WorkDir, "targets": targets}, nil)
}

func (s *server) handleManageSkillHubInstalled(req rpcRequest) {
	in, rt, _, err := s.manageSkillHubCatalog(req)
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	settings, err := s.manageSettings()
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	index, err := skillhub.NewLocalIndex(settings.GetGlobalSkillsDir(), skills.ProjectSkillDirs(rt.runtime.WorkDir))
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	s.writeResponse(req.ID, map[string]any{"sessionId": in.SessionID, "workDir": rt.runtime.WorkDir, "installed": index.List(), "session": s.manageSkillHubSessionState(rt)}, nil)
}

func (s *server) handleManageSkillHubInstall(req rpcRequest) {
	in, rt, service, err := s.manageSkillHubCatalog(req)
	if err == nil && (strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.TargetDir) == "") {
		err = fmt.Errorf("skill id and targetDir are required")
	}
	if err == nil && !filepath.IsAbs(in.TargetDir) {
		err = fmt.Errorf("targetDir must be an absolute path")
	}
	if err == nil && in.Scope != "project" && in.Scope != "global" {
		err = fmt.Errorf("scope must be project or global")
	}
	if err == nil {
		settings, _ := s.manageSettings()
		market, marketErr := manageSkillHubMarket(in.Market, manageSkillHubDefaultMarket(settings))
		if marketErr != nil {
			err = marketErr
		} else {
			result, installErr := service.Install(context.Background(), skillhub.InstallRequest{Market: market, ID: strings.TrimSpace(in.ID), Version: in.Version, Scope: in.Scope, TargetDir: in.TargetDir, Overwrite: in.Overwrite})
			if installErr == nil {
				if refreshErr := s.refreshManageSkillHubSession(rt, result.Name, in.Activate); refreshErr != nil {
					err = fmt.Errorf("installed, but failed to refresh session: %w", refreshErr)
				} else {
					s.writeResponse(req.ID, map[string]any{"install": result, "activated": in.Activate, "session": s.manageSkillHubSessionState(rt)}, nil)
					return
				}
			} else {
				err = installErr
			}
		}
	}
	s.writeManageSkillHubCatalogError(req, err)
}

func (s *server) handleManageSkillHubActivate(req rpcRequest) {
	in, rt, _, err := s.manageSkillHubCatalog(req)
	if err == nil && strings.TrimSpace(in.ID) == "" {
		err = fmt.Errorf("skill name is required")
	}
	if err == nil {
		err = s.refreshManageSkillHubSession(rt, strings.TrimSpace(in.ID), true)
	}
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	s.writeResponse(req.ID, map[string]any{"activated": true, "session": s.manageSkillHubSessionState(rt)}, nil)
}

func (s *server) handleManageSkillHubUninstall(req rpcRequest) {
	in, rt, service, err := s.manageSkillHubCatalog(req)
	activeName := ""
	if err == nil && (strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Market) == "") {
		err = fmt.Errorf("market and skill id are required")
	}
	if err == nil {
		market, marketErr := manageSkillHubMarket(in.Market, "")
		if marketErr != nil {
			err = marketErr
		} else {
			// Capture the local managed name before removal so reloading the
			// Runtime context cannot retain a reference to a removed skill.
			settings, settingsErr := s.manageSettings()
			if settingsErr != nil {
				err = settingsErr
			} else if index, indexErr := skillhub.NewLocalIndex(settings.GetGlobalSkillsDir(), skills.ProjectSkillDirs(rt.runtime.WorkDir)); indexErr != nil {
				err = indexErr
			} else if installed := index.State(market, strings.TrimSpace(in.ID)); installed != nil {
				activeName = installed.Name
			}
		}
		if err == nil {
			err = service.Uninstall(market, strings.TrimSpace(in.ID), in.Scope)
			if err == nil && activeName != "" {
				delete(rt.activeSkills, activeName)
			}
		}
	}
	if err == nil {
		err = s.refreshManageSkillHubSession(rt, "", false)
	}
	if err != nil {
		s.writeManageSkillHubCatalogError(req, err)
		return
	}
	s.writeResponse(req.ID, map[string]any{"uninstalled": true, "session": s.manageSkillHubSessionState(rt)}, nil)
}

// These active names are a Runtime resource choice, not Desktop state.  ACP
// currently materializes active skills on the loaded SessionRuntime; keeping
// the list here lets an install/activate preserve earlier choices in the same
// open session while RefreshResources remains the sole context assembler.
func (s *server) refreshManageSkillHubSession(rt *sessionRuntime, name string, activate bool) error {
	if rt == nil || rt.runtime == nil {
		return fmt.Errorf("unknown or inactive session")
	}
	return s.withSessionMutationLease(rt.id, func() error {
		if rt.activeSkills == nil {
			rt.activeSkills = make(map[string]bool)
		}
		previous := make(map[string]bool, len(rt.activeSkills))
		for skill, enabled := range rt.activeSkills {
			previous[skill] = enabled
		}
		if activate && name != "" {
			rt.activeSkills[name] = true
		}
		_, browserEnabled, _ := rt.runtime.CapabilitySnapshot()
		if err := rt.runtime.RefreshResources(s.settings, agentruntime.RefreshOptions{Workflows: s.workflows, Browser: browserEnabled, ActiveSkills: rt.activeSkills}); err != nil {
			rt.activeSkills = previous
			return err
		}
		return s.notifyAvailableCommandsFor(rt.id, rt.runtime.SkillsMgr)
	})
}

func (s *server) manageSkillHubSessionState(rt *sessionRuntime) map[string]any {
	active := make([]string, 0, len(rt.activeSkills))
	for name, enabled := range rt.activeSkills {
		if enabled {
			active = append(active, name)
		}
	}
	sort.Strings(active)
	return map[string]any{"sessionId": rt.id, "workDir": rt.runtime.WorkDir, "activeSkills": active}
}
