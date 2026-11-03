package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

// maxSubagentResultBytes bounds the worker text the parent can see. The full
// remote transcript stays a child projection; the lead only receives a bounded
// completion envelope.
const maxSubagentResultBytes = 16 * 1024

// ACPWorkerInvocation is the already-authorized Runtime input to one external
// ACP worker turn. It contains no adapter state or provider content; Router
// task construction owns that conversion before this boundary.
type ACPWorkerInvocation struct {
	Profile        config.ACPAgentProfile
	Contract       SubagentTaskContract
	WorkDir        string
	Launcher       acpclient.Launcher
	ClientInfo     acpclient.Implementation
	HandleRequest  acpclient.RequestHandler
	OnNotification func(acpclient.Message)
	// OnEvent receives canonical, non-terminal child events. The proxy owns the
	// single EventRunFinished terminal event.
	OnEvent func(agentpkg.Event)
}

// ACPWorkerResult is the protocol result of one remote worker turn. The proxy
// converts it into the single canonical terminal event.
type ACPWorkerResult struct {
	AgentInfo  acpclient.Implementation
	SessionID  string
	StopReason string
	Output     string
	Truncated  bool
	ToolCalls  int
}

// ACPWorkerBridge owns one ACP v1 worker connection. It is intentionally not
// an Agent, session store, or queue; Runtime supplies policy and consumes the
// typed protocol result.
type ACPWorkerBridge struct{}

