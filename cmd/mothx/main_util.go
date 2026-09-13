package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/startvibecoding/GoStreamingMarkdown/gsm"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

var debugEnabled bool

// clearStdin reads and discards any pending input from stdin.
// This is needed because some terminals send color query sequences on startup.
func clearStdin() {
	// Set a short read deadline so pending reads time out cleanly.
	// Some stdin types (pipes, certain PTYs) don't support deadlines;
	// if SetReadDeadline fails we skip clearing to avoid blocking forever.
	if err := os.Stdin.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		return
	}
	defer os.Stdin.SetReadDeadline(time.Time{}) // Clear deadline
	buf := make([]byte, 128)
	for {
		n, err := os.Stdin.Read(buf)
		if n == 0 || err != nil {
			return
		}
	}
}

// debugLog prints debug messages to stderr if debug mode is enabled.
func debugLog(format string, args ...interface{}) {
	if debugEnabled && os.Getenv(provider.DebugLogOnlyEnv) == "" {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format+"\n", args...)
	}
}

// createProvider creates a provider from config based on provider name.
func createProvider(settings *config.Settings, providerName, modelID string) (provider.Provider, *provider.Model, error) {
	return providerfactory.Create(settings, providerName, modelID)
}

func runPrint(args []string, p provider.Provider, providerName string, model *provider.Model, mode string, thinkingLevel provider.ThinkingLevel, settings *config.Settings, registry *tools.Registry, sess *session.Manager, extraContext string, ruleContent string, multiAgent bool, delegateMode bool, workflows bool, jsonOut bool, agentMgr *agent.AgentManager, sharedRuntimes ...*agentruntime.SessionRuntime) error {
	input := strings.Join(args, " ")
	if input == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("no input provided")
		}
		input = string(data)
	}

	if jsonOut {
		printJSONEmit(printJSONEvent{
			Type:     "start",
			Provider: p.Name(),
			Model:    model.ID,
			Mode:     mode,
		})
	} else {
		fmt.Fprintf(os.Stderr, "Using %s/%s in %s mode\n", p.Name(), model.ID, mode)
	}
	var (
		releaseRuntime func()
		execution      *agentruntime.ExecutionRuntime
		runID          string
		intentID       string
		turnID         string
		runInput       agentruntime.RunInput
		inputAccepted  bool
		userMessage    provider.Message
		messageBuilt   bool
		err            error
		runCtx         = context.Background()
	)
	if sess != nil && sess.GetHeader() != nil {
		guard, err := agentruntime.AcquireExecutionAdmission(context.Background(), sess.GetSessionDir(), sess.GetHeader().ID, agentruntime.ExecutionAdmissionOptions{})
		if err != nil {
			return fmt.Errorf("session %s cannot start execution: %w", sess.GetHeader().ID, err)
		}
		releaseRuntime = guard.Release
	}

	// Create gsm renderer for markdown
	wordWrap := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		wordWrap = w
	}
	mdWidth := wordWrap

	buildOptions := agentruntime.AgentBuildOptions{
		Provider: p, ProviderName: providerName, Model: model, Mode: mode,
		ThinkingLevel: thinkingLevel, Settings: settings, Allow: config.LoadAllow(),
		ExtraContext: extraContext, RuleContent: ruleContent,
		MultiAgent: multiAgent, DelegateMode: delegateMode, Workflows: workflows,
	}

	workDir := ""
	if sess != nil && sess.GetHeader() != nil {
		workDir = sess.GetHeader().Cwd
	}
	var runtime *agentruntime.SessionRuntime
	if len(sharedRuntimes) > 0 {
		runtime = sharedRuntimes[0]
	}
	if runtime == nil {
		runtime = &agentruntime.SessionRuntime{
			Source: agentruntime.SourceCLI, EntrySource: agentruntime.SourceCLI, WorkDir: workDir, Registry: registry,
			ExtraContext: extraContext, RuleContent: ruleContent, ArtifactEnabled: settings.IsArtifactEnabled(),
		}
		if sess != nil && sess.GetHeader() != nil {
			if err := runtime.BindSession(sess, agentruntime.SourceCLI); err != nil {
				return err
			}
		}
	}
	if sess != nil && sess.GetHeader() != nil {
		startedAt := time.Now()
		runID = "cli_" + session.GenerateID()
		intentID = "intent_" + session.GenerateID()
		turnID = "turn-" + intentID
		buildOptions.ConversationTurnID = turnID
		buildOptions.IntentID = intentID
		buildOptions.RunID = runID
		buildOptions.ConversationTurn = true
		runInput, err = runtime.AcceptInput(runCtx, runID, input, nil)
		if err != nil {
			releaseRuntime()
			return fmt.Errorf("normalize CLI input: %w", err)
		}
		inputAccepted = true
		userMessage, err = runtime.BuildUserMessage(runCtx, runInput)
		if err != nil {
			releaseRuntime()
			return fmt.Errorf("build CLI user message: %w", err)
		}
		messageBuilt = true

		requestSnapshot, snapshotErr := json.Marshal(map[string]any{
			"message": input, "model": model.ID, "mode": mode, "workDir": workDir,
		})
		if snapshotErr != nil {
			releaseRuntime()
			return snapshotErr
		}
		policySnapshot, snapshotErr := json.Marshal(map[string]any{
			"source": "cli", "mode": mode, "workDir": workDir,
			"approvalPolicy": "print", "questionPolicy": "unattended",
		})
		if snapshotErr != nil {
			releaseRuntime()
			return snapshotErr
		}
		digest := sha256.Sum256(requestSnapshot)
		intent := agentruntime.ExecutionIntent{
			ID: intentID, SessionID: sess.GetHeader().ID, Source: "cli", Model: model.ID, Mode: mode, WorkDir: workDir,
			RequestFingerprint: fmt.Sprintf("sha256:%x", digest[:]), Request: requestSnapshot, Policy: policySnapshot, CreatedAt: startedAt,
		}
		startData, _ := json.Marshal(map[string]any{"intentId": intentID, "attempt": 1})
		execution = &agentruntime.ExecutionRuntime{}
		execution.SetRunStore(agentruntime.RunStore{SessionDir: sess.GetSessionDir()})
		execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: sess.GetSessionDir()})
		runtime.SetExecution(execution)
		var beginErr error
		runCtx, beginErr = execution.BeginIntentDurable(runCtx, intent, agentruntime.DurableRun{
			ID: runID, SessionID: sess.GetHeader().ID, IntentID: intentID, Attempt: 1, WorkDir: workDir, Source: "cli",
			Model: model.ID, Mode: mode, Status: "running", StartedAt: startedAt, InputResourceIDs: runInput.ResourceIDs(),
			UserEntryID: session.RunUserEntryID(runID), UserMessage: &userMessage,
			ConversationTurnID: turnID, ConversationTurn: true,
		}, agentruntime.RunEvent{
			SessionID: sess.GetHeader().ID, RunID: runID, EventType: "started", Source: "cli", Status: "running",
			Model: model.ID, Mode: mode, Timestamp: startedAt, Data: startData,
		})
		if beginErr != nil {
			releaseRuntime()
			return beginErr
		}
	}
	if releaseRuntime != nil {
		defer releaseRuntime()
	}
	if !inputAccepted {
		runInput, err = runtime.AcceptInput(runCtx, runID, input, nil)
		if err != nil {
			return fmt.Errorf("normalize CLI input: %w", err)
		}
	}
	var artifacts *agentruntime.ArtifactCollector
	if runID != "" {
		artifacts, err = runtime.BeginArtifactCollection(runID)
		if err != nil {
			if execution != nil {
				_ = execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateFailed, err.Error(), agentruntime.RunEvent{EventType: "failed", Source: "cli", Timestamp: time.Now()})
			}
			return fmt.Errorf("begin CLI artifact collection: %w", err)
		}
		defer artifacts.Close()
	}
	if !messageBuilt {
		userMessage, err = runtime.BuildUserMessage(runCtx, runInput)
		if err != nil {
			if execution != nil {
				_ = execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateFailed, err.Error(), agentruntime.RunEvent{EventType: "failed", Source: "cli", Timestamp: time.Now()})
			}
			return fmt.Errorf("build CLI user message: %w", err)
		}
	}
	a, err := runtime.BuildAgent(buildOptions)
	if err != nil {
		if execution != nil {
			_ = execution.FinishDurableWithRetry(context.Background(), runID, agentruntime.RunStateFailed, err.Error(), agentruntime.RunEvent{EventType: "failed", Source: "cli", Timestamp: time.Now()})
		}
		return err
	}
	if execution != nil {
		a.SetConversationTurn(turnID, intentID, runID)
		execution.SetAgent(a)
	}
	if sess != nil {
		replayState := sess.GetReplayState()
		if len(replayState.Messages) > 0 {
			a.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
		}
	}
	if (multiAgent || delegateMode || workflows) && agentMgr != nil {
		agentMgr.Register(agent.NewAgentAdapter(a))
	}

	eventCh := a.RunWithUserMessage(runCtx, userMessage)

	var textBuffer strings.Builder
	var runErr error
	terminalState := agentruntime.RunStateCompleted

	// drainText flushes the accumulated text buffer. In text mode it renders
	// markdown to stdout. In JSON mode text deltas are emitted immediately as
	// NDJSON lines, so the buffer stays empty and this is a no-op.
	drainText := func() {
		if textBuffer.Len() == 0 {
			return
		}
		flushTextBuffer(&textBuffer, mdWidth)
	}

	// Print mode consumes the canonical event stream until it closes. Note the
	// boundary of this consumer: it returns as soon as runCtx is done, while the
	// Agent loop keeps sending its terminal events (EventRunFinished/EventDone/
	// EventError/agentEndEvent) unconditionally. A cancelled print run may
	// therefore leave the loop parked on one of those sends; the process exits
	// right after this command and reclaims it, which is why the CLI needs no
	// drain goroutine (unlike the TUI, which keeps consuming a retired stream).
	err = agent.ConsumeEvents(runCtx, eventCh, agent.EventHandlerFunc(func(_ context.Context, event agent.Event) error {
		switch event.Type {
		case agent.EventToolApprovalRequest:
			if jsonOut {
				printJSONEmit(printJSONEvent{
					Type:  "error",
					Error: fmt.Sprintf("tool approval required in print mode for %s; rerun interactively, use --mode yolo, or whitelist the command", event.ApprovalTool),
				})
			}
			return fmt.Errorf("tool approval required in print mode for %s; rerun interactively, use --mode yolo, or whitelist the command", event.ApprovalTool)
		case agent.EventTextDelta:
			if jsonOut {
				printJSONEmit(printJSONEvent{Type: "text_delta", Text: event.TextDelta})
			} else {
				textBuffer.WriteString(event.TextDelta)
			}
		case agent.EventThinkDelta:
			if jsonOut {
				printJSONEmit(printJSONEvent{Type: "think_delta", Think: event.ThinkDelta})
			}
		case agent.EventHostedItem:
			if jsonOut && event.HostedItem != nil {
				printJSONEmit(printJSONEvent{Type: "hosted_item", HostedItem: &printJSONHostedItem{
					ID: event.HostedItem.ID, Type: event.HostedItem.Type, Status: event.HostedItem.Status,
					OutputIndex: event.HostedItem.OutputIndex, Metadata: event.HostedItem.Metadata,
				}})
			}
		case agent.EventToolCall:
			// Flush text buffer before tool call (text mode only)
			drainText()
			if jsonOut {
				if event.ToolCall != nil {
					printJSONEmit(printJSONEvent{
						Type:      "tool_call",
						ID:        event.ToolCall.ID,
						Name:      event.ToolCall.Name,
						Arguments: event.ToolArgs,
					})
				}
			} else {
				fmt.Fprintf(os.Stderr, "\n[tool: %s]\n", event.ToolCall.Name)
			}
		case agent.EventToolExecutionStart:
			if jsonOut {
				printJSONEmit(printJSONEvent{Type: "tool_execution_start", Name: event.ToolName})
			} else {
				fmt.Fprintf(os.Stderr, "[running: %s] ", event.ToolName)
			}
		case agent.EventToolExecutionEnd:
			if jsonOut {
				ev := printJSONEvent{Type: "tool_execution_end", Name: event.ToolName}
				if event.ToolError != nil {
					ev.Error = event.ToolError.Error()
				}
				printJSONEmit(ev)
			} else {
				if event.ToolError != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", event.ToolError)
				} else {
					fmt.Fprintf(os.Stderr, "done\n")
				}
			}
		case agent.EventToolResult:
			if jsonOut {
				ev := printJSONEvent{
					Type:   "tool_result",
					ID:     event.ToolCallID,
					Name:   event.ToolName,
					Result: event.ToolResult,
				}
				if event.ToolError != nil {
					ev.Error = event.ToolError.Error()
				}
				if event.ToolDiff != nil {
					ev.Diff = printJSONDiffFromFileDiff(event.ToolDiff)
				}
				printJSONEmit(ev)
			} else {
				// Show full tool result for bash commands
				if event.ToolName == "bash" {
					fmt.Fprintf(os.Stderr, "\n%s\n", event.ToolResult)
				} else if event.ToolDiff != nil {
					fmt.Fprintf(os.Stderr, "\n[change: %s] +%d -%d (-%s +%s)\n",
						event.ToolDiff.Path,
						event.ToolDiff.Added,
						event.ToolDiff.Deleted,
						formatLineRanges(event.ToolDiff.DeletedLines),
						formatLineRanges(event.ToolDiff.AddedLines),
					)
				}
			}
		case agent.EventPlanUpdate:
			if jsonOut {
				if event.Plan != nil {
					printJSONEmit(printJSONEvent{Type: "plan_update", Plan: printJSONPlanFromTask(event.Plan)})
				}
			} else if event.Plan != nil {
				fmt.Fprintf(os.Stderr, "\n%s\n", formatTaskPlan(event.Plan))
			}
		case agent.EventRunFinished:
			// Canonical terminal event: classify the outcome once. failed/canceled
			// stop the run here; success/incomplete fall through to the legacy
			// EventDone rendering below.
			switch event.Status {
			case agent.TaskFailed, agent.TaskCanceled:
				if event.Status == agent.TaskCanceled {
					terminalState = agentruntime.RunStateCancelled
				} else {
					terminalState = agentruntime.RunStateFailed
				}
				runErr = event.Error
				if runErr == nil && event.Status == agent.TaskFailed {
					runErr = fmt.Errorf("run failed")
				}
				if runErr == nil && event.Status == agent.TaskCanceled {
					runErr = context.Canceled
				}
				drainText()
				if jsonOut {
					ev := printJSONEvent{Type: "finished", Status: string(event.Status), StopReason: event.StopReason}
					if runErr != nil {
						ev.Error = runErr.Error()
					}
					printJSONEmit(ev)
				}
				return runErr
			case agent.TaskIncomplete:
				terminalState = agentruntime.RunStateIncomplete
			}
		case agent.EventDone:
			// Flush remaining text buffer (text mode only)
			drainText()
			if jsonOut {
				ev := printJSONEvent{Type: "done", StopReason: event.StopReason}
				if event.Usage != nil {
					u := *event.Usage
					ev.Usage = &u
				}
				if event.ContextUsage != nil {
					ev.ContextUsage = printJSONContextFromUsage(event.ContextUsage)
				}
				printJSONEmit(ev)
			} else if event.ContextUsage != nil && event.ContextUsage.Percent != nil {
				// Show context usage
				fmt.Fprintf(os.Stderr, "\nContext: %.1f%%/%s\n",
					*event.ContextUsage.Percent,
					formatTokenCount(event.ContextUsage.ContextWindow))
			}
		case agent.EventError:
			runErr = event.Error
			// Flush text buffer before error (text mode only)
			drainText()
			if jsonOut {
				ev := printJSONEvent{Type: "error", StopReason: event.StopReason}
				if event.Error != nil {
					ev.Error = event.Error.Error()
				}
				printJSONEmit(ev)
			}
			if event.Error != nil {
				return event.Error
			}
		case agent.EventUsage:
			if jsonOut {
				ev := printJSONEvent{Type: "usage"}
				if event.Usage != nil {
					u := *event.Usage
					ev.Usage = &u
				}
				if event.ContextUsage != nil {
					ev.ContextUsage = printJSONContextFromUsage(event.ContextUsage)
				}
				printJSONEmit(ev)
			} else {
				if event.ContextUsage != nil && event.ContextUsage.Percent != nil {
					fmt.Fprintf(os.Stderr, "Context: %.1f%%/%s | ",
						*event.ContextUsage.Percent,
						formatTokenCount(event.ContextUsage.ContextWindow))
				}
				if event.Usage != nil {
					cacheInfo := ""
					if info := event.Usage.CacheInfo(); info != "" {
						cacheInfo = " | " + info
					}
					fmt.Fprintf(os.Stderr, "Tokens: %d↓/%d↑ $%.4f%s\n",
						event.Usage.TotalInputTokens(), event.Usage.Output, event.Usage.Cost.Total, cacheInfo)
				}
			}
		case agent.EventCompactionStart:
			if jsonOut {
				printJSONEmit(printJSONEvent{Type: "compaction_start"})
			} else {
				fmt.Fprintf(os.Stderr, "\n⏳ Compacting context...\n")
			}
		case agent.EventCompactionEnd:
			if jsonOut {
				ev := printJSONEvent{Type: "compaction_end"}
				if event.Error != nil {
					ev.Error = event.Error.Error()
				} else if event.StatusMessage != "" {
					ev.StatusMessage = event.StatusMessage
				}
				printJSONEmit(ev)
			} else {
				if event.Error != nil {
					fmt.Fprintf(os.Stderr, "Compaction failed: %v\n", event.Error)
				} else if event.StatusMessage != "" {
					fmt.Fprintf(os.Stderr, "✅ %s\n", event.StatusMessage)
				} else {
					fmt.Fprintf(os.Stderr, "✅ Context compacted\n")
				}
			}
		}
		return nil
	}))
	if (multiAgent || delegateMode || workflows) && agentMgr != nil {
		finishErr := runErr
		if finishErr == nil {
			finishErr = err
		}
		agentMgr.Finish(a.ID(), finishErr)
	}
	if err != nil {
		if execution != nil {
			state := terminalState
			if state == agentruntime.RunStateCompleted {
				state = agentruntime.RunStateFailed
			}
			if errors.Is(err, context.Canceled) {
				state = agentruntime.RunStateCancelled
			}
			_ = execution.FinishDurableWithRetry(context.Background(), runID, state, err.Error(), agentruntime.RunEvent{EventType: "finished", Source: "cli", Timestamp: time.Now()})
		}
		return err
	}
	if execution != nil {
		if err := execution.FinishDurableWithRetry(context.Background(), runID, terminalState, "", agentruntime.RunEvent{EventType: "finished", Source: "cli", Timestamp: time.Now()}); err != nil {
			return err
		}
	}

	return nil
}

