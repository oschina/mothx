// Package cron owns the Native projection of the ACP Cron management contract
// (mothx/manage/cron/list, create, update, remove, and run).
package cron

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/kit"
)

// This file owns the Native projection of the ACP Cron management contract
// (mothx/manage/cron/list, create, update, remove, and run). Cron jobs are
// Runtime-owned records in the shared SQLite store; this panel only projects
// them and writes through ACP. It never reads sessions.db or settings.json and
// never starts a scheduler of its own.

const ModeYolo = "yolo"

var Modes = []string{"agent", "yolo"}

// JobView mirrors the job shape projected by mothx/manage/cron/*. The A2A
// token is never projected by the server, so it is intentionally absent here.
type JobView struct {
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

// ListView mirrors the cron/list response envelope.
type ListView struct {
	Enabled bool      `json:"enabled"`
	Running bool      `json:"running"`
	Jobs    []JobView `json:"jobs"`
}

// JobResult is the create/update response envelope.
type JobResult struct {
	Job *JobView `json:"job"`
}

// RemoveResult is the remove response envelope.
type RemoveResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// RunResult is the run response envelope returned after a manual trigger.
type RunResult struct {
	OK        bool   `json:"ok"`
	JobID     string `json:"jobId"`
	Triggered bool   `json:"triggered"`
}

// JobDraft is the editable state of the create/edit dialog.
type JobDraft struct {
	ID       string
	Name     string
	Schedule string
	Prompt   string
	Mode     string
	Enabled  bool
}

type State struct {
	Enabled bool
	Running bool
	Jobs    []JobView
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
	Draft      JobDraft
	DialogErr  string

	PendingDelete   bool
	PendingDeleteID string

	// Seq guards every asynchronous cron call. Bumping it invalidates any
	// in-flight list/save/toggle/delete/run response, which is how tab exits,
	// settings close, reconnects, and editor changes prevent stale state.
	Seq int64
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{
		Jobs:  []JobView{},
		Draft: EmptyDraft(),
	}}
}

// Host adds the active page and panel visibility to the shared host surface.
type Host interface {
	host.Base
	PanelVisible(page, tab string) bool
	Page() string
}

func EmptyDraft() JobDraft {
	return JobDraft{Mode: ModeYolo, Enabled: true}
}

func DraftFromJob(job JobView) JobDraft {
	mode := strings.TrimSpace(job.Mode)
	if mode == "" {
		mode = ModeYolo
	}
	return JobDraft{
		ID:       strings.TrimSpace(job.ID),
		Name:     job.Name,
		Schedule: job.Schedule,
		Prompt:   job.Prompt,
		Mode:     mode,
		Enabled:  job.Enabled,
	}
}

func JobDisplayName(job JobView) string {
	if name := strings.TrimSpace(job.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(job.ID); id != "" {
		return id
	}
	return i18n.T("未命名任务")
}

// JobScheduleLabel renders an empty or @once schedule as a one-shot job,
// matching the shared cron normalization contract.
func JobScheduleLabel(job JobView) string {
	schedule := strings.TrimSpace(job.Schedule)
	if job.OneShot || schedule == "" || strings.EqualFold(schedule, "@once") {
		return i18n.T("单次")
	}
	return schedule
}

func RunStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "completed":
		return i18n.T("成功")
	case "failed", "failure", "error":
		return i18n.T("失败")
	case "running":
		return i18n.T("运行中")
	case "":
		return ""
	default:
		return strings.TrimSpace(status)
	}
}

