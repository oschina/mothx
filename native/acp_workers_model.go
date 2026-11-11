package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP worker profile management
// contract (mothx/manage/acp/profiles/list and
// mothx/manage/acp/profiles/patch). It projects the same narrow editor the
// Desktop AcpWorkersPanel exposes: model, driver, MCP bridge intent, and
// capability tightening. Argv, URLs, environment names, and trust flags stay
// operator-owned settings.json fields and never enter this panel.

var acpWorkerMCPBridges = []string{"auto", "off"}

// acpWorkerProfileView mirrors agentruntime.ACPWorkerProfileDescriptor. It is
// intentionally read-only for execution details; the panel never renders argv,
// environment names, credentials, or configuration paths.
type acpWorkerProfileView struct {
	ID                            string   `json:"id"`
	Title                         string   `json:"title,omitempty"`
	Transport                     string   `json:"transport,omitempty"`
	Model                         string   `json:"model,omitempty"`
	Enabled                       bool     `json:"enabled"`
	RouterEligible                bool     `json:"routerEligible"`
	PermissionPolicy              string   `json:"permissionPolicy,omitempty"`
	MaxConcurrentChildren         int      `json:"maxConcurrentChildren,omitempty"`
	MaxConcurrentChildrenEnforced int      `json:"maxConcurrentChildrenEnforced,omitempty"`
	ParallelBaseWorkspaceWrites   bool     `json:"parallelBaseWorkspaceWrites,omitempty"`
	Driver                        string   `json:"driver,omitempty"`
	MCPBridge                     string   `json:"mcpBridge,omitempty"`
	CapabilityOverrides           []string `json:"capabilityOverrides,omitempty"`
	InvalidReason                 string   `json:"invalidReason,omitempty"`
}

// acpWorkerCatalogView mirrors the profiles/list response envelope.
type acpWorkerCatalogView struct {
	Profiles        []acpWorkerProfileView `json:"profiles"`
	CapabilityNames []string               `json:"capabilityNames"`
}

// acpWorkerProfilesView mirrors the profiles/patch response envelope. The
// patch response republishes the catalog but not the capability vocabulary, so
// the panel keeps the names it already loaded.
type acpWorkerProfilesView struct {
	Profiles []acpWorkerProfileView `json:"profiles"`
}

// acpWorkerDraft is the editable state of one profile. Overrides is the set of
// capabilities the profile currently tightens; sending it as false preserves
// the Runtime's "tighten only" rule.
type acpWorkerDraft struct {
	Model     string
	Driver    string
	MCPBridge string
	Overrides map[string]bool
}

func acpWorkerDraftFromProfile(profile acpWorkerProfileView) acpWorkerDraft {
	overrides := make(map[string]bool, len(profile.CapabilityOverrides))
	for _, name := range profile.CapabilityOverrides {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			overrides[trimmed] = true
		}
	}
	driver := strings.TrimSpace(profile.Driver)
	if driver == "" {
		driver = "generic-acp"
	}
	bridge := strings.TrimSpace(profile.MCPBridge)
	if bridge == "" {
		bridge = "auto"
	}
	return acpWorkerDraft{
		Model:     profile.Model,
		Driver:    driver,
		MCPBridge: bridge,
		Overrides: overrides,
	}
}

type acpWorkersPanelState struct {
	Profiles        []acpWorkerProfileView
	CapabilityNames []string
	Drafts          map[string]*acpWorkerDraft

	Loaded  bool
	Loading bool
	// SavingID names the profile whose patch is in flight; empty means idle.
	SavingID string
	Err      string
	Status   string

	StatusIsError bool

	// Seq guards every asynchronous ACP worker call. Bumping it invalidates any
	// in-flight list/patch response, which is how tab exits, settings close,
	// capability fallback, and reconnects prevent stale state from landing.
	Seq int64
}

func newACPWorkersPanelState() acpWorkersPanelState {
	return acpWorkersPanelState{Drafts: make(map[string]*acpWorkerDraft)}
}

func (a *nativeApp) acpWorkersCapable() bool {
	return a.featureEnabled("manageACPProfiles")
}

func (a *nativeApp) acpWorkersPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "acpWorkers" &&
		seq == atomic.LoadInt64(&a.acpWorkers.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateACPWorkersPanel drops transient state and makes every in-flight
// ACP worker response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateACPWorkersPanel() {
	p := &a.acpWorkers
	atomic.AddInt64(&p.Seq, 1)
	p.Profiles = nil
	p.CapabilityNames = nil
	p.Drafts = make(map[string]*acpWorkerDraft)
	p.Loaded = false
	p.Loading = false
	p.SavingID = ""
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
}

// startACPWorkersPanelLoad starts one capability-gated profile catalog read.
// It is called from the UI thread on tab entry.
func (a *nativeApp) startACPWorkersPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "acpWorkers" {
		return
	}
	p := &a.acpWorkers
	if p.Loading {
		return
	}
	if !a.acpWorkersCapable() {
		p.Loaded = true
		p.Err = "当前运行时不支持 ACP 工作器配置管理（manageACPProfiles）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadACPWorkersPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadACPWorkersPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.acpWorkersPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.acpWorkers.Loading = false
			a.acpWorkers.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/acp/profiles/list", map[string]any{})
	a.dispatch(func() {
		if !a.acpWorkersPanelCurrent(seq, startReloadSeq) {
			return
		}
		p := &a.acpWorkers
		p.Loading = false
		if err != nil {
			p.Err = "读取 ACP 工作器配置失败：" + err.Error()
			return
		}
		var view acpWorkerCatalogView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "ACP 工作器配置响应无效：" + err.Error()
			return
		}
		a.applyACPWorkerCatalog(&view)
	})
}

