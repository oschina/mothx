package main

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP experts management contract
// (mothx/manage/experts/list|get|create|update|delete). Expert bundles are
// Runtime-owned files under the global experts directory or the project's
// .mothx/experts directory; Native never reads or writes them directly.
// Builtin bundles are packaged with the binary and are always read-only.

const (
	expertScopeGlobal  = "global"
	expertScopeProject = "project"

	expertSourceBuiltin = "builtin"

	expertTypeAgent = "agent"
	expertTypeTeam  = "team"

	expertRoleLead   = "lead"
	expertRoleMember = "member"
)

var (
	expertTypes = []string{expertTypeAgent, expertTypeTeam}
	expertRoles = []string{expertRoleLead, expertRoleMember}
)

// expertLocalizedText mirrors expert.LocalizedText.
type expertLocalizedText struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// expertMemberMeta mirrors expert.MemberMeta. Profession/Avatar are preserved
// across edits even though the first Native form exposes only identity,
// role, and display names, matching the Desktop editor.
type expertMemberMeta struct {
	ID         string              `json:"id"`
	Name       expertLocalizedText `json:"name"`
	Profession expertLocalizedText `json:"profession,omitempty"`
	Avatar     string              `json:"avatar,omitempty"`
	Role       string              `json:"role"`
}

// expertTeamInfo mirrors expert.TeamInfo.
type expertTeamInfo struct {
	LeadAgent    string   `json:"leadAgent"`
	MemberAgents []string `json:"memberAgents"`
}

// expertManifest mirrors expert.Manifest.
type expertManifest struct {
	SchemaVersion     int                   `json:"schemaVersion"`
	Name              string                `json:"name"`
	ExpertType        string                `json:"expertType"`
	AgentName         string                `json:"agentName,omitempty"`
	DisplayName       expertLocalizedText   `json:"displayName"`
	CategoryID        string                `json:"categoryId,omitempty"`
	QuickPrompts      []expertLocalizedText `json:"quickPrompts,omitempty"`
	DefaultInitPrompt expertLocalizedText   `json:"defaultInitPrompt,omitempty"`
	TeamInfo          *expertTeamInfo       `json:"teamInfo,omitempty"`
	Members           []expertMemberMeta    `json:"members,omitempty"`
}

// expertBundle mirrors expert.ManagedBundle. Agents values are complete
// agents/<id>.md source files, including frontmatter.
type expertBundle struct {
	Scope    string            `json:"scope"`
	Manifest expertManifest    `json:"manifest"`
	Agents   map[string]string `json:"agents"`
}

// expertSummary mirrors expert.Summary. Source is builtin/global/project;
// only global and project are writable.
type expertSummary struct {
	Name          string              `json:"name"`
	ExpertType    string              `json:"expertType"`
	DisplayName   expertLocalizedText `json:"displayName"`
	Source        string              `json:"source"`
	Invalid       bool                `json:"invalid"`
	InvalidReason string              `json:"invalidReason,omitempty"`
}

// expertListView mirrors the list response envelope. EffectiveExperts is the
// shadow-resolved catalog a SessionRuntime can select; Experts is the
// scope-specific writable layer.
type expertListView struct {
	Scope            string          `json:"scope"`
	Cwd              string          `json:"cwd,omitempty"`
	Experts          []expertSummary `json:"experts"`
	EffectiveExperts []expertSummary `json:"effectiveExperts"`
}

// expertMutationView is the create/update response envelope.
type expertMutationView struct {
	Scope  string        `json:"scope"`
	Cwd    string        `json:"cwd,omitempty"`
	Bundle *expertBundle `json:"bundle"`
}

// expertMemberDraft is the editable projection of one manifest members[]
// entry. IDs are fixed after creation, matching the Desktop editor.
type expertMemberDraft struct {
	ID           string
	Role         string
	NameZh       string
	NameEn       string
	ProfessionZh string
	ProfessionEn string
	Avatar       string
}

// expertAgentDraft is one editable agents/<id>.md source. Slices keep the UI
// deterministic; the wire payload converts them back into a map.
type expertAgentDraft struct {
	ID     string
	Source string
}

