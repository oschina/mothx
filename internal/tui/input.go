package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/provider"
	serviceruntime "github.com/startvibecoding/mothx/internal/serve/runtime"
	"github.com/startvibecoding/mothx/internal/session"
)

func (a *App) addMessage(msg string) {
	a.invalidateToolModalCache()
	idx := len(a.messages)
	a.messages = append(a.messages, msg)
	a.printMessageOnce(idx)
	a.updateViewportContent()
}

// addEventMessage keeps full-only lifecycle rows in the transcript so a
// later switch to full mode can replay them. Compact mode still omits them
// from the live view and terminal scrollback.
func (a *App) addEventMessage(msg string, visibleInCompact bool) {
	if visibleInCompact || !a.compactMode {
		a.addMessage(msg)
		return
	}
	a.invalidateToolModalCache()
	idx := len(a.messages)
	a.messages = append(a.messages, msg)
	if a.hiddenEventIdx == nil {
		a.hiddenEventIdx = make(map[int]bool)
	}
	a.hiddenEventIdx[idx] = true
	a.updateViewportContent()
}

func (a *App) printMessageOnce(idx int) {
	if a.printedMessageIdx == nil {
		a.printedMessageIdx = make(map[int]bool)
	}
	if idx < 0 || a.printedMessageIdx[idx] {
		return
	}
	rendered := strings.TrimRight(a.renderMessageAt(idx), "\n")
	if strings.TrimSpace(rendered) == "" {
		return
	}
	if a.program == nil {
		a.updateViewportContent()
		return
	}
	if a.printCond == nil {
		a.printCond = sync.NewCond(&a.printMu)
	}
	a.printMu.Lock()
	a.printQueue = append(a.printQueue, rendered)
	a.printCond.Signal()
	a.printMu.Unlock()
	a.printedMessageIdx[idx] = true
	a.updateViewportContent()
}

// printUnrenderedTranscript promotes events that were intentionally hidden by
// compact mode into terminal scrollback when the user switches to full mode.
// Active assistant/thinking/approval blocks stay in the managed viewport.
func (a *App) printUnrenderedTranscript() {
	for idx := range a.messages {
		if idx == a.currentThinkIdx || idx == a.currentAssistantIdx ||
			(a.waitingForApproval && idx == a.currentApprovalIdx) {
			continue
		}
		// Running tools remain live in the managed viewport. Printing them here
		// would leave a stale running row before the eventual result.
		if a.isToolMessageIndex(idx) && a.toolResultRunningAt(idx) {
			continue
		}
		a.printMessageOnce(idx)
	}
}

func (a *App) commitActiveStream() {
	hadActive := a.currentThinkIdx >= 0 || a.currentAssistantIdx >= 0
	if a.currentThinkIdx >= 0 {
		a.finalizeThinkStream(a.currentThinkIdx)
		a.printMessageOnce(a.currentThinkIdx)
	}
	if a.currentAssistantIdx >= 0 {
		a.finalizeAssistantStream(a.currentAssistantIdx)
		a.printMessageOnce(a.currentAssistantIdx)
	}
	if hadActive {
		a.currentThinkIdx = -1
		a.currentAssistantIdx = -1
		a.updateViewportContent()
	}
}

func (a *App) appendAssistantDelta(idx int, delta string) {
	if a.assistantBuilders == nil {
		a.assistantBuilders = make(map[int]*strings.Builder)
	}
	b := a.assistantBuilders[idx]
	if b == nil {
		b = &strings.Builder{}
		if existing := a.assistantRaw[idx]; existing != "" {
			b.WriteString(existing)
		}
		a.assistantBuilders[idx] = b
	}
	b.WriteString(delta)
	a.assistantRaw[idx] = b.String()
}

func (a *App) appendThinkDelta(idx int, delta string) {
	if a.thinkBuilders == nil {
		a.thinkBuilders = make(map[int]*strings.Builder)
	}
	b := a.thinkBuilders[idx]
	if b == nil {
		b = &strings.Builder{}
		if existing := a.thinkRaw[idx]; existing != "" {
			b.WriteString(existing)
		}
		a.thinkBuilders[idx] = b
	}
	b.WriteString(delta)
	a.thinkRaw[idx] = b.String()
}

