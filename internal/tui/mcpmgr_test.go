package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

func TestMCPMgrCRUDAndEnablement(t *testing.T) {
	workDir := t.TempDir()
	app := &App{cwd: workDir, width: 100}
	app.openMCPMgr("project")
	if !app.mcpMgr.Open || app.mcpMgr.Scope != "project" {
		t.Fatalf("manager state = %#v", app.mcpMgr)
	}

	app.beginMCPMgrCreate()
	if !app.mcpMgr.ChoosingType {
		t.Fatal("create must begin with transport selection")
	}
	app.chooseMCPMgrType()
	app.beginMCPMgrFieldInput("name")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture")
	app.submitMCPMgrFieldInput()
	app.beginMCPMgrFieldInput("command")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture-mcp")
	app.submitMCPMgrFieldInput()
	app.saveMCPMgrDraft()
	if app.mcpMgr.Error != "" || len(app.mcpMgr.Servers) != 1 {
		t.Fatalf("add error=%q servers=%#v", app.mcpMgr.Error, app.mcpMgr.Servers)
	}

	_, _ = app.handleMCPMgrKey(tea.KeyMsg{Type: tea.KeySpace})
	cfg, err := config.LoadMCPConfig(filepath.Join(workDir, config.ProjectMCPPath()))
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if len(cfg.MCPServers) != 1 || config.MCPServerEnabled(cfg.MCPServers[0]) {
		t.Fatalf("toggle did not persist disabled server: %#v", cfg.MCPServers)
	}

	app.beginMCPMgrEdit(0)
	app.mcpMgr.Draft.Name = "renamed"
	app.mcpMgr.Draft.Type = "http"
	app.mcpMgr.Draft.Command = ""
	app.mcpMgr.Draft.URL = "https://mcp.example.test"
	app.mcpMgr.Draft.Enabled = mcpMgrBoolPtr(true)
	app.saveMCPMgrDraft()
	if app.mcpMgr.Error != "" || app.mcpMgr.Servers[0].Name != "renamed" {
		t.Fatalf("edit error=%q servers=%#v", app.mcpMgr.Error, app.mcpMgr.Servers)
	}

	_, _ = app.handleMCPMgrKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	_, _ = app.handleMCPMgrKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(app.mcpMgr.Servers) != 0 {
		t.Fatalf("delete left servers=%#v", app.mcpMgr.Servers)
	}
}

// TestMCPMgrSaveSchedulesHotReload guards that saving the MCP configuration
// reconnects the servers in place instead of requiring a client restart. The
// reconnect command must be scheduled so the caller can run it off the
// update loop.
func TestMCPMgrSaveSchedulesHotReload(t *testing.T) {
	workDir := t.TempDir()
	app := &App{cwd: workDir, width: 100, runtime: &agentruntime.SessionRuntime{}}
	app.openMCPMgr("project")

	app.beginMCPMgrCreate()
	app.chooseMCPMgrType()
	app.beginMCPMgrFieldInput("name")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture")
	app.submitMCPMgrFieldInput()
	app.beginMCPMgrFieldInput("command")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture-mcp")
	app.submitMCPMgrFieldInput()

	cmd := app.saveMCPMgrDraft()
	if cmd == nil {
		t.Fatal("saving the MCP configuration must schedule a hot reload")
	}
}

// TestMCPMgrSaveWithoutRuntimeHasNoReloadCommand guards that a manager without
// a shared Runtime cannot schedule a reload: there is no Runtime-owned
// connection to replace.
func TestMCPMgrSaveWithoutRuntimeHasNoReloadCommand(t *testing.T) {
	workDir := t.TempDir()
	app := &App{cwd: workDir, width: 100}
	app.openMCPMgr("project")

	app.beginMCPMgrCreate()
	app.chooseMCPMgrType()
	app.beginMCPMgrFieldInput("name")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture")
	app.submitMCPMgrFieldInput()
	app.beginMCPMgrFieldInput("command")
	app.mcpMgr.Input = app.mcpMgr.Input.SetValue("fixture-mcp")
	app.submitMCPMgrFieldInput()

	if cmd := app.saveMCPMgrDraft(); cmd != nil {
		t.Fatal("a manager without a Runtime must not schedule a reload")
	}
}
