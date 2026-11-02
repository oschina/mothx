package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/components/editor"
)

// mcpMgrState is a UI projection of one mcp.json scope. Configuration remains
// owned by internal/config; this panel never alters Runtime-held MCP clients.
type mcpMgrState struct {
	Open          bool
	Scope         string
	Path          string
	Servers       []config.MCPServer
	Cursor        int
	ChoosingType  bool
	TypeCursor    int
	Editing       bool
	EditingIndex  int
	Draft         config.MCPServer
	FormCursor    int
	InputField    string
	Input         editor.Model
	ConfirmDelete bool
	Error         string
	Message       string
}

var mcpMgrTransportOptions = []struct {
	Type        string
	Title       string
	Description string
}{
	{"stdio", "Local stdio", "Start a local command and communicate over stdin/stdout"},
	{"http", "Streamable HTTP", "Connect to a current MCP remote endpoint"},
	{"sse", "Legacy SSE", "Connect using a legacy stream URL plus message URL"},
}

func mcpMgrBoolPtr(v bool) *bool { return &v }

func (a *App) openMCPMgr(scope string) {
	if scope == "" {
		scope = "project"
	}
	if scope != "project" && scope != "global" {
		a.addCommandError("Usage: /mcpmgr [project|global]")
		return
	}
	a.input = a.input.Blur()
	a.mcpMgr = mcpMgrState{Open: true, Scope: scope, EditingIndex: -1}
	a.loadMCPMgrScope(scope)
	a.scheduleRender()
}

