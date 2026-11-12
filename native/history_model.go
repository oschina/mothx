package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
)

// This file owns the Native projection of the ACP task-library contract:
// paginated mothx/session/listAll, project/scope filters, and the additive
// session operations (mothx/session/setTitle, mothx/session/setMeta,
// mothx/session/setWorkDir, session/fork, session/delete).
//
// Every mutation is executed by the shared Runtime. This client only builds
// the documented request shapes, projects canonical list rows, and refreshes.
// It never reads sessions.db, settings.json, or the project registry.

type projectView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	SessionCount *int   `json:"sessionCount,omitempty"`
}

type projectListResult struct {
	Projects []projectView `json:"projects"`
}

type historyState struct {
	Sessions []listedSession

	NextCursor string
	Scope      string
	ProjectID  string
	// AppliedQuery is the query the current page was loaded with. The input
	// field is a.historyQuery so typing never issues an ACP request.
	AppliedQuery string

	Loaded      bool
	Loading     bool
	LoadingMore bool
	Err         string
	Status      string

	// Seq guards paged list responses. Bumping it discards any in-flight page.
	Seq int64
	// ActionSeq guards session/project mutations independently of paging so a
	// completed page load cannot make a stale action response look current.
	ActionSeq int64

	// Session dialog: rename / project / delete.
	DialogOpen    bool
	DialogKind    string
	DialogSession string
	DialogTitle   string
	DialogValue   string
	DialogBusy    bool
	DialogErr     string

	// Project dialog: create / rename / delete. ProjectDialogSession, when
	// set for a create, assigns the new project to that session.
	ProjectDialogOpen    bool
	ProjectDialogKind    string
	ProjectDialogID      string
	ProjectDialogName    string
	ProjectDialogSession string
	ProjectDialogBusy    bool
	ProjectDialogErr     string

	// Worktree readiness by directory is projected from mothx/worktree/status.
	Worktrees map[string]string
}

func newHistoryState() historyState {
	return historyState{
		Scope:     "all",
		Worktrees: make(map[string]string),
	}
}

func (a *nativeApp) historyListCapable() bool {
	return a.featureEnabled("sessionListAll")
}

func (a *nativeApp) historyProjectsCapable() bool {
	return a.featureEnabled("projects")
}

func (a *nativeApp) historySessionMetaCapable() bool {
	return a.featureEnabled("sessionMeta")
}

func (a *nativeApp) historyForkCapable() bool {
	return a.featureEnabled("sessionFork")
}

func (a *nativeApp) historyWorkDirCapable() bool {
	return a.featureEnabled("sessionWorkDir")
}

func (a *nativeApp) historyPanelCurrent(seq int64) bool {
	return a.historyOpen && seq == atomic.LoadInt64(&a.historyPanel.Seq)
}

func (a *nativeApp) historyActionCurrent(actionSeq int64) bool {
	return actionSeq == atomic.LoadInt64(&a.historyPanel.ActionSeq)
}

// invalidateHistoryPanel makes every in-flight page/action stale and drops
// pending dialog state. The applied filters are kept so reopening the view
// resumes the last scope rather than silently widening it.
func (a *nativeApp) invalidateHistoryPanel() {
	p := &a.historyPanel
	atomic.AddInt64(&p.Seq, 1)
	atomic.AddInt64(&p.ActionSeq, 1)
	p.Sessions = nil
	p.NextCursor = ""
	p.Loaded = false
	p.Loading = false
	p.LoadingMore = false
	p.Err = ""
	p.Status = ""
	p.DialogOpen = false
	p.DialogKind = ""
	p.DialogSession = ""
	p.DialogTitle = ""
	p.DialogValue = ""
	p.DialogBusy = false
	p.DialogErr = ""
	p.ProjectDialogOpen = false
	p.ProjectDialogKind = ""
	p.ProjectDialogID = ""
	p.ProjectDialogName = ""
	p.ProjectDialogSession = ""
	p.ProjectDialogBusy = false
	p.ProjectDialogErr = ""
}

