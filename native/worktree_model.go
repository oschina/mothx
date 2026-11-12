package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the additive worktree contract
// (mothx/worktree/list|create|remove|reset and the mothx/worktree/status
// notification). Git mechanics, registry identity, workspace authorization,
// and readiness events stay in the shared Runtime; this client only renders
// them and requests explicit lifecycle actions.

type worktreeView struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Branch         string `json:"branch,omitempty"`
	Directory      string `json:"directory"`
	RepositoryRoot string `json:"repositoryRoot,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	StartCommand   string `json:"startCommand,omitempty"`
	Status         string `json:"status,omitempty"`
	Error          string `json:"error,omitempty"`
	External       bool   `json:"external,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
}

type worktreeListResult struct {
	Worktrees []worktreeView `json:"worktrees"`
}

type worktreeCreateResult struct {
	Worktree *worktreeView `json:"worktree"`
}

type worktreeStatusPayload struct {
	Worktree struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Branch    string `json:"branch"`
		Directory string `json:"directory"`
		Status    string `json:"status"`
		Error     string `json:"error,omitempty"`
	} `json:"worktree"`
}

type worktreePanelState struct {
	Open    bool
	BaseCwd string

	Worktrees []worktreeView
	Loaded    bool
	Loading   bool

	DraftName     string
	OpenInNewChat bool
	Creating      bool
	BusyDirectory string
	Err           string
	Status        string

	// PendingOpenDirectory/PendingOpenID remember a create request that asked
	// to open the worktree in a new session. Creation is asynchronous, so the
	// intent waits for the canonical ready event instead of being dropped when
	// create returns pending.
	PendingOpenDirectory string
	PendingOpenID        string
	// PendingOpenReady sticks once any canonical observation reports the
	// intent's worktree as ready. A lagging worktree/list snapshot may still
	// describe it as pending, and correctness must not depend on which
	// projection answered last.
	PendingOpenReady bool

	ConfirmOpen bool
	ConfirmKind string
	ConfirmTree worktreeView
	ConfirmBusy bool
	ConfirmErr  string

	Seq int64
}

func newWorktreePanelState() worktreePanelState {
	return worktreePanelState{OpenInNewChat: true}
}

func (a *nativeApp) worktreeCapable() bool {
	return a.featureEnabled("worktrees")
}

func (a *nativeApp) worktreeResetCapable() bool {
	return a.featureEnabled("worktrees") && a.featureEnabled("worktreeReset")
}

func (a *nativeApp) worktreeBaseDir() string {
	if a.currentSession != "" {
		if session, ok := a.historySessionByID(a.currentSession); ok && strings.TrimSpace(session.Cwd) != "" {
			return session.Cwd
		}
	}
	if strings.TrimSpace(a.workspace) != "" {
		return a.workspace
	}
	return a.cwd
}

func (a *nativeApp) openWorktreePanel() {
	if !a.worktreeCapable() {
		return
	}
	p := &a.worktree
	// Reopening must not discard the only ready evidence for a deferred open
	// intent: a lagging list snapshot after the reset would otherwise strand
	// the request. Keep the known-ready row, or synthesize it from the
	// canonical history hint.
	preservedReady := a.pendingWorktreeReadyRow()
	p.Open = true
	p.BaseCwd = a.worktreeBaseDir()
	p.Worktrees = nil
	p.Loaded = false
	p.Loading = false
	p.DraftName = ""
	p.OpenInNewChat = true
	p.Creating = false
	p.BusyDirectory = ""
	p.Err = ""
	p.Status = ""
	p.ConfirmOpen = false
	p.ConfirmKind = ""
	p.ConfirmTree = worktreeView{}
	p.ConfirmBusy = false
	p.ConfirmErr = ""
	if preservedReady != nil {
		p.Worktrees = []worktreeView{*preservedReady}
	}
	a.loadWorktrees()
}

// pendingWorktreeReadyRow returns the ready row currently known for a queued
// open intent, so reopening the panel can carry that evidence forward. It is a
// projection only; the Runtime still validates the directory.
func (a *nativeApp) pendingWorktreeReadyRow() *worktreeView {
	p := &a.worktree
	if p.PendingOpenDirectory == "" {
		return nil
	}
	row := worktreeView{ID: p.PendingOpenID, Directory: p.PendingOpenDirectory, Status: "ready"}
	if !a.worktreeRowKnownReady(row) {
		return nil
	}
	for _, known := range p.Worktrees {
		if worktreeSameRow(known, row) {
			row = known
			row.Status = "ready"
			row.Error = ""
			break
		}
	}
	return &row
}

