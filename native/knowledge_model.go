package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// This file owns the Native projection of the ACP knowledge-base management
// contract. Knowledge configuration, snapshots, runs, sources, schedules, and
// the standard Knowledge MCP server are all Runtime-owned; this panel only
// projects them through ACP and never reads the source directory or the
// per-knowledge-base SQLite store.

const (
	knowledgePollInterval = 3 * time.Second
	knowledgeNewDraftKey  = "new"
)

var (
	knowledgeProfiles = []string{"documents", "code", "notes", "mixed"}
	knowledgeModes    = []string{"yolo", "agent", "plan", "os"}
)

// knowledgeBase mirrors session.KnowledgeBase as projected by ACP.
type knowledgeBase struct {
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

// knowledgeSnapshot mirrors session.KnowledgeSnapshot. Timestamps stay strings
// so the Native UI never has to interpret Runtime time semantics.
type knowledgeSnapshot struct {
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

// knowledgeIndexProgress mirrors the live background scan projection. Hosts
// poll list/status while Running is true instead of blocking on scan.
type knowledgeIndexProgress struct {
	Running    bool   `json:"running"`
	Phase      string `json:"phase,omitempty"`
	FilesTotal int64  `json:"filesTotal"`
	FilesDone  int64  `json:"filesDone"`
	Chunks     int64  `json:"chunks"`
	StartedAt  string `json:"startedAt,omitempty"`
	RunID      string `json:"runId,omitempty"`
	Error      string `json:"error,omitempty"`
}

// knowledgeBaseView mirrors manageKnowledgeBaseView.
type knowledgeBaseView struct {
	KnowledgeBase knowledgeBase           `json:"knowledgeBase"`
	Snapshot      *knowledgeSnapshot      `json:"snapshot"`
	Status        string                  `json:"status"`
	Indexing      *knowledgeIndexProgress `json:"indexing,omitempty"`
}

type knowledgeListView struct {
	KnowledgeBases []knowledgeBaseView `json:"knowledgeBases"`
}

type knowledgeScanResult struct {
	Started        bool                    `json:"started"`
	AlreadyRunning bool                    `json:"alreadyRunning"`
	ID             string                  `json:"id"`
	Status         string                  `json:"status"`
	Indexing       *knowledgeIndexProgress `json:"indexing,omitempty"`
}

// knowledgeIndexRunView mirrors agentruntime.KnowledgeIndexRun.
type knowledgeIndexRunView struct {
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

type knowledgeRunsView struct {
	Runs []knowledgeIndexRunView `json:"runs"`
}

// knowledgeSourceView mirrors session.KnowledgeSource: file-level provenance
// only, never file contents.
type knowledgeSourceView struct {
	RelativePath  string `json:"relativePath"`
	Title         string `json:"title,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	Status        string `json:"status"`
	ByteSize      int64  `json:"byteSize"`
	ChunkCount    int    `json:"chunkCount"`
	ContentSHA256 string `json:"contentSHA256,omitempty"`
}

type knowledgeSourcesView struct {
	Sources []knowledgeSourceView `json:"sources"`
}

// knowledgeScheduleView mirrors the schedule projection returned by
// mothx/manage/knowledge-bases/schedule.
type knowledgeScheduleView struct {
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

type knowledgeMCPApplyResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type knowledgeChunk struct {
	ID           string `json:"id"`
	SnapshotID   string `json:"snapshotId"`
	FileID       string `json:"fileId"`
	RelativePath string `json:"relativePath,omitempty"`
	Ordinal      int    `json:"ordinal"`
	Text         string `json:"text"`
	StartLine    int    `json:"startLine"`
	EndLine      int    `json:"endLine"`
}

type knowledgeNode struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Summary    string  `json:"summary,omitempty"`
	Status     string  `json:"status,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type knowledgeGraphQuery struct {
	Snapshot    knowledgeSnapshot `json:"snapshot"`
	Chunks      []knowledgeChunk  `json:"chunks"`
	Nodes       []knowledgeNode   `json:"nodes"`
	Edges       []json.RawMessage `json:"edges"`
	Evidence    []json.RawMessage `json:"evidence"`
	Truncated   bool              `json:"truncated"`
	Uncertainty []json.RawMessage `json:"uncertainties"`
}

type knowledgeQueryResultView struct {
	Query knowledgeGraphQuery `json:"query"`
}

// knowledgeSettingsView is the safe subset of mothx/manage/settings/get used
// for knowledge-base defaults. It contains no credentials.
type knowledgeSettingsView struct {
	DefaultProvider string            `json:"defaultProvider"`
	DefaultModel    string            `json:"defaultModel"`
	DefaultMode     string            `json:"defaultMode"`
	ThinkingLevel   string            `json:"thinkingLevel"`
	Providers       []providerSummary `json:"providers"`
}

// knowledgeBaseDefaults is the create-form seed derived from the advertised
// settings/provider ACP projections.
type knowledgeBaseDefaults struct {
	Provider      string
	Model         string
	Mode          string
	ThinkingLevel string
	Providers     []string
	Models        []providerCatalogModel
}

// knowledgeBaseDraft is the editable, non-secret configuration of one
// knowledge base. IgnoreGlobs is preserved when an existing config carries it
// even though the first Desktop/native form does not expose a dedicated field.
type knowledgeBaseDraft struct {
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

// knowledgeInsightsState projects the additive run/source tabs and the debug
// query preview for one knowledge base. None of these facts are persisted
// locally; each tab is loaded through ACP and feature-gated independently.
type knowledgeInsightsState struct {
	Runs          []knowledgeIndexRunView
	Sources       []knowledgeSourceView
	RunsLoaded    bool
	SourcesLoaded bool
	Loading       bool
	Err           string
	Tab           int

	Query       string
	QueryResult *knowledgeGraphQuery
	QueryBusy   bool
	QueryErr    string

	RunDetail     *knowledgeIndexRunView
	RunDetailID   string
	RunDetailBusy bool
	RunDetailErr  string
}

// knowledgeInsightsData is the loaded form returned from the ACP worker.
type knowledgeInsightsData struct {
	Runs          []knowledgeIndexRunView
	Sources       []knowledgeSourceView
	RunsLoaded    bool
	SourcesLoaded bool
	Err           string
}

type knowledgePanelState struct {
	Views    []knowledgeBaseView
	Defaults *knowledgeBaseDefaults
	Loaded   bool
	Loading  bool
	Err      string

	Status        string
	StatusIsError bool

	Creating   bool
	Drafts     map[string]*knowledgeBaseDraft
	Insights   map[string]*knowledgeInsightsState
	Schedules  map[string]knowledgeScheduleView
	MCPServers []mcpServerView
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

func newKnowledgePanelState() knowledgePanelState {
	return knowledgePanelState{
		Drafts:    make(map[string]*knowledgeBaseDraft),
		Insights:  make(map[string]*knowledgeInsightsState),
		Schedules: make(map[string]knowledgeScheduleView),
		Busy:      make(map[string]string),
	}
}

func (a *nativeApp) knowledgeCapable() bool {
	return a.featureEnabled("manageKnowledgeBases")
}

func (a *nativeApp) knowledgeRunsCapable() bool {
	return a.featureEnabled("knowledgeRuns")
}

func (a *nativeApp) knowledgeSourcesCapable() bool {
	return a.featureEnabled("knowledgeSources")
}

func (a *nativeApp) knowledgeGraphCapable() bool {
	return a.featureEnabled("knowledgeGraphIndex")
}

func (a *nativeApp) knowledgeMCPCapable() bool {
	return a.featureEnabled("manageMcp")
}

func (a *nativeApp) knowledgePanelCurrent(seq, reloadSeq int64) bool {
	return a.panelVisible("library", "knowledge") &&
		seq == atomic.LoadInt64(&a.knowledge.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateKnowledgePanel drops transient state and makes every in-flight
// knowledge response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateKnowledgePanel() {
	k := &a.knowledge
	atomic.AddInt64(&k.Seq, 1)
	atomic.AddInt64(&k.PollSeq, 1)
	k.Views = nil
	k.Defaults = nil
	k.Loaded = false
	k.Loading = false
	k.Err = ""
	k.Status = ""
	k.StatusIsError = false
	k.Creating = false
	k.Drafts = make(map[string]*knowledgeBaseDraft)
	k.Insights = make(map[string]*knowledgeInsightsState)
	k.Schedules = make(map[string]knowledgeScheduleView)
	k.MCPServers = nil
	k.Busy = make(map[string]string)
	k.PendingDeleteID = ""
	k.PendingDeleteName = ""
	k.PendingDeleteRoot = ""
	k.PendingDeleteOpen = false
	k.PendingClearID = ""
	k.PendingClearName = ""
	k.PendingClearOpen = false
	k.Polling = false
}

// startKnowledgePanelLoad starts one capability-gated load. It is called from
// the UI thread on tab entry.
func (a *nativeApp) startKnowledgePanelLoad() {
	if !a.panelVisible("library", "knowledge") {
		return
	}
	k := &a.knowledge
	if k.Loading {
		return
	}
	if !a.knowledgeCapable() {
		k.Loaded = true
		k.Err = "当前运行时不支持知识库管理（manageKnowledgeBases）。"
		return
	}
	seq := atomic.AddInt64(&k.Seq, 1)
	atomic.AddInt64(&k.PollSeq, 1)
	k.Polling = false
	k.Loading = true
	k.Err = ""
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadKnowledgePanel(seq, startReloadSeq)
}

func (a *nativeApp) loadKnowledgePanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			k.Loading = false
			k.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/knowledge-bases/list", map[string]any{})
	if err != nil {
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			k.Loading = false
			k.Err = "读取知识库列表失败：" + err.Error()
		})
		return
	}
	var list knowledgeListView
	if err := json.Unmarshal(raw, &list); err != nil {
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			k.Loading = false
			k.Err = "知识库列表响应无效：" + err.Error()
		})
		return
	}

	defaults := a.loadKnowledgeDefaults(client)
	var servers []mcpServerView
	if a.knowledgeMCPCapable() {
		servers = a.loadKnowledgeMCPServers(client)
	}
	schedules := make(map[string]knowledgeScheduleView, len(list.KnowledgeBases))
	insights := make(map[string]knowledgeInsightsData, len(list.KnowledgeBases))
	for _, view := range list.KnowledgeBases {
		id := strings.TrimSpace(view.KnowledgeBase.ID)
		if id == "" {
			continue
		}
		if schedule, ok := a.loadKnowledgeSchedule(client, id); ok {
			schedules[id] = schedule
		}
		if a.knowledgeRunsCapable() || a.knowledgeSourcesCapable() {
			insights[id] = a.loadKnowledgeInsights(client, id)
		}
	}

	a.dispatch(func() {
		if !a.knowledgePanelCurrent(seq, startReloadSeq) {
			return
		}
		k := &a.knowledge
		k.Loading = false
		a.applyKnowledgePanel(list.KnowledgeBases, defaults, servers, schedules, insights)
		k.Err = ""
		a.startKnowledgePolling()
	})
}

func (a *nativeApp) loadKnowledgeDefaults(client *acpClient) *knowledgeBaseDefaults {
	defaults := &knowledgeBaseDefaults{Mode: "yolo"}
	var settings *knowledgeSettingsView
	if a.featureEnabled("manageSettings") {
		if raw, err := client.call("mothx/manage/settings/get", map[string]any{}); err == nil {
			var view knowledgeSettingsView
			if json.Unmarshal(raw, &view) == nil {
				settings = &view
			}
		}
	}
	var catalog *providerCatalogView
	if a.featureEnabled("manageProviders") {
		if raw, err := client.call("mothx/manage/providers/list", map[string]any{}); err == nil {
			var view providerCatalogView
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
	defaults.Providers = knowledgeUniqueStrings(providerNames)
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
		if choices := knowledgeModelChoices(defaults.Models, defaults.Provider); len(choices) > 0 {
			defaults.Model = choices[0]
		}
	}
	if defaults.Mode == "" {
		defaults.Mode = "yolo"
	}
	return defaults
}

func (a *nativeApp) loadKnowledgeMCPServers(client *acpClient) []mcpServerView {
	raw, err := client.call("mothx/manage/mcp/list", map[string]any{})
	if err != nil {
		return nil
	}
	var view mcpListView
	if json.Unmarshal(raw, &view) != nil {
		return nil
	}
	if view.Servers == nil {
		return []mcpServerView{}
	}
	return view.Servers
}

func (a *nativeApp) loadKnowledgeSchedule(client *acpClient, id string) (knowledgeScheduleView, bool) {
	raw, err := client.call("mothx/manage/knowledge-bases/schedule", map[string]any{"id": id})
	if err != nil {
		return knowledgeScheduleView{}, false
	}
	var view knowledgeScheduleView
	if json.Unmarshal(raw, &view) != nil {
		return knowledgeScheduleView{}, false
	}
	return view, true
}

func (a *nativeApp) loadKnowledgeInsights(client *acpClient, id string) knowledgeInsightsData {
	data := knowledgeInsightsData{}
	if a.knowledgeRunsCapable() {
		raw, err := client.call("mothx/manage/knowledge-bases/runs/list", map[string]any{"id": id, "limit": 20})
		if err != nil {
			data.Err = "读取索引历史失败：" + err.Error()
		} else {
			var view knowledgeRunsView
			if err := json.Unmarshal(raw, &view); err != nil {
				data.Err = "索引历史响应无效：" + err.Error()
			} else {
				data.Runs = view.Runs
				data.RunsLoaded = true
			}
		}
	}
	if a.knowledgeSourcesCapable() {
		raw, err := client.call("mothx/manage/knowledge-bases/sources/list", map[string]any{"id": id})
		if err != nil {
			if data.Err != "" {
				data.Err += "；"
			}
			data.Err += "读取来源文件失败：" + err.Error()
		} else {
			var view knowledgeSourcesView
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

// applyKnowledgePanel adopts a fresh list projection while preserving
// in-progress edits and query text. Newly discovered bases get a draft; deleted
// bases have their draft/insight/schedule state removed.
func (a *nativeApp) applyKnowledgePanel(views []knowledgeBaseView, defaults *knowledgeBaseDefaults, servers []mcpServerView, schedules map[string]knowledgeScheduleView, insights map[string]knowledgeInsightsData) {
	k := &a.knowledge
	if k.Drafts == nil {
		k.Drafts = make(map[string]*knowledgeBaseDraft)
	}
	if k.Insights == nil {
		k.Insights = make(map[string]*knowledgeInsightsState)
	}
	if k.Busy == nil {
		k.Busy = make(map[string]string)
	}
	if views == nil {
		views = []knowledgeBaseView{}
	}
	if defaults != nil {
		k.Defaults = defaults
	}
	k.Views = views
	k.MCPServers = servers
	if k.Schedules == nil {
		k.Schedules = make(map[string]knowledgeScheduleView)
	}
	k.Schedules = schedules
	for id, data := range insights {
		state := k.Insights[id]
		if state == nil {
			state = &knowledgeInsightsState{}
			k.Insights[id] = state
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
		if _, ok := k.Drafts[id]; !ok {
			k.Drafts[id] = knowledgeDraftFromBase(view.KnowledgeBase)
		}
		a.normalizeKnowledgeDraft(k.Drafts[id])
	}
	for id := range k.Drafts {
		if id == knowledgeNewDraftKey {
			if !k.Creating {
				delete(k.Drafts, id)
			}
			continue
		}
		if _, ok := valid[id]; !ok {
			delete(k.Drafts, id)
		}
	}
	for id := range k.Insights {
		if _, ok := valid[id]; !ok {
			delete(k.Insights, id)
		}
	}
	for id := range k.Schedules {
		if _, ok := valid[id]; !ok {
			delete(k.Schedules, id)
		}
	}
	if k.Creating {
		if _, ok := k.Drafts[knowledgeNewDraftKey]; !ok {
			k.Drafts[knowledgeNewDraftKey] = a.newKnowledgeDraft()
		}
	}
	k.Loaded = true
	k.Err = ""
}

func (a *nativeApp) newKnowledgeDraft() *knowledgeBaseDraft {
	draft := &knowledgeBaseDraft{
		PreprocessProfile: "documents",
		Mode:              "yolo",
		Schedule:          "manual",
		Enabled:           true,
	}
	if defaults := a.knowledge.Defaults; defaults != nil {
		draft.Provider = defaults.Provider
		draft.Model = defaults.Model
		if defaults.Mode != "" {
			draft.Mode = defaults.Mode
		}
		draft.ThinkingLevel = defaults.ThinkingLevel
	}
	a.normalizeKnowledgeDraft(draft)
	return draft
}

func knowledgeDraftFromBase(base knowledgeBase) *knowledgeBaseDraft {
	draft := &knowledgeBaseDraft{
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

func (a *nativeApp) normalizeKnowledgeDraft(draft *knowledgeBaseDraft) {
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
	var models []providerCatalogModel
	if a.knowledge.Defaults != nil {
		models = a.knowledge.Defaults.Models
	}
	choices := knowledgeModelChoices(models, draft.Provider)
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

func (a *nativeApp) ensureKnowledgeDraft(key string) *knowledgeBaseDraft {
	k := &a.knowledge
	if k.Drafts == nil {
		k.Drafts = make(map[string]*knowledgeBaseDraft)
	}
	if draft := k.Drafts[key]; draft != nil {
		return draft
	}
	var draft *knowledgeBaseDraft
	if key == knowledgeNewDraftKey {
		draft = a.newKnowledgeDraft()
	} else {
		for _, view := range k.Views {
			if strings.TrimSpace(view.KnowledgeBase.ID) == key {
				draft = knowledgeDraftFromBase(view.KnowledgeBase)
				break
			}
		}
	}
	if draft == nil {
		draft = a.newKnowledgeDraft()
	}
	k.Drafts[key] = draft
	a.normalizeKnowledgeDraft(draft)
	return draft
}

func (a *nativeApp) openKnowledgeNew() {
	k := &a.knowledge
	k.Creating = true
	if _, ok := k.Drafts[knowledgeNewDraftKey]; !ok {
		k.Drafts[knowledgeNewDraftKey] = a.newKnowledgeDraft()
	}
	k.Status = ""
	k.StatusIsError = false
}

func (a *nativeApp) cancelKnowledgeNew() {
	k := &a.knowledge
	k.Creating = false
	delete(k.Drafts, knowledgeNewDraftKey)
	delete(k.Busy, knowledgeNewDraftKey)
}

func knowledgeModelChoices(models []providerCatalogModel, provider string) []string {
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

func knowledgeUniqueStrings(values []string) []string {
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

func knowledgeDraftKey(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return knowledgeNewDraftKey
	}
	return id
}

func knowledgeDraftPayload(draft *knowledgeBaseDraft) map[string]any {
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

func validateKnowledgeDraft(draft *knowledgeBaseDraft) error {
	if draft == nil {
		return localizedError("知识库草稿不存在")
	}
	if strings.TrimSpace(draft.Name) == "" {
		return localizedError("知识库名称不能为空")
	}
	if strings.TrimSpace(draft.RootDir) == "" {
		return localizedError("知识源目录不能为空")
	}
	provider := strings.TrimSpace(draft.Provider)
	model := strings.TrimSpace(draft.Model)
	if provider != "" && model == "" {
		return localizedError("选择供应商后必须选择模型")
	}
	if provider == "" && model != "" {
		return localizedError("选择模型前必须先选择供应商")
	}
	return nil
}

func knowledgeMCPName(id string) string {
	return "knowledge-" + strings.TrimSpace(id)
}

func knowledgeMCPEnabled(view mcpServerView) bool {
	return view.Enabled == nil || *view.Enabled
}

func (a *nativeApp) knowledgeMCPEntry(id string) (mcpServerView, bool) {
	name := knowledgeMCPName(id)
	for _, server := range a.knowledge.MCPServers {
		if strings.TrimSpace(server.Name) == name {
			return server, true
		}
	}
	return mcpServerView{}, false
}

// knowledgeMutationBlocked serializes management mutations and reloads. One
// in-flight mutation must not be invalidated by another card's reload, so the
// panel behaves as one management transaction at a time.
func (a *nativeApp) knowledgeMutationBlocked() bool {
	return a.knowledge.Loading || knowledgePanelBusy(&a.knowledge)
}

func (a *nativeApp) setKnowledgeBusy(key, action string) {
	if a.knowledge.Busy == nil {
		a.knowledge.Busy = make(map[string]string)
	}
	a.knowledge.Busy[key] = action
}

func (a *nativeApp) clearKnowledgeBusy(key string) {
	delete(a.knowledge.Busy, key)
}

func (a *nativeApp) knowledgeViewByID(id string) *knowledgeBaseView {
	id = strings.TrimSpace(id)
	for index := range a.knowledge.Views {
		if strings.TrimSpace(a.knowledge.Views[index].KnowledgeBase.ID) == id {
			return &a.knowledge.Views[index]
		}
	}
	return nil
}

func (a *nativeApp) applyKnowledgeView(view knowledgeBaseView) {
	id := strings.TrimSpace(view.KnowledgeBase.ID)
	if id == "" {
		return
	}
	for index := range a.knowledge.Views {
		if strings.TrimSpace(a.knowledge.Views[index].KnowledgeBase.ID) == id {
			a.knowledge.Views[index] = view
			return
		}
	}
	a.knowledge.Views = append(a.knowledge.Views, view)
}

func (a *nativeApp) removeKnowledgeView(id string) {
	id = strings.TrimSpace(id)
	views := a.knowledge.Views[:0]
	for _, view := range a.knowledge.Views {
		if strings.TrimSpace(view.KnowledgeBase.ID) == id {
			continue
		}
		views = append(views, view)
	}
	a.knowledge.Views = views
	delete(a.knowledge.Drafts, id)
	delete(a.knowledge.Insights, id)
	delete(a.knowledge.Schedules, id)
	delete(a.knowledge.Busy, id)
}

func (a *nativeApp) saveKnowledgeBase(id string) {
	k := &a.knowledge
	if !a.knowledgeCapable() {
		k.Status = "当前运行时不支持知识库管理（manageKnowledgeBases）。"
		k.StatusIsError = true
		return
	}
	key := knowledgeDraftKey(id)
	if a.knowledgeMutationBlocked() {
		return
	}
	draft := a.ensureKnowledgeDraft(key)
	a.normalizeKnowledgeDraft(draft)
	if err := validateKnowledgeDraft(draft); err != nil {
		k.Status = err.Error()
		k.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "保存失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	creating := strings.TrimSpace(id) == ""
	method := "mothx/manage/knowledge-bases/update"
	params := map[string]any{
		"id":            strings.TrimSpace(id),
		"knowledgeBase": knowledgeDraftPayload(draft),
	}
	if creating {
		method = "mothx/manage/knowledge-bases/create"
		params = map[string]any{"knowledgeBase": knowledgeDraftPayload(draft)}
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "save")
	k.Status = "正在保存知识库配置…"
	k.StatusIsError = false
	savedPayload := knowledgeDraftPayload(draft)
	go func() {
		raw, err := client.call(method, params)
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "保存知识库失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			var view knowledgeBaseView
			if err := json.Unmarshal(raw, &view); err != nil {
				k.Status = "保存后知识库响应无效：" + err.Error()
				k.StatusIsError = true
				return
			}
			if creating {
				k.Creating = false
				delete(k.Drafts, knowledgeNewDraftKey)
				if view.KnowledgeBase.ID != "" {
					a.applyKnowledgeView(view)
					k.Drafts[view.KnowledgeBase.ID] = knowledgeDraftFromBase(view.KnowledgeBase)
					a.normalizeKnowledgeDraft(k.Drafts[view.KnowledgeBase.ID])
					k.Insights[view.KnowledgeBase.ID] = &knowledgeInsightsState{}
				}
				k.Status = "知识库已创建；可立即扫描建立索引。"
				k.StatusIsError = false
				a.startKnowledgePanelLoad()
				return
			}
			a.applyKnowledgeView(view)
			if draft := k.Drafts[key]; draft != nil {
				draft.Name = knowledgeStringValue(savedPayload, "name")
				draft.RootDir = knowledgeStringValue(savedPayload, "rootDir")
				draft.PreprocessProfile = knowledgeStringValue(savedPayload, "preprocessProfile")
				draft.Provider = knowledgeStringValue(savedPayload, "provider")
				draft.Model = knowledgeStringValue(savedPayload, "model")
				draft.Mode = knowledgeStringValue(savedPayload, "mode")
				draft.ThinkingLevel = knowledgeStringValue(savedPayload, "thinkingLevel")
				draft.Schedule = knowledgeStringValue(savedPayload, "schedule")
				if enabled, ok := savedPayload["enabled"].(bool); ok {
					draft.Enabled = enabled
				}
			}
			k.Status = "知识库配置已保存；请重新扫描后使用。"
			k.StatusIsError = false
			if insights := k.Insights[key]; insights != nil {
				insights.Sources = nil
				insights.SourcesLoaded = true
			}
			a.startKnowledgePanelLoad()
		})
	}()
}

func knowledgeStringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func (a *nativeApp) scanKnowledgeBase(id string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	key := knowledgeDraftKey(id)
	if !a.knowledgeCapable() || id == "" || a.knowledgeMutationBlocked() {
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "扫描失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "scan")
	k.Status = "正在启动后台扫描…"
	k.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/scan", map[string]any{"id": id})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "启动知识库扫描失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			var result knowledgeScanResult
			if err := json.Unmarshal(raw, &result); err != nil {
				k.Status = "扫描响应无效：" + err.Error()
				k.StatusIsError = true
				return
			}
			if view := a.knowledgeViewByID(id); view != nil {
				view.Indexing = result.Indexing
				if result.Status != "" {
					view.Status = result.Status
				}
			}
			if result.AlreadyRunning {
				k.Status = "知识库扫描已在后台运行，进度会周期刷新。"
			} else {
				k.Status = "扫描已在后台启动，进度会周期刷新。"
			}
			k.StatusIsError = false
			a.startKnowledgePanelLoad()
		})
	}()
}

// refreshKnowledgeBase reads one authoritative get projection. It does not
// overwrite an unsaved draft; only status/snapshot/indexing are refreshed.
func (a *nativeApp) refreshKnowledgeBase(id string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	key := knowledgeDraftKey(id)
	if !a.knowledgeCapable() || id == "" || a.knowledgeMutationBlocked() {
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "刷新失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "refresh")
	k.Status = "正在刷新知识库状态…"
	k.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/get", map[string]any{"id": id})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "刷新知识库失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			var view knowledgeBaseView
			if err := json.Unmarshal(raw, &view); err != nil {
				k.Status = "刷新后知识库响应无效：" + err.Error()
				k.StatusIsError = true
				return
			}
			a.applyKnowledgeView(view)
			k.Status = "知识库状态已刷新"
			k.StatusIsError = false
			a.startKnowledgePolling()
		})
	}()
}

func (a *nativeApp) requestDeleteKnowledgeBase(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	k := &a.knowledge
	view := a.knowledgeViewByID(id)
	if view == nil {
		k.Status = "要删除的知识库不存在。"
		k.StatusIsError = true
		return
	}
	k.PendingDeleteID = id
	k.PendingDeleteName = view.KnowledgeBase.Name
	k.PendingDeleteRoot = view.KnowledgeBase.RootDir
	k.PendingDeleteOpen = true
}

func (a *nativeApp) cancelDeleteKnowledgeBase() {
	k := &a.knowledge
	k.PendingDeleteID = ""
	k.PendingDeleteName = ""
	k.PendingDeleteRoot = ""
	k.PendingDeleteOpen = false
}

func (a *nativeApp) confirmDeleteKnowledgeBase() {
	k := &a.knowledge
	id := strings.TrimSpace(k.PendingDeleteID)
	name := k.PendingDeleteName
	if id == "" {
		a.cancelDeleteKnowledgeBase()
		return
	}
	key := knowledgeDraftKey(id)
	if a.knowledgeMutationBlocked() {
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "删除失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	a.cancelDeleteKnowledgeBase()
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "delete")
	k.Status = "正在删除知识库配置和索引…"
	k.StatusIsError = false
	go func() {
		_, err := client.call("mothx/manage/knowledge-bases/delete", map[string]any{"id": id})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			if err != nil {
				a.clearKnowledgeBusy(key)
				k.Status = "删除知识库失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			a.removeKnowledgeView(id)
			k.Status = Tf("已删除知识库「%s」的配置和索引；知识源目录保持不变。", name)
			k.StatusIsError = false
			a.startKnowledgePanelLoad()
		})
	}()
}

func (a *nativeApp) requestClearKnowledge(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	k := &a.knowledge
	view := a.knowledgeViewByID(id)
	if view == nil {
		return
	}
	k.PendingClearID = id
	k.PendingClearName = view.KnowledgeBase.Name
	k.PendingClearOpen = true
}

func (a *nativeApp) cancelClearKnowledge() {
	k := &a.knowledge
	k.PendingClearID = ""
	k.PendingClearName = ""
	k.PendingClearOpen = false
}

func (a *nativeApp) confirmClearKnowledge() {
	k := &a.knowledge
	id := strings.TrimSpace(k.PendingClearID)
	if id == "" {
		a.cancelClearKnowledge()
		return
	}
	key := knowledgeDraftKey(id)
	if a.knowledgeMutationBlocked() {
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "清除索引失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	a.cancelClearKnowledge()
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "clear")
	k.Status = "正在清除知识库索引…"
	k.StatusIsError = false
	go func() {
		_, err := client.call("mothx/manage/knowledge-bases/clear", map[string]any{"id": id})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "清除知识库索引失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			if view := a.knowledgeViewByID(id); view != nil {
				view.Snapshot = nil
				view.Indexing = nil
				view.Status = "unindexed"
				view.KnowledgeBase.ActiveSnapshotID = ""
			}
			if insights := k.Insights[id]; insights != nil {
				insights.Sources = nil
				insights.SourcesLoaded = true
			}
			k.Status = "索引已清除，配置与知识源目录保持不变。"
			k.StatusIsError = false
			a.startKnowledgePanelLoad()
		})
	}()
}

func (a *nativeApp) toggleKnowledgeSchedule(id string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	key := knowledgeDraftKey(id)
	schedule, ok := k.Schedules[id]
	if !a.knowledgeCapable() || id == "" || !ok || a.knowledgeMutationBlocked() {
		return
	}
	client := a.acp
	if client == nil {
		k.Status = "更新扫描计划失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	enabled := !schedule.Enabled
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "schedule")
	k.Status = "正在更新扫描计划…"
	k.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/schedule", map[string]any{"id": id, "enabled": enabled})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "更新扫描计划失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			var updated knowledgeScheduleView
			if err := json.Unmarshal(raw, &updated); err != nil {
				k.Status = "扫描计划响应无效：" + err.Error()
				k.StatusIsError = true
				return
			}
			k.Schedules[id] = updated
			if draft := k.Drafts[id]; draft != nil {
				draft.Enabled = updated.Enabled
			}
			if view := a.knowledgeViewByID(id); view != nil {
				view.KnowledgeBase.Enabled = updated.Enabled
				if !updated.Enabled {
					view.Status = "disabled"
				}
			}
			k.Status = "扫描计划已更新"
			k.StatusIsError = false
			a.startKnowledgePanelLoad()
		})
	}()
}

