package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP MCP management contract
// (mothx/manage/mcp/list and mothx/manage/mcp/set). Wire types mirror the
// complete local mcp.json schema in internal/acp/manage.go: environment and
// header values are first-class editable fields, and mcp/set is a full
// replacement of the global server list. Transcript state is Runtime-owned;
// this panel only projects and writes the configuration through ACP.

const (
	mcpScopeGlobal  = "global"
	mcpNewServerIdx = -1
)

var mcpServerTypes = []string{"stdio", "http", "sse"}

// mcpPair is one environment variable or request header entry.
type mcpPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// mcpServerView mirrors manageMCPViews. Enabled is a pointer so an omitted
// field keeps the legacy mcp.json meaning of "enabled".
type mcpServerView struct {
	Name       string    `json:"name"`
	Type       string    `json:"type,omitempty"`
	Command    string    `json:"command,omitempty"`
	Args       []string  `json:"args,omitempty"`
	URL        string    `json:"url,omitempty"`
	MessageURL string    `json:"messageUrl,omitempty"`
	Enabled    *bool     `json:"enabled,omitempty"`
	Env        []mcpPair `json:"env,omitempty"`
	Headers    []mcpPair `json:"headers,omitempty"`
}

// mcpListView mirrors the mcp/list and mcp/set response envelope.
type mcpListView struct {
	Scope            string          `json:"scope"`
	SessionID        string          `json:"sessionId,omitempty"`
	Path             string          `json:"path"`
	Servers          []mcpServerView `json:"servers"`
	ReloadedSessions []string        `json:"reloadedSessions,omitempty"`
}

type mcpPairDraft struct {
	Name  string
	Value string
}

// mcpServerDraft is the editable state of the add/edit dialog. Args are held
// as a shell-style word list so the native input can stay a single line.
type mcpServerDraft struct {
	Name       string
	Type       string
	Command    string
	ArgsInput  string
	URL        string
	MessageURL string
	Enabled    bool
	Env        []mcpPairDraft
	Headers    []mcpPairDraft
}

type mcpPanelState struct {
	Scope   string
	Path    string
	Servers []mcpServerView
	Loaded  bool
	Loading bool
	Err     string

	Saving        bool
	Deleting      bool
	Status        string
	StatusIsError bool

	EditorOpen   bool
	EditingIndex int
	Draft        mcpServerDraft
	DialogErr    string

	PendingDelete      bool
	PendingDeleteIndex int

	// Seq guards every asynchronous MCP call. Bumping it invalidates any
	// in-flight list/save/delete response, which is how tab exits, reconnects,
	// and editor changes prevent stale state from landing.
	Seq int64
}

func newMCPPanelState() mcpPanelState {
	return mcpPanelState{
		Scope:        mcpScopeGlobal,
		EditingIndex: mcpNewServerIdx,
		Draft:        mcpEmptyDraft(),
	}
}

func mcpEmptyDraft() mcpServerDraft {
	return mcpServerDraft{Type: "stdio", Enabled: true, Env: []mcpPairDraft{}, Headers: []mcpPairDraft{}}
}

func mcpServerType(view mcpServerView) string {
	value := strings.TrimSpace(view.Type)
	if value == "" {
		return "stdio"
	}
	return value
}

func mcpServerEnabled(view mcpServerView) bool {
	return view.Enabled == nil || *view.Enabled
}

func mcpDraftFromServer(view mcpServerView) mcpServerDraft {
	draft := mcpServerDraft{
		Name:       view.Name,
		Type:       mcpServerType(view),
		Command:    view.Command,
		ArgsInput:  strings.Join(view.Args, " "),
		URL:        view.URL,
		MessageURL: view.MessageURL,
		Enabled:    mcpServerEnabled(view),
		Env:        make([]mcpPairDraft, 0, len(view.Env)),
		Headers:    make([]mcpPairDraft, 0, len(view.Headers)),
	}
	for _, entry := range view.Env {
		draft.Env = append(draft.Env, mcpPairDraft{Name: entry.Name, Value: entry.Value})
	}
	for _, entry := range view.Headers {
		draft.Headers = append(draft.Headers, mcpPairDraft{Name: entry.Name, Value: entry.Value})
	}
	return draft
}

func mcpSplitArgs(input string) []string {
	return strings.Fields(input)
}