func formatTaskPlan(plan *tools.TaskPlan) string {
	if plan == nil || len(plan.Steps) == 0 {
		return "Plan updated."
	}
	var sb strings.Builder
	title := plan.Title
	if title == "" {
		title = "Plan"
	}
	sb.WriteString(title)
	for _, step := range plan.Steps {
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("%s %s", planStatusMarker(step.Status), step.Title))
	}
	if plan.Note != "" {
		sb.WriteString("\nnote: " + plan.Note)
	}
	return sb.String()
}

func planStatusMarker(status string) string {
	switch status {
	case "running":
		return ">"
	case "done":
		return "x"
	case "failed":
		return "!"
	default:
		return "-"
	}
}

func formatLineRanges(lines []int) string {
	if len(lines) == 0 {
		return "none"
	}
	var ranges []string
	start, prev := lines[0], lines[0]
	for _, line := range lines[1:] {
		if line == prev+1 {
			prev = line
			continue
		}
		ranges = append(ranges, formatLineRange(start, prev))
		start, prev = line, line
	}
	ranges = append(ranges, formatLineRange(start, prev))
	return strings.Join(ranges, ",")
}

func formatLineRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// flushTextBuffer renders and prints the accumulated text buffer.
func flushTextBuffer(buffer *strings.Builder, mdWidth int) {
	text := buffer.String()
	buffer.Reset()

	if mdWidth > 0 {
		rendered := gsm.Render(text, mdWidth, nil)
		fmt.Print(rendered)
	} else {
		fmt.Print(text)
	}
}

