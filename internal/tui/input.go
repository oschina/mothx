package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	serviceruntime "github.com/oschina/mothx/internal/serve/runtime"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tui/i18n"
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
	// A parallel tool batch commits as one tree block: it is only released to
	// scrollback once every member reached a terminal state, so the tree never
	// flickers apart into independent rows mid-flight.
	if gid := a.toolGroupIDAt(idx); gid > 0 && a.isMultiToolGroup(gid) {
		a.printToolGroupOnce(gid)
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

// printToolGroupOnce commits a fully terminal parallel tool batch to scrollback
// as a single tree block. While any member is still running the group stays in
// the managed viewport, so a partial batch is never printed.
func (a *App) printToolGroupOnce(groupID int) {
	members := a.toolGroupMembers(groupID)
	if len(members) < minToolGroupSize {
		return
	}
	for _, member := range members {
		if member.status == toolResultStatusRunning || a.printedMessageIdx[member.msgIndex] {
			return
		}
	}
	rendered := strings.TrimRight(a.renderToolGroupBlock(groupID), "\n")
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
	for _, member := range members {
		a.printedMessageIdx[member.msgIndex] = true
	}
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
				if err := a.resolveQuestionAnswer(answer); err != nil {
					a.addCommandError(fmt.Sprintf("Failed to record answer: %v", err))
				}
				a.addMessage(statusStyle.Render(fmt.Sprintf("✅ Selected: %s", answer)))
			} else {
				// Custom text input, including out-of-range numbers.
				if err := a.resolveQuestionAnswer(answer); err != nil {
					a.addCommandError(fmt.Sprintf("Failed to record answer: %v", err))
				}
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
	// Session creation for the very first submission runs off the Bubble Tea
	// update loop. While it is in flight the Runtime has no session to admit
	// against, so additional submissions stay in the TUI queue.
	if a.preparingSession {
		resources := a.pendingInputResources
		a.pendingInputResources = nil
		a.queuedPrompts = append(a.queuedPrompts, queuedPrompt{text: input, resources: resources})
		a.scheduleRender()
		return nil
	}
	// A session permits exactly one foreground execution at a time. Keep later
	// user submissions in the TUI instead of replacing a.run: replacing it
	// would orphan the active run's terminal cleanup and its runtime lease.
	if a.run != nil || a.isThinking {
		resources := a.pendingInputResources
		a.pendingInputResources = nil
		if a.program == nil {
			// Headless fixtures do not have a Bubble Tea owner to receive the
			// admission handoff; retain the compatibility queue for them.
			a.queuedPrompts = append(a.queuedPrompts, queuedPrompt{text: input, resources: resources})
			a.scheduleRender()
			return nil
		}
		if err := a.enqueueRuntimeSubmission(input, resources); err != nil {
			a.addCommandError(fmt.Sprintf("Failed to queue input: %v", err))
			return nil
		}
		// Surface the queue immediately: the live view renders a styled
		// "N queued" section with per-message previews until the run finishes
		// and each prompt is submitted in order.
		a.scheduleRender()
		return nil
	}

	if strings.HasPrefix(input, "/") {
		return a.handleCommand(input)
	}

	if a.session == nil {
		return a.beginSessionPreparation(input)
	}
	return a.launchPromptRun(input, true)
}

// beginSessionPreparation accepts the prompt immediately and moves the slow
// session bootstrap into a Bubble Tea command. The transcript echo and waiting
// state are committed before any database work, so Enter never appears to hang
// while a new session is created, migrated, or recovered.
func (a *App) beginSessionPreparation(input string) tea.Cmd {
	cwd := a.currentCwd()
	sessionDir := a.getSessionDir()
	background := a.backgroundSubmitter != nil && providerResponsesBackgroundEnabled(a.provider)

	a.preparingSession = true
	a.sessionPrepSeq++
	seq := a.sessionPrepSeq
	if !background {
		a.isThinking = true
		a.spinnerIndex = 0
		a.requestStart = time.Now()
		a.addMessage(userStyle.Render(a.translator.Text(i18n.MsgYouPrefix)) + input)
	}
	a.scheduleRender()

	create := func() tea.Msg {
		sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: sessionDir})
		return tuiSessionReadyMsg{seq: seq, sess: sess, input: input, err: err}
	}
	if background {
		return tea.Batch(create, a.tickSpinner())
	}
	// Reset only; the run-start transition owns the stopwatch start so the first
	// submission cannot create a second concurrent tick chain.
	return tea.Batch(create, a.tickSpinner(), a.timer.Reset())
}

