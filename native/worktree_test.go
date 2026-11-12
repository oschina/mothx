package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func worktreeStatusEvent(id, directory, status, errText string) worktreeStatusPayload {
	var payload worktreeStatusPayload
	payload.Worktree.ID = id
	payload.Worktree.Directory = directory
	payload.Worktree.Status = status
	payload.Worktree.Error = errText
	return payload
}

func worktreeTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{"worktrees": true, "worktreeReset": true})
	a.workspace = "/repo/main"
	a.worktree = newWorktreePanelState()
	a.worktree.Open = true
	a.worktree.BaseCwd = "/repo/main"
	a.worktree.Loaded = true
	a.worktree.Worktrees = []worktreeView{
		{ID: "w1", Name: "feature-login", Branch: "mothx/feature-login", Directory: "/repo/wt/login", Status: "ready"},
		{ID: "w2", Directory: "/repo/wt/join", Branch: "mothx/join", Status: "pending"},
		{Directory: "/repo/external", Status: "ready", External: true},
	}
	return a
}

func TestWorktreeCreateRequest(t *testing.T) {
	request := buildWorktreeCreateRequest(" /repo/main ", " feature ")
	if request["baseCwd"] != "/repo/main" || request["name"] != "feature" {
		t.Fatalf("create request = %#v", request)
	}
	request = buildWorktreeCreateRequest("/repo/main", "  ")
	if _, exists := request["name"]; exists {
		t.Fatalf("empty name must stay absent: %#v", request)
	}
}

func TestWorktreePanelStatusProjection(t *testing.T) {
	a := worktreeTestApp(t)
	payload := worktreeStatusPayload{}
	payload.Worktree.Directory = " /repo/wt/join "
	payload.Worktree.Status = "ready"
	payload.Worktree.Branch = "mothx/join"
	a.applyWorktreeStatus(payload)
	if a.worktree.Worktrees[1].Status != "ready" {
		t.Fatalf("status not folded: %+v", a.worktree.Worktrees[1])
	}
	if a.historyPanel.Worktrees["/repo/wt/join"] != "ready" {
		t.Fatalf("history status hint missing: %#v", a.historyPanel.Worktrees)
	}

	payload.Worktree.Directory = "/repo/wt/fresh"
	payload.Worktree.Status = "failed"
	payload.Worktree.Error = "git failed"
	a.applyWorktreeStatus(payload)
	found := false
	for _, tree := range a.worktree.Worktrees {
		if tree.Directory == "/repo/wt/fresh" {
			found = true
			if tree.Status != "failed" || tree.Error != "git failed" {
				t.Fatalf("new worktree = %+v", tree)
			}
		}
	}
	if !found {
		t.Fatalf("open panel must adopt an unknown worktree event: %+v", a.worktree.Worktrees)
	}
}

func TestWorktreeReadinessGate(t *testing.T) {
	if !worktreeReady(worktreeView{Status: "ready"}) || !worktreeReady(worktreeView{Status: ""}) {
		t.Fatalf("ready/legacy worktrees must be usable")
	}
	if worktreeReady(worktreeView{Status: "pending"}) || worktreeReady(worktreeView{Status: "failed"}) {
		t.Fatalf("unready worktrees must not host a session")
	}
	if worktreeReady(worktreeView{Status: "ready", External: true}) {
		t.Fatalf("external worktrees are read-only")
	}
}

func TestWorktreePendingOpenOpensOnReady(t *testing.T) {
	a := worktreeTestApp(t)
	a.queuePendingWorktreeOpen(worktreeView{ID: "w2", Directory: "/repo/wt/join/", Status: "pending"})
	if a.worktree.PendingOpenDirectory != "/repo/wt/join" || a.worktree.PendingOpenID != "w2" {
		t.Fatalf("queued intent = %+v", a.worktree)
	}

	a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", "pending", ""))
	if a.worktree.PendingOpenDirectory == "" {
		t.Fatalf("pending readiness must keep the open intent: %+v", a.worktree)
	}

	a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", "ready", ""))

	if a.worktree.PendingOpenDirectory != "" || a.worktree.PendingOpenID != "" {
		t.Fatalf("ready must consume the open intent: %+v", a.worktree)
	}
	if a.workspace != "/repo/wt/join" || a.draftCwd != "/repo/wt/join" {
		t.Fatalf("new session must use the ready worktree: workspace=%q draftCwd=%q", a.workspace, a.draftCwd)
	}
	if a.currentSession != "" {
		t.Fatalf("auto-open must start a new session, got %q", a.currentSession)
	}
	if a.worktree.Open {
		t.Fatalf("auto-open must close the worktree panel")
	}
}

