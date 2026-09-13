package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/acp"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

func TestRunPrintFailsWhenApprovalWouldBeRequired(t *testing.T) {
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1", ContextWindow: 128000}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{ID: "call_1", Name: "bash", Arguments: []byte(`{"command":"python script.py"}`)}},
		{Type: provider.StreamDone},
	})
	registry := tools.NewRegistry(t.TempDir(), nil)
	registry.RegisterDefaults()
	settings := config.DefaultSettings()
	settings.Approval.BashWhitelist = []string{"go "}

	err := runPrint(
		[]string{"run"},
		p,
		"configured-provider",
		p.Models()[0],
		"agent",
		provider.ThinkingOff,
		settings,
		registry,
		(*session.Manager)(nil),
		"",
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err == nil {
		t.Fatal("expected runPrint to fail when approval is required")
	}
	if !strings.Contains(err.Error(), "tool approval required in print mode") {
		t.Fatalf("err = %q, want approval error", err)
	}
}

func TestRunPrintJSONOutputsAssistantText(t *testing.T) {
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1", ContextWindow: 128000}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "Hello, "},
		{Type: provider.StreamTextDelta, TextDelta: "world!"},
		{Type: provider.StreamDone},
	})
	registry := tools.NewRegistry(t.TempDir(), nil)
	registry.RegisterDefaults()
	settings := config.DefaultSettings()

	// Capture stdout so the emitted JSON object can be inspected.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		b, err := io.ReadAll(r)
		ch <- readResult{data: b, err: err}
	}()

	err = runPrint(
		[]string{"hi"},
		p,
		"configured-provider",
		p.Models()[0],
		"agent",
		provider.ThinkingOff,
		settings,
		registry,
		(*session.Manager)(nil),
		"",
		"",
		false,
		false,
		false,
		true, // jsonOut
		nil,
	)
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe: %v", cerr)
	}
	res := <-ch
	if err != nil {
		t.Fatalf("runPrint: %v", err)
	}
	if res.err != nil {
		t.Fatalf("read stdout: %v", res.err)
	}

	// stdout is now a stream of NDJSON lines (one per event).
	lines := strings.Split(strings.TrimRight(string(res.data), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected at least one NDJSON line, got empty output")
	}

	// The first line must be a "start" event carrying provider/model/mode.
	var startEv printJSONEvent
	if jerr := json.Unmarshal([]byte(lines[0]), &startEv); jerr != nil {
		t.Fatalf("unmarshal first NDJSON line: %v\nline: %s", jerr, lines[0])
	}
	if startEv.Type != "start" {
		t.Fatalf("first event type = %q, want %q", startEv.Type, "start")
	}
	if startEv.Provider != "mock" {
		t.Fatalf("start.provider = %q, want %q", startEv.Provider, "mock")
	}
	if startEv.Model != "model1" {
		t.Fatalf("start.model = %q, want %q", startEv.Model, "model1")
	}
	if startEv.Mode != "agent" {
		t.Fatalf("start.mode = %q, want %q", startEv.Mode, "agent")
	}

	var text strings.Builder
	var sawDone, sawError bool
	for i, line := range lines {
		var ev printJSONEvent
		if jerr := json.Unmarshal([]byte(line), &ev); jerr != nil {
			t.Fatalf("unmarshal NDJSON line %d: %v\nline: %s", i, jerr, line)
		}
		switch ev.Type {
		case "text_delta":
			text.WriteString(ev.Text)
		case "done":
			sawDone = true
		case "error":
			sawError = true
		}
	}
	if got := text.String(); got != "Hello, world!" {
		t.Fatalf("accumulated text = %q, want %q", got, "Hello, world!")
	}
	if !sawDone {
		t.Fatal("expected a terminating done event in the NDJSON stream")
	}
	if sawError {
		t.Fatal("did not expect an error event in the NDJSON stream")
	}
}

func TestRunPrintJSONOutputsHostedItem(t *testing.T) {
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", ContextWindow: 128000}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamHostedItem, HostedItem: &provider.HostedItem{ID: "search-1", Type: "web_search_call", Status: "completed", OutputIndex: 1}},
		{Type: provider.StreamTextDelta, TextDelta: "done"},
		{Type: provider.StreamDone},
	})
	registry := tools.NewRegistry(t.TempDir(), nil)
	registry.RegisterDefaults()
	settings := config.DefaultSettings()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	readDone := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(r); readDone <- data }()
	if err := runPrint([]string{"hi"}, p, "configured-provider", p.Models()[0], "agent", provider.ThinkingOff, settings, registry, nil, "", "", false, false, false, true, nil); err != nil {
		t.Fatalf("runPrint: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(<-readDone)), "\n")
	var found printJSONEvent
	for _, line := range lines {
		var event printJSONEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "hosted_item" {
			found = event
			break
		}
	}
	if found.HostedItem == nil || found.HostedItem.ID != "search-1" || found.HostedItem.Status != "completed" {
		t.Fatalf("hosted NDJSON event = %#v", found)
	}
}

func TestRunPrintPersistsCanonicalDurableRun(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	mgr := session.New(workDir, sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", ContextWindow: 128000}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "done"},
		{Type: provider.StreamDone},
	})
	registry := tools.NewRegistry(workDir, nil)
	registry.RegisterDefaults()

	if err := runPrint([]string{"hi"}, p, "configured-provider", p.Models()[0], "yolo", provider.ThinkingOff, config.DefaultSettings(), registry, mgr, "", "", false, false, false, false, nil); err != nil {
		t.Fatalf("runPrint: %v", err)
	}
	runs, err := session.ListSessionRuns(sessionDir, mgr.GetHeader().ID, 10)
	if err != nil {
		t.Fatalf("list session runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run count = %d, want 1", len(runs))
	}
	if runs[0].Source != "cli" || runs[0].Status != "completed" || runs[0].IntentID == "" {
		t.Fatalf("durable CLI run = %#v, want completed cli run with intent", runs[0])
	}
	active, err := agentruntime.GetActiveDurableRun(t.Context(), sessionDir, mgr.GetHeader().ID)
	if err != nil {
		t.Fatalf("get active run: %v", err)
	}
	if active != nil {
		t.Fatalf("active CLI run remains after completion: %#v", active)
	}
}

func TestRootPrintJSONFlagParsesIntoRunOptions(t *testing.T) {
	var got runOptions

	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			got = opts
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	cmd.SetArgs([]string{"-P", "--json", "summarize"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !got.print {
		t.Fatal("expected print flag")
	}
	if !got.json {
		t.Fatal("expected json flag")
	}
}

func TestRunSetsProviderDebugEnvWhenDebugFlagSet(t *testing.T) {
	origDebug := debugEnabled
	origEnv, hadEnv := os.LookupEnv("VIBECODING_DEBUG")
	defer func() {
		debugEnabled = origDebug
		if hadEnv {
			_ = os.Setenv("VIBECODING_DEBUG", origEnv)
		} else {
			_ = os.Unsetenv("VIBECODING_DEBUG")
		}
	}()
	_ = os.Unsetenv("VIBECODING_DEBUG")

	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			debugEnabled = opts.debug
			if opts.debug {
				_ = os.Setenv("VIBECODING_DEBUG", "1")
			}
			return nil
		},
		nil,
	)
	cmd.SetArgs([]string{"--debug", "-P", "hello"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got := os.Getenv("VIBECODING_DEBUG"); got != "1" {
		t.Fatalf("VIBECODING_DEBUG = %q, want 1", got)
	}
}
