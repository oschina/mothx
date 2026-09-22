package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/startvibecoding/GoStreamingMarkdown/gsm"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/tui/i18n"
	"github.com/oschina/mothx/internal/tui/renderutil"
)

// btwReadOnlyTools is the tool set granted to a /btw side-question sub-agent.
// It is intentionally read-only: no write/edit/bash.
var btwReadOnlyTools = []string{"read", "grep", "find", "ls", "skill_ref"}

// btwThinkMax bounds the think summary shown in the overlay.
const btwThinkMax = 500

// btwStreamStartMsg signals the /btw sub-agent has started streaming.
type btwStreamStartMsg struct {
	eventCh    <-chan agent.Event
	generation uint64
}

// btwEventMsg carries one event from the /btw sub-agent.
type btwEventMsg struct {
	event      agent.Event
	generation uint64
}

// btwDoneMsg signals the /btw sub-agent finished.
type btwDoneMsg struct {
	err        error
	generation uint64
}

// handleBtwCommand starts a side-question sub-agent that inherits the main
// task's conversation history (read-only) without writing anything back to the
// main session or context. The answer is shown in a floating overlay.
func (a *App) handleBtwCommand(cmd string) tea.Cmd {
	question := strings.TrimSpace(strings.TrimPrefix(cmd, "/btw"))
	if question == "" {
		a.addCommandStatus(commandUsage(a.translator, "/btw <question> — ask a side question without touching the main task"))
		return nil
	}
	if a.btwActive {
		a.addCommandError("A /btw query is already running. Close it (Esc) before starting another.")
		return nil
	}

	// Inherit the main task's history as a read-only snapshot, truncated to
	// bound the side-query's own token cost.
	var snapshot []provider.Message
	if a.agent != nil {
		snapshot = a.btwTruncateHistory(a.agent.GetMessages())
	}

	// Build a read-only registry sharing the main workdir/sandbox.
	roRegistry := tools.NewRegistryWithConfig(tools.RegistryConfig{
		WorkDir:    a.registry.GetWorkDir(),
		Sandbox:    a.registry.GetSandbox(),
		ToolFilter: btwReadOnlyTools,
		SkillsMgr:  a.skillsMgr,
	})

	extra := a.extraContext
	if extra != "" {
		extra += "\n\n"
	}
	extra += btwSystemHint

	if a.runtime == nil {
		runtime := tuiRuntime(a.session, a.registry, a.sandboxInfo, a.extraContext, a.ruleContent, a.skillsMgr, a.settings)
		if runtime == nil {
			a.addCommandError("Failed to build side query agent: TUI session runtime is unavailable")
			return nil
		}
		a.SetRuntime(runtime)
	}
	sub, err := a.runtime.BuildTransientAgent(roRegistry, agentruntime.AgentBuildOptions{
		ID: agentpkg.AgentID("btw"), Provider: a.provider, ProviderName: a.providerName,
		Model: a.model, Mode: agentruntime.ModeAgent, Settings: a.settings, Allow: a.allow,
		ExtraContext: extra, ThinkingLevel: provider.ThinkingLevel(a.settings.DefaultThinkingLevel),
	})
	if err != nil {
		a.addCommandError(fmt.Sprintf("Failed to build side query agent: %v", err))
		return nil
	}
	if len(snapshot) > 0 {
		sub.LoadHistoryMessages(snapshot)
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.btwCancel = cancel
	a.btwGeneration++
	generation := a.btwGeneration
	a.btwActive = true
	a.btwOpen = true
	a.btwQuestion = question
	a.btwAnswer = ""
	a.btwThink = ""
	a.btwErr = nil
	a.btwScroll = 0
	// Reset streaming render state for the new query.
	a.btwAnswerBuilder = &strings.Builder{}
	a.btwThinkBuilder = &strings.Builder{}
	a.btwRendered = ""
	a.btwRenderWidth = 0
	a.btwAnswerDirty = false
	a.btwRenderer = nil

	return func() tea.Msg {
		return btwStreamStartMsg{eventCh: sub.Run(ctx, question), generation: generation}
	}
}

const btwSystemHint = "[Side question mode] You are answering a quick side question for the user. " +
	"Treat the prior conversation as read-only context. Do NOT modify any files. " +
	"Your answer will be shown in a temporary overlay and will NOT be remembered by the main task. " +
	"Be concise and directly answer the question."

// btwTruncateHistory bounds the inherited snapshot so a long main history does
// not blow up the side-query's token cost. It keeps the earliest two messages
// (task setup) plus the most recent messages within budget, inserting a
// placeholder for the elided middle.
func (a *App) btwTruncateHistory(msgs []provider.Message) []provider.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// Budget in approx tokens (~4 chars/token). Default to half the model's
	// context window, with a sane floor.
	budgetTokens := 64000
	if a.model != nil && a.model.ContextWindow > 0 {
		budgetTokens = a.model.ContextWindow / 2
	}
	budgetChars := budgetTokens * 4

	total := 0
	for _, m := range msgs {
		total += btwMessageLen(m)
	}
	if total <= budgetChars {
		return msgs
	}

	const headKeep = 2
	head := msgs
	if len(msgs) > headKeep {
		head = msgs[:headKeep]
	} else {
		head = nil
	}
	headChars := 0
	for _, m := range head {
		headChars += btwMessageLen(m)
	}

	// Fill the tail from the end until the remaining budget is exhausted.
	remaining := budgetChars - headChars
	tailStart := len(msgs)
	for i := len(msgs) - 1; i >= headKeep; i-- {
		l := btwMessageLen(msgs[i])
		if remaining-l < 0 {
			break
		}
		remaining -= l
		tailStart = i
	}

	if tailStart <= headKeep {
		// Everything fits once head is accounted for; nothing elided.
		return msgs
	}

	out := make([]provider.Message, 0, len(head)+1+(len(msgs)-tailStart))
	out = append(out, head...)
	out = append(out, provider.NewUserMessage("[... earlier conversation omitted to keep the side question lightweight ...]"))
	out = append(out, msgs[tailStart:]...)
	return out
}