func (a *App) finalizeAssistantStream(idx int) {
	if b := a.assistantBuilders[idx]; b != nil {
		a.assistantRaw[idx] = b.String()
		delete(a.assistantBuilders, idx)
	}
}

func (a *App) finalizeThinkStream(idx int) {
	if b := a.thinkBuilders[idx]; b != nil {
		a.thinkRaw[idx] = b.String()
		delete(a.thinkBuilders, idx)
	}
}

func (a *App) registerManagedAgent() {
	if !a.agentManagementEnabled() || a.agentMgr == nil || a.agent == nil {
		return
	}
	id := agentpkg.AgentID(a.agent.ID())
	if _, ok := a.agentMgr.Get(id); !ok {
		a.agentMgr.Register(agent.NewAgentAdapter(a.agent))
	}
	a.agentMgr.MarkRunning(id)
	a.activeAgent = id
}

func (a *App) finishManagedAgent(cause error) {
	if !a.agentManagementEnabled() || a.agentMgr == nil || a.agent == nil {
		return
	}
	id := a.agent.ID()
	a.agentMgr.Finish(id, cause)
	if a.activeAgent == id {
		a.activeAgent = ""
	}
}

// discardAgent drops the cached main Agent without touching the active run. Use
// it when the Agent instance itself can no longer be reused (for example after
// a Runtime-originated abort) and a fresh instance must be built.
func (a *App) discardAgent(cause error) {
	if a.agent == nil {
		return
	}
	a.finishManagedAgent(cause)
	a.agent = nil
	a.agentHistoryLoaded = false
}

func (a *App) resetAgent(cause error) {
	if a.run != nil {
		run := a.run
		run.cancel()
		run.finish(agentruntime.RunStateCancelled)
		a.run = nil
	}
	a.discardAgent(cause)
}

func (a *App) abortAndResetAgent(reason string) {
	a.finalizeAbortedRun()
	if a.agent != nil {
		a.agent.Abort()
	}
	a.resetAgent(errors.New(reason))
}

// finalizeAbortedRun commits the transcript of a run that is being canceled from
// the UI. An aborted run reports no further tool results, so the streaming
// assistant/thinking blocks and any tool row that was still running must be
// terminalized here: the running row would otherwise stay pinned in the managed
// viewport, and the streamed text would disappear when the next run starts
// streaming into a fresh message slot.
func (a *App) finalizeAbortedRun() {
	a.commitActiveStream()
	a.finalizeInterruptedTools()
}

func (a *App) finishRequestTimer() {
	if !a.requestStart.IsZero() {
		a.lastDuration = time.Since(a.requestStart)
		a.requestStart = time.Time{}
		return
	}
	if elapsed := a.timer.Elapsed(); elapsed > 0 {
		a.lastDuration = elapsed
	}
}

func (a *App) cycleMode() {
	switch a.mode {
	case "plan":
		a.mode = "agent"
	case "agent":
		a.mode = "yolo"
	case "yolo":
		a.mode = "os"
	case "os":
		a.mode = "plan"
	default:
		a.mode = "yolo"
	}

	if a.isThinking && a.agent != nil {
		a.pendingAbortReason = "mode change"
		a.abortAndResetAgent("mode changed")
		a.clearQueuedInput()
		a.isThinking = false
		a.finishRequestTimer()
		a.addMessage(statusStyle.Render("⏹ Aborted (mode change)"))
	} else if a.agent != nil {
		// Rebuild agent with new mode through the shared Runtime.
		oldMessages, oldMessageIDs := a.agent.GetHistoryState()
		a.finishManagedAgent(fmt.Errorf("mode changed"))
		runtimeAgent, err := a.buildRuntimeAgent()
		if err != nil {
			a.addCommandError(fmt.Sprintf("Failed to rebuild agent: %v", err))
			return
		}
		a.agent = runtimeAgent
		a.agent.LoadHistoryState(oldMessages, oldMessageIDs)
		a.registerManagedAgent()
	}

	var modeLabel string
	switch a.mode {
	case "plan":
		modeLabel = "🗒 PLAN - Read-only mode"
	case "agent":
		modeLabel = "🔧 AGENT - File edits, bash with approval"
	case "yolo":
		modeLabel = "🚀 YOLO - Full access"
	case "os":
		modeLabel = "🖥 OS - Bash only, no sandbox"
	}
	a.addMessage(statusStyle.Render(fmt.Sprintf("Mode: %s", modeLabel)))
}

