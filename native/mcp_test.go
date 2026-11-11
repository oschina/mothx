package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func mcpBoolPtr(value bool) *bool { return &value }

func mcpTestServers() []mcpServerView {
	return []mcpServerView{
		{
			Name:    "filesystem",
			Type:    "stdio",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			Enabled: mcpBoolPtr(true),
			Env:     []mcpPair{{Name: "TOKEN", Value: "env-secret"}},
		},
		{
			Name:    "remote",
			Type:    "http",
			URL:     "https://mcp.example.org/api",
			Enabled: mcpBoolPtr(false),
			Headers: []mcpPair{{Name: "Authorization", Value: "Bearer header-secret"}},
		},
	}
}

func mcpTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "mcp"
	a.ensureSettingsTabVisible()
	a.mcp.Loaded = true
	a.mcp.Path = "/tmp/mcp.json"
	a.mcp.Servers = mcpTestServers()
	return a
}

func TestMCPListDecodeAndDraftRoundtrip(t *testing.T) {
	raw := json.RawMessage(`{
		"scope": "global",
		"path": "/home/user/.mothx/mcp.json",
		"servers": [
			{"name":"local","type":"stdio","command":"npx","args":["-y","server"],"enabled":true,
			 "env":[{"name":"TOKEN","value":"env-secret"}],"headers":[{"name":"X-Test","value":"header-value"}]},
			{"name":"sse","type":"sse","url":"https://example.org/sse","messageUrl":"https://example.org/messages","enabled":false},
			{"name":"legacy","command":"/bin/legacy"}
		]
	}`)
	var view mcpListView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Scope != "global" || view.Path != "/home/user/.mothx/mcp.json" || len(view.Servers) != 3 {
		t.Fatalf("list view = %+v", view)
	}
	local := view.Servers[0]
	if mcpServerType(local) != "stdio" || !mcpServerEnabled(local) || len(local.Env) != 1 || local.Env[0].Value != "env-secret" {
		t.Fatalf("local server = %+v", local)
	}
	sse := view.Servers[1]
	if mcpServerType(sse) != "sse" || mcpServerEnabled(sse) || sse.MessageURL != "https://example.org/messages" {
		t.Fatalf("sse server = %+v", sse)
	}
	legacy := view.Servers[2]
	if mcpServerType(legacy) != "stdio" || !mcpServerEnabled(legacy) {
		t.Fatalf("legacy defaults = %+v", legacy)
	}

	draft := mcpDraftFromServer(local)
	if draft.Name != "local" || draft.Type != "stdio" || draft.Command != "npx" ||
		draft.ArgsInput != "-y server" || !draft.Enabled ||
		len(draft.Env) != 1 || len(draft.Headers) != 1 {
		t.Fatalf("draft = %+v", draft)
	}
	payload := mcpDraftPayload(&draft)
	if payload["name"] != "local" || payload["type"] != "stdio" || payload["command"] != "npx" {
		t.Fatalf("stdio payload = %#v", payload)
	}
	args, _ := payload["args"].([]string)
	if len(args) != 2 || args[0] != "-y" || args[1] != "server" {
		t.Fatalf("stdio args = %#v", payload["args"])
	}
	if payload["url"] != "" || payload["messageUrl"] != "" {
		t.Fatalf("stdio payload must explicitly clear remote fields: %#v", payload)
	}
}

