package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
)

// This file owns the Native sidebar's session pagination. The Runtime remains
// the canonical session catalog: each page is an ACP projection obtained with
// cursor + scope, and the client only merges loaded pages into the visible
// task tree. Recent stays intentionally bounded, while every project branch and
// the ungrouped branch can load further pages instead of dropping session 51+.

const (
	sessionPageAll       = "all"
	sessionPageUngrouped = "ungrouped"
)

type sessionPageStatus struct {
	Loaded     bool
	Loading    bool
	NextCursor string
	Err        string
}

type sessionPage struct {
	Key       string
	Scope     string
	ProjectID string
	Sessions  []listedSession
	sessionPageStatus
}

func newSessionPage(key, scope, projectID string) sessionPage {
	return sessionPage{Key: key, Scope: scope, ProjectID: projectID}
}

func sessionProjectPageKey(projectID string) string {
	return "project:" + strings.TrimSpace(projectID)
}

func normalizeSessionPageScope(scope, projectID string) (string, string) {
	scope, projectID = normalizeHistoryScope(scope, projectID)
	return scope, projectID
}

func sessionPageKey(scope, projectID string) string {
	scope, projectID = normalizeSessionPageScope(scope, projectID)
	if scope == "project" {
		return sessionProjectPageKey(projectID)
	}
	return scope
}

// mergeSessionRows appends rows whose ID has not been seen. It returns the
// combined catalog and the number of rows added, without mutating either input.
func mergeSessionRows(base, added []listedSession) ([]listedSession, int) {
	seen := make(map[string]bool, len(base)+len(added))
	out := make([]listedSession, 0, len(base)+len(added))
	for _, session := range base {
		id := strings.TrimSpace(session.SessionID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, session)
	}
	before := len(out)
	for _, session := range added {
		id := strings.TrimSpace(session.SessionID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, session)
	}
	return out, len(out) - before
}

// rebuildSessionCatalog projects the union of every loaded page. This is what
// keeps sessionByID and sidebar rows complete as project/ungrouped pages grow;
// the canonical ordering remains the Runtime's updatedAt projection.
func (a *nativeApp) rebuildSessionCatalog() {
	keys := make([]string, 0, len(a.sessionPages))
	for key := range a.sessionPages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var merged []listedSession
	for _, key := range keys {
		page := a.sessionPages[key]
		merged, _ = mergeSessionRows(merged, page.Sessions)
	}
	sortTaskSessions(merged)
	a.sessions = merged
}

func (a *nativeApp) sessionPageByKey(key string) sessionPage {
	if a.sessionPages == nil {
		return sessionPage{}
	}
	return a.sessionPages[key]
}

func (a *nativeApp) setSessionPage(page sessionPage) {
	if a.sessionPages == nil {
		a.sessionPages = make(map[string]sessionPage)
	}
	a.sessionPages[page.Key] = page
}

// resetSessionPages clears every derived page when the top-level all page is
// replaced. In-flight page loads are invalidated through the catalog sequence.
func (a *nativeApp) resetSessionPages() {
	atomic.AddInt64(&a.sessionCatalogSeq, 1)
	a.sessionPages = make(map[string]sessionPage)
}

// startFirstSidebarPages begins the auxiliary first pages after the all page
// loaded. Projects are loaded lazily on expand; ungrouped loads eagerly so the
// sidebar knows whether a "load more" row exists.
func (a *nativeApp) startFirstSidebarPages(features map[string]bool) {
	if !(features["sessionListAll"] || len(features) == 0) {
		return
	}
	go a.loadSessionScopePage(sessionPageUngrouped, "ungrouped", "", "", false)
}

// ensureUngroupedPageLoaded is used when the sidebar renders and no ungrouped
// page was requested yet (for example after a reconnect).
func (a *nativeApp) ensureUngroupedPageLoaded() {
	if !a.featureEnabled("sessionListAll") {
		return
	}
	page, ok := a.sessionPages[sessionPageUngrouped]
	if ok && (page.Loaded || page.Loading) {
		return
	}
	go a.loadSessionScopePage(sessionPageUngrouped, "ungrouped", "", "", false)
}

// ensureProjectPageLoaded loads the first project page when a branch is
// expanded. A branch that was loaded once stays cached until the next refresh.
func (a *nativeApp) ensureProjectPageLoaded(projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || !a.featureEnabled("sessionListAll") {
		return
	}
	key := sessionProjectPageKey(projectID)
	if page, ok := a.sessionPages[key]; ok && (page.Loaded || page.Loading) {
		return
	}
	go a.loadSessionScopePage(key, "project", projectID, "", false)
}

func (a *nativeApp) loadMoreUngroupedSessions() {
	page := a.sessionPageByKey(sessionPageUngrouped)
	if page.Loading || strings.TrimSpace(page.NextCursor) == "" {
		return
	}
	go a.loadSessionScopePage(sessionPageUngrouped, "ungrouped", "", page.NextCursor, true)
}

func (a *nativeApp) loadMoreProjectSessions(projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}
	key := sessionProjectPageKey(projectID)
	page := a.sessionPageByKey(key)
	if page.Loading || strings.TrimSpace(page.NextCursor) == "" {
		return
	}
	go a.loadSessionScopePage(key, "project", projectID, page.NextCursor, true)
}

// loadSessionScopePage fetches one listAll page. The UI state is updated on the
// main thread; failures stay scoped to their branch so one project cannot make
// the whole task tree unusable.
func (a *nativeApp) loadSessionScopePage(key, scope, projectID, cursor string, appendPage bool) {
	client := a.acp
	if client == nil {
		return
	}
	scope, projectID = normalizeSessionPageScope(scope, projectID)
	key = sessionPageKey(scope, projectID)
	if _, loaded := a.sessionPageInFlight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	defer a.sessionPageInFlight.Delete(key)
	seq := atomic.LoadInt64(&a.sessionCatalogSeq)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.dispatch(func() {
		page := a.sessionPages[key]
		if page.Key == "" {
			page = newSessionPage(key, scope, projectID)
		}
		if page.Loading {
			return
		}
		page.Loading = true
		page.Err = ""
		a.setSessionPage(page)
	})
	params := buildHistoryListRequest(scope, projectID, "", cursor)
	raw, err := client.call("mothx/session/listAll", params)
	a.dispatch(func() {
		if seq != atomic.LoadInt64(&a.sessionCatalogSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		page := a.sessionPages[key]
		if page.Key == "" {
			page = newSessionPage(key, scope, projectID)
		}
		page.Loading = false
		if err != nil {
			page.Err = "读取会话分页失败：" + err.Error()
			a.setSessionPage(page)
			return
		}
		var result sessionListResult
		if err := json.Unmarshal(raw, &result); err != nil {
			page.Err = "会话分页响应无效：" + err.Error()
			a.setSessionPage(page)
			return
		}
		a.applySessionScopePage(page, result, appendPage)
	})
}

// applySessionScopePage merges one decoded listAll page into its branch and
// rebuilds the visible catalog. It is separated from the transport closure so
// pagination behaviour across the server's fixed page size stays testable.
func (a *nativeApp) applySessionScopePage(page sessionPage, result sessionListResult, appendPage bool) {
	page.Loading = false
	if appendPage {
		page.Sessions, _ = mergeSessionRows(page.Sessions, result.Sessions)
	} else {
		page.Sessions = result.Sessions
	}
	page.NextCursor = result.NextCursor
	page.Loaded = true
	page.Err = ""
	a.setSessionPage(page)
	a.rebuildSessionCatalog()
	a.sessionsLoaded = true
	a.sessionsErr = ""
}