// Run starts one configured stdio ACP Agent, performs the MVP handshake and
// one text prompt turn, then closes the remote session when supported.
func (ACPWorkerBridge) Run(ctx context.Context, invocation ACPWorkerInvocation) (ACPWorkerResult, error) {
	if err := invocation.Profile.Validate(); err != nil {
		return ACPWorkerResult{}, err
	}
	if !invocation.Profile.Enabled {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker profile %q is disabled", invocation.Profile.ID)
	}
	if strings.TrimSpace(invocation.WorkDir) == "" || !filepath.IsAbs(invocation.WorkDir) {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker requires an absolute work directory")
	}
	if err := invocation.Contract.Validate(invocation.Profile); err != nil {
		return ACPWorkerResult{}, err
	}
	if invocation.Launcher == nil {
		return ACPWorkerResult{}, fmt.Errorf("ACP worker requires a Runtime process launcher")
	}

	env, err := acpWorkerEnvironment(invocation.Profile)
	if err != nil {
		return ACPWorkerResult{}, err
	}
	stderr := &boundedACPStderr{limit: 2048}
	client, err := acpclient.Start(ctx, acpclient.Config{
		Command:       invocation.Profile.Command,
		Dir:           invocation.WorkDir,
		Env:           env,
		Launcher:      invocation.Launcher,
		Stderr:        stderr,
		HandleRequest: invocation.HandleRequest,
	})
	if err != nil {
		return ACPWorkerResult{}, err
	}
	defer client.Close()

	var output strings.Builder
	truncated := false
	toolCalls := 0
	emit := func(event agentpkg.Event) {
		if invocation.OnEvent != nil {
			invocation.OnEvent(event)
		}
	}
	handleUpdate := func(message acpclient.Message) {
		update, ok := acpclient.DecodeSessionUpdate(message)
		if !ok {
			return
		}
		switch update.SessionUpdate {
		case "agent_message_chunk":
			if update.Content == nil || update.Content.Text == "" {
				return
			}
			text := update.Content.Text
			if output.Len()+len(text) > maxSubagentResultBytes {
				remaining := maxSubagentResultBytes - output.Len()
				if remaining > 0 {
					piece, _ := truncateACPText(text, remaining)
					output.WriteString(piece)
				}
				truncated = true
				return
			}
			output.WriteString(text)
			emit(agentpkg.Event{Type: agentpkg.EventTextDelta, TextDelta: text})
		case "agent_thought_chunk":
			if update.Content != nil && update.Content.Text != "" {
				text, _ := boundACPText(update.Content.Text)
				emit(agentpkg.Event{Type: agentpkg.EventThinkDelta, ThinkDelta: text})
			}
		case "tool_call":
			toolCalls++
			title, _ := boundACPText(update.Title)
			emit(agentpkg.Event{
				Type:       agentpkg.EventToolCall,
				ToolCallID: acpRemoteToolCallID(update.ToolCallID),
				ToolName:   title,
				ToolArgs:   decodeACPToolInput(update.RawInput),
				ToolCall: &agentpkg.ToolCallBlock{
					ID: acpRemoteToolCallID(update.ToolCallID), Name: title,
					Kind: update.Kind, Input: string(update.RawInput),
				},
			})
		case "tool_call_update":
			eventType := agentpkg.EventToolExecutionUpdate
			if update.Status == "completed" || update.Status == "failed" {
				eventType = agentpkg.EventToolExecutionEnd
			}
			title, _ := boundACPText(update.Title)
			result, _ := boundACPText(string(update.RawOutput))
			event := agentpkg.Event{
				Type:       eventType,
				ToolCallID: acpRemoteToolCallID(update.ToolCallID),
				ToolName:   title,
				ToolResult: result,
			}
			if update.Status == "failed" {
				event.ToolError = fmt.Errorf("remote tool call failed")
			}
			emit(event)
		case "plan":
			steps := make([]agentpkg.PlanStep, 0, len(update.Entries))
			for _, entry := range update.Entries {
				content, _ := boundACPText(entry.Content)
				steps = append(steps, agentpkg.PlanStep{Title: content, Status: entry.Status})
			}
			emit(agentpkg.Event{Type: agentpkg.EventPlanUpdate, Plan: &agentpkg.TaskPlan{Steps: steps}})
		case "usage_update":
			emit(agentpkg.Event{
				Type:         agentpkg.EventUsage,
				ContextUsage: &agentpkg.ContextUsage{TotalTokens: update.Used, ContextWindow: update.Size},
			})
		}
	}

	// Drain updates independently while session/prompt waits for its terminal
	// response; the bounded client notification buffer must never be allowed to
	// back up a long-running worker.
	notificationsDone := make(chan struct{})
	go func() {
		defer close(notificationsDone)
		notifications := client.Notifications()
		handle := func(notification acpclient.Message) {
			if invocation.OnNotification != nil {
				invocation.OnNotification(notification)
			}
			handleUpdate(notification)
		}
		for {
			select {
			case notification := <-notifications:
				handle(notification)
			case <-client.Done():
				// Notifications are not closed by the transport (closing would
				// race a concurrent send). Drain what is already buffered, then
				// stop; anything arriving after termination has no consumer.
				for {
					select {
					case notification := <-notifications:
						handle(notification)
					default:
						return
					}
				}
			}
		}
	}()
	// One teardown path for every success and error return: close the transport,
	// then join the drain goroutine before the proxy may close its event channel.
	defer func() {
		_ = client.Close()
		<-notificationsDone
	}()

	clientInfo := invocation.ClientInfo
	if clientInfo.Name == "" {
		clientInfo = acpclient.Implementation{Name: "mothx", Title: "MothX Router", Version: "dev"}
	}
	initialized, err := client.Initialize(ctx, acpclient.InitializeParams{
		ClientInfo: clientInfo,
		ClientCapabilities: map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"auth":     map[string]bool{"terminal": false},
		},
	})
	if err != nil {
		return ACPWorkerResult{}, fmt.Errorf("initialize ACP worker: %w", err)
	}
	session, err := client.NewSession(ctx, acpclient.NewSessionParams{Cwd: invocation.WorkDir})
	if err != nil {
		if len(initialized.AuthMethods) > 0 {
			return ACPWorkerResult{}, fmt.Errorf("ACP worker profile %q requires local authentication handled by its own client; MothX does not relay credentials: %w", invocation.Profile.ID, err)
		}
		return ACPWorkerResult{}, fmt.Errorf("create ACP worker session: %w", err)
	}

	// ACP session/cancel is the semantic cancellation signal for an active
	// remote turn. The client context cancellation alone must not be mistaken
	// for a successful remote completion.
	cancelDone := make(chan struct{})
	defer close(cancelDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Cancel(session.SessionID)
		case <-cancelDone:
		}
	}()
	prompt, promptErr := client.Prompt(ctx, acpclient.PromptParams{
		SessionID: session.SessionID,
		Prompt:    []acpclient.ContentBlock{{Type: "text", Text: invocation.Contract.RenderPrompt(invocation.WorkDir)}},
	})
	// Finish the notification drain before snapshotting the bounded output; the
	// defer above is the single teardown path for early errors.
	drain := func() {
		_ = client.Close()
		<-notificationsDone
	}
	partial := func(stopReason string) ACPWorkerResult {
		return ACPWorkerResult{
			AgentInfo: initialized.AgentInfo, SessionID: session.SessionID, StopReason: stopReason,
			Output: output.String(), Truncated: truncated, ToolCalls: toolCalls,
		}
	}
	if promptErr != nil {
		drain()
		return partial("error"), fmt.Errorf("run ACP worker prompt: %w", promptErr)
	}
	if supportsSessionClose(initialized.AgentCapabilities) {
		if err := client.CloseSession(ctx, session.SessionID); err != nil {
			drain()
			return partial(prompt.StopReason), fmt.Errorf("close ACP worker session: %w", err)
		}
	}
	drain()
	return partial(prompt.StopReason), nil
}

