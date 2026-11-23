package desktop

import (
	"strings"
	"sync/atomic"
)

// openMCPPanelForScope opens Settings → MCP at a specific Runtime-owned scope.
// Project scope is bound to the currently open session; it never guesses a
// project path in the client.
func (a *nativeApp) openMCPPanelForScope(scope string) {
	if !a.mcpCapable() {
		return
	}
	if scope == mcpScopeProject && strings.TrimSpace(a.currentSession) == "" {
		a.status = "请先打开一个会话再管理项目 MCP"
		return
	}
	m := &a.mcp
	atomic.AddInt64(&m.Seq, 1)
	m.Scope = scope
	m.Path = ""
	m.Servers = nil
	m.Loaded = false
	m.Loading = false
	m.Saving = false
	m.Deleting = false
	m.Err = ""
	m.Status = ""
	m.StatusIsError = false
	if a.historyPanel.DialogOpen {
		a.closeHistoryDialog()
	}
	if a.historyPanel.ProjectDialogOpen {
		a.closeProjectDialog()
	}
	if a.settingsOpen {
		a.setSettingsTab("mcp")
		return
	}
	a.historyOpen = false
	a.history = false
	a.closeRouterPanel()
	a.closeSubagentPanel()
	a.closeWorktreePanel()
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "mcp"
	a.settingsErr = ""
	a.settingsStatus = ""
	a.ensureSettingsTabVisible()
	a.maybeLoadActiveSettingsTab()
}

func (a *nativeApp) openProjectMCPPanel() {
	a.openMCPPanelForScope(mcpScopeProject)
}