func (a *App) closeMCPMgr() {
	a.mcpMgr = mcpMgrState{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

func (a *App) mcpMgrPath(scope string) string {
	if scope == "global" {
		return config.GlobalMCPPath()
	}
	return filepath.Join(a.currentCwd(), config.ProjectMCPPath())
}

func (a *App) loadMCPMgrScope(scope string) {
	path := a.mcpMgrPath(scope)
	cfg, err := config.LoadMCPConfig(path)
	if err != nil {
		if !os.IsNotExist(err) {
			a.mcpMgr.Error = "Load MCP config: " + err.Error()
			return
		}
		cfg = &config.MCPConfig{}
	}
	config.NormalizeMCPConfig(cfg)
	a.mcpMgr.Scope = scope
	a.mcpMgr.Path = path
	a.mcpMgr.Servers = append([]config.MCPServer(nil), cfg.MCPServers...)
	if a.mcpMgr.Cursor >= len(a.mcpMgr.Servers) {
		a.mcpMgr.Cursor = max(0, len(a.mcpMgr.Servers)-1)
	}
	a.mcpMgr.Error = ""
	a.mcpMgr.Message = ""
}

func (a *App) saveMCPMgr() error {
	cfg := &config.MCPConfig{MCPServers: append([]config.MCPServer(nil), a.mcpMgr.Servers...)}
	config.NormalizeMCPConfig(cfg)
	if err := config.SaveMCPConfig(a.mcpMgr.Path, cfg); err != nil {
		return err
	}
	a.mcpMgr.Servers = cfg.MCPServers
	a.mcpMgr.Message = "Saved. Run /reload to apply MCP changes to this session."
	return nil
}

func (a *App) beginMCPMgrCreate() {
	a.mcpMgr.ChoosingType = true
	a.mcpMgr.TypeCursor = 0
	a.mcpMgr.Error = ""
	a.mcpMgr.ConfirmDelete = false
}

func (a *App) chooseMCPMgrType() {
	if a.mcpMgr.TypeCursor < 0 || a.mcpMgr.TypeCursor >= len(mcpMgrTransportOptions) {
		return
	}
	typeName := mcpMgrTransportOptions[a.mcpMgr.TypeCursor].Type
	a.mcpMgr.ChoosingType = false
	if a.mcpMgr.Editing && a.mcpMgr.EditingIndex >= 0 {
		a.mcpMgr.Draft.Type = typeName
		a.mcpMgr.FormCursor = 0
		return
	}
	a.mcpMgr.Editing = true
	a.mcpMgr.EditingIndex = -1
	a.mcpMgr.Draft = config.MCPServer{Type: typeName, Enabled: mcpMgrBoolPtr(true)}
	a.mcpMgr.FormCursor = 0
	a.mcpMgr.Error = ""
}

func (a *App) beginMCPMgrEdit(index int) {
	if index < 0 || index >= len(a.mcpMgr.Servers) {
		return
	}
	a.mcpMgr.Editing = true
	a.mcpMgr.EditingIndex = index
	a.mcpMgr.Draft = a.mcpMgr.Servers[index]
	a.mcpMgr.FormCursor = 0
	a.mcpMgr.InputField = ""
	a.mcpMgr.Error = ""
}

func (a *App) closeMCPMgrForm() {
	a.mcpMgr.ChoosingType = false
	a.mcpMgr.Editing = false
	a.mcpMgr.EditingIndex = -1
	a.mcpMgr.Draft = config.MCPServer{}
	a.mcpMgr.FormCursor = 0
	a.mcpMgr.InputField = ""
	a.mcpMgr.Input = editor.Model{}
	a.mcpMgr.Error = ""
}

type mcpMgrField struct {
	Key         string
	Title       string
	Description string
}

func (a *App) mcpMgrFormFields() []mcpMgrField {
	draft := a.mcpMgr.Draft
	fields := []mcpMgrField{
		{Key: "name", Title: "Name", Description: valueOrDefault(draft.Name, "required")},
		{Key: "type", Title: "Transport", Description: draft.Type},
	}
	switch draft.Type {
	case "stdio":
		fields = append(fields, mcpMgrField{Key: "command", Title: "Command", Description: valueOrDefault(draft.Command, "required")})
	default:
		fields = append(fields, mcpMgrField{Key: "url", Title: "URL", Description: valueOrDefault(draft.URL, "required")})
		if draft.Type == "sse" {
			fields = append(fields, mcpMgrField{Key: "messageUrl", Title: "Message URL", Description: valueOrDefault(draft.MessageURL, "required for legacy SSE")})
		}
	}
	fields = append(fields,
		mcpMgrField{Key: "args", Title: "Arguments", Description: strings.Join(draft.Args, " ")},
		mcpMgrField{Key: "env", Title: "Environment", Description: mcpMgrPairsSummary(draft.Env)},
		mcpMgrField{Key: "headers", Title: "Headers", Description: mcpMgrPairsSummary(draft.Headers)},
		mcpMgrField{Key: "enabled", Title: "Enabled", Description: fmt.Sprintf("%t", config.MCPServerEnabled(draft))},
		mcpMgrField{Key: "save", Title: "Save server", Description: "Write this MCP server to mcp.json"},
		mcpMgrField{Key: "cancel", Title: "Cancel", Description: "Discard changes"},
	)
	return fields
}

func mcpMgrPairsSummary(pairs []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) string {
	if len(pairs) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d configured", len(pairs))
}

func (a *App) beginMCPMgrFieldInput(field string) {
	a.mcpMgr.InputField = field
	a.mcpMgr.Error = ""
	a.mcpMgr.Input = editor.New(max(28, a.width-8)).SetMaxLines(1).SetPlaceholder(a.mcpMgrFieldPlaceholder(field)).SetValue(a.mcpMgrFieldValue(field)).Focus()
}

func (a *App) mcpMgrFieldPlaceholder(field string) string {
	switch field {
	case "args":
		return "space-separated command arguments"
	case "env", "headers":
		return "NAME=value, one entry per line"
	default:
		return field
	}
}

func (a *App) mcpMgrFieldValue(field string) string {
	draft := a.mcpMgr.Draft
	switch field {
	case "name":
		return draft.Name
	case "command":
		return draft.Command
	case "url":
		return draft.URL
	case "messageUrl":
		return draft.MessageURL
	case "args":
		return strings.Join(draft.Args, " ")
	case "env":
		return mcpMgrPairsValue(draft.Env)
	case "headers":
		return mcpMgrPairsValue(draft.Headers)
	}
	return ""
}

func mcpMgrPairsValue(pairs []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}) string {
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		lines = append(lines, pair.Name+"="+pair.Value)
	}
	return strings.Join(lines, "\n")
}