// formatTokenCount formats a token count for display.
func formatTokenCount(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 10000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	if count < 1000000 {
		return fmt.Sprintf("%dk", count/1000)
	}
	if count < 10000000 {
		return fmt.Sprintf("%.1fM", float64(count)/1000000)
	}
	return fmt.Sprintf("%dM", count/1000000)
}

// printJSONEvent is one line in the NDJSON stream emitted by `mothx -P --json`.
// Each agent event becomes its own JSON object on a single line (JSON Lines /
// NDJSON) so consumers can read stdout line-by-line and react as events arrive.
// All progress, debug, and diagnostic output still goes to stderr, so stdout
// stays pure NDJSON. The stream always terminates with a "done" or "error"
// event, which signals completion.
type printJSONEvent struct {
	Type          string               `json:"type"`
	Text          string               `json:"text,omitempty"`
	Think         string               `json:"think,omitempty"`
	ID            string               `json:"id,omitempty"`
	Name          string               `json:"name,omitempty"`
	Provider      string               `json:"provider,omitempty"`
	Model         string               `json:"model,omitempty"`
	Mode          string               `json:"mode,omitempty"`
	Arguments     map[string]any       `json:"arguments,omitempty"`
	Result        string               `json:"result,omitempty"`
	Error         string               `json:"error,omitempty"`
	Diff          *printJSONDiff       `json:"diff,omitempty"`
	Plan          *printJSONPlan       `json:"plan,omitempty"`
	Usage         *provider.Usage      `json:"usage,omitempty"`
	ContextUsage  *printJSONContext    `json:"context_usage,omitempty"`
	StopReason    string               `json:"stop_reason,omitempty"`
	Status        string               `json:"status,omitempty"`
	StatusMessage string               `json:"status_message,omitempty"`
	HostedItem    *printJSONHostedItem `json:"hosted_item,omitempty"`
}

