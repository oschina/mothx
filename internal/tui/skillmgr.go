package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	browserfeature "github.com/oschina/mothx/internal/browser"
	"github.com/oschina/mothx/internal/tui/i18n"
	"github.com/oschina/mothx/internal/workflow"
)

// openSkillMgr launches the unified skill manager panel. It merges the former
// /skills listing and /skill <name> activation into one interactive overlay:
// Up/Down (or j/k) move the cursor, Space toggles a skill's activation, Enter
// applies the pending set, and Esc/q close without changes.
//
// The panel is a thin projection over the existing TUI activation path
// (activeSkills -> rebuildExtraContext -> resetAgent); it owns no skill
// discovery, context assembly, or enable/disable persistence of its own. The
// persisted enable/disable toggle (settings.skills.disabled) stays a settings
// concern, and feature-forced builtin skills (workflow/browser) are shown as
// locked because their activation is owned by feature flags, not this panel.
func (a *App) openSkillMgr() {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgSkillMgrBusy))
		return
	}
	if a.skillsMgr == nil {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillsUnavailable))
		return
	}
	items := a.skillsMgr.List()
	if len(items) == 0 {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillsEmpty))
		return
	}
	// Seed the pending selection from the currently active skills so the panel
	// opens reflecting reality; Space then toggles relative to that baseline.
	selected := make(map[string]bool, len(items))
	for _, s := range items {
		if _, ok := a.activeSkills[s.Name]; ok {
			selected[s.Name] = true
		}
	}
	// Feature-forced builtin skills are not user-togglable here: their
	// activation is owned by the workflow/browser feature flags.
	locked := make(map[string]bool, 2)
	if a.workflows {
		locked[workflow.SkillName] = true
	}
	if a.browserEnabled {
		locked[browserfeature.SkillName] = true
	}
	a.skillMgrItems = items
	a.skillMgrSelected = selected
	a.skillMgrLocked = locked
	a.skillMgrCursor = 0
	a.skillMgrMessage = ""
	a.skillMgrOpen = true
}

func (a *App) closeSkillMgr() {
	a.skillMgrOpen = false
	a.skillMgrItems = nil
	a.skillMgrSelected = nil
	a.skillMgrLocked = nil
	a.skillMgrCursor = 0
	a.skillMgrMessage = ""
}

func (a *App) handleSkillMgrKey(msg tea.KeyMsg) tea.Cmd {
	if !a.skillMgrOpen {
		return nil
	}
	switch {
	case msg.Type == tea.KeyCtrlC:
		a.finalizeForQuit()
		a.stopPrintLoop()
		return tea.Quit
	case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
		a.closeSkillMgr()
		return nil
	case msg.Type == tea.KeyUp || (msg.Type == tea.KeyRunes && string(msg.Runes) == "k"):
		if a.skillMgrCursor > 0 {
			a.skillMgrCursor--
		}
		return nil
	case msg.Type == tea.KeyDown || (msg.Type == tea.KeyRunes && string(msg.Runes) == "j"):
		if a.skillMgrCursor < len(a.skillMgrItems)-1 {
			a.skillMgrCursor++
		}
		return nil
	case msg.Type == tea.KeySpace:
		a.toggleSkillMgrCurrent()
		return nil
	case msg.Type == tea.KeyEnter:
		a.applySkillMgrSelection()
		return nil
	}
	return nil
}

func (a *App) toggleSkillMgrCurrent() {
	if a.skillMgrCursor < 0 || a.skillMgrCursor >= len(a.skillMgrItems) {
		return
	}
	name := a.skillMgrItems[a.skillMgrCursor].Name
	if a.skillMgrLocked[name] {
		a.skillMgrMessage = a.translator.Text(i18n.MsgSkillMgrLocked, name)
		return
	}
	if a.skillMgrSelected[name] {
		delete(a.skillMgrSelected, name)
	} else {
		a.skillMgrSelected[name] = true
	}
	a.skillMgrMessage = ""
}

// applySkillMgrSelection diffs the pending selection against the currently
// active skills and applies activations and deactivations in one pass, so the
// agent context is rebuilt (and the agent reset) at most once regardless of how
// many skills were toggled.
func (a *App) applySkillMgrSelection() {
	if a.skillsMgr == nil {
		a.closeSkillMgr()
		return
	}
	var activated, deactivated []string
	for _, s := range a.skillMgrItems {
		if a.skillMgrLocked[s.Name] {
			continue
		}
		want := a.skillMgrSelected[s.Name]
		_, has := a.activeSkills[s.Name]
		switch {
		case want && !has:
			a.activeSkills[s.Name] = a.skillsMgr.BuildSkillContext(s.Name)
			activated = append(activated, s.Name)
		case !want && has:
			delete(a.activeSkills, s.Name)
			deactivated = append(deactivated, s.Name)
		}
	}
	a.closeSkillMgr()
	if len(activated) == 0 && len(deactivated) == 0 {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillMgrNoChange))
		return
	}
	a.rebuildExtraContext()
	a.resetAgent(fmt.Errorf("skills updated"))
	switch {
	case len(activated) > 0 && len(deactivated) > 0:
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillMgrAppliedBoth, strings.Join(activated, ", "), strings.Join(deactivated, ", ")))
	case len(activated) > 0:
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillMgrAppliedOn, strings.Join(activated, ", ")))
	default:
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillMgrAppliedOff, strings.Join(deactivated, ", ")))
	}
}

func (a *App) renderSkillMgr() string {
	width := a.width - 4
	if width < 30 {
		width = 30
	}
	height := a.height - lipgloss.Height(a.renderFooter()) - 5
	if height < 5 {
		height = 5
	}
	hint := "↑↓/jk:move  space:toggle  enter:apply  esc:close"
	header := []string{"Skill Manager", xansi.Truncate(hint, width, "..."), strings.Repeat("-", width)}
	if a.skillMgrMessage != "" {
		header = append(header, xansi.Truncate(a.skillMgrMessage, width, "..."))
	}
	activeCount := 0
	for _, s := range a.skillMgrItems {
		if a.skillMgrSelected[s.Name] {
			activeCount++
		}
	}
	footerLines := []string{strings.Repeat("-", width), fmt.Sprintf("%d/%d active", activeCount, len(a.skillMgrItems))}

	capacity := height - len(header) - len(footerLines)
	if capacity < 1 {
		capacity = 1
	}
	start := 0
	if a.skillMgrCursor >= capacity {
		start = a.skillMgrCursor - capacity + 1
	}
	end := min(len(a.skillMgrItems), start+capacity)

	lines := append([]string{}, header...)
	for i := start; i < end; i++ {
		s := a.skillMgrItems[i]
		cursor := " "
		if i == a.skillMgrCursor {
			cursor = ">"
		}
		check := "[ ]"
		if a.skillMgrSelected[s.Name] {
			check = "[x]"
		}
		lock := ""
		if a.skillMgrLocked[s.Name] {
			lock = " (builtin)"
		}
		label := fmt.Sprintf("%s %s %s (%s)%s: %s", cursor, check, s.Name, s.Source, lock, s.Description)
		lines = append(lines, xansi.Truncate(label, width, "..."))
	}
	lines = append(lines, footerLines...)
	if len(lines) > height {
		lines = append(lines[:height-1], "...")
	}
	return toolModalStyle.Width(width).Height(height + 2).Render(strings.Join(lines, "\n"))
}
