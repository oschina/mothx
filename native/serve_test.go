package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func serveIntPtr(value int) *int {
	return &value
}

func serveFloatPtr(value float64) *float64 {
	return &value
}

// serveTestView returns a complete ACP-safe serve projection used by the
// Native panel tests.
func serveTestView() serveConfigView {
	return serveConfigView{
		API: serveAPIView{
			Listen:                  "127.0.0.1:7872",
			DefaultMode:             "agent",
			DefaultThinkingLevel:    "high",
			EnableSubAgents:         true,
			EnableDelegate:          true,
			EnableWorkflows:         true,
			EnableWebSearch:         true,
			EnableBrowser:           true,
			EnableArtifact:          true,
			EnableA2AMaster:         true,
			ToolVisibility:          serveToolVisibilityView{Mode: "sse_event", Detail: "expanded"},
			SystemPromptMode:        "ignore",
			RequestTimeoutSeconds:   serveIntPtr(60),
			BackgroundRunMaxSeconds: serveIntPtr(90),
			MaxConcurrentRequests:   serveIntPtr(4),
			LogLevel:                "debug",
			Session: serveAPISessionView{
				IdleTimeoutSeconds: serveIntPtr(300),
				MaxSessions:        serveIntPtr(12),
			},
		},
		Features: serveFeaturesView{
			WebUI:      true,
			OpenAIAPI:  true,
			MultiAgent: true,
			Cron:       true,
			Memory:     true,
		},
		WebUI:    serveWebUIView{Enabled: true, Dir: "/srv/web"},
		Cron:     serveCronView{Enabled: true, Interval: serveIntPtr(15)},
		Memory:   serveMemoryView{Enabled: true, Path: "/srv/memory.md"},
		Security: serveSecurityView{SmartApprovals: true},
		Agent: serveAgentView{
			MaxTurns:                 serveIntPtr(8),
			BudgetPressure:           true,
			ContextPressure:          true,
			BudgetPressureThreshold:  serveFloatPtr(0.25),
			ContextPressureThreshold: serveFloatPtr(0.6),
			RunStaleTimeoutSeconds:   serveIntPtr(120),
			RunMaxDurationSeconds:    serveIntPtr(3600),
			BackgroundRunMaxSecs:     serveIntPtr(7200),
		},
		LobsterMode: true,
	}
}

func validServeForm() serveForm {
	form := serveFormFromView(&serveConfigView{})
	form.Listen = "127.0.0.1:7872"
	form.WebUIDir = "ui/dist"
	return form
}

func serveTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "serve"
	a.ensureSettingsTabVisible()
	view := serveTestView()
	a.applyServeView(&view)
	return a
}

func TestServeFormFromViewCoversAllSafeFields(t *testing.T) {
	view := serveTestView()
	form := serveFormFromView(&view)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"listen", form.Listen, "127.0.0.1:7872"},
		{"webui dir", form.WebUIDir, "/srv/web"},
		{"request timeout", form.RequestTimeout, "60"},
		{"background run max", form.BackgroundRunMax, "90"},
		{"max concurrent", form.MaxConcurrent, "4"},
		{"log level", form.LogLevel, "debug"},
		{"default mode", form.DefaultMode, "agent"},
		{"default thinking", form.DefaultThinking, "high"},
		{"system prompt mode", form.SystemPromptMode, "ignore"},
		{"tool mode", form.ToolMode, "sse_event"},
		{"tool detail", form.ToolDetail, "expanded"},
		{"cron interval", form.CronInterval, "15"},
		{"memory path", form.MemoryPath, "/srv/memory.md"},
		{"idle timeout", form.IdleTimeout, "300"},
		{"max sessions", form.MaxSessions, "12"},
		{"max turns", form.MaxTurns, "8"},
		{"budget threshold", form.BudgetThreshold, "0.25"},
		{"context threshold", form.ContextThreshold, "0.6"},
		{"run stale timeout", form.RunStaleTimeout, "120"},
		{"run max duration", form.RunMaxDuration, "3600"},
		{"agent background run max", form.AgentBackgroundRunMax, "7200"},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.name, check.got, check.want)
		}
	}
	if !form.WebUI || !form.OpenAIAPI || !form.MultiAgent || !form.CronFeature || !form.MemoryFeature {
		t.Fatalf("feature switches = %+v", form)
	}
	if !form.EnableDelegate || !form.EnableWorkflows || !form.EnableWebSearch ||
		!form.EnableBrowser || !form.EnableArtifact || !form.EnableA2AMaster {
		t.Fatalf("capability switches = %+v", form)
	}
	if !form.SmartApprovals || !form.BudgetPressure || !form.ContextPressure || !form.LobsterMode {
		t.Fatalf("boolean switches = %+v", form)
	}
}

