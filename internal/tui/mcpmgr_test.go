package tui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