func (a *nativeApp) openHistory() {
	if a.settingsOpen {
		a.closeSettings()
	}
	a.closeRouterPanel()
	a.closeSubagentPanel()
	a.closeWorktreePanel()
	a.history = true
	a.historyOpen = true
	a.page = ""
	a.searching = false
	a.search = ""
	if !a.historyPanel.Loaded && !a.historyPanel.Loading {
		a.startHistoryLoad(false)
	}
	a.refreshProjects()
}

func (a *nativeApp) closeHistory() {
	a.historyOpen = false
	a.history = false
	a.invalidateHistoryPanel()
}

// startHistoryLoad loads the first page (or the next one when append is set).
func (a *nativeApp) startHistoryLoad(appendPage bool) {
	if !a.historyOpen {
		return
	}
	p := &a.historyPanel
	if p.Loading || p.LoadingMore {
		return
	}
	if a.conn == connFailed {
		p.Loaded = true
		p.Loading = false
		p.LoadingMore = false
		if a.connErr != "" {
			p.Err = a.connErr
		}
		return
	}
	if !a.featuresInitialized() {
		// The History view can be opened while ACP initialize is still in
		// flight. Keep the loading projection instead of caching a pre-init
		// “sessionListAll not advertised” fallback that never recovers.
		p.Loading = true
		p.LoadingMore = false
		p.Status = ""
		return
	}
	if !a.historyListCapable() {
		// Fallback for older runtimes: the sidebar catalog is the only list we
		// have, and local filtering keeps the view usable without inventing
		// pagination the server never offered.
		p.Loaded = true
		p.Loading = false
		p.Err = ""
		p.Status = "当前运行时未公开 sessionListAll；仅显示已加载的会话。"
		return
	}
	cursor := ""
	if appendPage {
		if strings.TrimSpace(p.NextCursor) == "" {
			return
		}
		cursor = p.NextCursor
	}
	p.Scope, p.ProjectID = normalizeHistoryScope(p.Scope, p.ProjectID)
	p.AppliedQuery = strings.TrimSpace(a.historyQuery)
	seq := atomic.AddInt64(&p.Seq, 1)
	if appendPage {
		p.LoadingMore = true
	} else {
		p.Loading = true
		p.Err = ""
	}
	p.Status = ""
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	scope, projectID, query := p.Scope, p.ProjectID, p.AppliedQuery
	go a.loadHistoryPage(seq, startReloadSeq, appendPage, cursor, scope, projectID, query)
}

func (a *nativeApp) loadHistoryPage(seq, startReloadSeq int64, appendPage bool, cursor, scope, projectID, query string) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.historyPanelCurrent(seq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p := &a.historyPanel
			p.Loading = false
			p.LoadingMore = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	params := buildHistoryListRequest(scope, projectID, query, cursor)
	raw, err := client.call("mothx/session/listAll", params)
	a.dispatch(func() {
		if !a.historyPanelCurrent(seq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		p := &a.historyPanel
		p.Loading = false
		p.LoadingMore = false
		if err != nil {
			p.Err = "读取会话历史失败：" + err.Error()
			return
		}
		var result sessionListResult
		if err := json.Unmarshal(raw, &result); err != nil {
			p.Err = "会话历史响应无效：" + err.Error()
			return
		}
		a.applyHistoryPage(&result, appendPage)
	})
}

// buildHistoryListRequest is the only place the adapter constructs the
// listAll parameters. Empty values stay absent so the server applies its own
// documented defaults ("all" scope, no query, first page).
func buildHistoryListRequest(scope, projectID, query, cursor string) map[string]any {
	scope, projectID = normalizeHistoryScope(scope, projectID)
	params := map[string]any{}
	if scope != "" && scope != "all" {
		params["scope"] = scope
	}
	if scope == "project" && projectID != "" {
		params["projectId"] = projectID
	}
	if query = strings.TrimSpace(query); query != "" {
		params["query"] = query
	}
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		params["cursor"] = cursor
	}
	return params
}

