// Package knowledge owns the Native projection of the ACP knowledge-base
// management contract (mothx/manage/knowledge-bases/*).
package knowledge

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/feature/mcp"
	"github.com/oschina/mothx/native/internal/feature/providers"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP knowledge-base management
// contract. Knowledge configuration, snapshots, runs, sources, schedules, and
// the standard Knowledge MCP server are all Runtime-owned; this panel only
// projects them through ACP and never reads the source directory or the
// per-knowledge-base SQLite store.

const (
	PollInterval = 3 * time.Second
	NewDraftKey  = "new"
)

var (
	Profiles = []string{"documents", "code", "notes", "mixed"}
	Modes    = []string{"yolo", "agent", "plan", "os"}
)

// Base mirrors session.KnowledgeBase as projected by ACP.
type Base struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	RootDir           string   `json:"rootDir"`
	PreprocessProfile string   `json:"preprocessProfile"`
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	Mode              string   `json:"mode"`
	ThinkingLevel     string   `json:"thinkingLevel,omitempty"`
	Schedule          string   `json:"schedule"`
	Enabled           bool     `json:"enabled"`
	IgnoreGlobs       []string `json:"ignoreGlobs,omitempty"`
	ActiveSnapshotID  string   `json:"activeSnapshotId,omitempty"`
	CreatedAt         string   `json:"createdAt,omitempty"`
	UpdatedAt         string   `json:"updatedAt,omitempty"`
}

// Snapshot mirrors session.KnowledgeSnapshot. Timestamps stay strings
// so the Native UI never has to interpret Runtime time semantics.
type Snapshot struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	RunID        string `json:"runId,omitempty"`
	SchemaVer    int    `json:"schemaVersion,omitempty"`
	FileCount    int    `json:"fileCount"`
	ChunkCount   int    `json:"chunkCount"`
	NodeCount    int    `json:"nodeCount"`
	EdgeCount    int    `json:"edgeCount"`
	StartedAt    string `json:"startedAt,omitempty"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	ErrorSummary string `json:"errorSummary,omitempty"`
}

// IndexProgress mirrors the live background scan projection. Hosts
// poll list/status while Running is true instead of blocking on scan.
type IndexProgress struct {
	Running    bool   `json:"running"`
	Phase      string `json:"phase,omitempty"`
	FilesTotal int64  `json:"filesTotal"`
	FilesDone  int64  `json:"filesDone"`
	Chunks     int64  `json:"chunks"`
	StartedAt  string `json:"startedAt,omitempty"`
	RunID      string `json:"runId,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BaseView mirrors manageKnowledgeBaseView.
type BaseView struct {
	KnowledgeBase Base           `json:"knowledgeBase"`
	Snapshot      *Snapshot      `json:"snapshot"`
	Status        string         `json:"status"`
	Indexing      *IndexProgress `json:"indexing,omitempty"`
}

type ListView struct {
	KnowledgeBases []BaseView `json:"knowledgeBases"`
}

type ScanResult struct {
	Started        bool           `json:"started"`
	AlreadyRunning bool           `json:"alreadyRunning"`
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	Indexing       *IndexProgress `json:"indexing,omitempty"`
}

// IndexRunView mirrors agentruntime.KnowledgeIndexRun.
type IndexRunView struct {
	RunID        string `json:"runId"`
	SnapshotID   string `json:"snapshotId,omitempty"`
	Status       string `json:"status"`
	StartedAt    string `json:"startedAt,omitempty"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorSummary string `json:"errorSummary,omitempty"`
	FileCount    int    `json:"fileCount"`
	ChunkCount   int    `json:"chunkCount"`
	NodeCount    int    `json:"nodeCount"`
	EdgeCount    int    `json:"edgeCount"`
	Active       bool   `json:"active"`
}

type RunsView struct {
	Runs []IndexRunView `json:"runs"`
}

// SourceView mirrors session.KnowledgeSource: file-level provenance
// only, never file contents.
type SourceView struct {
	RelativePath  string `json:"relativePath"`
	Title         string `json:"title,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	Status        string `json:"status"`
	ByteSize      int64  `json:"byteSize"`
	ChunkCount    int    `json:"chunkCount"`
	ContentSHA256 string `json:"contentSHA256,omitempty"`
}

type SourcesView struct {
	Sources []SourceView `json:"sources"`
}

// ScheduleView mirrors the schedule projection returned by
// mothx/manage/knowledge-bases/schedule.
type ScheduleView struct {
	ID            string `json:"id"`
	Enabled       bool   `json:"enabled"`
	Schedule      string `json:"schedule"`
	Configured    bool   `json:"configured"`
	RootAvailable bool   `json:"rootAvailable"`
	NextRun       string `json:"nextRun,omitempty"`
	LastRun       string `json:"lastRun,omitempty"`
	LastStatus    string `json:"lastStatus,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	RunCount      int    `json:"runCount,omitempty"`
}

type MCPApplyResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type Chunk struct {
	ID           string `json:"id"`
	SnapshotID   string `json:"snapshotId"`
	FileID       string `json:"fileId"`
	RelativePath string `json:"relativePath,omitempty"`
	Ordinal      int    `json:"ordinal"`
	Text         string `json:"text"`
	StartLine    int    `json:"startLine"`
	EndLine      int    `json:"endLine"`
}

type Node struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Summary    string  `json:"summary,omitempty"`
	Status     string  `json:"status,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type GraphQuery struct {
	Snapshot    Snapshot          `json:"snapshot"`
	Chunks      []Chunk           `json:"chunks"`
	Nodes       []Node            `json:"nodes"`
	Edges       []json.RawMessage `json:"edges"`
	Evidence    []json.RawMessage `json:"evidence"`
	Truncated   bool              `json:"truncated"`
	Uncertainty []json.RawMessage `json:"uncertainties"`
}

