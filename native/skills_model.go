package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP skills management contract
// (mothx/manage/skills/list and mothx/manage/skills/set). Skills are
// discovered by the Runtime from built-in, global, and workspace directories;
// this panel only projects the result and toggles the persisted disabled list.

// skillView is one skill from mothx/manage/skills/list. Enabled is a pointer
// so an omitted field keeps the Runtime's meaning of "enabled".
type skillView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// skillListView is the list response envelope. Cwd echoes the workspace used
// for project skill discovery.
type skillListView struct {
	Cwd    string      `json:"cwd,omitempty"`
	Skills []skillView `json:"skills"`
}

type skillsPanelState struct {
	Cwd     string
	Skills  []skillView
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

func newSkillsPanelState() skillsPanelState {
	return skillsPanelState{Skills: []skillView{}}
}

func skillEnabled(skill skillView) bool {
	return skill.Enabled == nil || *skill.Enabled
}

// commandKind reads the additive ACP command kind metadata. Commands without
// metadata, or with an unknown kind, stay plain commands; only the Runtime's
// explicit "skill" marker changes the card semantics.
func commandKind(command command) string {
	if raw, ok := command.Meta["mothx.dev"]; ok {
		var meta struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			if kind := strings.TrimSpace(meta.Kind); kind != "" {
				return kind
			}
		}
	}
	return "command"
}

func skillName(skill skillView) string {
	return strings.TrimSpace(skill.Name)
}

func skillDescription(skill skillView) string {
	parts := make([]string, 0, 2)
	if description := strings.TrimSpace(skill.Description); description != "" {
		parts = append(parts, description)
	}
	if source := strings.TrimSpace(skill.Source); source != "" {
		parts = append(parts, source)
	}
	return strings.Join(parts, " · ")
}