func TestMCPSetRequestBuildsFullReplacement(t *testing.T) {
	servers := mcpTestServers()

	draft := mcpServerDraft{
		Name:       "remote-sse",
		Type:       "sse",
		URL:        "https://example.org/sse",
		MessageURL: "https://example.org/messages",
		Enabled:    true,
		Env:        []mcpPairDraft{{Name: " TOKEN ", Value: "new-secret"}},
		Headers:    []mcpPairDraft{{Name: "Authorization", Value: "Bearer new-header"}},
	}
	request, err := buildMCPSetRequest(servers, &draft, mcpNewServerIdx)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := request["servers"].([]map[string]any)
	if len(payload) != 3 {
		t.Fatalf("replacement servers = %#v", payload)
	}
	// Existing entries are preserved byte-for-byte through the whitelist.
	if payload[0]["name"] != "filesystem" || payload[1]["name"] != "remote" {
		t.Fatalf("existing servers changed: %#v", payload)
	}
	added := payload[2]
	if added["name"] != "remote-sse" || added["type"] != "sse" ||
		added["url"] != "https://example.org/sse" || added["messageUrl"] != "https://example.org/messages" {
		t.Fatalf("sse payload = %#v", added)
	}
	if added["command"] != "" {
		t.Fatalf("sse payload must clear command: %#v", added)
	}
	if args, _ := added["args"].([]string); len(args) != 0 {
		t.Fatalf("sse payload must send an empty args array: %#v", added["args"])
	}
	env, _ := added["env"].([]map[string]any)
	if len(env) != 1 || env[0]["name"] != "TOKEN" || env[0]["value"] != "new-secret" {
		t.Fatalf("sse env = %#v", added["env"])
	}
	headers, _ := added["headers"].([]map[string]any)
	if len(headers) != 1 || headers[0]["name"] != "Authorization" {
		t.Fatalf("sse headers = %#v", added["headers"])
	}

	// Editing an existing stdio server and switching transport clears command
	// and args instead of letting mcp/set merge stale fields from the old row.
	edit := mcpDraftFromServer(servers[1])
	edit.Type = "http"
	edit.URL = "https://mcp.example.org/v2"
	request, err = buildMCPSetRequest(servers, &edit, 1)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ = request["servers"].([]map[string]any)
	if len(payload) != 2 {
		t.Fatalf("edited replacement = %#v", payload)
	}
	edited := payload[1]
	if edited["name"] != "remote" || edited["type"] != "http" || edited["url"] != "https://mcp.example.org/v2" {
		t.Fatalf("edited server = %#v", edited)
	}
	if edited["command"] != "" || edited["messageUrl"] != "" {
		t.Fatalf("edited remote server must clear stale fields: %#v", edited)
	}

	// Editing the first row replaces it in place rather than reordering the
	// card list.
	first := mcpDraftFromServer(servers[0])
	first.Name = "filesystem-renamed"
	request, err = buildMCPSetRequest(servers, &first, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ = request["servers"].([]map[string]any)
	if payload[0]["name"] != "filesystem-renamed" || payload[1]["name"] != "remote" {
		t.Fatalf("editing must preserve order: %#v", payload)
	}

	// Duplicate names are rejected against the replacement list, not the edit
	// slot being replaced.
	duplicate := mcpDraftFromServer(servers[1])
	duplicate.Name = "filesystem"
	if _, err := buildMCPSetRequest(servers, &duplicate, 1); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("duplicate name error = %v", err)
	}
}

func TestMCPDraftValidationUsesChineseErrors(t *testing.T) {
	servers := mcpTestServers()
	cases := []struct {
		name  string
		draft mcpServerDraft
		want  string
	}{
		{"empty name", mcpServerDraft{Type: "stdio", Command: "npx"}, "名称不能为空"},
		{"duplicate name", mcpServerDraft{Name: "filesystem", Type: "stdio", Command: "npx"}, "已存在"},
		{"invalid type", mcpServerDraft{Name: "new", Type: "carrier-pigeon", URL: "https://x"}, "传输类型"},
		{"stdio without command", mcpServerDraft{Name: "new", Type: "stdio"}, "必须填写命令"},
		{"http without url", mcpServerDraft{Name: "new", Type: "http"}, "必须填写 URL"},
		{"sse without url", mcpServerDraft{Name: "new", Type: "sse"}, "必须填写 URL"},
		{"empty env name", mcpServerDraft{Name: "new", Type: "stdio", Command: "npx", Env: []mcpPairDraft{{Name: " ", Value: "x"}}}, "环境变量名称不能为空"},
		{"empty header name", mcpServerDraft{Name: "new", Type: "stdio", Command: "npx", Headers: []mcpPairDraft{{Value: "x"}}}, "请求头名称不能为空"},
		{"duplicate env", mcpServerDraft{Name: "new", Type: "stdio", Command: "npx", Env: []mcpPairDraft{{Name: "A"}, {Name: " A "}}}, "环境变量名称"},
		{"duplicate header", mcpServerDraft{Name: "new", Type: "stdio", Command: "npx", Headers: []mcpPairDraft{{Name: "X"}, {Name: "X"}}}, "请求头名称"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateMCPServerDraft(&tc.draft, servers, mcpNewServerIdx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want Chinese substring %q", err, tc.want)
			}
		})
	}
}