// launchPromptRun performs the adapter-side preparation for one accepted
// prompt and returns the command that starts its Runtime execution. echo asks
// the stream-start message to render the user transcript line; paths that
// already rendered it immediately pass false.
func (a *App) launchPromptRun(input string, echo bool) tea.Cmd {
	if err := a.ensureSession(); err != nil {
		a.failPromptStart()
		a.addCommandError(sessionCreationError(a.translator, err, a.getSessionDir()))
		return nil
	}
	if a.backgroundSubmitter != nil && providerResponsesBackgroundEnabled(a.provider) {
		return a.submitBackgroundInput(input)
	}
	if err := a.syncESMTools(); err != nil {
		a.failPromptStart()
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
		a.failPromptStart()
		a.addCommandError(fmt.Sprintf("Failed to initialize session runtime: %v", err))
		return nil
	}
	if a.model != nil {
		a.run.model = a.model.ID
	}
	a.run.mode = a.mode
	a.run.workDir = a.currentCwd()
	a.run.coordinator = a.runtime.SubmissionCoordinator()
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
		a.failPromptStart()
		a.addCommandError(fmt.Sprintf("Failed to accept input: %v", err))
		return nil
	}
	a.pendingInputResources = nil
	artifacts, err := a.runtime.BeginArtifactCollection(a.run.id)
	if err != nil {
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run = nil
		a.failPromptStart()
		a.addCommandError(fmt.Sprintf("Failed to initialize artifact publishing: %v", err))
		return nil
	}
	userMessage, err := a.runtime.BuildUserMessage(context.Background(), runInput)
	if err != nil {
		artifacts.Close()
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run = nil
		a.failPromptStart()
		a.addCommandError(fmt.Sprintf("Failed to normalize input: %v", err))
		return nil
	}
	a.run.artifacts = artifacts
	a.prepareESMRun()
	// MCP tools that finished connecting during a prior run are now on the
	// shared registry; discard the cached Agent so this submission's build
	// freezes them into its system prompt.
	if a.mcpRebuildPending {
		a.discardAgent(errMCPToolsConnected)
		a.mcpRebuildPending = false
	}
	a.ensureAgent()
	if a.agent == nil {
		a.runtime.DiscardInput(context.Background(), runInput)
		a.run.finish(agentruntime.RunStateFailed)
		a.run = nil
		a.failPromptStart()
		return nil
	}
	a.registerManagedAgent()
	run := a.run
	runtimeAgent := a.agent
	streamInput := input
	if !echo {
		// The first-submission path already rendered the user line before the
		// asynchronous session bootstrap so the acknowledgement is immediate.
		streamInput = ""
	}
	return func() tea.Msg {
		eventCh, err := run.start(context.Background(), runtimeAgent, runInput, userMessage)
		return agentStreamStartMsg{
			input:      streamInput,
			submission: runInput,
			eventCh:    eventCh,
			err:        err,
			run:        run,
			compacting: false,
		}
	}
}

// failPromptStart clears the immediate waiting state when launchPromptRun
// fails before an agent stream owns it.
func (a *App) failPromptStart() {
	if a.isThinking || a.preparingSession {
		a.finishRequestTimer()
	}
	a.isThinking = false
	a.preparingSession = false
}

// cancelSessionPreparation abandons a pending first submission. Session
// creation itself is not cancellable, so its late result is invalidated by seq.
func (a *App) cancelSessionPreparation() tea.Cmd {
	a.sessionPrepSeq++
	a.preparingSession = false
	a.isThinking = false
	a.discardPendingInput()
	a.finishRequestTimer()
	a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgAborted)))
	a.scheduleRender()
	// Prompts queued behind the abandoned first submission still run; nothing
	// else restarts the local queue because no run terminal event will arrive.
	return tea.Batch(a.timer.Stop(), a.scheduleNextQueuedPrompt())
}