// expertDraft is the editable, non-secret representation of one managed
// bundle. OriginalName is the loaded bundle name and stays empty for a new
// bundle; the manifest name itself is immutable after creation.
type expertDraft struct {
	Scope        string
	OriginalName string
	IsNew        bool

	Kind      string
	Name      string
	DisplayZh string
	DisplayEn string
	AgentName string
	Category  string

	Members []expertMemberDraft
	Agents  []expertAgentDraft

	// Preserved manifest fields that the first Native form does not edit.
	QuickPrompts      []expertLocalizedText
	DefaultInitPrompt expertLocalizedText

	// Transient add-row inputs.
	NewMemberID string
	NewAgentID  string
}

type expertsPanelState struct {
	Scope string
	View  *expertListView
	Cwd   string

	Loaded  bool
	Loading bool
	Err     string

	Status        string
	StatusIsError bool

	Draft         *expertDraft
	LoadingBundle bool
	Saving        bool
	Deleting      bool

	PendingDeleteOpen bool
	PendingDeleteName string

	// Seq guards every asynchronous experts call. Bumping it invalidates any
	// in-flight list/get/mutation response, which is how scope changes, tab
	// exits, and reconnects prevent stale state from landing.
	Seq int64
}

func newExpertsPanelState() expertsPanelState {
	return expertsPanelState{Scope: expertScopeGlobal}
}

