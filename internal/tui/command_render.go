package tui

import (
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/tui/i18n"
)

type commandOutputKind int

const (
	commandOutputStatus commandOutputKind = iota
	commandOutputError
)

type commandOutput struct {
	kind  commandOutputKind
	lines []string
}

func (a *App) addCommandStatus(lines ...string) {
	a.addCommandOutput(commandOutput{kind: commandOutputStatus, lines: lines})
}

func (a *App) addCommandError(lines ...string) {
	a.addCommandOutput(commandOutput{kind: commandOutputError, lines: lines})
}

func (a *App) addCommandOutput(out commandOutput) {
	text := strings.Join(out.lines, "\n")
	switch out.kind {
	case commandOutputError:
		a.addMessage(errorStyle.Render(text))
	default:
		a.addMessage(statusStyle.Render(text))
	}
}

func commandUsage(tr i18n.Translator, syntax string) string {
	return tr.Text(i18n.MsgCommandUsage, syntax)
}

func commandHelpText(translators ...i18n.Translator) string {
	tr := i18n.New(i18n.LanguageEN)
	if len(translators) > 0 {
		tr = translators[0]
	}
	lines := []string{tr.Text(i18n.MsgCommandsTitle)}
	for _, spec := range commandSpecs {
		lines = append(lines, fmt.Sprintf("  %-50s - %s", spec.Usage, tr.Text(spec.Description)))
	}
	lines = append(lines, "", tr.Text(i18n.MsgKeyboardShortcutsTitle))
	shortcut := func(key string, id i18n.MessageID) string {
		return fmt.Sprintf("  %-18s - %s", key, tr.Text(id))
	}
	lines = append(lines,
		shortcut("Enter", i18n.MsgShortcutSubmitInput),
		shortcut("Alt+Enter/Ctrl+J", i18n.MsgShortcutInsertNewline),
		shortcut("Tab", i18n.MsgShortcutCycleMode),
		shortcut("Esc", i18n.MsgShortcutAbort),
		shortcut("Ctrl+O", i18n.MsgShortcutToolDetails),
		shortcut("Ctrl+E", i18n.MsgShortcutESMProgress),
		shortcut("Ctrl+R", i18n.MsgShortcutPreviewImage),
		shortcut("Ctrl+G", i18n.MsgShortcutCompactTools),
		shortcut("Up/Down", i18n.MsgShortcutMoveHistory),
		shortcut("Left/Right", i18n.MsgShortcutSwitchDetailTarget),
		shortcut("PgUp/PgDn", i18n.MsgShortcutPagePanel),
	)
	return strings.Join(lines, "\n")
}