type QueryResultView struct {
	Query GraphQuery `json:"query"`
}

// SettingsView is the safe subset of mothx/manage/settings/get used
// for knowledge-base defaults. It contains no credentials.
type SettingsView struct {
	DefaultProvider string              `json:"defaultProvider"`
	DefaultModel    string              `json:"defaultModel"`
	DefaultMode     string              `json:"defaultMode"`
	ThinkingLevel   string              `json:"thinkingLevel"`
	Providers       []providers.Summary `json:"providers"`
}

// BaseDefaults is the create-form seed derived from the advertised
// settings/provider ACP projections.
type BaseDefaults struct {
	Provider      string
	Model         string
	Mode          string
	ThinkingLevel string
	Providers     []string
	Models        []providers.CatalogModel
}

// BaseDraft is the editable, non-secret configuration of one
// knowledge base. IgnoreGlobs is preserved when an existing config carries it
// even though the first Desktop/native form does not expose a dedicated field.
type BaseDraft struct {
	Name              string
	RootDir           string
	PreprocessProfile string
	Provider          string
	Model             string
	Mode              string
	ThinkingLevel     string
	Schedule          string
	Enabled           bool
	IgnoreGlobs       []string
}

// InsightsState projects the additive run/source tabs and the debug
// query preview for one knowledge base. None of these facts are persisted
// locally; each tab is loaded through ACP and feature-gated independently.
type InsightsState struct {
	Runs          []IndexRunView
	Sources       []SourceView
	RunsLoaded    bool
	SourcesLoaded bool
	Loading       bool
	Err           string
	Tab           int

	Query       string
	QueryResult *GraphQuery
	QueryBusy   bool
	QueryErr    string

	RunDetail     *IndexRunView
	RunDetailID   string
	RunDetailBusy bool
	RunDetailErr  string
}

// InsightsData is the loaded form returned from the ACP worker.
type InsightsData struct {
	Runs          []IndexRunView
	Sources       []SourceView
	RunsLoaded    bool
	SourcesLoaded bool
	Err           string
}

type State struct {
	Views    []BaseView
	Defaults *BaseDefaults
	Loaded   bool
	Loading  bool
	Err      string

	Status        string
	StatusIsError bool

	Creating   bool
	Drafts     map[string]*BaseDraft
	Insights   map[string]*InsightsState
	Schedules  map[string]ScheduleView
	MCPServers []mcp.ServerView
	Busy       map[string]string

	PendingDeleteID   string
	PendingDeleteName string
	PendingDeleteRoot string
	PendingDeleteOpen bool
	PendingClearID    string
	PendingClearName  string
	PendingClearOpen  bool

	Polling bool
	PollSeq int64

	// Seq guards every asynchronous knowledge call. Bumping it invalidates any
	// in-flight list/load/mutation response, which is how tab exits, reconnects,
	// and explicit invalidation prevent stale state from landing.
	Seq int64
}

type Host interface {
	host.Base
	PanelVisible(page, tab string) bool
	Page() string
	CurrentSession() string
	OpenDirectory(title string, onPick func(string))
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: NewPanelState()}
}

func NewPanelState() State {
	return State{
		Drafts:    make(map[string]*BaseDraft),
		Insights:  make(map[string]*InsightsState),
		Schedules: make(map[string]ScheduleView),
		Busy:      make(map[string]string),
	}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageKnowledgeBases")
}

func (p *Panel) RunsCapable(h host.Base) bool {
	return h.FeatureEnabled("knowledgeRuns")
}

func (p *Panel) SourcesCapable(h host.Base) bool {
	return h.FeatureEnabled("knowledgeSources")
}

func (p *Panel) GraphCapable(h host.Base) bool {
	return h.FeatureEnabled("knowledgeGraphIndex")
}

func (p *Panel) MCPCapable(h host.Base) bool {
	return h.FeatureEnabled("manageMcp")
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.PanelVisible("library", "knowledge") &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight
// knowledge response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	atomic.AddInt64(&p.PollSeq, 1)
	p.Views = nil
	p.Defaults = nil
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	p.Creating = false
	p.Drafts = make(map[string]*BaseDraft)
	p.Insights = make(map[string]*InsightsState)
	p.Schedules = make(map[string]ScheduleView)
	p.MCPServers = nil
	p.Busy = make(map[string]string)
	p.PendingDeleteID = ""
	p.PendingDeleteName = ""
	p.PendingDeleteRoot = ""
	p.PendingDeleteOpen = false
	p.PendingClearID = ""
	p.PendingClearName = ""
	p.PendingClearOpen = false
	p.Polling = false
}

// StartLoad starts one capability-gated load. It is called from
// the UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.PanelVisible("library", "knowledge") {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持知识库管理（manageKnowledgeBases）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	atomic.AddInt64(&p.PollSeq, 1)
	p.Polling = false
	p.Loading = true
	p.Err = ""
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq)
}

