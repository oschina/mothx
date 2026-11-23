// Package skillhub owns the Native projection of the ACP SkillHub config,
// catalogue, search, install, activate, and uninstall contract.
package skillhub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/kit"

	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
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

// DefaultSort mirrors Desktop's initial SkillHub sort. Markets that do
// not support sorting (clawhub.ai) still omit sort entirely in the request.
const DefaultSort = "downloads"

type MarketView struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	SiteURL            string `json:"siteURL,omitempty"`
	APIURL             string `json:"apiURL,omitempty"`
	Enabled            bool   `json:"enabled"`
	APITokenConfigured bool   `json:"apiTokenConfigured"`
}

// ConfigView mirrors the token-free skillhub/get and /patch response.
type ConfigView struct {
	DefaultMarket       string       `json:"defaultMarket"`
	DefaultInstallScope string       `json:"defaultInstallScope"`
	OfficialHandles     []string     `json:"officialHandles"`
	Markets             []MarketView `json:"markets"`
}

// MarketDraft is the editable configuration row. Token and ClearToken
// are transient patch directives and are never hydrated from the server.
type MarketDraft struct {
	ID         string
	Name       string
	SiteURL    string
	APIURL     string
	Enabled    bool
	Token      string
	ClearToken bool
	Configured bool
}

type MarketInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	SiteURL string `json:"siteUrl"`
}

type Category struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	NameEn   string     `json:"nameEn,omitempty"`
	Children []Category `json:"children,omitempty"`
}

type CategoriesView struct {
	Categories []Category `json:"categories"`
}

type Target struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
	Label string `json:"label"`
}