// expertsWorkspaceCwd resolves the project workspace the same way Desktop
// does: the active/selected session cwd first, then the new-session default.
func expertsWorkspaceCwd(workspace, draftCwd string) string {
	for _, candidate := range []string{workspace, draftCwd} {
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

func (a *nativeApp) expertsWorkspace() string {
	return expertsWorkspaceCwd(a.workspace, a.draftCwd)
}

func (a *nativeApp) expertsCapable() bool {
	return a.featureEnabled("manageExperts")
}

// expertsRequestCwd returns the cwd to include in project-scope requests,
// falling back to the selected workspace when a list response omitted it.
func (a *nativeApp) expertsRequestCwd() string {
	cwd := strings.TrimSpace(a.experts.Cwd)
	if cwd == "" && a.experts.Scope == expertScopeProject {
		return a.expertsWorkspace()
	}
	return cwd
}

func (a *nativeApp) expertsPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "experts" &&
		seq == atomic.LoadInt64(&a.experts.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// expertRequestScoped builds a params map for an experts request. cwd is
// included only for project scope; global bundles are resolved from the user
// config directory by the Runtime.
func expertRequestScoped(scope, cwd string, extra map[string]any) map[string]any {
	params := map[string]any{"scope": scope}
	for key, value := range extra {
		params[key] = value
	}
	if scope == expertScopeProject && strings.TrimSpace(cwd) != "" {
		params["cwd"] = strings.TrimSpace(cwd)
	}
	return params
}

func buildExpertsListRequest(scope, cwd string) map[string]any {
	return expertRequestScoped(scope, cwd, nil)
}

func buildExpertGetRequest(scope, cwd, name string) map[string]any {
	return expertRequestScoped(scope, cwd, map[string]any{"name": strings.TrimSpace(name)})
}

func buildExpertSaveRequest(scope, cwd string, bundle map[string]any) map[string]any {
	return expertRequestScoped(scope, cwd, map[string]any{"bundle": bundle})
}

func buildExpertDeleteRequest(scope, cwd, name string) map[string]any {
	return expertRequestScoped(scope, cwd, map[string]any{"name": strings.TrimSpace(name)})
}

// expertListItems mirrors Desktop: the shadow-resolved effective catalog is
// shown, falling back to the scope-only list when the runtime omits it.
func expertListItems(view *expertListView) []expertSummary {
	if view == nil {
		return nil
	}
	if view.EffectiveExperts != nil {
		return view.EffectiveExperts
	}
	return view.Experts
}

func (a *nativeApp) expertSummaryByName(name string) *expertSummary {
	name = strings.TrimSpace(name)
	items := expertListItems(a.experts.View)
	for index := range items {
		if strings.TrimSpace(items[index].Name) == name {
			return &items[index]
		}
	}
	return nil
}

func expertDisplayName(item expertSummary) string {
	name := strings.TrimSpace(item.DisplayName.Zh)
	other := strings.TrimSpace(item.DisplayName.En)
	if currentLocale() == "en" {
		name, other = other, name
	}
	if name == "" {
		name = other
	}
	if name == "" {
		name = strings.TrimSpace(item.Name)
	}
	return name
}

func expertSourceLabel(source string) string {
	switch source {
	case expertSourceBuiltin:
		return "内置"
	case expertScopeGlobal:
		return "全局"
	case expertScopeProject:
		return "项目"
	default:
		return source
	}
}

func expertScopeLabel(scope string) string {
	if scope == expertScopeProject {
		return "项目"
	}
	return "全局"
}

func expertSummaryDescription(item expertSummary) string {
	parts := make([]string, 0, 3)
	if name := strings.TrimSpace(item.Name); name != "" {
		parts = append(parts, name)
	}
	if kind := strings.TrimSpace(item.ExpertType); kind != "" {
		parts = append(parts, kind)
	}
	if item.Invalid && strings.TrimSpace(item.InvalidReason) != "" {
		parts = append(parts, item.InvalidReason)
	}
	return strings.Join(parts, " · ")
}

// expertCanEdit enforces the Desktop rule: only bundles physically present in
// the selected writable scope may be edited or deleted. Builtins and bundles
// from the other scope are read-only projections.
func expertCanEdit(item expertSummary, scope string) bool {
	source := strings.TrimSpace(item.Source)
	if source == expertSourceBuiltin || source == "" {
		return false
	}
	return source == scope
}

func expertTypeLabel(kind string) string {
	switch kind {
	case expertTypeTeam:
		return T("团队")
	case expertTypeAgent:
		return T("单 Agent")
	default:
		return kind
	}
}

func expertDraftFromBundle(bundle expertBundle) *expertDraft {
	draft := &expertDraft{
		Scope:             strings.TrimSpace(bundle.Scope),
		OriginalName:      strings.TrimSpace(bundle.Manifest.Name),
		Kind:              strings.TrimSpace(bundle.Manifest.ExpertType),
		Name:              bundle.Manifest.Name,
		DisplayZh:         bundle.Manifest.DisplayName.Zh,
		DisplayEn:         bundle.Manifest.DisplayName.En,
		AgentName:         bundle.Manifest.AgentName,
		Category:          bundle.Manifest.CategoryID,
		QuickPrompts:      append([]expertLocalizedText(nil), bundle.Manifest.QuickPrompts...),
		DefaultInitPrompt: bundle.Manifest.DefaultInitPrompt,
	}
	if draft.Kind == "" {
		draft.Kind = expertTypeAgent
	}
	seenMembers := make(map[string]bool)
	for _, member := range bundle.Manifest.Members {
		id := strings.TrimSpace(member.ID)
		if id == "" || seenMembers[id] {
			continue
		}
		seenMembers[id] = true
		role := strings.TrimSpace(member.Role)
		if role == "" {
			role = expertRoleMember
		}
		draft.Members = append(draft.Members, expertMemberDraft{
			ID:           id,
			Role:         role,
			NameZh:       member.Name.Zh,
			NameEn:       member.Name.En,
			ProfessionZh: member.Profession.Zh,
			ProfessionEn: member.Profession.En,
			Avatar:       member.Avatar,
		})
	}
	ids := make([]string, 0, len(bundle.Agents))
	for id := range bundle.Agents {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		draft.Agents = append(draft.Agents, expertAgentDraft{ID: id, Source: bundle.Agents[id]})
	}
	return draft
}

func newExpertDraft(scope string) *expertDraft {
	if scope == "" {
		scope = expertScopeGlobal
	}
	return &expertDraft{
		Scope:     scope,
		IsNew:     true,
		Kind:      expertTypeAgent,
		AgentName: "lead",
		Members: []expertMemberDraft{{
			ID:     "lead",
			Role:   expertRoleLead,
			NameZh: "主角",
			NameEn: "Lead",
		}},
		Agents: []expertAgentDraft{{
			ID:     "lead",
			Source: "---\nname: lead\n---\n",
		}},
	}
}

func (a *nativeApp) startNewExpert() {
	e := &a.experts
	if !a.expertsCapable() {
		e.Status = "当前运行时不支持团队管理（manageExperts）。"
		e.StatusIsError = true
		return
	}
	if e.Saving || e.LoadingBundle || e.Deleting {
		return
	}
	e.Draft = newExpertDraft(e.Scope)
	e.Status = "正在编辑新团队；保存后才会出现在目录中。"
	e.StatusIsError = false
}

func (a *nativeApp) cancelExpertEditor() {
	e := &a.experts
	e.Draft = nil
	e.LoadingBundle = false
	e.Status = ""
	e.StatusIsError = false
}

// invalidateExpertsPanel drops transient state and makes every in-flight
// experts response stale. Scope resets to the product default so re-entering
// the tab behaves like a fresh Desktop panel.
func (a *nativeApp) invalidateExpertsPanel() {
	e := &a.experts
	atomic.AddInt64(&e.Seq, 1)
	e.Scope = expertScopeGlobal
	e.View = nil
	e.Cwd = ""
	e.Loaded = false
	e.Loading = false
	e.Err = ""
	e.Status = ""
	e.StatusIsError = false
	e.Draft = nil
	e.LoadingBundle = false
	e.Saving = false
	e.Deleting = false
	e.PendingDeleteOpen = false
	e.PendingDeleteName = ""
}

// startExpertsPanelLoad starts one capability-gated list for the selected
// scope. It is called from the UI thread on tab entry and scope changes.
func (a *nativeApp) startExpertsPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "experts" {
		return
	}
	e := &a.experts
	if e.Loading {
		return
	}
	if !a.expertsCapable() {
		e.Loaded = true
		e.Err = "当前运行时不支持团队管理（manageExperts）。"
		return
	}
	cwd := ""
	if e.Scope == expertScopeProject {
		cwd = a.expertsWorkspace()
		if cwd == "" {
			e.Loaded = true
			e.Err = "项目范围需要已选择工作目录。"
			return
		}
	}
	seq := atomic.AddInt64(&e.Seq, 1)
	e.Loading = true
	e.Err = ""
	e.Status = ""
	e.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadExpertsPanel(seq, startReloadSeq, e.Scope, cwd)
}

func (a *nativeApp) loadExpertsPanel(seq, startReloadSeq int64, scope, cwd string) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.expertsPanelCurrent(seq, startReloadSeq) {
				return
			}
			e := &a.experts
			e.Loading = false
			e.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/experts/list", buildExpertsListRequest(scope, cwd))
	a.dispatch(func() {
		if !a.expertsPanelCurrent(seq, startReloadSeq) {
			return
		}
		e := &a.experts
		e.Loading = false
		if err != nil {
			e.Err = "读取团队目录失败：" + err.Error()
			return
		}
		var view expertListView
		if err := json.Unmarshal(raw, &view); err != nil {
			e.Err = "团队目录响应无效：" + err.Error()
			return
		}
		a.applyExpertsListView(&view)
	})
}

func (a *nativeApp) applyExpertsListView(view *expertListView) {
	if view == nil {
		return
	}
	e := &a.experts
	if view.Experts == nil {
		view.Experts = []expertSummary{}
	}
	if scope := strings.TrimSpace(view.Scope); scope == expertScopeGlobal || scope == expertScopeProject {
		e.Scope = scope
	}
	cwd := strings.TrimSpace(view.Cwd)
	if cwd == "" && e.Scope == expertScopeProject {
		cwd = a.expertsWorkspace()
	}
	e.Cwd = cwd
	e.View = view
	e.Loaded = true
	e.Loading = false
	e.Err = ""
}

// setExpertScope switches the writable layer. Project scope is rejected when
// no workspace is available, matching the Desktop surface.
func (a *nativeApp) setExpertScope(scope string) {
	e := &a.experts
	if scope != expertScopeGlobal && scope != expertScopeProject {
		return
	}
	if e.Scope == scope {
		return
	}
	if e.Saving || e.LoadingBundle || e.Deleting {
		return
	}
	if scope == expertScopeProject && a.expertsWorkspace() == "" {
		e.Status = "项目范围需要已选择工作目录。"
		e.StatusIsError = true
		return
	}
	atomic.AddInt64(&e.Seq, 1)
	e.Scope = scope
	e.View = nil
	e.Cwd = ""
	e.Loaded = false
	e.Loading = false
	e.Err = ""
	e.Draft = nil
	e.Status = ""
	e.StatusIsError = false
	e.PendingDeleteOpen = false
	e.PendingDeleteName = ""
	a.startExpertsPanelLoad()
}

// openExpertEditor loads exactly one writable bundle through experts/get.
// Builtin and other-scope bundles are rejected before any call is made.
func (a *nativeApp) openExpertEditor(name string) {
	e := &a.experts
	if !a.expertsCapable() {
		e.Status = "当前运行时不支持团队管理（manageExperts）。"
		e.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if e.Saving || e.LoadingBundle || e.Deleting {
		return
	}
	item := a.expertSummaryByName(name)
	if item == nil {
		e.Status = "要编辑的团队不存在。"
		e.StatusIsError = true
		return
	}
	if !expertCanEdit(*item, e.Scope) {
		e.Status = "只能编辑当前作用范围内的团队；内置团队只读。"
		e.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		e.Status = "读取团队失败：尚未连接到 MothX ACP。"
		e.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&e.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	scope := e.Scope
	cwd := a.expertsRequestCwd()
	params := buildExpertGetRequest(scope, cwd, name)
	e.LoadingBundle = true
	e.Status = Tf("正在读取团队 %s…", name)
	e.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/experts/get", params)
		a.dispatch(func() {
			if !a.expertsPanelCurrent(seq, startReloadSeq) {
				return
			}
			e := &a.experts
			e.LoadingBundle = false
			if err != nil {
				e.Status = "读取团队失败：" + err.Error()
				e.StatusIsError = true
				return
			}
			var view expertMutationView
			if err := json.Unmarshal(raw, &view); err != nil {
				e.Status = "团队响应无效：" + err.Error()
				e.StatusIsError = true
				return
			}
			if view.Bundle == nil {
				e.Status = "团队响应缺少 bundle。"
				e.StatusIsError = true
				return
			}
			draft := expertDraftFromBundle(*view.Bundle)
			if draft.Scope == "" {
				draft.Scope = e.Scope
			}
			e.Draft = draft
			e.Status = ""
			e.StatusIsError = false
		})
	}()
}

// expertDraftBundle validates and converts the editor draft into the exact
// ManagedBundle shape accepted by experts/create and experts/update.
func expertDraftBundle(draft *expertDraft) (map[string]any, error) {
	if draft == nil {
		return nil, localizedError("团队草稿不存在")
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return nil, localizedError("团队标识不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, localizedError("团队标识不能包含路径分隔符")
	}

	kind := strings.TrimSpace(draft.Kind)
	if kind == "" {
		kind = expertTypeAgent
	}
	if kind != expertTypeAgent && kind != expertTypeTeam {
		return nil, localizedError("团队类型必须是 agent 或 team")
	}

	members := make([]map[string]any, 0, len(draft.Members))
	seenMembers := make(map[string]bool)
	for _, member := range draft.Members {
		id := strings.TrimSpace(member.ID)
		if id == "" {
			continue
		}
		if seenMembers[id] {
			return nil, localizedErrorf("该成员 ID %q 已存在", id)
		}
		seenMembers[id] = true
		role := strings.TrimSpace(member.Role)
		if role == "" {
			role = expertRoleMember
		}
		if role != expertRoleLead && role != expertRoleMember {
			return nil, localizedErrorf("成员 %s 的角色无效", id)
		}
		entry := map[string]any{
			"id":   id,
			"role": role,
			"name": map[string]any{
				"zh": strings.TrimSpace(member.NameZh),
				"en": strings.TrimSpace(member.NameEn),
			},
		}
		profession := map[string]any{
			"zh": strings.TrimSpace(member.ProfessionZh),
			"en": strings.TrimSpace(member.ProfessionEn),
		}
		if profession["zh"] != "" || profession["en"] != "" {
			entry["profession"] = profession
		}
		if avatar := strings.TrimSpace(member.Avatar); avatar != "" {
			entry["avatar"] = avatar
		}
		members = append(members, entry)
	}
	if len(members) == 0 {
		return nil, localizedError("至少需要一个成员")
	}

	agents := make(map[string]any, len(draft.Agents))
	seenAgents := make(map[string]bool)
	for _, agent := range draft.Agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			continue
		}
		if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
			return nil, localizedErrorf("Agent ID %q 无效", id)
		}
		if seenAgents[id] {
			return nil, localizedErrorf("该 Agent ID %q 已存在", id)
		}
		seenAgents[id] = true
		if strings.TrimSpace(agent.Source) == "" {
			return nil, localizedErrorf("Agent %s 的源文件不能为空", id)
		}
		agents[id] = agent.Source
	}
	if len(agents) == 0 {
		return nil, localizedError("至少需要一个 Agent 源文件")
	}

	lead := ""
	for _, member := range members {
		if member["role"] == expertRoleLead {
			lead, _ = member["id"].(string)
			break
		}
	}
	if lead == "" {
		lead, _ = members[0]["id"].(string)
	}
	if kind == expertTypeTeam {
		hasLead := false
		for _, member := range members {
			if member["role"] == expertRoleLead {
				hasLead = true
				break
			}
		}
		if !hasLead {
			return nil, localizedError("team 类型需要一个 role=lead 的成员")
		}
	}

	agentName := strings.TrimSpace(draft.AgentName)
	if agentName == "" {
		agentName = lead
	}
	if kind == expertTypeAgent {
		if agentName == "" {
			return nil, localizedError("主 Agent 标识不能为空")
		}
		if _, ok := agents[agentName]; !ok {
			return nil, localizedErrorf("主 Agent 标识 %q 缺少对应的 Agent 源文件", agentName)
		}
	}

	manifest := map[string]any{
		"schemaVersion": 1,
		"name":          name,
		"expertType":    kind,
		"agentName":     agentName,
		"displayName": map[string]any{
			"zh": strings.TrimSpace(draft.DisplayZh),
			"en": strings.TrimSpace(draft.DisplayEn),
		},
		"members": members,
	}
	if category := strings.TrimSpace(draft.Category); category != "" {
		manifest["categoryId"] = category
	}
	if len(draft.QuickPrompts) > 0 {
		quickPrompts := make([]map[string]any, 0, len(draft.QuickPrompts))
		for _, prompt := range draft.QuickPrompts {
			quickPrompts = append(quickPrompts, map[string]any{
				"zh": prompt.Zh,
				"en": prompt.En,
			})
		}
		manifest["quickPrompts"] = quickPrompts
	}
	if strings.TrimSpace(draft.DefaultInitPrompt.Zh) != "" || strings.TrimSpace(draft.DefaultInitPrompt.En) != "" {
		manifest["defaultInitPrompt"] = map[string]any{
			"zh": draft.DefaultInitPrompt.Zh,
			"en": draft.DefaultInitPrompt.En,
		}
	}
	if kind == expertTypeTeam {
		memberAgents := make([]string, 0, len(members)-1)
		for _, member := range members {
			id, _ := member["id"].(string)
			if id == "" || id == lead {
				continue
			}
			memberAgents = append(memberAgents, id)
		}
		if _, ok := agents[lead]; !ok {
			return nil, localizedErrorf("lead 成员 %q 缺少对应的 Agent 源文件", lead)
		}
		manifest["teamInfo"] = map[string]any{
			"leadAgent":    lead,
			"memberAgents": memberAgents,
		}
	}

	return map[string]any{
		"scope":    draft.Scope,
		"manifest": manifest,
		"agents":   agents,
	}, nil
}