func TestMCPDeleteRequestReplacesRemainingList(t *testing.T) {
	servers := mcpTestServers()
	request, err := buildMCPDeleteRequest(servers, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := request["servers"].([]map[string]any)
	if len(payload) != 1 || payload[0]["name"] != "remote" {
		t.Fatalf("delete payload = %#v", payload)
	}
	if _, err := buildMCPDeleteRequest(servers, 9); err == nil {
		t.Fatal("out-of-range delete index should fail")
	}
	only := []mcpServerView{servers[0]}
	request, err = buildMCPDeleteRequest(only, 0)
	if err != nil {
		t.Fatal(err)
	}
	payload, present := request["servers"].([]map[string]any)
	if !present || len(payload) != 0 {
		t.Fatalf("deleting the last server must send an empty list: %#v", request)
	}
}

func TestMCPPanelHeadlessRendersServersAndActions(t *testing.T) {
	a := mcpTestApp(t, map[string]bool{"manageMcp": true})
	tt := ui.NewTester(a.view, 1180, 900)
	want := []string{
		"MCP 服务器", "配置文件：/tmp/mcp.json", "完整替换列表",
		"filesystem", "remote", "stdio: npx", "http: https://mcp.example.org/api",
		"已启用", "已停用", "env: 1", "headers: 1", "编辑", "删除", "新增服务器",
	}
	for _, text := range want {
		if !textPresent(tt, text) {
			t.Fatalf("MCP panel missing %q: %q", text, tt.Texts())
		}
	}
	// The list only projects pair counts; values remain in the editor/dialog.
	if strings.Contains(strings.Join(tt.Texts(), " "), "env-secret") {
		t.Fatalf("MCP list leaked environment values: %q", tt.Texts())
	}
}

func TestMCPCapabilityGatePreventsLoadAndForm(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "mcp"
	a.startMCPPanelLoad()
	if a.mcpCapable() {
		t.Fatal("manageMcp must be required")
	}
	if a.mcp.Loading || !a.mcp.Loaded || !strings.Contains(a.mcp.Err, "manageMcp") {
		t.Fatalf("gated load state = %+v", a.mcp)
	}

	// The MCP section itself is hidden by the capability projection.
	if _, ok := a.settingsSectionByID("mcp"); !ok {
		t.Fatal("mcp section metadata is missing")
	}
	a.settingsCategory = "integrations"
	a.settingsTab = "mcp"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "mcp" {
		t.Fatalf("MCP tab must be hidden without manageMcp: %+v", a.mcp)
	}
}

func TestMCPEditorDialogRendersTransportFields(t *testing.T) {
	a := mcpTestApp(t, map[string]bool{"manageMcp": true})
	a.openMCPNewDraft()
	tt := ui.NewTester(a.view, 1180, 900)
	for _, text := range []string{
		"新建 MCP 服务器", "名称", "类型", "启用该服务器", "命令", "参数（空格分隔）",
		"请求头", "环境变量", "添加请求头", "添加环境变量", "保存", "取消",
	} {
		if !textPresent(tt, text) {
			t.Fatalf("new MCP dialog missing %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("消息 URL") {
		t.Fatalf("stdio draft must not show the SSE message URL: %q", tt.Texts())
	}

	a.mcp.Draft.Type = "sse"
	tt.Frame()
	for _, text := range []string{"URL", "消息 URL"} {
		if !textPresent(tt, text) {
			t.Fatalf("sse dialog missing %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("参数（空格分隔）") {
		t.Fatalf("sse draft must not show stdio arguments: %q", tt.Texts())
	}

	a.closeMCPEditor()
	a.openMCPEditDraft(1)
	tt.Frame()
	if !textPresent(tt, "编辑 MCP 服务器") {
		t.Fatalf("edit dialog missing title: %q", tt.Texts())
	}
}

func TestMCPDeleteRequiresNativeConfirmation(t *testing.T) {
	a := mcpTestApp(t, map[string]bool{"manageMcp": true})
	a.requestDeleteMCP(1)
	if !a.mcp.PendingDelete || a.mcp.PendingDeleteIndex != 1 {
		t.Fatalf("delete request state = %+v", a.mcp)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, text := range []string{"删除 MCP 服务器？", "remote", "确认删除", "取消", "完整替换服务器列表"} {
		if !textPresent(tt, text) {
			t.Fatalf("delete dialog missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click cancel: %v", err)
	}
	if a.mcp.PendingDelete {
		t.Fatal("cancel did not close the delete confirmation")
	}

	// Without an ACP connection the confirmation still closes and reports a
	// Chinese error instead of silently mutating local state.
	a.requestDeleteMCP(0)
	a.confirmDeleteMCP()
	if a.mcp.PendingDelete || !a.mcp.StatusIsError || !strings.Contains(a.mcp.Status, "尚未连接") {
		t.Fatalf("offline delete state = %+v", a.mcp)
	}
}

func TestMCPSaveValidationAndOfflineErrors(t *testing.T) {
	a := mcpTestApp(t, map[string]bool{"manageMcp": true})
	a.openMCPNewDraft()
	a.mcp.Draft.Name = ""
	a.saveMCPDraft()
	if !strings.Contains(a.mcp.DialogErr, "名称不能为空") {
		t.Fatalf("validation error = %q", a.mcp.DialogErr)
	}

	a.mcp.Draft.Name = "new-server"
	a.mcp.Draft.Command = "npx"
	a.saveMCPDraft()
	if !strings.Contains(a.mcp.DialogErr, "尚未连接") {
		t.Fatalf("offline save error = %q", a.mcp.DialogErr)
	}
	if a.mcp.Saving {
		t.Fatal("offline save must not remain in the saving state")
	}
}

func TestMCPPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := mcpTestApp(t, map[string]bool{"manageMcp": true})
	seq := atomic.LoadInt64(&a.mcp.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.mcpPanelCurrent(seq, reloadSeq) {
		t.Fatal("current MCP request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.mcpPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := mcpTestApp(t, map[string]bool{"manageMcp": true})
	b.openMCPNewDraft()
	b.mcp.Status = "正在保存 MCP 服务器…"
	b.mcp.Saving = true
	oldSeq := atomic.LoadInt64(&b.mcp.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.mcp.Seq) <= oldSeq {
		t.Fatal("leaving the MCP tab must invalidate in-flight state")
	}
	if b.mcp.Loaded || b.mcp.Saving || b.mcp.EditorOpen || b.mcp.Servers != nil {
		t.Fatalf("MCP state was not invalidated on tab exit: %+v", b.mcp)
	}

	c := mcpTestApp(t, map[string]bool{"manageMcp": true})
	c.openMCPNewDraft()
	c.closeSettings()
	if c.mcp.Loaded || c.mcp.EditorOpen || c.mcp.Servers != nil {
		t.Fatalf("closing settings must invalidate MCP state: %+v", c.mcp)
	}
}