func TestWorktreePendingOpenSurvivesBusyThenOpens(t *testing.T) {
	a := worktreeTestApp(t)
	a.worktree.PendingOpenDirectory = "/repo/wt/join"
	a.running = true
	a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", "ready", ""))
	if a.worktree.PendingOpenDirectory == "" || a.workspace == "/repo/wt/join" {
		t.Fatalf("busy shell must defer, not drop or force, the open intent: %+v", a.worktree)
	}
	a.markRunDone("end_turn")
	if a.workspace != "/repo/wt/join" || a.worktree.PendingOpenDirectory != "" {
		t.Fatalf("idle shell must consume the deferred intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}
}

func TestWorktreePendingOpenDropsOnTerminalStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		err    string
	}{
		{name: "failed", status: "failed", err: "checkout failed"},
		{name: "removed", status: "removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := worktreeTestApp(t)
			a.worktree.PendingOpenDirectory = "/repo/wt/join"
			a.worktree.PendingOpenID = "w2"
			a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", tc.status, tc.err))
			if a.worktree.PendingOpenDirectory != "" || a.worktree.PendingOpenID != "" {
				t.Fatalf("%s must drop the open intent: %+v", tc.status, a.worktree)
			}
			if a.workspace == "/repo/wt/join" {
				t.Fatalf("%s must not open a session in the worktree", tc.status)
			}
			if a.worktree.Err == "" {
				t.Fatalf("%s must surface a terminal reason", tc.status)
			}
		})
	}
}

func TestWorktreeAsyncCreatePendingKeepsIntent(t *testing.T) {
	a := worktreeTestApp(t)
	a.applyWorktreeCreated(worktreeView{ID: "w3", Directory: "/repo/wt/async", Status: "pending"}, true)
	if a.worktree.PendingOpenDirectory != "/repo/wt/async" || a.worktree.PendingOpenID != "w3" {
		t.Fatalf("pending create must retain the open intent: %+v", a.worktree)
	}
	if a.workspace == "/repo/wt/async" {
		t.Fatalf("pending worktree must not open a session yet")
	}
	if !strings.Contains(a.worktree.Status, "待就绪") {
		t.Fatalf("pending create status = %q", a.worktree.Status)
	}
}

