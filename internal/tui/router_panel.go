package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/components/editor"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// routerPanelState is a UI projection of the global `acpAgents` list and the
// current session's Router binding.
//
// Layering (see AGENTS.md):
//   - validation and durable writes belong to internal/config
//     (ValidateACPAgentProfileSet / SaveACPAgentProfiles);
//   - the read-only catalog and eligibility projection belong to the shared
//     Runtime (ListACPWorkerProfiles);
//   - activation belongs to the shared Runtime (SessionRuntime.ActivateRouter).
//
// This panel only renders values and collects input. It never computes
// eligibility itself, never reads a profile's argv for execution, and never
// writes settings.json directly.
type routerPanelState struct {
	Open          bool
	Profiles      []config.ACPAgentProfile
	Cursor        int
	ChoosingColon bool
	ColonCursor   int
	ChoosingRole  bool
	RoleCursor    int
	Editing       bool
	EditingIndex  int
	Draft         config.ACPAgentProfile
	Roles         map[config.ACPAgentRole]bool
	FormCursor    int
	InputField    string
	Input         editor.Model
	ConfirmDelete bool
	// Presets is the well-known-agent picker shown when creating a profile. It
	// exists so a user does not have to know that `codex acp` is not a valid ACP
	// launch and which adapter package is.
	ChoosingPreset bool
	PresetCursor   int
	Error          string
	Message        string
}

// routerPanelPermissionOptions is the ordered permission-policy vocabulary the
// panel cycles through. Ordinary selection deliberately excludes `interactive`:
// a Router worker that blocks on a UI decision would stall the lead.
var routerPanelPermissionOptions = []config.ACPPermissionPolicy{
	config.ACPPermissionAutoAllowOnce,
	config.ACPPermissionAutoReject,
}

// routerPanelRoleOptions is the fixed role vocabulary, in display order.
var routerPanelRoleOptions = []config.ACPAgentRole{
	config.ACPRoleInvestigate,
	config.ACPRoleImplement,
	config.ACPRoleTest,
	config.ACPRoleReview,
	config.ACPRoleIntegrate,
}

func (a *App) openRouterPanel() {
	// Viewing and editing configuration is safe while a run is active: the panel
	// never touches Runtime resources, and activation itself is fenced by the
	// shared Runtime (idle check + source policy). Do not add an adapter-local
	// "running" gate here.
	a.input = a.input.Blur()
	a.routerPanel = routerPanelState{Open: true, EditingIndex: -1, Roles: map[config.ACPAgentRole]bool{}}
	a.loadRouterPanelProfiles()
	a.scheduleRender()
}

func (a *App) closeRouterPanel() {
	a.routerPanel = routerPanelState{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

// loadRouterPanelProfiles reloads the durable profile list. The catalog of
// eligible workers is resolved separately through the shared Runtime so the
// panel's eligibility column can never drift from Runtime policy.
func (a *App) loadRouterPanelProfiles() {
	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelLoadFailed, err)
		return
	}
	a.routerPanel.Profiles = append([]config.ACPAgentProfile(nil), settings.ACPAgents...)
	if a.routerPanel.Cursor >= len(a.routerPanel.Profiles) {
		a.routerPanel.Cursor = max(0, len(a.routerPanel.Profiles)-1)
	}
	a.routerPanel.Error = ""
	a.routerPanel.Message = ""
}

// routerPanelDescriptors projects the shared Runtime catalog for the current
// in-panel profile list. It resolves through the same function the management
// projection uses, so "eligible" here means exactly what Runtime would accept.
func (a *App) routerPanelDescriptors() map[string]agentruntime.ACPWorkerProfileDescriptor {
	descriptors := agentruntime.ListACPWorkerProfiles(&config.Settings{ACPAgents: a.routerPanel.Profiles})
	byID := make(map[string]agentruntime.ACPWorkerProfileDescriptor, len(descriptors))
	for _, descriptor := range descriptors {
		byID[descriptor.ID] = descriptor
	}
	return byID
}

// routerPanelActiveProfile returns the worker ID this session is bound to, if
// any. The binding is Runtime-owned session state, never panel state.
func (a *App) routerPanelActiveProfile() string {
	if a.runtime == nil {
		return ""
	}
	binding, ok := a.runtime.RouterBinding()
	if !ok {
		return ""
	}
	return binding.ACPProfileID
}

