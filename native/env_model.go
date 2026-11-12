package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP environment-variable
// management contract (mothx/manage/env/get and mothx/manage/env/patch).
//
// The wire contract is deliberately write-only for values: get returns sorted
// names plus a configured flag, never a value. The panel therefore keeps only
// local drafts (empty by default) and submits one atomic set/unset patch. A
// configured value must never be reconstructed from a response or kept in
// client state after a save.

// envVariableView mirrors one manageEnvVariableView entry.
type envVariableView struct {
	Name            string `json:"name"`
	ValueConfigured bool   `json:"valueConfigured"`
}

// envView mirrors the mothx/manage/env get/patch response.
type envView struct {
	Variables []envVariableView `json:"variables"`
}

// envDraft is the local edit state of one variable. Value is empty until the
// user types a replacement; Touched marks that replacement for the next patch.
type envDraft struct {
	Value   string
	Deleted bool
	IsNew   bool
	Touched bool
}

type envPanelState struct {
	Variables []envVariableView
	Drafts    map[string]*envDraft

	NewName  string
	NewValue string

	Loaded  bool
	Loading bool
	Saving  bool
	Err     string
	Status  string

	StatusIsError bool

	// Seq guards every asynchronous env call. Bumping it invalidates any
	// in-flight get/patch response, which is how tab exits, settings close,
	// capability fallback, and reconnects prevent stale state from landing.
	Seq int64
}

func newEnvPanelState() envPanelState {
	return envPanelState{Drafts: make(map[string]*envDraft)}
}

func (a *nativeApp) envCapable() bool {
	return a.featureEnabled("manageEnv")
}

func (a *nativeApp) envPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "env" &&
		seq == atomic.LoadInt64(&a.env.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateEnvPanel drops transient state and makes every in-flight env
// response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateEnvPanel() {
	p := &a.env
	atomic.AddInt64(&p.Seq, 1)
	p.Variables = nil
	p.Drafts = make(map[string]*envDraft)
	p.NewName = ""
	p.NewValue = ""
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
}

// startEnvPanelLoad starts one capability-gated variable read. It is called
// from the UI thread on tab entry.
func (a *nativeApp) startEnvPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "env" {
		return
	}
	p := &a.env
	if p.Loading {
		return
	}
	if !a.envCapable() {
		p.Loaded = true
		p.Err = "当前运行时不支持环境变量管理（manageEnv）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadEnvPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadEnvPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.envPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.env.Loading = false
			a.env.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/env/get", map[string]any{})
	a.dispatch(func() {
		if !a.envPanelCurrent(seq, startReloadSeq) {
			return
		}
		p := &a.env
		p.Loading = false
		if err != nil {
			p.Err = "读取环境变量失败：" + err.Error()
			return
		}
		var view envView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "环境变量响应无效：" + err.Error()
			return
		}
		a.applyEnvView(&view)
	})
}

// applyEnvView replaces the catalog with server state. Values are never
// part of the response, so every draft starts empty and untouched.
func (a *nativeApp) applyEnvView(view *envView) {
	if view == nil {
		return
	}
	p := &a.env
	variables := append([]envVariableView(nil), view.Variables...)
	sort.SliceStable(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	p.Variables = variables
	p.Drafts = make(map[string]*envDraft, len(variables))
	for _, variable := range variables {
		p.Drafts[variable.Name] = &envDraft{}
	}
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// visibleEnvNames returns the saved names that are not queued for removal.
func (p *envPanelState) visibleEnvNames() []string {
	names := make([]string, 0, len(p.Drafts))
	for name, draft := range p.Drafts {
		if draft == nil || draft.Deleted {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// addEnvVariable stages one new variable. It never contacts ACP; the patch is
// submitted by saveEnvChanges.
func (a *nativeApp) addEnvVariable() {
	p := &a.env
	name := strings.TrimSpace(p.NewName)
	value := p.NewValue
	if name == "" {
		p.Status = "变量名不能为空。"
		p.StatusIsError = true
		return
	}
	existing, ok := p.Drafts[name]
	if ok && existing != nil && !existing.Deleted {
		if existing.IsNew {
			// Re-adding a staged name replaces its staged value.
			existing.Value = value
			existing.Touched = true
		} else {
			p.Status = Tf("变量 %s 已存在；请直接编辑已配置的值。", name)
			p.StatusIsError = true
			return
		}
	} else {
		p.Drafts[name] = &envDraft{Value: value, IsNew: true, Touched: true}
	}
	p.NewName = ""
	p.NewValue = ""
	p.Status = "已暂存新变量，保存后写入 env.json。"
	p.StatusIsError = false
}

// removeEnvVariable stages removal. A staged-but-unsaved name is dropped
// immediately; a saved name becomes unset in the next patch.
func (a *nativeApp) removeEnvVariable(name string) {
	p := &a.env
	draft, ok := p.Drafts[name]
	if !ok || draft == nil {
		return
	}
	if draft.IsNew {
		delete(p.Drafts, name)
		p.Status = ""
		p.StatusIsError = false
		return
	}
	draft.Deleted = true
	draft.Value = ""
	draft.Touched = false
	p.Status = "已标记删除，保存后从 env.json 移除。"
	p.StatusIsError = false
}

// buildEnvPatchRequest projects local drafts into the atomic set/unset patch
// the ACP handler accepts. It never round-trips unchanged values: only new or
// explicitly edited variables are set and only marked variables are unset.
func buildEnvPatchRequest(drafts map[string]*envDraft) (map[string]any, error) {
	set := make([]map[string]string, 0, len(drafts))
	unset := make([]string, 0, len(drafts))
	seen := make(map[string]struct{}, len(drafts))
	for name, draft := range drafts {
		if draft == nil {
			continue
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, localizedError("变量名不能为空")
		}
		if _, exists := seen[trimmed]; exists {
			return nil, localizedErrorf("变量 %s 重复", trimmed)
		}
		seen[trimmed] = struct{}{}
		switch {
		case draft.Deleted:
			unset = append(unset, trimmed)
		case draft.IsNew || draft.Touched:
			set = append(set, map[string]string{"name": trimmed, "value": draft.Value})
		}
	}
	if len(set) == 0 && len(unset) == 0 {
		return nil, nil
	}
	sort.Slice(set, func(i, j int) bool { return set[i]["name"] < set[j]["name"] })
	sort.Strings(unset)
	return map[string]any{"set": set, "unset": unset}, nil
}

// saveEnvChanges submits one patch. A no-op patch stays local instead of
// producing an empty ACP request the server would reject.
func (a *nativeApp) saveEnvChanges() {
	p := &a.env
	if !a.envCapable() {
		p.Status = "当前运行时不支持环境变量管理（manageEnv）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Saving {
		return
	}
	request, err := buildEnvPatchRequest(p.Drafts)
	if err != nil {
		p.Status = "保存失败：" + err.Error()
		p.StatusIsError = true
		return
	}
	if request == nil {
		p.Status = "没有需要保存的变更"
		p.StatusIsError = false
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
	p.Saving = true
	p.Status = "正在保存环境变量…"
	p.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/env/patch", request)
		a.dispatch(func() {
			if !a.envPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.env
			p.Saving = false
			if err != nil {
				p.Status = "保存环境变量失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view envView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后环境变量响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			a.applyEnvView(&view)
			p.Status = "环境变量已保存"
			p.StatusIsError = false
		})
	}()
}
