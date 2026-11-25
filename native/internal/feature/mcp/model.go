// Package mcp owns the Native projection of the ACP MCP server management
// contract (mothx/manage/mcp/list and mothx/manage/mcp/set).
package mcp

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// This file owns the Native projection of the ACP MCP management contract
// (mothx/manage/mcp/list and mothx/manage/mcp/set). Wire types mirror the
// complete local mcp.json schema in internal/acp/manage.go: environment and
// header values are first-class editable fields, and mcp/set is a full
// replacement of the global server list. Transcript state is Runtime-owned;
// this panel only projects and writes the configuration through ACP.

const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
	NewServerIdx = -1
)

var mcpServerTypes = []string{"stdio", "http", "sse"}

// Pair is one environment variable or request header entry.
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ServerView mirrors manageMCPViews. Enabled is a pointer so an omitted
// field keeps the legacy mcp.json meaning of "enabled".
type ServerView struct {
	Name       string   `json:"name"`
	Type       string   `json:"type,omitempty"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	URL        string   `json:"url,omitempty"`
	MessageURL string   `json:"messageUrl,omitempty"`
	Enabled    *bool    `json:"enabled,omitempty"`
	Env        []Pair   `json:"env,omitempty"`
	Headers    []Pair   `json:"headers,omitempty"`
}

// ListView mirrors the mcp/list and mcp/set response envelope.
type ListView struct {
	Scope            string       `json:"scope"`
	SessionID        string       `json:"sessionId,omitempty"`
	Path             string       `json:"path"`
	Servers          []ServerView `json:"servers"`
	ReloadedSessions []string     `json:"reloadedSessions,omitempty"`
}

type PairDraft struct {
	Name  string
	Value string
}

// ServerDraft is the editable state of the add/edit dialog.
type ServerDraft struct {
	Name    string
	Type    string
	Command string
	// ArgsInput is the single-line form of the arguments. Fill it with JoinArgs
	// and read it back with SplitArgs: joining with plain spaces and splitting
	// on whitespace used to break any argument that contains a space, such as
	// a path under "Program Files".
	ArgsInput  string
	URL        string
	MessageURL string
	Enabled    bool
	Env        []PairDraft
	Headers    []PairDraft
}

type State struct {
	Scope   string
	Path    string
	Servers []ServerView
	Loaded  bool
	Loading bool
	Err     string

	Saving        bool
	Deleting      bool
	Status        string
	StatusIsError bool

	EditorOpen   bool
	EditingIndex int
	Draft        ServerDraft
	DialogErr    string

	PendingDelete      bool
	PendingDeleteIndex int

	// Seq guards every asynchronous MCP call. Bumping it invalidates any
	// in-flight list/save/delete response, which is how tab exits, reconnects,
	// and editor changes prevent stale state from landing.
	Seq int64
}

type Host interface {
	host.Base
	CurrentSession() string
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: NewPanelState()}
}

func NewPanelState() State {
	return State{
		Scope:        ScopeGlobal,
		EditingIndex: NewServerIdx,
		Draft:        EmptyDraft(),
	}
}

func EmptyDraft() ServerDraft {
	return ServerDraft{Type: "stdio", Enabled: true, Env: []PairDraft{}, Headers: []PairDraft{}}
}

func ServerType(view ServerView) string {
	value := strings.TrimSpace(view.Type)
	if value == "" {
		return "stdio"
	}
	return value
}

func ServerEnabled(view ServerView) bool {
	return view.Enabled == nil || *view.Enabled
}

func DraftFromServer(view ServerView) ServerDraft {
	draft := ServerDraft{
		Name:       view.Name,
		Type:       ServerType(view),
		Command:    view.Command,
		ArgsInput:  JoinArgs(view.Args),
		URL:        view.URL,
		MessageURL: view.MessageURL,
		Enabled:    ServerEnabled(view),
		Env:        make([]PairDraft, 0, len(view.Env)),
		Headers:    make([]PairDraft, 0, len(view.Headers)),
	}
	for _, entry := range view.Env {
		draft.Env = append(draft.Env, PairDraft{Name: entry.Name, Value: entry.Value})
	}
	for _, entry := range view.Headers {
		draft.Headers = append(draft.Headers, PairDraft{Name: entry.Name, Value: entry.Value})
	}
	return draft
}

// SplitArgs parses the single-line arguments field: unquoted whitespace
// separates arguments, while single quotes, double quotes, and a backslash
// before a quote keep whitespace and quotes inside one argument. A backslash
// on its own is literal, so a Windows path such as C:\tools\server.exe reads
// as written. It is the inverse of JoinArgs.
func SplitArgs(input string) []string {
	var args []string
	var current strings.Builder
	started := false
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	var quote byte
	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch quote {
		case '\'':
			if ch == '\'' {
				quote = 0
				continue
			}
			current.WriteByte(ch)
			continue
		case '"':
			if ch == '"' {
				quote = 0
				continue
			}
			if ch == '\\' && i+1 < len(input) && input[i+1] == '"' {
				i++
				current.WriteByte('"')
				continue
			}
			current.WriteByte(ch)
			continue
		}
		switch ch {
		case ' ', '\t', '\r', '\n':
			flush()
		case '\'', '"':
			quote = ch
			started = true
		case '\\':
			started = true
			if i+1 < len(input) && (input[i+1] == '\'' || input[i+1] == '"') {
				i++
				current.WriteByte(input[i])
				continue
			}
			current.WriteByte(ch)
		default:
			started = true
			current.WriteByte(ch)
		}
	}
	flush()
	return args
}

// JoinArgs renders arguments back into the field so SplitArgs(JoinArgs(args))
// is the identity. An argument that needs no quoting is written as is, so an
// existing server reads naturally; any other argument is single-quoted, where
// every character is literal.
func JoinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "" && !strings.ContainsAny(arg, " \t\r\n'\"") {
			parts = append(parts, arg)
			continue
		}
		parts = append(parts, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(parts, " ")
}

func PairNameExists(pairs []PairDraft, name string, skip int) bool {
	for index, entry := range pairs {
		if index == skip {
			continue
		}
		if strings.TrimSpace(entry.Name) == name {
			return true
		}
	}
	return false
}

// ValidateServerDraft enforces the mcp/set contract client-side so an
// invalid editor never reaches ACP. editingIndex is the row being replaced
// (NewServerIdx for a new entry).
func ValidateServerDraft(draft *ServerDraft, servers []ServerView, editingIndex int) error {
	if draft == nil {
		return i18n.LocalizedError("MCP 服务器草稿不存在")
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return i18n.LocalizedError("MCP 服务器名称不能为空")
	}
	for index, server := range servers {
		if index == editingIndex {
			continue
		}
		if strings.TrimSpace(server.Name) == name {
			return i18n.LocalizedErrorf("MCP 服务器名称 %q 已存在", name)
		}
	}
	serverType := strings.TrimSpace(draft.Type)
	if serverType == "" {
		serverType = "stdio"
	}
	allowed := false
	for _, option := range mcpServerTypes {
		if option == serverType {
			allowed = true
			break
		}
	}
	if !allowed {
		return i18n.LocalizedError("MCP 传输类型必须是 stdio、http 或 sse")
	}
	switch serverType {
	case "stdio":
		if strings.TrimSpace(draft.Command) == "" {
			return i18n.LocalizedError("stdio 服务器必须填写命令")
		}
	case "http", "sse":
		if strings.TrimSpace(draft.URL) == "" {
			return i18n.LocalizedErrorf("%s 服务器必须填写 URL", serverType)
		}
	}
	for _, field := range []struct {
		label string
		pairs []PairDraft
	}{
		{"环境变量", draft.Env},
		{"请求头", draft.Headers},
	} {
		seen := make(map[string]bool, len(field.pairs))
		for _, entry := range field.pairs {
			pairName := strings.TrimSpace(entry.Name)
			if pairName == "" {
				return i18n.LocalizedErrorf("%s名称不能为空", field.label)
			}
			if seen[pairName] {
				return i18n.LocalizedErrorf("%s名称 %q 重复", field.label, pairName)
			}
			seen[pairName] = true
		}
	}
	return nil
}

func PairsPayload(pairs []PairDraft) []map[string]any {
	out := make([]map[string]any, 0, len(pairs))
	for _, entry := range pairs {
		out = append(out, map[string]any{
			"name":  strings.TrimSpace(entry.Name),
			"value": entry.Value,
		})
	}
	return out
}

func ViewPairsPayload(pairs []Pair) []map[string]any {
	out := make([]map[string]any, 0, len(pairs))
	for _, entry := range pairs {
		out = append(out, map[string]any{
			"name":  strings.TrimSpace(entry.Name),
			"value": entry.Value,
		})
	}
	return out
}

// DraftPayload emits every whitelisted mcp/set field. mcp/set merges
// omitted fields from the existing server with the same name, so explicit
// empty values and arrays are required to clear fields when the transport or
// pair lists change.
func DraftPayload(draft *ServerDraft) map[string]any {
	serverType := strings.TrimSpace(draft.Type)
	if serverType == "" {
		serverType = "stdio"
	}
	payload := map[string]any{
		"name":       strings.TrimSpace(draft.Name),
		"type":       serverType,
		"enabled":    draft.Enabled,
		"command":    "",
		"args":       []string{},
		"url":        "",
		"messageUrl": "",
		"headers":    PairsPayload(draft.Headers),
		"env":        PairsPayload(draft.Env),
	}
	switch serverType {
	case "stdio":
		payload["command"] = strings.TrimSpace(draft.Command)
		payload["args"] = SplitArgs(draft.ArgsInput)
	case "http":
		payload["url"] = strings.TrimSpace(draft.URL)
	case "sse":
		payload["url"] = strings.TrimSpace(draft.URL)
		payload["messageUrl"] = strings.TrimSpace(draft.MessageURL)
	}
	return payload
}

// ViewPayload round-trips an unedited existing server through the complete
// whitelist so a full replacement cannot drop fields from other entries.
func ViewPayload(view ServerView) map[string]any {
	args := view.Args
	if args == nil {
		args = []string{}
	}
	return map[string]any{
		"name":       strings.TrimSpace(view.Name),
		"type":       ServerType(view),
		"enabled":    ServerEnabled(view),
		"command":    view.Command,
		"args":       args,
		"url":        view.URL,
		"messageUrl": view.MessageURL,
		"headers":    ViewPairsPayload(view.Headers),
		"env":        ViewPairsPayload(view.Env),
	}
}

// BuildSetRequest builds the complete replacement list for mcp/set. The
// draft replaces editingIndex in place, or is appended when editingIndex is
// NewServerIdx, so editing never reorders the visible server list.
func BuildSetRequest(servers []ServerView, draft *ServerDraft, editingIndex int) (map[string]any, error) {
	if err := ValidateServerDraft(draft, servers, editingIndex); err != nil {
		return nil, err
	}
	editing := editingIndex >= 0 && editingIndex < len(servers)
	payload := make([]map[string]any, 0, len(servers)+1)
	for index, server := range servers {
		if editing && index == editingIndex {
			payload = append(payload, DraftPayload(draft))
			continue
		}
		payload = append(payload, ViewPayload(server))
	}
	if !editing {
		payload = append(payload, DraftPayload(draft))
	}
	return map[string]any{"servers": payload}, nil
}

// BuildDeleteRequest removes one server and sends the remaining complete
// list. Deleting the final server therefore clears the global list, matching
// the mcp/set full-replacement contract.
func BuildDeleteRequest(servers []ServerView, index int) (map[string]any, error) {
	if index < 0 || index >= len(servers) {
		return nil, i18n.LocalizedError("要删除的 MCP 服务器不存在")
	}
	payload := make([]map[string]any, 0, len(servers)-1)
	for i, server := range servers {
		if i == index {
			continue
		}
		payload = append(payload, ViewPayload(server))
	}
	return map[string]any{"servers": payload}, nil
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageMcp")
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "mcp" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight MCP
// response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Scope = ScopeGlobal
	p.Path = ""
	p.Servers = nil
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Deleting = false
	p.Status = ""
	p.StatusIsError = false
	p.EditorOpen = false
	p.EditingIndex = NewServerIdx
	p.Draft = EmptyDraft()
	p.DialogErr = ""
	p.PendingDelete = false
	p.PendingDeleteIndex = 0
}

// StartLoad starts one capability-gated global MCP load. It is called
// from the UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.SettingsOpen() || h.SettingsTab() != "mcp" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持 MCP 管理（manageMcp）。"
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

// mcpScopeParams projects the scope/session target shared by mcp/list and
// mcp/set. Project scope is authorized by the active Runtime session; the
// client never derives the project path itself.
func (p *Panel) ScopeParams(h Host) (map[string]any, error) {
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = ScopeGlobal
	}
	params := map[string]any{"scope": scope}
	if scope == ScopeProject {
		if strings.TrimSpace(h.CurrentSession()) == "" {
			return nil, i18n.LocalizedError("项目 MCP 需要先打开一个会话")
		}
		params["sessionId"] = h.CurrentSession()
	}
	return params, nil
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
	params, err := p.ScopeParams(h)
	if err != nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Loaded = true
			p.Err = err.Error()
		})
		return
	}
	raw, err := client.Call("mothx/manage/mcp/list", params)
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取 MCP 服务器配置失败：" + err.Error()
			return
		}
		var view ListView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "MCP 服务器配置响应无效：" + err.Error()
			return
		}
		p.ApplyListView(&view)
	})
}

func (p *Panel) ApplyListView(view *ListView) {
	if view == nil {
		return
	}
	scope := strings.TrimSpace(view.Scope)
	if scope == "" {
		scope = ScopeGlobal
	}
	servers := view.Servers
	if servers == nil {
		servers = []ServerView{}
	}
	p.Scope = scope
	p.Path = view.Path
	p.Servers = servers
	p.Loaded = true
	p.Err = ""
}

func (p *Panel) OpenNewDraft() {
	p.EditorOpen = true
	p.EditingIndex = NewServerIdx
	p.Draft = EmptyDraft()
	p.DialogErr = ""
}

func (p *Panel) OpenEditDraft(index int) {
	if index < 0 || index >= len(p.Servers) {
		return
	}
	p.EditorOpen = true
	p.EditingIndex = index
	p.Draft = DraftFromServer(p.Servers[index])
	p.DialogErr = ""
}

func (p *Panel) CloseEditor() {
	p.EditorOpen = false
	p.EditingIndex = NewServerIdx
	p.Draft = EmptyDraft()
	p.DialogErr = ""
}

func (p *Panel) AddPair(kind string) {
	entry := PairDraft{}
	if kind == "headers" {
		p.Draft.Headers = append(p.Draft.Headers, entry)
		return
	}
	p.Draft.Env = append(p.Draft.Env, entry)
}

func (p *Panel) RemovePair(kind string, index int) {
	if kind == "headers" {
		if index >= 0 && index < len(p.Draft.Headers) {
			p.Draft.Headers = append(p.Draft.Headers[:index], p.Draft.Headers[index+1:]...)
		}
		return
	}
	if index >= 0 && index < len(p.Draft.Env) {
		p.Draft.Env = append(p.Draft.Env[:index], p.Draft.Env[index+1:]...)
	}
}

func (p *Panel) Save(h Host) {
	if !p.Capable(h) {
		p.DialogErr = "当前运行时不支持 MCP 管理（manageMcp）。"
		return
	}
	request, err := BuildSetRequest(p.Servers, &p.Draft, p.EditingIndex)
	if err != nil {
		p.DialogErr = err.Error()
		return
	}
	scopeParams, err := p.ScopeParams(h)
	if err != nil {
		p.DialogErr = err.Error()
		return
	}
	for key, value := range scopeParams {
		request[key] = value
	}
	client := h.ACP()
	if client == nil {
		p.DialogErr = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = true
	p.DialogErr = ""
	p.Status = "正在保存 MCP 服务器…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/mcp/set", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				message := "保存 MCP 服务器失败：" + err.Error()
				p.Status = message
				p.StatusIsError = true
				p.DialogErr = message
				return
			}
			var view ListView
			if err := json.Unmarshal(raw, &view); err != nil {
				message := "保存后 MCP 服务器配置响应无效：" + err.Error()
				p.Status = message
				p.StatusIsError = true
				p.DialogErr = message
				return
			}
			p.ApplyListView(&view)
			p.EditorOpen = false
			p.EditingIndex = NewServerIdx
			p.Draft = EmptyDraft()
			p.DialogErr = ""
			p.Status = "MCP 服务器已保存"
			if len(view.ReloadedSessions) > 0 {
				p.Status += i18n.Tf("，%d 个会话已重新连接", len(view.ReloadedSessions))
			}
			p.StatusIsError = false
		})
	}()
}

func (p *Panel) RequestDelete(index int) {
	if index < 0 || index >= len(p.Servers) {
		p.Status = "要删除的 MCP 服务器不存在。"
		p.StatusIsError = true
		return
	}
	p.PendingDelete = true
	p.PendingDeleteIndex = index
}

func (p *Panel) CancelDelete() {
	p.PendingDelete = false
	p.PendingDeleteIndex = 0
}

func (p *Panel) ConfirmDelete(h Host) {
	index := p.PendingDeleteIndex
	if index < 0 || index >= len(p.Servers) {
		p.PendingDelete = false
		p.Status = "要删除的 MCP 服务器不存在。"
		p.StatusIsError = true
		return
	}
	if !p.Capable(h) {
		p.PendingDelete = false
		p.Status = "当前运行时不支持 MCP 管理（manageMcp）。"
		p.StatusIsError = true
		return
	}
	request, err := BuildDeleteRequest(p.Servers, index)
	if err != nil {
		p.PendingDelete = false
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	scopeParams, err := p.ScopeParams(h)
	if err != nil {
		p.PendingDelete = false
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	for key, value := range scopeParams {
		request[key] = value
	}
	client := h.ACP()
	if client == nil {
		p.PendingDelete = false
		p.Status = "删除失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	deletedName := p.Servers[index].Name
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Deleting = true
	p.Status = "正在删除 MCP 服务器…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/mcp/set", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Deleting = false
			p.PendingDelete = false
			p.PendingDeleteIndex = 0
			if err != nil {
				p.Status = "删除 MCP 服务器失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view ListView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "删除后 MCP 服务器配置响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyListView(&view)
			p.Status = i18n.Tf("MCP 服务器 %q 已删除", deletedName)
			if len(view.ReloadedSessions) > 0 {
				p.Status += i18n.Tf("，%d 个会话已重新连接", len(view.ReloadedSessions))
			}
			p.StatusIsError = false
		})
	}()
}