func (a *nativeApp) applyKnowledgeMCP(id string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	key := knowledgeDraftKey(id)
	if !a.knowledgeCapable() || !a.knowledgeMCPCapable() || id == "" || a.knowledgeMutationBlocked() {
		return
	}
	entry, exists := a.knowledgeMCPEntry(id)
	enabled := !exists || !knowledgeMCPEnabled(entry)
	client := a.acp
	if client == nil {
		k.Status = "更新知识库 MCP 失败：尚未连接到 MothX ACP。"
		k.StatusIsError = true
		return
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.setKnowledgeBusy(key, "mcp")
	k.Status = "正在更新知识库 MCP 配置…"
	k.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/mcp/apply", map[string]any{"id": id, "enabled": enabled})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			k := &a.knowledge
			a.clearKnowledgeBusy(key)
			if err != nil {
				k.Status = "更新知识库 MCP 失败：" + err.Error()
				k.StatusIsError = true
				return
			}
			var result knowledgeMCPApplyResult
			if err := json.Unmarshal(raw, &result); err != nil {
				k.Status = "知识库 MCP 响应无效：" + err.Error()
				k.StatusIsError = true
				return
			}
			name := strings.TrimSpace(result.Name)
			if name == "" {
				name = knowledgeMCPName(id)
			}
			updated := false
			for index := range k.MCPServers {
				if strings.TrimSpace(k.MCPServers[index].Name) == name {
					enabledValue := result.Enabled
					k.MCPServers[index].Enabled = &enabledValue
					updated = true
					break
				}
			}
			if !updated {
				enabledValue := result.Enabled
				k.MCPServers = append(k.MCPServers, mcpServerView{Name: name, Type: "stdio", Enabled: &enabledValue})
			}
			k.Status = "知识库 MCP 已更新；新建或重新加载会话后生效。"
			k.StatusIsError = false
			a.startKnowledgePanelLoad()
		})
	}()
}

