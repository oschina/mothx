package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func cronTestJobs() []cronJobView {
	return []cronJobView{
		{
			ID:         "cron-daily",
			Name:       "每日报告",
			Prompt:     "生成昨天的项目日报并总结风险",
			Schedule:   "@daily",
			Mode:       "yolo",
			Enabled:    true,
			WorkDir:    "/home/user/project",
			RunCount:   3,
			LastStatus: "success",
			LastRun:    "2026-11-10T09:00:00Z",
			NextRun:    "2026-11-12T00:00:00Z",
			CreatedAt:  "2026-11-01T08:00:00Z",
		},
		{
			ID:         "cron-once",
			Name:       "一次性清理",
			Prompt:     "清理临时缓存",
			Schedule:   "",
			OneShot:    true,
			Mode:       "agent",
			Enabled:    false,
			LastStatus: "failed",
			LastError:  "provider timeout",
			CreatedAt:  "2026-11-02T08:00:00Z",
		},
	}
}

func cronTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "automation"
	a.settingsTab = "cron"
	a.ensureSettingsTabVisible()
	a.cron.Loaded = true
	a.cron.Enabled = true
	a.cron.Running = true
	a.cron.Jobs = cronTestJobs()
	return a
}

func TestCronListDecodeAndJobFormatting(t *testing.T) {
	raw := json.RawMessage(`{
		"enabled": true,
		"running": true,
		"jobs": [
			{"id":"cron-1","name":"nightly","prompt":"run it","schedule":"@daily","mode":"yolo","enabled":true,
			 "runCount":4,"lastStatus":"success","lastRun":"2026-11-10T09:00:00Z","nextRun":"2026-11-12T00:00:00Z",
			 "workDir":"/tmp/project","createdAt":"2026-11-01T08:00:00Z"},
			{"id":"cron-2","name":"","prompt":"clean","schedule":"","oneshot":true,"mode":"agent","enabled":false,"lastStatus":"failed"}
		]
	}`)
	var view cronListView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if !view.Enabled || !view.Running || len(view.Jobs) != 2 {
		t.Fatalf("list view = %+v", view)
	}
	job := view.Jobs[0]
	if job.ID != "cron-1" || job.Name != "nightly" || job.Mode != "yolo" || job.RunCount != 4 || job.LastStatus != "success" {
		t.Fatalf("job = %+v", job)
	}
	if cronJobScheduleLabel(job) != "@daily" {
		t.Fatalf("schedule label = %q", cronJobScheduleLabel(job))
	}
	meta := cronJobMeta(job)
	for _, want := range []string{"计划:@daily", "模式:yolo", "工作区:/tmp/project", "上次运行:", "成功", "下次运行:", "运行次数:4"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("meta = %q, missing %q", meta, want)
		}
	}

	oneShot := view.Jobs[1]
	if cronJobDisplayName(oneShot) != "cron-2" || cronJobScheduleLabel(oneShot) != "单次" {
		t.Fatalf("one-shot projection = %+v", oneShot)
	}
	if !strings.Contains(cronJobMeta(oneShot), "状态:失败") {
		t.Fatalf("one-shot meta = %q", cronJobMeta(oneShot))
	}
	if got := cronRunStatusLabel("completed"); got != "成功" {
		t.Fatalf("completed status = %q", got)
	}
	if got := cronRunStatusLabel("unknown"); got != "unknown" {
		t.Fatalf("unknown status = %q", got)
	}

	a := testApp()
	a.applyCronListView(&view)
	if !a.cron.Loaded || a.cron.Loading || len(a.cron.Jobs) != 2 || !a.cron.Running {
		t.Fatalf("applied list = %+v", a.cron)
	}
}