func TestServeFormMissingFieldsKeepDesktopDefaults(t *testing.T) {
	form := serveFormFromView(&serveConfigView{})
	if form.RequestTimeout != "1" || form.BackgroundRunMax != "1" || form.MaxConcurrent != "0" ||
		form.LogLevel != "info" || form.DefaultMode != "yolo" || form.DefaultThinking != "medium" ||
		form.SystemPromptMode != "append" || form.ToolMode != "content" || form.ToolDetail != "collapsed" ||
		form.CronInterval != "1" || form.IdleTimeout != "1800" || form.MaxSessions != "0" ||
		form.MaxTurns != "1" || form.BudgetThreshold != "0.2" || form.ContextThreshold != "0.55" ||
		form.RunStaleTimeout != "1" || form.RunMaxDuration != "1" || form.AgentBackgroundRunMax != "1" {
		t.Fatalf("default form = %+v", form)
	}
}

func TestServePatchCoversAllSafeFields(t *testing.T) {
	form := serveTestViewForm()
	request, err := buildServePatch(form)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	patch, ok := body["patch"].(map[string]any)
	if !ok {
		t.Fatalf("patch envelope = %#v", body)
	}
	if len(patch) != 8 {
		t.Fatalf("patch sections = %d, want 8: %#v", len(patch), patch)
	}
	api := patch["api"].(map[string]any)
	if len(api) != 17 {
		t.Fatalf("api patch fields = %d, want 17: %#v", len(api), api)
	}
	for key, want := range map[string]any{
		"listen":                  "127.0.0.1:7872",
		"defaultMode":             "agent",
		"defaultThinkingLevel":    "high",
		"enableSubAgents":         true,
		"enableDelegate":          true,
		"enableWorkflows":         true,
		"enableWebSearch":         true,
		"enableBrowser":           true,
		"enableArtifact":          true,
		"enableA2AMaster":         true,
		"systemPromptMode":        "ignore",
		"requestTimeoutSeconds":   float64(60),
		"backgroundRunMaxSeconds": float64(90),
		"maxConcurrentRequests":   float64(4),
		"logLevel":                "debug",
	} {
		if api[key] != want {
			t.Errorf("api.%s = %#v, want %#v", key, api[key], want)
		}
	}
	toolVisibility := api["toolVisibility"].(map[string]any)
	if toolVisibility["mode"] != "sse_event" || toolVisibility["detail"] != "expanded" {
		t.Fatalf("toolVisibility = %#v", toolVisibility)
	}
	session := api["session"].(map[string]any)
	if session["idleTimeoutSeconds"] != float64(300) || session["maxSessions"] != float64(12) {
		t.Fatalf("api.session = %#v", session)
	}

	features := patch["features"].(map[string]any)
	if len(features) != 5 || features["webUI"] != true || features["openAIAPI"] != true ||
		features["multiAgent"] != true || features["cron"] != true || features["memory"] != true {
		t.Fatalf("features = %#v", features)
	}
	webUI := patch["webUI"].(map[string]any)
	if webUI["enabled"] != true || webUI["dir"] != "/srv/web" {
		t.Fatalf("webUI = %#v", webUI)
	}
	cron := patch["cron"].(map[string]any)
	if cron["enabled"] != true || cron["interval"] != float64(15) {
		t.Fatalf("cron = %#v", cron)
	}
	memory := patch["memory"].(map[string]any)
	if memory["enabled"] != true || memory["path"] != "/srv/memory.md" {
		t.Fatalf("memory = %#v", memory)
	}
	security := patch["security"].(map[string]any)
	if security["smartApprovals"] != true {
		t.Fatalf("security = %#v", security)
	}
	agent := patch["agent"].(map[string]any)
	if len(agent) != 8 || agent["maxTurns"] != float64(8) || agent["budgetPressure"] != true ||
		agent["contextPressure"] != true || agent["budgetPressureThreshold"] != 0.25 ||
		agent["contextPressureThreshold"] != 0.6 || agent["runStaleTimeoutSeconds"] != float64(120) ||
		agent["runMaxDurationSeconds"] != float64(3600) || agent["backgroundRunMaxSecs"] != float64(7200) {
		t.Fatalf("agent = %#v", agent)
	}
	if patch["lobsterMode"] != true {
		t.Fatalf("lobsterMode = %#v", patch["lobsterMode"])
	}
}