func normalizeHistoryScope(scope, projectID string) (string, string) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	projectID = strings.TrimSpace(projectID)
	switch scope {
	case "project":
		if projectID == "" {
			return "all", ""
		}
		return "project", projectID
	case "ungrouped":
		return "ungrouped", ""
	case "all":
		return "all", ""
	default:
		if projectID != "" {
			return "project", projectID
		}
		return "all", ""
	}
}

func (a *nativeApp) applyHistoryPage(result *sessionListResult, appendPage bool) {
	if result == nil {
		return
	}
	p := &a.historyPanel
	if appendPage {
		p.Sessions = append(p.Sessions, result.Sessions...)
	} else {
		p.Sessions = append([]listedSession(nil), result.Sessions...)
	}
	p.NextCursor = result.NextCursor
	p.Loaded = true
	p.Loading = false
	p.LoadingMore = false
	p.Err = ""
}

func (a *nativeApp) setHistoryScope(scope, projectID string) {
	p := &a.historyPanel
	scope, projectID = normalizeHistoryScope(scope, projectID)
	if scope == p.Scope && projectID == p.ProjectID && p.Loaded {
		return
	}
	p.Scope, p.ProjectID = scope, projectID
	// Reset the page projection first so a slow previous-scope response cannot
	// append into the wrong list; the sequence bump in startHistoryLoad makes
	// it stale anyway.
	p.NextCursor = ""
	p.Sessions = nil
	p.Loaded = false
	a.startHistoryLoad(false)
}

func (a *nativeApp) applyHistoryQuery() {
	p := &a.historyPanel
	query := strings.TrimSpace(a.historyQuery)
	if query == p.AppliedQuery {
		return
	}
	p.NextCursor = ""
	p.Sessions = nil
	p.Loaded = false
	a.startHistoryLoad(false)
}

func (a *nativeApp) clearHistoryQuery() {
	if a.historyQuery == "" && a.historyPanel.AppliedQuery == "" {
		return
	}
	a.historyQuery = ""
	a.historyPanel.AppliedQuery = ""
	a.historyPanel.NextCursor = ""
	a.historyPanel.Sessions = nil
	a.historyPanel.Loaded = false
	a.startHistoryLoad(false)
}

func (a *nativeApp) refreshHistory() {
	p := &a.historyPanel
	// Cancel any in-flight page instead of refusing to refresh: the sequence
	// bump makes its response stale, and a delete/fork/project change must not
	// be hidden just because a previous page is still loading.
	if p.Loading || p.LoadingMore {
		atomic.AddInt64(&p.Seq, 1)
		p.Loading = false
		p.LoadingMore = false
	}
	p.NextCursor = ""
	p.Loaded = false
	a.startHistoryLoad(false)
	a.refreshProjects()
}

func (a *nativeApp) loadMoreHistory() {
	a.startHistoryLoad(true)
}

// historyVisibleSessions is the rendered list. With sessionListAll the server
// already applied scope/query/pagination; the fallback filters the sidebar
// catalog client-side so an older runtime still renders a usable history.
func (a *nativeApp) historyVisibleSessions() []listedSession {
	p := &a.historyPanel
	if a.historyListCapable() {
		return p.Sessions
	}
	query := strings.ToLower(strings.TrimSpace(p.AppliedQuery))
	out := make([]listedSession, 0, len(a.sessions))
	for _, s := range a.sessions {
		if query != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.Cwd+" "+s.Provider+" "+s.Model), query) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// --- projects ---------------------------------------------------------------

// loadHistoryProjects refreshes the shared project catalog used by both the
// sidebar task tree and the history filters.
func (a *nativeApp) loadHistoryProjects() {
	a.refreshProjects()
}