func (p *Panel) load(h Host, seq, startReloadSeq int64) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.Call("mothx/manage/knowledge-bases/list", map[string]any{})
	if err != nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "读取知识库列表失败：" + err.Error()
		})
		return
	}
	var list ListView
	if err := json.Unmarshal(raw, &list); err != nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "知识库列表响应无效：" + err.Error()
		})
		return
	}

	defaults := p.loadDefaults(h, client)
	var servers []mcp.ServerView
	if p.MCPCapable(h) {
		servers = p.loadMCPServers(client)
	}
	schedules := make(map[string]ScheduleView, len(list.KnowledgeBases))
	insights := make(map[string]InsightsData, len(list.KnowledgeBases))
	for _, view := range list.KnowledgeBases {
		id := strings.TrimSpace(view.KnowledgeBase.ID)
		if id == "" {
			continue
		}
		if schedule, ok := p.loadSchedule(client, id); ok {
			schedules[id] = schedule
		}
		if p.RunsCapable(h) || p.SourcesCapable(h) {
			insights[id] = p.loadInsights(h, client, id)
		}
	}

	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		p.ApplyPanel(list.KnowledgeBases, defaults, servers, schedules, insights)
		p.Err = ""
		p.StartPolling(h)
	})
}

func (p *Panel) loadDefaults(h Host, client *acp.Client) *BaseDefaults {
	defaults := &BaseDefaults{Mode: "yolo"}
	var settings *SettingsView
	if h.FeatureEnabled("manageSettings") {
		if raw, err := client.Call("mothx/manage/settings/get", map[string]any{}); err == nil {
			var view SettingsView
			if json.Unmarshal(raw, &view) == nil {
				settings = &view
			}
		}
	}
	var catalog *providers.CatalogView
	if h.FeatureEnabled("manageProviders") {
		if raw, err := client.Call("mothx/manage/providers/list", map[string]any{}); err == nil {
			var view providers.CatalogView
			if json.Unmarshal(raw, &view) == nil {
				catalog = &view
			}
		}
	}

	var providerNames []string
	if catalog != nil && len(catalog.Providers) > 0 {
		for _, provider := range catalog.Providers {
			providerNames = append(providerNames, provider.Name)
		}
	} else if settings != nil {
		for _, provider := range settings.Providers {
			providerNames = append(providerNames, provider.Name)
		}
	}
	defaults.Providers = UniqueStrings(providerNames)
	if catalog != nil {
		defaults.Models = catalog.Models
	}
	if settings != nil {
		defaults.Provider = settings.DefaultProvider
		defaults.Model = settings.DefaultModel
		defaults.Mode = strings.TrimSpace(settings.DefaultMode)
		defaults.ThinkingLevel = strings.TrimSpace(settings.ThinkingLevel)
	}
	if catalog != nil {
		if defaults.Provider == "" {
			defaults.Provider = catalog.DefaultProvider
		}
		if defaults.Model == "" {
			defaults.Model = catalog.DefaultModel
		}
	}
	if defaults.Provider == "" && len(defaults.Providers) > 0 {
		defaults.Provider = defaults.Providers[0]
	}
	if defaults.Model == "" && defaults.Provider != "" {
		if choices := ModelChoices(defaults.Models, defaults.Provider); len(choices) > 0 {
			defaults.Model = choices[0]
		}
	}
	if defaults.Mode == "" {
		defaults.Mode = "yolo"
	}
	return defaults
}

func (p *Panel) loadMCPServers(client *acp.Client) []mcp.ServerView {
	raw, err := client.Call("mothx/manage/mcp/list", map[string]any{})
	if err != nil {
		return nil
	}
	var view mcp.ListView
	if json.Unmarshal(raw, &view) != nil {
		return nil
	}
	if view.Servers == nil {
		return []mcp.ServerView{}
	}
	return view.Servers
}

func (p *Panel) loadSchedule(client *acp.Client, id string) (ScheduleView, bool) {
	raw, err := client.Call("mothx/manage/knowledge-bases/schedule", map[string]any{"id": id})
	if err != nil {
		return ScheduleView{}, false
	}
	var view ScheduleView
	if json.Unmarshal(raw, &view) != nil {
		return ScheduleView{}, false
	}
	return view, true
}

func (p *Panel) loadInsights(h Host, client *acp.Client, id string) InsightsData {
	data := InsightsData{}
	if p.RunsCapable(h) {
		raw, err := client.Call("mothx/manage/knowledge-bases/runs/list", map[string]any{"id": id, "limit": 20})
		if err != nil {
			data.Err = "读取索引历史失败：" + err.Error()
		} else {
			var view RunsView
			if err := json.Unmarshal(raw, &view); err != nil {
				data.Err = "索引历史响应无效：" + err.Error()
			} else {
				data.Runs = view.Runs
				data.RunsLoaded = true
			}
		}
	}
	if p.SourcesCapable(h) {
		raw, err := client.Call("mothx/manage/knowledge-bases/sources/list", map[string]any{"id": id})
		if err != nil {
			if data.Err != "" {
				data.Err += "；"
			}
			data.Err += "读取来源文件失败：" + err.Error()
		} else {
			var view SourcesView
			if err := json.Unmarshal(raw, &view); err != nil {
				if data.Err != "" {
					data.Err += "；"
				}
				data.Err += "来源文件响应无效：" + err.Error()
			} else {
				data.Sources = view.Sources
				data.SourcesLoaded = true
			}
		}
	}
	return data
}