func btwMessageLen(m provider.Message) int {
	n := len(m.Content)
	for _, c := range m.Contents {
		n += len(c.Text)
	}
	return n
}

// listenBtwEvents consumes one event from the /btw channel.
func (a *App) listenBtwEvents(generation uint64) tea.Cmd {
	eventCh := a.btwEventCh
	return func() tea.Msg {
		if eventCh == nil {
			return btwDoneMsg{generation: generation}
		}
		var next agent.Event
		err := agent.ConsumeEvents(context.Background(), eventCh, agent.EventHandlerFunc(func(_ context.Context, event agent.Event) error {
			next = event
			return context.Canceled
		}))
		if next.Type != 0 || err == context.Canceled {
			return btwEventMsg{event: next, generation: generation}
		}
		return btwDoneMsg{err: err, generation: generation}
	}
}

// handleBtwEvent updates overlay state from a /btw event and returns a command
// to keep listening.
func (a *App) handleBtwEvent(event agent.Event) tea.Cmd {
	if !a.btwOpen || a.btwEventCh == nil {
		return nil
	}
	switch event.Type {
	case agent.EventTextDelta:
		if a.btwAnswerBuilder == nil {
			a.btwAnswerBuilder = &strings.Builder{}
		}
		a.btwAnswerBuilder.WriteString(event.TextDelta)
		a.btwAnswer = a.btwAnswerBuilder.String()
		a.btwAnswerDirty = true
		a.scheduleRender()
	case agent.EventThinkDelta:
		if a.btwThinkBuilder == nil {
			a.btwThinkBuilder = &strings.Builder{}
		}
		a.btwThinkBuilder.WriteString(event.ThinkDelta)
		think := a.btwThinkBuilder.String()
		if len(think) > btwThinkMax {
			think = think[len(think)-btwThinkMax:]
		}
		a.btwThink = think
		a.scheduleRender()
	case agent.EventError:
		a.btwErr = event.Error
		a.scheduleRender()
	case agent.EventDone, agent.EventAgentEnd, agent.EventRunFinished:
		// handled by btwDoneMsg as well; ignore here
	}
	return a.listenBtwEvents(a.btwGeneration)
}

// closeBtw closes the overlay and cancels any running side-query.
func (a *App) closeBtw() {
	a.btwGeneration++
	if a.btwCancel != nil {
		a.btwCancel()
		a.btwCancel = nil
	}
	a.btwOpen = false
	a.btwActive = false
	a.btwEventCh = nil
	a.btwAnswerBuilder = nil
	a.btwThinkBuilder = nil
	a.btwRenderer = nil
	a.btwRendered = ""
	a.btwRenderWidth = 0
	a.btwAnswerDirty = false
}