func (a *nativeApp) historyProjectByID(id string) (projectView, bool) {
	return a.projectByID(id)
}

// sessionProjectID decodes the additive _meta.projectId, which the server
// serializes as a string or null.
func sessionProjectID(s listedSession) string {
	switch value := s.Meta.ProjectID.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	default:
		return ""
	}
}

func (a *nativeApp) updateSessionProject(sessionID, projectID string) {
	for i := range a.historyPanel.Sessions {
		if a.historyPanel.Sessions[i].SessionID != sessionID {
			continue
		}
		if projectID == "" {
			a.historyPanel.Sessions[i].Meta.ProjectID = nil
		} else {
			a.historyPanel.Sessions[i].Meta.ProjectID = projectID
		}
	}
	for i := range a.sessions {
		if a.sessions[i].SessionID != sessionID {
			continue
		}
		if projectID == "" {
			a.sessions[i].Meta.ProjectID = nil
		} else {
			a.sessions[i].Meta.ProjectID = projectID
		}
	}
}

func (a *nativeApp) updateSessionTitleLocal(sessionID, title string) {
	for i := range a.historyPanel.Sessions {
		if a.historyPanel.Sessions[i].SessionID == sessionID {
			a.historyPanel.Sessions[i].Title = title
		}
	}
	a.updateSessionTitle(sessionID, title, "")
	if a.currentSession == sessionID {
		a.currentTitle = title
	}
}

func (a *nativeApp) updateSessionCwdLocal(sessionID, cwd string) {
	for i := range a.historyPanel.Sessions {
		if a.historyPanel.Sessions[i].SessionID == sessionID {
			a.historyPanel.Sessions[i].Cwd = cwd
		}
	}
	for i := range a.sessions {
		if a.sessions[i].SessionID == sessionID {
			a.sessions[i].Cwd = cwd
		}
	}
	if a.currentSession == sessionID {
		a.workspace = cwd
	}
}

func (a *nativeApp) setSessionPinnedLocal(sessionID string, pinned bool) {
	for i := range a.historyPanel.Sessions {
		if a.historyPanel.Sessions[i].SessionID == sessionID {
			a.historyPanel.Sessions[i].Meta.Pinned = pinned
		}
	}
	for i := range a.sessions {
		if a.sessions[i].SessionID == sessionID {
			a.sessions[i].Meta.Pinned = pinned
		}
	}
}

func (a *nativeApp) historySessionByID(id string) (listedSession, bool) {
	for _, s := range a.historyPanel.Sessions {
		if s.SessionID == id {
			return s, true
		}
	}
	for _, s := range a.sessions {
		if s.SessionID == id {
			return s, true
		}
	}
	return listedSession{}, false
}

// --- request builders -------------------------------------------------------

func historyWorkspaceMeta(cwd string) map[string]any {
	return map[string]any{"workspace": map[string]any{"cwd": cwd}}
}

func buildSessionTitleRequest(session listedSession, title string) map[string]any {
	return map[string]any{
		"sessionId": session.SessionID,
		"title":     strings.TrimSpace(title),
		"_meta":     map[string]any{"mothx": historyWorkspaceMeta(session.Cwd)},
	}
}

// buildSessionMetaRequest keeps absent fields absent: pinned is only sent when
// the caller supplies it, and projectID nil is an explicit clear (JSON null).
func buildSessionMetaRequest(session listedSession, pinned *bool, projectID *string) map[string]any {
	request := map[string]any{
		"sessionId": session.SessionID,
		"_meta":     map[string]any{"mothx": historyWorkspaceMeta(session.Cwd)},
	}
	if pinned != nil {
		request["pinned"] = *pinned
	}
	if projectID != nil {
		if strings.TrimSpace(*projectID) == "" {
			request["projectId"] = nil
		} else {
			request["projectId"] = strings.TrimSpace(*projectID)
		}
	}
	return request
}

