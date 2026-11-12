package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the SkillHub ACP contract:
//
//   - mothx/manage/skillhub/get and /patch own the global configuration;
//   - mothx/manage/skillhub/markets, /targets, and /installed own catalogue
//     bootstrap state for the current Runtime session;
//   - /search, /detail, /install, /activate, and /uninstall own every online
//     marketplace action.
//
// Native never reads settings.json, discovers a market, writes a skill, or
// constructs a SkillHub client. Tokens are write-only: the server projects
// only apiTokenConfigured, and this panel keeps a typed token in memory just
// long enough to submit one patch, then drops it on hydration.

const skillHubDefaultScope = "project"

type skillHubMarketView struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	SiteURL            string `json:"siteURL,omitempty"`
	APIURL             string `json:"apiURL,omitempty"`
	Enabled            bool   `json:"enabled"`
	APITokenConfigured bool   `json:"apiTokenConfigured"`
}

// skillHubConfigView mirrors the token-free skillhub/get and /patch response.
type skillHubConfigView struct {
	DefaultMarket       string               `json:"defaultMarket"`
	DefaultInstallScope string               `json:"defaultInstallScope"`
	OfficialHandles     []string             `json:"officialHandles"`
	Markets             []skillHubMarketView `json:"markets"`
}

// skillHubMarketDraft is the editable configuration row. Token and ClearToken
// are transient patch directives and are never hydrated from the server.
type skillHubMarketDraft struct {
	ID         string
	Name       string
	SiteURL    string
	APIURL     string
	Enabled    bool
	Token      string
	ClearToken bool
	Configured bool
}

type skillHubMarketInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	SiteURL string `json:"siteUrl"`
}

type skillHubCategory struct {
	Key      string             `json:"key"`
	Name     string             `json:"name"`
	NameEn   string             `json:"nameEn,omitempty"`
	Children []skillHubCategory `json:"children,omitempty"`
}

type skillHubCategoriesView struct {
	Categories []skillHubCategory `json:"categories"`
}

type skillHubTarget struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Label string `json:"label"`
}

type skillHubInstalledState struct {
	Installed       bool   `json:"installed"`
	Scope           string `json:"scope"`
	Dir             string `json:"dir"`
	Market          string `json:"market,omitempty"`
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Local           bool   `json:"local,omitempty"`
	Version         string `json:"version,omitempty"`
	Active          bool   `json:"active,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable,omitempty"`
}

type skillHubSessionState struct {
	SessionID    string   `json:"sessionId"`
	WorkDir      string   `json:"workDir,omitempty"`
	ActiveSkills []string `json:"activeSkills"`
}

type skillHubSkill struct {
	Market             string                  `json:"market"`
	ID                 string                  `json:"id"`
	Slug               string                  `json:"slug,omitempty"`
	Name               string                  `json:"name,omitempty"`
	DisplayName        string                  `json:"displayName,omitempty"`
	Description        string                  `json:"description,omitempty"`
	Version            string                  `json:"version,omitempty"`
	Author             string                  `json:"author,omitempty"`
	Category           string                  `json:"category,omitempty"`
	Tags               []string                `json:"tags,omitempty"`
	Downloads          int64                   `json:"downloads,omitempty"`
	Installs           int64                   `json:"installs,omitempty"`
	Stars              int64                   `json:"stars,omitempty"`
	Suspicious         bool                    `json:"suspicious,omitempty"`
	SecurityStatus     string                  `json:"securityStatus,omitempty"`
	SecurityStatusName string                  `json:"securityStatusName,omitempty"`
	Installed          *skillHubInstalledState `json:"installed,omitempty"`
}

type skillHubFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type skillHubDownloadSource struct {
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Fallback bool   `json:"fallback,omitempty"`
}

type skillHubDetail struct {
	skillHubSkill
	Files           []skillHubFile           `json:"files,omitempty"`
	DownloadSources []skillHubDownloadSource `json:"downloadSources,omitempty"`
	Readme          string                   `json:"readme,omitempty"`
	SecurityReports json.RawMessage          `json:"securityReports,omitempty"`
	Evaluation      json.RawMessage          `json:"evaluation,omitempty"`
}

type skillHubSearchPage struct {
	Items      []skillHubSkill `json:"items"`
	Total      int64           `json:"total,omitempty"`
	Page       int             `json:"page,omitempty"`
	PageSize   int             `json:"pageSize,omitempty"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type skillHubMarketsView struct {
	Markets       []skillHubMarketInfo `json:"markets"`
	DefaultMarket string               `json:"defaultMarket,omitempty"`
}

type skillHubTargetsView struct {
	SessionID string           `json:"sessionId"`
	WorkDir   string           `json:"workDir,omitempty"`
	Targets   []skillHubTarget `json:"targets"`
}

type skillHubInstalledView struct {
	SessionID string                   `json:"sessionId"`
	WorkDir   string                   `json:"workDir,omitempty"`
	Installed []skillHubInstalledState `json:"installed"`
	Session   skillHubSessionState     `json:"session"`
}

type skillHubInstallResult struct {
	Name             string   `json:"name"`
	Market           string   `json:"market"`
	Version          string   `json:"version"`
	Scope            string   `json:"scope"`
	Dir              string   `json:"dir"`
	Installed        bool     `json:"installed"`
	AlreadyInstalled bool     `json:"alreadyInstalled,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type skillHubInstallResponse struct {
	Install   skillHubInstallResult `json:"install"`
	Activated bool                  `json:"activated"`
	Session   skillHubSessionState  `json:"session"`
}

type skillHubActionResponse struct {
	Activated   bool                 `json:"activated"`
	Uninstalled bool                 `json:"uninstalled"`
	Session     skillHubSessionState `json:"session"`
}

