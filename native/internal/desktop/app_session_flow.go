package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
)

func (a *nativeApp) newChat() {
	if a.creatingSession {
		a.status = "请等待当前会话创建完成"
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.saveActiveSession()
	a.invalidateRouterPanel()
	a.invalidateSubagentPanel()
	a.closeArtifactPreview()
	a.page = ""
	a.clearSessionState("")
	a.currentTitle = ""
	a.draft = ""
	a.attachments = nil
	a.draftConfig = make(map[string]string)
	a.resetTranscript()
	// Desktop's New Task draft has no inherited directory: it stays unset
	// until the native picker (or a worktree action) supplies one, and send
	// asks again if it is still empty. Only the launch quick-start seeds a
	// scratch directory without asking.
	a.draftSource = "new-task"
	a.workspace = ""
	a.draftCwd = ""
	go a.refreshDraftConfig(a.client(), "", a.featureSnapshot())
}

// startNewTask mirrors Desktop's New Task: reset to an unset draft and ask
// for the directory immediately; cancelling leaves it unset for send time.
func (a *nativeApp) startNewTask() {
	a.newChat()
	a.requestDraftDirectory(func(picked string) {
		if picked == "" {
			return
		}
		a.applyWorkspaceChoice(picked)
	})
}

// requestDraftDirectory opens the native directory picker once at a time and
// delivers the choice (or "" on cancel) on the UI thread. With no window it
// resolves to cancel so headless tests stay deterministic.
func (a *nativeApp) requestDraftDirectory(onPick func(string)) {
	if a.pickingDraftDir {
		return
	}
	if a.win == nil {
		onPick("")
		return
	}
	a.pickingDraftDir = true
	win := a.win
	defaultPath := a.lastWorkspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择新会话工作区"),
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		picked := ""
		if err == nil && len(paths) > 0 {
			picked = paths[0]
		}
		a.dispatch(func() {
			a.pickingDraftDir = false
			onPick(picked)
		})
	}()
}

func (a *nativeApp) newChatIn(cwd string) {
	if a.creatingSession {
		a.status = "请等待当前会话创建完成"
		return
	}
	a.newChat()
	if cwd != "" {
		a.recordWorkspace(cwd)
		a.workspace = cwd
		a.draftCwd = cwd
		a.draftSource = "selected"
		go a.refreshDraftConfig(a.client(), cwd, a.featureSnapshot())
	}
}

func (a *nativeApp) chooseWorkspace() {
	// An open session owns its cwd: the picker changes that session through
	// the canonical setWorkDir path. Only a draft shell changes the directory
	// for the next new session; it must never silently replace the chat.
	if a.currentSession != "" {
		session, ok := a.sessionByID(a.currentSession)
		if !ok {
			session = listedSession{SessionID: a.currentSession, Cwd: a.workspace}
		} else if a.sessionRunActive(session) {
			a.status = "该会话正在运行，请先停止"
			return
		}
		if !a.historyWorkDirCapable() {
			a.status = "当前运行时不支持修改会话工作目录"
			return
		}
		a.chooseHistoryWorkDir(session)
		return
	}
	win := a.win
	defaultPath := a.lastWorkspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择新会话工作区"),
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.dispatch(func() {
			a.applyWorkspaceChoice(path)
		})
	}()
}

// applyWorkspaceChoice applies one explicitly picked directory with the same
// semantics as the native picker: an open session changes its canonical cwd
// through setWorkDir; a draft only changes the next new-session directory.
// Recent-directory menu entries reuse this path instead of forking it.
func (a *nativeApp) applyWorkspaceChoice(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if a.currentSession != "" {
		session, ok := a.sessionByID(a.currentSession)
		if !ok {
			session = listedSession{SessionID: a.currentSession, Cwd: a.workspace}
		} else if a.sessionRunActive(session) {
			a.status = "该会话正在运行，请先停止"
			return
		}
		if !a.historyWorkDirCapable() {
			a.status = "当前运行时不支持修改会话工作目录"
			return
		}
		a.recordWorkspace(path)
		a.runHistoryWorkDir(session, path)
		return
	}
	a.recordWorkspace(path)
	a.workspace = path
	a.draftCwd = path
	a.draftSource = "selected"
	a.status = "新会话工作区已切换"
	go a.refreshDraftConfig(a.client(), path, a.featureSnapshot())
}

