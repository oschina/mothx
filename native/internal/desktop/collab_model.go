package desktop

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// This file owns the Native projections of the session-scoped collaboration
// surfaces:
//
//   - mothx/manage/router/{state,activate,deactivate}: the external ACP worker
//     selection that puts a session in Router mode.
//   - mothx/subagent/{list,messages}: the read-only child-agent roster and
//     transcript.
//
// Both are projections of Runtime-owned state. Activation validation, the idle
// fence, delegation tools, and child lifecycles stay in internal/agentruntime;
// this client never constructs agents, prompts, or child runs.

// --- Router ----------------------------------------------------------------

type routerStateView struct {
	Enabled        bool     `json:"enabled"`
	ProfileIDs     []string `json:"profileIds,omitempty"`
	PrimaryProfile string   `json:"primaryProfile,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

type routerPanelState struct {
	Open bool

	SessionID  string
	Enabled    bool
	Mode       string
	ProfileIDs []string
	Selection  map[string]bool
	Workers    []acpWorkerProfileView

	Messages         []workerMessageView
	PendingQuestions []workerQuestionView
	WorkerLoaded     bool
	WorkerLoading    bool
	WorkerErr        string

	Loaded  bool
	Loading bool
	Busy    bool
	Err     string
	Status  string

	Seq       int64
	WorkerSeq int64
}

// Worker messages are the read-only Router evidence projection. They never
// provide an answer surface: pending questions stay pending until the Runtime
// decision path resolves them.
type workerMessageView struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionId,omitempty"`
	ChildID     string `json:"childId,omitempty"`
	ParentRunID string `json:"parentRunId,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Body        string `json:"body,omitempty"`
	Status      string `json:"status,omitempty"`
	RunID       string `json:"runId,omitempty"`
	Reason      string `json:"reason,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type workerQuestionView struct {
	ID        string   `json:"id"`
	SessionID string   `json:"sessionId,omitempty"`
	RunID     string   `json:"runId,omitempty"`
	ChildID   string   `json:"childId,omitempty"`
	Question  string   `json:"question,omitempty"`
	Options   []string `json:"options,omitempty"`
	CreatedAt string   `json:"createdAt,omitempty"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

type workerMessagesResultView struct {
	Messages         []workerMessageView  `json:"messages"`
	PendingQuestions []workerQuestionView `json:"pendingQuestions"`
}

func newRouterPanelState() routerPanelState {
	return routerPanelState{Selection: make(map[string]bool)}
}

func (a *nativeApp) routerCapable() bool {
	return a.featureEnabled("manageRouter")
}

func (a *nativeApp) routerPanelCurrent(seq int64, reloadSeq int64) bool {
	p := &a.router
	return p.Open && p.SessionID == a.currentSession &&
		seq == atomic.LoadInt64(&p.Seq) && reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) invalidateRouterPanel() {
	p := &a.router
	atomic.AddInt64(&p.Seq, 1)
	atomic.AddInt64(&p.WorkerSeq, 1)
	p.Open = false
	p.SessionID = ""
	p.Enabled = false
	p.Mode = ""
	p.ProfileIDs = nil
	p.Selection = make(map[string]bool)
	p.Workers = nil
	p.Messages = nil
	p.PendingQuestions = nil
	p.WorkerLoaded = false
	p.WorkerLoading = false
	p.WorkerErr = ""
	p.Loaded = false
	p.Loading = false
	p.Busy = false
	p.Err = ""
	p.Status = ""
}

func (a *nativeApp) openRouterPanel() {
	if !a.routerCapable() || a.currentSession == "" {
		return
	}
	p := &a.router
	a.invalidateRouterPanel()
	p.Open = true
	p.SessionID = a.currentSession
	a.loadRouterState()
}

func (a *nativeApp) closeRouterPanel() {
	atomic.AddInt64(&a.router.Seq, 1)
	a.router.Open = false
	a.router.SessionID = ""
	a.router.Busy = false
}

func (a *nativeApp) loadRouterState() {
	p := &a.router
	if !p.Open || p.Loading || !a.routerCapable() {
		return
	}
	client := a.acp
	if client == nil {
		p.Loaded = true
		p.Err = "尚未连接到 MothX ACP。"
		return
	}
	sessionID := p.SessionID
	if sessionID == "" {
		sessionID = a.currentSession
		p.SessionID = sessionID
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Loading = true
	p.Err = ""
	go func() {
		raw, err := client.Call("mothx/manage/router/state", map[string]any{"sessionId": sessionID})
		var workers []acpWorkerProfileView
		if a.featureEnabled("manageACPProfiles") {
			if catalogRaw, catalogErr := client.Call("mothx/manage/acp/profiles/list", map[string]any{}); catalogErr == nil {
				var catalog acpWorkerCatalogView
				if json.Unmarshal(catalogRaw, &catalog) == nil {
					workers = catalog.Profiles
				}
			}
		}
		a.dispatch(func() {
			if !a.routerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.router
			p.Loading = false
			if err != nil {
				p.Err = "读取 Router 状态失败：" + err.Error()
				return
			}
			var state routerStateView
			if err := json.Unmarshal(raw, &state); err != nil {
				p.Err = "Router 响应无效：" + err.Error()
				return
			}
			a.applyRouterState(&state, workers)
			a.startWorkerMessagesLoad()
		})
	}()
}

func (a *nativeApp) applyRouterState(state *routerStateView, workers []acpWorkerProfileView) {
	if state == nil {
		return
	}
	p := &a.router
	p.Loaded = true
	p.Loading = false
	p.Err = ""
	p.Enabled = state.Enabled
	p.Mode = state.Mode
	p.ProfileIDs = append([]string(nil), state.ProfileIDs...)
	p.Workers = workers
	p.Selection = make(map[string]bool, len(p.ProfileIDs))
	for _, id := range p.ProfileIDs {
		p.Selection[id] = true
	}
}

// routerSelectableWorkers returns only enabled, Router-eligible profiles, in
// catalog order so activation sends a stable selection.
func (a *nativeApp) routerSelectableWorkers() []acpWorkerProfileView {
	out := make([]acpWorkerProfileView, 0, len(a.router.Workers))
	for _, worker := range a.router.Workers {
		if worker.Enabled && worker.RouterEligible {
			out = append(out, worker)
		}
	}
	return out
}

func (a *nativeApp) toggleRouterWorker(id string) {
	id = strings.TrimSpace(id)
	if id == "" || a.router.Busy {
		return
	}
	eligible := false
	for _, worker := range a.routerSelectableWorkers() {
		if worker.ID == id {
			eligible = true
			break
		}
	}
	if !eligible {
		return
	}
	if a.router.Selection == nil {
		a.router.Selection = make(map[string]bool)
	}
	if a.router.Selection[id] {
		delete(a.router.Selection, id)
	} else {
		a.router.Selection[id] = true
	}
}

// routerSelectedIDs returns the selected worker ids in catalog order.
func (a *nativeApp) routerSelectedIDs() []string {
	ids := make([]string, 0, len(a.router.Selection))
	for _, worker := range a.routerSelectableWorkers() {
		if a.router.Selection[worker.ID] {
			ids = append(ids, worker.ID)
		}
	}
	return ids
}

func (a *nativeApp) routerWorkerMessagesCapable() bool {
	return a.featureEnabled("workerMessages")
}

func (a *nativeApp) routerWorkerMessagesCurrent(seq, reloadSeq int64) bool {
	p := &a.router
	return p.Open && p.SessionID == a.currentSession &&
		seq == atomic.LoadInt64(&p.WorkerSeq) && reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// startWorkerMessagesLoad reads the Runtime-owned worker evidence projection.
// It is advisory: a session without Router activity simply has no messages.
func (a *nativeApp) startWorkerMessagesLoad() {
	p := &a.router
	if !a.routerWorkerMessagesCapable() || p.WorkerLoading || p.SessionID == "" {
		return
	}
	client := a.acp
	if client == nil {
		p.WorkerLoaded = true
		return
	}
	seq := atomic.AddInt64(&p.WorkerSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	sessionID := p.SessionID
	p.WorkerLoading = true
	p.WorkerErr = ""
	go func() {
		raw, err := client.Call("mothx/worker/messages", map[string]any{"sessionId": sessionID})
		a.dispatch(func() {
			if !a.routerWorkerMessagesCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.router
			p.WorkerLoading = false
			if err != nil {
				p.WorkerLoaded = true
				p.WorkerErr = "读取 Worker 消息失败：" + err.Error()
				return
			}
			var result workerMessagesResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.WorkerLoaded = true
				p.WorkerErr = "Worker 消息响应无效：" + err.Error()
				return
			}
			p.Messages = result.Messages
			p.PendingQuestions = result.PendingQuestions
			p.WorkerLoaded = true
			p.WorkerErr = ""
		})
	}()
}

func (a *nativeApp) activateRouter() {
	p := &a.router
	if !a.routerCapable() || p.Busy || p.Loading {
		return
	}
	client := a.acp
	if client == nil {
		p.Err = "尚未连接到 MothX ACP。"
		return
	}
	ids := a.routerSelectedIDs()
	if len(ids) == 0 {
		p.Err = "请至少选择一个可用的 Router 工作器。"
		return
	}
	a.runRouterMutation("mothx/manage/router/activate", map[string]any{
		"sessionId":  p.SessionID,
		"profileIds": ids,
	}, "Router 已启用")
}

func (a *nativeApp) deactivateRouter() {
	p := &a.router
	if !a.routerCapable() || p.Busy || p.Loading {
		return
	}
	a.runRouterMutation("mothx/manage/router/deactivate", map[string]any{
		"sessionId": p.SessionID,
	}, "Router 已停用")
}

func (a *nativeApp) runRouterMutation(method string, request map[string]any, okMessage string) {
	p := &a.router
	client := a.acp
	if client == nil {
		p.Err = "尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Busy = true
	p.Err = ""
	p.Status = "正在更新 Router…"
	go func() {
		raw, err := client.Call(method, request)
		a.dispatch(func() {
			if !a.routerPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.router
			p.Busy = false
			if err != nil {
				p.Status = ""
				p.Err = "更新 Router 失败：" + err.Error()
				return
			}
			var state routerStateView
			if err := json.Unmarshal(raw, &state); err != nil {
				p.Status = ""
				p.Err = "Router 响应无效：" + err.Error()
				return
			}
			a.applyRouterState(&state, p.Workers)
			p.Status = okMessage
			if state.Mode != "" {
				a.mode = state.Mode
			}
		})
	}()
}

// --- Subagents --------------------------------------------------------------

type subagentInfoView struct {
	AgentID           string `json:"agentId"`
	ParentAgentID     string `json:"parentAgentId,omitempty"`
	MemberID          string `json:"memberId,omitempty"`
	ExpertID          string `json:"expertId,omitempty"`
	MemberDisplayName string `json:"memberDisplayName,omitempty"`
	MemberEmoji       string `json:"memberEmoji,omitempty"`
	MemberRole        string `json:"memberRole,omitempty"`
	Status            string `json:"status"`
	Active            bool   `json:"active"`
	MessageCount      int    `json:"messageCount"`
	LastResponse      string `json:"lastResponse,omitempty"`
	Error             string `json:"error,omitempty"`
	StartedAt         string `json:"startedAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type subagentListResultView struct {
	Agents []subagentInfoView `json:"agents"`
}