func TestCronDraftValidationAndRequestBuilders(t *testing.T) {
	draft := cronJobDraft{}
	if err := validateCronDraft(&draft); err == nil || !strings.Contains(err.Error(), "名称") {
		t.Fatalf("empty name error = %v", err)
	}
	draft.Name = "nightly"
	if err := validateCronDraft(&draft); err == nil || !strings.Contains(err.Error(), "表达式") {
		t.Fatalf("empty schedule error = %v", err)
	}
	draft.Schedule = "@daily"
	if err := validateCronDraft(&draft); err == nil || !strings.Contains(err.Error(), "提示词") {
		t.Fatalf("empty prompt error = %v", err)
	}
	draft.Prompt = "run"
	draft.Mode = "turbo"
	if err := validateCronDraft(&draft); err == nil || !strings.Contains(err.Error(), "agent 或 yolo") {
		t.Fatalf("bad mode error = %v", err)
	}

	draft = cronJobDraft{
		Name:     " nightly ",
		Schedule: " @daily ",
		Prompt:   " run ",
		Enabled:  true,
	}
	request, err := buildCronCreateRequest(&draft)
	if err != nil {
		t.Fatal(err)
	}
	if request["name"] != "nightly" || request["schedule"] != "@daily" || request["prompt"] != "run" ||
		request["mode"] != cronModeYolo || request["enabled"] != true {
		t.Fatalf("create request = %#v", request)
	}
	if _, hasID := request["id"]; hasID {
		t.Fatalf("create request must not carry an id: %#v", request)
	}

	draft.ID = "cron-1"
	draft.Mode = "agent"
	request, err = buildCronUpdateRequest(&draft)
	if err != nil {
		t.Fatal(err)
	}
	if request["id"] != "cron-1" || request["mode"] != "agent" {
		t.Fatalf("update request = %#v", request)
	}
	if _, err := buildCronUpdateRequest(&cronJobDraft{Name: "n", Schedule: "@daily", Prompt: "p"}); err == nil ||
		!strings.Contains(err.Error(), "ID") {
		t.Fatalf("update without id error = %v", err)
	}

	for name, build := range map[string]func(string) (map[string]any, error){
		"remove": buildCronRemoveRequest,
		"run":    buildCronRunRequest,
	} {
		request, err := build(" cron-1 ")
		if err != nil {
			t.Fatalf("%s request: %v", name, err)
		}
		if request["id"] != "cron-1" {
			t.Fatalf("%s request = %#v", name, request)
		}
		if _, err := build(" "); err == nil {
			t.Fatalf("%s must reject an empty id", name)
		}
	}

	toggle, err := buildCronToggleRequest("cron-1", false)
	if err != nil || toggle["id"] != "cron-1" || toggle["enabled"] != false {
		t.Fatalf("toggle request = %#v, err = %v", toggle, err)
	}
}

func TestCronPanelHeadlessRendersJobsAndActions(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	tt := ui.NewTester(a.view, 1180, 900)
	want := []string{
		"定时任务", "调度器运行中", "2 个任务",
		"每日报告", "一次性清理", "已启用", "已暂停", "单次", "yolo", "agent",
		"计划:@daily", "上次运行:", "成功", "下次运行:", "运行次数:3",
		"工作区:/home/user/project", "生成昨天的项目日报", "最近错误：provider timeout",
		"立即运行", "暂停", "恢复", "编辑", "删除", "新建任务", "刷新",
	}
	for _, text := range want {
		if !textPresent(tt, text) {
			t.Fatalf("cron panel missing %q: %q", text, tt.Texts())
		}
	}
}

func TestCronCapabilityGatePreventsLoadAndForm(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "cron"
	a.startCronPanelLoad()
	if a.cronCapable() {
		t.Fatal("manageCron must be required")
	}
	if a.cron.Loading || !a.cron.Loaded || !strings.Contains(a.cron.Err, "manageCron") {
		t.Fatalf("gated load state = %+v", a.cron)
	}

	if _, ok := a.settingsSectionByID("cron"); !ok {
		t.Fatal("cron section metadata is missing")
	}
	a.settingsCategory = "automation"
	a.settingsTab = "cron"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "cron" {
		t.Fatalf("cron tab must be hidden without manageCron: %+v", a.cron)
	}

	// The editor and delete confirmation cannot open through the gated panel.
	a.openCronNewDraft()
	a.saveCronDraft()
	if !strings.Contains(a.cron.DialogErr, "manageCron") {
		t.Fatalf("gated save error = %q", a.cron.DialogErr)
	}
}

func TestCronEditorDialogRendersAndPrefills(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	tt := ui.NewTester(a.view, 1180, 900)
	if err := tt.Click("新建任务"); err != nil {
		t.Fatalf("click new task: %v", err)
	}
	if !a.cron.EditorOpen || a.cron.EditingID != "" {
		t.Fatalf("new task button did not open an empty editor: %+v", a.cron)
	}
	tt.Frame()
	for _, text := range []string{
		"新建定时任务", "名称", "Cron 表达式", "模式", "启用任务", "任务提示词",
		"保存", "取消", "mothx/manage/cron/create", "@once",
	} {
		if !textPresent(tt, text) {
			t.Fatalf("new cron dialog missing %q: %q", text, tt.Texts())
		}
	}

	a.closeCronEditor()
	a.openCronEditDraft(0)
	tt.Frame()
	if !textPresent(tt, "编辑定时任务") {
		t.Fatalf("edit dialog missing title: %q", tt.Texts())
	}
	if a.cron.EditingID != "cron-daily" || a.cron.Draft.Name != "每日报告" ||
		a.cron.Draft.Schedule != "@daily" || a.cron.Draft.Mode != "yolo" || !a.cron.Draft.Enabled {
		t.Fatalf("edit draft = %+v", a.cron.Draft)
	}
	if a.cron.Draft.Prompt == "" {
		t.Fatal("edit draft must prefill the prompt")
	}
}