func (a *App) recordInputHistory(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	if len(a.inputHistory) > 0 && a.inputHistory[len(a.inputHistory)-1] == input {
		a.resetInputHistoryNavigation()
		return
	}
	a.inputHistory = append(a.inputHistory, input)
	const maxInputHistory = 200
	if len(a.inputHistory) > maxInputHistory {
		a.inputHistory = a.inputHistory[len(a.inputHistory)-maxInputHistory:]
	}
	a.resetInputHistoryNavigation()
}

func (a *App) navigateInputHistory(direction int) bool {
	if a.waitingForApproval || len(a.inputHistory) == 0 {
		return false
	}

	switch {
	case direction < 0:
		if !a.inputHistoryBrowsing {
			a.inputHistoryDraft = a.input.Value()
			a.inputHistoryIndex = len(a.inputHistory) - 1
			a.inputHistoryBrowsing = true
		} else if a.inputHistoryIndex > 0 {
			a.inputHistoryIndex--
		}
	case direction > 0:
		if !a.inputHistoryBrowsing {
			return false
		}
		if a.inputHistoryIndex < len(a.inputHistory)-1 {
			a.inputHistoryIndex++
		} else {
			a.inputHistoryBrowsing = false
			a.inputHistoryIndex = 0
			a.input.SetValue(a.inputHistoryDraft)
			a.input.CursorEnd()
			a.inputHistoryDraft = ""
			a.scheduleRender()
			return true
		}
	default:
		return false
	}

	if a.inputHistoryIndex >= 0 && a.inputHistoryIndex < len(a.inputHistory) {
		a.input.SetValue(a.inputHistory[a.inputHistoryIndex])
		a.input.CursorEnd()
		a.scheduleRender()
		return true
	}
	return false
}

func (a *App) resetInputHistoryNavigation() {
	a.inputHistoryBrowsing = false
	a.inputHistoryIndex = 0
	a.inputHistoryDraft = ""
}

func (a *App) handleInputSubmit() tea.Cmd {
	if a.commandSuggestionsVisible() && a.commandNameInputActive() && a.applySelectedCommandSuggestion() {
		return nil
	}

	input := strings.TrimSpace(a.input.Value())

	// Check if waiting for a question
	if a.waitingForQuestion {
		if a.agent != nil {
			answer := strings.TrimSpace(input)
			if answer == "" {
				// Empty input — re-prompt
				a.input.Reset()
				a.resetInputHistoryNavigation()
				a.scheduleRender()
				return nil
			}

			// Resolve numbered selections to the actual option text so the
			// question tool result is meaningful to the model.
			var num int
			if _, err := fmt.Sscanf(answer, "%d", &num); err == nil && num > 0 && num <= len(a.currentQuestion.options) {
				answer = a.currentQuestion.options[num-1]
				a.agent.HandleQuestionResponse(a.pendingQuestionID, answer)
				a.addMessage(statusStyle.Render(fmt.Sprintf("✅ Selected: %s", answer)))
			} else {
				// Custom text input, including out-of-range numbers.
				a.agent.HandleQuestionResponse(a.pendingQuestionID, answer)
				a.addMessage(statusStyle.Render(fmt.Sprintf("✅ Answer: %s", answer)))
			}
		}
		// Show next queued question or clear waiting state
		if len(a.questionQueue) > 0 {
			a.showNextQuestion()
		} else {
			a.waitingForQuestion = false
			a.pendingQuestionID = ""
			a.currentQuestion = pendingQuestion{}
		}
		a.input.Reset()
		a.resetInputHistoryNavigation()
		a.scheduleRender()
		return nil
	}

	if input != "" {
		if a.manualCompactionActive {
			a.addCommandError("Cannot send input while context compaction is running.")
			return nil
		}
		a.input.Reset()
		a.suggest = a.suggest.SetItems(commandSuggestionItems(a.translator))
		a.suggest = a.suggest.Update("")
		a.recordInputHistory(input)
		expandedInput := a.expandPasteMarkers(input)
		return a.processInput(expandedInput)
	}
	return nil
}