type subagentMessagesResultView struct {
	AgentID string          `json:"agentId"`
	Updates []sessionUpdate `json:"updates"`
}

type subagentPanelState struct {
	Open bool

	SessionID string
	Agents    []subagentInfoView
	Selected  string
	Updates   []sessionUpdate
	Messages  []*chatMessage

	Loaded     bool
	Loading    bool
	LoadingMsg bool
	Err        string
	Status     string

	Seq         int64
	MessagesSeq int64
	PollSeq     int64
}

func (a *nativeApp) subagentCapable() bool {
	return a.featureEnabled("subagentPanel")
}

func (a *nativeApp) subagentPanelCurrent(seq int64, reloadSeq int64) bool {
	p := &a.subagent
	return p.Open && p.SessionID == a.currentSession &&
		seq == atomic.LoadInt64(&p.Seq) && reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) invalidateSubagentPanel() {
	p := &a.subagent
	atomic.AddInt64(&p.Seq, 1)
	atomic.AddInt64(&p.MessagesSeq, 1)
	p.Open = false
	p.SessionID = ""
	p.Agents = nil
	p.Selected = ""
	p.Updates = nil
	p.Messages = nil
	p.Loaded = false
	atomic.AddInt64(&p.PollSeq, 1)
	p.Loading = false
	p.LoadingMsg = false
	p.Err = ""
	p.Status = ""
}