func ParseTimestamp(value string) (time.Time, bool) {
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

func LastRunLabel(value string) string {
	value = strings.TrimSpace(value)
	parsed, ok := ParseTimestamp(value)
	if !ok {
		return value
	}
	label := parsed.Local().Format("2006-01-02 15:04")
	if relative := kit.RelativeTime(value); relative != "" {
		return relative + " · " + label
	}
	return label
}

func NextRunLabel(value string) string {
	value = strings.TrimSpace(value)
	parsed, ok := ParseTimestamp(value)
	if !ok {
		return value
	}
	return parsed.Local().Format("2006-01-02 15:04")
}

// JobMeta projects the run metadata shown in each job row. Timestamps are
// display-only; the Runtime list response remains authoritative.
func JobMeta(job JobView) string {
	parts := []string{i18n.Tf("计划:%s", JobScheduleLabel(job))}
	if mode := strings.TrimSpace(job.Mode); mode != "" {
		parts = append(parts, i18n.Tf("模式:%s", mode))
	}
	if workDir := strings.TrimSpace(job.WorkDir); workDir != "" {
		parts = append(parts, i18n.Tf("工作区:%s", workDir))
	}
	if job.LastRun != "" {
		last := i18n.Tf("上次运行:%s", LastRunLabel(job.LastRun))
		if status := RunStatusLabel(job.LastStatus); status != "" {
			last += " · " + status
		}
		parts = append(parts, last)
	} else if status := RunStatusLabel(job.LastStatus); status != "" {
		parts = append(parts, i18n.Tf("状态:%s", status))
	}
	if job.NextRun != "" {
		parts = append(parts, i18n.Tf("下次运行:%s", NextRunLabel(job.NextRun)))
	}
	if job.RunCount > 0 {
		parts = append(parts, i18n.Tf("运行次数:%d", job.RunCount))
	}
	return strings.Join(parts, " · ")
}

// CompletionStatus projects the cron_completed session event into the
// native status banner so a run finishing outside this panel is observable.
func CompletionStatus(ev acp.SessionEvent) (string, bool) {
	name := strings.TrimSpace(ev.JobID)
	if name == "" {
		name = "定时任务"
	}
	if strings.EqualFold(strings.TrimSpace(ev.Status), "failed") {
		return i18n.Tf("定时任务 %s 运行失败", name), true
	}
	return i18n.Tf("定时任务 %s 运行完成", name), false
}

func NormalizedMode(value string) string {
	mode := strings.TrimSpace(value)
	if mode == "" {
		return ModeYolo
	}
	return mode
}

// ValidateDraft mirrors the management create/update validation that can
// be decided client-side. Schedule grammar stays server-authoritative: the ACP
// handler runs the shared cron normalization and returns cron_schedule_invalid.
func ValidateDraft(draft *JobDraft) error {
	if draft == nil {
		return i18n.LocalizedError("定时任务草稿不存在")
	}
	if strings.TrimSpace(draft.Name) == "" {
		return i18n.LocalizedError("任务名称不能为空")
	}
	if strings.TrimSpace(draft.Schedule) == "" {
		return i18n.LocalizedError("Cron 表达式不能为空；单次任务请填写 @once")
	}
	if strings.TrimSpace(draft.Prompt) == "" {
		return i18n.LocalizedError("任务提示词不能为空")
	}
	mode := NormalizedMode(draft.Mode)
	if mode != "agent" && mode != "yolo" {
		return i18n.LocalizedError("任务模式必须是 agent 或 yolo")
	}
	return nil
}

func DraftPayload(draft *JobDraft) map[string]any {
	return map[string]any{
		"name":     strings.TrimSpace(draft.Name),
		"schedule": strings.TrimSpace(draft.Schedule),
		"prompt":   strings.TrimSpace(draft.Prompt),
		"mode":     NormalizedMode(draft.Mode),
		"enabled":  draft.Enabled,
	}
}

func BuildCreateRequest(draft *JobDraft) (map[string]any, error) {
	if err := ValidateDraft(draft); err != nil {
		return nil, err
	}
	return DraftPayload(draft), nil
}

func BuildUpdateRequest(draft *JobDraft) (map[string]any, error) {
	if draft == nil {
		return nil, i18n.LocalizedError("定时任务草稿不存在")
	}
	id := strings.TrimSpace(draft.ID)
	if id == "" {
		return nil, i18n.LocalizedError("要更新的定时任务 ID 不能为空")
	}
	if err := ValidateDraft(draft); err != nil {
		return nil, err
	}
	payload := DraftPayload(draft)
	payload["id"] = id
	return payload, nil
}

func BuildToggleRequest(id string, enabled bool) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, i18n.LocalizedError("要切换的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id, "enabled": enabled}, nil
}

func BuildRemoveRequest(id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, i18n.LocalizedError("要删除的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id}, nil
}

func BuildRunRequest(id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, i18n.LocalizedError("要运行的定时任务 ID 不能为空")
	}
	return map[string]any{"id": id}, nil
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageCron")
}

func (p *Panel) Busy() bool {
	return p.Saving || p.Deleting || p.TogglingID != "" || p.RunningJobID != ""
}

func (p *Panel) Current(h Host, seq, reloadSeq int64) bool {
	return h.PanelVisible("automation", "cron") &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight cron
// response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Enabled = false
	p.Running = false
	p.Jobs = nil
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Saving = false
	p.TogglingID = ""
	p.RunningJobID = ""
	p.Deleting = false
	p.Status = ""
	p.StatusIsError = false
	p.RefreshPending = false
	p.EditorOpen = false
	p.EditingID = ""
	p.Draft = EmptyDraft()
	p.DialogErr = ""
	p.PendingDelete = false
	p.PendingDeleteID = ""
}

// releaseBusy drops the busy marks of the actions a new load made stale.
// Their answers are about to be dropped, and a mark left behind would keep
// every action disabled until the panel was reopened.
func (p *Panel) releaseBusy() {
	p.Saving = false
	p.Deleting = false
	p.TogglingID = ""
	p.RunningJobID = ""
}

// StartLoad starts one capability-gated cron list load. It is called
// from the UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.PanelVisible("automation", "cron") {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持定时任务管理（manageCron）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.releaseBusy()
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq)
}