type skillHubPanelState struct {
	// Tab 0 browses the catalogue; tab 1 edits the global configuration.
	Tab int

	// Configuration projection from skillhub/get and skillhub/patch.
	ConfigLoaded        bool
	ConfigLoading       bool
	ConfigSaving        bool
	ConfigErr           string
	ConfigStatus        string
	ConfigStatusIsError bool
	DefaultMarket       string
	DefaultInstallScope string
	OfficialHandles     string
	Markets             []skillHubMarketDraft

	// Catalogue bootstrap for the active Runtime session.
	CatalogLoaded  bool
	CatalogLoading bool
	CatalogErr     string
	SessionID      string
	WorkDir        string
	MarketInfos    []skillHubMarketInfo
	Targets        []skillHubTarget
	Installed      []skillHubInstalledState
	ActiveSkills   []string
	SelectedMarket string
	TargetLabel    string
	TargetPath     string
	TargetScope    string

	// Search projection.
	Query             string
	Category          string
	Categories        []skillHubCategory
	CategoriesLoaded  bool
	CategoryLoading   bool
	CategoryErr       string
	Official          bool
	SearchBusy        bool
	SearchErr         string
	Results           []skillHubSkill
	ResultsLoaded     bool
	ResultsQuery      string
	ResultsTotal      int64
	ResultsNextCursor string

	// Detail and action projection.
	DetailOpen           bool
	DetailBusy           bool
	Detail               *skillHubDetail
	DetailErr            string
	ActionBusy           string
	CatalogStatus        string
	CatalogStatusIsError bool
	Overwrite            bool
	ActivateOnInstall    bool

	// Native confirmation/dialog state.
	UninstallOpen   bool
	UninstallMarket string
	UninstallID     string
	UninstallName   string
	UninstallScope  string

	AddMarketOpen bool
	AddMarketID   string
	AddMarketErr  string

	// Independent sequences guard configuration and catalogue responses. A
	// config save cannot invalidate an in-flight catalogue read, while leaving
	// the tab still invalidates both.
	ConfigSeq   int64
	CatalogSeq  int64
	CategorySeq int64
}

func newSkillHubPanelState() skillHubPanelState {
	return skillHubPanelState{
		DefaultInstallScope: skillHubDefaultScope,
		Markets:             []skillHubMarketDraft{},
		MarketInfos:         []skillHubMarketInfo{},
		Targets:             []skillHubTarget{},
		Installed:           []skillHubInstalledState{},
		ActiveSkills:        []string{},
		Results:             []skillHubSkill{},
	}
}

func (a *nativeApp) skillHubConfigCapable() bool {
	return a.featureEnabled("manageSkillHub")
}

func (a *nativeApp) skillHubCatalogCapable() bool {
	return a.featureEnabled("manageSkillHubCatalog")
}

func (a *nativeApp) skillHubCapable() bool {
	return a.skillHubConfigCapable() || a.skillHubCatalogCapable()
}