func TestCronDeleteRequiresNativeConfirmation(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	a.requestDeleteCron(1)
	if !a.cron.PendingDelete || a.cron.PendingDeleteID != "cron-once" {
		t.Fatalf("delete request state = %+v", a.cron)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, text := range []string{"删除定时任务？", "一次性清理", "确认删除", "取消", "共享 Cron 存储"} {
		if !textPresent(tt, text) {
			t.Fatalf("delete dialog missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click cancel: %v", err)
	}
	if a.cron.PendingDelete {
		t.Fatal("cancel did not close the delete confirmation")
	}

	// Without an ACP connection the confirmation still closes and reports a
	// Chinese error instead of silently mutating local state.
	a.requestDeleteCron(0)
	a.confirmDeleteCron()
	if a.cron.PendingDelete || !a.cron.StatusIsError || !strings.Contains(a.cron.Status, "尚未连接") {
		t.Fatalf("offline delete state = %+v", a.cron)
	}
}

func TestCronSaveValidationAndOfflineErrors(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	a.openCronNewDraft()
	a.cron.Draft.Name = ""
	a.saveCronDraft()
	if !strings.Contains(a.cron.DialogErr, "名称不能为空") {
		t.Fatalf("validation error = %q", a.cron.DialogErr)
	}

	a.cron.Draft.Name = "new-job"
	a.cron.Draft.Schedule = "@daily"
	a.cron.Draft.Prompt = "do it"
	a.saveCronDraft()
	if !strings.Contains(a.cron.DialogErr, "尚未连接") {
		t.Fatalf("offline save error = %q", a.cron.DialogErr)
	}
	if a.cron.Saving {
		t.Fatal("offline save must not remain in the saving state")
	}
}

func TestCronToggleAndRunOfflineErrors(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})

	a.toggleCronJob(0)
	if a.cron.TogglingID != "" || !a.cron.StatusIsError || !strings.Contains(a.cron.Status, "尚未连接") {
		t.Fatalf("offline toggle state = %+v", a.cron)
	}
	a.runCronJob(1)
	if a.cron.RunningJobID != "" || !a.cron.StatusIsError || !strings.Contains(a.cron.Status, "尚未连接") {
		t.Fatalf("offline run state = %+v", a.cron)
	}

	a.runCronJob(9)
	if !strings.Contains(a.cron.Status, "不存在") {
		t.Fatalf("out-of-range run status = %q", a.cron.Status)
	}
	a.toggleCronJob(-1)
	if !strings.Contains(a.cron.Status, "不存在") {
		t.Fatalf("out-of-range toggle status = %q", a.cron.Status)
	}
}

func TestCronPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	seq := atomic.LoadInt64(&a.cron.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.cronPanelCurrent(seq, reloadSeq) {
		t.Fatal("current cron request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.cronPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := cronTestApp(t, map[string]bool{"manageCron": true})
	b.openCronNewDraft()
	b.cron.Status = "正在保存定时任务…"
	b.cron.Saving = true
	b.requestDeleteCron(0)
	b.cron.RefreshPending = true
	oldSeq := atomic.LoadInt64(&b.cron.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.cron.Seq) <= oldSeq {
		t.Fatal("leaving the cron tab must invalidate in-flight state")
	}
	if b.cron.Loaded || b.cron.Saving || b.cron.EditorOpen || b.cron.PendingDelete ||
		b.cron.Jobs != nil || b.cron.RefreshPending {
		t.Fatalf("cron state was not invalidated on tab exit: %+v", b.cron)
	}

	c := cronTestApp(t, map[string]bool{"manageCron": true})
	c.openCronNewDraft()
	c.closeSettings()
	if c.cron.Loaded || c.cron.EditorOpen || c.cron.Jobs != nil {
		t.Fatalf("closing settings must invalidate cron state: %+v", c.cron)
	}
}

func TestCronCompletedEventMarksStatusAndDefersRefresh(t *testing.T) {
	a := cronTestApp(t, map[string]bool{"manageCron": true})
	a.cron.Loading = true
	a.handleSessionEvent(sessionEvent{Event: "cron_completed", JobID: "cron-daily", Status: "success"})
	if !strings.Contains(a.cron.Status, "运行完成") || a.cron.StatusIsError {
		t.Fatalf("success completion status = %+v", a.cron)
	}
	if !a.cron.RefreshPending {
		t.Fatal("completion during an in-flight request must defer the refresh")
	}

	a.cron.RefreshPending = false
	a.handleSessionEvent(sessionEvent{Event: "cron_completed", JobID: "cron-once", Status: "failed"})
	if !strings.Contains(a.cron.Status, "运行失败") || !a.cron.StatusIsError {
		t.Fatalf("failed completion status = %+v", a.cron)
	}
	if !a.cron.RefreshPending {
		t.Fatal("failed completion during an in-flight request must defer the refresh")
	}

	// A completion event for a closed settings page is ignored.
	b := testApp()
	b.setFeatures(map[string]bool{"manageCron": true})
	b.handleSessionEvent(sessionEvent{Event: "cron_completed", JobID: "x", Status: "success"})
	if b.cron.Status != "" || b.cron.RefreshPending {
		t.Fatalf("closed settings reacted to cron_completed: %+v", b.cron)
	}
}