// boundedACPStderr keeps only a bounded amount of worker stderr for local
// diagnostics and never exposes it to the model. Exceeding the limit discards
// the excess instead of blocking the child.
type boundedACPStderr struct {
	buf   []byte
	limit int
}

func (w *boundedACPStderr) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return len(p), nil
	}
	remaining := w.limit - len(w.buf)
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		w.buf = append(w.buf, p[:remaining]...)
	}
	return len(p), nil
}

// acpRemoteToolCallID namespaces an opaque remote tool-call ID so it can never
// collide with a MothX tool call or be mistaken for a canonical Run ID.
func acpRemoteToolCallID(id string) string {
	if id == "" {
		return ""
	}
	return "acp:" + id
}

func decodeACPToolInput(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return map[string]any{"_raw": string(raw)}
	}
	return args
}

func supportsSessionClose(capabilities map[string]any) bool {
	sessionCaps, ok := capabilities["sessionCapabilities"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = sessionCaps["close"]
	return ok
}

// safeACPBaseEnvironment is the minimal inherited environment an external
// worker needs to execute normally. The profile's own `environment` list
// overlays it; no other process variables (tokens, proxies with credentials,
// cloud credentials) are forwarded implicitly.
var safeACPBaseEnvironment = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP",
	"LANG", "LC_ALL", "LC_CTYPE", "TERM",
	"SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE",
	"APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
}

func acpWorkerEnvironment(profile config.ACPAgentProfile) ([]string, error) {
	configured := config.LoadEnv().List()
	values := map[string]string{}
	for _, name := range safeACPBaseEnvironment {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}
	for _, name := range profile.Environment {
		value, ok := configured[name]
		if !ok {
			value, ok = os.LookupEnv(name)
		}
		if !ok {
			return nil, fmt.Errorf("ACP worker profile %q requires environment variable %q", profile.ID, name)
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+values[name])
	}
	return pairs, nil
}

// maxACPProjectionTextBytes bounds one projected remote field so a verbose or
// hostile worker cannot inflate canonical events or the parent context.
const maxACPProjectionTextBytes = 4 * 1024

func boundACPText(text string) (string, bool) {
	if len(text) <= maxACPProjectionTextBytes {
		return text, false
	}
	bounded, _ := truncateACPText(text, maxACPProjectionTextBytes)
	return bounded, true
}

// truncateACPText returns the longest valid UTF-8 prefix no longer than limit
// bytes. A plain byte slice could split a multi-byte rune and corrupt the
// projected event or completion text.
func truncateACPText(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", len(text) > 0
	}
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}