func (a *App) submitMCPMgrFieldInput() {
	field := a.mcpMgr.InputField
	value := strings.TrimSpace(a.mcpMgr.Input.Value())
	draft := &a.mcpMgr.Draft
	switch field {
	case "name":
		draft.Name = value
	case "command":
		draft.Command = value
	case "url":
		draft.URL = value
	case "messageUrl":
		draft.MessageURL = value
	case "args":
		if value == "" {
			draft.Args = nil
		} else {
			draft.Args = strings.Fields(value)
		}
	case "env":
		pairs, err := parseMCPMgrPairs(value)
		if err != nil {
			a.mcpMgr.Error = "Environment: " + err.Error()
			return
		}
		draft.Env = pairs
	case "headers":
		pairs, err := parseMCPMgrPairs(value)
		if err != nil {
			a.mcpMgr.Error = "Headers: " + err.Error()
			return
		}
		draft.Headers = pairs
	}
	a.mcpMgr.InputField = ""
	a.mcpMgr.Input = editor.Model{}
}

func parseMCPMgrPairs(value string) ([]struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}, error) {
	var pairs []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("each entry must use NAME=value")
		}
		pairs = append(pairs, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{Name: name, Value: val})
	}
	return pairs, nil
}

func (a *App) saveMCPMgrDraft() {
	draft := a.mcpMgr.Draft
	if err := validateMCPMgrServer(draft, a.mcpMgr.Servers, a.mcpMgr.EditingIndex); err != nil {
		a.mcpMgr.Error = err.Error()
		return
	}
	draft.Name = strings.TrimSpace(draft.Name)
	draft.Type = strings.TrimSpace(draft.Type)
	draft.Command = strings.TrimSpace(draft.Command)
	draft.URL = strings.TrimSpace(draft.URL)
	draft.MessageURL = strings.TrimSpace(draft.MessageURL)
	if a.mcpMgr.EditingIndex < 0 {
		a.mcpMgr.Servers = append(a.mcpMgr.Servers, draft)
		a.mcpMgr.Cursor = len(a.mcpMgr.Servers) - 1
	} else {
		a.mcpMgr.Servers[a.mcpMgr.EditingIndex] = draft
	}
	if err := a.saveMCPMgr(); err != nil {
		a.mcpMgr.Error = "Save MCP config: " + err.Error()
		return
	}
	a.closeMCPMgrForm()
}

func validateMCPMgrServer(server config.MCPServer, existing []config.MCPServer, editingIndex int) error {
	server.Name = strings.TrimSpace(server.Name)
	server.Type = strings.TrimSpace(server.Type)
	if server.Name == "" {
		return fmt.Errorf("MCP server name is required")
	}
	for i, current := range existing {
		if i != editingIndex && current.Name == server.Name {
			return fmt.Errorf("MCP server name %q already exists", server.Name)
		}
	}
	switch server.Type {
	case "stdio":
		if strings.TrimSpace(server.Command) == "" {
			return fmt.Errorf("stdio MCP server requires command")
		}
	case "http":
		if strings.TrimSpace(server.URL) == "" {
			return fmt.Errorf("http MCP server requires url")
		}
	case "sse":
		if strings.TrimSpace(server.URL) == "" || strings.TrimSpace(server.MessageURL) == "" {
			return fmt.Errorf("legacy sse MCP server requires url and messageUrl")
		}
	default:
		return fmt.Errorf("MCP transport must be stdio, http, or sse")
	}
	return nil
}

