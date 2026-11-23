// Package skills owns the Native projection of the ACP skills management
// contract (mothx/manage/skills/list and mothx/manage/skills/set).
package skills

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP skills management contract
// (mothx/manage/skills/list and mothx/manage/skills/set). Skills are
// discovered by the Runtime from built-in, global, and workspace directories;
// this panel only projects the result and toggles the persisted disabled list.

// SkillView is one skill from mothx/manage/skills/list. Enabled is a pointer
// so an omitted field keeps the Runtime's meaning of "enabled".
type SkillView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// ListView is the list response envelope. Cwd echoes the workspace used
// for project skill discovery.
type ListView struct {
	Cwd    string      `json:"cwd,omitempty"`
	Skills []SkillView `json:"skills"`
}

type State struct {
	Cwd     string
	Skills  []SkillView
	Loaded  bool
	Loading bool
	Err     string

	Toggling      string
	Status        string
	StatusIsError bool

	// Seq guards every asynchronous skills call. Bumping it invalidates any
	// in-flight list/toggle response, which is how tab exits and reconnects
	// prevent stale state from landing.
	Seq int64
}

type Host interface {
	host.Base
	PanelVisible(page, tab string) bool
	SelectedWorkspaceCwd() string
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{Skills: []SkillView{}}}
}

func Enabled(skill SkillView) bool {
	return skill.Enabled == nil || *skill.Enabled
}

func Name(skill SkillView) string {
	return strings.TrimSpace(skill.Name)
}

func Description(skill SkillView) string {
	parts := make([]string, 0, 2)
	if description := strings.TrimSpace(skill.Description); description != "" {
		parts = append(parts, description)
	}
	if source := strings.TrimSpace(skill.Source); source != "" {
		parts = append(parts, source)
	}
	return strings.Join(parts, " · ")
}

// RequestCwd resolves the workspace cwd passed to skills/list and
// skills/set. The selected workspace is authoritative; draft and process
// fallbacks keep the panel usable before a session is loaded.
func RequestCwd(workspace, draftCwd, processCwd string) string {
	for _, candidate := range []string{workspace, draftCwd, processCwd} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if abs, err := filepath.Abs(candidate); err == nil {
			return abs
		}
		return candidate
	}
	return ""
}

func BuildListRequest(cwd string) map[string]any {
	return map[string]any{"cwd": cwd}
}

func BuildSetRequest(cwd, name string, enabled bool) map[string]any {
	return map[string]any{
		"cwd":     cwd,
		"name":    strings.TrimSpace(name),
		"enabled": enabled,
	}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageSkills")
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.PanelVisible("skills", "skills") &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight skills
// response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Cwd = ""
	p.Skills = nil
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Toggling = ""
	p.Status = ""
	p.StatusIsError = false
}

// StartLoad starts one capability-gated skills load for the
// selected workspace. It is called from the UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.PanelVisible("skills", "skills") {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持技能管理（manageSkills）。"
		return
	}
	cwd := h.SelectedWorkspaceCwd()
	if cwd == "" {
		p.Loaded = true
		p.Err = "无法解析当前工作区路径。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq, cwd)
}

func (p *Panel) load(h Host, seq, startReloadSeq int64, cwd string) {
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
	raw, err := client.Call("mothx/manage/skills/list", BuildListRequest(cwd))
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取技能列表失败：" + err.Error()
			return
		}
		var view ListView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "技能列表响应无效：" + err.Error()
			return
		}
		p.ApplyListView(&view, cwd)
	})
}

func (p *Panel) ApplyListView(view *ListView, workspaceCwd string) {
	if view == nil {
		return
	}
	skills := view.Skills
	if skills == nil {
		skills = []SkillView{}
	}
	cwd := strings.TrimSpace(view.Cwd)
	if cwd == "" {
		cwd = workspaceCwd
	}
	p.Cwd = cwd
	p.Skills = skills
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// Toggle flips one skill through mothx/manage/skills/set, then reloads
// the list so descriptions and source stay Runtime-authoritative. Only one
// toggle can be in flight at a time because the Runtime persists a single
// disabled-skill list.
func (p *Panel) Toggle(h Host, index int) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持技能管理（manageSkills）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Toggling != "" {
		return
	}
	if index < 0 || index >= len(p.Skills) {
		p.Status = "要切换的技能不存在。"
		p.StatusIsError = true
		return
	}
	skill := p.Skills[index]
	name := Name(skill)
	if name == "" {
		p.Status = "要切换的技能名称不能为空。"
		p.StatusIsError = true
		return
	}
	cwd := h.SelectedWorkspaceCwd()
	if cwd == "" {
		p.Status = "无法解析当前工作区路径。"
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "切换技能失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}

	target := !Enabled(skill)
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Toggling = name
	if target {
		p.Status = i18n.Tf("正在启用技能 %s…", name)
	} else {
		p.Status = i18n.Tf("正在停用技能 %s…", name)
	}
	p.StatusIsError = false
	go func() {
		_, setErr := client.Call("mothx/manage/skills/set", BuildSetRequest(cwd, name, target))
		var listView *ListView
		var listErr error
		if setErr == nil {
			raw, err := client.Call("mothx/manage/skills/list", BuildListRequest(cwd))
			if err != nil {
				listErr = err
			} else {
				var view ListView
				if err := json.Unmarshal(raw, &view); err != nil {
					listErr = err
				} else {
					listView = &view
				}
			}
		}
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Toggling = ""
			if setErr != nil {
				p.Status = "切换技能失败：" + setErr.Error()
				p.StatusIsError = true
				return
			}
			if listView != nil {
				p.ApplyListView(listView, cwd)
			} else {
				p.ApplyToggleLocally(name, target)
			}
			switch {
			case target:
				p.Status = i18n.Tf("技能 %s 已启用", name)
			default:
				p.Status = i18n.Tf("技能 %s 已停用", name)
			}
			p.StatusIsError = false
			if listErr != nil {
				p.Status += "；刷新技能列表失败：" + listErr.Error()
				p.StatusIsError = true
			}
		})
	}()
}

// ApplyToggleLocally keeps the visible row in sync when skills/set
// succeeded but the follow-up list refresh failed.
func (p *Panel) ApplyToggleLocally(name string, enabled bool) {
	for index := range p.Skills {
		if Name(p.Skills[index]) != name {
			continue
		}
		value := enabled
		p.Skills[index].Enabled = &value
		return
	}
}

// ToggleStatus is the shared in-flight label for a skill toggle; it keeps
// the dynamic status translatable regardless of when the locale was set.
func ToggleStatus(name string, target bool) string {
	if target {
		return i18n.Tf("正在启用技能 %s…", name)
	}
	return i18n.Tf("正在停用技能 %s…", name)
}
