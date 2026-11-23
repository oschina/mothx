// Package env owns the Native projection of the ACP environment-variable
// management contract (mothx/manage/env/get and mothx/manage/env/patch).
//
// The wire contract is deliberately write-only for values: get returns sorted
// names plus a configured flag, never a value. The panel therefore keeps only
// local drafts (empty by default) and submits one atomic set/unset patch. A
// configured value must never be reconstructed from a response or kept in
// client state after a save.
package env

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// VariableView mirrors one manageEnvVariableView entry.
type VariableView struct {
	Name            string `json:"name"`
	ValueConfigured bool   `json:"valueConfigured"`
}

// View mirrors the mothx/manage/env get/patch response.
type View struct {
	Variables []VariableView `json:"variables"`
}

// Draft is the local edit state of one variable. Value is empty until the
// user types a replacement; Touched marks that replacement for the next patch.
type Draft struct {
	Value   string
	Deleted bool
	IsNew   bool
	Touched bool
}

// Host exposes only the shell services this panel needs.
type Host = host.Base

// State is the env panel's projected state.
type State struct {
	Variables []VariableView
	Drafts    map[string]*Draft

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

// Panel couples the env state with its ACP lifecycle.
type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{Drafts: make(map[string]*Draft)}}
}

func (p *Panel) Capable(h Host) bool {
	return h.FeatureEnabled("manageEnv")
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "env" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight env response
// stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Variables = nil
	p.Drafts = make(map[string]*Draft)
	p.NewName = ""
	p.NewValue = ""
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
}

// StartLoad starts one capability-gated variable read. It is called from the
// UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.SettingsOpen() || h.SettingsTab() != "env" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持环境变量管理（manageEnv）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
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
	raw, err := client.Call("mothx/manage/env/get", map[string]any{})
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取环境变量失败：" + err.Error()
			return
		}
		var view View
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "环境变量响应无效：" + err.Error()
			return
		}
		p.ApplyView(&view)
	})
}

// ApplyView replaces the catalog with server state. Values are never part of
// the response, so every draft starts empty and untouched.
func (p *Panel) ApplyView(view *View) {
	if view == nil {
		return
	}
	variables := append([]VariableView(nil), view.Variables...)
	sort.SliceStable(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	p.Variables = variables
	p.Drafts = make(map[string]*Draft, len(variables))
	for _, variable := range variables {
		p.Drafts[variable.Name] = &Draft{}
	}
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// VisibleNames returns the saved names that are not queued for removal.
func (p *Panel) VisibleNames() []string {
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

// Add stages one new variable. It never contacts ACP; the patch is submitted
// by SaveChanges.
func (p *Panel) Add() {
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
			p.Status = i18n.Tf("变量 %s 已存在；请直接编辑已配置的值。", name)
			p.StatusIsError = true
			return
		}
	} else {
		p.Drafts[name] = &Draft{Value: value, IsNew: true, Touched: true}
	}
	p.NewName = ""
	p.NewValue = ""
	p.Status = "已暂存新变量，保存后写入 env.json。"
	p.StatusIsError = false
}

// Remove stages removal. A staged-but-unsaved name is dropped immediately; a
// saved name becomes unset in the next patch.
func (p *Panel) Remove(name string) {
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

// BuildPatchRequest projects local drafts into the atomic set/unset patch the
// ACP handler accepts. It never round-trips unchanged values: only new or
// explicitly edited variables are set and only marked variables are unset.
func BuildPatchRequest(drafts map[string]*Draft) (map[string]any, error) {
	set := make([]map[string]string, 0, len(drafts))
	unset := make([]string, 0, len(drafts))
	seen := make(map[string]struct{}, len(drafts))
	for name, draft := range drafts {
		if draft == nil {
			continue
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return nil, i18n.LocalizedError("变量名不能为空")
		}
		if _, exists := seen[trimmed]; exists {
			return nil, i18n.LocalizedErrorf("变量 %s 重复", trimmed)
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

// SaveChanges submits one patch. A no-op patch stays local instead of producing
// an empty ACP request the server would reject.
func (p *Panel) SaveChanges(h Host) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持环境变量管理（manageEnv）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Saving {
		return
	}
	request, err := BuildPatchRequest(p.Drafts)
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
	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = true
	p.Status = "正在保存环境变量…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/env/patch", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				p.Status = "保存环境变量失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view View
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后环境变量响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyView(&view)
			p.Status = "环境变量已保存"
			p.StatusIsError = false
		})
	}()
}

// SummaryLabel keeps the secret-free settings entry wording in one place.
func SummaryLabel(count int) string {
	if count == 0 {
		return "尚未配置环境变量"
	}
	return i18n.Tf("%d 个环境变量已配置（值不回显）", count)
}