func (a *App) handleMCPMgrKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.mcpMgr.Open {
		return false, nil
	}
	if a.mcpMgr.InputField != "" {
		switch msg.Type {
		case tea.KeyEsc:
			a.mcpMgr.InputField = ""
			a.mcpMgr.Input = editor.Model{}
		case tea.KeyEnter:
			a.submitMCPMgrFieldInput()
		default:
			var cmd tea.Cmd
			a.mcpMgr.Input, cmd = a.mcpMgr.Input.Update(msg)
			a.scheduleRender()
			return true, cmd
		}
		a.scheduleRender()
		return true, nil
	}
	if a.mcpMgr.ChoosingType {
		switch {
		case msg.Type == tea.KeyEsc:
			a.mcpMgr.ChoosingType = false
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.mcpMgr.TypeCursor = max(0, a.mcpMgr.TypeCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.mcpMgr.TypeCursor = min(len(mcpMgrTransportOptions)-1, a.mcpMgr.TypeCursor+1)
		case msg.Type == tea.KeyEnter:
			a.chooseMCPMgrType()
		}
		a.scheduleRender()
		return true, nil
	}
	if a.mcpMgr.Editing {
		fields := a.mcpMgrFormFields()
		switch {
		case msg.Type == tea.KeyEsc:
			a.closeMCPMgrForm()
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.mcpMgr.FormCursor = max(0, a.mcpMgr.FormCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.mcpMgr.FormCursor = min(len(fields)-1, a.mcpMgr.FormCursor+1)
		case msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "e"):
			if a.mcpMgr.FormCursor < 0 || a.mcpMgr.FormCursor >= len(fields) {
				break
			}
			switch fields[a.mcpMgr.FormCursor].Key {
			case "type":
				a.mcpMgr.ChoosingType = true
				for i, option := range mcpMgrTransportOptions {
					if option.Type == a.mcpMgr.Draft.Type {
						a.mcpMgr.TypeCursor = i
					}
				}
			case "enabled":
				a.mcpMgr.Draft.Enabled = mcpMgrBoolPtr(!config.MCPServerEnabled(a.mcpMgr.Draft))
			case "save":
				a.saveMCPMgrDraft()
			case "cancel":
				a.closeMCPMgrForm()
			default:
				a.beginMCPMgrFieldInput(fields[a.mcpMgr.FormCursor].Key)
			}
		}
		a.scheduleRender()
		return true, nil
	}
	if a.mcpMgr.ConfirmDelete {
		switch {
		case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "n")):
			a.mcpMgr.ConfirmDelete = false
		case msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y"):
			if a.mcpMgr.Cursor < len(a.mcpMgr.Servers) {
				a.mcpMgr.Servers = append(a.mcpMgr.Servers[:a.mcpMgr.Cursor], a.mcpMgr.Servers[a.mcpMgr.Cursor+1:]...)
				if a.mcpMgr.Cursor >= len(a.mcpMgr.Servers) {
					a.mcpMgr.Cursor = max(0, len(a.mcpMgr.Servers)-1)
				}
				if err := a.saveMCPMgr(); err != nil {
					a.mcpMgr.Error = "Save MCP config: " + err.Error()
				}
			}
			a.mcpMgr.ConfirmDelete = false
		}
		a.scheduleRender()
		return true, nil
	}
	switch {
	case msg.Type == tea.KeyCtrlC:
		a.finalizeForQuit()
		a.stopPrintLoop()
		return true, tea.Quit
	case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
		a.closeMCPMgr()
	case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
		a.mcpMgr.Cursor = max(0, a.mcpMgr.Cursor-1)
	case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
		a.mcpMgr.Cursor = min(len(a.mcpMgr.Servers)-1, a.mcpMgr.Cursor+1)
	case msg.Type == tea.KeySpace:
		if a.mcpMgr.Cursor < len(a.mcpMgr.Servers) {
			server := &a.mcpMgr.Servers[a.mcpMgr.Cursor]
			server.Enabled = mcpMgrBoolPtr(!config.MCPServerEnabled(*server))
			if err := a.saveMCPMgr(); err != nil {
				a.mcpMgr.Error = "Save MCP config: " + err.Error()
			}
		}
	case msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "e"):
		a.beginMCPMgrEdit(a.mcpMgr.Cursor)
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "a":
		a.beginMCPMgrCreate()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "d":
		if a.mcpMgr.Cursor < len(a.mcpMgr.Servers) {
			a.mcpMgr.ConfirmDelete = true
		}
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "r":
		a.loadMCPMgrScope(a.mcpMgr.Scope)
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "g":
		a.loadMCPMgrScope("global")
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "p":
		a.loadMCPMgrScope("project")
	}
	a.scheduleRender()
	return true, nil
}