func (a *nativeApp) saveExpertDraft() {
	e := &a.experts
	if !a.expertsCapable() {
		e.Status = "当前运行时不支持团队管理（manageExperts）。"
		e.StatusIsError = true
		return
	}
	if e.Draft == nil {
		return
	}
	if e.Saving || e.LoadingBundle || e.Deleting {
		return
	}
	bundle, err := expertDraftBundle(e.Draft)
	if err != nil {
		e.Status = err.Error()
		e.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		e.Status = "保存失败：尚未连接到 MothX ACP。"
		e.StatusIsError = true
		return
	}
	creating := e.Draft.IsNew
	method := "mothx/manage/experts/update"
	if creating {
		method = "mothx/manage/experts/create"
	}
	params := buildExpertSaveRequest(e.Scope, a.expertsRequestCwd(), bundle)
	listParams := buildExpertsListRequest(e.Scope, a.expertsRequestCwd())
	seq := atomic.AddInt64(&e.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	e.Saving = true
	e.Status = "正在保存团队…"
	e.StatusIsError = false
	go func() {
		raw, callErr := client.call(method, params)
		var saved *expertMutationView
		decodeErr := ""
		if callErr == nil {
			var view expertMutationView
			if err := json.Unmarshal(raw, &view); err != nil {
				decodeErr = "保存后团队响应无效：" + err.Error()
			} else {
				saved = &view
			}
		}
		var listView *expertListView
		listErr := ""
		if callErr == nil && decodeErr == "" {
			listRaw, err := client.call("mothx/manage/experts/list", listParams)
			if err != nil {
				listErr = "刷新团队目录失败：" + err.Error()
			} else {
				var view expertListView
				if err := json.Unmarshal(listRaw, &view); err != nil {
					listErr = "刷新团队目录响应无效：" + err.Error()
				} else {
					listView = &view
				}
			}
		}
		a.dispatch(func() {
			if !a.expertsPanelCurrent(seq, startReloadSeq) {
				return
			}
			e := &a.experts
			e.Saving = false
			if callErr != nil {
				e.Status = "保存团队失败：" + callErr.Error()
				e.StatusIsError = true
				return
			}
			if decodeErr != "" {
				e.Status = decodeErr
				e.StatusIsError = true
				return
			}
			if saved != nil && saved.Bundle == nil {
				e.Status = "保存后团队响应缺少 bundle。"
				e.StatusIsError = true
				return
			}
			// Desktop closes the editor after a successful save and reloads the
			// authoritative catalog.
			e.Draft = nil
			if listView != nil {
				a.applyExpertsListView(listView)
			}
			switch {
			case creating:
				e.Status = "团队已创建"
			default:
				e.Status = "团队已保存"
			}
			e.StatusIsError = false
			if listErr != "" {
				e.Status += "；" + listErr
				e.StatusIsError = true
			}
		})
	}()
}

func (a *nativeApp) requestDeleteExpert(name string) {
	e := &a.experts
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if e.Saving || e.LoadingBundle || e.Deleting {
		return
	}
	item := a.expertSummaryByName(name)
	if item == nil {
		e.Status = "要删除的团队不存在。"
		e.StatusIsError = true
		return
	}
	if !expertCanEdit(*item, e.Scope) {
		e.Status = "只能删除当前作用范围内的团队；内置团队只读。"
		e.StatusIsError = true
		return
	}
	e.PendingDeleteOpen = true
	e.PendingDeleteName = name
}

func (a *nativeApp) cancelDeleteExpert() {
	e := &a.experts
	e.PendingDeleteOpen = false
	e.PendingDeleteName = ""
}

func (a *nativeApp) confirmDeleteExpert() {
	e := &a.experts
	name := strings.TrimSpace(e.PendingDeleteName)
	if name == "" {
		a.cancelDeleteExpert()
		return
	}
	if e.Saving || e.Loading || e.Deleting {
		return
	}
	client := a.acp
	if client == nil {
		e.Status = "删除失败：尚未连接到 MothX ACP。"
		e.StatusIsError = true
		return
	}
	a.cancelDeleteExpert()
	seq := atomic.AddInt64(&e.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	e.Deleting = true
	e.Status = Tf("正在删除团队 %s…", name)
	e.StatusIsError = false
	params := buildExpertDeleteRequest(e.Scope, a.expertsRequestCwd(), name)
	listParams := buildExpertsListRequest(e.Scope, a.expertsRequestCwd())
	go func() {
		_, callErr := client.call("mothx/manage/experts/delete", params)
		var listView *expertListView
		listErr := ""
		if callErr == nil {
			listRaw, err := client.call("mothx/manage/experts/list", listParams)
			if err != nil {
				listErr = "刷新团队目录失败：" + err.Error()
			} else {
				var view expertListView
				if err := json.Unmarshal(listRaw, &view); err != nil {
					listErr = "刷新团队目录响应无效：" + err.Error()
				} else {
					listView = &view
				}
			}
		}
		a.dispatch(func() {
			if !a.expertsPanelCurrent(seq, startReloadSeq) {
				return
			}
			e := &a.experts
			e.Deleting = false
			if callErr != nil {
				e.Status = "删除团队失败：" + callErr.Error()
				e.StatusIsError = true
				return
			}
			if e.Draft != nil && !e.Draft.IsNew && strings.TrimSpace(e.Draft.OriginalName) == name {
				e.Draft = nil
			}
			if listView != nil {
				a.applyExpertsListView(listView)
			}
			e.Status = Tf("团队 %s 已删除", name)
			e.StatusIsError = false
			if listErr != "" {
				e.Status += "；" + listErr
				e.StatusIsError = true
			}
		})
	}()
}

// addExpertMember appends the transient member-id input to the draft. IDs are
// immutable once added, matching the Desktop editor.
func (a *nativeApp) addExpertMember() {
	e := &a.experts
	if e.Draft == nil {
		return
	}
	id := strings.TrimSpace(e.Draft.NewMemberID)
	if id == "" {
		e.Status = "成员 ID 不能为空。"
		e.StatusIsError = true
		return
	}
	for _, member := range e.Draft.Members {
		if strings.TrimSpace(member.ID) == id {
			e.Status = Tf("成员 ID %q 已存在。", id)
			e.StatusIsError = true
			return
		}
	}
	e.Draft.Members = append(e.Draft.Members, expertMemberDraft{ID: id, Role: expertRoleMember})
	e.Draft.NewMemberID = ""
	e.Status = "已添加成员 " + id
	e.StatusIsError = false
}

func (a *nativeApp) removeExpertMember(index int) {
	e := &a.experts
	if e.Draft == nil || index < 0 || index >= len(e.Draft.Members) {
		return
	}
	e.Draft.Members = append(e.Draft.Members[:index], e.Draft.Members[index+1:]...)
}

// addExpertAgent appends the transient agent-id input with a minimal valid
// frontmatter stub, mirroring the Desktop editor.
func (a *nativeApp) addExpertAgent() {
	e := &a.experts
	if e.Draft == nil {
		return
	}
	id := strings.TrimSpace(e.Draft.NewAgentID)
	if id == "" {
		e.Status = "Agent ID 不能为空。"
		e.StatusIsError = true
		return
	}
	for _, agent := range e.Draft.Agents {
		if strings.TrimSpace(agent.ID) == id {
			e.Status = Tf("Agent ID %q 已存在。", id)
			e.StatusIsError = true
			return
		}
	}
	e.Draft.Agents = append(e.Draft.Agents, expertAgentDraft{
		ID:     id,
		Source: "---\nname: " + id + "\n---\n",
	})
	e.Draft.NewAgentID = ""
	e.Status = "已添加 Agent " + id
	e.StatusIsError = false
}

func (a *nativeApp) removeExpertAgent(index int) {
	e := &a.experts
	if e.Draft == nil || index < 0 || index >= len(e.Draft.Agents) {
		return
	}
	e.Draft.Agents = append(e.Draft.Agents[:index], e.Draft.Agents[index+1:]...)
}