func (a *nativeApp) applyACPWorkerCatalog(view *acpWorkerCatalogView) {
	if view == nil {
		return
	}
	p := &a.acpWorkers
	profiles := view.Profiles
	if profiles == nil {
		profiles = []acpWorkerProfileView{}
	}
	names := view.CapabilityNames
	if names == nil {
		names = []string{}
	}
	p.Profiles = profiles
	p.CapabilityNames = names
	p.rebuildDrafts()
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// applyACPWorkerProfiles adopts the patched catalog while keeping the
// capability vocabulary from the list response.
func (a *nativeApp) applyACPWorkerProfiles(profiles []acpWorkerProfileView) {
	p := &a.acpWorkers
	if profiles == nil {
		profiles = []acpWorkerProfileView{}
	}
	p.Profiles = profiles
	p.rebuildDrafts()
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

func (p *acpWorkersPanelState) rebuildDrafts() {
	drafts := make(map[string]*acpWorkerDraft, len(p.Profiles))
	for _, profile := range p.Profiles {
		if strings.TrimSpace(profile.ID) == "" {
			continue
		}
		draft := acpWorkerDraftFromProfile(profile)
		drafts[profile.ID] = &draft
	}
	p.Drafts = drafts
}

func (a *nativeApp) acpWorkerDraft(id string) (acpWorkerDraft, bool) {
	draft, ok := a.acpWorkers.Drafts[id]
	if !ok || draft == nil {
		return acpWorkerDraft{}, false
	}
	return *draft, true
}

func (a *nativeApp) acpWorkerProfile(id string) (acpWorkerProfileView, bool) {
	for _, profile := range a.acpWorkers.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return acpWorkerProfileView{}, false
}

func (a *nativeApp) updateACPWorkerDraft(id string, mutate func(*acpWorkerDraft)) {
	draft, ok := a.acpWorkers.Drafts[id]
	if !ok || draft == nil {
		profile, found := a.acpWorkerProfile(id)
		if !found {
			return
		}
		fresh := acpWorkerDraftFromProfile(profile)
		draft = &fresh
		a.acpWorkers.Drafts[id] = draft
	}
	mutate(draft)
}

func (a *nativeApp) toggleACPWorkerCapability(id, name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	a.updateACPWorkerDraft(id, func(draft *acpWorkerDraft) {
		if draft.Overrides == nil {
			draft.Overrides = make(map[string]bool)
		}
		if draft.Overrides[name] {
			delete(draft.Overrides, name)
			return
		}
		draft.Overrides[name] = true
	})
}

// buildACPWorkerPatchRequest projects one draft into the narrow patch surface.
// It never emits command/args/env/url/trust fields; the ACP handler and config
// validator remain the final authority.
func buildACPWorkerPatchRequest(profile acpWorkerProfileView, draft acpWorkerDraft) (map[string]any, error) {
	id := strings.TrimSpace(profile.ID)
	if id == "" {
		return nil, fmt.Errorf("ACP 工作器 ID 不能为空")
	}
	bridge := strings.TrimSpace(draft.MCPBridge)
	if bridge == "" {
		bridge = "auto"
	}
	allowed := false
	for _, option := range acpWorkerMCPBridges {
		if option == bridge {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("MCP 桥接模式必须是 auto 或 off")
	}
	overrides := make(map[string]bool, len(draft.Overrides))
	for name, tightened := range draft.Overrides {
		name = strings.TrimSpace(name)
		if name == "" || !tightened {
			continue
		}
		// Runtime validation only accepts false values: every override here
		// tightens an advertised capability rather than widening authority.
		overrides[name] = false
	}
	return map[string]any{
		"id":                  id,
		"model":               strings.TrimSpace(draft.Model),
		"driver":              strings.TrimSpace(draft.Driver),
		"mcpBridge":           bridge,
		"capabilityOverrides": overrides,
	}, nil
}

func (a *nativeApp) saveACPWorkerProfile(id string) {
	p := &a.acpWorkers
	if !a.acpWorkersCapable() {
		p.Status = "当前运行时不支持 ACP 工作器配置管理（manageACPProfiles）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.SavingID != "" {
		return
	}
	profile, ok := a.acpWorkerProfile(id)
	if !ok {
		p.Status = "要保存的 ACP 工作器不存在。"
		p.StatusIsError = true
		return
	}
	draft, ok := a.acpWorkerDraft(id)
	if !ok {
		draft = acpWorkerDraftFromProfile(profile)
	}
	request, err := buildACPWorkerPatchRequest(profile, draft)
	if err != nil {
		p.Status = "保存失败：" + err.Error()
		p.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}

	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.SavingID = id
	p.Status = "正在保存 ACP 工作器配置…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/acp/profiles/patch", request)
		a.dispatch(func() {
			if !a.acpWorkersPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.acpWorkers
			p.SavingID = ""
			if err != nil {
				p.Status = "保存 ACP 工作器配置失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view acpWorkerProfilesView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后 ACP 工作器配置响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			a.applyACPWorkerProfiles(view.Profiles)
			p.Status = "ACP 工作器配置已保存"
			p.StatusIsError = false
		})
	}()
}