// scrollBtw adjusts the overlay scroll offset.
func (a *App) scrollBtw(delta int) {
	a.btwScroll += delta
	if a.btwScroll < 0 {
		a.btwScroll = 0
	}
	a.scheduleRender()
}

// renderBtwOverlay renders the floating layer for the /btw side question.
func (a *App) renderBtwOverlay() string {
	width := a.width - 4
	if width < 20 {
		width = 20
	}
	innerWidth := width - 2
	if innerWidth < 10 {
		innerWidth = 10
	}

	footerHeight := 0
	if a.height > 0 {
		footerHeight = lipgloss.Height(a.renderFooter())
	}
	// chrome: border(2) + padding(0) + title + divider + status line
	height := a.height - footerHeight - 5
	if height < 3 {
		height = 3
	}

	questionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	header := questionStyle.Render(a.translator.Text(i18n.MsgBTWTitle, truncatePlain(a.btwQuestion, innerWidth-10)))

	var bodyLines []string
	if a.btwThink != "" && a.btwAnswer == "" {
		wrappedThink := renderutil.WrapPlainText("\U0001F4AD "+strings.TrimSpace(a.btwThink), innerWidth)
		bodyLines = append(bodyLines, strings.Split(thinkStyle.Render(wrappedThink), "\n")...)
	}
	if a.btwAnswer != "" {
		body := a.btwAnswerBody(innerWidth)
		bodyLines = append(bodyLines, strings.Split(body, "\n")...)
	}
	if a.btwErr != nil {
		bodyLines = append(bodyLines, errorStyle.Render(a.translator.Text(i18n.MsgBTWError, activityFailureMessage(a.btwErr))))
	}
	if len(bodyLines) == 0 {
		if a.btwActive {
			bodyLines = append(bodyLines, statusStyle.Render(a.translator.Text(i18n.MsgBTWThinking)))
		} else {
			bodyLines = append(bodyLines, statusStyle.Render(a.translator.Text(i18n.MsgBTWNoAnswer)))
		}
	}

	// Clamp scroll and take the visible window.
	maxOffset := len(bodyLines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if a.btwScroll > maxOffset {
		a.btwScroll = maxOffset
	}
	end := a.btwScroll + height
	if end > len(bodyLines) {
		end = len(bodyLines)
	}
	visible := strings.Join(bodyLines[a.btwScroll:end], "\n")
	if visible == "" {
		visible = " "
	}

	state := "done"
	if a.btwActive {
		state = "running"
	}
	status := statusStyle.Render(a.translator.Text(i18n.MsgBTWStatus, state, a.btwScroll+1, end, len(bodyLines)))

	content := header + "\n" + strings.Repeat("\u2500", minInt(innerWidth, lipgloss.Width(header))) + "\n" + visible + "\n" + status
	return toolModalStyle.Width(width).Render(content)
}

// btwAnswerBody renders the accumulated answer, caching the wrapped/markdown
// output so View does not re-render every frame. Mirrors the main-agent
// assistant rendering (dirty-flag + streaming markdown renderer).
func (a *App) btwAnswerBody(innerWidth int) string {
	if !a.btwAnswerDirty && a.btwRenderWidth == innerWidth && a.btwRendered != "" {
		return a.btwRendered
	}

	raw := a.btwAnswer
	var out string
	if renderutil.LooksLikeMarkdown(raw) {
		mdWidth := renderutil.MarkdownStyleWrapWidth(innerWidth)
		if a.btwRenderer == nil || a.btwRenderWidth != innerWidth {
			a.btwRenderer = gsm.NewStream(mdWidth, nil)
		}
		a.btwRenderer.Update(raw)
		rendered := renderutil.TrimANSIBlankLines(a.btwRenderer.Output())
		if strings.TrimSpace(rendered) != "" {
			out = renderutil.WrapANSI(rendered, innerWidth)
		}
	}
	if out == "" {
		out = renderutil.WrapPlainText(raw, innerWidth)
	}

	a.btwRendered = out
	a.btwRenderWidth = innerWidth
	a.btwAnswerDirty = false
	return out
}