func mcpPairNameExists(pairs []mcpPairDraft, name string, skip int) bool {
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

// validateMCPServerDraft enforces the mcp/set contract client-side so an
// invalid editor never reaches ACP. editingIndex is the row being replaced
// (mcpNewServerIdx for a new entry).
func validateMCPServerDraft(draft *mcpServerDraft, servers []mcpServerView, editingIndex int) error {
	if draft == nil {
		return fmt.Errorf("MCP 服务器草稿不存在")
	}
	name := strings.TrimSpace(draft.Name)
	if name == "" {
		return fmt.Errorf("MCP 服务器名称不能为空")
	}
	for index, server := range servers {
		if index == editingIndex {
			continue
		}
		if strings.TrimSpace(server.Name) == name {
			return fmt.Errorf("MCP 服务器名称 %q 已存在", name)
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
		return fmt.Errorf("MCP 传输类型必须是 stdio、http 或 sse")
	}
	switch serverType {
	case "stdio":
		if strings.TrimSpace(draft.Command) == "" {
			return fmt.Errorf("stdio 服务器必须填写命令")
		}
	case "http", "sse":
		if strings.TrimSpace(draft.URL) == "" {
			return fmt.Errorf("%s 服务器必须填写 URL", serverType)
		}
	}
	for _, field := range []struct {
		label string
		pairs []mcpPairDraft
	}{
		{"环境变量", draft.Env},
		{"请求头", draft.Headers},
	} {
		seen := make(map[string]bool, len(field.pairs))
		for _, entry := range field.pairs {
			pairName := strings.TrimSpace(entry.Name)
			if pairName == "" {
				return fmt.Errorf("%s名称不能为空", field.label)
			}
			if seen[pairName] {
				return fmt.Errorf("%s名称 %q 重复", field.label, pairName)
			}
			seen[pairName] = true
		}
	}
	return nil
}

func mcpPairsPayload(pairs []mcpPairDraft) []map[string]any {
	out := make([]map[string]any, 0, len(pairs))
	for _, entry := range pairs {
		out = append(out, map[string]any{
			"name":  strings.TrimSpace(entry.Name),
			"value": entry.Value,
		})
	}
	return out
}

func mcpViewPairsPayload(pairs []mcpPair) []map[string]any {
	out := make([]map[string]any, 0, len(pairs))
	for _, entry := range pairs {
		out = append(out, map[string]any{
			"name":  strings.TrimSpace(entry.Name),
			"value": entry.Value,
		})
	}
	return out
}

// mcpDraftPayload emits every whitelisted mcp/set field. mcp/set merges
// omitted fields from the existing server with the same name, so explicit
// empty values and arrays are required to clear fields when the transport or
// pair lists change.
func mcpDraftPayload(draft *mcpServerDraft) map[string]any {
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
		"headers":    mcpPairsPayload(draft.Headers),
		"env":        mcpPairsPayload(draft.Env),
	}
	switch serverType {
	case "stdio":
		payload["command"] = strings.TrimSpace(draft.Command)
		payload["args"] = mcpSplitArgs(draft.ArgsInput)
	case "http":
		payload["url"] = strings.TrimSpace(draft.URL)
	case "sse":
		payload["url"] = strings.TrimSpace(draft.URL)
		payload["messageUrl"] = strings.TrimSpace(draft.MessageURL)
	}
	return payload
}

// mcpViewPayload round-trips an unedited existing server through the complete
// whitelist so a full replacement cannot drop fields from other entries.
func mcpViewPayload(view mcpServerView) map[string]any {
	args := view.Args
	if args == nil {
		args = []string{}
	}
	return map[string]any{
		"name":       strings.TrimSpace(view.Name),
		"type":       mcpServerType(view),
		"enabled":    mcpServerEnabled(view),
		"command":    view.Command,
		"args":       args,
		"url":        view.URL,
		"messageUrl": view.MessageURL,
		"headers":    mcpViewPairsPayload(view.Headers),
		"env":        mcpViewPairsPayload(view.Env),
	}
}

// buildMCPSetRequest builds the complete replacement list for mcp/set. The
// draft replaces editingIndex in place, or is appended when editingIndex is
// mcpNewServerIdx, so editing never reorders the visible server list.
func buildMCPSetRequest(servers []mcpServerView, draft *mcpServerDraft, editingIndex int) (map[string]any, error) {
	if err := validateMCPServerDraft(draft, servers, editingIndex); err != nil {
		return nil, err
	}
	editing := editingIndex >= 0 && editingIndex < len(servers)
	payload := make([]map[string]any, 0, len(servers)+1)
	for index, server := range servers {
		if editing && index == editingIndex {
			payload = append(payload, mcpDraftPayload(draft))
			continue
		}
		payload = append(payload, mcpViewPayload(server))
	}
	if !editing {
		payload = append(payload, mcpDraftPayload(draft))
	}
	return map[string]any{"servers": payload}, nil
}

// buildMCPDeleteRequest removes one server and sends the remaining complete
// list. Deleting the final server therefore clears the global list, matching
// the mcp/set full-replacement contract.
func buildMCPDeleteRequest(servers []mcpServerView, index int) (map[string]any, error) {
	if index < 0 || index >= len(servers) {
		return nil, fmt.Errorf("要删除的 MCP 服务器不存在")
	}
	payload := make([]map[string]any, 0, len(servers)-1)
	for i, server := range servers {
		if i == index {
			continue
		}
		payload = append(payload, mcpViewPayload(server))
	}
	return map[string]any{"servers": payload}, nil
}

func (a *nativeApp) mcpCapable() bool {
	return a.featureEnabled("manageMcp")
}

func (a *nativeApp) mcpPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "mcp" &&
		seq == atomic.LoadInt64(&a.mcp.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateMCPPanel drops transient state and makes every in-flight MCP
// response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateMCPPanel() {
	m := &a.mcp
	atomic.AddInt64(&m.Seq, 1)
	m.Scope = mcpScopeGlobal
	m.Path = ""
	m.Servers = nil
	m.Loaded = false
	m.Loading = false
	m.Saving = false
	m.Deleting = false
	m.Status = ""
	m.StatusIsError = false
	m.EditorOpen = false
	m.EditingIndex = mcpNewServerIdx
	m.Draft = mcpEmptyDraft()
	m.DialogErr = ""
	m.PendingDelete = false
	m.PendingDeleteIndex = 0
}

// startMCPPanelLoad starts one capability-gated global MCP load. It is called
// from the UI thread on tab entry.
func (a *nativeApp) startMCPPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "mcp" {
		return
	}
	m := &a.mcp
	if m.Loading {
		return
	}
	if !a.mcpCapable() {
		m.Loaded = true
		m.Err = "当前运行时不支持 MCP 管理（manageMcp）。"
		return
	}
	seq := atomic.AddInt64(&m.Seq, 1)
	m.Loading = true
	m.Err = ""
	m.Status = ""
	m.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadMCPPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadMCPPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.mcpPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.mcp.Loading = false
			a.mcp.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/mcp/list", map[string]any{})
	a.dispatch(func() {
		if !a.mcpPanelCurrent(seq, startReloadSeq) {
			return
		}
		m := &a.mcp
		m.Loading = false
		if err != nil {
			m.Err = "读取 MCP 服务器配置失败：" + err.Error()
			return
		}
		var view mcpListView
		if err := json.Unmarshal(raw, &view); err != nil {
			m.Err = "MCP 服务器配置响应无效：" + err.Error()
			return
		}
		a.applyMCPListView(&view)
	})
}