func buildSessionForkRequest(session listedSession, expertID ...string) map[string]any {
	request := map[string]any{
		"sessionId": session.SessionID,
		"cwd":       session.Cwd,
		"_meta": map[string]any{"mothx": map[string]any{
			"workspace":       map[string]any{"cwd": session.Cwd},
			"parentSessionId": session.SessionID,
		}},
	}
	if len(expertID) > 0 && strings.TrimSpace(expertID[0]) != "" {
		request["expertId"] = strings.TrimSpace(expertID[0])
	}
	return request
}

func buildSessionWorkDirRequest(session listedSession, cwd string) map[string]any {
	return map[string]any{
		"sessionId": session.SessionID,
		"cwd":       strings.TrimSpace(cwd),
		"_meta":     map[string]any{"mothx": historyWorkspaceMeta(session.Cwd)},
	}
}

func buildProjectRequest(id, name string) map[string]any {
	request := map[string]any{"name": strings.TrimSpace(name)}
	if id = strings.TrimSpace(id); id != "" {
		request["id"] = id
	}
	return request
}

// --- session dialog actions -------------------------------------------------

const (
	historyDialogRename  = "rename"
	historyDialogProject = "project"
	historyDialogDelete  = "delete"
)

func (a *nativeApp) openHistoryDialog(kind, sessionID string) {
	session, ok := a.historySessionByID(sessionID)
	if !ok {
		return
	}
	p := &a.historyPanel
	p.DialogOpen = true
	p.DialogKind = kind
	p.DialogSession = sessionID
	p.DialogTitle = session.Title
	p.DialogValue = ""
	p.DialogErr = ""
	p.DialogBusy = false
	switch kind {
	case historyDialogRename:
		p.DialogValue = session.Title
	case historyDialogProject:
		p.DialogValue = sessionProjectID(session)
	}
}

func (a *nativeApp) closeHistoryDialog() {
	p := &a.historyPanel
	atomic.AddInt64(&p.ActionSeq, 1)
	p.DialogOpen = false
	p.DialogKind = ""
	p.DialogSession = ""
	p.DialogTitle = ""
	p.DialogValue = ""
	p.DialogBusy = false
	p.DialogErr = ""
}

func (a *nativeApp) confirmHistoryDialog() {
	p := &a.historyPanel
	if p.DialogBusy {
		return
	}
	session, ok := a.historySessionByID(p.DialogSession)
	if !ok {
		p.DialogErr = "该会话已不在当前列表中。"
		return
	}
	switch p.DialogKind {
	case historyDialogRename:
		title := strings.TrimSpace(p.DialogValue)
		if title == "" {
			p.DialogErr = "会话标题不能为空。"
			return
		}
		a.runHistoryRename(session, title)
	case historyDialogProject:
		projectID := strings.TrimSpace(p.DialogValue)
		if projectID != "" {
			if _, ok := a.historyProjectByID(projectID); !ok {
				p.DialogErr = "所选项目不存在。"
				return
			}
		}
		a.runHistoryProjectAssignment(session, projectID)
	case historyDialogDelete:
		a.runHistoryDelete(session)
	}
}

func (a *nativeApp) runHistoryRename(session listedSession, title string) {
	client := a.acp
	p := &a.historyPanel
	if client == nil {
		p.DialogErr = "尚未连接到 MothX ACP。"
		return
	}
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.DialogBusy = true
	p.DialogErr = ""
	go func() {
		_, err := client.call("mothx/session/setTitle", buildSessionTitleRequest(session, title))
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p := &a.historyPanel
			p.DialogBusy = false
			if err != nil {
				p.DialogErr = "重命名失败：" + err.Error()
				return
			}
			a.updateSessionTitleLocal(session.SessionID, title)
			p.DialogOpen = false
			a.setSessionActionStatus("会话已重命名")
		})
	}()
}