func (a *App) beginRouterPanelCreate() {
	// Start with the well-known-agent picker: the most common configuration
	// mistake is assuming `codex acp` / `claude acp` start an ACP server, so the
	// user should not have to remember each tool's launch recipe.
	a.routerPanel.ChoosingPreset = true
	a.routerPanel.PresetCursor = 0
	a.routerPanel.Error = ""
	a.routerPanel.Message = ""
	a.routerPanel.ConfirmDelete = false
	a.routerPanel.Editing = true
	a.routerPanel.EditingIndex = -1
	a.routerPanel.Draft = config.ACPAgentProfile{Enabled: true, PermissionPolicy: config.ACPPermissionAutoReject}
	a.routerPanel.Roles = map[config.ACPAgentRole]bool{config.ACPRoleInvestigate: true}
	a.routerPanel.FormCursor = 0
	a.routerPanel.InputField = ""
}

// beginRouterPanelCreateFromPreset fills the draft from a known launch recipe.
func (a *App) beginRouterPanelCreateFromPreset(preset agentruntime.ACPAgentPreset) {
	a.routerPanel.ChoosingPreset = false
	a.routerPanel.Draft = agentruntime.ACPAgentProfileFromPreset(preset)
	a.routerPanel.Roles = map[config.ACPAgentRole]bool{config.ACPRoleInvestigate: true}
	a.routerPanel.FormCursor = 0
	a.routerPanel.InputField = ""
	a.routerPanel.Error = ""
	a.routerPanel.Message = preset.Note
}

// beginRouterPanelCreateFromScratch opens an empty form for a custom agent.
func (a *App) beginRouterPanelCreateFromScratch() {
	a.routerPanel.ChoosingPreset = false
	a.routerPanel.Draft = config.ACPAgentProfile{Enabled: true, PermissionPolicy: config.ACPPermissionAutoReject}
	a.routerPanel.Roles = map[config.ACPAgentRole]bool{config.ACPRoleInvestigate: true}
	a.routerPanel.FormCursor = 0
	a.routerPanel.InputField = ""
	a.routerPanel.Error = ""
	a.routerPanel.Message = ""
}

func (a *App) beginRouterPanelEdit(index int) {
	if index < 0 || index >= len(a.routerPanel.Profiles) {
		return
	}
	a.routerPanel.Editing = true
	a.routerPanel.EditingIndex = index
	a.routerPanel.Draft = a.routerPanel.Profiles[index]
	a.routerPanel.Roles = map[config.ACPAgentRole]bool{}
	for _, role := range a.routerPanel.Draft.EffectiveRoles() {
		a.routerPanel.Roles[role] = true
	}
	a.routerPanel.FormCursor = 0
	a.routerPanel.InputField = ""
	a.routerPanel.Error = ""
}

func (a *App) closeRouterPanelForm() {
	a.routerPanel.Editing = false
	a.routerPanel.EditingIndex = -1
	// The preset picker is part of the create flow, so leaving the form must also
	// leave the picker. Otherwise Esc appears to do nothing: the picker still
	// swallows every key.
	a.routerPanel.ChoosingPreset = false
	a.routerPanel.PresetCursor = 0
	a.routerPanel.Draft = config.ACPAgentProfile{}
	a.routerPanel.Roles = map[config.ACPAgentRole]bool{}
	a.routerPanel.FormCursor = 0
	a.routerPanel.InputField = ""
	a.routerPanel.Input = editor.Model{}
	a.routerPanel.Error = ""
}

type routerPanelField struct {
	Key         string
	Title       string
	Description string
}

