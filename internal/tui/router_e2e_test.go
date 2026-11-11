//go:build tui_router_e2e

// Package tui Router end-to-end tests.
//
// These tests drive a real TUI submission through the shared Runtime into an
// external ACP worker, then assert the canonical projection and durable
// records. They spawn a real child process for the fake ACP Agent, so they are
// deliberately excluded from the default build and NEVER run in CI:
//
//	go test -tags tui_router_e2e ./internal/tui/ -run 'RouterE2E' -v
//
// Keeping them behind a build tag means `go test ./...` does not even compile
// this file, so the normal CI suite is unaffected.
package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

const routerE2EHelperEnv = "MOTHX_TUI_ROUTER_E2E_HELPER"

const routerE2EWorkerText = "worker inspected the router path"

// Helper behaviour switches, all read from the child process environment.
const (
	// routerE2EPromptDelayEnv delays each prompt response so several workers
	// genuinely overlap instead of finishing before the next one is created.
	routerE2EPromptDelayEnv = "MOTHX_TUI_ROUTER_E2E_PROMPT_DELAY_MS"
	// routerE2EPromptFailEnv makes every prompt fail with an RPC error, which
	// exercises the worker-failure evidence path.
	routerE2EPromptFailEnv = "MOTHX_TUI_ROUTER_E2E_PROMPT_FAIL"
)

// TestRouterE2EHelperProcess is the fake ACP Agent. It is the same test binary
// re-executed as a child process; it speaks ACP v1 NDJSON on stdio and never
// exits normally, so the parent controls its lifetime through stdin.
func TestRouterE2EHelperProcess(t *testing.T) {
	if os.Getenv(routerE2EHelperEnv) != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	var writeMu sync.Mutex
	promptDelay := time.Duration(0)
	if raw := strings.TrimSpace(os.Getenv(routerE2EPromptDelayEnv)); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			promptDelay = time.Duration(ms) * time.Millisecond
		}
	}
	promptFails := os.Getenv(routerE2EPromptFailEnv) == "1"
	nextSession := 0
	write := func(message acpclient.Message) {
		encoded, _ := json.Marshal(message)
		writeMu.Lock()
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
		writeMu.Unlock()
	}
	respond := func(id json.RawMessage, result any) {
		encoded, _ := json.Marshal(result)
		write(acpclient.Message{JSONRPC: "2.0", ID: id, Result: encoded})
	}
	respondError := func(id json.RawMessage, code int, message string) {
		write(acpclient.Message{JSONRPC: "2.0", ID: id, Error: &acpclient.RPCError{Code: code, Message: message}})
	}

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// Exit without letting the test framework print a PASS line: the
			// parent treats every stdout line as an ACP frame.
			os.Exit(0)
		}
		var request acpclient.Message
		if err := json.Unmarshal(line, &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			respond(request.ID, acpclient.InitializeResult{
				ProtocolVersion: acpclient.ProtocolVersion,
				AgentInfo:       acpclient.Implementation{Name: "router-e2e-worker", Version: "1"},
				AgentCapabilities: map[string]any{
					"sessionCapabilities": map[string]any{"close": map[string]any{}},
				},
			})
		case "session/new":
			// A distinct id per session lets the parent prove each worker got
			// its own remote session even though they share one process.
			nextSession++
			respond(request.ID, acpclient.NewSessionResult{SessionID: "router-e2e-session-" + strconv.Itoa(nextSession)})
		case "session/prompt":
			sessionID := acpclient.DecodeSessionID(request.Params)
			if sessionID == "" {
				sessionID = "router-e2e-session"
			}
			if promptDelay > 0 {
				time.Sleep(promptDelay)
			}
			if promptFails {
				respondError(request.ID, -32000, "worker exploded")
				continue
			}
			update := map[string]any{
				"sessionId": sessionID,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": routerE2EWorkerText},
				},
			}
			encoded, _ := json.Marshal(update)
			write(acpclient.Message{JSONRPC: "2.0", Method: "session/update", Params: encoded})
			respond(request.ID, acpclient.PromptResult{StopReason: "end_turn"})
		default:
			respond(request.ID, map[string]any{})
		}
	}
}

