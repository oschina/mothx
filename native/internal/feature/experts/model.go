// Package experts owns the Native projection of the ACP experts management
// contract (mothx/manage/experts/*).
package experts

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP experts management contract
// (mothx/manage/experts/list|get|create|update|delete). Expert bundles are
// Runtime-owned files under the global experts directory or the project's
// .mothx/experts directory; Native never reads or writes them directly.
// Builtin bundles are packaged with the binary and are always read-only.

const (
	ScopeGlobal  = "global"
	ScopeProject = "project"

	SourceBuiltin = "builtin"

	TypeAgent = "agent"
	TypeTeam  = "team"

	RoleLead   = "lead"
	RoleMember = "member"
)

var (
	Types = []string{TypeAgent, TypeTeam}
	Roles = []string{RoleLead, RoleMember}
)

// LocalizedText mirrors expert.LocalizedText.
type LocalizedText struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// MemberMeta mirrors expert.MemberMeta. Profession/Avatar are preserved
// across edits even though the first Native form exposes only identity,
// role, and display names, matching the Desktop editor.
type MemberMeta struct {
	ID         string        `json:"id"`
	Name       LocalizedText `json:"name"`
	Profession LocalizedText `json:"profession,omitempty"`
	Avatar     string        `json:"avatar,omitempty"`
	Role       string        `json:"role"`
}

// TeamInfo mirrors expert.TeamInfo.
type TeamInfo struct {
	LeadAgent    string   `json:"leadAgent"`
	MemberAgents []string `json:"memberAgents"`
}

// Manifest mirrors expert.Manifest.
type Manifest struct {
	SchemaVersion     int             `json:"schemaVersion"`
	Name              string          `json:"name"`
	ExpertType        string          `json:"expertType"`
	AgentName         string          `json:"agentName,omitempty"`
	DisplayName       LocalizedText   `json:"displayName"`
	CategoryID        string          `json:"categoryId,omitempty"`
	QuickPrompts      []LocalizedText `json:"quickPrompts,omitempty"`
	DefaultInitPrompt LocalizedText   `json:"defaultInitPrompt,omitempty"`
	TeamInfo          *TeamInfo       `json:"teamInfo,omitempty"`
	Members           []MemberMeta    `json:"members,omitempty"`
}

// Bundle mirrors expert.ManagedBundle. Agents values are complete
// agents/<id>.md source files, including frontmatter.
type Bundle struct {
	Scope    string            `json:"scope"`
	Manifest Manifest          `json:"manifest"`
	Agents   map[string]string `json:"agents"`
}

// Summary mirrors expert.Summary. Source is builtin/global/project;
// only global and project are writable.
type Summary struct {
	Name          string        `json:"name"`
	ExpertType    string        `json:"expertType"`
	DisplayName   LocalizedText `json:"displayName"`
	Source        string        `json:"source"`
	Invalid       bool          `json:"invalid"`
	InvalidReason string        `json:"invalidReason,omitempty"`
}

// ListView mirrors the list response envelope. EffectiveExperts is the
// shadow-resolved catalog a SessionRuntime can select; Experts is the
// scope-specific writable layer.
type ListView struct {
	Scope            string    `json:"scope"`
	Cwd              string    `json:"cwd,omitempty"`
	Experts          []Summary `json:"experts"`
	EffectiveExperts []Summary `json:"effectiveExperts"`
}

// MutationView is the create/update response envelope.
type MutationView struct {
	Scope  string  `json:"scope"`
	Cwd    string  `json:"cwd,omitempty"`
	Bundle *Bundle `json:"bundle"`
}

// MemberDraft is the editable projection of one manifest members[]
// entry. IDs are fixed after creation, matching the Desktop editor.
type MemberDraft struct {
	ID           string
	Role         string
	NameZh       string
	NameEn       string
	ProfessionZh string
	ProfessionEn string
	Avatar       string
}

// AgentDraft is one editable agents/<id>.md source. Slices keep the UI
// deterministic; the wire payload converts them back into a map.
type AgentDraft struct {
	ID     string
	Source string
}