func (a *App) routerPanelFormFields() []routerPanelField {
	draft := a.routerPanel.Draft
	roles := a.routerPanelRolesValue()
	enabled := a.translator.Text(i18n.MsgRouterPanelValueOff)
	if draft.Enabled {
		enabled = a.translator.Text(i18n.MsgRouterPanelValueOn)
	}
	terminal := a.translator.Text(i18n.MsgRouterPanelValueOff)
	if draft.TerminalAccess {
		terminal = a.translator.Text(i18n.MsgRouterPanelValueOn)
	}
	parallelWrites := a.translator.Text(i18n.MsgRouterPanelValueOff)
	if draft.AllowParallelBaseWorkspaceWrites {
		parallelWrites = a.translator.Text(i18n.MsgRouterPanelValueOn)
	}
	unknown := a.translator.Text(i18n.MsgRouterPanelValueUnset)
	fields := []routerPanelField{
		{Key: "id", Title: a.translator.Text(i18n.MsgRouterPanelFieldID), Description: valueOrDefault(draft.ID, a.translator.Text(i18n.MsgRouterPanelValueRequired))},
		{Key: "title", Title: a.translator.Text(i18n.MsgRouterPanelFieldTitle), Description: valueOrDefault(draft.Title, unknown)},
		{Key: "transport", Title: a.translator.Text(i18n.MsgRouterPanelFieldTransport), Description: string(draft.EffectiveTransport())},
		{Key: "url", Title: a.translator.Text(i18n.MsgRouterPanelFieldURL), Description: valueOrDefault(draft.URL, unknown)},
		{Key: "command", Title: a.translator.Text(i18n.MsgRouterPanelFieldCommand), Description: valueOrDefault(strings.Join(draft.Command, " "), a.translator.Text(i18n.MsgRouterPanelValueRequired))},
		{Key: "environment", Title: a.translator.Text(i18n.MsgRouterPanelFieldEnvironment), Description: valueOrDefault(strings.Join(draft.Environment, ", "), unknown)},
		{Key: "permission", Title: a.translator.Text(i18n.MsgRouterPanelFieldPermission), Description: valueOrDefault(string(draft.PermissionPolicy), string(config.ACPPermissionAutoReject))},
		{Key: "roles", Title: a.translator.Text(i18n.MsgRouterPanelFieldRoles), Description: roles},
		{Key: "writeRoots", Title: a.translator.Text(i18n.MsgRouterPanelFieldWriteRoots), Description: valueOrDefault(strings.Join(draft.WriteRoots, ", "), unknown)},
		{Key: "terminal", Title: a.translator.Text(i18n.MsgRouterPanelFieldTerminal), Description: terminal},
		{Key: "parallelWrites", Title: a.translator.Text(i18n.MsgRouterPanelFieldParallelWrites), Description: parallelWrites},
		{Key: "enabled", Title: a.translator.Text(i18n.MsgRouterPanelFieldEnabled), Description: enabled},
		{Key: "save", Title: a.translator.Text(i18n.MsgRouterPanelFieldSave), Description: a.translator.Text(i18n.MsgRouterPanelFieldSaveHint)},
		{Key: "cancel", Title: a.translator.Text(i18n.MsgRouterPanelFieldCancel), Description: a.translator.Text(i18n.MsgRouterPanelFieldCancelHint)},
	}
	return fields
}

func (a *App) routerPanelRolesValue() string {
	names := make([]string, 0, len(routerPanelRoleOptions))
	for _, role := range routerPanelRoleOptions {
		if a.routerPanel.Roles[role] {
			names = append(names, string(role))
		}
	}
	if len(names) == 0 {
		return a.translator.Text(i18n.MsgRouterPanelValueNone)
	}
	return strings.Join(names, ", ")
}

func (a *App) beginRouterPanelFieldInput(field string) {
	a.routerPanel.InputField = field
	a.routerPanel.Error = ""
	a.routerPanel.Input = editor.New(max(28, a.width-8)).SetMaxLines(1).
		SetPlaceholder(a.routerPanelFieldPlaceholder(field)).
		SetValue(a.routerPanelFieldValue(field)).Focus()
}

func (a *App) routerPanelFieldPlaceholder(field string) string {
	switch field {
	case "id":
		return "codex (lowercase ascii, digits, - and _)"
	case "title":
		return "Codex"
	case "transport":
		return "stdio | websocket | http"
	case "url":
		return "wss://agent.example/acp   (remote transports only)"
	case "command":
		return "opencode acp   (or /absolute/path/to/worker acp)"
	case "environment":
		return "NAME, NAME (never NAME=value)"
	case "writeRoots":
		return "src, internal (relative to the session directory)"
	default:
		return field
	}
}

func (a *App) routerPanelFieldValue(field string) string {
	draft := a.routerPanel.Draft
	switch field {
	case "id":
		return draft.ID
	case "title":
		return draft.Title
	case "transport":
		return string(draft.EffectiveTransport())
	case "url":
		return draft.URL
	case "command":
		return strings.Join(draft.Command, " ")
	case "environment":
		return strings.Join(draft.Environment, ", ")
	case "writeRoots":
		return strings.Join(draft.WriteRoots, ", ")
	}
	return ""
}