// ApplyPanel adopts a fresh list projection while preserving
// in-progress edits and query text. Newly discovered bases get a draft; deleted
// bases have their draft/insight/schedule state removed.
func (p *Panel) ApplyPanel(views []BaseView, defaults *BaseDefaults, servers []mcp.ServerView, schedules map[string]ScheduleView, insights map[string]InsightsData) {
	if p.Drafts == nil {
		p.Drafts = make(map[string]*BaseDraft)
	}
	if p.Insights == nil {
		p.Insights = make(map[string]*InsightsState)
	}
	if p.Busy == nil {
		p.Busy = make(map[string]string)
	}
	if views == nil {
		views = []BaseView{}
	}
	if defaults != nil {
		p.Defaults = defaults
	}
	p.Views = views
	p.MCPServers = servers
	if p.Schedules == nil {
		p.Schedules = make(map[string]ScheduleView)
	}
	p.Schedules = schedules
	for id, data := range insights {
		state := p.Insights[id]
		if state == nil {
			state = &InsightsState{}
			p.Insights[id] = state
		}
		state.Runs = data.Runs
		state.Sources = data.Sources
		state.RunsLoaded = data.RunsLoaded
		state.SourcesLoaded = data.SourcesLoaded
		state.Loading = false
		state.Err = data.Err
	}

	valid := make(map[string]struct{}, len(views))
	for _, view := range views {
		id := strings.TrimSpace(view.KnowledgeBase.ID)
		if id == "" {
			continue
		}
		valid[id] = struct{}{}
		if _, ok := p.Drafts[id]; !ok {
			p.Drafts[id] = DraftFromBase(view.KnowledgeBase)
		}
		p.NormalizeDraft(p.Drafts[id])
	}
	for id := range p.Drafts {
		if id == NewDraftKey {
			if !p.Creating {
				delete(p.Drafts, id)
			}
			continue
		}
		if _, ok := valid[id]; !ok {
			delete(p.Drafts, id)
		}
	}
	for id := range p.Insights {
		if _, ok := valid[id]; !ok {
			delete(p.Insights, id)
		}
	}
	for id := range p.Schedules {
		if _, ok := valid[id]; !ok {
			delete(p.Schedules, id)
		}
	}
	if p.Creating {
		if _, ok := p.Drafts[NewDraftKey]; !ok {
			p.Drafts[NewDraftKey] = p.NewDraft()
		}
	}
	p.Loaded = true
	p.Err = ""
}

func (p *Panel) NewDraft() *BaseDraft {
	draft := &BaseDraft{
		PreprocessProfile: "documents",
		Mode:              "yolo",
		Schedule:          "manual",
		Enabled:           true,
	}
	if defaults := p.Defaults; defaults != nil {
		draft.Provider = defaults.Provider
		draft.Model = defaults.Model
		if defaults.Mode != "" {
			draft.Mode = defaults.Mode
		}
		draft.ThinkingLevel = defaults.ThinkingLevel
	}
	p.NormalizeDraft(draft)
	return draft
}

func DraftFromBase(base Base) *BaseDraft {
	draft := &BaseDraft{
		Name:              base.Name,
		RootDir:           base.RootDir,
		PreprocessProfile: base.PreprocessProfile,
		Provider:          base.Provider,
		Model:             base.Model,
		Mode:              base.Mode,
		ThinkingLevel:     base.ThinkingLevel,
		Schedule:          base.Schedule,
		Enabled:           base.Enabled,
		IgnoreGlobs:       append([]string(nil), base.IgnoreGlobs...),
	}
	if draft.PreprocessProfile == "" {
		draft.PreprocessProfile = "documents"
	}
	if draft.Mode == "" {
		draft.Mode = "yolo"
	}
	if draft.Schedule == "" {
		draft.Schedule = "manual"
	}
	return draft
}

func (p *Panel) NormalizeDraft(draft *BaseDraft) {
	if draft == nil {
		return
	}
	if draft.PreprocessProfile == "" {
		draft.PreprocessProfile = "documents"
	}
	if draft.Mode == "" {
		draft.Mode = "yolo"
	}
	if draft.Schedule == "" {
		draft.Schedule = "manual"
	}
	if draft.Provider == "" {
		draft.Model = ""
		return
	}
	var models []providers.CatalogModel
	if p.Defaults != nil {
		models = p.Defaults.Models
	}
	choices := ModelChoices(models, draft.Provider)
	if len(choices) == 0 {
		return
	}
	for _, choice := range choices {
		if choice == draft.Model {
			return
		}
	}
	draft.Model = choices[0]
}

func (p *Panel) EnsureDraft(key string) *BaseDraft {
	if p.Drafts == nil {
		p.Drafts = make(map[string]*BaseDraft)
	}
	if draft := p.Drafts[key]; draft != nil {
		return draft
	}
	var draft *BaseDraft
	if key == NewDraftKey {
		draft = p.NewDraft()
	} else {
		for _, view := range p.Views {
			if strings.TrimSpace(view.KnowledgeBase.ID) == key {
				draft = DraftFromBase(view.KnowledgeBase)
				break
			}
		}
	}
	if draft == nil {
		draft = p.NewDraft()
	}
	p.Drafts[key] = draft
	p.NormalizeDraft(draft)
	return draft
}