func (a *nativeApp) openSubagentPanel() {
	if !a.subagentCapable() || a.currentSession == "" {
		return
	}
	p := &a.subagent
	a.invalidateSubagentPanel()
	p.Open = true
	p.SessionID = a.currentSession
	a.loadSubagents()
}

func (a *nativeApp) closeSubagentPanel() {
	atomic.AddInt64(&a.subagent.Seq, 1)
	atomic.AddInt64(&a.subagent.MessagesSeq, 1)
	atomic.AddInt64(&a.subagent.PollSeq, 1)
	a.subagent.Open = false
	a.subagent.SessionID = ""
}

func (a *nativeApp) loadSubagents() {
	p := &a.subagent
	if !p.Open || p.Loading || !a.subagentCapable() {
		return
	}
	client := a.acp
	if client == nil {
		p.Loaded = true
		p.Err = "尚未连接到 MothX ACP。"
		return
	}
	sessionID := p.SessionID
	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.Loading = true
	p.Err = ""
	go func() {
		raw, err := client.Call("mothx/subagent/list", map[string]any{"sessionId": sessionID})
		a.dispatch(func() {
			if !a.subagentPanelCurrent(seq, startReloadSeq) {
				return
			}
			p := &a.subagent
			p.Loading = false
			if err != nil {
				p.Err = "读取子代理失败：" + err.Error()
				return
			}
			var result subagentListResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Err = "子代理响应无效：" + err.Error()
				return
			}
			p.Agents = result.Agents
			p.Loaded = true
			if p.Selected == "" && len(result.Agents) > 0 {
				p.Selected = result.Agents[0].AgentID
				a.loadSubagentMessages()
				a.startSubagentPoll(p.Selected)
			} else {
				stillThere := false
				for _, agent := range result.Agents {
					if agent.AgentID == p.Selected {
						stillThere = true
						break
					}
				}
				if !stillThere {
					p.Selected = ""
					p.Updates = nil
					p.Messages = nil
				}
			}
		})
	}()
}