func (a *App) processInput(input string) tea.Cmd {
	if a.manualCompactionActive {
		a.addCommandError("Cannot send input while context compaction is running.")
		return nil
	}
	// A session permits exactly one foreground execution at a time. Keep later
	// user submissions in the TUI instead of replacing a.run: replacing it
	// would orphan the active run's terminal cleanup and its runtime lease.
	if a.run != nil || a.isThinking {
		resources := a.pendingInputResources
		a.pendingInputResources = nil
		a.queuedPrompts = append(a.queuedPrompts, queuedPrompt{text: input, resources: resources})
		return nil
	}

	if strings.HasPrefix(input, "/") {
		return a.handleCommand(input)
	}

	if err := a.ensureSession(); err != nil {
		a.addCommandError(fmt.Sprintf("Error creating session: %v", err))
		return nil
	}
	if a.backgroundSubmitter != nil && providerResponsesBackgroundEnabled(a.provider) {
		return a.submitBackgroundInput(input)
	}
	if err := a.syncESMTools(); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to sync ESM tools: %v", err))
		return nil
	}
	a.run = newTUIRun(func() string {
		if a.session != nil && a.session.GetHeader() != nil {
			return a.session.GetHeader().ID
		}
		return ""
	}(), a.getSessionDir())
	if err := a.ensureRuntime(); err != nil {
		a.run = nil
		a.addCommandError(fmt.Sprintf("Failed to initialize session runtime: %v", err))
		return nil
	}
	if a.model != nil {
		a.run.model = a.model.ID
	}
	a.run.mode = a.mode
	a.run.workDir = a.currentCwd()
	a.runtime.SetExecution(a.run.execution)
	a.runtime.SetDecisions(a.run.decisions)

	// TUI text and staged clipboard resources follow the same Runtime-owned
	// input/content contract as every other frontend.
	var runInput agentruntime.InputSubmission
	var err error
	if len(a.pendingInputResources) > 0 {
		runInput, err = a.runtime.AttachPreparedInput(context.Background(), input, a.pendingInputResources)
	} else {
		runInput, err = a.runtime.AcceptInput(context.Background(), a.run.id, input, nil)
	}
	if err != nil {
		a.discardPendingInput()
		a.run = nil
		a.addCommandError(fmt.Sprintf("Failed to accept input: %v", err))
		return nil
	}
	a.pendingInputResources = nil
	artifacts, err := a.runtime.BeginArtifactCollection(a.run.id)
	if err != nil {
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run = nil
		a.addCommandError(fmt.Sprintf("Failed to initialize artifact publishing: %v", err))
		return nil
	}
	userMessage, err := a.runtime.BuildUserMessage(context.Background(), runInput)
	if err != nil {
		artifacts.Close()
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run = nil
		a.addCommandError(fmt.Sprintf("Failed to normalize input: %v", err))
		return nil
	}
	a.run.artifacts = artifacts
	a.prepareESMRun()
	a.ensureAgent()
	if a.agent == nil {
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run.finish(agentruntime.RunStateFailed)
		a.run = nil
		return nil
	}
	a.registerManagedAgent()
	run := a.run
	runtimeAgent := a.agent
	return func() tea.Msg {
		eventCh, err := run.start(context.Background(), runtimeAgent, runInput, userMessage)
		return agentStreamStartMsg{
			input:      input,
			submission: runInput,
			eventCh:    eventCh,
			err:        err,
			run:        run,
			compacting: false,
		}
	}
}