// skillsRequestCwd resolves the workspace cwd passed to skills/list and
// skills/set. The selected workspace is authoritative; draft and process
// fallbacks keep the panel usable before a session is loaded.
func skillsRequestCwd(workspace, draftCwd, processCwd string) string {
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

func (a *nativeApp) selectedWorkspaceCwd() string {
	return skillsRequestCwd(a.workspace, a.draftCwd, a.cwd)
}

func buildSkillsListRequest(cwd string) map[string]any {
	return map[string]any{"cwd": cwd}
}

func buildSkillSetRequest(cwd, name string, enabled bool) map[string]any {
	return map[string]any{
		"cwd":     cwd,
		"name":    strings.TrimSpace(name),
		"enabled": enabled,
	}
}

func (a *nativeApp) skillsCapable() bool {
	return a.featureEnabled("manageSkills")
}

func (a *nativeApp) skillsPanelCurrent(seq, reloadSeq int64) bool {
	return a.panelVisible("skills", "skills") &&
		seq == atomic.LoadInt64(&a.skills.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateSkillsPanel drops transient state and makes every in-flight skills
// response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateSkillsPanel() {
	s := &a.skills
	atomic.AddInt64(&s.Seq, 1)
	s.Cwd = ""
	s.Skills = nil
	s.Loaded = false
	s.Loading = false
	s.Err = ""
	s.Toggling = ""
	s.Status = ""
	s.StatusIsError = false
}

// startSkillsPanelLoad starts one capability-gated skills load for the
// selected workspace. It is called from the UI thread on tab entry.
func (a *nativeApp) startSkillsPanelLoad() {
	if !a.panelVisible("skills", "skills") {
		return
	}
	s := &a.skills
	if s.Loading {
		return
	}
	if !a.skillsCapable() {
		s.Loaded = true
		s.Err = "当前运行时不支持技能管理（manageSkills）。"
		return
	}
	cwd := a.selectedWorkspaceCwd()
	if cwd == "" {
		s.Loaded = true
		s.Err = "无法解析当前工作区路径。"
		return
	}
	seq := atomic.AddInt64(&s.Seq, 1)
	s.Loading = true
	s.Err = ""
	s.Status = ""
	s.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadSkillsPanel(seq, startReloadSeq, cwd)
}

func (a *nativeApp) loadSkillsPanel(seq, startReloadSeq int64, cwd string) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.skillsPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.skills.Loading = false
			a.skills.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/skills/list", buildSkillsListRequest(cwd))
	a.dispatch(func() {
		if !a.skillsPanelCurrent(seq, startReloadSeq) {
			return
		}
		s := &a.skills
		s.Loading = false
		if err != nil {
			s.Err = "读取技能列表失败：" + err.Error()
			return
		}
		var view skillListView
		if err := json.Unmarshal(raw, &view); err != nil {
			s.Err = "技能列表响应无效：" + err.Error()
			return
		}
		a.applySkillsListView(&view)
	})
}

func (a *nativeApp) applySkillsListView(view *skillListView) {
	if view == nil {
		return
	}
	s := &a.skills
	skills := view.Skills
	if skills == nil {
		skills = []skillView{}
	}
	cwd := strings.TrimSpace(view.Cwd)
	if cwd == "" {
		cwd = a.selectedWorkspaceCwd()
	}
	s.Cwd = cwd
	s.Skills = skills
	s.Loaded = true
	s.Loading = false
	s.Err = ""
}

// toggleSkill flips one skill through mothx/manage/skills/set, then reloads
// the list so descriptions and source stay Runtime-authoritative. Only one
// toggle can be in flight at a time because the Runtime persists a single
// disabled-skill list.
func (a *nativeApp) toggleSkill(index int) {
	s := &a.skills
	if !a.skillsCapable() {
		s.Status = "当前运行时不支持技能管理（manageSkills）。"
		s.StatusIsError = true
		return
	}
	if s.Loading || s.Toggling != "" {
		return
	}
	if index < 0 || index >= len(s.Skills) {
		s.Status = "要切换的技能不存在。"
		s.StatusIsError = true
		return
	}
	skill := s.Skills[index]
	name := skillName(skill)
	if name == "" {
		s.Status = "要切换的技能名称不能为空。"
		s.StatusIsError = true
		return
	}
	cwd := a.selectedWorkspaceCwd()
	if cwd == "" {
		s.Status = "无法解析当前工作区路径。"
		s.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		s.Status = "切换技能失败：尚未连接到 MothX ACP。"
		s.StatusIsError = true
		return
	}

	target := !skillEnabled(skill)
	seq := atomic.AddInt64(&s.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	s.Toggling = name
	if target {
		s.Status = Tf("正在启用技能 %s…", name)
	} else {
		s.Status = Tf("正在停用技能 %s…", name)
	}
	s.StatusIsError = false
	go func() {
		_, setErr := client.call("mothx/manage/skills/set", buildSkillSetRequest(cwd, name, target))
		var listView *skillListView
		var listErr error
		if setErr == nil {
			raw, err := client.call("mothx/manage/skills/list", buildSkillsListRequest(cwd))
			if err != nil {
				listErr = err
			} else {
				var view skillListView
				if err := json.Unmarshal(raw, &view); err != nil {
					listErr = err
				} else {
					listView = &view
				}
			}
		}
		a.dispatch(func() {
			if !a.skillsPanelCurrent(seq, startReloadSeq) {
				return
			}
			s := &a.skills
			s.Toggling = ""
			if setErr != nil {
				s.Status = "切换技能失败：" + setErr.Error()
				s.StatusIsError = true
				return
			}
			if listView != nil {
				a.applySkillsListView(listView)
			} else {
				a.applySkillToggleLocally(name, target)
			}
			switch {
			case target:
				s.Status = Tf("技能 %s 已启用", name)
			default:
				s.Status = Tf("技能 %s 已停用", name)
			}
			s.StatusIsError = false
			if listErr != nil {
				s.Status += "；刷新技能列表失败：" + listErr.Error()
				s.StatusIsError = true
			}
		})
	}()
}

// applySkillToggleLocally keeps the visible row in sync when skills/set
// succeeded but the follow-up list refresh failed.
func (a *nativeApp) applySkillToggleLocally(name string, enabled bool) {
	s := &a.skills
	for index := range s.Skills {
		if skillName(s.Skills[index]) != name {
			continue
		}
		value := enabled
		s.Skills[index].Enabled = &value
		return
	}
}

// skillToggleStatus is the shared in-flight label for a skill toggle; it keeps
// the dynamic status translatable regardless of when the locale was set.
func skillToggleStatus(name string, target bool) string {
	if target {
		return Tf("正在启用技能 %s…", name)
	}
	return Tf("正在停用技能 %s…", name)
}