func (p *Panel) OpenNew(h Host) {
	p.Creating = true
	if _, ok := p.Drafts[NewDraftKey]; !ok {
		p.Drafts[NewDraftKey] = p.NewDraft()
	}
	p.Status = ""
	p.StatusIsError = false
}

func (p *Panel) CancelNew() {
	p.Creating = false
	delete(p.Drafts, NewDraftKey)
	delete(p.Busy, NewDraftKey)
}

func ModelChoices(models []providers.CatalogModel, provider string) []string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if strings.TrimSpace(model.Provider) != provider {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func UniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func DraftKey(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return NewDraftKey
	}
	return id
}

func DraftPayload(draft *BaseDraft) map[string]any {
	payload := map[string]any{
		"name":              strings.TrimSpace(draft.Name),
		"rootDir":           strings.TrimSpace(draft.RootDir),
		"preprocessProfile": strings.TrimSpace(draft.PreprocessProfile),
		"provider":          strings.TrimSpace(draft.Provider),
		"model":             strings.TrimSpace(draft.Model),
		"mode":              strings.TrimSpace(draft.Mode),
		"thinkingLevel":     strings.TrimSpace(draft.ThinkingLevel),
		"schedule":          strings.TrimSpace(draft.Schedule),
		"enabled":           draft.Enabled,
	}
	if draft.IgnoreGlobs != nil {
		payload["ignoreGlobs"] = append([]string(nil), draft.IgnoreGlobs...)
	}
	return payload
}

func ValidateDraft(draft *BaseDraft) error {
	if draft == nil {
		return i18n.LocalizedError("知识库草稿不存在")
	}
	if strings.TrimSpace(draft.Name) == "" {
		return i18n.LocalizedError("知识库名称不能为空")
	}
	if strings.TrimSpace(draft.RootDir) == "" {
		return i18n.LocalizedError("知识源目录不能为空")
	}
	provider := strings.TrimSpace(draft.Provider)
	model := strings.TrimSpace(draft.Model)
	if provider != "" && model == "" {
		return i18n.LocalizedError("选择供应商后必须选择模型")
	}
	if provider == "" && model != "" {
		return i18n.LocalizedError("选择模型前必须先选择供应商")
	}
	return nil
}

func MCPName(id string) string {
	return "knowledge-" + strings.TrimSpace(id)
}

func MCPEnabled(view mcp.ServerView) bool {
	return view.Enabled == nil || *view.Enabled
}

func (p *Panel) MCPEntry(id string) (mcp.ServerView, bool) {
	name := MCPName(id)
	for _, server := range p.MCPServers {
		if strings.TrimSpace(server.Name) == name {
			return server, true
		}
	}
	return mcp.ServerView{}, false
}

// MutationBlocked serializes management mutations and reloads. One
// in-flight mutation must not be invalidated by another card's reload, so the
// panel behaves as one management transaction at a time.
func (p *Panel) MutationBlocked() bool {
	return p.Loading || PanelBusy(&p.State)
}

func (p *Panel) SetBusy(key, action string) {
	if p.Busy == nil {
		p.Busy = make(map[string]string)
	}
	p.Busy[key] = action
}

func (p *Panel) ClearBusy(key string) {
	delete(p.Busy, key)
}

func (p *Panel) ViewByID(id string) *BaseView {
	id = strings.TrimSpace(id)
	for index := range p.Views {
		if strings.TrimSpace(p.Views[index].KnowledgeBase.ID) == id {
			return &p.Views[index]
		}
	}
	return nil
}

func (p *Panel) ApplyView(view BaseView) {
	id := strings.TrimSpace(view.KnowledgeBase.ID)
	if id == "" {
		return
	}
	for index := range p.Views {
		if strings.TrimSpace(p.Views[index].KnowledgeBase.ID) == id {
			p.Views[index] = view
			return
		}
	}
	p.Views = append(p.Views, view)
}

func (p *Panel) RemoveView(id string) {
	id = strings.TrimSpace(id)
	views := p.Views[:0]
	for _, view := range p.Views {
		if strings.TrimSpace(view.KnowledgeBase.ID) == id {
			continue
		}
		views = append(views, view)
	}
	p.Views = views
	delete(p.Drafts, id)
	delete(p.Insights, id)
	delete(p.Schedules, id)
	delete(p.Busy, id)
}

