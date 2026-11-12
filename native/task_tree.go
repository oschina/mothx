package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// This file owns the Native task-tree projection shared by the sidebar and the
// history view. Sessions and projects are Runtime-owned ACP facts; the client
// only groups the projected rows, filters them for display, and decides which
// project branches are expanded in this window.

const sidebarRecentLimit = 8

type sidebarProjectBranch struct {
	Project  projectView
	Sessions []listedSession
	Page     sessionPageStatus
}

type sidebarTaskTree struct {
	Projects      []sidebarProjectBranch
	Recent        []listedSession
	Ungrouped     []listedSession
	UngroupedPage sessionPageStatus
}

func pageStatusOf(page sessionPage) sessionPageStatus {
	return sessionPageStatus{
		Loaded:     page.Loaded,
		Loading:    page.Loading,
		NextCursor: page.NextCursor,
		Err:        page.Err,
	}
}

// buildSidebarTaskTree projects the Desktop task tree: every project branch,
// a bounded recent list, and the sessions without a known project. Sessions in
// a project also stay in Recent, matching Desktop's recentSessions projection.
func buildSidebarTaskTree(sessions []listedSession, projects []projectView, query string, recentLimit int) sidebarTaskTree {
	projectNames := make(map[string]string, len(projects))
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	term := strings.ToLower(strings.TrimSpace(query))
	matches := func(session listedSession) bool {
		if term == "" {
			return true
		}
		haystack := session.SessionID + " " + session.Title + " " + session.Cwd
		if projectID := sessionProjectID(session); projectID != "" {
			haystack += " " + projectNames[projectID]
		}
		return strings.Contains(strings.ToLower(haystack), term)
	}

	filtered := make([]listedSession, 0, len(sessions))
	for _, session := range sessions {
		if matches(session) {
			filtered = append(filtered, session)
		}
	}
	sortTaskSessions(filtered)

	tree := sidebarTaskTree{Recent: filtered}
	if recentLimit >= 0 && len(tree.Recent) > recentLimit {
		tree.Recent = tree.Recent[:recentLimit]
	}

	byProject := make(map[string][]listedSession, len(projects))
	knownProjects := make(map[string]bool, len(projects))
	for _, project := range projects {
		knownProjects[project.ID] = true
	}
	for _, session := range filtered {
		projectID := sessionProjectID(session)
		if projectID != "" && knownProjects[projectID] {
			byProject[projectID] = append(byProject[projectID], session)
			continue
		}
		tree.Ungrouped = append(tree.Ungrouped, session)
	}

	tree.Projects = make([]sidebarProjectBranch, 0, len(projects))
	for _, project := range projects {
		branchSessions := byProject[project.ID]
		sortTaskSessions(branchSessions)
		tree.Projects = append(tree.Projects, sidebarProjectBranch{
			Project:  project,
			Sessions: branchSessions,
		})
	}
	return tree
}

func sortTaskSessions(sessions []listedSession) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt == sessions[j].UpdatedAt {
			return sessions[i].SessionID < sessions[j].SessionID
		}
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
}

func (a *nativeApp) taskTree() sidebarTaskTree {
	tree := buildSidebarTaskTree(a.sessions, a.projects, a.search, sidebarRecentLimit)
	for i := range tree.Projects {
		page := a.sessionPageByKey(sessionProjectPageKey(tree.Projects[i].Project.ID))
		tree.Projects[i].Page = pageStatusOf(page)
	}
	tree.UngroupedPage = pageStatusOf(a.sessionPageByKey(sessionPageUngrouped))
	return tree
}

func (a *nativeApp) projectByID(id string) (projectView, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return projectView{}, false
	}
	for _, project := range a.projects {
		if project.ID == id {
			return project, true
		}
	}
	return projectView{}, false
}

func (a *nativeApp) projectName(id string) string {
	if project, ok := a.projectByID(id); ok && project.Name != "" {
		return project.Name
	}
	return strings.TrimSpace(id)
}

func (a *nativeApp) upsertLocalProject(project projectView) {
	if strings.TrimSpace(project.ID) == "" {
		return
	}
	for i := range a.projects {
		if a.projects[i].ID == project.ID {
			a.projects[i] = project
			return
		}
	}
	a.projects = append(a.projects, project)
}

func (a *nativeApp) renameLocalProject(id, name string) {
	for i := range a.projects {
		if a.projects[i].ID == id {
			a.projects[i].Name = name
			return
		}
	}
}

func (a *nativeApp) removeLocalProject(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	projects := a.projects[:0]
	for _, project := range a.projects {
		if project.ID != id {
			projects = append(projects, project)
		}
	}
	a.projects = projects
	clearProjectID := func(sessions []listedSession) {
		for i := range sessions {
			if sessionProjectID(sessions[i]) == id {
				sessions[i].Meta.ProjectID = nil
			}
		}
	}
	clearProjectID(a.sessions)
	clearProjectID(a.historyPanel.Sessions)
}