func (a *nativeApp) skillHubConfigCurrent(seq, reloadSeq int64) bool {
	return a.panelVisible("skills", "skillhub") &&
		seq == atomic.LoadInt64(&a.skillhub.ConfigSeq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) skillHubCatalogCurrent(seq, reloadSeq int64) bool {
	return a.panelVisible("skills", "skillhub") &&
		seq == atomic.LoadInt64(&a.skillhub.CatalogSeq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateSkillHubPanel drops every transient projection and makes both
// in-flight config and catalogue responses stale. Tokens typed into the form
// are discarded with the rest of the state.
func (a *nativeApp) invalidateSkillHubPanel() {
	s := &a.skillhub
	atomic.AddInt64(&s.ConfigSeq, 1)
	atomic.AddInt64(&s.CatalogSeq, 1)
	s.Tab = 0
	s.ConfigLoaded = false
	s.ConfigLoading = false
	s.ConfigSaving = false
	s.ConfigErr = ""
	s.ConfigStatus = ""
	s.ConfigStatusIsError = false
	s.DefaultMarket = ""
	s.DefaultInstallScope = skillHubDefaultScope
	s.OfficialHandles = ""
	s.Markets = nil
	s.CatalogLoaded = false
	s.CatalogLoading = false
	s.CatalogErr = ""
	s.SessionID = ""
	s.WorkDir = ""
	s.MarketInfos = nil
	s.Targets = nil
	s.Installed = nil
	s.ActiveSkills = nil
	s.SelectedMarket = ""
	s.TargetLabel = ""
	s.TargetPath = ""
	s.TargetScope = ""
	s.Query = ""
	s.Category = ""
	s.Categories = nil
	s.CategoriesLoaded = false
	s.CategoryLoading = false
	s.CategoryErr = ""
	s.Official = false
	atomic.AddInt64(&s.CategorySeq, 1)
	s.SearchBusy = false
	s.SearchErr = ""
	s.Results = nil
	s.ResultsLoaded = false
	s.ResultsQuery = ""
	s.ResultsTotal = 0
	s.ResultsNextCursor = ""
	s.DetailOpen = false
	s.DetailBusy = false
	s.Detail = nil
	s.DetailErr = ""
	s.ActionBusy = ""
	s.CatalogStatus = ""
	s.CatalogStatusIsError = false
	s.Overwrite = false
	s.ActivateOnInstall = false
	s.UninstallOpen = false
	s.UninstallMarket = ""
	s.UninstallID = ""
	s.UninstallName = ""
	s.UninstallScope = ""
	s.AddMarketOpen = false
	s.AddMarketID = ""
	s.AddMarketErr = ""
}

// startSkillHubPanelLoad starts the capability-gated configuration and
// catalogue loads. Each projection is independent: a runtime that advertises
// only manageSkillHub still gets the config editor, and one that advertises
// only manageSkillHubCatalog still gets the browser.
func (a *nativeApp) startSkillHubPanelLoad() {
	if !a.panelVisible("skills", "skillhub") {
		return
	}
	s := &a.skillhub
	if !a.skillHubConfigCapable() {
		if !s.ConfigLoaded {
			s.ConfigLoaded = true
			s.ConfigErr = "当前运行时不支持 SkillHub 配置管理（manageSkillHub）。"
		}
	} else if !s.ConfigLoaded && !s.ConfigLoading {
		a.startSkillHubConfigLoad()
	}
	if !a.skillHubCatalogCapable() {
		if !s.CatalogLoaded {
			s.CatalogLoaded = true
			s.CatalogErr = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		}
		return
	}
	if strings.TrimSpace(a.currentSession) == "" {
		if !s.CatalogLoaded {
			s.CatalogLoaded = true
			s.CatalogErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		}
		return
	}
	if !s.CatalogLoaded && !s.CatalogLoading {
		a.startSkillHubCatalogLoad()
	}
}

func (a *nativeApp) startSkillHubConfigLoad() {
	if !a.panelVisible("skills", "skillhub") || !a.skillHubConfigCapable() {
		return
	}
	s := &a.skillhub
	if s.ConfigLoading {
		return
	}
	seq := atomic.AddInt64(&s.ConfigSeq, 1)
	s.ConfigLoading = true
	s.ConfigErr = ""
	s.ConfigStatus = ""
	s.ConfigStatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadSkillHubConfig(seq, startReloadSeq)
}

func (a *nativeApp) loadSkillHubConfig(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.skillHubConfigCurrent(seq, startReloadSeq) {
				return
			}
			a.skillhub.ConfigLoading = false
			a.skillhub.ConfigErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/skillhub/get", map[string]any{})
	a.dispatch(func() {
		if !a.skillHubConfigCurrent(seq, startReloadSeq) {
			return
		}
		s := &a.skillhub
		s.ConfigLoading = false
		if err != nil {
			s.ConfigErr = "读取 SkillHub 配置失败：" + err.Error()
			return
		}
		var view skillHubConfigView
		if err := json.Unmarshal(raw, &view); err != nil {
			s.ConfigErr = "SkillHub 配置响应无效：" + err.Error()
			return
		}
		a.applySkillHubConfigView(&view)
	})
}

func (a *nativeApp) applySkillHubConfigView(view *skillHubConfigView) {
	if view == nil {
		return
	}
	s := &a.skillhub
	defaultScope := strings.TrimSpace(view.DefaultInstallScope)
	if defaultScope != "project" && defaultScope != "global" {
		defaultScope = skillHubDefaultScope
	}
	s.DefaultMarket = strings.TrimSpace(view.DefaultMarket)
	s.DefaultInstallScope = defaultScope
	s.OfficialHandles = strings.Join(view.OfficialHandles, "\n")
	markets := make([]skillHubMarketDraft, 0, len(view.Markets))
	for _, market := range view.Markets {
		markets = append(markets, skillHubMarketDraft{
			ID:         strings.TrimSpace(market.ID),
			Name:       market.Name,
			SiteURL:    market.SiteURL,
			APIURL:     market.APIURL,
			Enabled:    market.Enabled,
			Configured: market.APITokenConfigured,
		})
	}
	s.Markets = markets
	s.ConfigLoaded = true
	s.ConfigLoading = false
	s.ConfigErr = ""
}

// skillHubParseHandles converts the textarea (one handle per line) into the
// patch array. Blank lines are ignored, matching the Desktop textarea.
func skillHubParseHandles(text string) []string {
	out := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func skillHubMarketIDs(markets []skillHubMarketDraft) []string {
	ids := make([]string, 0, len(markets))
	for _, market := range markets {
		if id := strings.TrimSpace(market.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// buildSkillHubConfigPatch validates the local form and projects it into the
// strict skillhub/patch envelope. A market token is submitted only when the
// user typed one and did not request a clear; otherwise the Runtime preserves
// the existing stored secret.
func buildSkillHubConfigPatch(defaultMarket, defaultScope, officialHandles string, markets []skillHubMarketDraft) (map[string]any, error) {
	defaultMarket = strings.TrimSpace(defaultMarket)
	if defaultMarket == "" {
		return nil, localizedError("默认市场不能为空")
	}
	defaultScope = strings.TrimSpace(defaultScope)
	if defaultScope != "project" && defaultScope != "global" {
		return nil, localizedError("默认安装范围必须是 project 或 global")
	}
	if len(markets) == 0 {
		return nil, localizedError("至少需要保留一个市场")
	}
	seen := make(map[string]bool, len(markets))
	payload := make([]map[string]any, 0, len(markets))
	for index, market := range markets {
		id := strings.TrimSpace(market.ID)
		if id == "" {
			return nil, localizedErrorf("第 %d 个市场缺少 ID", index+1)
		}
		if seen[id] {
			return nil, localizedErrorf("市场 ID %q 重复", id)
		}
		seen[id] = true
		entry := map[string]any{
			"id":      id,
			"name":    strings.TrimSpace(market.Name),
			"siteURL": strings.TrimSpace(market.SiteURL),
			"apiURL":  strings.TrimSpace(market.APIURL),
			"enabled": market.Enabled,
		}
		token := strings.TrimSpace(market.Token)
		if market.ClearToken && token != "" {
			return nil, localizedErrorf("市场 %q 不能同时提交新 Token 和清除 Token", id)
		}
		if market.ClearToken {
			entry["clearApiToken"] = true
		} else if token != "" {
			entry["apiToken"] = token
		}
		payload = append(payload, entry)
	}
	patch := map[string]any{
		"defaultMarket":       defaultMarket,
		"defaultInstallScope": defaultScope,
		"officialHandles":     skillHubParseHandles(officialHandles),
		"markets":             payload,
	}
	return map[string]any{"patch": patch}, nil
}

func (a *nativeApp) saveSkillHubConfig() {
	s := &a.skillhub
	if !a.skillHubConfigCapable() {
		s.ConfigStatus = "当前运行时不支持 SkillHub 配置管理（manageSkillHub）。"
		s.ConfigStatusIsError = true
		return
	}
	if s.ConfigLoading || s.ConfigSaving {
		return
	}
	request, err := buildSkillHubConfigPatch(s.DefaultMarket, s.DefaultInstallScope, s.OfficialHandles, s.Markets)
	if err != nil {
		s.ConfigStatus = "保存失败：" + err.Error()
		s.ConfigStatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		s.ConfigStatus = "保存失败：尚未连接到 MothX ACP。"
		s.ConfigStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&s.ConfigSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	s.ConfigSaving = true
	s.ConfigStatus = "正在保存 SkillHub 配置…"
	s.ConfigStatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/skillhub/patch", request)
		a.dispatch(func() {
			if !a.skillHubConfigCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.ConfigSaving = false
			if err != nil {
				s.ConfigStatus = "保存 SkillHub 配置失败：" + err.Error()
				s.ConfigStatusIsError = true
				return
			}
			var view skillHubConfigView
			if err := json.Unmarshal(raw, &view); err != nil {
				s.ConfigStatus = "保存后 SkillHub 配置响应无效：" + err.Error()
				s.ConfigStatusIsError = true
				return
			}
			a.applySkillHubConfigView(&view)
			s.ConfigStatus = "SkillHub 配置已保存"
			s.ConfigStatusIsError = false
		})
	}()
}

func (a *nativeApp) openSkillHubAddMarket() {
	if !a.skillHubConfigCapable() {
		return
	}
	s := &a.skillhub
	s.AddMarketOpen = true
	s.AddMarketID = ""
	s.AddMarketErr = ""
}

func (a *nativeApp) closeSkillHubAddMarket() {
	s := &a.skillhub
	s.AddMarketOpen = false
	s.AddMarketID = ""
	s.AddMarketErr = ""
}

func (a *nativeApp) confirmSkillHubAddMarket() {
	s := &a.skillhub
	id := strings.TrimSpace(s.AddMarketID)
	if id == "" {
		s.AddMarketErr = "市场 ID 不能为空"
		return
	}
	for _, market := range s.Markets {
		if strings.TrimSpace(market.ID) == id {
			s.AddMarketErr = Tf("市场 ID %q 已存在", id)
			return
		}
	}
	s.Markets = append(s.Markets, skillHubMarketDraft{ID: id, Enabled: true})
	s.AddMarketOpen = false
	s.AddMarketID = ""
	s.AddMarketErr = ""
}

func (a *nativeApp) removeSkillHubMarket(index int) {
	s := &a.skillhub
	if index < 0 || index >= len(s.Markets) {
		return
	}
	s.Markets = append(s.Markets[:index], s.Markets[index+1:]...)
}

func (a *nativeApp) startSkillHubCatalogLoad() {
	if !a.panelVisible("skills", "skillhub") || !a.skillHubCatalogCapable() {
		return
	}
	s := &a.skillhub
	if s.CatalogLoading {
		return
	}
	sessionID := strings.TrimSpace(a.currentSession)
	if sessionID == "" {
		s.CatalogLoaded = true
		s.CatalogErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		return
	}
	seq := atomic.AddInt64(&s.CatalogSeq, 1)
	s.CatalogLoading = true
	s.CatalogErr = ""
	s.CatalogStatus = ""
	s.CatalogStatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadSkillHubCatalog(seq, startReloadSeq, sessionID)
}

func skillHubCall(client *acpClient, method string, params map[string]any, target any) error {
	raw, err := client.call(method, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return localizedErrorf("%s 响应无效：%w", strings.TrimPrefix(method, "mothx/manage/skillhub/"), err)
	}
	return nil
}

func (a *nativeApp) loadSkillHubCatalog(seq, startReloadSeq int64, sessionID string) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) || a.currentSession != sessionID {
				return
			}
			a.skillhub.CatalogLoading = false
			a.skillhub.CatalogErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	var markets skillHubMarketsView
	if err := skillHubCall(client, "mothx/manage/skillhub/markets", buildSkillHubMarketsRequest(sessionID), &markets); err != nil {
		a.skillHubCatalogFailed(seq, startReloadSeq, sessionID, "读取市场列表失败："+err.Error())
		return
	}
	var targets skillHubTargetsView
	if err := skillHubCall(client, "mothx/manage/skillhub/targets", buildSkillHubTargetsRequest(sessionID), &targets); err != nil {
		a.skillHubCatalogFailed(seq, startReloadSeq, sessionID, "读取安装目录失败："+err.Error())
		return
	}
	var installed skillHubInstalledView
	if err := skillHubCall(client, "mothx/manage/skillhub/installed", buildSkillHubInstalledRequest(sessionID), &installed); err != nil {
		a.skillHubCatalogFailed(seq, startReloadSeq, sessionID, "读取已安装技能失败："+err.Error())
		return
	}
	a.dispatch(func() {
		if !a.skillHubCatalogCurrent(seq, startReloadSeq) || a.currentSession != sessionID {
			return
		}
		a.applySkillHubCatalogViews(sessionID, &markets, &targets, &installed)
	})
}

func (a *nativeApp) skillHubCatalogFailed(seq, startReloadSeq int64, sessionID, message string) {
	a.dispatch(func() {
		if !a.skillHubCatalogCurrent(seq, startReloadSeq) || a.currentSession != sessionID {
			return
		}
		a.skillhub.CatalogLoading = false
		a.skillhub.CatalogErr = message
	})
}

func (a *nativeApp) applySkillHubCatalogViews(sessionID string, markets *skillHubMarketsView, targets *skillHubTargetsView, installed *skillHubInstalledView) {
	if markets == nil || targets == nil || installed == nil {
		return
	}
	s := &a.skillhub
	s.SessionID = sessionID
	if strings.TrimSpace(markets.DefaultMarket) != "" {
		s.SelectedMarket = skillHubChooseMarket(s.SelectedMarket, s.DefaultMarket, markets.DefaultMarket, markets.Markets)
	} else {
		s.SelectedMarket = skillHubChooseMarket(s.SelectedMarket, s.DefaultMarket, "", markets.Markets)
	}
	s.MarketInfos = append([]skillHubMarketInfo(nil), markets.Markets...)
	if s.MarketInfos == nil {
		s.MarketInfos = []skillHubMarketInfo{}
	}
	s.Targets = append([]skillHubTarget(nil), targets.Targets...)
	if s.Targets == nil {
		s.Targets = []skillHubTarget{}
	}
	if workDir := strings.TrimSpace(targets.WorkDir); workDir != "" {
		s.WorkDir = workDir
	} else {
		s.WorkDir = strings.TrimSpace(installed.WorkDir)
	}
	s.Installed = append([]skillHubInstalledState(nil), installed.Installed...)
	if s.Installed == nil {
		s.Installed = []skillHubInstalledState{}
	}
	s.ActiveSkills = append([]string(nil), installed.Session.ActiveSkills...)
	if s.ActiveSkills == nil {
		s.ActiveSkills = []string{}
	}
	a.skillHubRefreshActiveFlags()

	label, path, scope := skillHubChooseTarget(s.TargetLabel, s.DefaultInstallScope, s.Targets)
	s.TargetLabel, s.TargetPath, s.TargetScope = label, path, scope
	a.skillHubApplyInstalledToResults()
	s.CatalogLoaded = true
	s.CatalogLoading = false
	s.CatalogErr = ""
	if !s.CategoriesLoaded {
		a.loadSkillHubCategories()
	}
}

// skillHubChooseMarket prefers the current selection, then the user's saved
// default, then the ACP-projected default, then the first available market.
func skillHubChooseMarket(current, configDefault, projectedDefault string, markets []skillHubMarketInfo) string {
	available := make(map[string]bool, len(markets))
	first := ""
	for _, market := range markets {
		id := strings.TrimSpace(market.ID)
		if id == "" {
			continue
		}
		if first == "" {
			first = id
		}
		available[id] = true
	}
	for _, candidate := range []string{current, configDefault, projectedDefault} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && available[candidate] {
			return candidate
		}
	}
	return first
}

func skillHubTargetLabels(targets []skillHubTarget) []string {
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		label := strings.TrimSpace(target.Label)
		if label == "" {
			label = T("技能目录")
		}
		labels = append(labels, fmt.Sprintf("%s · %s · %s", label, target.Scope, target.Path))
	}
	return labels
}

func skillHubTargetLabel(target skillHubTarget) string {
	label := strings.TrimSpace(target.Label)
	if label == "" {
		label = "技能目录"
	}
	return fmt.Sprintf("%s · %s · %s", label, target.Scope, target.Path)
}

func skillHubTargetByLabel(targets []skillHubTarget, label string) (skillHubTarget, bool) {
	for _, target := range targets {
		if skillHubTargetLabel(target) == label {
			return target, true
		}
	}
	return skillHubTarget{}, false
}

// skillHubChooseTarget keeps the current path when it is still advertised,
// otherwise picks a target in the configured install scope, then the first.
func skillHubChooseTarget(currentLabel, scope string, targets []skillHubTarget) (label, path, targetScope string) {
	for _, target := range targets {
		if strings.TrimSpace(target.Path) != "" && skillHubTargetLabel(target) == currentLabel {
			return skillHubTargetLabel(target), target.Path, target.Scope
		}
	}
	for _, target := range targets {
		if target.Scope == scope && strings.TrimSpace(target.Path) != "" {
			return skillHubTargetLabel(target), target.Path, target.Scope
		}
	}
	for _, target := range targets {
		if strings.TrimSpace(target.Path) != "" {
			return skillHubTargetLabel(target), target.Path, target.Scope
		}
	}
	return "", "", ""
}

func skillHubOnMarketChanged(s *skillHubPanelState) {
	atomic.AddInt64(&s.CategorySeq, 1)
	s.Category = ""
	s.Categories = nil
	s.CategoriesLoaded = false
	s.CategoryLoading = false
	s.CategoryErr = ""
	s.Official = false
	s.Results = []skillHubSkill{}
	s.ResultsLoaded = false
	s.ResultsQuery = ""
	s.ResultsTotal = 0
	s.ResultsNextCursor = ""
	s.SearchErr = ""
}

func skillHubOnTargetChanged(s *skillHubPanelState) {
	if target, ok := skillHubTargetByLabel(s.Targets, s.TargetLabel); ok {
		s.TargetPath = target.Path
		s.TargetScope = target.Scope
	}
}

func buildSkillHubMarketsRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func buildSkillHubTargetsRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func buildSkillHubInstalledRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func skillHubSearchSort(market string) string {
	if strings.TrimSpace(market) == "clawhub.ai" {
		return ""
	}
	return "downloads"
}

func buildSkillHubSearchRequest(sessionID, market, query, category string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"query":     strings.TrimSpace(query),
		"category":  strings.TrimSpace(category),
		"sort":      skillHubSearchSort(market),
		"order":     "desc",
		"page":      1,
		"limit":     20,
	}
}

func buildSkillHubOfficialRequest(sessionID, market, query string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"query":     strings.TrimSpace(query),
		"page":      1,
		"limit":     20,
	}
}

func buildSkillHubDetailRequest(sessionID, market, id string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"id":        strings.TrimSpace(id),
	}
}

