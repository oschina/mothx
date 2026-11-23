package desktop

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

func (a *nativeApp) refreshDraftConfig(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	if strings.TrimSpace(cwd) == "" {
		// An unset New Task draft has no cwd for draft-config-options; Desktop
		// still projects the provider/model menu from the provider catalog.
		a.refreshDraftProviderModelOptions(client, features)
		return
	}
	if len(features) > 0 && !features["sessionDraftConfigOptions"] {
		return
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	raw, err := client.Call("mothx/session/draft-config-options", map[string]any{"cwd": cwd})
	if err != nil {
		return
	}
	var res draftConfigResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	a.dispatch(func() {
		selectedCwd := a.workspace
		if selectedCwd == "" {
			selectedCwd = a.draftCwd
		}
		if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) || a.currentSession != "" || selectedCwd != cwd || len(res.ConfigOptions) == 0 {
			return
		}
		a.configOptions = res.ConfigOptions
		for configID, value := range a.draftConfig {
			if option := a.configOptionByID(configID); option != nil {
				option.CurrentValue = value
			}
		}
		a.syncModelState()
		if provider := a.draftConfig["provider"]; provider != "" {
			a.refreshDraftProviderModels(provider)
		}
	})
}

func (a *nativeApp) refreshSessions(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	// The task tree is one projection: keep the project catalog in step with
	// every session refresh, including refreshes that the sidebar coalesces.
	go a.refreshProjectsWith(features)
	if a.refreshing.Swap(true) {
		// Do not silently drop a refresh triggered by a newer connection or
		// run. The active refresh re-runs one queued refresh with the
		// connection that exists when it finishes.
		a.sessionRefreshPending.Store(true)
		return
	}
	defer func() {
		a.refreshing.Store(false)
		if a.sessionRefreshPending.Swap(false) {
			a.dispatch(func() {
				if a.closing.Load() {
					return
				}
				current := a.client()
				if current == nil {
					return
				}
				pendingCwd := a.workspace
				if pendingCwd == "" {
					pendingCwd = a.cwd
				}
				go a.refreshSessions(current, pendingCwd, a.featureSnapshot())
			})
		}
	}()
	// Capture the connection generation before the request so a response from
	// an ACP child that has since been replaced can never overwrite the
	// catalog of the new one.
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if cwd == "" {
		cwd = a.cwd
	}
	var raw json.RawMessage
	var err error
	if features["sessionListAll"] || len(features) == 0 {
		raw, err = client.Call("mothx/session/listAll", map[string]any{"scope": "all", "limit": sidebarSessionPageSize})
	}
	if err != nil || len(raw) == 0 {
		raw, err = client.Call("session/list", map[string]any{"cwd": cwd})
	}
	if err != nil {
		a.dispatch(func() {
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	var res sessionListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		a.dispatch(func() {
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	allPage := newSessionPage(sessionPageAll, sessionPageAll, "")
	allPage.Sessions = res.Sessions
	allPage.NextCursor = res.NextCursor
	allPage.Loaded = true
	a.dispatch(func() {
		if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		a.resetSessionPages()
		a.setSessionPage(allPage)
		a.rebuildSessionCatalog()
		a.sessionsErr = ""
		a.sessionsLoaded = true
		for _, listed := range res.Sessions {
			if listed.SessionID != a.currentSession {
				continue
			}
			a.workspace = listed.Cwd
			if a.draftCwd == "" {
				a.draftCwd = listed.Cwd
			}
			if a.currentTitle == "" || a.currentTitle == T("新会话") {
				a.currentTitle = listed.Title
			}
		}
		a.startFirstSidebarPages(features)
	})
}