func serveTestViewForm() serveForm {
	form := serveTestView()
	return serveFormFromView(&form)
}

func TestServePatchTrimsEditablePaths(t *testing.T) {
	form := serveTestViewForm()
	form.Listen = "  127.0.0.1:9000  "
	form.WebUIDir = "  /srv/trimmed-web  "
	form.MemoryPath = "  /srv/trimmed-memory.md  "
	request, err := buildServePatch(form)
	if err != nil {
		t.Fatal(err)
	}
	patch := request["patch"].(map[string]any)
	api := patch["api"].(map[string]any)
	if api["listen"] != "127.0.0.1:9000" {
		t.Fatalf("listen = %#v", api["listen"])
	}
	if patch["webUI"].(map[string]any)["dir"] != "/srv/trimmed-web" {
		t.Fatalf("webUI dir = %#v", patch["webUI"])
	}
	if patch["memory"].(map[string]any)["path"] != "/srv/trimmed-memory.md" {
		t.Fatalf("memory path = %#v", patch["memory"])
	}
}

func TestServeFormValidation(t *testing.T) {
	base := validServeForm()
	cases := []struct {
		name   string
		mutate func(*serveForm)
		want   string
	}{
		{"empty listen", func(f *serveForm) { f.Listen = " " }, "监听地址"},
		{"empty webui dir", func(f *serveForm) { f.WebUIDir = "" }, "Web UI 目录"},
		{"invalid mode", func(f *serveForm) { f.DefaultMode = "turbo" }, "默认模式"},
		{"invalid thinking", func(f *serveForm) { f.DefaultThinking = "ultra" }, "默认思考级别"},
		{"invalid log level", func(f *serveForm) { f.LogLevel = "trace" }, "日志级别"},
		{"invalid system prompt mode", func(f *serveForm) { f.SystemPromptMode = "replace" }, "系统提示模式"},
		{"invalid tool mode", func(f *serveForm) { f.ToolMode = "json" }, "工具可见模式"},
		{"invalid tool detail", func(f *serveForm) { f.ToolDetail = "full" }, "工具详情级别"},
		{"zero request timeout", func(f *serveForm) { f.RequestTimeout = "0" }, "请求超时"},
		{"non numeric request timeout", func(f *serveForm) { f.RequestTimeout = "soon" }, "请求超时"},
		{"zero background run max", func(f *serveForm) { f.BackgroundRunMax = "0" }, "后台运行上限"},
		{"negative max concurrent", func(f *serveForm) { f.MaxConcurrent = "-1" }, "最大并发请求数"},
		{"zero cron interval", func(f *serveForm) { f.CronInterval = "0" }, "Cron 检查间隔"},
		{"zero idle timeout", func(f *serveForm) { f.IdleTimeout = "0" }, "会话空闲超时"},
		{"negative max sessions", func(f *serveForm) { f.MaxSessions = "-1" }, "最大会话数"},
		{"zero max turns", func(f *serveForm) { f.MaxTurns = "0" }, "最大轮次"},
		{"budget threshold too low", func(f *serveForm) { f.BudgetThreshold = "-0.1" }, "预算压力阈值"},
		{"budget threshold too high", func(f *serveForm) { f.BudgetThreshold = "1.1" }, "预算压力阈值"},
		{"budget threshold not a number", func(f *serveForm) { f.BudgetThreshold = "nan-ish" }, "预算压力阈值"},
		{"budget threshold NaN", func(f *serveForm) { f.BudgetThreshold = "NaN" }, "预算压力阈值"},
		{"context threshold too high", func(f *serveForm) { f.ContextThreshold = "2" }, "上下文压力阈值"},
		{"zero run stale timeout", func(f *serveForm) { f.RunStaleTimeout = "0" }, "运行停滞超时"},
		{"zero run max duration", func(f *serveForm) { f.RunMaxDuration = "0" }, "运行最长时长"},
		{"zero agent background run max", func(f *serveForm) { f.AgentBackgroundRunMax = "0" }, "Agent 后台运行上限"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := base
			tc.mutate(&form)
			err := validateServeForm(&form)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation error = %v, want substring %q", err, tc.want)
			}
			if _, err := buildServePatch(form); err == nil {
				t.Fatal("invalid form must not build a patch")
			}
		})
	}

	valid := base
	valid.MaxConcurrent = "0"
	valid.MaxSessions = "0"
	valid.BudgetThreshold = "1.0"
	valid.ContextThreshold = "1"
	if err := validateServeForm(&valid); err != nil {
		t.Fatalf("boundary values must be accepted: %v", err)
	}
	if _, err := buildServePatch(valid); err != nil {
		t.Fatalf("boundary values must build a patch: %v", err)
	}
	if err := validateServeForm(nil); err == nil {
		t.Fatal("nil form must be rejected")
	}
}