// currentSessionStatusLabel is the live status projected for the chat header.
// It never replaces the canonical Run state; it only chooses the label for the
// same lastRun/live projection used by the sidebar.
func (a *nativeApp) currentSessionStatusLabel() string {
	if session, ok := a.sessionByID(a.currentSession); ok {
		return historySessionStatus(session)
	}
	if a.currentSession != "" {
		if a.running {
			return "运行中"
		}
		return "空闲"
	}
	return ""
}

// backgroundAttentionCount projects non-current sessions whose canonical last
// run is waiting for an approval/question. It is a display hint only; the
// sidebar still renders the canonical per-session status.
func (a *nativeApp) backgroundAttentionCount() int {
	count := 0
	for _, session := range a.sessions {
		if session.SessionID == a.currentSession {
			continue
		}
		last := session.Meta.LastRun
		if last == nil {
			continue
		}
		switch last.Status {
		case "waiting_for_approval", "waiting_for_question", "pending":
			count++
		}
	}
	return count
}

// setSessionActionStatus surfaces a session/project action in both the chat
// shell and the history view. It never fabricates a canonical session state.
func (a *nativeApp) setSessionActionStatus(message string) {
	a.status = message
	a.historyPanel.Status = message
	if message != "" {
		a.showToast(message)
	}
}

// sessionByID resolves one projected session without inventing a row. The
// sidebar catalog is the primary projection; the history page is consulted
// while the task tree is still loading.
func (a *nativeApp) sessionByID(id string) (listedSession, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return listedSession{}, false
	}
	for _, session := range a.sessions {
		if session.SessionID == id {
			return session, true
		}
	}
	for _, session := range a.historyPanel.Sessions {
		if session.SessionID == id {
			return session, true
		}
	}
	return listedSession{}, false
}

// sessionRunActive reports whether the canonical last-run projection says the
// session is still executing. The current session also counts when the live
// shell guard is set, because the list projection can lag one refresh behind.
func (a *nativeApp) sessionRunActive(session listedSession) bool {
	if session.SessionID == a.currentSession && a.running {
		return true
	}
	last := session.Meta.LastRun
	if last == nil {
		return false
	}
	if last.Active {
		return true
	}
	switch last.Status {
	case "created", "queued", "running", "cancelling", "terminalizing",
		"planning", "waiting_for_approval", "waiting_for_question":
		return true
	default:
		return false
	}
}

// sessionStatusKind returns the Desktop dot/status vocabulary for a session.
func (a *nativeApp) sessionStatusKind(session listedSession) string {
	if session.SessionID == a.currentSession && a.running {
		return "working"
	}
	last := session.Meta.LastRun
	if last == nil {
		return "idle"
	}
	status := last.Status
	if last.Active && status == "" {
		return "working"
	}
	switch status {
	case "created", "queued", "running", "cancelling", "terminalizing", "planning", "working":
		return "working"
	case "waiting_for_approval", "waiting_for_question", "pending":
		return "pending"
	case "completed":
		return "completed"
	case "incomplete":
		return "incomplete"
	case "failed", "expired", "timed_out":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "idle"
	}
}

// refreshProjects reloads the shared project catalog. Responses are guarded by
// a sequence and the connection generation so a reconnect cannot project a
// stale catalog over the new one.
func (a *nativeApp) refreshProjects() {
	a.refreshProjectsWith(a.featureSnapshot())
}

// refreshProjectsWith accepts the caller's capability snapshot so the first
// refresh after initialize does not race the UI-thread feature update.
func (a *nativeApp) refreshProjectsWith(features map[string]bool) {
	if !features["projects"] {
		a.projects = nil
		a.projectsLoaded = true
		a.projectsErr = ""
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	seq := atomic.AddInt64(&a.projectsSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/projects/list", map[string]any{})
		a.dispatch(func() {
			if seq != atomic.LoadInt64(&a.projectsSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.projectsLoaded = true
			if err != nil {
				a.projectsErr = "读取项目失败：" + err.Error()
				return
			}
			var result projectListResult
			if err := json.Unmarshal(raw, &result); err != nil {
				a.projectsErr = "项目响应无效：" + err.Error()
				return
			}
			a.projects = result.Projects
			a.projectsErr = ""
		})
	}()
}

// refreshTaskLibrary refreshes both projections after a mutation. It does not
// replace the caller's current page state; the history view keeps its own
// paging sequence and refreshes explicitly when it is visible.
func (a *nativeApp) refreshTaskLibrary() {
	client := a.acp
	if client == nil {
		return
	}
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	go a.refreshSessions(client, cwd, a.featureSnapshot())
}