func (a *nativeApp) closeWorktreePanel() {
	p := &a.worktree
	atomic.AddInt64(&p.Seq, 1)
	p.Open = false
	p.ConfirmOpen = false
	p.ConfirmKind = ""
	p.ConfirmTree = worktreeView{}
	p.ConfirmBusy = false
	p.ConfirmErr = ""
}

func (a *nativeApp) worktreePanelCurrent(seq int64, reloadSeq int64) bool {
	return a.worktree.Open && seq == atomic.LoadInt64(&a.worktree.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) loadWorktrees() {
	p := &a.worktree
	if !a.worktreeCapable() || p.Loading {
		return
	}
	client := a.acp
	if client == nil {
		p.Err = "尚未连接到 MothX ACP。"
		p.Loaded = true
		return
	}
	base := strings.TrimSpace(p.BaseCwd)
	if base == "" {
		base = a.worktreeBaseDir()
		p.BaseCwd = base
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Loading = true
	p.Err = ""
	go func() {
		raw, err := client.call("mothx/worktree/list", map[string]any{"repositoryRoot": base})
		a.dispatch(func() {
			if !a.worktreePanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.worktree
			p.Loading = false
			if err != nil {
				p.Err = "读取工作树失败：" + err.Error()
				return
			}
			var result worktreeListResult
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Err = "工作树响应无效：" + err.Error()
				return
			}
			a.applyWorktreeList(result.Worktrees)
		})
	}()
}

// applyWorktreeList folds one canonical list snapshot into the panel and then
// settles any deferred open intent, because a refresh can observe readiness
// that arrived without (or before) a status notification.
func (a *nativeApp) applyWorktreeList(trees []worktreeView) {
	p := &a.worktree
	p.Worktrees = a.mergeWorktreeList(trees)
	p.Loaded = true
	p.Err = ""
	a.notePendingWorktreeReadiness()
	a.settleWorktreeOpen()
}

// notePendingWorktreeReadiness sticks a ready observation from the current
// projection onto the open intent. It runs before the busy gate so a list
// snapshot that reports readiness while the shell is still busy cannot be lost
// (the panel row is transient; the intent is not).
func (a *nativeApp) notePendingWorktreeReadiness() {
	p := &a.worktree
	if p.PendingOpenDirectory == "" || p.PendingOpenReady {
		return
	}
	if a.worktreeRowKnownReady(worktreeView{ID: p.PendingOpenID, Directory: p.PendingOpenDirectory}) {
		p.PendingOpenReady = true
	}
}

// mergeWorktreeList keeps a list snapshot from downgrading a worktree whose
// readiness was already observed. A stale pending snapshot must never revoke a
// consumed ready event, so a known-ready row is re-marked ready.
func (a *nativeApp) mergeWorktreeList(trees []worktreeView) []worktreeView {
	merged := make([]worktreeView, len(trees))
	copy(merged, trees)
	for i := range merged {
		if merged[i].External || (merged[i].Status != "" && merged[i].Status != "pending") {
			continue
		}
		if a.worktreeRowKnownReady(merged[i]) {
			merged[i].Status = "ready"
			merged[i].Error = ""
		}
	}
	return merged
}

// worktreeRowKnownReady reports whether any current projection already saw this
// worktree as ready. The hints are advisory display state only; the Runtime
// still validates the directory when a session is created.
func (a *nativeApp) worktreeRowKnownReady(tree worktreeView) bool {
	for _, known := range a.worktree.Worktrees {
		if known.External {
			continue
		}
		if worktreeSameRow(tree, known) && known.Status == "ready" {
			return true
		}
	}
	for directory, status := range a.historyPanel.Worktrees {
		if status == "ready" && worktreeSameDirectory(directory, tree.Directory) {
			return true
		}
	}
	return false
}

func worktreeSameRow(left, right worktreeView) bool {
	if worktreeSameDirectory(left.Directory, right.Directory) {
		return true
	}
	lid, rid := strings.TrimSpace(left.ID), strings.TrimSpace(right.ID)
	return lid != "" && lid == rid
}

// buildWorktreeCreateRequest keeps the base directory in the negotiated
// workspace and forwards only an optional human name plus the UI preference.
func buildWorktreeCreateRequest(baseCwd, name string) map[string]any {
	request := map[string]any{"baseCwd": strings.TrimSpace(baseCwd)}
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		request["name"] = trimmed
	}
	return request
}