func (p *Panel) Save(h Host, id string) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持知识库管理（manageKnowledgeBases）。"
		p.StatusIsError = true
		return
	}
	key := DraftKey(id)
	if p.MutationBlocked() {
		return
	}
	draft := p.EnsureDraft(key)
	p.NormalizeDraft(draft)
	if err := ValidateDraft(draft); err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	creating := strings.TrimSpace(id) == ""
	method := "mothx/manage/knowledge-bases/update"
	params := map[string]any{
		"id":   strings.TrimSpace(id),
		"Base": DraftPayload(draft),
	}
	if creating {
		method = "mothx/manage/knowledge-bases/create"
		params = map[string]any{"Base": DraftPayload(draft)}
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "save")
	p.Status = "正在保存知识库配置…"
	p.StatusIsError = false
	savedPayload := DraftPayload(draft)
	go func() {
		raw, err := client.Call(method, params)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "保存知识库失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view BaseView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后知识库响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if creating {
				p.Creating = false
				delete(p.Drafts, NewDraftKey)
				if view.KnowledgeBase.ID != "" {
					p.ApplyView(view)
					p.Drafts[view.KnowledgeBase.ID] = DraftFromBase(view.KnowledgeBase)
					p.NormalizeDraft(p.Drafts[view.KnowledgeBase.ID])
					p.Insights[view.KnowledgeBase.ID] = &InsightsState{}
				}
				p.Status = "知识库已创建；可立即扫描建立索引。"
				p.StatusIsError = false
				p.StartLoad(h)
				return
			}
			p.ApplyView(view)
			if draft := p.Drafts[key]; draft != nil {
				draft.Name = StringValue(savedPayload, "name")
				draft.RootDir = StringValue(savedPayload, "rootDir")
				draft.PreprocessProfile = StringValue(savedPayload, "preprocessProfile")
				draft.Provider = StringValue(savedPayload, "provider")
				draft.Model = StringValue(savedPayload, "model")
				draft.Mode = StringValue(savedPayload, "mode")
				draft.ThinkingLevel = StringValue(savedPayload, "thinkingLevel")
				draft.Schedule = StringValue(savedPayload, "schedule")
				if enabled, ok := savedPayload["enabled"].(bool); ok {
					draft.Enabled = enabled
				}
			}
			p.Status = "知识库配置已保存；请重新扫描后使用。"
			p.StatusIsError = false
			if insights := p.Insights[key]; insights != nil {
				insights.Sources = nil
				insights.SourcesLoaded = true
			}
			p.StartLoad(h)
		})
	}()
}

func StringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func (p *Panel) Scan(h Host, id string) {
	id = strings.TrimSpace(id)
	key := DraftKey(id)
	if !p.Capable(h) || id == "" || p.MutationBlocked() {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "扫描失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "scan")
	p.Status = "正在启动后台扫描…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/scan", map[string]any{"id": id})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "启动知识库扫描失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var result ScanResult
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "扫描响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if view := p.ViewByID(id); view != nil {
				view.Indexing = result.Indexing
				if result.Status != "" {
					view.Status = result.Status
				}
			}
			if result.AlreadyRunning {
				p.Status = "知识库扫描已在后台运行，进度会周期刷新。"
			} else {
				p.Status = "扫描已在后台启动，进度会周期刷新。"
			}
			p.StatusIsError = false
			p.StartLoad(h)
		})
	}()
}

// Refresh reads one authoritative get projection. It does not
// overwrite an unsaved draft; only status/snapshot/indexing are refreshed.
func (p *Panel) Refresh(h Host, id string) {
	id = strings.TrimSpace(id)
	key := DraftKey(id)
	if !p.Capable(h) || id == "" || p.MutationBlocked() {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "刷新失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "refresh")
	p.Status = "正在刷新知识库状态…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/get", map[string]any{"id": id})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "刷新知识库失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view BaseView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "刷新后知识库响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyView(view)
			p.Status = "知识库状态已刷新"
			p.StatusIsError = false
			p.StartPolling(h)
		})
	}()
}

func (p *Panel) RequestDelete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	view := p.ViewByID(id)
	if view == nil {
		p.Status = "要删除的知识库不存在。"
		p.StatusIsError = true
		return
	}
	p.PendingDeleteID = id
	p.PendingDeleteName = view.KnowledgeBase.Name
	p.PendingDeleteRoot = view.KnowledgeBase.RootDir
	p.PendingDeleteOpen = true
}

func (p *Panel) CancelDelete() {
	p.PendingDeleteID = ""
	p.PendingDeleteName = ""
	p.PendingDeleteRoot = ""
	p.PendingDeleteOpen = false
}

func (p *Panel) ConfirmDelete(h Host) {
	id := strings.TrimSpace(p.PendingDeleteID)
	name := p.PendingDeleteName
	if id == "" {
		p.CancelDelete()
		return
	}
	key := DraftKey(id)
	if p.MutationBlocked() {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "删除失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	p.CancelDelete()
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "delete")
	p.Status = "正在删除知识库配置和索引…"
	p.StatusIsError = false
	go func() {
		_, err := client.Call("mothx/manage/knowledge-bases/delete", map[string]any{"id": id})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			if err != nil {
				p.ClearBusy(key)
				p.Status = "删除知识库失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.RemoveView(id)
			p.Status = i18n.Tf("已删除知识库「%s」的配置和索引；知识源目录保持不变。", name)
			p.StatusIsError = false
			p.StartLoad(h)
		})
	}()
}

func (p *Panel) RequestClear(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	view := p.ViewByID(id)
	if view == nil {
		return
	}
	p.PendingClearID = id
	p.PendingClearName = view.KnowledgeBase.Name
	p.PendingClearOpen = true
}

func (p *Panel) CancelClear() {
	p.PendingClearID = ""
	p.PendingClearName = ""
	p.PendingClearOpen = false
}