// Draft is the editable, non-secret representation of one managed
// bundle. OriginalName is the loaded bundle name and stays empty for a new
// bundle; the manifest name itself is immutable after creation.
type Draft struct {
	Scope        string
	OriginalName string
	IsNew        bool

	Kind      string
	Name      string
	DisplayZh string
	DisplayEn string
	AgentName string
	Category  string

	Members []MemberDraft
	Agents  []AgentDraft

	// Preserved manifest fields that the first Native form does not edit.
	QuickPrompts      []LocalizedText
	DefaultInitPrompt LocalizedText

	// Transient add-row inputs.
	NewMemberID string
	NewAgentID  string
}

type State struct {
	Scope string
	List  *ListView
	Cwd   string

	Loaded  bool
	Loading bool
	Err     string

	Status        string
	StatusIsError bool

	Draft         *Draft
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

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{Scope: ScopeGlobal}}
}

// Host adds the selected project workspace to the shared base host surface.
type Host interface {
	host.Base
	ExpertsWorkspace() string
}

// WorkspaceCwd resolves the project workspace the same way Desktop
// does: the active/selected session cwd first, then the new-session default.
func WorkspaceCwd(workspace, draftCwd string) string {
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

func (p *Panel) Capable(h Host) bool {
	return h.FeatureEnabled("manageExperts")
}

// RequestCwd returns the cwd to include in project-scope requests,
// falling back to the selected workspace when a list response omitted it.
func (p *Panel) RequestCwd(h Host) string {
	cwd := strings.TrimSpace(p.Cwd)
	if cwd == "" && p.Scope == ScopeProject {
		return h.ExpertsWorkspace()
	}
	return cwd
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "experts" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// RequestScoped builds a params map for an experts request. cwd is
// included only for project scope; global bundles are resolved from the user
// config directory by the Runtime.
func RequestScoped(scope, cwd string, extra map[string]any) map[string]any {
	params := map[string]any{"scope": scope}
	for key, value := range extra {
		params[key] = value
	}
	if scope == ScopeProject && strings.TrimSpace(cwd) != "" {
		params["cwd"] = strings.TrimSpace(cwd)
	}
	return params
}

func BuildListRequest(scope, cwd string) map[string]any {
	return RequestScoped(scope, cwd, nil)
}

func BuildGetRequest(scope, cwd, name string) map[string]any {
	return RequestScoped(scope, cwd, map[string]any{"name": strings.TrimSpace(name)})
}

func BuildSaveRequest(scope, cwd string, bundle map[string]any) map[string]any {
	return RequestScoped(scope, cwd, map[string]any{"bundle": bundle})
}

func BuildDeleteRequest(scope, cwd, name string) map[string]any {
	return RequestScoped(scope, cwd, map[string]any{"name": strings.TrimSpace(name)})
}

// ListItems mirrors Desktop: the shadow-resolved effective catalog is
// shown, falling back to the scope-only list when the runtime omits it.
func ListItems(view *ListView) []Summary {
	if view == nil {
		return nil
	}
	if view.EffectiveExperts != nil {
		return view.EffectiveExperts
	}
	return view.Experts
}

func (p *Panel) SummaryByName(name string) *Summary {
	name = strings.TrimSpace(name)
	items := ListItems(p.List)
	for index := range items {
		if strings.TrimSpace(items[index].Name) == name {
			return &items[index]
		}
	}
	return nil
}

func DisplayName(item Summary) string {
	name := strings.TrimSpace(item.DisplayName.Zh)
	other := strings.TrimSpace(item.DisplayName.En)
	if i18n.CurrentLocale() == "en" {
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

func SourceLabel(source string) string {
	switch source {
	case SourceBuiltin:
		return "内置"
	case ScopeGlobal:
		return "全局"
	case ScopeProject:
		return "项目"
	default:
		return source
	}
}

func ScopeLabel(scope string) string {
	if scope == ScopeProject {
		return "项目"
	}
	return "全局"
}

func SummaryDescription(item Summary) string {
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

// CanEdit enforces the Desktop rule: only bundles physically present in
// the selected writable scope may be edited or deleted. Builtins and bundles
// from the other scope are read-only projections.
func CanEdit(item Summary, scope string) bool {
	source := strings.TrimSpace(item.Source)
	if source == SourceBuiltin || source == "" {
		return false
	}
	return source == scope
}

func TypeLabel(kind string) string {
	switch kind {
	case TypeTeam:
		return i18n.T("团队")
	case TypeAgent:
		return i18n.T("单 Agent")
	default:
		return kind
	}
}

func DraftFromBundle(bundle Bundle) *Draft {
	draft := &Draft{
		Scope:             strings.TrimSpace(bundle.Scope),
		OriginalName:      strings.TrimSpace(bundle.Manifest.Name),
		Kind:              strings.TrimSpace(bundle.Manifest.ExpertType),
		Name:              bundle.Manifest.Name,
		DisplayZh:         bundle.Manifest.DisplayName.Zh,
		DisplayEn:         bundle.Manifest.DisplayName.En,
		AgentName:         bundle.Manifest.AgentName,
		Category:          bundle.Manifest.CategoryID,
		QuickPrompts:      append([]LocalizedText(nil), bundle.Manifest.QuickPrompts...),
		DefaultInitPrompt: bundle.Manifest.DefaultInitPrompt,
	}
	if draft.Kind == "" {
		draft.Kind = TypeAgent
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
			role = RoleMember
		}
		draft.Members = append(draft.Members, MemberDraft{
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
		draft.Agents = append(draft.Agents, AgentDraft{ID: id, Source: bundle.Agents[id]})
	}
	return draft
}

func NewDraft(scope string) *Draft {
	if scope == "" {
		scope = ScopeGlobal
	}
	return &Draft{
		Scope:     scope,
		IsNew:     true,
		Kind:      TypeAgent,
		AgentName: "lead",
		Members: []MemberDraft{{
			ID:     "lead",
			Role:   RoleLead,
			NameZh: "主角",
			NameEn: "Lead",
		}},
		Agents: []AgentDraft{{
			ID:     "lead",
			Source: "---\nname: lead\n---\n",
		}},
	}
}

func (p *Panel) StartNew(h Host) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持团队管理（manageExperts）。"
		p.StatusIsError = true
		return
	}
	if p.Saving || p.LoadingBundle || p.Deleting {
		return
	}
	p.Draft = NewDraft(p.Scope)
	p.Status = "正在编辑新团队；保存后才会出现在目录中。"
	p.StatusIsError = false
}

func (p *Panel) CancelEditor() {
	p.Draft = nil
	p.LoadingBundle = false
	p.Status = ""
	p.StatusIsError = false
}

// Invalidate drops transient state and makes every in-flight
// experts response stale. Scope resets to the product default so re-entering
// the tab behaves like a fresh Desktop panel.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Scope = ScopeGlobal
	p.List = nil
	p.Cwd = ""
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	p.Draft = nil
	p.LoadingBundle = false
	p.Saving = false
	p.Deleting = false
	p.PendingDeleteOpen = false
	p.PendingDeleteName = ""
}

// StartLoad starts one capability-gated list for the selected
// scope. It is called from the UI thread on tab entry and scope changes.
func (p *Panel) StartLoad(h Host) {
	if !h.SettingsOpen() || h.SettingsTab() != "experts" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持团队管理（manageExperts）。"
		return
	}
	cwd := ""
	if p.Scope == ScopeProject {
		cwd = h.ExpertsWorkspace()
		if cwd == "" {
			p.Loaded = true
			p.Err = "项目范围需要已选择工作目录。"
			return
		}
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq, p.Scope, cwd)
}

func (p *Panel) load(h Host, seq, startReloadSeq int64, scope, cwd string) {
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
	raw, err := client.Call("mothx/manage/experts/list", BuildListRequest(scope, cwd))
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取团队目录失败：" + err.Error()
			return
		}
		var view ListView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "团队目录响应无效：" + err.Error()
			return
		}
		p.ApplyListView(h, &view)
	})
}

