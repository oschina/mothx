package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func routerTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{
		"manageRouter":      true,
		"manageACPProfiles": true,
		"workerMessages":    true,
		"subagentPanel":     true,
		"expertTeams":       true,
	})
	a.currentSession = "s1"
	a.router.Open = true
	a.router.SessionID = "s1"
	a.router.Loaded = true
	a.router.Workers = []acpWorkerProfileView{
		{ID: "w1", Title: "Claude", Enabled: true, RouterEligible: true, Model: "sonnet", Driver: "generic-acp"},
		{ID: "w2", Enabled: true, RouterEligible: false},
		{ID: "w3", Title: "Disabled", Enabled: false, RouterEligible: true},
		{ID: "w4", Title: "Codex", Enabled: true, RouterEligible: true},
	}
	return a
}

func TestRouterSelectableWorkersAndOrder(t *testing.T) {
	a := routerTestApp(t)
	workers := a.routerSelectableWorkers()
	if len(workers) != 2 || workers[0].ID != "w1" || workers[1].ID != "w4" {
		t.Fatalf("selectable workers = %+v", workers)
	}
	a.toggleRouterWorker("w4")
	a.toggleRouterWorker("w1")
	ids := a.routerSelectedIDs()
	if len(ids) != 2 || ids[0] != "w1" || ids[1] != "w4" {
		t.Fatalf("selected ids = %#v", ids)
	}
	a.toggleRouterWorker("w2")
	if a.router.Selection["w2"] {
		t.Fatalf("ineligible worker must not be selectable")
	}
	a.toggleRouterWorker("w1")
	if ids := a.routerSelectedIDs(); len(ids) != 1 || ids[0] != "w4" {
		t.Fatalf("unselect failed: %#v", ids)
	}
}

func TestApplyRouterState(t *testing.T) {
	a := routerTestApp(t)
	a.applyRouterState(&routerStateView{
		Enabled:        true,
		Mode:           "router",
		ProfileIDs:     []string{"w4"},
		PrimaryProfile: "w4",
	}, a.router.Workers)
	if !a.router.Enabled || a.router.Mode != "router" || len(a.router.ProfileIDs) != 1 {
		t.Fatalf("router state = %+v", a.router)
	}
	if !a.router.Selection["w4"] || a.router.Selection["w1"] {
		t.Fatalf("router selection = %#v", a.router.Selection)
	}
}

func TestWorkerMessageProjection(t *testing.T) {
	a := routerTestApp(t)
	a.router.WorkerLoaded = true
	a.router.Messages = []workerMessageView{
		{ID: "m1", Direction: "child", Kind: "report", ChildID: "w1", Status: "completed", Body: "完成接口审阅"},
	}
	a.router.PendingQuestions = []workerQuestionView{{
		ID:       "q1",
		ChildID:  "w1",
		RunID:    "r1",
		Question: "是否继续？",
		Options:  []string{"继续", "停止"},
	}}
	line := workerMessageLine(a.router.Messages[0])
	for _, want := range []string{"child", "report", "w1", "completed", "完成接口审阅"} {
		if !strings.Contains(line, want) {
			t.Fatalf("worker line missing %q: %q", want, line)
		}
	}
	if got := workerMessageLine(workerMessageView{}); got != "" {
		t.Fatalf("empty worker message rendered %q", got)
	}
	if got := workerQuestionIdentity(a.router.PendingQuestions[0]); got != "子代理 w1 · 运行 r1" {
		t.Fatalf("question identity = %q", got)
	}
	if got := workerQuestionOptionsLine(a.router.PendingQuestions[0]); got != "选项：继续 / 停止" {
		t.Fatalf("question options = %q", got)
	}
	if got := workerQuestionIdentity(workerQuestionView{ID: "q2"}); got != "问题 q2" {
		t.Fatalf("fallback question identity = %q", got)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"Worker 活动", "完成接口审阅", "待回答的 Worker 问题", "子代理 w1 · 运行 r1", "是否继续？", "选项：继续 / 停止"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("router worker UI missing %q: %s", want, texts)
		}
	}
}