func TestServeCapabilityGatePreventsLoadAndHidesTab(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "serve"
	a.startServePanelLoad()
	if a.serveCapable() {
		t.Fatal("manageServeConfig must be required")
	}
	if a.serve.Loading || !a.serve.Loaded || !strings.Contains(a.serve.Err, "manageServeConfig") {
		t.Fatalf("gated load state = %+v", a.serve)
	}

	a.settingsCategory = "system"
	a.settingsTab = "serve"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "serve" {
		t.Fatalf("serve tab must be hidden without manageServeConfig: %+v", a.serve)
	}
	if a.settingsTab != "runtime" {
		t.Fatalf("expected local runtime fallback, got %q", a.settingsTab)
	}
}

func TestServeSaveValidatesAndReportsOfflineErrors(t *testing.T) {
	a := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	a.acp = nil

	a.serve.Form.Listen = "   "
	a.saveServeSettings()
	if a.serve.Saving || !a.serve.StatusIsError || !strings.Contains(a.serve.Status, "监听地址") {
		t.Fatalf("invalid listen save state = %+v", a.serve)
	}

	a.serve.Status = ""
	a.serve.StatusIsError = false
	a.serve.Form.Listen = "127.0.0.1:7872"
	a.saveServeSettings()
	if a.serve.Saving || !a.serve.StatusIsError || !strings.Contains(a.serve.Status, "尚未连接") {
		t.Fatalf("offline save state = %+v", a.serve)
	}

	a.serve.Status = ""
	a.serve.StatusIsError = false
	a.serve.Form.WebUIDir = ""
	a.saveServeSettings()
	if !a.serve.StatusIsError || !strings.Contains(a.serve.Status, "Web UI 目录") {
		t.Fatalf("invalid webui dir state = %+v", a.serve)
	}

	a.serve.Status = ""
	a.serve.StatusIsError = false
	a.serve.Form.WebUIDir = "ui/dist"
	a.setFeatures(nil)
	a.saveServeSettings()
	if !a.serve.StatusIsError || !strings.Contains(a.serve.Status, "manageServeConfig") {
		t.Fatalf("uncapable save state = %+v", a.serve)
	}
}

func TestServeSaveIsIgnoredWhileLoadingOrSaving(t *testing.T) {
	a := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	a.acp = nil
	a.serve.Status = ""
	a.serve.Loading = true
	a.saveServeSettings()
	if a.serve.Status != "" || a.serve.Saving {
		t.Fatalf("save while loading = %+v", a.serve)
	}

	a.serve.Loading = false
	a.serve.Saving = true
	a.saveServeSettings()
	if a.serve.Status != "" {
		t.Fatalf("save while saving = %+v", a.serve)
	}
}