// startNextQueuedPrompt starts exactly one queued foreground submission after
// the preceding run has reached its canonical terminal state.
func (a *App) startNextQueuedPrompt() tea.Cmd {
	if a == nil || len(a.queuedPrompts) == 0 || a.run != nil || a.isThinking || a.manualCompactionActive {
		return nil
	}
	prompt := a.queuedPrompts[0]
	a.queuedPrompts = a.queuedPrompts[1:]
	a.pendingInputResources = prompt.resources
	return a.processInput(prompt.text)
}

func (a *App) scheduleNextQueuedPrompt() tea.Cmd {
	if a == nil || len(a.queuedPrompts) == 0 {
		return nil
	}
	return func() tea.Msg { return queuedPromptStartMsg{} }
}

func (a *App) submitBackgroundInput(input string) tea.Cmd {
	header := a.session.GetHeader()
	if header == nil || strings.TrimSpace(header.ID) == "" {
		a.addCommandError("Cannot submit background run without an active session.")
		return nil
	}
	modelID := ""
	if a.model != nil {
		modelID = a.model.ID
	}
	if err := a.ensureRuntime(); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to initialize session runtime: %v", err))
		return nil
	}
	runID := "tui_" + session.GenerateID()
	var runInput agentruntime.InputSubmission
	var err error
	if len(a.pendingInputResources) > 0 {
		runInput, err = a.runtime.AttachPreparedInput(context.Background(), input, a.pendingInputResources)
	} else {
		runInput, err = a.runtime.AcceptInput(context.Background(), runID, input, nil)
	}
	if err != nil {
		a.discardPendingInput()
		a.addCommandError(fmt.Sprintf("Failed to accept input: %v", err))
		return nil
	}
	a.pendingInputResources = nil
	request := serviceruntime.BackgroundRequest{
		Context:   context.Background(),
		SessionID: header.ID,
		WorkDir:   a.currentCwd(),
		Platform:  "tui",
		ModelID:   modelID,
		Mode:      a.mode,
		RunID:     runID,
		Input:     runInput,
		Progress: func(progress string) {
			if a.program != nil {
				a.program.Send(backgroundProgressMsg{Text: progress})
				return
			}
			a.addMessage(statusStyle.Render(progress))
		},
	}
	submitter := a.backgroundSubmitter
	return func() tea.Msg {
		runID, err := submitter(request)
		return backgroundSubmittedMsg{Input: input, Submission: runInput, RunID: runID, Err: err}
	}
}

func (a *App) discardPendingInput() {
	if a == nil || len(a.pendingInputResources) == 0 || a.runtime == nil {
		return
	}
	a.runtime.DiscardInput(context.Background(), agentruntime.InputSubmission{Resources: a.pendingInputResources})
	a.pendingInputResources = nil
}

func providerResponsesBackgroundEnabled(p provider.Provider) bool {
	background, ok := p.(interface{ ResponsesBackgroundEnabled() bool })
	return ok && background.ResponsesBackgroundEnabled()
}

// submitAgentPrompt runs the agent with an internally-generated prompt (e.g.
// /systeminit) without echoing the raw prompt text as a "You:" message.
func (a *App) submitAgentPrompt(prompt string) tea.Cmd {
	if a.manualCompactionActive {
		a.addCommandError("Cannot run while context compaction is running.")
		return nil
	}
	if err := a.ensureSession(); err != nil {
		a.addCommandError(fmt.Sprintf("Error creating session: %v", err))
		return nil
	}
	if err := a.syncESMTools(); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to sync ESM tools: %v", err))
		return nil
	}
	a.prepareESMRun()
	a.ensureAgent()
	a.registerManagedAgent()
	runtimeAgent := a.agent
	ctx := context.Background()
	return func() tea.Msg {
		return agentStreamStartMsg{
			input:      "",
			eventCh:    runtimeAgent.Run(ctx, prompt),
			compacting: false,
		}
	}
}

