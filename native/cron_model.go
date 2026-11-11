package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// This file owns the Native projection of the ACP Cron management contract
// (mothx/manage/cron/list, create, update, remove, and run). Cron jobs are
// Runtime-owned records in the shared SQLite store; this panel only projects
// them and writes through ACP. It never reads sessions.db or settings.json and
// never starts a scheduler of its own.

const cronModeYolo = "yolo"

var cronModes = []string{"agent", "yolo"}

// cronJobView mirrors the job shape projected by mothx/manage/cron/*. The A2A
// token is never projected by the server, so it is intentionally absent here.
type cronJobView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prompt     string `json:"prompt"`
	Schedule   string `json:"schedule"`
	OneShot    bool   `json:"oneshot"`
	Mode       string `json:"mode"`
	Enabled    bool   `json:"enabled"`
	WorkDir    string `json:"workDir,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	A2ATarget  string `json:"a2aTarget,omitempty"`
	RunCount   int    `json:"runCount"`
	LastStatus string `json:"lastStatus,omitempty"`
	LastRun    string `json:"lastRun,omitempty"`
	NextRun    string `json:"nextRun,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// cronListView mirrors the cron/list response envelope.
type cronListView struct {
	Enabled bool          `json:"enabled"`
	Running bool          `json:"running"`
	Jobs    []cronJobView `json:"jobs"`
}

// cronJobResult is the create/update response envelope.
type cronJobResult struct {
	Job *cronJobView `json:"job"`
}