func (a *App) submitRouterPanelFieldInput() {
	field := a.routerPanel.InputField
	value := strings.TrimSpace(a.routerPanel.Input.Value())
	draft := &a.routerPanel.Draft
	switch field {
	case "id":
		draft.ID = value
	case "title":
		draft.Title = value
	case "transport":
		draft.Transport = strings.ToLower(value)
	case "url":
		draft.URL = value
	case "command":
		// The command is argv: split on whitespace, never shell-expanded.
		draft.Command = strings.Fields(value)
	case "environment":
		draft.Environment = splitRouterPanelList(value)
	case "writeRoots":
		draft.WriteRoots = splitRouterPanelList(value)
	}
	a.routerPanel.InputField = ""
	a.routerPanel.Input = editor.Model{}
}

// splitRouterPanelList parses a comma or whitespace separated list.
func splitRouterPanelList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	items := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// routerPanelDraftProfiles returns the candidate list a save would persist:
// the draft replaces or appends to the current list.
func (a *App) routerPanelDraftProfiles() []config.ACPAgentProfile {
	draft := a.routerPanel.Draft
	draft.ID = strings.TrimSpace(draft.ID)
	draft.Title = strings.TrimSpace(draft.Title)
	draft.PermissionPolicy = a.routerPanelEffectivePermission(draft.PermissionPolicy)
	roles := make([]config.ACPAgentRole, 0, len(routerPanelRoleOptions))
	for _, role := range routerPanelRoleOptions {
		if a.routerPanel.Roles[role] {
			roles = append(roles, role)
		}
	}
	draft.Roles = roles

	profiles := append([]config.ACPAgentProfile(nil), a.routerPanel.Profiles...)
	if a.routerPanel.EditingIndex < 0 {
		return append(profiles, draft)
	}
	profiles[a.routerPanel.EditingIndex] = draft
	return profiles
}

// routerPanelEffectivePermission keeps an explicitly configured `interactive`
// policy visible when editing an existing profile; the panel only cycles the two
// non-blocking values for ordinary selection.
func (a *App) routerPanelEffectivePermission(current config.ACPPermissionPolicy) config.ACPPermissionPolicy {
	for _, option := range routerPanelPermissionOptions {
		if option == current {
			return current
		}
	}
	if current == config.ACPPermissionInteractive {
		return current
	}
	return config.ACPPermissionAutoReject
}

func (a *App) saveRouterPanelDraft() {
	profiles := a.routerPanelDraftProfiles()
	// A profile is executable-code trust: the shared config owner validates the
	// complete candidate set (IDs, absolute argv, env names, roles/writeRoots,
	// concurrency, duplicates) before anything is written.
	if err := config.ValidateACPAgentProfileSet(profiles); err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelInvalidProfile, err)
		return
	}
	if err := config.SaveACPAgentProfiles(profiles); err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelSaveFailed, err)
		return
	}
	// Reload through the same shared loader the Runtime uses, so the panel and
	// the next activation observe one value.
	a.refreshSettingsAfterRouterPanelSave()
	a.closeRouterPanelForm()
	a.loadRouterPanelProfiles()
	a.routerPanel.Message = a.translator.Text(i18n.MsgRouterPanelSaved)
}

// refreshSettingsAfterRouterPanelSave reloads global settings into the adapter
// and rebuilds the Runtime-owned agent manager so the newly configured profile
// catalog is visible to the next activation without restarting.
func (a *App) refreshSettingsAfterRouterPanelSave() {
	effective, err := config.LoadSettings()
	if err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelLoadFailed, err)
		return
	}
	a.settings = effective
	a.syncAgentManagerRuntime()
}