func (p *Panel) ConfirmClear(h Host) {
	id := strings.TrimSpace(p.PendingClearID)
	if id == "" {
		p.CancelClear()
		return
	}
	key := DraftKey(id)
	if p.MutationBlocked() {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "清除索引失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	p.CancelClear()
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "clear")
	p.Status = "正在清除知识库索引…"
	p.StatusIsError = false
	go func() {
		_, err := client.Call("mothx/manage/knowledge-bases/clear", map[string]any{"id": id})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "清除知识库索引失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			if view := p.ViewByID(id); view != nil {
				view.Snapshot = nil
				view.Indexing = nil
				view.Status = "unindexed"
				view.KnowledgeBase.ActiveSnapshotID = ""
			}
			if insights := p.Insights[id]; insights != nil {
				insights.Sources = nil
				insights.SourcesLoaded = true
			}
			p.Status = "索引已清除，配置与知识源目录保持不变。"
			p.StatusIsError = false
			p.StartLoad(h)
		})
	}()
}

func (p *Panel) ToggleSchedule(h Host, id string) {
	id = strings.TrimSpace(id)
	key := DraftKey(id)
	schedule, ok := p.Schedules[id]
	if !p.Capable(h) || id == "" || !ok || p.MutationBlocked() {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "更新扫描计划失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	enabled := !schedule.Enabled
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "schedule")
	p.Status = "正在更新扫描计划…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/schedule", map[string]any{"id": id, "enabled": enabled})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "更新扫描计划失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var updated ScheduleView
			if err := json.Unmarshal(raw, &updated); err != nil {
				p.Status = "扫描计划响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.Schedules[id] = updated
			if draft := p.Drafts[id]; draft != nil {
				draft.Enabled = updated.Enabled
			}
			if view := p.ViewByID(id); view != nil {
				view.KnowledgeBase.Enabled = updated.Enabled
				if !updated.Enabled {
					view.Status = "disabled"
				}
			}
			p.Status = "扫描计划已更新"
			p.StatusIsError = false
			p.StartLoad(h)
		})
	}()
}

func (p *Panel) ApplyMCP(h Host, id string) {
	id = strings.TrimSpace(id)
	key := DraftKey(id)
	if !p.Capable(h) || !p.MCPCapable(h) || id == "" || p.MutationBlocked() {
		return
	}
	entry, exists := p.MCPEntry(id)
	enabled := !exists || !MCPEnabled(entry)
	client := h.ACP()
	if client == nil {
		p.Status = "更新知识库 MCP 失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	p.SetBusy(key, "mcp")
	p.Status = "正在更新知识库 MCP 配置…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/mcp/apply", map[string]any{"id": id, "enabled": enabled})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.ClearBusy(key)
			if err != nil {
				p.Status = "更新知识库 MCP 失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var result MCPApplyResult
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "知识库 MCP 响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			name := strings.TrimSpace(result.Name)
			if name == "" {
				name = MCPName(id)
			}
			updated := false
			for index := range p.MCPServers {
				if strings.TrimSpace(p.MCPServers[index].Name) == name {
					enabledValue := result.Enabled
					p.MCPServers[index].Enabled = &enabledValue
					updated = true
					break
				}
			}
			if !updated {
				enabledValue := result.Enabled
				p.MCPServers = append(p.MCPServers, mcp.ServerView{Name: name, Type: "stdio", Enabled: &enabledValue})
			}
			p.Status = "知识库 MCP 已更新；新建或重新加载会话后生效。"
			p.StatusIsError = false
			p.StartLoad(h)
		})
	}()
}

func (p *Panel) RunQuery(h Host, id string) {
	id = strings.TrimSpace(id)
	insights := p.Insights[id]
	if !p.Capable(h) || id == "" || insights == nil || insights.QueryBusy {
		return
	}
	query := strings.TrimSpace(insights.Query)
	if query == "" {
		insights.QueryErr = "请输入查询内容"
		return
	}
	client := h.ACP()
	if client == nil {
		insights.QueryErr = "查询失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	insights.QueryBusy = true
	insights.QueryErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/query", map[string]any{
			"id": id, "query": query, "limit": 8,
		})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			insights := p.Insights[id]
			if insights == nil {
				return
			}
			insights.QueryBusy = false
			if err != nil {
				insights.QueryErr = "查询失败：" + err.Error()
				return
			}
			var result QueryResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				insights.QueryErr = "查询响应无效：" + err.Error()
				return
			}
			insights.QueryErr = ""
			insights.QueryResult = &result.Query
		})
	}()
}