func TestServeApplyViewHydratesTheGroupedEditor(t *testing.T) {
	a := testApp()
	view := serveTestView()
	a.applyServeView(&view)
	if !a.serve.Loaded || a.serve.Loading || a.serve.Err != "" {
		t.Fatalf("loaded state = %+v", a.serve)
	}
	if a.serve.Form.Listen != "127.0.0.1:7872" || a.serve.Form.WebUIDir != "/srv/web" ||
		a.serve.Form.MemoryPath != "/srv/memory.md" || !a.serve.Form.LobsterMode {
		t.Fatalf("hydrated form = %+v", a.serve.Form)
	}
	a.applyServeView(nil)
	if a.serve.Form.Listen != "127.0.0.1:7872" {
		t.Fatal("nil view must not clear hydrated state")
	}
}

func TestServePanelHeadlessRendersGroupedCards(t *testing.T) {
	a := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	tt := ui.NewTester(a.view, 1180, 3200)
	for _, want := range []string{
		"运行时 / API", "监听地址", "Web UI 目录", "请求超时（秒）",
		"后台运行最大时长（秒）", "最大并发请求数", "日志级别", "默认模式",
		"默认思考等级", "系统提示词模式",
		"功能开关", "OpenAI API", "多 Agent",
		"能力开关", "启用委派", "启用工作流", "启用 Web 搜索", "启用浏览器",
		"启用 WebUI/API 制品发布", "启用 A2A Master",
		"输出与工具可见性", "工具可见性模式", "工具详情",
		"自动化与记忆", "启用 Cron", "Cron 检查间隔（秒）", "启用 Memory", "Memory 文件路径",
		"安全", "智能审批",
		"会话限制", "空闲超时（秒）", "最大会话数",
		"Agent 行为", "最大回合数", "预算压力", "上下文压力",
		"预算压力阈值（0-1）", "上下文压力阈值（0-1）",
		"运行无事件超时（秒）", "运行最大时长（秒）", "后台运行最大时长（秒）",
		"Lobster 模式", "保存 Serve 配置", "重新加载",
		"这些设置仅保存到全局 serve.json",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("serve panel missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("需要 ACP 能力 manageServeConfig") {
		t.Fatalf("supported serve panel rendered the unsupported gate: %q", tt.Texts())
	}
	if tt.HasText("该设置项由 ACP-backed 服务管理") {
		t.Fatalf("serve panel still rendered the managed placeholder: %q", tt.Texts())
	}
}

func TestServePanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	seq := atomic.LoadInt64(&a.serve.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.servePanelCurrent(seq, reloadSeq) {
		t.Fatal("current serve request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.servePanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	b.serve.Saving = true
	b.serve.Status = "正在保存 Serve 配置…"
	oldSeq := atomic.LoadInt64(&b.serve.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.serve.Seq) <= oldSeq {
		t.Fatal("leaving the serve tab must invalidate in-flight state")
	}
	if b.serve.Loaded || b.serve.Saving || b.serve.Status != "" || b.serve.Form.Listen != "" {
		t.Fatalf("serve state was not invalidated on tab exit: %+v", b.serve)
	}

	c := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	c.closeSettings()
	if c.serve.Loaded || c.serve.Saving {
		t.Fatalf("closing settings must invalidate serve state: %+v", c.serve)
	}

	d := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	oldSeq = atomic.LoadInt64(&d.serve.Seq)
	d.setSettingsCategory("agent")
	if atomic.LoadInt64(&d.serve.Seq) <= oldSeq || d.serve.Loaded {
		t.Fatalf("changing settings category must invalidate serve state: %+v", d.serve)
	}

	e := serveTestApp(t, map[string]bool{"manageServeConfig": true})
	e.serve.Saving = true
	oldSeq = atomic.LoadInt64(&e.serve.Seq)
	e.setFeatures(nil)
	e.ensureSettingsTabVisible()
	if e.settingsTab == "serve" {
		t.Fatal("capability fallback did not leave the serve tab")
	}
	if atomic.LoadInt64(&e.serve.Seq) <= oldSeq || e.serve.Saving {
		t.Fatalf("capability fallback must invalidate serve state: %+v", e.serve)
	}
}