func (p *Panel) ApplyListView(h Host, view *ListView) {
	if view == nil {
		return
	}
	if view.Experts == nil {
		view.Experts = []Summary{}
	}
	if scope := strings.TrimSpace(view.Scope); scope == ScopeGlobal || scope == ScopeProject {
		p.Scope = scope
	}
	cwd := strings.TrimSpace(view.Cwd)
	if cwd == "" && p.Scope == ScopeProject {
		cwd = h.ExpertsWorkspace()
	}
	p.Cwd = cwd
	p.List = view
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// SetScope switches the writable layer. Project scope is rejected when
// no workspace is available, matching the Desktop surface.
func (p *Panel) SetScope(h Host, scope string) {
	if scope != ScopeGlobal && scope != ScopeProject {
		return
	}
	if p.Scope == scope {
		return
	}
	if p.Saving || p.LoadingBundle || p.Deleting {
		return
	}
	if scope == ScopeProject && h.ExpertsWorkspace() == "" {
		p.Status = "项目范围需要已选择工作目录。"
		p.StatusIsError = true
		return
	}
	atomic.AddInt64(&p.Seq, 1)
	p.Scope = scope
	p.List = nil
	p.Cwd = ""
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Draft = nil
	p.Status = ""
	p.StatusIsError = false
	p.PendingDeleteOpen = false
	p.PendingDeleteName = ""
	p.StartLoad(h)
}

// OpenEditor loads exactly one writable bundle through experts/get.
// Builtin and other-scope bundles are rejected before any call is made.
func (p *Panel) OpenEditor(h Host, name string) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持团队管理（manageExperts）。"
		p.StatusIsError = true
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if p.Saving || p.LoadingBundle || p.Deleting {
		return
	}
	item := p.SummaryByName(name)
	if item == nil {
		p.Status = "要编辑的团队不存在。"
		p.StatusIsError = true
		return
	}
	if !CanEdit(*item, p.Scope) {
		p.Status = "只能编辑当前作用范围内的团队；内置团队只读。"
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "读取团队失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	scope := p.Scope
	cwd := p.RequestCwd(h)
	params := BuildGetRequest(scope, cwd, name)
	p.LoadingBundle = true
	p.Status = i18n.Tf("正在读取团队 %s…", name)
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/experts/get", params)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.LoadingBundle = false
			if err != nil {
				p.Status = "读取团队失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view MutationView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "团队响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			if view.Bundle == nil {
				p.Status = "团队响应缺少 bundle。"
				p.StatusIsError = true
				return
			}
			draft := DraftFromBundle(*view.Bundle)
			if draft.Scope == "" {
				draft.Scope = p.Scope
			}
			p.Draft = draft
			p.Status = ""
			p.StatusIsError = false
		})
	}()
}