// cronRemoveResult is the remove response envelope.
type cronRemoveResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// cronRunResult is the run response envelope returned after a manual trigger.
type cronRunResult struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"jobId"`
	Triggered bool   `json:"triggered"`
}

// cronJobDraft is the editable state of the create/edit dialog.
type cronJobDraft struct {
	ID       string
	Name     string
	Schedule string
	Prompt   string
	Mode     string
	Enabled  bool
}

type cronPanelState struct {
	Enabled bool
	Running bool
	Jobs    []cronJobView
	Loaded  bool
	Loading bool
	Err     string

	Saving         bool
	TogglingID     string
	RunningJobID   string
	Deleting       bool
	Status         string
	StatusIsError  bool
	RefreshPending bool

	EditorOpen bool
	EditingID  string
	Draft      cronJobDraft
	DialogErr  string

	PendingDelete   bool
	PendingDeleteID string

	// Seq guards every asynchronous cron call. Bumping it invalidates any
	// in-flight list/save/toggle/delete/run response, which is how tab exits,
	// settings close, reconnects, and editor changes prevent stale state.
	Seq int64
}

func newCronPanelState() cronPanelState {
	return cronPanelState{
		Jobs:  []cronJobView{},
		Draft: cronEmptyDraft(),
	}
}

func cronEmptyDraft() cronJobDraft {
	return cronJobDraft{Mode: cronModeYolo, Enabled: true}
}

func cronDraftFromJob(job cronJobView) cronJobDraft {
	mode := strings.TrimSpace(job.Mode)
	if mode == "" {
		mode = cronModeYolo
	}
	return cronJobDraft{
		ID:       strings.TrimSpace(job.ID),
		Name:     job.Name,
		Schedule: job.Schedule,
		Prompt:   job.Prompt,
		Mode:     mode,
		Enabled:  job.Enabled,
	}
}

func cronJobDisplayName(job cronJobView) string {
	if name := strings.TrimSpace(job.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(job.ID); id != "" {
		return id
	}
	return "未命名任务"
}

// cronJobScheduleLabel renders an empty or @once schedule as a one-shot job,
// matching the shared cron normalization contract.
func cronJobScheduleLabel(job cronJobView) string {
	schedule := strings.TrimSpace(job.Schedule)
	if job.OneShot || schedule == "" || strings.EqualFold(schedule, "@once") {
		return "单次"
	}
	return schedule
}

func cronRunStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "completed":
		return "成功"
	case "failed", "failure", "error":
		return "失败"
	case "running":
		return "运行中"
	case "":
		return ""
	default:
		return strings.TrimSpace(status)
	}
}

func cronParseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func cronLastRunLabel(value string) string {
	value = strings.TrimSpace(value)
	parsed, ok := cronParseTimestamp(value)
	if !ok {
		return value
	}
	label := parsed.Local().Format("2006-01-02 15:04")
	if relative := relativeTime(value); relative != "" {
		return relative + " · " + label
	}
	return label
}

func cronNextRunLabel(value string) string {
	value = strings.TrimSpace(value)
	parsed, ok := cronParseTimestamp(value)
	if !ok {
		return value
	}
	return parsed.Local().Format("2006-01-02 15:04")
}

// cronJobMeta projects the run metadata shown in each job row. Timestamps are
// display-only; the Runtime list response remains authoritative.
func cronJobMeta(job cronJobView) string {
	parts := []string{"计划:" + cronJobScheduleLabel(job)}
	if mode := strings.TrimSpace(job.Mode); mode != "" {
		parts = append(parts, "模式:"+mode)
	}
	if workDir := strings.TrimSpace(job.WorkDir); workDir != "" {
		parts = append(parts, "工作区:"+workDir)
	}
	if job.LastRun != "" {
		last := "上次运行:" + cronLastRunLabel(job.LastRun)
		if status := cronRunStatusLabel(job.LastStatus); status != "" {
			last += " · " + status
		}
		parts = append(parts, last)
	} else if status := cronRunStatusLabel(job.LastStatus); status != "" {
		parts = append(parts, "状态:"+status)
	}
	if job.NextRun != "" {
		parts = append(parts, "下次运行:"+cronNextRunLabel(job.NextRun))
	}
	if job.RunCount > 0 {
		parts = append(parts, fmt.Sprintf("运行次数:%d", job.RunCount))
	}
	return strings.Join(parts, " · ")
}

// cronCompletionStatus projects the cron_completed session event into the
// native status banner so a run finishing outside this panel is observable.
func cronCompletionStatus(ev sessionEvent) (string, bool) {
	name := strings.TrimSpace(ev.JobID)
	if name == "" {
		name = "定时任务"
	}
	if strings.EqualFold(strings.TrimSpace(ev.Status), "failed") {
		return fmt.Sprintf("定时任务 %s 运行失败", name), true
	}
	return fmt.Sprintf("定时任务 %s 运行完成", name), false
}

func cronNormalizedMode(value string) string {
	mode := strings.TrimSpace(value)
	if mode == "" {
		return cronModeYolo
	}
	return mode
}

// validateCronDraft mirrors the management create/update validation that can
// be decided client-side. Schedule grammar stays server-authoritative: the ACP
// handler runs the shared cron normalization and returns cron_schedule_invalid.
func validateCronDraft(draft *cronJobDraft) error {
	if draft == nil {
		return fmt.Errorf("定时任务草稿不存在")
	}
	if strings.TrimSpace(draft.Name) == "" {
		return fmt.Errorf("任务名称不能为空")
	}
	if strings.TrimSpace(draft.Schedule) == "" {
		return fmt.Errorf("Cron 表达式不能为空；单次任务请填写 @once")
	}
	if strings.TrimSpace(draft.Prompt) == "" {
		return fmt.Errorf("任务提示词不能为空")
	}
	mode := cronNormalizedMode(draft.Mode)
	if mode != "agent" && mode != "yolo" {
		return fmt.Errorf("任务模式必须是 agent 或 yolo")
	}
	return nil
}

func cronDraftPayload(draft *cronJobDraft) map[string]any {
	return map[string]any{
		"name":     strings.TrimSpace(draft.Name),
		"schedule": strings.TrimSpace(draft.Schedule),
		"prompt":   strings.TrimSpace(draft.Prompt),
		"mode":     cronNormalizedMode(draft.Mode),
		"enabled":  draft.Enabled,
	}
}

func buildCronCreateRequest(draft *cronJobDraft) (map[string]any, error) {
	if err := validateCronDraft(draft); err != nil {
		return nil, err
	}
	return cronDraftPayload(draft), nil
}

func buildCronUpdateRequest(draft *cronJobDraft) (map[string]any, error) {
	if draft == nil {
		return nil, fmt.Errorf("定时任务草稿不存在")
	}
	id := strings.TrimSpace(draft.ID)
	if id == "" {
		return nil, fmt.Errorf("要更新的定时任务 ID 不能为空")
	}
	if err := validateCronDraft(draft); err != nil {
		return nil, err
	}
	payload := cronDraftPayload(draft)
	payload["id"] = id
	return payload, nil
}

func buildCronToggleRequest(id string, enabled bool) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("要切换的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id, "enabled": enabled}, nil
}

func buildCronRemoveRequest(id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("要删除的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id}, nil
}

func buildCronRunRequest(id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("要运行的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id}, nil
}

func (a *nativeApp) cronCapable() bool {
	return a.featureEnabled("manageCron")
}

func (a *nativeApp) cronBusy() bool {
	c := &a.cron
	return c.Saving || c.Deleting || c.TogglingID != "" || c.RunningJobID != ""
}

func (a *nativeApp) cronPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "cron" &&
		seq == atomic.LoadInt64(&a.cron.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateCronPanel drops transient state and makes every in-flight cron
// response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateCronPanel() {
	c := &a.cron
	atomic.AddInt64(&c.Seq, 1)
	c.Enabled = false
	c.Running = false
	c.Jobs = nil
	c.Loaded = false
	c.Loading = false
	c.Err = ""
	c.Saving = false
	c.TogglingID = ""
	c.RunningJobID = ""
	c.Deleting = false
	c.Status = ""
	c.StatusIsError = false
	c.RefreshPending = false
	c.EditorOpen = false
	c.EditingID = ""
	c.Draft = cronEmptyDraft()
	c.DialogErr = ""
	c.PendingDelete = false
	c.PendingDeleteID = ""
}

// startCronPanelLoad starts one capability-gated cron list load. It is called
// from the UI thread on tab entry.
func (a *nativeApp) startCronPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "cron" {
		return
	}
	c := &a.cron
	if c.Loading {
		return
	}
	if !a.cronCapable() {
		c.Loaded = true
		c.Err = "当前运行时不支持定时任务管理（manageCron）。"
		return
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	c.Loading = true
	c.Err = ""
	c.Status = ""
	c.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadCronPanel(seq, startReloadSeq)
}

// startCronPanelRefresh reloads the list without clearing the visible rows.
// It is used by run completion events and manual refresh.
func (a *nativeApp) startCronPanelRefresh() {
	if !a.settingsOpen || a.settingsTab != "cron" || !a.cronCapable() {
		return
	}
	c := &a.cron
	if c.Loading {
		return
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	c.Loading = true
	c.RefreshPending = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadCronPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadCronPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.cronPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.cron.Loading = false
			a.cron.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/cron/list", map[string]any{})
	a.dispatch(func() {
		if !a.cronPanelCurrent(seq, startReloadSeq) {
			return
		}
		c := &a.cron
		c.Loading = false
		if err != nil {
			c.Err = "读取定时任务失败：" + err.Error()
			a.flushCronRefreshPending()
			return
		}
		var view cronListView
		if err := json.Unmarshal(raw, &view); err != nil {
			c.Err = "定时任务列表响应无效：" + err.Error()
			a.flushCronRefreshPending()
			return
		}
		a.applyCronListView(&view)
		a.flushCronRefreshPending()
	})
}

func (a *nativeApp) applyCronListView(view *cronListView) {
	if view == nil {
		return
	}
	c := &a.cron
	jobs := view.Jobs
	if jobs == nil {
		jobs = []cronJobView{}
	}
	c.Enabled = view.Enabled
	c.Running = view.Running
	c.Jobs = jobs
	c.Loaded = true
	c.Loading = false
	c.Err = ""
}

// applyCronSavedJob merges one create/update response into the visible list.
// New jobs are prepended because the server orders jobs by creation time
// descending; existing rows keep their position when edited.
func (a *nativeApp) applyCronSavedJob(job *cronJobView) {
	if job == nil {
		return
	}
	id := strings.TrimSpace(job.ID)
	if id == "" {
		return
	}
	c := &a.cron
	for index := range c.Jobs {
		if strings.TrimSpace(c.Jobs[index].ID) == id {
			c.Jobs[index] = *job
			return
		}
	}
	c.Jobs = append([]cronJobView{*job}, c.Jobs...)
}

func (a *nativeApp) removeCronJobByID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	c := &a.cron
	jobs := c.Jobs[:0]
	for _, job := range c.Jobs {
		if strings.TrimSpace(job.ID) == id {
			continue
		}
		jobs = append(jobs, job)
	}
	c.Jobs = jobs
}

func (a *nativeApp) cronJobByID(id string) (cronJobView, bool) {
	id = strings.TrimSpace(id)
	for _, job := range a.cron.Jobs {
		if strings.TrimSpace(job.ID) == id {
			return job, true
		}
	}
	return cronJobView{}, false
}

func (a *nativeApp) openCronNewDraft() {
	c := &a.cron
	c.EditorOpen = true
	c.EditingID = ""
	c.Draft = cronEmptyDraft()
	c.DialogErr = ""
}

func (a *nativeApp) openCronEditDraft(index int) {
	c := &a.cron
	if index < 0 || index >= len(c.Jobs) {
		c.Status = "要编辑的定时任务不存在。"
		c.StatusIsError = true
		return
	}
	c.EditorOpen = true
	c.EditingID = strings.TrimSpace(c.Jobs[index].ID)
	c.Draft = cronDraftFromJob(c.Jobs[index])
	c.DialogErr = ""
}

func (a *nativeApp) closeCronEditor() {
	c := &a.cron
	c.EditorOpen = false
	c.EditingID = ""
	c.Draft = cronEmptyDraft()
	c.DialogErr = ""
}

func (a *nativeApp) saveCronDraft() {
	c := &a.cron
	if !a.cronCapable() {
		c.DialogErr = "当前运行时不支持定时任务管理（manageCron）。"
		return
	}
	if c.Saving {
		return
	}
	editing := strings.TrimSpace(c.EditingID) != ""
	var (
		request map[string]any
		err     error
		method  = "mothx/manage/cron/create"
	)
	if editing {
		method = "mothx/manage/cron/update"
		request, err = buildCronUpdateRequest(&c.Draft)
	} else {
		request, err = buildCronCreateRequest(&c.Draft)
	}
	if err != nil {
		c.DialogErr = err.Error()
		return
	}
	client := a.acp
	if client == nil {
		c.DialogErr = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.Saving = true
	c.DialogErr = ""
	c.Status = "正在保存定时任务…"
	c.StatusIsError = false
	go func() {
		raw, err := client.call(method, request)
		a.dispatch(func() {
			if !a.cronPanelCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.cron
			c.Saving = false
			if err != nil {
				message := "保存定时任务失败：" + err.Error()
				c.Status = message
				c.StatusIsError = true
				c.DialogErr = message
				a.flushCronRefreshPending()
				return
			}
			var result cronJobResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Job == nil || strings.TrimSpace(result.Job.ID) == "" {
				message := "保存后定时任务响应无效。"
				c.Status = message
				c.StatusIsError = true
				c.DialogErr = message
				a.flushCronRefreshPending()
				return
			}
			a.applyCronSavedJob(result.Job)
			name := cronJobDisplayName(*result.Job)
			c.EditorOpen = false
			c.EditingID = ""
			c.Draft = cronEmptyDraft()
			c.DialogErr = ""
			if editing {
				c.Status = fmt.Sprintf("定时任务 %q 已更新", name)
			} else {
				c.Status = fmt.Sprintf("定时任务 %q 已创建", name)
			}
			c.StatusIsError = false
			a.flushCronRefreshPending()
		})
	}()
}

func (a *nativeApp) toggleCronJob(index int) {
	c := &a.cron
	if !a.cronCapable() {
		c.Status = "当前运行时不支持定时任务管理（manageCron）。"
		c.StatusIsError = true
		return
	}
	if a.cronBusy() || c.Loading {
		return
	}
	if index < 0 || index >= len(c.Jobs) {
		c.Status = "要切换的定时任务不存在。"
		c.StatusIsError = true
		return
	}
	job := c.Jobs[index]
	id := strings.TrimSpace(job.ID)
	request, err := buildCronToggleRequest(id, !job.Enabled)
	if err != nil {
		c.Status = err.Error()
		c.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		c.Status = "切换定时任务失败：尚未连接到 MothX ACP。"
		c.StatusIsError = true
		return
	}
	name := cronJobDisplayName(job)
	action := "暂停"
	if !job.Enabled {
		action = "恢复"
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.TogglingID = id
	c.Status = fmt.Sprintf("正在%s定时任务 %q…", action, name)
	c.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/cron/update", request)
		a.dispatch(func() {
			if !a.cronPanelCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.cron
			c.TogglingID = ""
			if err != nil {
				c.Status = "切换定时任务失败：" + err.Error()
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			var result cronJobResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Job == nil || strings.TrimSpace(result.Job.ID) == "" {
				c.Status = "切换后定时任务响应无效。"
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			a.applyCronSavedJob(result.Job)
			if result.Job.Enabled {
				c.Status = fmt.Sprintf("定时任务 %q 已恢复", name)
			} else {
				c.Status = fmt.Sprintf("定时任务 %q 已暂停", name)
			}
			c.StatusIsError = false
			a.flushCronRefreshPending()
		})
	}()
}

func (a *nativeApp) runCronJob(index int) {
	c := &a.cron
	if !a.cronCapable() {
		c.Status = "当前运行时不支持定时任务管理（manageCron）。"
		c.StatusIsError = true
		return
	}
	if a.cronBusy() || c.Loading {
		return
	}
	if index < 0 || index >= len(c.Jobs) {
		c.Status = "要运行的定时任务不存在。"
		c.StatusIsError = true
		return
	}
	job := c.Jobs[index]
	id := strings.TrimSpace(job.ID)
	request, err := buildCronRunRequest(id)
	if err != nil {
		c.Status = err.Error()
		c.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		c.Status = "运行定时任务失败：尚未连接到 MothX ACP。"
		c.StatusIsError = true
		return
	}
	name := cronJobDisplayName(job)
	seq := atomic.AddInt64(&c.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.RunningJobID = id
	c.Status = fmt.Sprintf("正在触发定时任务 %q…", name)
	c.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/cron/run", request)
		a.dispatch(func() {
			if !a.cronPanelCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.cron
			c.RunningJobID = ""
			if err != nil {
				c.Status = "运行定时任务失败：" + err.Error()
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			var result cronRunResult
			if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
				c.Status = "触发定时任务后响应无效。"
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			c.Status = fmt.Sprintf("已触发定时任务 %q；完成后列表会自动刷新", name)
			c.StatusIsError = false
			a.flushCronRefreshPending()
		})
	}()
}

func (a *nativeApp) requestDeleteCron(index int) {
	c := &a.cron
	if index < 0 || index >= len(c.Jobs) {
		c.Status = "要删除的定时任务不存在。"
		c.StatusIsError = true
		return
	}
	c.PendingDelete = true
	c.PendingDeleteID = strings.TrimSpace(c.Jobs[index].ID)
}

func (a *nativeApp) cancelDeleteCron() {
	a.cron.PendingDelete = false
	a.cron.PendingDeleteID = ""
}

func (a *nativeApp) confirmDeleteCron() {
	c := &a.cron
	id := strings.TrimSpace(c.PendingDeleteID)
	if id == "" {
		c.PendingDelete = false
		c.Status = "要删除的定时任务不存在。"
		c.StatusIsError = true
		return
	}
	job, ok := a.cronJobByID(id)
	name := id
	if ok {
		name = cronJobDisplayName(job)
	}
	if !a.cronCapable() {
		c.PendingDelete = false
		c.Status = "当前运行时不支持定时任务管理（manageCron）。"
		c.StatusIsError = true
		return
	}
	request, err := buildCronRemoveRequest(id)
	if err != nil {
		c.PendingDelete = false
		c.Status = err.Error()
		c.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		c.PendingDelete = false
		c.Status = "删除定时任务失败：尚未连接到 MothX ACP。"
		c.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.Deleting = true
	c.Status = "正在删除定时任务…"
	c.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/cron/remove", request)
		a.dispatch(func() {
			if !a.cronPanelCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.cron
			c.Deleting = false
			c.PendingDelete = false
			c.PendingDeleteID = ""
			if err != nil {
				c.Status = "删除定时任务失败：" + err.Error()
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			var result cronRemoveResult
			if err := json.Unmarshal(raw, &result); err != nil || !result.Deleted {
				c.Status = "删除后定时任务响应无效。"
				c.StatusIsError = true
				a.flushCronRefreshPending()
				return
			}
			a.removeCronJobByID(id)
			c.Status = fmt.Sprintf("定时任务 %q 已删除", name)
			c.StatusIsError = false
			a.flushCronRefreshPending()
		})
	}()
}

// handleCronCompletedEvent projects the Runtime cron_completed event into the
// panel. It refreshes run metadata without letting the event race an in-flight
// mutation; a pending refresh is flushed after that mutation completes.
func (a *nativeApp) handleCronCompletedEvent(ev sessionEvent) {
	if !a.settingsOpen || a.settingsTab != "cron" || !a.cronCapable() {
		return
	}
	message, isError := cronCompletionStatus(ev)
	c := &a.cron
	c.Status = message
	c.StatusIsError = isError
	if c.Loading || a.cronBusy() {
		c.RefreshPending = true
		return
	}
	a.startCronPanelRefresh()
}

func (a *nativeApp) flushCronRefreshPending() {
	c := &a.cron
	if !c.RefreshPending {
		return
	}
	if c.Loading || a.cronBusy() {
		return
	}
	c.RefreshPending = false
	a.startCronPanelRefresh()
}