func (a *nativeApp) runHistoryProjectAssignment(session listedSession, projectID string) {
	client := a.acp
	p := &a.historyPanel
	if client == nil {
		p.DialogErr = "尚未连接到 MothX ACP。"
		return
	}
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	value := projectID
	p.DialogBusy = true
	p.DialogErr = ""
	go func() {
		_, err := client.call("mothx/session/setMeta", buildSessionMetaRequest(session, nil, &value))
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p := &a.historyPanel
			p.DialogBusy = false
			if err != nil {
				p.DialogErr = "更新项目归属失败：" + err.Error()
				return
			}
			a.updateSessionProject(session.SessionID, projectID)
			p.DialogOpen = false
			if projectID == "" {
				a.setSessionActionStatus("已清除项目归属")
			} else {
				a.setSessionActionStatus("已更新项目归属")
			}
			// A project-scoped list may no longer contain the session.
			if p.Scope == "project" {
				a.refreshHistory()
			} else {
				a.refreshProjects()
			}
			a.refreshTaskLibrary()
		})
	}()
}

func (a *nativeApp) runHistoryDelete(session listedSession) {
	client := a.acp
	p := &a.historyPanel
	if client == nil {
		p.DialogErr = "尚未连接到 MothX ACP。"
		return
	}
	if a.sessionRunActive(session) {
		p.DialogErr = "该会话正在运行，无法删除。"
		return
	}
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.DialogBusy = true
	p.DialogErr = ""
	go func() {
		// session/close releases the open runtime; both calls tolerate an
		// already-closed session, and delete is idempotent server-side.
		_, _ = client.call("session/close", map[string]any{"sessionId": session.SessionID})
		_, err := client.call("session/delete", map[string]any{"sessionId": session.SessionID})
		if err != nil {
			_, err = client.call("mothx/session/delete", map[string]any{"sessionId": session.SessionID})
		}
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p := &a.historyPanel
			p.DialogBusy = false
			if err != nil {
				p.DialogErr = "删除失败：" + err.Error()
				return
			}
			deletedCurrent := a.currentSession == session.SessionID
			delete(a.sessionStates, session.SessionID)
			p.Sessions = removeListedSession(p.Sessions, session.SessionID)
			a.sessions = removeListedSession(a.sessions, session.SessionID)
			p.DialogOpen = false
			a.setSessionActionStatus("会话已删除")
			// Deletion cascades to child sessions in the Runtime, so a local
			// row removal is not enough: re-project the canonical page and the
			// sidebar catalog so an orphaned child can never stay visible.
			a.refreshHistory()
			a.refreshTaskLibrary()
			if deletedCurrent {
				// The deleted task has no canonical row; do not cache it again
				// while clearing the shell.
				a.currentSession = ""
				a.newChat()
			}
		})
	}()
}

func removeListedSession(sessions []listedSession, sessionID string) []listedSession {
	out := sessions[:0]
	for _, s := range sessions {
		if s.SessionID != sessionID {
			out = append(out, s)
		}
	}
	return out
}

func (a *nativeApp) toggleHistoryPin(session listedSession) {
	client := a.acp
	if client == nil || !a.historySessionMetaCapable() {
		return
	}
	p := &a.historyPanel
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	next := !session.Meta.Pinned
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		_, err := client.call("mothx/session/setMeta", buildSessionMetaRequest(session, &next, nil))
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			if err != nil {
				a.setSessionActionStatus("更新置顶失败：" + err.Error())
				return
			}
			a.setSessionPinnedLocal(session.SessionID, next)
			if next {
				a.setSessionActionStatus("会话已置顶")
			} else {
				a.setSessionActionStatus("已取消置顶")
			}
		})
	}()
}