// activateRouterFromPanel runs the one shared activation path. The panel only
// supplies the selected profile ID; Runtime validates eligibility, the idle
// fence, the source policy, and the approval strategy.
func (a *App) activateRouterFromPanel() {
	profile := a.routerPanel.Profiles[a.routerPanel.Cursor]
	if strings.TrimSpace(profile.ID) == "" {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelInvalidProfile, fmt.Errorf("empty profile id"))
		return
	}
	if err := a.ensureSession(); err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgCommandRouterFailed, err)
		return
	}
	runtime, err := a.expertRuntime()
	if err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgCommandRouterFailed, err)
		return
	}
	sessionDir := ""
	if a.session != nil {
		sessionDir = a.session.GetSessionDir()
	}
	if err := runtime.ActivateRouter(context.Background(), sessionDir, a.settings, agentruntime.RouterBinding{ACPProfileID: profile.ID}); err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgCommandRouterFailed, err)
		return
	}
	// Activation already persisted Router mode through the shared Runtime; mirror
	// that resolved value instead of writing the adapter field.
	a.adoptRuntimeMode()
	a.syncToolGroups()
	a.resetAgent(fmt.Errorf("router activated"))
	a.closeRouterPanel()
	a.addCommandStatus(a.translator.Text(i18n.MsgCommandRouterActivated, profile.ID))
	a.addCommandStatus(a.translator.Text(i18n.MsgCommandPermissionsRouter))
}

func (a *App) handleRouterPanelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.routerPanel.Open {
		return false, nil
	}
	if a.routerPanel.InputField != "" {
		switch msg.Type {
		case tea.KeyEsc:
			a.routerPanel.InputField = ""
			a.routerPanel.Input = editor.Model{}
		case tea.KeyEnter:
			a.submitRouterPanelFieldInput()
		default:
			var cmd tea.Cmd
			a.routerPanel.Input, cmd = a.routerPanel.Input.Update(msg)
			a.scheduleRender()
			return true, cmd
		}
		a.scheduleRender()
		return true, nil
	}
	if a.routerPanel.ChoosingColon {
		switch {
		case msg.Type == tea.KeyEsc:
			a.routerPanel.ChoosingColon = false
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.routerPanel.ColonCursor = max(0, a.routerPanel.ColonCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.routerPanel.ColonCursor = min(len(routerPanelPermissionOptions)-1, a.routerPanel.ColonCursor+1)
		case msg.Type == tea.KeyEnter:
			a.routerPanel.Draft.PermissionPolicy = routerPanelPermissionOptions[a.routerPanel.ColonCursor]
			a.routerPanel.ChoosingColon = false
		}
		a.scheduleRender()
		return true, nil
	}
	if a.routerPanel.ChoosingRole {
		switch {
		case msg.Type == tea.KeyEsc:
			a.routerPanel.ChoosingRole = false
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.routerPanel.RoleCursor = max(0, a.routerPanel.RoleCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.routerPanel.RoleCursor = min(len(routerPanelRoleOptions)-1, a.routerPanel.RoleCursor+1)
		case msg.Type == tea.KeySpace || msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "t"):
			a.toggleRouterPanelRole(routerPanelRoleOptions[a.routerPanel.RoleCursor])
		}
		a.scheduleRender()
		return true, nil
	}
	if a.routerPanel.ChoosingPreset {
		presets := agentruntime.ACPAgentPresets()
		// One extra row at the end opens a blank form for a custom agent.
		last := len(presets)
		switch {
		case msg.Type == tea.KeyEsc:
			a.closeRouterPanelForm()
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.routerPanel.PresetCursor = max(0, a.routerPanel.PresetCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.routerPanel.PresetCursor = min(last, a.routerPanel.PresetCursor+1)
		case msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "e"):
			if a.routerPanel.PresetCursor == last {
				a.beginRouterPanelCreateFromScratch()
			} else {
				a.beginRouterPanelCreateFromPreset(presets[a.routerPanel.PresetCursor])
			}
		}
		a.scheduleRender()
		return true, nil
	}
	if a.routerPanel.Editing {
		fields := a.routerPanelFormFields()
		switch {
		case msg.Type == tea.KeyEsc:
			a.closeRouterPanelForm()
		case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
			a.routerPanel.FormCursor = max(0, a.routerPanel.FormCursor-1)
		case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
			a.routerPanel.FormCursor = min(len(fields)-1, a.routerPanel.FormCursor+1)
		case msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "e"):
			if a.routerPanel.FormCursor < 0 || a.routerPanel.FormCursor >= len(fields) {
				break
			}
			switch fields[a.routerPanel.FormCursor].Key {
			case "permission":
				a.routerPanel.ChoosingColon = true
				a.routerPanel.ColonCursor = 0
				for i, option := range routerPanelPermissionOptions {
					if option == a.routerPanel.Draft.PermissionPolicy {
						a.routerPanel.ColonCursor = i
					}
				}
			case "roles":
				a.routerPanel.ChoosingRole = true
				a.routerPanel.RoleCursor = 0
			case "enabled":
				a.routerPanel.Draft.Enabled = !a.routerPanel.Draft.Enabled
			case "terminal":
				a.routerPanel.Draft.TerminalAccess = !a.routerPanel.Draft.TerminalAccess
			case "parallelWrites":
				a.routerPanel.Draft.AllowParallelBaseWorkspaceWrites = !a.routerPanel.Draft.AllowParallelBaseWorkspaceWrites
			case "save":
				a.saveRouterPanelDraft()
			case "cancel":
				a.closeRouterPanelForm()
			default:
				a.beginRouterPanelFieldInput(fields[a.routerPanel.FormCursor].Key)
			}
		}
		a.scheduleRender()
		return true, nil
	}
	if a.routerPanel.ConfirmDelete {
		switch {
		case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "n")):
			a.routerPanel.ConfirmDelete = false
		case msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y"):
			a.deleteRouterPanelProfile()
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
		a.closeRouterPanel()
	case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
		a.routerPanel.Cursor = max(0, a.routerPanel.Cursor-1)
	case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
		a.routerPanel.Cursor = min(len(a.routerPanel.Profiles)-1, a.routerPanel.Cursor+1)
	case msg.Type == tea.KeyEnter || (msg.Type == tea.KeyRunes && string(msg.Runes) == "e"):
		a.beginRouterPanelEdit(a.routerPanel.Cursor)
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "a":
		a.beginRouterPanelCreate()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "t":
		a.activateRouterFromPanel()
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "d":
		if a.routerPanel.Cursor < len(a.routerPanel.Profiles) {
			a.routerPanel.ConfirmDelete = true
		}
	case msg.Type == tea.KeyRunes && string(msg.Runes) == "r":
		a.loadRouterPanelProfiles()
	}
	a.scheduleRender()
	return true, nil
}