func TestRouterDialogRenders(t *testing.T) {
	a := routerTestApp(t)
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"Router 协作", "Claude · w1", "Codex · w4", "启用 Router"} {
		if !textPresent(tt, want) {
			t.Fatalf("router dialog missing %q: %q", want, tt.Texts())
		}
	}
	// Ineligible and disabled workers are not offered.
	if strings.Contains(strings.Join(tt.Texts(), "\n"), "Disabled · w3") {
		t.Fatalf("disabled worker leaked into router dialog: %q", tt.Texts())
	}
}

func TestRouterCapabilityAndSessionGating(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.setFeatures(map[string]bool{})
	a.currentSession = "s1"
	a.openRouterPanel()
	if a.router.Open {
		t.Fatalf("router must be gated by manageRouter")
	}
	a.setFeatures(map[string]bool{"manageRouter": true})
	a.currentSession = ""
	a.openRouterPanel()
	if a.router.Open {
		t.Fatalf("router must require an open session")
	}
	a.currentSession = "s2"
	a.openRouterPanel()
	if !a.router.Open || a.router.SessionID != "s2" {
		t.Fatalf("router did not open for the session: %+v", a.router)
	}
}

func subagentTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := routerTestApp(t)
	a.subagent.Open = true
	a.subagent.SessionID = "s1"
	a.subagent.Loaded = true
	a.subagent.Agents = []subagentInfoView{
		{AgentID: "a1", MemberDisplayName: "架构师", MemberEmoji: "🧭", MemberRole: "architect", Status: "completed", MessageCount: 12},
		{AgentID: "a2", Status: "running", MessageCount: 3, MemberRole: "critic"},
	}
	a.subagent.Selected = "a1"
	a.subagent.Updates = []sessionUpdate{
		{SessionUpdate: "agent_message_chunk", Content: json.RawMessage(`{"type":"text","text":"已审阅接口"}`)},
		{SessionUpdate: "agent_thought_chunk", Content: json.RawMessage(`{"type":"text","text":"检查边界"}`)},
		{SessionUpdate: "tool_call", ToolCallID: "tc1", Title: "grep", Status: "completed", RawOutput: map[string]any{"content": "3 matches"}},
	}
	return a
}

func TestSubagentUpdateLines(t *testing.T) {
	line := subagentUpdateLine(sessionUpdate{SessionUpdate: "agent_message_chunk", Content: json.RawMessage(`{"type":"text","text":"完成"}`)})
	if line != "Agent：完成" {
		t.Fatalf("agent line = %q", line)
	}
	line = subagentUpdateLine(sessionUpdate{SessionUpdate: "tool_call", Title: "bash", Status: "completed", RawOutput: map[string]any{"content": "ok"}})
	if !strings.Contains(line, "工具：bash") || !strings.Contains(line, "ok") {
		t.Fatalf("tool line = %q", line)
	}
	if line := subagentUpdateLine(sessionUpdate{SessionUpdate: "usage_update"}); line != "" {
		t.Fatalf("unhandled update should render empty, got %q", line)
	}
	planned := subagentUpdateLine(sessionUpdate{SessionUpdate: "plan", Entries: []planEntry{
		{Content: "一"}, {Content: "二"},
	}})
	if planned != "计划：一; 二" {
		t.Fatalf("plan line = %q", planned)
	}
}

func TestSubagentDisplayAndDialog(t *testing.T) {
	a := subagentTestApp(t)
	if got := subagentDisplayName(a.subagent.Agents[0]); got != "🧭 架构师" {
		t.Fatalf("display name = %q", got)
	}
	if got := subagentDisplayName(a.subagent.Agents[1]); got != "critic" {
		t.Fatalf("role display name = %q", got)
	}
	info, ok := a.subagentSelectedInfo()
	if !ok || info.AgentID != "a1" {
		t.Fatalf("selected info = %+v", info)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"子代理", "🧭 架构师", "critic", "已审阅接口", "已思考", "grep"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("subagent dialog missing %q: %s", want, texts)
		}
	}
}

func TestSubagentCapabilityGating(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{})
	a.openSubagentPanel()
	if a.subagent.Open {
		t.Fatalf("subagent panel must be gated by subagentPanel")
	}
	a.setFeatures(map[string]bool{"subagentPanel": true})
	a.openSubagentPanel()
	if !a.subagent.Open || a.subagent.SessionID != "s1" {
		t.Fatalf("subagent panel did not open: %+v", a.subagent)
	}
	a.closeSubagentPanel()
	if a.subagent.Open {
		t.Fatalf("subagent panel did not close")
	}
}