func (a *App) renderMCPMgr() string {
	width := max(42, a.width-4)
	height := max(8, a.height-lipgloss.Height(a.renderFooter())-5)
	lines := []string{"MCP Manager", strings.Repeat("-", width)}
	switch {
	case a.mcpMgr.InputField != "":
		lines = append(lines, "Set "+a.mcpMgr.InputField+":", a.mcpMgr.Input.View(), "", "Enter: confirm  Esc: back")
	case a.mcpMgr.ChoosingType:
		lines = append(lines, "Choose a transport:")
		for i, option := range mcpMgrTransportOptions {
			cursor := " "
			if i == a.mcpMgr.TypeCursor {
				cursor = ">"
			}
			lines = append(lines, fmt.Sprintf("%s %s - %s", cursor, option.Title, option.Description))
		}
		lines = append(lines, "", "Up/Down: choose  Enter: next  Esc: cancel")
	case a.mcpMgr.Editing:
		lines = append(lines, "Configure MCP server:")
		for i, field := range a.mcpMgrFormFields() {
			cursor := " "
			if i == a.mcpMgr.FormCursor {
				cursor = ">"
			}
			lines = append(lines, xansi.Truncate(fmt.Sprintf("%s %-14s %s", cursor, field.Title, field.Description), width, "..."))
		}
		lines = append(lines, "", "Up/Down: choose  Enter: set/toggle  Esc: cancel")
	case a.mcpMgr.ConfirmDelete:
		name := a.mcpMgr.Servers[a.mcpMgr.Cursor].Name
		lines = append(lines, fmt.Sprintf("Delete %q? y/n", name))
	default:
		lines = append(lines, "g/p: global/project  a:add  enter/e:edit  space:enable  d:delete  r:reload  esc:close", fmt.Sprintf("Scope: %s  Path: %s", a.mcpMgr.Scope, a.mcpMgr.Path))
		if len(a.mcpMgr.Servers) == 0 {
			lines = append(lines, "(no MCP servers; press a to add one)")
		}
		for i, server := range a.mcpMgr.Servers {
			cursor := " "
			if i == a.mcpMgr.Cursor {
				cursor = ">"
			}
			state := "off"
			if config.MCPServerEnabled(server) {
				state = "on"
			}
			target := server.Command
			if target == "" {
				target = server.URL
			}
			lines = append(lines, xansi.Truncate(fmt.Sprintf("%s [%s] %-18s %-5s %s", cursor, state, server.Name, server.Type, target), width, "..."))
		}
	}
	if a.mcpMgr.Message != "" {
		lines = append(lines, "", a.mcpMgr.Message)
	}
	if a.mcpMgr.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.mcpMgr.Error))
	}
	if len(lines) > height {
		lines = append(lines[:height-1], "...")
	}
	return toolModalStyle.Width(width).Height(height + 2).Render(strings.Join(lines, "\n"))
}