// scriptedProvider returns one pre-recorded response batch per Chat call. It is
// stateful by design: MockProvider replays the same batch forever, which makes a
// lead that requests a tool loop to the iteration ceiling instead of finishing.
type scriptedProvider struct {
	mu    sync.Mutex
	name  string
	model *provider.Model
	steps [][]provider.StreamEvent
	calls int
}

func newScriptedProvider(name string, steps ...[]provider.StreamEvent) *scriptedProvider {
	return &scriptedProvider{
		name:  name,
		model: &provider.Model{ID: "model", ContextWindow: 128000},
		steps: steps,
	}
}

func (p *scriptedProvider) Chat(ctx context.Context, _ provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	index := p.calls
	p.calls++
	p.mu.Unlock()

	var step []provider.StreamEvent
	if index < len(p.steps) {
		step = p.steps[index]
	}
	ch := make(chan provider.StreamEvent, len(step)+1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
			return
		default:
		}
		for _, event := range step {
			select {
			case <-ctx.Done():
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
				return
			case ch <- event:
			}
		}
	}()
	return ch
}

func (p *scriptedProvider) Name() string                       { return p.name }
func (p *scriptedProvider) API() string                        { return "openai-chat" }
func (p *scriptedProvider) Models() []*provider.Model          { return []*provider.Model{p.model} }
func (p *scriptedProvider) GetModel(id string) *provider.Model { return p.model }

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// blockingProvider parks every Chat call until its context is cancelled. It
// models a lead that is mid-request so the adapter's Esc path can be exercised.
type blockingProvider struct {
	mu    sync.Mutex
	name  string
	model *provider.Model
	calls int
}

func (p *blockingProvider) Chat(ctx context.Context, _ provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
	}()
	return ch
}

func (p *blockingProvider) Name() string                       { return p.name }
func (p *blockingProvider) API() string                        { return "openai-chat" }
func (p *blockingProvider) Models() []*provider.Model          { return []*provider.Model{p.model} }
func (p *blockingProvider) GetModel(id string) *provider.Model { return p.model }

// routerE2EFixture assembles the exact production path: a persisted session, a
// shared Runtime, an AgentManager built by the Runtime, and one eligible ACP
// worker profile backed by the helper child process.
type routerE2EFixture struct {
	app        *App
	runtime    *agentruntime.SessionRuntime
	manager    *session.Manager
	sessionID  string
	sessionDir string
	workDir    string
}

// routerE2EOptions configures the worker profiles the fixture registers.
type routerE2EOptions struct {
	// workerIDs are the configured ACP worker profiles. The first one is the
	// Router default target.
	workerIDs []string
	// maxConcurrentChildren is the per-profile live-worker ceiling. Zero keeps
	// the product default of one.
	maxConcurrentChildren int
	// helperEnv carries extra switches to the fake ACP Agent process.
	helperEnv map[string]string
}

func newRouterE2EFixture(t *testing.T, p provider.Provider) *routerE2EFixture {
	t.Helper()
	return newRouterE2EFixtureWithOptions(t, p, routerE2EOptions{})
}