func (a *App) ensureSession() error {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.session != nil && a.session.GetHeader() != nil {
		return nil
	}
	cwd := a.currentCwd()
	if a.session != nil {
		sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: a.getSessionDir()})
		if err != nil {
			return err
		}
		a.session = sess
		if sess.GetHeader() != nil && sess.GetHeader().Cwd != "" {
			a.cwd = sess.GetHeader().Cwd
		}
		if err := recoverTUIOrphanedDecisions(a.getSessionDir(), sess.GetHeader().ID); err != nil {
			return err
		}
		return a.bindRuntimeSession(sess)
	}
	sessionDir := a.getSessionDir()
	sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: sessionDir})
	if err != nil {
		return err
	}
	if sess.GetHeader() != nil && sess.GetHeader().Cwd != "" {
		a.cwd = sess.GetHeader().Cwd
	}
	a.session = sess
	if err := recoverTUIOrphanedDecisions(a.getSessionDir(), sess.GetHeader().ID); err != nil {
		return err
	}
	return a.bindRuntimeSession(sess)
}

// ensureAgent lazily constructs the main agent and loads session history.
func (a *App) ensureAgent() {
	if a.agent != nil {
		if !a.agent.Aborted() {
			return
		}
		// Agent.Abort is one-shot and permanent. A Runtime-originated abort (for
		// example a lost execution lease) closes the abort channel even when the
		// run terminalizes as failed rather than canceled, so the cached instance
		// can never run again. Reusing it would cancel every later run
		// immediately ("The run was cancelled.: context canceled"); discard it and
		// build a fresh one. a.run already belongs to the new submission, so
		// discardAgent must not touch it.
		a.discardAgent(errors.New("agent instance was aborted"))
	}
	runtimeAgent, err := a.buildRuntimeAgent()
	if err != nil || runtimeAgent == nil {
		a.addCommandError(fmt.Sprintf("Failed to build agent: %v", err))
		return
	}
	a.agent = runtimeAgent
	a.registerManagedAgent()

	// Load history messages from session if available and not yet loaded.
	a.sessionMu.Lock()
	agentHistoryLoaded := a.agentHistoryLoaded
	a.sessionMu.Unlock()
	if a.session != nil && !agentHistoryLoaded {
		a.sessionMu.Lock()
		replayState := a.session.GetReplayState()
		a.sessionMu.Unlock()

		if len(replayState.Messages) > 0 {
			a.agent.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
			a.sessionMu.Lock()
			a.agentHistoryLoaded = true
			a.sessionMu.Unlock()
		}
	}
}

// rebuildAgentWithCurrentConfig replaces an existing main agent immediately
// so settings changes affect the current agent as well as future sub-agents.
// The conversation history is kept in memory and reloaded into the new agent.
func (a *App) rebuildAgentWithCurrentConfig(cause error) {
	if a.agent == nil {
		return
	}
	oldMessages, oldMessageIDs := a.agent.GetHistoryState()
	a.finishManagedAgent(cause)
	runtimeAgent, err := a.buildRuntimeAgent()
	if err != nil || runtimeAgent == nil {
		a.addCommandError(fmt.Sprintf("Failed to rebuild agent: %v", err))
		return
	}
	a.agent = runtimeAgent
	a.agent.LoadHistoryState(oldMessages, oldMessageIDs)
	a.registerManagedAgent()
}

func (a *App) startManualCompaction() tea.Cmd {
	compactAgent := a.agent
	return func() tea.Msg {
		eventCh := make(chan agent.Event, 100)
		go func() {
			defer close(eventCh)
			_ = compactAgent.CompactForced(context.Background(), eventCh)
		}()
		return agentStreamStartMsg{
			eventCh:    eventCh,
			compacting: true,
		}
	}
}