// LoadRunDetail projects one canonical index Run through the additive
// runs/get method. The list already carries bounded stats; this detail request
// adds run/snapshot identity and terminal diagnostics on demand.
func (p *Panel) LoadRunDetail(h Host, id, runID string) {
	id = strings.TrimSpace(id)
	runID = strings.TrimSpace(runID)
	insights := p.Insights[id]
	if !p.RunsCapable(h) || id == "" || runID == "" || insights == nil || insights.RunDetailBusy {
		return
	}
	client := h.ACP()
	if client == nil {
		insights.RunDetailErr = "读取运行详情失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	insights.RunDetailBusy = true
	insights.RunDetailID = runID
	insights.RunDetailErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/knowledge-bases/runs/get", map[string]any{"id": id, "runId": runID})
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			insights := p.Insights[id]
			if insights == nil {
				return
			}
			insights.RunDetailBusy = false
			if err != nil {
				insights.RunDetailErr = "读取运行详情失败：" + err.Error()
				return
			}
			var response struct {
				Run IndexRunView `json:"run"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				insights.RunDetailErr = "运行详情响应无效：" + err.Error()
				return
			}
			insights.RunDetailErr = ""
			insights.RunDetailID = response.Run.RunID
			insights.RunDetail = &response.Run
		})
	}()
}

// StartPolling starts one poll loop for the currently running index
// jobs. It reads only the seq/poll tokens from the worker; all view mutation is
// dispatched to the UI thread.
func (p *Panel) StartPolling(h Host) {
	if p.Polling || !p.Loaded || !h.SettingsOpen() || h.SettingsTab() != "knowledge" {
		return
	}
	ids := make([]string, 0, len(p.Views))
	for _, view := range p.Views {
		if view.Indexing != nil && view.Indexing.Running {
			if id := strings.TrimSpace(view.KnowledgeBase.ID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	client := h.ACP()
	if client == nil {
		return
	}
	p.Polling = true
	pollToken := atomic.AddInt64(&p.PollSeq, 1)
	seq := atomic.LoadInt64(&p.Seq)
	startReloadSeq := h.ReloadSequence()
	go p.pollIndexes(h, client, ids, seq, startReloadSeq, pollToken)
}

func (p *Panel) pollIndexes(h Host, client *acp.Client, ids []string, seq, startReloadSeq, pollToken int64) {
	for {
		time.Sleep(PollInterval)
		if pollToken != atomic.LoadInt64(&p.PollSeq) ||
			seq != atomic.LoadInt64(&p.Seq) {
			return
		}
		anyRunning := false
		okCount := 0
		for _, id := range ids {
			raw, err := client.Call("mothx/manage/knowledge-bases/status", map[string]any{"id": id})
			if err != nil {
				continue
			}
			var view BaseView
			if json.Unmarshal(raw, &view) != nil || strings.TrimSpace(view.KnowledgeBase.ID) == "" {
				continue
			}
			okCount++
			if view.Indexing != nil && view.Indexing.Running {
				anyRunning = true
			}
			updated := view
			h.Dispatch(func() {
				if !p.Current(h, seq, startReloadSeq) {
					return
				}
				p.ApplyView(updated)
			})
		}
		if okCount == 0 || !anyRunning {
			h.Dispatch(func() {
				if p.Current(h, seq, startReloadSeq) {
					p.Polling = false
				}
			})
			return
		}
	}
}

// StatusText mirrors the Desktop panel's status precedence: disabled,
// running background index, missing snapshot, then the snapshot status.
func StatusText(view BaseView) string {
	base := view.KnowledgeBase
	if !base.Enabled {
		return i18n.T("已停用")
	}
	if view.Indexing != nil && view.Indexing.Running {
		label := i18n.T("索引中")
		if phase := strings.TrimSpace(view.Indexing.Phase); phase != "" {
			label = i18n.Tf("索引中：%s", phase)
		}
		if view.Indexing.FilesTotal > 0 {
			label += fmt.Sprintf(" · %d/%d", view.Indexing.FilesDone, view.Indexing.FilesTotal)
		}
		return label
	}
	if view.Snapshot == nil {
		return i18n.T("尚未建立可用索引")
	}
	status := strings.TrimSpace(view.Status)
	if status == "" {
		status = strings.TrimSpace(view.Snapshot.Status)
	}
	if status == "" {
		return i18n.T("尚未建立可用索引")
	}
	return i18n.Tf("索引状态：%s", status)
}

func StatsText(snapshot *Snapshot) string {
	if snapshot == nil {
		return ""
	}
	return i18n.Tf("文件 %d · 分块 %d · 节点 %d · 关系 %d",
		snapshot.FileCount, snapshot.ChunkCount, snapshot.NodeCount, snapshot.EdgeCount)
}

func RunStatsText(run IndexRunView) string {
	return i18n.Tf("文件 %d · 分块 %d · 节点 %d · 关系 %d",
		run.FileCount, run.ChunkCount, run.NodeCount, run.EdgeCount)
}

func ScheduleText(schedule ScheduleView) string {
	if schedule.Schedule == "" {
		return i18n.T("仅手动扫描")
	}
	if schedule.Configured && schedule.NextRun != "" {
		if next := FormatDate(schedule.NextRun); next != "" {
			return i18n.T("下次运行：") + next
		}
	}
	return i18n.T("仅手动扫描")
}

func FormatDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if parsed.Year() <= 1 {
			return ""
		}
		return parsed.Local().Format("2006-01-02 15:04")
	}
	return value
}

func FormatSize(bytes int64) string {
	size := bytes
	if size < 0 {
		size = 0
	}
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}

func SourceMeta(source SourceView) string {
	return i18n.Tf("%s · %s · %d 个分块", i18n.T(source.Status), FormatSize(source.ByteSize), source.ChunkCount)
}

func Truncate(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// ChooseRoot opens the native directory picker and writes the chosen
// path into the current draft only if the panel and sequence are still current.
func (p *Panel) ChooseRoot(h Host, key string) {
	h.OpenDirectory("选择知识源目录", func(path string) {
		seq := atomic.LoadInt64(&p.Seq)
		startReloadSeq := h.ReloadSequence()
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			draft := p.Drafts[key]
			if draft == nil {
				return
			}
			draft.RootDir = path
		})
	})
}

// PanelBusy reports whether any knowledge mutation is in flight.
func PanelBusy(p *State) bool {
	for _, action := range p.Busy {
		if action != "" {
			return true
		}
	}
	return false
}