func (a *nativeApp) selectSession(s listedSession) {
	if a.currentSession == s.SessionID {
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.invalidateRouterPanel()
	a.invalidateSubagentPanel()
	a.closeArtifactPreview()
	a.page = ""
	a.selectSessionState(s.SessionID)
	a.loadingSession = s.SessionID
	a.currentTitle = s.Title
	a.workspace = s.Cwd
	a.draftConfig = make(map[string]string)
	if a.draftCwd == "" {
		a.draftCwd = s.Cwd
	}
	if !a.running {
		a.status = "正在载入会话…"
	} else {
		a.status = "该会话正在运行…"
	}
	client := a.client()
	if client == nil {
		a.loadingSession = ""
		a.running = false
		a.status = ""
		a.settleWorktreeOpen()
		return
	}
	seq := atomic.AddInt64(&a.sessionLoadSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	features := a.featureSnapshot()
	params := map[string]any{
		"sessionId": s.SessionID,
		"cwd":       s.Cwd,
	}
	if a.transcriptPagingEnabled() || features["sessionHistoryPaging"] {
		params["historyLimit"] = transcriptPageSize
	}
	go func() {
		raw, err := client.Call("session/load", params)
		if err != nil {
			a.dispatch(func() { a.failSessionLoad(s.SessionID, err, seq, startReloadSeq) })
			return
		}
		var res sessionResult
		if err := json.Unmarshal(raw, &res); err != nil {
			a.dispatch(func() {
				a.failSessionLoad(s.SessionID, fmt.Errorf("会话响应无效：%w", err), seq, startReloadSeq)
			})
			return
		}
		a.dispatch(func() { a.finishSessionLoad(s.SessionID, res, s.Title, seq, startReloadSeq) })
		go a.refreshSessions(client, s.Cwd, features)
	}()
}

// sessionLoadCurrent reports whether a session/load response still belongs to
// the visible load. A newer selection, reconnect, or a failed load of a session
// the user already left must not mutate the current shell.
func (a *nativeApp) sessionLoadCurrent(sessionID string, seq, startReloadSeq int64) bool {
	if seq != atomic.LoadInt64(&a.sessionLoadSeq) {
		return false
	}
	if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
		return false
	}
	return a.currentSession == sessionID && a.loadingSession == sessionID
}

// finishSessionLoad applies a completed session/load and then gives a deferred
// worktree open intent the chance to run now that loadingSession is clear.
func (a *nativeApp) finishSessionLoad(sessionID string, res sessionResult, title string, seq, startReloadSeq int64) {
	if !a.sessionLoadCurrent(sessionID, seq, startReloadSeq) {
		return
	}
	a.loadingSession = ""
	a.status = ""
	busy := false
	if res.Execution != nil {
		busy = res.Execution.Busy
	} else if session, ok := a.sessionByID(sessionID); ok {
		busy = a.sessionRunActive(session)
	}
	a.running = busy
	a.applySessionResult(res)
	if busy {
		a.status = "该会话正在运行…"
		for i := 0; i < len(a.messages)-1; i++ {
			a.messages[i].Done = true
		}
	} else {
		for _, m := range a.messages {
			m.Done = true
		}
	}
	if a.currentTitle == "" && res.SessionID != "" {
		a.currentTitle = title
	}
	a.refreshSubagentRoster(sessionID)
	a.settleWorktreeOpen()
}

// failSessionLoad restores the idle shell after a failed load. A stale failure
// for a session the user already left only reports a toast; it never clears the
// visible session or reuses that session's cached transcript in a new draft.
// The deferred worktree intent is consumed first so its new session does not
// hide the error.
func (a *nativeApp) failSessionLoad(sessionID string, err error, seq, startReloadSeq int64) {
	if seq != atomic.LoadInt64(&a.sessionLoadSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
		a.showErrorToast("载入会话失败：" + err.Error())
		return
	}
	if a.currentSession != sessionID && a.loadingSession != sessionID {
		a.showErrorToast("载入会话失败：" + err.Error())
		return
	}
	// Drop the failed session's cached projection so selecting it again
	// reloads canonically instead of reviving a stale transcript in a draft.
	if a.sessionStates != nil {
		delete(a.sessionStates, sessionID)
	}
	a.clearSessionState("")
	a.currentTitle = ""
	a.status = ""
	a.settleWorktreeOpen()
	a.appendErrorFromErr("载入会话失败：", err)
}

// cancelSessionRun stops one projected session, active or background. The
// Runtime decides whether the cancel is accepted; the client only forwards the
// canonical session/cancel request and projects the immediate status.
func (a *nativeApp) cancelSessionRun(sessionID string) {
	client := a.client()
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	a.setSessionRunLocal(sessionID, "cancelling", true)
	if a.currentSession == sessionID {
		a.status = "正在停止…"
	}
	go func() {
		_, _ = client.Call("session/cancel", map[string]any{"sessionId": sessionID})
	}()
}

func (a *nativeApp) stop() {
	if !a.running {
		return
	}
	client := a.client()
	sessionID := a.currentSession
	a.status = "正在停止…"
	if client != nil && sessionID != "" {
		go func() {
			_, _ = client.Call("session/cancel", map[string]any{"sessionId": sessionID})
		}()
		return
	}
	a.markRunDone("cancelled")
}

// finishSessionCreation clears the transient create guard and consumes a
// deferred worktree open intent now that the shell can accept a new chat.
func (a *nativeApp) finishSessionCreation() {
	a.creatingSession = false
	a.settleWorktreeOpen()
}