func (a *nativeApp) createWorktree() {
	p := &a.worktree
	if !a.worktreeCapable() || p.Creating || p.Loading {
		return
	}
	client := a.acp
	if client == nil {
		p.Err = "尚未连接到 MothX ACP。"
		return
	}
	base := strings.TrimSpace(p.BaseCwd)
	if base == "" {
		base = a.worktreeBaseDir()
	}
	if base == "" {
		p.Err = "没有可用的仓库目录。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	name := p.DraftName
	openInNewChat := p.OpenInNewChat
	p.Creating = true
	p.Err = ""
	p.Status = "正在创建隔离工作树…"
	go func() {
		raw, err := client.call("mothx/worktree/create", buildWorktreeCreateRequest(base, name))
		a.dispatch(func() {
			if !a.worktreePanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.worktree
			p.Creating = false
			if err != nil {
				p.Status = ""
				p.Err = "创建工作树失败：" + err.Error()
				return
			}
			var result worktreeCreateResult
			if err := json.Unmarshal(raw, &result); err != nil || result.Worktree == nil ||
				strings.TrimSpace(result.Worktree.Directory) == "" {
				p.Status = ""
				p.Err = "创建工作树响应无效。"
				return
			}
			a.applyWorktreeCreated(*result.Worktree, openInNewChat)
		})
	}()
}

// applyWorktreeCreated folds one canonical create result into the panel. When
// the user asked to open the worktree, the intent is queued even if the runtime
// returned "pending": readiness arrives later as a mothx/worktree/status
// notification and is consumed by settleWorktreeOpen once the shell is idle.
func (a *nativeApp) applyWorktreeCreated(created worktreeView, openInNewChat bool) {
	p := &a.worktree
	p.Worktrees = append(p.Worktrees, created)
	p.DraftName = ""
	p.Status = "工作树已创建：" + created.Directory
	if openInNewChat {
		switch created.Status {
		case "failed":
			p.Status = ""
			p.Err = "工作树准备失败，无法打开新会话。"
		case "removed":
			p.Status = ""
			p.Err = "工作树已被移除，无法打开新会话。"
		default:
			a.queuePendingWorktreeOpen(created)
			if a.settleWorktreeOpen() {
				return
			}
			if created.Status == "pending" {
				p.Status = "工作树已创建，待就绪后自动打开新会话：" + created.Directory
			} else {
				p.Status = "工作树已创建，将在空闲后打开新会话：" + created.Directory
			}
		}
	}
	a.loadWorktrees()
}

const (
	worktreeConfirmRemove = "remove"
	worktreeConfirmReset  = "reset"
)

func (a *nativeApp) askRemoveWorktree(tree worktreeView) {
	p := &a.worktree
	p.ConfirmOpen = true
	p.ConfirmKind = worktreeConfirmRemove
	p.ConfirmTree = tree
	p.ConfirmErr = ""
	p.ConfirmBusy = false
}

func (a *nativeApp) askResetWorktree(tree worktreeView) {
	p := &a.worktree
	p.ConfirmOpen = true
	p.ConfirmKind = worktreeConfirmReset
	p.ConfirmTree = tree
	p.ConfirmErr = ""
	p.ConfirmBusy = false
}

func (a *nativeApp) closeWorktreeConfirm() {
	p := &a.worktree
	p.ConfirmOpen = false
	p.ConfirmKind = ""
	p.ConfirmTree = worktreeView{}
	p.ConfirmBusy = false
	p.ConfirmErr = ""
}

func (a *nativeApp) confirmWorktreeAction() {
	p := &a.worktree
	if p.ConfirmBusy || strings.TrimSpace(p.ConfirmTree.Directory) == "" {
		return
	}
	client := a.acp
	if client == nil {
		p.ConfirmErr = "尚未连接到 MothX ACP。"
		return
	}
	method := "mothx/worktree/remove"
	if p.ConfirmKind == worktreeConfirmReset {
		method = "mothx/worktree/reset"
	}
	request := map[string]any{"directory": p.ConfirmTree.Directory}
	if id := strings.TrimSpace(p.ConfirmTree.ID); id != "" {
		request["id"] = id
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	display := p.ConfirmTree.Directory
	p.ConfirmBusy = true
	p.ConfirmErr = ""
	go func() {
		_, err := client.call(method, request)
		a.dispatch(func() {
			if !a.worktreePanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.worktree
			p.ConfirmBusy = false
			if err != nil {
				p.ConfirmErr = "工作树操作失败：" + err.Error()
				return
			}
			p.ConfirmOpen = false
			if p.ConfirmKind == worktreeConfirmReset {
				p.Status = "工作树已重置：" + display
			} else {
				p.Status = "工作树已移除：" + display
			}
			a.loadWorktrees()
		})
	}()
}

// applyWorktreeStatus folds one canonical mothx/worktree/status event into the
// advisory readiness projection. It never authorizes an action; removal and
// reset still go through the Runtime.
func (a *nativeApp) applyWorktreeStatus(payload worktreeStatusPayload) {
	directory := strings.TrimSpace(payload.Worktree.Directory)
	if directory == "" {
		return
	}
	a.historyPanel.Worktrees[directory] = payload.Worktree.Status
	p := &a.worktree
	for i := range p.Worktrees {
		if !worktreeSameDirectory(p.Worktrees[i].Directory, directory) {
			continue
		}
		if payload.Worktree.Status != "" {
			p.Worktrees[i].Status = payload.Worktree.Status
		}
		if payload.Worktree.Error != "" {
			p.Worktrees[i].Error = payload.Worktree.Error
		} else {
			p.Worktrees[i].Error = ""
		}
		if payload.Worktree.ID != "" {
			p.Worktrees[i].ID = payload.Worktree.ID
		}
		if payload.Worktree.Branch != "" {
			p.Worktrees[i].Branch = payload.Worktree.Branch
		}
		if payload.Worktree.Name != "" {
			p.Worktrees[i].Name = payload.Worktree.Name
		}
		a.resolvePendingWorktreeStatus(directory, payload.Worktree.Status, payload.Worktree.Error, payload.Worktree.ID)
		return
	}
	if a.worktree.Open && payload.Worktree.Status != "removed" {
		p.Worktrees = append(p.Worktrees, worktreeView{
			ID:        payload.Worktree.ID,
			Name:      payload.Worktree.Name,
			Branch:    payload.Worktree.Branch,
			Directory: directory,
			Status:    payload.Worktree.Status,
			Error:     payload.Worktree.Error,
		})
	}
	a.resolvePendingWorktreeStatus(directory, payload.Worktree.Status, payload.Worktree.Error, payload.Worktree.ID)
}

// worktreePathKey normalizes a directory for client-side identity matching only.
// It never authorizes a session; the Runtime still validates the directory.
func worktreePathKey(directory string) string {
	return strings.TrimRight(strings.TrimSpace(directory), "/\\")
}

func worktreeSameDirectory(left, right string) bool {
	a, b := worktreePathKey(left), worktreePathKey(right)
	return a != "" && a == b
}

// queuePendingWorktreeOpen remembers an "open in new session" intent keyed by
// both the canonical directory and the worktree id returned by create.
func (a *nativeApp) queuePendingWorktreeOpen(tree worktreeView) {
	p := &a.worktree
	p.PendingOpenDirectory = worktreePathKey(tree.Directory)
	p.PendingOpenID = strings.TrimSpace(tree.ID)
	p.PendingOpenReady = worktreeReady(tree)
}

func (a *nativeApp) clearPendingWorktreeOpen() {
	a.worktree.PendingOpenDirectory = ""
	a.worktree.PendingOpenID = ""
	a.worktree.PendingOpenReady = false
}

// settleWorktreeOpen consumes a deferred "open in new session" intent whenever
// the shell is idle. Every transition that clears running, creatingSession, or
// loadingSession calls it, so an intent can never hang after the UI has
// promised to open the worktree once the shell is free.
func (a *nativeApp) settleWorktreeOpen() bool {
	if a.backgroundRouting || a.running || a.creatingSession || a.loadingSession != "" {
		return false
	}
	return a.attemptPendingWorktreeOpen()
}

// resolvePendingWorktreeStatus consumes a readiness event for a queued intent.
// Ready opens the new session once idle; failed/removed drop the intent and
// surface the terminal reason instead of leaving a dead request behind.
func (a *nativeApp) resolvePendingWorktreeStatus(directory, status, errText, id string) {
	p := &a.worktree
	if p.PendingOpenDirectory == "" {
		return
	}
	matched := worktreeSameDirectory(p.PendingOpenDirectory, directory) ||
		(p.PendingOpenID != "" && strings.TrimSpace(id) != "" && p.PendingOpenID == strings.TrimSpace(id))
	if !matched {
		return
	}
	switch status {
	case "ready":
		// Record readiness on the intent itself so a later stale list snapshot
		// cannot revoke it while the shell is still busy.
		p.PendingOpenReady = true
		if a.attemptPendingWorktreeOpen() {
			return
		}
		p.Status = "工作树已就绪，空闲后自动打开新会话。"
	case "failed":
		a.clearPendingWorktreeOpen()
		p.Status = ""
		if message := strings.TrimSpace(errText); message != "" {
			p.Err = "工作树准备失败：" + message
		} else {
			p.Err = "工作树准备失败，无法打开新会话。"
		}
	case "removed":
		a.clearPendingWorktreeOpen()
		p.Status = ""
		p.Err = "工作树已被移除，无法打开新会话。"
	}
}

// attemptPendingWorktreeOpen opens the queued worktree as the workspace of a
// fresh session exactly once. It only consumes the intent when the shell is
// idle and the canonical state is usable; a pending or busy shell keeps it.
func (a *nativeApp) attemptPendingWorktreeOpen() bool {
	p := &a.worktree
	directory := strings.TrimSpace(p.PendingOpenDirectory)
	if directory == "" {
		return false
	}
	// Fold any readiness the current projection already observes into the
	// intent before the busy gate, so a deferred open cannot lose it while the
	// shell is still busy.
	a.notePendingWorktreeReadiness()
	if a.running || a.creatingSession || a.loadingSession != "" {
		return false
	}
	status := ""
	found := false
	for _, tree := range p.Worktrees {
		if worktreeSameRow(tree, worktreeView{ID: p.PendingOpenID, Directory: directory}) {
			status = tree.Status
			if candidate := strings.TrimSpace(tree.Directory); candidate != "" {
				directory = candidate
			}
			found = true
			break
		}
	}
	if !found {
		for known, knownStatus := range a.historyPanel.Worktrees {
			if worktreeSameDirectory(known, directory) {
				status = knownStatus
				found = true
				break
			}
		}
	}
	switch status {
	case "failed":
		a.clearPendingWorktreeOpen()
		p.Status = ""
		if p.Err == "" {
			p.Err = "工作树准备失败，无法打开新会话。"
		}
		return false
	case "removed":
		a.clearPendingWorktreeOpen()
		p.Status = ""
		p.Err = "工作树已被移除，无法打开新会话。"
		return false
	}
	// An already-observed ready event outranks a lagging pending snapshot.
	if !p.PendingOpenReady && (!found || !worktreeReady(worktreeView{Directory: directory, Status: status})) {
		return false
	}
	a.clearPendingWorktreeOpen()
	p.Status = ""
	p.Err = ""
	if p.Open {
		a.closeWorktreePanel()
	}
	a.newChatIn(directory)
	return true
}

// worktreeReady reports whether the directory can host a new session. An empty
// status is treated as ready for older servers that did not project readiness.
func worktreeReady(tree worktreeView) bool {
	if tree.External {
		return false
	}
	return tree.Status == "" || tree.Status == "ready"
}

func worktreeDisplayName(tree worktreeView) string {
	if name := strings.TrimSpace(tree.Name); name != "" {
		return name
	}
	directory := strings.TrimSuffix(strings.TrimSpace(tree.Directory), "/")
	if idx := strings.LastIndex(directory, "/"); idx >= 0 && idx+1 < len(directory) {
		return directory[idx+1:]
	}
	if directory == "" {
		return T("工作树")
	}
	return directory
}

func worktreeStatusLabel(status string) string {
	switch status {
	case "pending":
		return "准备中"
	case "ready":
		return "就绪"
	case "failed":
		return "失败"
	case "removed":
		return "已移除"
	case "":
		return "未知"
	default:
		return status
	}
}

func worktreeSummary(count int) string {
	if count == 0 {
		return T("尚未创建隔离工作树")
	}
	return Tf("%d 个工作树", count)
}