func (a *nativeApp) forkHistorySession(session listedSession) {
	client := a.acp
	if client == nil || !a.historyForkCapable() {
		return
	}
	if a.sessionRunActive(session) {
		a.setSessionActionStatus("该会话正在运行，请先停止")
		return
	}
	p := &a.historyPanel
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("session/fork", buildSessionForkRequest(session))
		a.dispatch(func() {
			if actionSeq != atomic.LoadInt64(&a.historyPanel.ActionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			if err != nil {
				a.setSessionActionStatus("Fork 失败：" + err.Error())
				return
			}
			var result sessionResult
			if err := json.Unmarshal(raw, &result); err != nil || result.SessionID == "" {
				a.setSessionActionStatus("Fork 响应无效")
				return
			}
			a.setSessionActionStatus("已创建 Fork 会话")
			a.closeHistory()
			a.selectSession(listedSession{
				SessionID: result.SessionID,
				Cwd:       session.Cwd,
			})
		})
	}()
}

// chooseHistoryWorkDir opens the Native folder picker and moves the idle
// session through mothx/session/setWorkDir. Directory authorization remains a
// Runtime decision; an out-of-window pick surfaces as a server error.
func (a *nativeApp) chooseHistoryWorkDir(session listedSession) {
	if !a.historyWorkDirCapable() {
		return
	}
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择会话工作目录"),
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() { a.runHistoryWorkDir(session, paths[0]) })
	}()
}

func (a *nativeApp) runHistoryWorkDir(session listedSession, cwd string) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return
	}
	client := a.acp
	p := &a.historyPanel
	if client == nil {
		p.Status = "尚未连接到 MothX ACP。"
		return
	}
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/session/setWorkDir", buildSessionWorkDirRequest(session, cwd))
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			if err != nil {
				a.setSessionActionStatus("修改工作目录失败：" + err.Error())
				return
			}
			resolved := cwd
			var result struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(raw, &result) == nil && strings.TrimSpace(result.Cwd) != "" {
				resolved = result.Cwd
			}
			a.updateSessionCwdLocal(session.SessionID, resolved)
			a.setSessionActionStatus("工作目录已更新为 " + resolved)
			a.refreshTaskLibrary()
		})
	}()
}

// --- project dialog actions -------------------------------------------------

const (
	projectDialogCreate = "create"
	projectDialogRename = "rename"
	projectDialogDelete = "delete"
)

func (a *nativeApp) openProjectDialog(kind, projectID string) {
	a.openProjectDialogForSession(kind, projectID, "")
}

// openProjectDialogForSession opens the project create/rename/delete dialog.
// When sessionID is set for create, the new project is assigned to that
// session after creation instead of requiring a second menu step.
func (a *nativeApp) openProjectDialogForSession(kind, projectID, sessionID string) {
	if !a.historyProjectsCapable() {
		return
	}
	p := &a.historyPanel
	p.ProjectDialogOpen = true
	p.ProjectDialogKind = kind
	p.ProjectDialogID = projectID
	p.ProjectDialogSession = strings.TrimSpace(sessionID)
	p.ProjectDialogName = ""
	p.ProjectDialogErr = ""
	p.ProjectDialogBusy = false
	if kind == projectDialogRename {
		if project, ok := a.historyProjectByID(projectID); ok {
			p.ProjectDialogName = project.Name
		}
	}
	if kind == projectDialogDelete {
		if project, ok := a.historyProjectByID(projectID); ok {
			p.ProjectDialogName = project.Name
		}
	}
}

func (a *nativeApp) closeProjectDialog() {
	p := &a.historyPanel
	atomic.AddInt64(&p.ActionSeq, 1)
	p.ProjectDialogOpen = false
	p.ProjectDialogKind = ""
	p.ProjectDialogID = ""
	p.ProjectDialogName = ""
	p.ProjectDialogSession = ""
	p.ProjectDialogBusy = false
	p.ProjectDialogErr = ""
}