type printJSONHostedItem struct {
	ID          string         `json:"id,omitempty"`
	Type        string         `json:"type,omitempty"`
	Status      string         `json:"status,omitempty"`
	OutputIndex int            `json:"output_index,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// printJSONPlan is the JSON-friendly mirror of tools.TaskPlan.
type printJSONPlan struct {
	Title string          `json:"title"`
	Steps []printJSONStep `json:"steps,omitempty"`
	Note  string          `json:"note,omitempty"`
}

// printJSONStep is one step in a JSON task plan.
type printJSONStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

// printJSONDiff is the JSON-friendly mirror of tools.FileDiff.
type printJSONDiff struct {
	Path         string `json:"path,omitempty"`
	Added        int    `json:"added"`
	Deleted      int    `json:"deleted"`
	AddedLines   []int  `json:"addedLines,omitempty"`
	DeletedLines []int  `json:"deletedLines,omitempty"`
	Unified      string `json:"unified,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

// printJSONContext is the JSON-friendly mirror of context.ContextUsage.
type printJSONContext struct {
	Tokens        int      `json:"tokens"`
	TotalTokens   int      `json:"total_tokens"`
	Input         int      `json:"input"`
	CacheRead     int      `json:"cache_read"`
	CacheWrite    int      `json:"cache_write"`
	ContextWindow int      `json:"context_window"`
	Percent       *float64 `json:"percent,omitempty"`
}

func printJSONDiffFromFileDiff(d *tools.FileDiff) *printJSONDiff {
	if d == nil {
		return nil
	}
	return &printJSONDiff{
		Path:         d.Path,
		Added:        d.Added,
		Deleted:      d.Deleted,
		AddedLines:   d.AddedLines,
		DeletedLines: d.DeletedLines,
		Unified:      d.Unified,
		Truncated:    d.Truncated,
	}
}

func printJSONContextFromUsage(c *ctxpkg.ContextUsage) *printJSONContext {
	if c == nil {
		return nil
	}
	res := &printJSONContext{
		Tokens:        c.TotalTokens,
		TotalTokens:   c.TotalTokens,
		Input:         c.Input,
		CacheRead:     c.CacheRead,
		CacheWrite:    c.CacheWrite,
		ContextWindow: c.ContextWindow,
	}
	if c.Percent != nil {
		p := *c.Percent
		res.Percent = &p
	}
	return res
}

func printJSONPlanFromTask(p *tools.TaskPlan) *printJSONPlan {
	if p == nil {
		return nil
	}
	res := &printJSONPlan{Title: p.Title, Note: p.Note}
	for _, s := range p.Steps {
		res.Steps = append(res.Steps, printJSONStep{Title: s.Title, Status: s.Status})
	}
	return res
}

// printJSONEmit writes one NDJSON line to stdout so consumers can read events
// as they arrive instead of waiting for the run to finish. Each agent event
// becomes its own JSON object on a single line (JSON Lines / NDJSON). The
// stream always terminates with a "done" or "error" event.
func printJSONEmit(ev printJSONEvent) {
	out, marshalErr := json.Marshal(ev)
	if marshalErr != nil {
		return
	}
	fmt.Println(string(out))
}