func (a *nativeApp) runKnowledgeQuery(id string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	insights := k.Insights[id]
	if !a.knowledgeCapable() || id == "" || insights == nil || insights.QueryBusy {
		return
	}
	query := strings.TrimSpace(insights.Query)
	if query == "" {
		insights.QueryErr = "请输入查询内容"
		return
	}
	client := a.acp
	if client == nil {
		insights.QueryErr = "查询失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	insights.QueryBusy = true
	insights.QueryErr = ""
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/query", map[string]any{
			"id": id, "query": query, "limit": 8,
		})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			insights := a.knowledge.Insights[id]
			if insights == nil {
				return
			}
			insights.QueryBusy = false
			if err != nil {
				insights.QueryErr = "查询失败：" + err.Error()
				return
			}
			var result knowledgeQueryResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				insights.QueryErr = "查询响应无效：" + err.Error()
				return
			}
			insights.QueryErr = ""
			insights.QueryResult = &result.Query
		})
	}()
}

// loadKnowledgeRunDetail projects one canonical index Run through the additive
// runs/get method. The list already carries bounded stats; this detail request
// adds run/snapshot identity and terminal diagnostics on demand.
func (a *nativeApp) loadKnowledgeRunDetail(id, runID string) {
	k := &a.knowledge
	id = strings.TrimSpace(id)
	runID = strings.TrimSpace(runID)
	insights := k.Insights[id]
	if !a.knowledgeRunsCapable() || id == "" || runID == "" || insights == nil || insights.RunDetailBusy {
		return
	}
	client := a.acp
	if client == nil {
		insights.RunDetailErr = "读取运行详情失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	insights.RunDetailBusy = true
	insights.RunDetailID = runID
	insights.RunDetailErr = ""
	go func() {
		raw, err := client.call("mothx/manage/knowledge-bases/runs/get", map[string]any{"id": id, "runId": runID})
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			insights := a.knowledge.Insights[id]
			if insights == nil {
				return
			}
			insights.RunDetailBusy = false
			if err != nil {
				insights.RunDetailErr = "读取运行详情失败：" + err.Error()
				return
			}
			var response struct {
				Run knowledgeIndexRunView `json:"run"`
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

// startKnowledgePolling starts one poll loop for the currently running index
// jobs. It reads only the seq/poll tokens from the worker; all view mutation is
// dispatched to the UI thread.
func (a *nativeApp) startKnowledgePolling() {
	k := &a.knowledge
	if k.Polling || !k.Loaded || !a.settingsOpen || a.settingsTab != "knowledge" {
		return
	}
	ids := make([]string, 0, len(k.Views))
	for _, view := range k.Views {
		if view.Indexing != nil && view.Indexing.Running {
			if id := strings.TrimSpace(view.KnowledgeBase.ID); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	k.Polling = true
	pollToken := atomic.AddInt64(&k.PollSeq, 1)
	seq := atomic.LoadInt64(&k.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.pollKnowledgeIndexes(client, ids, seq, startReloadSeq, pollToken)
}

func (a *nativeApp) pollKnowledgeIndexes(client *acpClient, ids []string, seq, startReloadSeq, pollToken int64) {
	for {
		time.Sleep(knowledgePollInterval)
		if pollToken != atomic.LoadInt64(&a.knowledge.PollSeq) ||
			seq != atomic.LoadInt64(&a.knowledge.Seq) {
			return
		}
		anyRunning := false
		okCount := 0
		for _, id := range ids {
			raw, err := client.call("mothx/manage/knowledge-bases/status", map[string]any{"id": id})
			if err != nil {
				continue
			}
			var view knowledgeBaseView
			if json.Unmarshal(raw, &view) != nil || strings.TrimSpace(view.KnowledgeBase.ID) == "" {
				continue
			}
			okCount++
			if view.Indexing != nil && view.Indexing.Running {
				anyRunning = true
			}
			updated := view
			a.dispatch(func() {
				if !a.knowledgePanelCurrent(seq, startReloadSeq) {
					return
				}
				a.applyKnowledgeView(updated)
			})
		}
		if okCount == 0 || !anyRunning {
			a.dispatch(func() {
				if a.knowledgePanelCurrent(seq, startReloadSeq) {
					a.knowledge.Polling = false
				}
			})
			return
		}
	}
}

// knowledgeStatusText mirrors the Desktop panel's status precedence: disabled,
// running background index, missing snapshot, then the snapshot status.
func knowledgeStatusText(view knowledgeBaseView) string {
	base := view.KnowledgeBase
	if !base.Enabled {
		return T("已停用")
	}
	if view.Indexing != nil && view.Indexing.Running {
		label := T("索引中")
		if phase := strings.TrimSpace(view.Indexing.Phase); phase != "" {
			label = Tf("索引中：%s", phase)
		}
		if view.Indexing.FilesTotal > 0 {
			label += fmt.Sprintf(" · %d/%d", view.Indexing.FilesDone, view.Indexing.FilesTotal)
		}
		return label
	}
	if view.Snapshot == nil {
		return T("尚未建立可用索引")
	}
	status := strings.TrimSpace(view.Status)
	if status == "" {
		status = strings.TrimSpace(view.Snapshot.Status)
	}
	if status == "" {
		return T("尚未建立可用索引")
	}
	return Tf("索引状态：%s", status)
}

func knowledgeStatsText(snapshot *knowledgeSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return Tf("文件 %d · 分块 %d · 节点 %d · 关系 %d",
		snapshot.FileCount, snapshot.ChunkCount, snapshot.NodeCount, snapshot.EdgeCount)
}

func knowledgeRunStatsText(run knowledgeIndexRunView) string {
	return Tf("文件 %d · 分块 %d · 节点 %d · 关系 %d",
		run.FileCount, run.ChunkCount, run.NodeCount, run.EdgeCount)
}

func knowledgeScheduleText(schedule knowledgeScheduleView) string {
	if schedule.Schedule == "" {
		return T("仅手动扫描")
	}
	if schedule.Configured && schedule.NextRun != "" {
		if next := knowledgeFormatDate(schedule.NextRun); next != "" {
			return T("下次运行：") + next
		}
	}
	return T("仅手动扫描")
}

func knowledgeFormatDate(value string) string {
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

func knowledgeFormatSize(bytes int64) string {
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

func knowledgeSourceMeta(source knowledgeSourceView) string {
	return Tf("%s · %s · %d 个分块", T(source.Status), knowledgeFormatSize(source.ByteSize), source.ChunkCount)
}

func knowledgeTruncate(text string, limit int) string {
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

// knowledgeChooseRoot opens the native directory picker and writes the chosen
// path into the current draft only if the panel and sequence are still current.
func (a *nativeApp) knowledgeChooseRoot(key string) {
	win := a.win
	seq := atomic.LoadInt64(&a.knowledge.Seq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择知识源目录"),
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.dispatch(func() {
			if !a.knowledgePanelCurrent(seq, startReloadSeq) {
				return
			}
			draft := a.knowledge.Drafts[key]
			if draft == nil {
				return
			}
			draft.RootDir = path
		})
	}()
}