type InstalledState struct {
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

type SessionState struct {
	SessionID    string   `json:"sessionId"`
	WorkDir      string   `json:"workDir,omitempty"`
	ActiveSkills []string `json:"activeSkills"`
}

type Skill struct {
	Market             string          `json:"market"`
	ID                 string          `json:"id"`
	Slug               string          `json:"slug,omitempty"`
	Name               string          `json:"name,omitempty"`
	DisplayName        string          `json:"displayName,omitempty"`
	Description        string          `json:"description,omitempty"`
	Version            string          `json:"version,omitempty"`
	Author             string          `json:"author,omitempty"`
	IconURL            string          `json:"iconUrl,omitempty"`
	Category           string          `json:"category,omitempty"`
	Tags               []string        `json:"tags,omitempty"`
	Downloads          int64           `json:"downloads,omitempty"`
	Installs           int64           `json:"installs,omitempty"`
	Stars              int64           `json:"stars,omitempty"`
	Suspicious         bool            `json:"suspicious,omitempty"`
	SecurityStatus     string          `json:"securityStatus,omitempty"`
	SecurityStatusName string          `json:"securityStatusName,omitempty"`
	Installed          *InstalledState `json:"installed,omitempty"`
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

type DownloadSource struct {
	URL      string `json:"url"`
	Kind     string `json:"kind"`
	Fallback bool   `json:"fallback,omitempty"`
}

type Detail struct {
	Skill
	Files           []File           `json:"files,omitempty"`
	DownloadSources []DownloadSource `json:"downloadSources,omitempty"`
	Readme          string           `json:"readme,omitempty"`
	SecurityReports json.RawMessage  `json:"securityReports,omitempty"`
	Evaluation      json.RawMessage  `json:"evaluation,omitempty"`
}

type SearchPage struct {
	Items      []Skill `json:"items"`
	Total      int64   `json:"total,omitempty"`
	Page       int     `json:"page,omitempty"`
	PageSize   int     `json:"pageSize,omitempty"`
	NextCursor string  `json:"nextCursor,omitempty"`
}

type MarketsView struct {
	Markets       []MarketInfo `json:"markets"`
	DefaultMarket string       `json:"defaultMarket,omitempty"`
}

type TargetsView struct {
	SessionID string   `json:"sessionId"`
	WorkDir   string   `json:"workDir,omitempty"`
	Targets   []Target `json:"targets"`
}

type InstalledView struct {
	SessionID string           `json:"sessionId"`
	WorkDir   string           `json:"workDir,omitempty"`
	Installed []InstalledState `json:"installed"`
	Session   SessionState     `json:"session"`
}

type InstallResult struct {
	Name             string   `json:"name"`
	Market           string   `json:"market"`
	Version          string   `json:"version"`
	Scope            string   `json:"scope"`
	Dir              string   `json:"dir"`
	Installed        bool     `json:"installed"`
	AlreadyInstalled bool     `json:"alreadyInstalled,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

type InstallResponse struct {
	Install   InstallResult `json:"install"`
	Activated bool          `json:"activated"`
	Session   SessionState  `json:"session"`
}

type ActionResponse struct {
	Activated   bool         `json:"activated"`
	Uninstalled bool         `json:"uninstalled"`
	Session     SessionState `json:"session"`
}

type State struct {
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
	Markets             []MarketDraft

	// Catalogue bootstrap for the active Runtime session.
	CatalogLoaded  bool
	CatalogLoading bool
	CatalogErr     string
	SessionID      string
	WorkDir        string
	MarketInfos    []MarketInfo
	Targets        []Target
	Installed      []InstalledState
	ActiveSkills   []string
	SelectedMarket string
	TargetLabel    string
	TargetPath     string
	TargetScope    string

	// Search projection.
	Query             string
	Category          string
	Categories        []Category
	CategoriesLoaded  bool
	CategoryLoading   bool
	CategoryErr       string
	Sort              string
	Official          bool
	SearchBusy        bool
	SearchErr         string
	Results           []Skill
	ResultsLoaded     bool
	ResultsQuery      string
	ResultsTotal      int64
	ResultsNextCursor string

	// Detail and action projection.
	DetailOpen           bool
	DetailBusy           bool
	Detail               *Detail
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

type Host interface {
	host.Base
	PanelVisible(page, tab string) bool
	Page() string
	CurrentSession() string
	StartNewTask()
	SkillIconElement(c *ui.Context, pal kit.Palette, skill Skill, size float32)
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: NewPanelState()}
}

func NewPanelState() State {
	return State{
		DefaultInstallScope: skillHubDefaultScope,
		Sort:                DefaultSort,
		Markets:             []MarketDraft{},
		MarketInfos:         []MarketInfo{},
		Targets:             []Target{},
		Installed:           []InstalledState{},
		ActiveSkills:        []string{},
		Results:             []Skill{},
	}
}

func (p *Panel) ConfigCapable(h host.Base) bool {
	return h.FeatureEnabled("manageSkillHub")
}

func (p *Panel) CatalogCapable(h host.Base) bool {
	return h.FeatureEnabled("manageSkillHubCatalog")
}

func (p *Panel) Capable(h Host) bool {
	return p.ConfigCapable(h) || p.CatalogCapable(h)
}

func (p *Panel) ConfigCurrent(h Host, seq, reloadSeq int64) bool {
	return h.PanelVisible("skills", "skillhub") &&
		seq == atomic.LoadInt64(&p.ConfigSeq) &&
		reloadSeq == h.ReloadSequence()
}

func (p *Panel) CatalogCurrent(h Host, seq, reloadSeq int64) bool {
	return h.PanelVisible("skills", "skillhub") &&
		seq == atomic.LoadInt64(&p.CatalogSeq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops every transient projection and makes both
// in-flight config and catalogue responses stale. Tokens typed into the form
// are discarded with the rest of the state.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.ConfigSeq, 1)
	atomic.AddInt64(&p.CatalogSeq, 1)
	p.Tab = 0
	p.ConfigLoaded = false
	p.ConfigLoading = false
	p.ConfigSaving = false
	p.ConfigErr = ""
	p.ConfigStatus = ""
	p.ConfigStatusIsError = false
	p.DefaultMarket = ""
	p.DefaultInstallScope = skillHubDefaultScope
	p.OfficialHandles = ""
	p.Markets = nil
	p.CatalogLoaded = false
	p.CatalogLoading = false
	p.CatalogErr = ""
	p.SessionID = ""
	p.WorkDir = ""
	p.MarketInfos = nil
	p.Targets = nil
	p.Installed = nil
	p.ActiveSkills = nil
	p.SelectedMarket = ""
	p.TargetLabel = ""
	p.TargetPath = ""
	p.TargetScope = ""
	p.Query = ""
	p.Category = ""
	p.Categories = nil
	p.CategoriesLoaded = false
	p.CategoryLoading = false
	p.CategoryErr = ""
	p.Official = false
	atomic.AddInt64(&p.CategorySeq, 1)
	p.SearchBusy = false
	p.SearchErr = ""
	p.Results = nil
	p.ResultsLoaded = false
	p.ResultsQuery = ""
	p.ResultsTotal = 0
	p.ResultsNextCursor = ""
	p.DetailOpen = false
	p.DetailBusy = false
	p.Detail = nil
	p.DetailErr = ""
	p.ActionBusy = ""
	p.CatalogStatus = ""
	p.CatalogStatusIsError = false
	p.Overwrite = false
	p.ActivateOnInstall = false
	p.UninstallOpen = false
	p.UninstallMarket = ""
	p.UninstallID = ""
	p.UninstallName = ""
	p.UninstallScope = ""
	p.AddMarketOpen = false
	p.AddMarketID = ""
	p.AddMarketErr = ""
}

// StartPanelLoad starts the capability-gated configuration and
// catalogue loads. Each projection is independent: a runtime that advertises
// only manageSkillHub still gets the config editor, and one that advertises
// only manageSkillHubCatalog still gets the browser.
func (p *Panel) StartPanelLoad(h Host) {
	if !h.PanelVisible("skills", "skillhub") {
		return
	}
	if !p.ConfigCapable(h) {
		if !p.ConfigLoaded {
			p.ConfigLoaded = true
			p.ConfigErr = "当前运行时不支持 SkillHub 配置管理（manageSkillHub）。"
		}
	} else if !p.ConfigLoaded && !p.ConfigLoading {
		p.StartConfigLoad(h)
	}
	if !p.CatalogCapable(h) {
		if !p.CatalogLoaded {
			p.CatalogLoaded = true
			p.CatalogErr = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		}
		return
	}
	if strings.TrimSpace(h.CurrentSession()) == "" {
		if !p.CatalogLoaded {
			p.CatalogLoaded = true
			p.CatalogErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		}
		return
	}
	if !p.CatalogLoaded && !p.CatalogLoading {
		p.StartCatalogLoad(h)
	}
}

func (p *Panel) StartConfigLoad(h Host) {
	if !h.PanelVisible("skills", "skillhub") || !p.ConfigCapable(h) {
		return
	}
	if p.ConfigLoading {
		return
	}
	seq := atomic.AddInt64(&p.ConfigSeq, 1)
	p.ConfigLoading = true
	p.ConfigErr = ""
	p.ConfigStatus = ""
	p.ConfigStatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.loadConfig(h, seq, startReloadSeq)
}

func (p *Panel) loadConfig(h Host, seq, startReloadSeq int64) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.ConfigCurrent(h, seq, startReloadSeq) {
				return
			}
			p.ConfigLoading = false
			p.ConfigErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.Call("mothx/manage/skillhub/get", map[string]any{})
	h.Dispatch(func() {
		if !p.ConfigCurrent(h, seq, startReloadSeq) {
			return
		}
		p.ConfigLoading = false
		if err != nil {
			p.ConfigErr = "读取 SkillHub 配置失败：" + err.Error()
			return
		}
		var view ConfigView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.ConfigErr = "SkillHub 配置响应无效：" + err.Error()
			return
		}
		p.ApplyConfigView(&view)
	})
}

func (p *Panel) ApplyConfigView(view *ConfigView) {
	if view == nil {
		return
	}
	defaultScope := strings.TrimSpace(view.DefaultInstallScope)
	if defaultScope != "project" && defaultScope != "global" {
		defaultScope = skillHubDefaultScope
	}
	p.DefaultMarket = strings.TrimSpace(view.DefaultMarket)
	p.DefaultInstallScope = defaultScope
	p.OfficialHandles = strings.Join(view.OfficialHandles, "\n")
	markets := make([]MarketDraft, 0, len(view.Markets))
	for _, market := range view.Markets {
		markets = append(markets, MarketDraft{
			ID:         strings.TrimSpace(market.ID),
			Name:       market.Name,
			SiteURL:    market.SiteURL,
			APIURL:     market.APIURL,
			Enabled:    market.Enabled,
			Configured: market.APITokenConfigured,
		})
	}
	p.Markets = markets
	p.ConfigLoaded = true
	p.ConfigLoading = false
	p.ConfigErr = ""
}

// ParseHandles converts the textarea (one handle per line) into the
// patch array. Blank lines are ignored, matching the Desktop textarea.
func ParseHandles(text string) []string {
	out := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func MarketIDs(markets []MarketDraft) []string {
	ids := make([]string, 0, len(markets))
	for _, market := range markets {
		if id := strings.TrimSpace(market.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// BuildConfigPatch validates the local form and projects it into the
// strict skillhub/patch envelope. A market token is submitted only when the
// user typed one and did not request a clear; otherwise the Runtime preserves
// the existing stored secret.
func BuildConfigPatch(defaultMarket, defaultScope, officialHandles string, markets []MarketDraft) (map[string]any, error) {
	defaultMarket = strings.TrimSpace(defaultMarket)
	if defaultMarket == "" {
		return nil, i18n.LocalizedError("默认市场不能为空")
	}
	defaultScope = strings.TrimSpace(defaultScope)
	if defaultScope != "project" && defaultScope != "global" {
		return nil, i18n.LocalizedError("默认安装范围必须是 project 或 global")
	}
	if len(markets) == 0 {
		return nil, i18n.LocalizedError("至少需要保留一个市场")
	}
	seen := make(map[string]bool, len(markets))
	payload := make([]map[string]any, 0, len(markets))
	for index, market := range markets {
		id := strings.TrimSpace(market.ID)
		if id == "" {
			return nil, i18n.LocalizedErrorf("第 %d 个市场缺少 ID", index+1)
		}
		if seen[id] {
			return nil, i18n.LocalizedErrorf("市场 ID %q 重复", id)
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
			return nil, i18n.LocalizedErrorf("市场 %q 不能同时提交新 Token 和清除 Token", id)
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
		"officialHandles":     ParseHandles(officialHandles),
		"markets":             payload,
	}
	return map[string]any{"patch": patch}, nil
}

func (p *Panel) SaveConfig(h Host) {
	if !p.ConfigCapable(h) {
		p.ConfigStatus = "当前运行时不支持 SkillHub 配置管理（manageSkillHub）。"
		p.ConfigStatusIsError = true
		return
	}
	if p.ConfigLoading || p.ConfigSaving {
		return
	}
	request, err := BuildConfigPatch(p.DefaultMarket, p.DefaultInstallScope, p.OfficialHandles, p.Markets)
	if err != nil {
		p.ConfigStatus = "保存失败：" + err.Error()
		p.ConfigStatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.ConfigStatus = "保存失败：尚未连接到 MothX ACP。"
		p.ConfigStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.ConfigSeq, 1)
	startReloadSeq := h.ReloadSequence()
	p.ConfigSaving = true
	p.ConfigStatus = "正在保存 SkillHub 配置…"
	p.ConfigStatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/patch", request)
		h.Dispatch(func() {
			if !p.ConfigCurrent(h, seq, startReloadSeq) {
				return
			}
			p.ConfigSaving = false
			if err != nil {
				p.ConfigStatus = "保存 SkillHub 配置失败：" + err.Error()
				p.ConfigStatusIsError = true
				return
			}
			var view ConfigView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.ConfigStatus = "保存后 SkillHub 配置响应无效：" + err.Error()
				p.ConfigStatusIsError = true
				return
			}
			p.ApplyConfigView(&view)
			p.ConfigStatus = "SkillHub 配置已保存"
			p.ConfigStatusIsError = false
		})
	}()
}

func (p *Panel) OpenAddMarket(h Host) {
	if !p.ConfigCapable(h) {
		return
	}
	p.AddMarketOpen = true
	p.AddMarketID = ""
	p.AddMarketErr = ""
}

func (p *Panel) CloseAddMarket() {
	p.AddMarketOpen = false
	p.AddMarketID = ""
	p.AddMarketErr = ""
}

func (p *Panel) ConfirmAddMarket() {
	id := strings.TrimSpace(p.AddMarketID)
	if id == "" {
		p.AddMarketErr = "市场 ID 不能为空"
		return
	}
	for _, market := range p.Markets {
		if strings.TrimSpace(market.ID) == id {
			p.AddMarketErr = i18n.Tf("市场 ID %q 已存在", id)
			return
		}
	}
	p.Markets = append(p.Markets, MarketDraft{ID: id, Enabled: true})
	p.AddMarketOpen = false
	p.AddMarketID = ""
	p.AddMarketErr = ""
}

func (p *Panel) RemoveMarket(index int) {
	if index < 0 || index >= len(p.Markets) {
		return
	}
	p.Markets = append(p.Markets[:index], p.Markets[index+1:]...)
}

func (p *Panel) StartCatalogLoad(h Host) {
	if !h.PanelVisible("skills", "skillhub") || !p.CatalogCapable(h) {
		return
	}
	if p.CatalogLoading {
		return
	}
	sessionID := strings.TrimSpace(h.CurrentSession())
	if sessionID == "" {
		p.CatalogLoaded = true
		p.CatalogErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		return
	}
	seq := atomic.AddInt64(&p.CatalogSeq, 1)
	p.CatalogLoading = true
	p.CatalogErr = ""
	p.CatalogStatus = ""
	p.CatalogStatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.loadCatalog(h, seq, startReloadSeq, sessionID)
}

func Call(client *acp.Client, method string, params map[string]any, target any) error {
	raw, err := client.Call(method, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return i18n.LocalizedErrorf("%s 响应无效：%w", strings.TrimPrefix(method, "mothx/manage/skillhub/"), err)
	}
	return nil
}

func (p *Panel) loadCatalog(h Host, seq, startReloadSeq int64, sessionID string) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) || h.CurrentSession() != sessionID {
				return
			}
			p.CatalogLoading = false
			p.CatalogErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	var markets MarketsView
	if err := Call(client, "mothx/manage/skillhub/markets", BuildMarketsRequest(sessionID), &markets); err != nil {
		p.CatalogFailed(h, seq, startReloadSeq, sessionID, "读取市场列表失败："+err.Error())
		return
	}
	var targets TargetsView
	if err := Call(client, "mothx/manage/skillhub/targets", BuildTargetsRequest(sessionID), &targets); err != nil {
		p.CatalogFailed(h, seq, startReloadSeq, sessionID, "读取安装目录失败："+err.Error())
		return
	}
	var installed InstalledView
	if err := Call(client, "mothx/manage/skillhub/installed", BuildInstalledRequest(sessionID), &installed); err != nil {
		p.CatalogFailed(h, seq, startReloadSeq, sessionID, "读取已安装技能失败："+err.Error())
		return
	}
	h.Dispatch(func() {
		if !p.CatalogCurrent(h, seq, startReloadSeq) || h.CurrentSession() != sessionID {
			return
		}
		p.ApplyCatalogViews(h, sessionID, &markets, &targets, &installed)
	})
}

func (p *Panel) CatalogFailed(h Host, seq, startReloadSeq int64, sessionID, message string) {
	h.Dispatch(func() {
		if !p.CatalogCurrent(h, seq, startReloadSeq) || h.CurrentSession() != sessionID {
			return
		}
		p.CatalogLoading = false
		p.CatalogErr = message
	})
}

func (p *Panel) ApplyCatalogViews(h Host, sessionID string, markets *MarketsView, targets *TargetsView, installed *InstalledView) {
	if markets == nil || targets == nil || installed == nil {
		return
	}
	p.SessionID = sessionID
	if strings.TrimSpace(markets.DefaultMarket) != "" {
		p.SelectedMarket = ChooseMarket(p.SelectedMarket, p.DefaultMarket, markets.DefaultMarket, markets.Markets)
	} else {
		p.SelectedMarket = ChooseMarket(p.SelectedMarket, p.DefaultMarket, "", markets.Markets)
	}
	p.MarketInfos = append([]MarketInfo(nil), markets.Markets...)
	if p.MarketInfos == nil {
		p.MarketInfos = []MarketInfo{}
	}
	p.Targets = append([]Target(nil), targets.Targets...)
	if p.Targets == nil {
		p.Targets = []Target{}
	}
	if workDir := strings.TrimSpace(targets.WorkDir); workDir != "" {
		p.WorkDir = workDir
	} else {
		p.WorkDir = strings.TrimSpace(installed.WorkDir)
	}
	p.Installed = append([]InstalledState(nil), installed.Installed...)
	if p.Installed == nil {
		p.Installed = []InstalledState{}
	}
	p.ActiveSkills = append([]string(nil), installed.Session.ActiveSkills...)
	if p.ActiveSkills == nil {
		p.ActiveSkills = []string{}
	}
	p.RefreshActiveFlags()

	label, path, scope := ChooseTarget(p.TargetLabel, p.DefaultInstallScope, p.Targets)
	p.TargetLabel, p.TargetPath, p.TargetScope = label, path, scope
	p.ApplyInstalledToResults()
	p.CatalogLoaded = true
	p.CatalogLoading = false
	p.CatalogErr = ""
	if !p.CategoriesLoaded {
		p.LoadCategories(h)
	}
}

// ChooseMarket prefers the current selection, then the user's saved
// default, then the ACP-projected default, then the first available market.
func ChooseMarket(current, configDefault, projectedDefault string, markets []MarketInfo) string {
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

func TargetLabels(targets []Target) []string {
	labels := make([]string, 0, len(targets))
	for _, target := range targets {
		label := strings.TrimSpace(target.Label)
		if label == "" {
			label = i18n.T("技能目录")
		}
		labels = append(labels, fmt.Sprintf("%s · %s · %s", label, target.Scope, target.Path))
	}
	return labels
}

func TargetLabel(target Target) string {
	label := strings.TrimSpace(target.Label)
	if label == "" {
		label = "技能目录"
	}
	return fmt.Sprintf("%s · %s · %s", label, target.Scope, target.Path)
}

func TargetByLabel(targets []Target, label string) (Target, bool) {
	for _, target := range targets {
		if TargetLabel(target) == label {
			return target, true
		}
	}
	return Target{}, false
}

// ChooseTarget keeps the current path when it is still advertised,
// otherwise picks a target in the configured install scope, then the first.
func ChooseTarget(currentLabel, scope string, targets []Target) (label, path, targetScope string) {
	for _, target := range targets {
		if strings.TrimSpace(target.Path) != "" && TargetLabel(target) == currentLabel {
			return TargetLabel(target), target.Path, target.Scope
		}
	}
	for _, target := range targets {
		if target.Scope == scope && strings.TrimSpace(target.Path) != "" {
			return TargetLabel(target), target.Path, target.Scope
		}
	}
	for _, target := range targets {
		if strings.TrimSpace(target.Path) != "" {
			return TargetLabel(target), target.Path, target.Scope
		}
	}
	return "", "", ""
}

func OnMarketChanged(p *State) {
	atomic.AddInt64(&p.CategorySeq, 1)
	p.Category = ""
	p.Categories = nil
	p.CategoriesLoaded = false
	p.CategoryLoading = false
	p.CategoryErr = ""
	p.Sort = DefaultSort
	p.Official = false
	p.Results = []Skill{}
	p.ResultsLoaded = false
	p.ResultsQuery = ""
	p.ResultsTotal = 0
	p.ResultsNextCursor = ""
	p.SearchErr = ""
}

func OnTargetChanged(p *State) {
	if target, ok := TargetByLabel(p.Targets, p.TargetLabel); ok {
		p.TargetPath = target.Path
		p.TargetScope = target.Scope
	}
}

func BuildMarketsRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func BuildTargetsRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func BuildInstalledRequest(sessionID string) map[string]any {
	return map[string]any{"sessionId": strings.TrimSpace(sessionID)}
}

func SearchSort(market, sort string) string {
	if strings.EqualFold(strings.TrimSpace(market), "clawhub.ai") {
		return ""
	}
	switch strings.TrimSpace(sort) {
	case "stars", "updated_at":
		return sort
	default:
		return DefaultSort
	}
}

func BuildSearchRequest(sessionID, market, query, category, sort string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"query":     strings.TrimSpace(query),
		"category":  strings.TrimSpace(category),
		"sort":      SearchSort(market, sort),
		"order":     "desc",
		"page":      1,
		"limit":     20,
	}
}

func BuildOfficialRequest(sessionID, market, query string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"query":     strings.TrimSpace(query),
		"page":      1,
		"limit":     20,
	}
}

func BuildDetailRequest(sessionID, market, id string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"id":        strings.TrimSpace(id),
	}
}

func BuildInstallRequest(sessionID, market, id, version, scope, targetDir string, overwrite, activate bool) map[string]any {
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

func BuildActivateRequest(sessionID, id string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"id":        strings.TrimSpace(id),
	}
}

func BuildUninstallRequest(sessionID, market, id, scope string) map[string]any {
	return map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"market":    strings.TrimSpace(market),
		"id":        strings.TrimSpace(id),
		"scope":     strings.TrimSpace(scope),
	}
}

func Title(skill Skill) string {
	for _, value := range []string{skill.DisplayName, skill.Name, skill.Slug, skill.ID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return i18n.T("未命名技能")
}

func InstalledName(skill Skill) string {
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

func Active(active []string, name string) bool {
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

func (p *Panel) RefreshActiveFlags() {
	for index := range p.Installed {
		if p.Installed[index].Name != "" {
			p.Installed[index].Active = Active(p.ActiveSkills, p.Installed[index].Name)
		}
	}
}

func FindInstalled(installed []InstalledState, market, id string) *InstalledState {
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

func (p *Panel) ApplyInstalledToResults() {
	for index := range p.Results {
		if state := FindInstalled(p.Installed, p.Results[index].Market, p.Results[index].ID); state != nil {
			state.Active = Active(p.ActiveSkills, InstalledName(p.Results[index]))
			state.UpdateAvailable = VersionDiffers(p.Results[index].Version, state.Version)
			p.Results[index].Installed = state
		} else {
			p.Results[index].Installed = nil
		}
	}
	if p.Detail != nil {
		if state := FindInstalled(p.Installed, p.Detail.Market, p.Detail.ID); state != nil {
			state.Active = Active(p.ActiveSkills, InstalledName(p.Detail.Skill))
			state.UpdateAvailable = VersionDiffers(p.Detail.Version, state.Version)
			p.Detail.Installed = state
		} else {
			p.Detail.Installed = nil
		}
	}
}

func VersionDiffers(remote, installed string) bool {
	remote = strings.TrimSpace(remote)
	installed = strings.TrimSpace(installed)
	return remote != "" && installed != "" && remote != installed
}

func (p *Panel) ApplySession(session SessionState) {
	if strings.TrimSpace(session.SessionID) != "" {
		p.SessionID = strings.TrimSpace(session.SessionID)
	}
	if strings.TrimSpace(session.WorkDir) != "" {
		p.WorkDir = strings.TrimSpace(session.WorkDir)
	}
	p.ActiveSkills = append([]string(nil), session.ActiveSkills...)
	if p.ActiveSkills == nil {
		p.ActiveSkills = []string{}
	}
	sort.Strings(p.ActiveSkills)
	p.RefreshActiveFlags()
	p.ApplyInstalledToResults()
}

func (p *Panel) MarkInstalled(market, id string, result InstallResult) {
	state := InstalledState{
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
		state.Scope = p.TargetScope
	}
	state.Active = Active(p.ActiveSkills, state.Name)
	replaced := false
	for index := range p.Installed {
		if strings.TrimSpace(p.Installed[index].Market) == state.Market && strings.TrimSpace(p.Installed[index].ID) == state.ID {
			p.Installed[index] = state
			replaced = true
			break
		}
	}
	if !replaced {
		p.Installed = append(p.Installed, state)
	}
	p.ApplyInstalledToResults()
}

func (p *Panel) RemoveInstalled(market, id string) {
	filtered := make([]InstalledState, 0, len(p.Installed))
	for _, entry := range p.Installed {
		if strings.TrimSpace(entry.Market) == strings.TrimSpace(market) && strings.TrimSpace(entry.ID) == strings.TrimSpace(id) {
			continue
		}
		filtered = append(filtered, entry)
	}
	p.Installed = filtered
	p.ApplyInstalledToResults()
}

func (p *Panel) BrowseBusy() bool {
	return p.CatalogLoading || p.SearchBusy || p.ActionBusy != "" || p.DetailBusy
}

func CategoryName(category Category) string {
	for _, value := range []string{category.Name, category.NameEn, category.Key} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return i18n.T("未命名分类")
}

func FlattenCategories(categories []Category) []Category {
	out := make([]Category, 0, len(categories))
	for _, category := range categories {
		out = append(out, category)
		out = append(out, FlattenCategories(category.Children)...)
	}
	return out
}

// LoadCategories projects the selected market's category tree. It is
// optional: a market without category support leaves the selector hidden.
func (p *Panel) LoadCategories(h Host) {
	if !p.CatalogCapable(h) || p.CategoryLoading || p.SessionID == "" {
		return
	}
	if strings.TrimSpace(p.SelectedMarket) == "" {
		return
	}
	client := h.ACP()
	if client == nil {
		p.CategoriesLoaded = true
		return
	}
	seq := atomic.AddInt64(&p.CategorySeq, 1)
	startReloadSeq := h.ReloadSequence()
	sessionID := p.SessionID
	market := p.SelectedMarket
	p.CategoryLoading = true
	p.CategoryErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/categories", map[string]any{
			"sessionId": sessionID,
			"market":    market,
		})
		h.Dispatch(func() {
			if seq != atomic.LoadInt64(&p.CategorySeq) || startReloadSeq != h.ReloadSequence() ||
				h.CurrentSession() != sessionID || p.SelectedMarket != market {
				return
			}
			p.CategoryLoading = false
			p.CategoriesLoaded = true
			if err != nil {
				p.CategoryErr = "读取技能分类失败：" + err.Error()
				return
			}
			var view CategoriesView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.CategoryErr = "技能分类响应无效：" + err.Error()
				return
			}
			p.Categories = view.Categories
			p.CategoryErr = ""
		})
	}()
}

// Search searches the selected market. An empty query is a valid
// browse request and uses the market's download ordering.
func (p *Panel) Search(h Host) {
	if !p.CatalogCapable(h) {
		p.SearchErr = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		return
	}
	if strings.TrimSpace(p.SessionID) == "" || h.CurrentSession() == "" {
		p.SearchErr = "SkillHub 市场需要活动会话，请先在对话中打开或新建会话。"
		return
	}
	if p.CatalogLoading || p.SearchBusy || p.ActionBusy != "" || p.DetailBusy {
		return
	}
	market := strings.TrimSpace(p.SelectedMarket)
	if market == "" {
		p.SearchErr = "请选择技能市场。"
		return
	}
	client := h.ACP()
	if client == nil {
		p.SearchErr = "搜索失败：尚未连接到 MothX ACP。"
		return
	}
	query := strings.TrimSpace(p.Query)
	sessionID := p.SessionID
	seq := atomic.AddInt64(&p.CatalogSeq, 1)
	startReloadSeq := h.ReloadSequence()
	p.SearchBusy = true
	p.SearchErr = ""
	p.CatalogStatus = ""
	p.CatalogStatusIsError = false
	method := "mothx/manage/skillhub/search"
	request := BuildSearchRequest(sessionID, market, query, p.Category, p.Sort)
	if p.Official {
		method = "mothx/manage/skillhub/official"
		request = BuildOfficialRequest(sessionID, market, query)
	}
	go func() {
		raw, err := client.Call(method, request)
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) {
				return
			}
			p.SearchBusy = false
			if err != nil {
				p.SearchErr = "搜索技能失败：" + err.Error()
				return
			}
			var page SearchPage
			if err := json.Unmarshal(raw, &page); err != nil {
				p.SearchErr = "技能搜索响应无效：" + err.Error()
				return
			}
			p.Results = append([]Skill(nil), page.Items...)
			if p.Results == nil {
				p.Results = []Skill{}
			}
			p.ResultsLoaded = true
			p.ResultsQuery = query
			p.ResultsTotal = page.Total
			p.ResultsNextCursor = page.NextCursor
			p.ApplyInstalledToResults()
		})
	}()
}

func (p *Panel) RefreshCatalog(h Host) {
	if p.BrowseBusy() {
		return
	}
	p.CatalogLoaded = false
	p.CatalogErr = ""
	p.StartPanelLoad(h)
}

func (p *Panel) OpenDetail(h Host, market, id string) {
	if !p.CatalogCapable(h) || p.DetailBusy || p.ActionBusy != "" || p.SearchBusy {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		p.CatalogStatus = "技能市场和 ID 不能为空。"
		p.CatalogStatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.CatalogStatus = "读取技能详情失败：尚未连接到 MothX ACP。"
		p.CatalogStatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&p.CatalogSeq)
	startReloadSeq := h.ReloadSequence()
	sessionID := p.SessionID
	p.DetailOpen = true
	p.DetailBusy = true
	p.Detail = nil
	p.DetailErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/detail", BuildDetailRequest(sessionID, market, id))
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) {
				return
			}
			p.DetailBusy = false
			if err != nil {
				p.DetailErr = "读取技能详情失败：" + err.Error()
				return
			}
			var detail Detail
			if err := json.Unmarshal(raw, &detail); err != nil {
				p.DetailErr = "技能详情响应无效：" + err.Error()
				return
			}
			if detail.Market == "" {
				detail.Market = market
			}
			if detail.ID == "" {
				detail.ID = id
			}
			if state := FindInstalled(p.Installed, detail.Market, detail.ID); state != nil {
				state.Active = Active(p.ActiveSkills, InstalledName(detail.Skill))
				detail.Installed = state
			}
			p.Detail = &detail
			p.DetailErr = ""
		})
	}()
}

func (p *Panel) CloseDetail() {
	p.DetailOpen = false
	p.DetailBusy = false
	p.Detail = nil
	p.DetailErr = ""
}

func (p *Panel) Install(h Host, market, id, version string) {
	p.InstallWithOverwrite(h, market, id, version, p.Overwrite)
}

// InstallWithOverwrite lets the explicit "update" action request an
// overwrite without mutating the user's persistent form choice.
func (p *Panel) InstallWithOverwrite(h Host, market, id, version string, overwrite bool) {
	if !p.CatalogCapable(h) {
		p.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		p.CatalogStatusIsError = true
		return
	}
	if p.CatalogLoading || p.SearchBusy || p.ActionBusy != "" || p.DetailBusy {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		p.CatalogStatus = "技能市场和 ID 不能为空。"
		p.CatalogStatusIsError = true
		return
	}
	if strings.TrimSpace(p.TargetPath) == "" {
		p.CatalogStatus = "请选择安装目录。"
		p.CatalogStatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.CatalogStatus = "安装技能失败：尚未连接到 MothX ACP。"
		p.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.CatalogSeq, 1)
	startReloadSeq := h.ReloadSequence()
	sessionID := p.SessionID
	targetScope := p.TargetScope
	targetPath := p.TargetPath
	activate := p.ActivateOnInstall
	p.ActionBusy = "install:" + id
	p.CatalogStatus = i18n.Tf("正在安装技能 %s…", id)
	p.CatalogStatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/install", BuildInstallRequest(
			sessionID, market, id, version, targetScope, targetPath, overwrite, activate))
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) {
				return
			}
			p.ActionBusy = ""
			if err != nil {
				p.CatalogStatus = "安装技能失败：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			var response InstallResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				p.CatalogStatus = "安装技能响应无效：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			p.ApplySession(response.Session)
			p.MarkInstalled(market, id, response.Install)
			p.DetailOpen = false
			p.Detail = nil
			p.DetailErr = ""
			name := strings.TrimSpace(response.Install.Name)
			if name == "" {
				name = id
			}
			p.CatalogStatus = InstallStatus(
				name,
				response.Install.AlreadyInstalled,
				response.Install.Installed,
				response.Activated,
				len(response.Install.Warnings),
			)
			p.CatalogStatusIsError = false
		})
	}()
}

func (p *Panel) Activate(h Host, market, id, name string) {
	if !p.CatalogCapable(h) {
		p.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		p.CatalogStatusIsError = true
		return
	}
	if p.CatalogLoading || p.SearchBusy || p.ActionBusy != "" || p.DetailBusy {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		p.CatalogStatus = "要激活的技能名称不能为空。"
		p.CatalogStatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.CatalogStatus = "激活技能失败：尚未连接到 MothX ACP。"
		p.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.CatalogSeq, 1)
	startReloadSeq := h.ReloadSequence()
	sessionID := p.SessionID
	p.ActionBusy = "activate:" + name
	p.CatalogStatus = i18n.Tf("正在激活技能 %s…", name)
	p.CatalogStatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/activate", BuildActivateRequest(sessionID, name))
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) {
				return
			}
			p.ActionBusy = ""
			if err != nil {
				p.CatalogStatus = "激活技能失败：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			var response ActionResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				p.CatalogStatus = "激活技能响应无效：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			p.ApplySession(response.Session)
			p.CatalogStatus = i18n.Tf("技能 %s 已在当前会话激活", name)
			p.CatalogStatusIsError = false
		})
	}()
}

func (p *Panel) RequestUninstall(h Host, market, id, scope, name string) {
	if !p.CatalogCapable(h) {
		p.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		p.CatalogStatusIsError = true
		return
	}
	if p.ActionBusy != "" || p.SearchBusy || p.DetailBusy || p.CatalogLoading {
		return
	}
	market, id = strings.TrimSpace(market), strings.TrimSpace(id)
	if market == "" || id == "" {
		p.CatalogStatus = "技能市场和 ID 不能为空。"
		p.CatalogStatusIsError = true
		return
	}
	p.UninstallOpen = true
	p.UninstallMarket = market
	p.UninstallID = id
	p.UninstallName = strings.TrimSpace(name)
	if p.UninstallName == "" {
		p.UninstallName = id
	}
	p.UninstallScope = strings.TrimSpace(scope)
}

func (p *Panel) CancelUninstall() {
	p.UninstallOpen = false
	p.UninstallMarket = ""
	p.UninstallID = ""
	p.UninstallName = ""
	p.UninstallScope = ""
}

func (p *Panel) ConfirmUninstall(h Host) {
	if !p.UninstallOpen {
		return
	}
	if !p.CatalogCapable(h) {
		p.CancelUninstall()
		p.CatalogStatus = "当前运行时不支持 SkillHub 市场目录（manageSkillHubCatalog）。"
		p.CatalogStatusIsError = true
		return
	}
	market, id, name, scope := p.UninstallMarket, p.UninstallID, p.UninstallName, p.UninstallScope
	if market == "" || id == "" {
		p.CancelUninstall()
		p.CatalogStatus = "技能市场和 ID 不能为空。"
		p.CatalogStatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.CancelUninstall()
		p.CatalogStatus = "卸载技能失败：尚未连接到 MothX ACP。"
		p.CatalogStatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.CatalogSeq, 1)
	startReloadSeq := h.ReloadSequence()
	sessionID := p.SessionID
	p.ActionBusy = "uninstall:" + id
	p.CatalogStatus = i18n.Tf("正在卸载技能 %s…", name)
	p.CatalogStatusIsError = false
	p.UninstallOpen = false
	go func() {
		raw, err := client.Call("mothx/manage/skillhub/uninstall", BuildUninstallRequest(sessionID, market, id, scope))
		h.Dispatch(func() {
			if !p.CatalogCurrent(h, seq, startReloadSeq) {
				return
			}
			p.ActionBusy = ""
			if err != nil {
				p.CatalogStatus = "卸载技能失败：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			var response ActionResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				p.CatalogStatus = "卸载技能响应无效：" + err.Error()
				p.CatalogStatusIsError = true
				return
			}
			p.ApplySession(response.Session)
			p.RemoveInstalled(market, id)
			p.CatalogStatus = i18n.Tf("技能 %s 已卸载", name)
			p.CatalogStatusIsError = false
		})
	}()
}

// InstallStatus is the shared post-install status label. It keeps every
// dynamic fragment translatable instead of concatenating Chinese punctuation.
func InstallStatus(name string, alreadyInstalled, installed, activated bool, warnings int) string {
	var status string
	switch {
	case alreadyInstalled:
		status = i18n.Tf("技能 %s 已是最新版本", name)
	case installed:
		status = i18n.Tf("技能 %s 已安装", name)
	default:
		status = i18n.Tf("技能 %s 安装完成", name)
	}
	if activated {
		status += i18n.T("，并已在当前会话激活")
	}
	if warnings > 0 {
		status += i18n.Tf("；%d 条警告", warnings)
	}
	return status
}