func TestWorktreeAsyncCreateReadyOpensImmediately(t *testing.T) {
	a := worktreeTestApp(t)
	a.applyWorktreeCreated(worktreeView{ID: "w4", Directory: "/repo/wt/done", Status: "ready"}, true)
	if a.worktree.PendingOpenDirectory != "" || a.workspace != "/repo/wt/done" {
		t.Fatalf("ready create must open the new session: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}
}

func TestWorktreePendingOpenConsumedAfterSessionLoad(t *testing.T) {
	a := worktreeTestApp(t)
	a.currentSession = "s9"
	a.currentTitle = "old"
	a.loadingSession = "s9"
	a.queuePendingWorktreeOpen(worktreeView{ID: "w2", Directory: "/repo/wt/join"})
	a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", "ready", ""))
	if a.worktree.PendingOpenDirectory == "" || a.workspace == "/repo/wt/join" {
		t.Fatalf("ready during load must defer the open: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}

	a.finishSessionLoad("s9", sessionResult{SessionID: "s9"}, "old")
	if a.worktree.PendingOpenDirectory != "" || a.workspace != "/repo/wt/join" || a.draftCwd != "/repo/wt/join" {
		t.Fatalf("load completion must consume the deferred intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}
	if a.loadingSession != "" || a.currentSession != "" {
		t.Fatalf("auto-open must leave a fresh idle shell: loading=%q session=%q", a.loadingSession, a.currentSession)
	}
}

func TestWorktreePendingOpenConsumedAfterPromptFailure(t *testing.T) {
	a := worktreeTestApp(t)
	a.running = true
	a.creatingSession = true
	a.queuePendingWorktreeOpen(worktreeView{ID: "w1", Directory: "/repo/wt/login"})
	a.applyPromptFailure(errors.New("rpc closed"))
	if a.worktree.PendingOpenDirectory != "" || a.workspace != "/repo/wt/login" {
		t.Fatalf("failed send must not strand the intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}
	if a.running || a.creatingSession {
		t.Fatalf("failed send must restore idle state")
	}
	if len(a.messages) == 0 || !strings.Contains(a.messages[len(a.messages)-1].Text, "发送失败") {
		t.Fatalf("failed send must stay visible in the new chat: %+v", a.messages)
	}
}

func TestWorktreePendingOpenConsumedAfterFailedSessionLoad(t *testing.T) {
	a := worktreeTestApp(t)
	a.currentSession = "s9"
	a.loadingSession = "s9"
	a.queuePendingWorktreeOpen(worktreeView{ID: "w2", Directory: "/repo/wt/join"})
	a.applyWorktreeStatus(worktreeStatusEvent("w2", "/repo/wt/join", "ready", ""))
	a.failSessionLoad(errors.New("load failed"))
	if a.worktree.PendingOpenDirectory != "" || a.workspace != "/repo/wt/join" {
		t.Fatalf("failed load must consume the intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
	}
	if len(a.messages) == 0 || !strings.Contains(a.messages[len(a.messages)-1].Text, "载入会话失败") {
		t.Fatalf("failed load must keep its error visible: %+v", a.messages)
	}
}

func TestWorktreePendingOpenConsumedAfterConfigSwitch(t *testing.T) {
	a := worktreeTestApp(t)
	a.creatingSession = true
	a.queuePendingWorktreeOpen(worktreeView{ID: "w1", Directory: "/repo/wt/login"})
	a.finishSessionCreation()
	if a.creatingSession || a.worktree.PendingOpenDirectory != "" || a.workspace != "/repo/wt/login" {
		t.Fatalf("config switch completion must consume the intent: creating=%v workspace=%q pending=%q", a.creatingSession, a.workspace, a.worktree.PendingOpenDirectory)
	}
}

func TestWorktreePendingOpenSurvivesStalePendingList(t *testing.T) {
	a := worktreeTestApp(t)
	a.running = true
	a.queuePendingWorktreeOpen(worktreeView{ID: "w5", Directory: "/repo/wt/late", Status: "pending"})
	if a.worktree.PendingOpenReady {
		t.Fatalf("a pending create must not remember readiness: %+v", a.worktree)
	}

	// Readiness arrives while the run is still busy: the panel status alone is
	// transient, so the intent itself has to remember it.
	a.applyWorktreeStatus(worktreeStatusEvent("w5", "/repo/wt/late", "ready", ""))
	if !a.worktree.PendingOpenReady || a.worktree.PendingOpenDirectory == "" {
		t.Fatalf("ready while busy must stick to the intent: %+v", a.worktree)
	}
	if a.workspace == "/repo/wt/late" {
		t.Fatalf("busy shell must not switch workspaces")
	}

	// A lagging list snapshot still describes the worktree as pending. It must
	// neither downgrade the row nor revoke the queued intent.
	a.applyWorktreeList([]worktreeView{
		{ID: "w2", Directory: "/repo/wt/join", Status: "pending"},
		{ID: "w5", Directory: "/repo/wt/late", Status: "pending"},
	})
	found := false
	for _, tree := range a.worktree.Worktrees {
		if tree.ID == "w5" {
			found = true
			if tree.Status != "ready" {
				t.Fatalf("stale pending list downgraded a ready row: %+v", tree)
			}
		}
	}
	if !found {
		t.Fatalf("list snapshot lost the created worktree: %+v", a.worktree.Worktrees)
	}
	if a.worktree.PendingOpenDirectory == "" || a.workspace == "/repo/wt/late" {
		t.Fatalf("stale list must keep the deferred intent: pending=%q workspace=%q", a.worktree.PendingOpenDirectory, a.workspace)
	}

	a.markRunDone("end_turn")
	if a.workspace != "/repo/wt/late" || a.draftCwd != "/repo/wt/late" {
		t.Fatalf("stale list must not block the deferred open: workspace=%q draftCwd=%q", a.workspace, a.draftCwd)
	}
	if a.worktree.PendingOpenDirectory != "" || a.worktree.PendingOpenID != "" || a.worktree.PendingOpenReady {
		t.Fatalf("auto-open must clear the whole intent: %+v", a.worktree)
	}
	if a.currentSession != "" || a.worktree.Open {
		t.Fatalf("auto-open must leave a fresh idle shell: session=%q open=%v", a.currentSession, a.worktree.Open)
	}
}

func TestWorktreeListRefreshSettlesDeferredOpen(t *testing.T) {
	t.Run("ready snapshot while idle opens", func(t *testing.T) {
		a := worktreeTestApp(t)
		a.queuePendingWorktreeOpen(worktreeView{ID: "w6", Directory: "/repo/wt/refresh", Status: "pending"})
		a.applyWorktreeList([]worktreeView{{ID: "w6", Directory: "/repo/wt/refresh", Status: "pending"}})
		if a.worktree.PendingOpenDirectory == "" || a.workspace == "/repo/wt/refresh" {
			t.Fatalf("pending snapshot must defer the open: pending=%q workspace=%q", a.worktree.PendingOpenDirectory, a.workspace)
		}
		a.applyWorktreeList([]worktreeView{{ID: "w6", Directory: "/repo/wt/refresh", Status: "ready"}})
		if a.workspace != "/repo/wt/refresh" || a.worktree.PendingOpenDirectory != "" {
			t.Fatalf("list refresh must settle the intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
		}
	})

	t.Run("ready snapshot while busy defers", func(t *testing.T) {
		a := worktreeTestApp(t)
		a.running = true
		a.queuePendingWorktreeOpen(worktreeView{ID: "w7", Directory: "/repo/wt/later", Status: "pending"})
		a.applyWorktreeList([]worktreeView{{ID: "w7", Directory: "/repo/wt/later", Status: "ready"}})
		if a.workspace == "/repo/wt/later" || a.worktree.PendingOpenDirectory == "" {
			t.Fatalf("busy refresh must defer the open: pending=%q workspace=%q", a.worktree.PendingOpenDirectory, a.workspace)
		}
		if !a.worktree.PendingOpenReady {
			t.Fatalf("busy refresh must stick readiness to the intent: %+v", a.worktree)
		}
		a.markRunDone("end_turn")
		if a.workspace != "/repo/wt/later" || a.worktree.PendingOpenDirectory != "" {
			t.Fatalf("run completion must consume the refreshed intent: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
		}
	})

	t.Run("terminal snapshot drops intent", func(t *testing.T) {
		a := worktreeTestApp(t)
		a.queuePendingWorktreeOpen(worktreeView{ID: "w8", Directory: "/repo/wt/bad", Status: "pending"})
		a.applyWorktreeList([]worktreeView{{ID: "w8", Directory: "/repo/wt/bad", Status: "failed"}})
		if a.worktree.PendingOpenDirectory != "" || a.workspace == "/repo/wt/bad" {
			t.Fatalf("failed snapshot must drop the intent: pending=%q workspace=%q", a.worktree.PendingOpenDirectory, a.workspace)
		}
		if a.worktree.Err == "" {
			t.Fatalf("failed snapshot must surface a reason")
		}
	})

	t.Run("history ready hint beats stale panel row", func(t *testing.T) {
		a := worktreeTestApp(t)
		a.queuePendingWorktreeOpen(worktreeView{ID: "w9", Directory: "/repo/wt/hint", Status: "pending"})
		// Simulate a stale panel row with no sticky intent flag but a canonical
		// ready hint already projected by a status notification.
		a.historyPanel.Worktrees["/repo/wt/hint"] = "ready"
		a.worktree.Worktrees = []worktreeView{{ID: "w9", Directory: "/repo/wt/hint", Status: "pending"}}
		a.settleWorktreeOpen()
		if a.workspace != "/repo/wt/hint" || a.worktree.PendingOpenDirectory != "" {
			t.Fatalf("known ready hint must win over a stale row: workspace=%q pending=%q", a.workspace, a.worktree.PendingOpenDirectory)
		}
	})
}

func TestWorktreePendingOpenSurvivesPanelReopen(t *testing.T) {
	a := worktreeTestApp(t)
	a.running = true
	a.queuePendingWorktreeOpen(worktreeView{ID: "w10", Directory: "/repo/wt/reopen", Status: "pending"})

	// A canonical list snapshot reports readiness while the shell is still
	// busy. The intent, not the transient panel row, has to remember it.
	a.applyWorktreeList([]worktreeView{
		{ID: "w2", Directory: "/repo/wt/join", Status: "pending"},
		{ID: "w10", Directory: "/repo/wt/reopen", Status: "ready"},
	})
	if !a.worktree.PendingOpenReady {
		t.Fatalf("busy list readiness must stick to the intent: %+v", a.worktree)
	}
	if a.workspace == "/repo/wt/reopen" {
		t.Fatalf("busy shell must not switch workspaces")
	}

	// Closing and reopening the panel clears its rows. The ready evidence has
	// to survive the reset, otherwise one lagging snapshot permanently parks
	// the promised open.
	a.closeWorktreePanel()
	a.openWorktreePanel()
	if a.worktree.PendingOpenDirectory != "/repo/wt/reopen" || !a.worktree.PendingOpenReady {
		t.Fatalf("reopen must keep the ready intent: %+v", a.worktree)
	}
	carriedReady := false
	for _, tree := range a.worktree.Worktrees {
		if tree.ID == "w10" && tree.Status == "ready" {
			carriedReady = true
		}
	}
	if !carriedReady {
		t.Fatalf("reopen must carry the known ready row: %+v", a.worktree.Worktrees)
	}

	// A lagging pending list must neither downgrade the carried row nor the
	// intent itself.
	a.applyWorktreeList([]worktreeView{{ID: "w10", Directory: "/repo/wt/reopen", Status: "pending"}})
	if !a.worktree.PendingOpenReady || a.worktree.PendingOpenDirectory == "" || a.workspace == "/repo/wt/reopen" {
		t.Fatalf("stale list after reopen must keep the deferred intent: %+v", a.worktree)
	}

	a.markRunDone("end_turn")
	if a.workspace != "/repo/wt/reopen" || a.draftCwd != "/repo/wt/reopen" {
		t.Fatalf("idle shell must open the survived intent: workspace=%q draftCwd=%q", a.workspace, a.draftCwd)
	}
	if a.worktree.PendingOpenDirectory != "" || a.worktree.PendingOpenID != "" || a.worktree.PendingOpenReady {
		t.Fatalf("auto-open must clear the whole intent: %+v", a.worktree)
	}
}

func TestWorktreeDisplayHelpers(t *testing.T) {
	if got := worktreeDisplayName(worktreeView{Name: "named", Directory: "/x/y"}); got != "named" {
		t.Fatalf("named display = %q", got)
	}
	if got := worktreeDisplayName(worktreeView{Directory: "/repo/wt/joined"}); got != "joined" {
		t.Fatalf("basename display = %q", got)
	}
	if got := worktreeStatusLabel("ready"); got != "就绪" {
		t.Fatalf("status label = %q", got)
	}
	if got := worktreeSummary(0); got != "尚未创建隔离工作树" {
		t.Fatalf("zero summary = %q", got)
	}
}

func TestWorktreeDialogRenders(t *testing.T) {
	a := worktreeTestApp(t)
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"隔离工作树", "创建工作树", "feature-login", "就绪", "准备中", "外部", "重置", "移除", "打开会话"} {
		if !textPresent(tt, want) {
			t.Fatalf("worktree dialog missing %q: %q", want, tt.Texts())
		}
	}
	// External worktrees are read-only: no reset/remove controls are rendered
	// for the external row. Count keys rather than relying on label order.
	resetKeys := 0
	removeKeys := 0
	for _, tree := range a.worktree.Worktrees {
		if tree.External {
			continue
		}
		resetKeys++
		removeKeys++
	}
	if resetKeys != 2 || removeKeys != 2 {
		t.Fatalf("non-external worktree count = %d", resetKeys)
	}
}

func TestWorktreeCapabilityGating(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.workspace = "/repo/main"
	a.setFeatures(map[string]bool{})
	a.openWorktreePanel()
	if a.worktree.Open {
		t.Fatalf("worktree panel must not open without the worktrees feature")
	}
	a.setFeatures(map[string]bool{"worktrees": true})
	a.openWorktreePanel()
	if !a.worktree.Open {
		t.Fatalf("worktree panel did not open")
	}
	// Reset is a separately advertised capability.
	a.setFeatures(map[string]bool{"worktrees": true})
	if a.worktreeResetCapable() {
		t.Fatalf("reset must require worktreeReset")
	}
	a.worktree.Open = false
}

func TestWorktreeConfirmLifecycle(t *testing.T) {
	a := worktreeTestApp(t)
	tree := a.worktree.Worktrees[0]
	a.askRemoveWorktree(tree)
	if !a.worktree.ConfirmOpen || a.worktree.ConfirmKind != worktreeConfirmRemove {
		t.Fatalf("remove confirm state = %+v", a.worktree)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"移除工作树？", "确认移除", "feature-login"} {
		if !textPresent(tt, want) {
			t.Fatalf("remove dialog missing %q: %q", want, tt.Texts())
		}
	}
	a.closeWorktreeConfirm()
	if a.worktree.ConfirmOpen || a.worktree.ConfirmKind != "" {
		t.Fatalf("confirm did not close: %+v", a.worktree)
	}

	a.askResetWorktree(tree)
	if a.worktree.ConfirmKind != worktreeConfirmReset {
		t.Fatalf("reset kind = %q", a.worktree.ConfirmKind)
	}
	a.closeWorktreePanel()
	if a.worktree.Open || a.worktree.ConfirmOpen {
		t.Fatalf("closeWorktreePanel must close both surfaces: %+v", a.worktree)
	}
}