// StartRefresh reloads the list without clearing the visible rows.
// It is used by run completion events and manual refresh.
func (p *Panel) StartRefresh(h Host) {
	if !h.PanelVisible("automation", "cron") || !p.Capable(h) {
		return
	}
	if p.Loading {
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.releaseBusy()
	p.Loading = true
	p.RefreshPending = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq)
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
	raw, err := client.Call("mothx/manage/cron/list", map[string]any{})
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取定时任务失败：" + err.Error()
			p.FlushRefreshPending(h)
			return
		}
		var view ListView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "定时任务列表响应无效：" + err.Error()
			p.FlushRefreshPending(h)
			return
		}
		p.ApplyListView(&view)
		p.FlushRefreshPending(h)
	})
}

func (p *Panel) ApplyListView(view *ListView) {
	if view == nil {
		return
	}
	jobs := view.Jobs
	if jobs == nil {
		jobs = []JobView{}
	}
	p.Enabled = view.Enabled
	p.Running = view.Running
	p.Jobs = jobs
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// ApplySavedJob merges one create/update response into the visible list.
// New jobs are prepended because the server orders jobs by creation time
// descending; existing rows keep their position when edited.
func (p *Panel) ApplySavedJob(job *JobView) {
	if job == nil {
		return
	}
	id := strings.TrimSpace(job.ID)
	if id == "" {
		return
	}
	for index := range p.Jobs {
		if strings.TrimSpace(p.Jobs[index].ID) == id {
			p.Jobs[index] = *job
			return
		}
	}
	p.Jobs = append([]JobView{*job}, p.Jobs...)
}

func (p *Panel) RemoveJobByID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	jobs := p.Jobs[:0]
	for _, job := range p.Jobs {
		if strings.TrimSpace(job.ID) == id {
			continue
		}
		jobs = append(jobs, job)
	}
	p.Jobs = jobs
}

func (p *Panel) JobByID(id string) (JobView, bool) {
	id = strings.TrimSpace(id)
	for _, job := range p.Jobs {
		if strings.TrimSpace(job.ID) == id {
			return job, true
		}
	}
	return JobView{}, false
}