func (a *nativeApp) applyMCPListView(view *mcpListView) {
	if view == nil {
		return
	}
	m := &a.mcp
	scope := strings.TrimSpace(view.Scope)
	if scope == "" {
		scope = mcpScopeGlobal
	}
	servers := view.Servers
	if servers == nil {
		servers = []mcpServerView{}
	}
	m.Scope = scope
	m.Path = view.Path
	m.Servers = servers
	m.Loaded = true
	m.Err = ""
}

func (a *nativeApp) openMCPNewDraft() {
	m := &a.mcp
	m.EditorOpen = true
	m.EditingIndex = mcpNewServerIdx
	m.Draft = mcpEmptyDraft()
	m.DialogErr = ""
}

func (a *nativeApp) openMCPEditDraft(index int) {
	m := &a.mcp
	if index < 0 || index >= len(m.Servers) {
		return
	}
	m.EditorOpen = true
	m.EditingIndex = index
	m.Draft = mcpDraftFromServer(m.Servers[index])
	m.DialogErr = ""
}

func (a *nativeApp) closeMCPEditor() {
	m := &a.mcp
	m.EditorOpen = false
	m.EditingIndex = mcpNewServerIdx
	m.Draft = mcpEmptyDraft()
	m.DialogErr = ""
}

func (a *nativeApp) addMCPPair(kind string) {
	m := &a.mcp
	entry := mcpPairDraft{}
	if kind == "headers" {
		m.Draft.Headers = append(m.Draft.Headers, entry)
		return
	}
	m.Draft.Env = append(m.Draft.Env, entry)
}