func buildSkillHubInstallRequest(sessionID, market, id, version, scope, targetDir string, overwrite, activate bool) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"id":        strings.TrimSpace(id),
		"version":   strings.TrimSpace(version),
		"scope":     strings.TrimSpace(scope),
		"targetDir": strings.TrimSpace(targetDir),
		"overwrite": overwrite,
		"activate":  activate,
	}
}

func buildSkillHubActivateRequest(sessionID, id string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"id":        strings.TrimSpace(id),
	}
}

func buildSkillHubUninstallRequest(sessionID, market, id, scope string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"id":        strings.TrimSpace(id),
		"scope":     strings.TrimSpace(scope),
	}
}

func skillHubTitle(skill skillHubSkill) string {
	for _, value := range []string{skill.DisplayName, skill.Name, skill.Slug, skill.ID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return T("未命名技能")
}

func skillHubInstalledName(skill skillHubSkill) string {
	if skill.Installed != nil {
		for _, value := range []string{skill.Installed.Name, skill.Slug, skill.Name} {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	for _, value := range []string{skill.Slug, skill.Name, skill.ID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func skillHubActive(active []string, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, entry := range active {
		if strings.TrimSpace(entry) == name {
			return true
		}
	}
	return false
}

func (a *nativeApp) skillHubRefreshActiveFlags() {
	s := &a.skillhub
	for index := range s.Installed {
		if s.Installed[index].Name != "" {
			s.Installed[index].Active = skillHubActive(s.ActiveSkills, s.Installed[index].Name)
		}
	}
}

func skillHubFindInstalled(installed []skillHubInstalledState, market, id string) *skillHubInstalledState {
	market = strings.TrimSpace(market)
	id = strings.TrimSpace(id)
	for index := range installed {
		if strings.TrimSpace(installed[index].Market) == market && strings.TrimSpace(installed[index].ID) == id {
			copy := installed[index]
			return &copy
		}
	}
	return nil
}

func (a *nativeApp) skillHubApplyInstalledToResults() {
	s := &a.skillhub
	for index := range s.Results {
		if state := skillHubFindInstalled(s.Installed, s.Results[index].Market, s.Results[index].ID); state != nil {
			state.Active = skillHubActive(s.ActiveSkills, skillHubInstalledName(s.Results[index]))
			state.UpdateAvailable = skillHubVersionDiffers(s.Results[index].Version, state.Version)
			s.Results[index].Installed = state
		} else {
			s.Results[index].Installed = nil
		}
	}
	if s.Detail != nil {
		if state := skillHubFindInstalled(s.Installed, s.Detail.Market, s.Detail.ID); state != nil {
			state.Active = skillHubActive(s.ActiveSkills, skillHubInstalledName(s.Detail.skillHubSkill))
			state.UpdateAvailable = skillHubVersionDiffers(s.Detail.Version, state.Version)
			s.Detail.Installed = state
		} else {
			s.Detail.Installed = nil
		}
	}
}

func skillHubVersionDiffers(remote, installed string) bool {
	remote = strings.TrimSpace(remote)
	installed = strings.TrimSpace(installed)
	return remote != "" && installed != "" && remote != installed
}

func (a *nativeApp) skillHubApplySession(session skillHubSessionState) {
	s := &a.skillhub
	if strings.TrimSpace(session.SessionID) != "" {
		s.SessionID = strings.TrimSpace(session.SessionID)
	}
	if strings.TrimSpace(session.WorkDir) != "" {
		s.WorkDir = strings.TrimSpace(session.WorkDir)
	}
	s.ActiveSkills = append([]string(nil), session.ActiveSkills...)
	if s.ActiveSkills == nil {
		s.ActiveSkills = []string{}
	}
	sort.Strings(s.ActiveSkills)
	a.skillHubRefreshActiveFlags()
	a.skillHubApplyInstalledToResults()
}

func (a *nativeApp) skillHubMarkInstalled(market, id string, result skillHubInstallResult) {
	s := &a.skillhub
	state := skillHubInstalledState{
		Installed: true,
		Scope:     result.Scope,
		Dir:       result.Dir,
		Market:    result.Market,
		ID:        strings.TrimSpace(id),
		Name:      result.Name,
		Version:   result.Version,
	}
	if state.Market == "" {
		state.Market = strings.TrimSpace(market)
	}
	if state.Scope == "" {
		state.Scope = s.TargetScope
	}
	state.Active = skillHubActive(s.ActiveSkills, state.Name)
	replaced := false
	for index := range s.Installed {
		if strings.TrimSpace(s.Installed[index].Market) == state.Market && strings.TrimSpace(s.Installed[index].ID) == state.ID {
			s.Installed[index] = state
			replaced = true
			break
		}
	}
	if !replaced {
		s.Installed = append(s.Installed, state)
	}
	a.skillHubApplyInstalledToResults()
}

func (a *nativeApp) skillHubRemoveInstalled(market, id string) {
	s := &a.skillhub
	filtered := make([]skillHubInstalledState, 0, len(s.Installed))
	for _, entry := range s.Installed {
		if strings.TrimSpace(entry.Market) == strings.TrimSpace(market) && strings.TrimSpace(entry.ID) == strings.TrimSpace(id) {
			continue
		}
		filtered = append(filtered, entry)
	}
	s.Installed = filtered
	a.skillHubApplyInstalledToResults()
}

func (a *nativeApp) skillHubBrowseBusy() bool {
	s := &a.skillhub
	return s.CatalogLoading || s.SearchBusy || s.ActionBusy != "" || s.DetailBusy
}

func skillHubCategoryName(category skillHubCategory) string {
	for _, value := range []string{category.Name, category.NameEn, category.Key} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return T("未命名分类")
}

func skillHubFlattenCategories(categories []skillHubCategory) []skillHubCategory {
	out := make([]skillHubCategory, 0, len(categories))
	for _, category := range categories {
		out = append(out, category)
		out = append(out, skillHubFlattenCategories(category.Children)...)
	}
	return out
}

// loadSkillHubCategories projects the selected market's category tree. It is
// optional: a market without category support leaves the selector hidden.
func (a *nativeApp) loadSkillHubCategories() {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() || s.CategoryLoading || s.SessionID == "" {
		return
	}
	if strings.TrimSpace(s.SelectedMarket) == "" {
		return
	}
	client := a.acp
	if client == nil {
		s.CategoriesLoaded = true
		return
	}
	seq := atomic.AddInt64(&s.CategorySeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := s.SessionID
	market := s.SelectedMarket
	s.CategoryLoading = true
	s.CategoryErr = ""
	go func() {
		raw, err := client.call("mothx/manage/skillhub/categories", map[string]any{
			"sessionId": sessionID,
			"market":    market,
		})
		a.dispatch(func() {
			s := &a.skillhub
			if seq != atomic.LoadInt64(&s.CategorySeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) ||
				a.currentSession != sessionID || s.SelectedMarket != market {
				return
			}
			s.CategoryLoading = false
			s.CategoriesLoaded = true
			if err != nil {
				s.CategoryErr = "读取技能分类失败：" + err.Error()
				return
			}
			var view skillHubCategoriesView
			if err := json.Unmarshal(raw, &view); err != nil {
				s.CategoryErr = "技能分类响应无效：" + err.Error()
				return
			}
			s.Categories = view.Categories
			s.CategoryErr = ""
		})
	}()
}

// searchSkillHub searches the selected market. An empty query is a valid
// browse request and uses the market's download ordering.
func (a *nativeApp) searchSkillHub() {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() {
		s.SearchErr = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		return
	}
	if strings.TrimSpace(s.SessionID) == "" || a.currentSession == "" {
		s.SearchErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		return
	}
	if s.CatalogLoading || s.SearchBusy || s.ActionBusy != "" || s.DetailBusy {
		return
	}
	market := strings.TrimSpace(s.SelectedMarket)
	if market == "" {
		s.SearchErr = "请选择技能市场。"
		return
	}
	client := a.acp
	if client == nil {
		s.SearchErr = "搜索失败：尚未连接到 MothX ACP。"
		return
	}
	query := strings.TrimSpace(s.Query)
	sessionID := s.SessionID
	seq := atomic.AddInt64(&s.CatalogSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	s.SearchBusy = true
	s.SearchErr = ""
	s.CatalogStatus = ""
	s.CatalogStatusIsError = false
	method := "mothx/manage/skillhub/search"
	request := buildSkillHubSearchRequest(sessionID, market, query, s.Category)
	if s.Official {
		method = "mothx/manage/skillhub/official"
		request = buildSkillHubOfficialRequest(sessionID, market, query)
	}
	go func() {
		raw, err := client.call(method, request)
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.SearchBusy = false
			if err != nil {
				s.SearchErr = "搜索技能失败：" + err.Error()
				return
			}
			var page skillHubSearchPage
			if err := json.Unmarshal(raw, &page); err != nil {
				s.SearchErr = "技能搜索响应无效：" + err.Error()
				return
			}
			s.Results = append([]skillHubSkill(nil), page.Items...)
			if s.Results == nil {
				s.Results = []skillHubSkill{}
			}
			s.ResultsLoaded = true
			s.ResultsQuery = query
			s.ResultsTotal = page.Total
			s.ResultsNextCursor = page.NextCursor
			a.skillHubApplyInstalledToResults()
		})
	}()
}

func (a *nativeApp) refreshSkillHubCatalog() {
	s := &a.skillhub
	if a.skillHubBrowseBusy() {
		return
	}
	s.CatalogLoaded = false
	s.CatalogErr = ""
	a.startSkillHubPanelLoad()
}

func (a *nativeApp) openSkillHubDetail(market, id string) {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() || s.DetailBusy || s.ActionBusy != "" || s.SearchBusy {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		s.CatalogStatus = "技能市场和 ID 不能为空。"
		s.CatalogStatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		s.CatalogStatus = "读取技能详情失败：尚未连接到 MothX ACP。"
		s.CatalogStatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&s.CatalogSeq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := s.SessionID
	s.DetailOpen = true
	s.DetailBusy = true
	s.Detail = nil
	s.DetailErr = ""
	go func() {
		raw, err := client.call("mothx/manage/skillhub/detail", buildSkillHubDetailRequest(sessionID, market, id))
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.DetailBusy = false
			if err != nil {
				s.DetailErr = "读取技能详情失败：" + err.Error()
				return
			}
			var detail skillHubDetail
			if err := json.Unmarshal(raw, &detail); err != nil {
				s.DetailErr = "技能详情响应无效：" + err.Error()
				return
			}
			if detail.Market == "" {
				detail.Market = market
			}
			if detail.ID == "" {
				detail.ID = id
			}
			if state := skillHubFindInstalled(a.skillhub.Installed, detail.Market, detail.ID); state != nil {
				state.Active = skillHubActive(a.skillhub.ActiveSkills, skillHubInstalledName(detail.skillHubSkill))
				detail.Installed = state
			}
			a.skillhub.Detail = &detail
			a.skillhub.DetailErr = ""
		})
	}()
}

func (a *nativeApp) closeSkillHubDetail() {
	s := &a.skillhub
	s.DetailOpen = false
	s.DetailBusy = false
	s.Detail = nil
	s.DetailErr = ""
}

func (a *nativeApp) installSkillHub(market, id, version string) {
	a.installSkillHubWithOverwrite(market, id, version, a.skillhub.Overwrite)
}

// installSkillHubWithOverwrite lets the explicit "update" action request an
// overwrite without mutating the user's persistent form choice.
func (a *nativeApp) installSkillHubWithOverwrite(market, id, version string, overwrite bool) {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() {
		s.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		s.CatalogStatusIsError = true
		return
	}
	if s.CatalogLoading || s.SearchBusy || s.ActionBusy != "" || s.DetailBusy {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		s.CatalogStatus = "技能市场和 ID 不能为空。"
		s.CatalogStatusIsError = true
		return
	}
	if strings.TrimSpace(s.TargetPath) == "" {
		s.CatalogStatus = "请选择安装目录。"
		s.CatalogStatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		s.CatalogStatus = "安装技能失败：尚未连接到 MothX ACP。"
		s.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&s.CatalogSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := s.SessionID
	targetScope := s.TargetScope
	targetPath := s.TargetPath
	activate := s.ActivateOnInstall
	s.ActionBusy = "install:" + id
	s.CatalogStatus = Tf("正在安装技能 %s…", id)
	s.CatalogStatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/skillhub/install", buildSkillHubInstallRequest(
			sessionID, market, id, version, targetScope, targetPath, overwrite, activate))
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.ActionBusy = ""
			if err != nil {
				s.CatalogStatus = "安装技能失败：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			var response skillHubInstallResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				s.CatalogStatus = "安装技能响应无效：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			a.skillHubApplySession(response.Session)
			a.skillHubMarkInstalled(market, id, response.Install)
			s.DetailOpen = false
			s.Detail = nil
			s.DetailErr = ""
			name := strings.TrimSpace(response.Install.Name)
			if name == "" {
				name = id
			}
			s.CatalogStatus = skillHubInstallStatus(
				name,
				response.Install.AlreadyInstalled,
				response.Install.Installed,
				response.Activated,
				len(response.Install.Warnings),
			)
			s.CatalogStatusIsError = false
		})
	}()
}

func (a *nativeApp) activateSkillHub(market, id, name string) {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() {
		s.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		s.CatalogStatusIsError = true
		return
	}
	if s.CatalogLoading || s.SearchBusy || s.ActionBusy != "" || s.DetailBusy {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		s.CatalogStatus = "要激活的技能名称不能为空。"
		s.CatalogStatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		s.CatalogStatus = "激活技能失败：尚未连接到 MothX ACP。"
		s.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&s.CatalogSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := s.SessionID
	s.ActionBusy = "activate:" + name
	s.CatalogStatus = Tf("正在激活技能 %s…", name)
	s.CatalogStatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/skillhub/activate", buildSkillHubActivateRequest(sessionID, name))
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.ActionBusy = ""
			if err != nil {
				s.CatalogStatus = "激活技能失败：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			var response skillHubActionResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				s.CatalogStatus = "激活技能响应无效：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			a.skillHubApplySession(response.Session)
			s.CatalogStatus = Tf("技能 %s 已在当前会话激活", name)
			s.CatalogStatusIsError = false
		})
	}()
}