func (p *Panel) OpenNewDraft() {
	p.EditorOpen = true
	p.EditingID = ""
	p.Draft = EmptyDraft()
	p.DialogErr = ""
}

func (p *Panel) OpenEditDraft(index int) {
	if index < 0 || index >= len(p.Jobs) {
		p.Status = "要编辑的定时任务不存在。"
		p.StatusIsError = true
		return
	}
	p.EditorOpen = true
	p.EditingID = strings.TrimSpace(p.Jobs[index].ID)
	p.Draft = DraftFromJob(p.Jobs[index])
	p.DialogErr = ""
}

func (p *Panel) CloseEditor() {
	p.EditorOpen = false
	p.EditingID = ""
	p.Draft = EmptyDraft()
	p.DialogErr = ""
}

func (p *Panel) Save(h Host) {
	if !p.Capable(h) {
		p.DialogErr = "当前运行时不支持定时任务管理（manageCron）。"
		return
	}
	if p.Saving {
		return
	}
	editing := strings.TrimSpace(p.EditingID) != ""
	var (
		request map[string]any
		err     error
		method  = "mothx/manage/cron/create"
	)
	if editing {
		method = "mothx/manage/cron/update"
		request, err = BuildUpdateRequest(&p.Draft)
	} else {
		request, err = BuildCreateRequest(&p.Draft)
	}
	if err != nil {
		p.DialogErr = err.Error()
		return
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
	p.Status = "正在保存定时任务…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call(method, request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				message := "保存定时任务失败：" + err.Error()
				p.Status = message
				p.StatusIsError = true
				p.DialogErr = message
				p.FlushRefreshPending(h)
				return
			}
			var result JobResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Job == nil || strings.TrimSpace(result.Job.ID) == "" {
				message := "保存后定时任务响应无效。"
				p.Status = message
				p.StatusIsError = true
				p.DialogErr = message
				p.FlushRefreshPending(h)
				return
			}
			p.ApplySavedJob(result.Job)
			name := JobDisplayName(*result.Job)
			p.EditorOpen = false
			p.EditingID = ""
			p.Draft = EmptyDraft()
			p.DialogErr = ""
			if editing {
				p.Status = i18n.Tf("定时任务 %q 已更新", name)
			} else {
				p.Status = i18n.Tf("定时任务 %q 已创建", name)
			}
			p.StatusIsError = false
			p.FlushRefreshPending(h)
		})
	}()
}

func (p *Panel) Toggle(h Host, index int) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持定时任务管理（manageCron）。"
		p.StatusIsError = true
		return
	}
	if p.Busy() || p.Loading {
		return
	}
	if index < 0 || index >= len(p.Jobs) {
		p.Status = "要切换的定时任务不存在。"
		p.StatusIsError = true
		return
	}
	job := p.Jobs[index]
	id := strings.TrimSpace(job.ID)
	request, err := BuildToggleRequest(id, !job.Enabled)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "切换定时任务失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	name := JobDisplayName(job)
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.TogglingID = id
	p.Status = ToggleStatus(name, job.Enabled)
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/cron/update", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.TogglingID = ""
			if err != nil {
				p.Status = "切换定时任务失败：" + err.Error()
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			var result JobResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Job == nil || strings.TrimSpace(result.Job.ID) == "" {
				p.Status = "切换后定时任务响应无效。"
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			p.ApplySavedJob(result.Job)
			if result.Job.Enabled {
				p.Status = i18n.Tf("定时任务 %q 已恢复", name)
			} else {
				p.Status = i18n.Tf("定时任务 %q 已暂停", name)
			}
			p.StatusIsError = false
			p.FlushRefreshPending(h)
		})
	}()
}