func newRouterE2EFixtureWithOptions(t *testing.T, p provider.Provider, opts routerE2EOptions) *routerE2EFixture {
	t.Helper()
	t.Setenv(routerE2EHelperEnv, "1")
	for key, value := range opts.helperEnv {
		t.Setenv(key, value)
	}

	workerIDs := opts.workerIDs
	if len(workerIDs) == 0 {
		workerIDs = []string{"e2e-worker"}
	}

	workDir, sessionDir := t.TempDir(), t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = sessionDir
	settings.SkillsDir = t.TempDir()
	// The Runtime forwards only the safe base environment plus the names the
	// profile declares explicitly. The helper switches must therefore be
	// declared here, exactly like any other worker credential.
	helperEnv := []string{routerE2EHelperEnv}
	for key := range opts.helperEnv {
		helperEnv = append(helperEnv, key)
	}
	for _, id := range workerIDs {
		settings.ACPAgents = append(settings.ACPAgents, config.ACPAgentProfile{
			ID:      id,
			Command: []string{os.Args[0], "-test.run=TestRouterE2EHelperProcess", "--"},
			Enabled: true, Environment: helperEnv,
			PermissionPolicy:      config.ACPPermissionAutoAllowOnce,
			Roles:                 []config.ACPAgentRole{config.ACPRoleInvestigate},
			MaxConcurrentChildren: opts.maxConcurrentChildren,
		})
	}

	manager, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{
		WorkDir: workDir, SessionDir: sessionDir,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	runtime, err := (agentruntime.Builder{Settings: settings}).Build(context.Background(), agentruntime.BuildOptions{
		ID: manager.GetHeader().ID, Source: agentruntime.SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	t.Cleanup(runtime.Close)

	app := NewApp(p, p.Models()[0], settings, manager, runtime.Registry, "",
		runtime.ExtraContext, runtime.RuleContent, runtime.SkillsMgr, "yolo",
		true /*multiAgent*/, true /*delegateMode*/, nil, nil, nil)
	app.SetRuntime(runtime)
	if err := app.refreshExpertAwareAgentManager(); err != nil {
		t.Fatalf("assemble agent manager: %v", err)
	}

	runRouterActivation(t, app, app.handleCommand("/router "+strings.Join(workerIDs, ",")))
	if app.mode != agentruntime.ModeRouter {
		t.Fatalf("Router activation failed: mode=%q transcript=%q",
			app.mode, stripANSI(strings.Join(app.messages, "\n")))
	}

	return &routerE2EFixture{
		app: app, runtime: runtime, manager: manager,
		sessionID: manager.GetHeader().ID, sessionDir: sessionDir, workDir: workDir,
	}
}

// startPrompt submits one input through the adapter and installs the resulting
// stream. The returned channel is the canonical Agent event stream.
func (f *routerE2EFixture) startPrompt(t *testing.T, input string) <-chan agent.Event {
	t.Helper()
	cmd := f.app.processInput(input)
	if cmd == nil {
		t.Fatalf("processInput(%q) returned nil", input)
	}
	msg, ok := cmd().(agentStreamStartMsg)
	if !ok || msg.err != nil || msg.eventCh == nil {
		t.Fatalf("stream start = %#v", msg)
	}
	f.app.Update(msg)
	return msg.eventCh
}

// pumpUntilIdle forwards events into the adapter until the run terminalizes.
// It mirrors the Bubble Tea loop without a real terminal.
func (f *routerE2EFixture) pumpUntilIdle(t *testing.T, events <-chan agent.Event, timeout time.Duration) *agent.Event {
	t.Helper()
	deadline := time.After(timeout)
	var terminal *agent.Event
	for f.app.isThinking {
		select {
		case event, open := <-events:
			if !open {
				f.app.Update(agentDoneMsg{eventCh: events})
				continue
			}
			copied := event
			f.app.Update(agentEventMsg{event: event, eventCh: events})
			if event.Type == agent.EventRunFinished {
				terminal = &copied
			}
		case <-deadline:
			t.Fatal("Router run never terminalized")
		}
	}
	return terminal
}

func (f *routerE2EFixture) transcript() string {
	// Assistant and thinking text live in assistantRaw/thinkRaw with an empty
	// placeholder in App.messages, so render each slot through the same path the
	// live transcript uses instead of joining the raw slice.
	blocks := make([]string, 0, len(f.app.messages))
	for index := range f.app.messages {
		if rendered := strings.TrimSpace(stripANSI(f.app.renderMessageAt(index))); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	f.app.printMu.Lock()
	printed := append([]string(nil), f.app.printQueue...)
	f.app.printMu.Unlock()
	for _, line := range printed {
		if rendered := strings.TrimSpace(stripANSI(line)); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return strings.Join(blocks, "\n")
}

// waitForChildRuns waits until the session has the expected number of durable
// child records. A spawned external worker outlives the lead turn that created
// it, so its history row can land after the lead run already terminalized.
func (f *routerE2EFixture) waitForChildRuns(t *testing.T, count int, timeout time.Duration) []session.ChildRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var children []session.ChildRun
	for {
		loaded, err := session.ListChildRuns(f.sessionDir, f.sessionID)
		if err != nil {
			t.Fatalf("list child runs: %v", err)
		}
		children = loaded
		if len(children) >= count {
			return children
		}
		if time.Now().After(deadline) {
			t.Fatalf("child runs = %d (%#v), want %d", len(children), children, count)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForChildTerminal waits until every durable child record reached a
// terminal state.
func (f *routerE2EFixture) waitForChildTerminal(t *testing.T, count int, timeout time.Duration) []session.ChildRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		children := f.waitForChildRuns(t, count, timeout)
		terminal := 0
		for _, child := range children {
			switch child.Status {
			case string(agent.TaskSuccess), string(agent.TaskError),
				string(agent.TaskIncomplete), string(agent.TaskCanceled):
				terminal++
			}
		}
		if terminal == count {
			return children
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal child runs = %d of %d (%#v)", terminal, count, children)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRouterE2EDelegatesToACPWorker is the core end-to-end contract: a TUI
// submission in Router mode reaches the external ACP worker, the lead's final
// text is projected, and the child is durably recorded as an ACP run with a
// real remote session identity. The last assertion is what catches a silent
// downgrade to a local child or a swallowed delegation error.
func TestRouterE2EDelegatesToACPWorker(t *testing.T) {
	delegate := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"delegate","agent":"e2e-worker","task":"inspect the router path","role":"investigate"}`),
		}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	finish := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "delegation complete"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	p := newScriptedProvider("mock", delegate, finish)
	fixture := newRouterE2EFixture(t, p)
	defer fixture.app.waitForRunFinalization()

	events := fixture.startPrompt(t, "delegate an inspection")
	terminal := fixture.pumpUntilIdle(t, events, 90*time.Second)
	if terminal == nil || terminal.Status != agent.TaskSuccess {
		t.Fatalf("terminal event = %#v, want success", terminal)
	}
	if got := p.callCount(); got < 2 {
		t.Fatalf("lead provider calls = %d, want at least 2 (tool + final)", got)
	}

	if transcript := fixture.transcript(); !strings.Contains(transcript, "delegation complete") {
		t.Fatalf("lead final text was not projected: %q", transcript)
	}

	// The canonical proof that the external worker actually ran.
	children, err := session.ListChildRuns(fixture.sessionDir, fixture.sessionID)
	if err != nil {
		t.Fatalf("list child runs: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("child runs = %d (%#v), want exactly one external worker", len(children), children)
	}
	child := children[0]
	if child.Backend != "acp" || child.ProfileID != "e2e-worker" {
		t.Fatalf("child run backend/profile = %q/%q, want acp/e2e-worker", child.Backend, child.ProfileID)
	}
	if child.RemoteSessionID == "" {
		t.Fatal("child run has no remote session identity; the ACP worker was never reached")
	}
	if child.Status != string(agent.TaskSuccess) {
		t.Fatalf("child status = %q, want %q", child.Status, agent.TaskSuccess)
	}
	if !strings.Contains(child.TranscriptJSON, routerE2EWorkerText) {
		t.Fatalf("child transcript does not contain the worker output: %s", child.TranscriptJSON)
	}
}

// TestRouterE2EProjectsWorkerTranscriptSeparately proves the worker's output is
// attributed to the child event stream, not fabricated as lead text. The lead
// says one thing; the worker says another; both must be observable and distinct.
func TestRouterE2EProjectsWorkerTranscriptSeparately(t *testing.T) {
	delegate := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"delegate","agent":"e2e-worker","task":"inspect","role":"investigate"}`),
		}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	finish := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "lead summary only"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	fixture := newRouterE2EFixture(t, newScriptedProvider("mock", delegate, finish))
	defer fixture.app.waitForRunFinalization()

	events := fixture.startPrompt(t, "delegate an inspection")
	fixture.pumpUntilIdle(t, events, 90*time.Second)

	children, err := session.ListChildRuns(fixture.sessionDir, fixture.sessionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("child runs = %#v (err %v), want one", children, err)
	}
	if !strings.Contains(children[0].TranscriptJSON, routerE2EWorkerText) {
		t.Fatalf("worker output missing from the child transcript: %s", children[0].TranscriptJSON)
	}
	if strings.Contains(children[0].TranscriptJSON, "lead summary only") {
		t.Fatal("lead text leaked into the child transcript")
	}
	transcript := fixture.transcript()
	if !strings.Contains(transcript, "lead summary only") {
		t.Fatalf("lead text missing from the TUI transcript: %q", transcript)
	}
}

// TestRouterE2EFansOutReadOnlyWorkersConcurrently proves the Runtime admits
// several read-only external workers at once when the profile's ceiling
// allows it, and that each one owns a distinct remote session. The helper
// delays every prompt so the workers genuinely overlap instead of finishing
// before the next one is created.
//
// The workers are spawned by the same lead turn, so the lead's run ends while
// the workers are still executing. Each worker therefore has to survive its
// parent's termination to reach a terminal state of its own.
func TestRouterE2EFansOutReadOnlyWorkersConcurrently(t *testing.T) {
	spawn := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"spawn","agent":"e2e-worker","task":"inspect the first path","role":"investigate"}`),
		}},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-2",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"spawn","agent":"e2e-worker","task":"inspect the second path","role":"investigate"}`),
		}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	finish := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "both workers reported"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	p := newScriptedProvider("mock", spawn, finish)
	fixture := newRouterE2EFixtureWithOptions(t, p, routerE2EOptions{
		workerIDs:             []string{"e2e-worker"},
		maxConcurrentChildren: 2,
		helperEnv:             map[string]string{routerE2EPromptDelayEnv: "400"},
	})
	defer fixture.app.waitForRunFinalization()

	events := fixture.startPrompt(t, "fan out two inspections")
	fixture.pumpUntilIdle(t, events, 120*time.Second)

	children := fixture.waitForChildTerminal(t, 2, 60*time.Second)
	if len(children) != 2 {
		t.Fatalf("child runs = %d (%#v), want two concurrent workers", len(children), children)
	}
	sessions := map[string]bool{}
	for _, child := range children {
		if child.Backend != "acp" {
			t.Fatalf("child backend = %q, want acp", child.Backend)
		}
		if child.RemoteSessionID == "" {
			t.Fatal("a child has no remote session identity")
		}
		if sessions[child.RemoteSessionID] {
			t.Fatalf("two workers share the remote session %q", child.RemoteSessionID)
		}
		sessions[child.RemoteSessionID] = true
	}
	if len(sessions) != 2 {
		t.Fatalf("distinct remote sessions = %d, want 2", len(sessions))
	}
	// Both workers must reach a terminal state of their own. A worker that is
	// cancelled with its parent never completes its task, so the fan-out did
	// not actually happen.
	for _, child := range children {
		if child.Status == string(agent.TaskCanceled) {
			t.Fatalf("worker %q was cancelled with its parent instead of completing: %#v", child.ChildID, child)
		}
	}
	if transcript := fixture.transcript(); !strings.Contains(transcript, "both workers reported") {
		t.Fatalf("lead final text was not projected: %q", transcript)
	}
}

// TestRouterE2ERejectsFanOutAboveProfileCeiling proves the Runtime keeps
// enforcing the configured live-worker ceiling. With a ceiling of one, the
// second spawn must be refused instead of silently running.
func TestRouterE2ERejectsFanOutAboveProfileCeiling(t *testing.T) {
	spawn := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"spawn","agent":"e2e-worker","task":"first inspection","role":"investigate"}`),
		}},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-2",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"spawn","agent":"e2e-worker","task":"second inspection","role":"investigate"}`),
		}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	finish := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "fan out settled"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	fixture := newRouterE2EFixtureWithOptions(t, newScriptedProvider("mock", spawn, finish), routerE2EOptions{
		workerIDs:             []string{"e2e-worker"},
		maxConcurrentChildren: 1,
		helperEnv:             map[string]string{routerE2EPromptDelayEnv: "400"},
	})
	defer fixture.app.waitForRunFinalization()

	events := fixture.startPrompt(t, "fan out two inspections")
	fixture.pumpUntilIdle(t, events, 120*time.Second)

	children, err := session.ListChildRuns(fixture.sessionDir, fixture.sessionID)
	if err != nil {
		t.Fatalf("list child runs: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("child runs = %d (%#v), want the ceiling to admit exactly one", len(children), children)
	}
	// The refused spawn must be visible as evidence, not a silent success.
	transcript := fixture.transcript()
	if !strings.Contains(transcript, "fan out settled") {
		t.Fatalf("lead final text was not projected: %q", transcript)
	}
}

// TestRouterE2EReportsWorkerFailureAsEvidence proves a failing external worker
// is surfaced as failure evidence instead of being reported as success. The
// lead still finishes, but the durable child record must carry the error and
// the transcript must not claim the inspection succeeded.
func TestRouterE2EReportsWorkerFailureAsEvidence(t *testing.T) {
	delegate := []provider.StreamEvent{
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "delegate_worker_mgr",
			Arguments: []byte(`{"action":"delegate","agent":"e2e-worker","task":"inspect the failing path","role":"investigate"}`),
		}},
		{Type: provider.StreamDone, StopReason: "toolUse"},
	}
	finish := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "the worker did not succeed"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	fixture := newRouterE2EFixtureWithOptions(t, newScriptedProvider("mock", delegate, finish), routerE2EOptions{
		helperEnv: map[string]string{routerE2EPromptFailEnv: "1"},
	})
	defer fixture.app.waitForRunFinalization()

	events := fixture.startPrompt(t, "delegate a failing inspection")
	fixture.pumpUntilIdle(t, events, 120*time.Second)

	children, err := session.ListChildRuns(fixture.sessionDir, fixture.sessionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("child runs = %#v (err %v), want one", children, err)
	}
	child := children[0]
	if child.Status == string(agent.TaskSuccess) {
		t.Fatalf("failing worker recorded success: %#v", child)
	}
	if child.Status == "" {
		t.Fatalf("failing worker recorded no terminal status: %#v", child)
	}
	if child.Backend != "acp" || child.RemoteSessionID == "" {
		t.Fatalf("failing worker lost its ACP identity: %#v", child)
	}
}