func (a *nativeApp) requestUninstallSkillHub(market, id, scope, name string) {
	s := &a.skillhub
	if !a.skillHubCatalogCapable() {
		s.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		s.CatalogStatusIsError = true
		return
	}
	if s.ActionBusy != "" || s.SearchBusy || s.DetailBusy || s.CatalogLoading {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		s.CatalogStatus = "技能市场和 ID 不能为空。"
		s.CatalogStatusIsError = true
		return
	}
	s.UninstallOpen = true
	s.UninstallMarket = market
	s.UninstallID = id
	s.UninstallName = strings.TrimSpace(name)
	if s.UninstallName == "" {
		s.UninstallName = id
	}
	s.UninstallScope = strings.TrimSpace(scope)
}

func (a *nativeApp) cancelUninstallSkillHub() {
	s := &a.skillhub
	s.UninstallOpen = false
	s.UninstallMarket = ""
	s.UninstallID = ""
	s.UninstallName = ""
	s.UninstallScope = ""
}

func (a *nativeApp) confirmUninstallSkillHub() {
	s := &a.skillhub
	if !s.UninstallOpen {
		return
	}
	if !a.skillHubCatalogCapable() {
		a.cancelUninstallSkillHub()
		s.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		s.CatalogStatusIsError = true
		return
	}
	market, id, name, scope := s.UninstallMarket, s.UninstallID, s.UninstallName, s.UninstallScope
	if market == "" || id == "" {
		a.cancelUninstallSkillHub()
		s.CatalogStatus = "技能市场和 ID 不能为空。"
		s.CatalogStatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		a.cancelUninstallSkillHub()
		s.CatalogStatus = "卸载技能失败：尚未连接到 MothX ACP。"
		s.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&s.CatalogSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := s.SessionID
	s.ActionBusy = "uninstall:" + id
	s.CatalogStatus = Tf("正在卸载技能 %s…", name)
	s.CatalogStatusIsError = false
	s.UninstallOpen = false
	go func() {
		raw, err := client.call("mothx/manage/skillhub/uninstall", buildSkillHubUninstallRequest(sessionID, market, id, scope))
		a.dispatch(func() {
			if !a.skillHubCatalogCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skillhub
			s.ActionBusy = ""
			if err != nil {
				s.CatalogStatus = "卸载技能失败：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			var response skillHubActionResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				s.CatalogStatus = "卸载技能响应无效：" + err.Error()
				s.CatalogStatusIsError = true
				return
			}
			a.skillHubApplySession(response.Session)
			a.skillHubRemoveInstalled(market, id)
			s.CatalogStatus = Tf("技能 %s 已卸载", name)
			s.CatalogStatusIsError = false
		})
	}()
}

// skillHubInstallStatus is the shared post-install status label. It keeps every
// dynamic fragment translatable instead of concatenating Chinese punctuation.
func skillHubInstallStatus(name string, alreadyInstalled, installed, activated bool, warnings int) string {
	var status string
	switch {
	case alreadyInstalled:
		status = Tf("技能 %s 已是最新版本", name)
	case installed:
		status = Tf("技能 %s 已安装", name)
	default:
		status = Tf("技能 %s 安装完成", name)
	}
	if activated {
		status += T("，并已在当前会话激活")
	}
	if warnings > 0 {
		status += Tf("；%d 条警告", warnings)
	}
	return status
}