func (a *nativeApp) removeMCPPair(kind string, index int) {
	m := &a.mcp
	if kind == "headers" {
		if index >= 0 && index < len(m.Draft.Headers) {
			m.Draft.Headers = append(m.Draft.Headers[:index], m.Draft.Headers[index+1:]...)
		}
		return
	}
	if index >= 0 && index < len(m.Draft.Env) {
		m.Draft.Env = append(m.Draft.Env[:index], m.Draft.Env[index+1:]...)
	}
}

func (a *nativeApp) saveMCPDraft() {
	m := &a.mcp
	if !a.mcpCapable() {
		m.DialogErr = "当前运行时不支持 MCP 管理（manageMcp）。"
		return
	}
	request, err := buildMCPSetRequest(m.Servers, &m.Draft, m.EditingIndex)
	if err != nil {
		m.DialogErr = err.Error()
		return
	}
	client := a.acp
	if client == nil {
		m.DialogErr = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&m.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	m.Saving = true
	m.DialogErr = ""
	m.Status = "正在保存 MCP 服务器…"
	m.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/mcp/set", request)
		a.dispatch(func() {
			if !a.mcpPanelCurrent(seq, startReloadSeq) {
				return
			}
			m := &a.mcp
			m.Saving = false
			if err != nil {
				message := "保存 MCP 服务器失败：" + err.Error()
				m.Status = message
				m.StatusIsError = true
				m.DialogErr = message
				return
			}
			var view mcpListView
			if err := json.Unmarshal(raw, &view); err != nil {
				message := "保存后 MCP 服务器配置响应无效：" + err.Error()
				m.Status = message
				m.StatusIsError = true
				m.DialogErr = message
				return
			}
			a.applyMCPListView(&view)
			m.EditorOpen = false
			m.EditingIndex = mcpNewServerIdx
			m.Draft = mcpEmptyDraft()
			m.DialogErr = ""
			m.Status = "MCP 服务器已保存"
			if len(view.ReloadedSessions) > 0 {
				m.Status += fmt.Sprintf("，%d 个会话已重新连接", len(view.ReloadedSessions))
			}
			m.StatusIsError = false
		})
	}()
}

func (a *nativeApp) requestDeleteMCP(index int) {
	m := &a.mcp
	if index < 0 || index >= len(m.Servers) {
		m.Status = "要删除的 MCP 服务器不存在。"
		m.StatusIsError = true
		return
	}
	m.PendingDelete = true
	m.PendingDeleteIndex = index
}

func (a *nativeApp) cancelDeleteMCP() {
	a.mcp.PendingDelete = false
	a.mcp.PendingDeleteIndex = 0
}

func (a *nativeApp) confirmDeleteMCP() {
	m := &a.mcp
	index := m.PendingDeleteIndex
	if index < 0 || index >= len(m.Servers) {
		m.PendingDelete = false
		m.Status = "要删除的 MCP 服务器不存在。"
		m.StatusIsError = true
		return
	}
	if !a.mcpCapable() {
		m.PendingDelete = false
		m.Status = "当前运行时不支持 MCP 管理（manageMcp）。"
		m.StatusIsError = true
		return
	}
	request, err := buildMCPDeleteRequest(m.Servers, index)
	if err != nil {
		m.PendingDelete = false
		m.Status = err.Error()
		m.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		m.PendingDelete = false
		m.Status = "删除失败：尚未连接到 MothX ACP。"
		m.StatusIsError = true
		return
	}
	deletedName := m.Servers[index].Name
	seq := atomic.AddInt64(&m.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	m.Deleting = true
	m.Status = "正在删除 MCP 服务器…"
	m.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/mcp/set", request)
		a.dispatch(func() {
			if !a.mcpPanelCurrent(seq, startReloadSeq) {
				return
			}
			m := &a.mcp
			m.Deleting = false
			m.PendingDelete = false
			m.PendingDeleteIndex = 0
			if err != nil {
				m.Status = "删除 MCP 服务器失败：" + err.Error()
				m.StatusIsError = true
				return
			}
			var view mcpListView
			if err := json.Unmarshal(raw, &view); err != nil {
				m.Status = "删除后 MCP 服务器配置响应无效：" + err.Error()
				m.StatusIsError = true
				return
			}
			a.applyMCPListView(&view)
			m.Status = fmt.Sprintf("MCP 服务器 %q 已删除", deletedName)
			if len(view.ReloadedSessions) > 0 {
				m.Status += fmt.Sprintf("，%d 个会话已重新连接", len(view.ReloadedSessions))
			}
			m.StatusIsError = false
		})
	}()
}