func (a *nativeApp) confirmProjectDialog() {
	p := &a.historyPanel
	if p.ProjectDialogBusy {
		return
	}
	client := a.acp
	if client == nil {
		p.ProjectDialogErr = "尚未连接到 MothX ACP。"
		return
	}
	var method string
	var request map[string]any
	switch p.ProjectDialogKind {
	case projectDialogCreate:
		name := strings.TrimSpace(p.ProjectDialogName)
		if name == "" {
			p.ProjectDialogErr = "项目名称不能为空。"
			return
		}
		method = "mothx/projects/create"
		request = buildProjectRequest("", name)
	case projectDialogRename:
		name := strings.TrimSpace(p.ProjectDialogName)
		if name == "" {
			p.ProjectDialogErr = "项目名称不能为空。"
			return
		}
		method = "mothx/projects/rename"
		request = buildProjectRequest(p.ProjectDialogID, name)
	case projectDialogDelete:
		if strings.TrimSpace(p.ProjectDialogID) == "" {
			p.ProjectDialogErr = "项目不存在。"
			return
		}
		method = "mothx/projects/delete"
		request = map[string]any{"id": p.ProjectDialogID}
	default:
		return
	}
	actionSeq := atomic.AddInt64(&p.ActionSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	p.ProjectDialogBusy = true
	p.ProjectDialogErr = ""
	kind := p.ProjectDialogKind
	deletedID := ""
	if kind == projectDialogDelete {
		deletedID = strings.TrimSpace(p.ProjectDialogID)
	}
	assignSessionID := p.ProjectDialogSession
	go func() {
		raw, err := client.call(method, request)
		a.dispatch(func() {
			if !a.historyActionCurrent(actionSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			p := &a.historyPanel
			p.ProjectDialogBusy = false
			if err != nil {
				p.ProjectDialogErr = "项目操作失败：" + err.Error()
				return
			}
			p.ProjectDialogOpen = false
			createdProjectID := ""
			switch kind {
			case projectDialogCreate:
				p.Status = "项目已创建"
				var created projectView
				if json.Unmarshal(raw, &created) == nil && created.ID != "" {
					createdProjectID = created.ID
					a.upsertLocalProject(created)
				}
			case projectDialogRename:
				p.Status = "项目已重命名"
				a.renameLocalProject(p.ProjectDialogID, p.ProjectDialogName)
			case projectDialogDelete:
				p.Status = "项目已删除"
				a.removeLocalProject(deletedID)
				// The deleted project can no longer be the active filter.
				if p.Scope == "project" && p.ProjectID == deletedID {
					p.Scope = "all"
					p.ProjectID = ""
					p.Sessions = nil
					p.Loaded = false
				}
			}
			a.setSessionActionStatus(p.Status)
			a.loadHistoryProjects()
			if a.historyOpen {
				a.startHistoryLoad(false)
			}
			a.refreshTaskLibrary()
			if createdProjectID != "" && assignSessionID != "" {
				if session, ok := a.sessionByID(assignSessionID); ok {
					a.runHistoryProjectAssignment(session, createdProjectID)
				}
			}
		})
	}()
}

// historyEmptyReason explains an empty projection without pretending the
// server failed.
func (a *nativeApp) historyEmptyReason() string {
	p := &a.historyPanel
	if strings.TrimSpace(p.AppliedQuery) != "" {
		return "没有匹配的会话"
	}
	switch p.Scope {
	case "ungrouped":
		return "没有未归类的会话"
	case "project":
		if project, ok := a.historyProjectByID(p.ProjectID); ok {
			return Tf("项目「%s」下没有会话", project.Name)
		}
		return "该项目下没有会话"
	default:
		return "还没有会话"
	}
}

// worktreeStatusLabel project the last mothx/worktree/status value for a
// directory. The status is advisory; the Runtime remains the authority.
func (a *nativeApp) worktreeStatusLabel(directory string) string {
	return strings.TrimSpace(a.historyPanel.Worktrees[directory])
}