func (a *nativeApp) selectSubagent(agentID string) {
	p := &a.subagent
	if agentID == "" || agentID == p.Selected {
		return
	}
	p.Selected = agentID
	p.Updates = nil
	p.Messages = nil
	a.loadSubagentMessages()
	a.startSubagentPoll(agentID)
}

// startSubagentPoll refreshes a running child at a gentle cadence while the
// panel is visible. Desktop does the same; the child has no separate stream.
func (a *nativeApp) startSubagentPoll(agentID string) {
	if a.win == nil || agentID == "" {
		return
	}
	p := &a.subagent
	seq := atomic.AddInt64(&p.PollSeq, 1)
	go func() {
		for {
			time.Sleep(2 * time.Second)
			done := make(chan bool, 1)
			a.win.Update(func() {
				if !p.Open || p.Selected != agentID || seq != atomic.LoadInt64(&p.PollSeq) {
					done <- false
					return
				}
				info, ok := a.subagentSelectedInfo()
				if !ok || !info.Active {
					done <- false
					return
				}
				if !p.LoadingMsg {
					a.loadSubagentMessages()
				}
				done <- true
			})
			select {
			case active := <-done:
				if !active {
					return
				}
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()
}

func (a *nativeApp) loadSubagentMessages() {
	p := &a.subagent
	if !p.Open || p.LoadingMsg || p.Selected == "" {
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	sessionID := p.SessionID
	agentID := p.Selected
	seq := atomic.AddInt64(&p.MessagesSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.LoadingMsg = true
	go func() {
		raw, err := client.Call("mothx/subagent/messages", map[string]any{
			"sessionId": sessionID,
			"agentId":   agentID,
		})
		a.dispatch(func() {
			if !p.Open || seq != atomic.LoadInt64(&p.MessagesSeq) ||
				p.Selected != agentID || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p.LoadingMsg = false
			if err != nil {
				p.Status = "读取子代理消息失败：" + err.Error()
				return
			}
			var result subagentMessagesResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				p.Status = "子代理消息响应无效：" + err.Error()
				return
			}
			p.Updates = result.Updates
			p.Messages = reduceSubagentUpdates(sessionID, result.Updates)
			p.Status = ""
		})
	}()
}

// reduceSubagentUpdates replays the canonical child updates through the same
// transcript reducer as the main chat. The resulting cards keep Markdown,
// thoughts, tool input/diffs, and expansion state instead of flattening the
// child conversation into single text lines.
func reduceSubagentUpdates(sessionID string, updates []sessionUpdate) []*chatMessage {
	temp := &nativeApp{
		currentSession:   sessionID,
		messageIndex:     make(map[string]*chatMessage),
		replayingHistory: true,
	}
	for _, update := range updates {
		temp.applyUpdate(sessionUpdateNotification{SessionID: sessionID, Update: update})
	}
	for _, message := range temp.messages {
		message.Done = true
	}
	return temp.messages
}

func subagentDisplayName(agent subagentInfoView) string {
	if name := strings.TrimSpace(agent.MemberDisplayName); name != "" {
		if emoji := strings.TrimSpace(agent.MemberEmoji); emoji != "" {
			return emoji + " " + name
		}
		return name
	}
	if role := strings.TrimSpace(agent.MemberRole); role != "" {
		return role
	}
	if id := strings.TrimSpace(agent.AgentID); id != "" {
		return id
	}
	return T("子代理")
}

// subagentRunSummary projects the header count only; it never changes the
// canonical child status or starts a run.
func subagentRunSummary(agents []subagentInfoView) (running, total int) {
	for _, agent := range agents {
		total++
		switch strings.ToLower(strings.TrimSpace(agent.Status)) {
		case "running", "working", "in_progress", "active":
			running++
		}
	}
	return running, total
}

// subagentUpdateLine renders one replay update as a single readable line. The
// projection never merges or re-orders canonical child events; it only formats
// the same chunks the chat transcript consumes.
func subagentUpdateLine(update sessionUpdate) string {
	switch update.SessionUpdate {
	case "user_message_chunk":
		if text := contentText(update.Content); text != "" {
			return "用户：" + text
		}
	case "agent_message_chunk":
		if text := contentText(update.Content); text != "" {
			return "Agent：" + text
		}
	case "agent_thought_chunk":
		if text := contentText(update.Content); text != "" {
			return "思考：" + text
		}
	case "tool_call", "tool_call_update":
		title := strings.TrimSpace(update.Title)
		if title == "" {
			title = "工具调用"
		}
		status := strings.TrimSpace(update.Status)
		if status != "" {
			title += " · " + status
		}
		if text := toolContentText(update.Content, update.RawOutput); text != "" {
			return "工具：" + title + "\n" + text
		}
		return "工具：" + title
	case "plan":
		if len(update.Entries) > 0 {
			lines := make([]string, 0, len(update.Entries))
			for _, entry := range update.Entries {
				lines = append(lines, entry.Content)
			}
			return "计划：" + strings.Join(lines, "; ")
		}
	}
	return ""
}

func (a *nativeApp) subagentSelectedInfo() (subagentInfoView, bool) {
	for _, agent := range a.subagent.Agents {
		if agent.AgentID == a.subagent.Selected {
			return agent, true
		}
	}
	return subagentInfoView{}, false
}

// --- Subagent live projection ----------------------------------------------
//
// The Runtime emits `_mothx/session_event` with event=subagent whenever a
// managed child starts or reaches a terminal state. Native projects that
// canonical stream into (1) an inline transcript card, (2) a per-session
// roster used by the header summary, and (3) the open panel's live list.
// Child identity, status, and transcripts stay Runtime-owned; the roster is
// an in-memory projection cache reconciled by mothx/subagent/list.

// subagentStatusKind normalizes the Runtime child status vocabulary into the
// five projection states shared by the inline card, roster, and summary.
func subagentStatusKind(status string) string {
	switch status {
	case "done", "completed":
		return "completed"
	case "error", "failed":
		return "failed"
	case "canceled", "cancelled":
		return "cancelled"
	case "incomplete":
		return "incomplete"
	default:
		return "running"
	}
}

// A long-lived team/ESM session can spawn many children. Keep running agents
// and the newest terminal ones; older terminal projections are recoverable
// from the Runtime list when the panel is reopened.
const maxSubagentsPerSession = 200

func capSubagentRoster(list []subagentInfoView) []subagentInfoView {
	if len(list) <= maxSubagentsPerSession {
		return list
	}
	running := make([]subagentInfoView, 0, len(list))
	terminal := make([]subagentInfoView, 0, len(list))
	for _, info := range list {
		if subagentStatusKind(info.Status) == "running" {
			running = append(running, info)
		} else {
			terminal = append(terminal, info)
		}
	}
	if len(running) >= maxSubagentsPerSession {
		return running[len(running)-maxSubagentsPerSession:]
	}
	keep := maxSubagentsPerSession - len(running)
	if len(terminal) > keep {
		terminal = terminal[len(terminal)-keep:]
	}
	return append(running, terminal...)
}

// mergeSubagentInfo keeps previously projected fields the new projection
// omits, then normalizes the status. The newer projection wins on conflicts.
func mergeSubagentInfo(previous *subagentInfoView, next subagentInfoView) subagentInfoView {
	merged := next
	if previous != nil {
		if merged.ParentAgentID == "" {
			merged.ParentAgentID = previous.ParentAgentID
		}
		if merged.MemberID == "" {
			merged.MemberID = previous.MemberID
		}
		if merged.ExpertID == "" {
			merged.ExpertID = previous.ExpertID
		}
		if merged.MemberDisplayName == "" {
			merged.MemberDisplayName = previous.MemberDisplayName
		}
		if merged.MemberEmoji == "" {
			merged.MemberEmoji = previous.MemberEmoji
		}
		if merged.MemberRole == "" {
			merged.MemberRole = previous.MemberRole
		}
		if merged.MessageCount == 0 {
			merged.MessageCount = previous.MessageCount
		}
		if merged.LastResponse == "" {
			merged.LastResponse = previous.LastResponse
		}
		if merged.StartedAt == "" {
			merged.StartedAt = previous.StartedAt
		}
	}
	merged.Status = subagentStatusKind(merged.Status)
	return merged
}

func (a *nativeApp) subagentRoster(sessionID string) []subagentInfoView {
	if sessionID == "" || a.subagentRosters == nil {
		return nil
	}
	return a.subagentRosters[sessionID]
}

// subagentSummary projects the header badge counts from the live roster.
func (a *nativeApp) subagentSummary(sessionID string) (total, running, failed int) {
	for _, info := range a.subagentRoster(sessionID) {
		total++
		switch subagentStatusKind(info.Status) {
		case "running":
			running++
		case "failed":
			failed++
		}
	}
	return total, running, failed
}

// applySubagentEvent folds one canonical child lifecycle event into the
// session roster and the owning transcript. handleSessionEvent has already
// routed background sessions, so the inline card lands in the right cache.
func (a *nativeApp) applySubagentEvent(ev sessionEvent) {
	if ev.SessionID == "" || ev.AgentID == "" {
		return
	}
	if a.subagentRosters == nil {
		a.subagentRosters = make(map[string][]subagentInfoView)
	}
	roster := a.subagentRosters[ev.SessionID]
	index := -1
	for i := range roster {
		if roster[i].AgentID == ev.AgentID {
			index = i
			break
		}
	}
	next := subagentInfoView{
		AgentID:           ev.AgentID,
		ParentAgentID:     ev.ParentAgentID,
		MemberID:          ev.MemberID,
		ExpertID:          ev.ExpertID,
		MemberDisplayName: ev.MemberDisplayName,
		MemberEmoji:       ev.MemberEmoji,
		MemberRole:        ev.MemberRole,
		Status:            ev.Status,
	}
	var previous *subagentInfoView
	if index >= 0 {
		previous = &roster[index]
	}
	merged := mergeSubagentInfo(previous, next)
	merged.Active = subagentStatusKind(merged.Status) == "running"
	if index >= 0 {
		roster[index] = merged
	} else {
		roster = append(roster, merged)
	}
	a.subagentRosters[ev.SessionID] = capSubagentRoster(roster)
	a.syncSubagentPanelRoster(ev.SessionID)

	m := a.upsertMessage("subagent:"+ev.AgentID, messageSubagent)
	m.SubagentID = ev.AgentID
	m.SubagentStatus = subagentStatusKind(merged.Status)
	m.SubagentTitle = subagentDisplayName(merged)
	m.SubagentRole = merged.MemberRole
	m.SubagentExpertID = merged.ExpertID
	m.Done = m.SubagentStatus != "running"
}

// syncSubagentPanelRoster keeps an open panel's list live without waiting for
// its next poll. The server list may carry extra fields; event updates merge
// into it rather than replacing it.
func (a *nativeApp) syncSubagentPanelRoster(sessionID string) {
	p := &a.subagent
	if !p.Open || p.SessionID != sessionID {
		return
	}
	roster := a.subagentRoster(sessionID)
	for _, info := range roster {
		found := false
		for i := range p.Agents {
			if p.Agents[i].AgentID == info.AgentID {
				p.Agents[i] = mergeSubagentInfo(&p.Agents[i], info)
				found = true
				break
			}
		}
		if !found {
			p.Agents = append(p.Agents, info)
		}
	}
}

// refreshSubagentRoster re-reads the canonical child list after a terminal
// event or session load. A failed read keeps the last known roster instead
// of inventing idle or empty state.
func (a *nativeApp) refreshSubagentRoster(sessionID string) {
	if sessionID == "" || !a.subagentCapable() {
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	if a.subagentRosterSeq == nil {
		a.subagentRosterSeq = make(map[string]int64)
	}
	a.subagentRosterSeq[sessionID]++
	seq := a.subagentRosterSeq[sessionID]
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.Call("mothx/subagent/list", map[string]any{"sessionId": sessionID})
		a.dispatch(func() {
			if err != nil || seq != a.subagentRosterSeq[sessionID] ||
				startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			var result subagentListResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				return
			}
			previous := a.subagentRosters[sessionID]
			merged := make([]subagentInfoView, 0, len(result.Agents))
			for _, info := range result.Agents {
				var prev *subagentInfoView
				for i := range previous {
					if previous[i].AgentID == info.AgentID {
						prev = &previous[i]
						break
					}
				}
				merged = append(merged, mergeSubagentInfo(prev, info))
			}
			if a.subagentRosters == nil {
				a.subagentRosters = make(map[string][]subagentInfoView)
			}
			a.subagentRosters[sessionID] = capSubagentRoster(merged)
			a.syncSubagentPanelRoster(sessionID)
		})
	}()
}

// openSubagentPanelFor opens the panel focused on one child, used by the
// inline transcript card. The canonical list still loads; the requested
// selection survives when the Runtime reports that child.
func (a *nativeApp) openSubagentPanelFor(agentID string) {
	if !a.subagentCapable() || a.currentSession == "" {
		return
	}
	a.openSubagentPanel()
	if agentID == "" {
		return
	}
	p := &a.subagent
	p.Selected = agentID
	a.loadSubagentMessages()
	a.startSubagentPoll(agentID)
}