// toggleRouterPanelRole flips one role. It always leaves at least one role
// selected so a profile can never be saved with an empty ceiling.
func (a *App) toggleRouterPanelRole(role config.ACPAgentRole) {
	next := !a.routerPanel.Roles[role]
	if !next && len(a.selectedRouterPanelRoles()) <= 1 {
		a.routerPanel.Message = a.translator.Text(i18n.MsgRouterPanelRoleRequired)
		return
	}
	a.routerPanel.Roles[role] = next
	a.routerPanel.Message = ""
}

func (a *App) selectedRouterPanelRoles() []config.ACPAgentRole {
	roles := make([]config.ACPAgentRole, 0, len(routerPanelRoleOptions))
	for _, role := range routerPanelRoleOptions {
		if a.routerPanel.Roles[role] {
			roles = append(roles, role)
		}
	}
	return roles
}

func (a *App) deleteRouterPanelProfile() {
	if a.routerPanel.Cursor < 0 || a.routerPanel.Cursor >= len(a.routerPanel.Profiles) {
		a.routerPanel.ConfirmDelete = false
		return
	}
	profiles := append([]config.ACPAgentProfile(nil), a.routerPanel.Profiles...)
	profiles = append(profiles[:a.routerPanel.Cursor], profiles[a.routerPanel.Cursor+1:]...)
	if err := config.SaveACPAgentProfiles(profiles); err != nil {
		a.routerPanel.Error = a.translator.Text(i18n.MsgRouterPanelSaveFailed, err)
		a.routerPanel.ConfirmDelete = false
		return
	}
	a.refreshSettingsAfterRouterPanelSave()
	a.loadRouterPanelProfiles()
	if a.routerPanel.Cursor >= len(a.routerPanel.Profiles) {
		a.routerPanel.Cursor = max(0, len(a.routerPanel.Profiles)-1)
	}
	a.routerPanel.ConfirmDelete = false
	a.routerPanel.Message = a.translator.Text(i18n.MsgRouterPanelDeleted)
}