// DraftBundle validates and converts the editor draft into the exact
// ManagedBundle shape accepted by experts/create and experts/update.
func DraftBundle(draft *Draft) (map[string]any, error) {
	if draft == nil {
		return nil, i18n.LocalizedError("团队草稿不存在")
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return nil, i18n.LocalizedError("团队标识不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, i18n.LocalizedError("团队标识不能包含路径分隔符")
	}

	kind := strings.TrimSpace(draft.Kind)
	if kind == "" {
		kind = TypeAgent
	}
	if kind != TypeAgent && kind != TypeTeam {
		return nil, i18n.LocalizedError("团队类型必须是 agent 或 team")
	}

	members := make([]map[string]any, 0, len(draft.Members))
	seenMembers := make(map[string]bool)
	for _, member := range draft.Members {
		id := strings.TrimSpace(member.ID)
		if id == "" {
			continue
		}
		if seenMembers[id] {
			return nil, i18n.LocalizedErrorf("该成员 ID %q 已存在", id)
		}
		seenMembers[id] = true
		role := strings.TrimSpace(member.Role)
		if role == "" {
			role = RoleMember
		}
		if role != RoleLead && role != RoleMember {
			return nil, i18n.LocalizedErrorf("成员 %s 的角色无效", id)
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
		return nil, i18n.LocalizedError("至少需要一个成员")
	}

	agents := make(map[string]any, len(draft.Agents))
	seenAgents := make(map[string]bool)
	for _, agent := range draft.Agents {
		id := strings.TrimSpace(agent.ID)
		if id == "" {
			continue
		}
		if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
			return nil, i18n.LocalizedErrorf("Agent ID %q 无效", id)
		}
		if seenAgents[id] {
			return nil, i18n.LocalizedErrorf("该 Agent ID %q 已存在", id)
		}
		seenAgents[id] = true
		if strings.TrimSpace(agent.Source) == "" {
			return nil, i18n.LocalizedErrorf("Agent %s 的源文件不能为空", id)
		}
		agents[id] = agent.Source
	}
	if len(agents) == 0 {
		return nil, i18n.LocalizedError("至少需要一个 Agent 源文件")
	}

	lead := ""
	for _, member := range members {
		if member["role"] == RoleLead {
			lead, _ = member["id"].(string)
			break
		}
	}
	if lead == "" {
		lead, _ = members[0]["id"].(string)
	}
	if kind == TypeTeam {
		hasLead := false
		for _, member := range members {
			if member["role"] == RoleLead {
				hasLead = true
				break
			}
		}
		if !hasLead {
			return nil, i18n.LocalizedError("team 类型需要一个 role=lead 的成员")
		}
	}

	agentName := strings.TrimSpace(draft.AgentName)
	if agentName == "" {
		agentName = lead
	}
	if kind == TypeAgent {
		if agentName == "" {
			return nil, i18n.LocalizedError("主 Agent 标识不能为空")
		}
		if _, ok := agents[agentName]; !ok {
			return nil, i18n.LocalizedErrorf("主 Agent 标识 %q 缺少对应的 Agent 源文件", agentName)
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
	if kind == TypeTeam {
		memberAgents := make([]string, 0, len(members)-1)
		for _, member := range members {
			id, _ := member["id"].(string)
			if id == "" || id == lead {
				continue
			}
			memberAgents = append(memberAgents, id)
		}
		if _, ok := agents[lead]; !ok {
			return nil, i18n.LocalizedErrorf("lead 成员 %q 缺少对应的 Agent 源文件", lead)
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

func (p *Panel) Save(h Host) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持团队管理（manageExperts）。"
		p.StatusIsError = true
		return
	}
	if p.Draft == nil {
		return
	}
	if p.Saving || p.LoadingBundle || p.Deleting {
		return
	}
	bundle, err := DraftBundle(p.Draft)
	if err != nil {
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
	creating := p.Draft.IsNew
	method := "mothx/manage/experts/update"
	if creating {
		method = "mothx/manage/experts/create"
	}
	params := BuildSaveRequest(p.Scope, p.RequestCwd(h), bundle)
	listParams := BuildListRequest(p.Scope, p.RequestCwd(h))
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = true
	p.Status = "正在保存团队…"
	p.StatusIsError = false
	go func() {
		raw, callErr := client.Call(method, params)
		var saved *MutationView
		decodeErr := ""
		if callErr == nil {
			var view MutationView
			if err := json.Unmarshal(raw, &view); err != nil {
				decodeErr = "保存后团队响应无效：" + err.Error()
			} else {
				saved = &view
			}
		}
		var listView *ListView
		listErr := ""
		if callErr == nil && decodeErr == "" {
			listRaw, err := client.Call("mothx/manage/experts/list", listParams)
			if err != nil {
				listErr = "刷新团队目录失败：" + err.Error()
			} else {
				var view ListView
				if err := json.Unmarshal(listRaw, &view); err != nil {
					listErr = "刷新团队目录响应无效：" + err.Error()
				} else {
					listView = &view
				}
			}
		}
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if callErr != nil {
				p.Status = "保存团队失败：" + callErr.Error()
				p.StatusIsError = true
				return
			}
			if decodeErr != "" {
				p.Status = decodeErr
				p.StatusIsError = true
				return
			}
			if saved != nil && saved.Bundle == nil {
				p.Status = "保存后团队响应缺少 bundle。"
				p.StatusIsError = true
				return
			}
			// Desktop closes the editor after a successful save and reloads the
			// authoritative catalog.
			p.Draft = nil
			if listView != nil {
				p.ApplyListView(h, listView)
			}
			switch {
			case creating:
				p.Status = "团队已创建"
			default:
				p.Status = "团队已保存"
			}
			p.StatusIsError = false
			if listErr != "" {
				p.Status += "；" + listErr
				p.StatusIsError = true
			}
		})
	}()
}

func (p *Panel) RequestDelete(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if p.Saving || p.LoadingBundle || p.Deleting {
		return
	}
	item := p.SummaryByName(name)
	if item == nil {
		p.Status = "要删除的团队不存在。"
		p.StatusIsError = true
		return
	}
	if !CanEdit(*item, p.Scope) {
		p.Status = "只能删除当前作用范围内的团队；内置团队只读。"
		p.StatusIsError = true
		return
	}
	p.PendingDeleteOpen = true
	p.PendingDeleteName = name
}

func (p *Panel) CancelDelete() {
	p.PendingDeleteOpen = false
	p.PendingDeleteName = ""
}

func (p *Panel) ConfirmDelete(h Host) {
	name := strings.TrimSpace(p.PendingDeleteName)
	if name == "" {
		p.CancelDelete()
		return
	}
	if p.Saving || p.Loading || p.Deleting {
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "删除失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	p.CancelDelete()
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Deleting = true
	p.Status = i18n.Tf("正在删除团队 %s…", name)
	p.StatusIsError = false
	params := BuildDeleteRequest(p.Scope, p.RequestCwd(h), name)
	listParams := BuildListRequest(p.Scope, p.RequestCwd(h))
	go func() {
		_, callErr := client.Call("mothx/manage/experts/delete", params)
		var listView *ListView
		listErr := ""
		if callErr == nil {
			listRaw, err := client.Call("mothx/manage/experts/list", listParams)
			if err != nil {
				listErr = "刷新团队目录失败：" + err.Error()
			} else {
				var view ListView
				if err := json.Unmarshal(listRaw, &view); err != nil {
					listErr = "刷新团队目录响应无效：" + err.Error()
				} else {
					listView = &view
				}
			}
		}
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Deleting = false
			if callErr != nil {
				p.Status = "删除团队失败：" + callErr.Error()
				p.StatusIsError = true
				return
			}
			if p.Draft != nil && !p.Draft.IsNew && strings.TrimSpace(p.Draft.OriginalName) == name {
				p.Draft = nil
			}
			if listView != nil {
				p.ApplyListView(h, listView)
			}
			p.Status = i18n.Tf("团队 %s 已删除", name)
			p.StatusIsError = false
			if listErr != "" {
				p.Status += "；" + listErr
				p.StatusIsError = true
			}
		})
	}()
}

// AddMember appends the transient member-id input to the draft. IDs are
// immutable once added, matching the Desktop editor.
func (p *Panel) AddMember() {
	if p.Draft == nil {
		return
	}
	id := strings.TrimSpace(p.Draft.NewMemberID)
	if id == "" {
		p.Status = "成员 ID 不能为空。"
		p.StatusIsError = true
		return
	}
	for _, member := range p.Draft.Members {
		if strings.TrimSpace(member.ID) == id {
			p.Status = i18n.Tf("成员 ID %q 已存在。", id)
			p.StatusIsError = true
			return
		}
	}
	p.Draft.Members = append(p.Draft.Members, MemberDraft{ID: id, Role: RoleMember})
	p.Draft.NewMemberID = ""
	p.Status = "已添加成员 " + id
	p.StatusIsError = false
}

func (p *Panel) RemoveMember(index int) {
	if p.Draft == nil || index < 0 || index >= len(p.Draft.Members) {
		return
	}
	p.Draft.Members = append(p.Draft.Members[:index], p.Draft.Members[index+1:]...)
}

// AddAgent appends the transient agent-id input with a minimal valid
// frontmatter stub, mirroring the Desktop editor.
func (p *Panel) AddAgent() {
	if p.Draft == nil {
		return
	}
	id := strings.TrimSpace(p.Draft.NewAgentID)
	if id == "" {
		p.Status = "Agent ID 不能为空。"
		p.StatusIsError = true
		return
	}
	for _, agent := range p.Draft.Agents {
		if strings.TrimSpace(agent.ID) == id {
			p.Status = i18n.Tf("Agent ID %q 已存在。", id)
			p.StatusIsError = true
			return
		}
	}
	p.Draft.Agents = append(p.Draft.Agents, AgentDraft{
		ID:     id,
		Source: "---\nname: " + id + "\n---\n",
	})
	p.Draft.NewAgentID = ""
	p.Status = "已添加 Agent " + id
	p.StatusIsError = false
}

func (p *Panel) RemoveAgent(index int) {
	if p.Draft == nil || index < 0 || index >= len(p.Draft.Agents) {
		return
	}
	p.Draft.Agents = append(p.Draft.Agents[:index], p.Draft.Agents[index+1:]...)
}