// enqueueRuntimeSubmission transfers a queued foreground input to the shared
// Runtime. The coordinator owns ordering and admission; Bubble Tea only
// receives the main-thread handoff after the fenced lease is acquired.
func (a *App) enqueueRuntimeSubmission(input string, resources []agentruntime.PreparedInput) error {
	if err := a.ensureRuntime(); err != nil {
		return err
	}
	if a.session == nil || a.session.GetHeader() == nil {
		return fmt.Errorf("session is unavailable")
	}
	coordinator := a.runtime.SubmissionCoordinator()
	runID := "tui_" + session.GenerateID()
	ready := make(chan tuiSubmissionStartResult, 1)
	handleReady := make(chan *agentruntime.ExecutionHandle, 1)
	var handle *agentruntime.ExecutionHandle
	var err error
	handle, err = coordinator.Submit(context.Background(), runID, agentruntime.SubmitPolicy{QueueBehindLocal: true}, func(ctx context.Context) (func(), error) {
		guard, acquireErr := agentruntime.AcquireExecutionAdmission(ctx, a.getSessionDir(), a.session.GetHeader().ID, agentruntime.ExecutionAdmissionOptions{})
		if acquireErr != nil {
			return nil, acquireErr
		}
		return guard.Release, nil
	}, func(ctx context.Context) error {
		var admitted *agentruntime.ExecutionHandle
		select {
		case admitted = <-handleReady:
		case <-ctx.Done():
			return ctx.Err()
		}
		a.program.Send(tuiSubmissionAdmittedMsg{handle: admitted, input: input, resources: resources, ctx: ctx, ready: ready})
		select {
		case result := <-ready:
			if result.err != nil {
				return result.err
			}
			select {
			case finishErr := <-result.done:
				return finishErr
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		return err
	}
	handleReady <- handle
	go a.watchQueuedSubmission(handle)
	// The Runtime submission closure owns prepared resources until admission or
	// cancellation. The TUI keeps only text and the handle for projection.
	a.queuedPrompts = append(a.queuedPrompts, queuedPrompt{text: input, handle: handle})
	return nil
}

func (a *App) startAdmittedSubmission(msg tuiSubmissionAdmittedMsg) tea.Cmd {
	if a == nil || msg.handle == nil || a.run != nil || a.manualCompactionActive {
		if msg.ready != nil {
			msg.ready <- tuiSubmissionStartResult{err: fmt.Errorf("TUI cannot attach admitted submission")}
		}
		return nil
	}
	if err := a.ensureSession(); err != nil {
		msg.ready <- tuiSubmissionStartResult{err: err}
		return nil
	}
	if err := a.syncESMTools(); err != nil {
		msg.ready <- tuiSubmissionStartResult{err: err}
		return nil
	}
	run := newTUIRun(a.session.GetHeader().ID, a.getSessionDir())
	run.id = msg.handle.RunID
	run.coordinator = a.runtime.SubmissionCoordinator()
	run.submission = msg.handle
	run.admitted = true
	run.submissionDone = make(chan error, 1)
	run.model = ""
	if a.model != nil {
		run.model = a.model.ID
	}
	run.mode = a.mode
	run.workDir = a.currentCwd()
	a.run = run
	a.runtime.SetExecution(run.execution)
	a.runtime.SetDecisions(run.decisions)
	var input agentruntime.InputSubmission
	var err error
	if len(msg.resources) > 0 {
		input, err = a.runtime.AttachPreparedInput(msg.ctx, msg.input, msg.resources)
	} else {
		input, err = a.runtime.AcceptInput(msg.ctx, run.id, msg.input, nil)
	}
	if err == nil {
		var artifacts *agentruntime.ArtifactCollector
		artifacts, err = a.runtime.BeginArtifactCollection(run.id)
		if err == nil {
			run.artifacts = artifacts
			var userMessage provider.Message
			userMessage, err = a.runtime.BuildUserMessage(msg.ctx, input)
			if err != nil {
				artifacts.Close()
			}
			if err == nil {
				a.prepareESMRun()
				if a.mcpRebuildPending {
					a.discardAgent(errMCPToolsConnected)
					a.mcpRebuildPending = false
				}
				a.ensureAgent()
				if a.agent == nil {
					err = fmt.Errorf("agent construction failed")
				} else {
					a.registerManagedAgent()
					events, startErr := run.startAdmitted(msg.ctx, a.agent, input, userMessage)
					err = startErr
					if err == nil {
						a.removeQueuedSubmission(msg.handle)
						msg.ready <- tuiSubmissionStartResult{events: events, done: run.submissionDone}
						return func() tea.Msg {
							return agentStreamStartMsg{input: msg.input, submission: input, eventCh: events, run: run}
						}
					}
				}
			}
		}
	}
	if a.run == run {
		a.run = nil
	}
	if err != nil {
		a.runtime.DiscardInput(msg.ctx, input)
		msg.ready <- tuiSubmissionStartResult{err: err}
		a.removeQueuedSubmission(msg.handle)
	}
	return nil
}

func (a *App) removeQueuedSubmission(handle *agentruntime.ExecutionHandle) {
	if a == nil || handle == nil {
		return
	}
	for i, prompt := range a.queuedPrompts {
		if prompt.handle == handle {
			a.queuedPrompts = append(a.queuedPrompts[:i], a.queuedPrompts[i+1:]...)
			return
		}
	}
}

// watchQueuedSubmission projects a terminal admission failure back to the UI.
// The coordinator only sends tuiSubmissionAdmittedMsg after a successful
// admission, so without this watcher an externally owned or closed session
// would leave the queued row visible with no explanation and no retry.
func (a *App) watchQueuedSubmission(handle *agentruntime.ExecutionHandle) {
	if a == nil || handle == nil {
		return
	}
	completion, ok := <-handle.Completed
	if !ok {
		completion = agentruntime.ExecutionCompletion{SubmissionID: handle.SubmissionID, RunID: handle.RunID, Error: agentruntime.ErrSubmissionClosed}
	}
	if completion.Error == nil || errors.Is(completion.Error, agentruntime.ErrSubmissionCancelled) || errors.Is(completion.Error, context.Canceled) {
		return
	}
	if a.program != nil {
		a.program.Send(tuiSubmissionFailedMsg{handle: handle, err: completion.Error})
	}
}

// startNextQueuedPrompt starts exactly one queued foreground submission after
// the preceding run has reached its canonical terminal state.
func (a *App) startNextQueuedPrompt() tea.Cmd {
	if a == nil || len(a.queuedPrompts) == 0 || a.run != nil || a.isThinking || a.preparingSession || a.manualCompactionActive {
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

// resolveQuestionAnswer answers the pending question through the Runtime
// decision service so the resolution is persisted for the run ledger, then
// unblocks the agent. A ledger failure falls back to the direct response so the
// asking agent can never stay parked on a question that was already answered.
func (a *App) resolveQuestionAnswer(answer string) error {
	questionID := a.pendingQuestionID
	if a.run == nil || questionID == "" {
		if a.agent != nil {
			a.agent.HandleQuestionResponse(questionID, answer)
		}
		return nil
	}
	run := a.run
	if err := run.resolveDecision(questionID, agentruntime.DecisionQuestion, answer); err != nil {
		if a.agent != nil {
			a.agent.HandleQuestionResponse(questionID, answer)
		}
		return err
	}
	return nil
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
		a.addCommandError(sessionCreationError(a.translator, err, a.getSessionDir()))
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
		return a.activateSession(sess)
	}
	sessionDir := a.getSessionDir()
	sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: sessionDir})
	if err != nil {
		return err
	}
	return a.activateSession(sess)
}

// activateSession commits a newly created or opened session to the TUI only
// after recovery and Runtime binding have both succeeded. Retaining the prior
// session on failure prevents the adapter and shared Runtime from diverging.
func (a *App) activateSession(sess *session.Manager) error {
	if sess == nil || sess.GetHeader() == nil {
		return fmt.Errorf("initialized session manager is required")
	}
	if err := recoverTUIOrphanedDecisions(a.getSessionDir(), sess.GetHeader().ID); err != nil {
		return err
	}
	if err := a.bindRuntimeSession(sess); err != nil {
		return err
	}
	a.session = sess
	// The TUI directory always mirrors what the Runtime bound: BindSession made
	// header.Cwd the authoritative WorkDir, so committing a different value (or
	// keeping the previous one when it is empty) would recreate the divergence
	// this ordering exists to prevent.
	a.cwd = sess.GetHeader().Cwd
	// Project cross-process ownership for the freshly activated session (an
	// idle snapshot stays silent). Pure Runtime projection, no local state.
	a.notifySessionExecutionState(sess.GetHeader().ID)
	return nil
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