func (a *App) renderRouterPanel() string {
	width := max(42, a.width-4)
	height := max(8, a.height-lipgloss.Height(a.renderFooter())-5)
	descriptors := a.routerPanelDescriptors()
	active := a.routerPanelActiveProfile()
	lines := []string{a.translator.Text(i18n.MsgRouterPanelTitle), strings.Repeat("-", width)}
	switch {
	case a.routerPanel.ChoosingPreset:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelChoosePreset)+":")
		presets := agentruntime.ACPAgentPresets()
		for i, preset := range presets {
			cursor := " "
			if i == a.routerPanel.PresetCursor {
				cursor = ">"
			}
			lines = append(lines, xansi.Truncate(fmt.Sprintf("%s %-14s %-8s %s", cursor, preset.ID, strings.Join(preset.Command, " "), preset.Note), width, "..."))
		}
		cursor := " "
		if a.routerPanel.PresetCursor == len(presets) {
			cursor = ">"
		}
		lines = append(lines, fmt.Sprintf("%s %s", cursor, a.translator.Text(i18n.MsgRouterPanelPresetCustom)))
		lines = append(lines, "", a.translator.Text(i18n.MsgRouterPanelPresetHint))
	case a.routerPanel.InputField != "":
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelSetField, a.routerPanel.InputField)+":", a.routerPanel.Input.View(), "", a.translator.Text(i18n.MsgRouterPanelInputHint))
	case a.routerPanel.ChoosingColon:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelChoosePermission)+":")
		for i, option := range routerPanelPermissionOptions {
			cursor := " "
			if i == a.routerPanel.ColonCursor {
				cursor = ">"
			}
			lines = append(lines, fmt.Sprintf("%s %s", cursor, option))
		}
		lines = append(lines, "", a.translator.Text(i18n.MsgRouterPanelChooseHint))
	case a.routerPanel.ChoosingRole:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelChooseRole)+":")
		for i, role := range routerPanelRoleOptions {
			cursor := " "
			if i == a.routerPanel.RoleCursor {
				cursor = ">"
			}
			state := "[ ]"
			if a.routerPanel.Roles[role] {
				state = "[x]"
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", cursor, state, role))
		}
		lines = append(lines, "", a.translator.Text(i18n.MsgRouterPanelChooseRoleHint))
	case a.routerPanel.Editing:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelConfigure))
		for i, field := range a.routerPanelFormFields() {
			cursor := " "
			if i == a.routerPanel.FormCursor {
				cursor = ">"
			}
			lines = append(lines, xansi.Truncate(fmt.Sprintf("%s %-16s %s", cursor, field.Title, field.Description), width, "..."))
		}
		lines = append(lines, "", a.translator.Text(i18n.MsgRouterPanelFormHint))
	case a.routerPanel.ConfirmDelete:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelConfirmDelete, a.routerPanel.Profiles[a.routerPanel.Cursor].ID))
	default:
		lines = append(lines, a.translator.Text(i18n.MsgRouterPanelKeys))
		if len(a.routerPanel.Profiles) == 0 {
			lines = append(lines, a.translator.Text(i18n.MsgRouterPanelEmpty))
		}
		for i, profile := range a.routerPanel.Profiles {
			cursor := " "
			if i == a.routerPanel.Cursor {
				cursor = ">"
			}
			descriptor := descriptors[profile.ID]
			state := a.translator.Text(i18n.MsgRouterPanelStateIneligible)
			switch {
			case descriptor.InvalidReason != "":
				state = a.translator.Text(i18n.MsgRouterPanelStateInvalid)
			case descriptor.RouterEligible:
				state = a.translator.Text(i18n.MsgRouterPanelStateEligible)
			case descriptor.PermissionPolicy == config.ACPPermissionInteractive:
				// Interactive profiles are valid configuration but cannot be a
				// Router worker, so do not report them as merely "disabled".
				state = a.translator.Text(i18n.MsgRouterPanelStateInteractive)
			}
			if profile.ID == active && active != "" {
				state += " " + a.translator.Text(i18n.MsgRouterPanelStateActive)
			}
			label := profile.Title
			if strings.TrimSpace(label) == "" {
				label = profile.ID
			}
			lines = append(lines, xansi.Truncate(fmt.Sprintf("%s %-18s %-12s %s", cursor, profile.ID, state, label), width, "..."))
		}
	}
	if a.routerPanel.Message != "" {
		lines = append(lines, "", a.routerPanel.Message)
	}
	if a.routerPanel.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.routerPanel.Error))
	}
	if len(lines) > height {
		lines = append(lines[:height-1], "...")
	}
	return toolModalStyle.Width(width).Height(height + 2).Render(strings.Join(lines, "\n"))
}