func (p *Panel) Run(h Host, index int) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持定时任务管理（manageCron）。"
		p.StatusIsError = true
		return
	}
	if p.Busy() || p.Loading {
		return
	}
	if index < 0 || index >= len(p.Jobs) {
		p.Status = "要运行的定时任务不存在。"
		p.StatusIsError = true
		return
	}
	job := p.Jobs[index]
	id := strings.TrimSpace(job.ID)
	request, err := BuildRunRequest(id)
	if err != nil {
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.Status = "运行定时任务失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	name := JobDisplayName(job)
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.RunningJobID = id
	p.Status = i18n.Tf("正在触发定时任务 %q…", name)
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/cron/run", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.RunningJobID = ""
			if err != nil {
				p.Status = "运行定时任务失败：" + err.Error()
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			var result RunResult
			if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
				p.Status = "触发定时任务后响应无效。"
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			p.Status = i18n.Tf("已触发定时任务 %q；完成后列表会自动刷新", name)
			p.StatusIsError = false
			p.FlushRefreshPending(h)
		})
	}()
}

func (p *Panel) RequestDelete(index int) {
	if index < 0 || index >= len(p.Jobs) {
		p.Status = "要删除的定时任务不存在。"
		p.StatusIsError = true
		return
	}
	p.PendingDelete = true
	p.PendingDeleteID = strings.TrimSpace(p.Jobs[index].ID)
}

func (p *Panel) CancelDelete() {
	p.PendingDelete = false
	p.PendingDeleteID = ""
}

func (p *Panel) ConfirmDelete(h Host) {
	id := strings.TrimSpace(p.PendingDeleteID)
	if id == "" {
		p.PendingDelete = false
		p.Status = "要删除的定时任务不存在。"
		p.StatusIsError = true
		return
	}
	job, ok := p.JobByID(id)
	name := id
	if ok {
		name = JobDisplayName(job)
	}
	if !p.Capable(h) {
		p.PendingDelete = false
		p.Status = "当前运行时不支持定时任务管理（manageCron）。"
		p.StatusIsError = true
		return
	}
	request, err := BuildRemoveRequest(id)
	if err != nil {
		p.PendingDelete = false
		p.Status = err.Error()
		p.StatusIsError = true
		return
	}
	client := h.ACP()
	if client == nil {
		p.PendingDelete = false
		p.Status = "删除定时任务失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Deleting = true
	p.Status = "正在删除定时任务…"
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/cron/remove", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Deleting = false
			p.PendingDelete = false
			p.PendingDeleteID = ""
			if err != nil {
				p.Status = "删除定时任务失败：" + err.Error()
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			var result RemoveResult
			if err := json.Unmarshal(raw, &result); err != nil || !result.Deleted {
				p.Status = "删除后定时任务响应无效。"
				p.StatusIsError = true
				p.FlushRefreshPending(h)
				return
			}
			p.RemoveJobByID(id)
			p.Status = i18n.Tf("定时任务 %q 已删除", name)
			p.StatusIsError = false
			p.FlushRefreshPending(h)
		})
	}()
}

// handleCronCompletedEvent projects the Runtime cron_completed event into the
// panel. It refreshes run metadata without letting the event race an in-flight
// mutation; a pending refresh is flushed after that mutation completes.
func (p *Panel) HandleCompletedEvent(h Host, ev acp.SessionEvent) {
	if !h.PanelVisible("automation", "cron") || !p.Capable(h) {
		return
	}
	message, isError := CompletionStatus(ev)
	p.Status = message
	p.StatusIsError = isError
	if p.Loading || p.Busy() {
		p.RefreshPending = true
		return
	}
	p.StartRefresh(h)
}

func (p *Panel) FlushRefreshPending(h Host) {
	if !p.RefreshPending {
		return
	}
	if p.Loading || p.Busy() {
		return
	}
	p.RefreshPending = false
	p.StartRefresh(h)
}

// ToggleStatus is the shared in-flight label for a cron pause/resume.
func ToggleStatus(name string, enabled bool) string {
	if enabled {
		return i18n.Tf("正在暂停定时任务 %q…", name)
	}
	return i18n.Tf("正在恢复定时任务 %q…", name)
}