// TestRouterE2EEscCancelsRunAndAllowsNextSubmission proves an interrupted
// Router run releases the execution lease: the next submission must start
// instead of being queued behind a dead run.
func TestRouterE2EEscCancelsRunAndAllowsNextSubmission(t *testing.T) {
	blocking := &blockingProvider{name: "mock", model: &provider.Model{ID: "model", ContextWindow: 128000}}
	fixture := newRouterE2EFixture(t, blocking)
	defer fixture.app.waitForRunFinalization()

	fixture.startPrompt(t, "wait forever")
	if !fixture.app.isThinking {
		t.Fatal("run did not enter the thinking state")
	}

	fixture.app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	fixture.app.waitForRunFinalization()
	if fixture.app.isThinking {
		t.Fatal("Esc did not terminalize the Router run")
	}
	if fixture.app.run != nil {
		t.Fatal("Esc left the TUI run attached")
	}

	// A fresh submission must be admitted. It will block on the provider again,
	// so only assert that it starts, then cancel it to leave no goroutine behind.
	cmd := fixture.app.processInput("try again")
	if cmd == nil {
		t.Fatal("next submission returned nil after cancellation")
	}
	result := make(chan any, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		start, ok := msg.(agentStreamStartMsg)
		if !ok || start.err != nil || start.eventCh == nil {
			t.Fatalf("next stream start = %#v", msg)
		}
		fixture.app.Update(start)
		fixture.app.Update(tea.KeyMsg{Type: tea.KeyEsc})
		fixture.app.waitForRunFinalization()
	case <-time.After(30 * time.Second):
		t.Fatal("next submission never started; the execution lease was not released")
	}
}
