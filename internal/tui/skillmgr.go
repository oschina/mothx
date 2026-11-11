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
// The panel is a thin projection over Runtime-owned activation state
// (SessionRuntime.ActiveSkills/ExtraContext); before the first session it only
// maintains a local draft. It owns no skill discovery, context assembly, or
// enable/disable persistence. The persisted enable/disable toggle
// (settings.skills.disabled) stays a settings concern, and feature-forced
// builtin skills (workflow/browser) are shown as locked because their
// activation is owned by feature flags, not this panel.
// skillLocked reports whether a skill's activation is owned by a feature flag
// rather than the per-session activation set. Base context already injects the
// corresponding skill when the feature is enabled.
func (a *App) skillLocked(name string) bool {
	return (a.workflows && name == workflow.SkillName) || (a.browserEnabled && name == browserfeature.SkillName)
}

func (a *App) openSkillMgr() {
	if !a.expertControlIdle() {
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
		if a.isSkillActive(s.Name) {
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
// many skills were toggled. The Runtime remains the single owner of the active
// set and the composed ExtraContext; this method only projects the panel.
func (a *App) applySkillMgrSelection() {
	if a.skillsMgr == nil {
		a.closeSkillMgr()
		return
	}
	if !a.expertControlIdle() {
		// A run or team member started after the panel opened. Do not reset the
		// Agent (which would cancel them) from a stale panel action.
		a.skillMgrMessage = a.translator.Text(i18n.MsgSkillMgrBusy)
		return
	}
	current := a.activeSkillNames()
	currentSet := skillNameSet(current)
	var activated, deactivated []string
	for _, s := range a.skillMgrItems {
		if a.skillMgrLocked[s.Name] {
			// Feature-forced builtins are injected by the base context and are not
			// part of the user activation set.
			continue
		}
		want := a.skillMgrSelected[s.Name]
		has := currentSet[s.Name]
		switch {
		case want && !has:
			activated = append(activated, s.Name)
		case !want && has:
			deactivated = append(deactivated, s.Name)
		}
	}
	if len(activated) == 0 && len(deactivated) == 0 {
		a.closeSkillMgr()
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillMgrNoChange))
		return
	}

	names := append([]string{}, current...)
	names = append(names, activated...)
	if len(deactivated) > 0 {
		removed := skillNameSet(deactivated)
		kept := names[:0]
		for _, name := range names {
			if !removed[name] {
				kept = append(kept, name)
			}
		}
		names = kept
	}
	if err := a.applyActiveSkills(names); err != nil {
		// Keep the panel open so the user can correct the selection.
		a.skillMgrMessage = err.Error()
		return
	}
	a.closeSkillMgr()
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
