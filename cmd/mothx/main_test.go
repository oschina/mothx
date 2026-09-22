package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/acp"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/contextfiles"
	"github.com/oschina/mothx/internal/debugpprof"
	"github.com/oschina/mothx/internal/provider"
)

func TestRootPrintAcceptsMessageArgument(t *testing.T) {
	var gotArgs []string
	var gotOpts runOptions

	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			gotArgs = args
			gotOpts = opts
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	cmd.SetArgs([]string{"-P", "review"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !gotOpts.print {
		t.Fatal("expected print mode to be enabled")
	}
	if want := []string{"review"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
}

func TestArtifactFlagMapsIndependentlyToTerminalAndACP(t *testing.T) {
	var terminalOpts runOptions
	terminal := newRootCommand(
		func(_ []string, opts runOptions) error {
			terminalOpts = opts
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	terminal.SetArgs([]string{"--artifact", "-P", "publish report"})
	if err := terminal.Execute(); err != nil {
		t.Fatalf("execute terminal command: %v", err)
	}
	if !terminalOpts.artifact {
		t.Fatal("terminal --artifact flag was not mapped")
	}

	var acpOpts acp.RunOptions
	acpCommand := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected terminal command execution")
			return nil
		},
		func(opts acp.RunOptions) error {
			acpOpts = opts
			return nil
		},
	)
	acpCommand.SetArgs([]string{"acp", "--artifact"})
	if err := acpCommand.Execute(); err != nil {
		t.Fatalf("execute ACP command: %v", err)
	}
	if !acpOpts.Artifact {
		t.Fatal("ACP --artifact flag was not mapped")
	}
}

func TestBuildInitialMessageForCreatedGlobalConfig(t *testing.T) {
	msg := buildInitialMessage(runInteractiveConfig{
		settings: config.DefaultSettings(),
		settingsMeta: config.LoadMeta{
			CreatedGlobalConfig: true,
			GlobalSettingsPath:  "/tmp/vibecoding/settings.json",
		},
	})

	if !strings.Contains(msg, "Created default config: /tmp/vibecoding/settings.json") {
		t.Fatalf("initial message = %q, want created config path", msg)
	}
	if !strings.Contains(msg, "Opening /auth") {
		t.Fatalf("initial message = %q, want /auth prompt", msg)
	}
}

func TestSetupAgentRuntimeDefaultMultiAgentDoesNotDeadlock(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	p := provider.NewMockProvider("mock", []*provider.Model{{
		ID:            "model1",
		ContextWindow: 128000,
	}}, nil)
	workDir := t.TempDir()

	type result struct {
		runtime runtimeSetup
		err     error
	}
	done := make(chan result, 1)
	go func() {
		runtime, err := setupAgentRuntime(
			context.Background(), p, "mock", p.Models()[0], settings,
			runOptions{}, nil, "runtime-hook-test", workDir,
		)
		done <- result{runtime: runtime, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("setupAgentRuntime: %v", got.err)
		}
		if got.runtime.cleanup != nil {
			got.runtime.cleanup()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setupAgentRuntime deadlocked while registering default sub-agent tools")
	}
}

func TestSetupAgentRuntimeBindsRequestedExpertBeforeAgentManagerAssembly(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	settings.SessionDir = t.TempDir()
	settings.SkillsDir = t.TempDir()
	workDir := t.TempDir()
	sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: settings.SessionDir})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	p := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", ContextWindow: 128000}}, nil)

	runtime, err := setupAgentRuntime(context.Background(), p, "mock", p.Models()[0], settings,
		runOptions{expert: "software-company"}, sess, sess.GetHeader().ID, workDir)
	if err != nil {
		t.Fatalf("setup agent runtime: %v", err)
	}
	t.Cleanup(runtime.cleanup)
	if got := sess.GetExpertID(); got != "software-company" {
		t.Fatalf("session expert = %q, want software-company", got)
	}
	if !runtime.runtime.TeamExpertActive() || runtime.agentManager.Members == nil || runtime.agentManager.ExpertID != "software-company" {
		t.Fatalf("runtime team assembly = runtime=%#v manager=%#v", runtime.runtime, runtime.agentManager)
	}
	if _, ok := runtime.runtime.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("--expert team must register sub-agent tools through the shared runtime")
	}
}

func TestFormatContextFilesInfoIncludesLoadedRule(t *testing.T) {
	tmpDir := t.TempDir()
	rulePath := filepath.Join(tmpDir, contextfiles.RuleFile)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0755); err != nil {
		t.Fatalf("mkdir rule dir: %v", err)
	}
	ruleContent := "project safety rules"
	if err := os.WriteFile(rulePath, []byte(ruleContent), 0644); err != nil {
		t.Fatalf("write rule file: %v", err)
	}

	info := formatContextFilesInfo(&contextfiles.LoadResult{
		ProjectFiles: []contextfiles.FileContent{{Name: "AGENTS.md", Path: filepath.Join(tmpDir, "AGENTS.md"), Content: "# Agent"}},
	}, tmpDir, ruleContent)

	if !strings.Contains(info, "✓ AGENTS.md (project)") {
		t.Fatalf("info = %q, want project context file", info)
	}
	if !strings.Contains(info, "✓ "+contextfiles.RuleFile+" (project rules)") {
		t.Fatalf("info = %q, want loaded rule", info)
	}
}

func TestFormatContextFilesInfoPromptsRuleWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()

	info := formatContextFilesInfo(&contextfiles.LoadResult{}, tmpDir, "")

	if !strings.Contains(info, contextfiles.RuleFile+" not found") {
		t.Fatalf("info = %q, want missing rule", info)
	}
	if !strings.Contains(info, "run /rule") {
		t.Fatalf("info = %q, want /rule prompt", info)
	}
}

func TestRootParsesSessionFlags(t *testing.T) {
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
	cmd.SetArgs([]string{
		"--provider", "openai",
		"--model", "gpt-test",
		"--mode", "plan",
		"--thinking", "high",
		"--continue",
		"--resume", "abc123",
		"--session", "def456",
		"--expert", "frontend-developer",
		"--sandbox",
		"--web-search",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.provider != "openai" {
		t.Fatalf("provider = %q, want openai", got.provider)
	}
	if got.model != "gpt-test" {
		t.Fatalf("model = %q, want gpt-test", got.model)
	}
	if got.mode != "plan" {
		t.Fatalf("mode = %q, want plan", got.mode)
	}
	if got.thinking != "high" {
		t.Fatalf("thinking = %q, want high", got.thinking)
	}
	if !got.continue_ {
		t.Fatal("expected continue flag")
	}
	if got.resume != "abc123" {
		t.Fatalf("resume = %q, want abc123", got.resume)
	}
	if got.session != "def456" {
		t.Fatalf("session = %q, want def456", got.session)
	}
	if got.expert != "frontend-developer" {
		t.Fatalf("expert = %q, want frontend-developer", got.expert)
	}
	if !got.sandbox {
		t.Fatal("expected sandbox flag")
	}
	if !got.webSearch {
		t.Fatal("expected web-search flag")
	}
}

func TestRootParsesWorkflowFlagIndependently(t *testing.T) {
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
	cmd.SetArgs([]string{"--workflows", "-P", "plan workflow"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !got.workflows {
		t.Fatal("expected workflows flag")
	}
	if got.multiAgent {
		t.Fatal("did not expect workflows to enable multi-agent")
	}
	if got.cron {
		t.Fatal("did not expect workflows to enable cron")
	}
}

func TestRootMultiAgentDoesNotEnableWorkflows(t *testing.T) {
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
	cmd.SetArgs([]string{"--multi-agent", "-P", "delegate"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !got.multiAgent {
		t.Fatal("expected multi-agent flag")
	}
	if got.workflows {
		t.Fatal("did not expect multi-agent to enable workflows")
	}
	if got.cron {
		t.Fatal("did not expect multi-agent to enable cron")
	}
}

func TestRootCronFlagDoesNotEnableMultiAgent(t *testing.T) {
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
	cmd.SetArgs([]string{"--cron", "-P", "scheduled task"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !got.cron {
		t.Fatal("expected cron flag")
	}
	if got.multiAgent {
		t.Fatal("did not expect cron to enable multi-agent")
	}
	if got.workflows {
		t.Fatal("did not expect cron to enable workflows")
	}
}

func TestResolveProviderSelectionDefaultsToYolo(t *testing.T) {
	got := resolveProviderSelection(&config.Settings{}, runOptions{})
	if got.mode != "yolo" {
		t.Fatalf("empty settings mode = %q, want yolo", got.mode)
	}
	got = resolveProviderSelection(&config.Settings{DefaultMode: "plan"}, runOptions{})
	if got.mode != "plan" {
		t.Fatalf("settings default mode = %q, want plan", got.mode)
	}
}

func TestACPParsesSharedFlagsWithoutRootFlags(t *testing.T) {
	var got acp.RunOptions

	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(opts acp.RunOptions) error {
			got = opts
			return nil
		},
	)
	cmd.SetArgs([]string{"acp", "-p", "anthropic", "-m", "claude-test", "-M", "yolo", "-t", "medium", "--sandbox", "--verbose", "--debug", "--workflows"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if got.Provider != "anthropic" {
		t.Fatalf("Provider = %q, want anthropic", got.Provider)
	}
	if got.Model != "claude-test" {
		t.Fatalf("Model = %q, want claude-test", got.Model)
	}
	if got.Mode != "yolo" {
		t.Fatalf("Mode = %q, want yolo", got.Mode)
	}
	if got.Thinking != "medium" {
		t.Fatalf("Thinking = %q, want medium", got.Thinking)
	}
	if !got.Sandbox || !got.Verbose || !got.Debug {
		t.Fatalf("flags = sandbox:%v verbose:%v debug:%v, want all true", got.Sandbox, got.Verbose, got.Debug)
	}
	if !got.Workflows {
		t.Fatal("expected workflows flag")
	}
	if got.MultiAgent {
		t.Fatal("did not expect workflows to enable multi-agent")
	}
}

func TestACPStartupErrorSilencesCobraText(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "startup-test"
	settings.DefaultModel = "model"
	settings.Providers = map[string]*config.ProviderConfig{
		"startup-test": {
			APIKey:  "${STARTUP_TEST_API_KEY}",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "model", Name: "Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	previousStderr := os.Stderr
	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writeStderr
	defer func() {
		os.Stderr = previousStderr
		_ = readStderr.Close()
		_ = writeStderr.Close()
	}()

	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		acp.Run,
	)
	cmd.SetArgs([]string{"acp"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected ACP startup error")
	}
	if !cmd.SilenceErrors || !cmd.SilenceUsage {
		t.Fatalf("startup failure did not silence Cobra: errors=%v usage=%v", cmd.SilenceErrors, cmd.SilenceUsage)
	}
	if err := writeStderr.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(readStderr)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(data))
	if !strings.HasPrefix(got, "MOTHX_ACP_ERROR {") || strings.Contains(got, "\n") {
		t.Fatalf("stderr = %q, want one MOTHX_ACP_ERROR JSON line", got)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, "MOTHX_ACP_ERROR ")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "provider_unusable" {
		t.Fatalf("startup code = %q, want provider_unusable", payload.Code)
	}
}

func TestDoctorJSONWritesOnlyOneJSONResponse(t *testing.T) {
	t.Setenv("MOTHX_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd := newDoctorCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version string `json:"version"`
		Checks  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not one JSON response: %q: %v", stdout.String(), err)
	}
	if result.Version == "" || len(result.Checks) == 0 {
		t.Fatalf("doctor result = %#v, want version and checks", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor --json stderr = %q, want empty", stderr.String())
	}
}

func TestRootStillDispatchesACPSubcommand(t *testing.T) {
	var calledACP bool

	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(opts acp.RunOptions) error {
			calledACP = true
			if opts.Model != "test-model" {
				t.Fatalf("model = %q, want test-model", opts.Model)
			}
			return nil
		},
	)
	cmd.SetArgs([]string{"acp", "-m", "test-model"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if !calledACP {
		t.Fatal("expected ACP command execution")
	}
}

func TestUnknownRootFlagSuggestsSimilarFlag(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--modle", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	got := err.Error()
	for _, want := range []string{
		"invalid argument: unknown flag: --modle",
		"Did you mean --model?",
		"Run 'mothx --help' to see all commands.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%s", want, got)
		}
	}
}

func TestUnknownSubcommandFlagSuggestsSimilarFlag(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serve", "--wur-dir", "."})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	got := err.Error()
	for _, want := range []string{
		"invalid argument: unknown flag: --wur-dir",
		"Did you mean --work-dir?",
		"Run 'mothx --help' to see all commands.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%s", want, got)
		}
	}
}

func TestUnknownFlagWithoutSimilarFlagShowsHelpHint(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--definitely-not-a-real-option"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown flag error")
	}
	got := err.Error()
	for _, want := range []string{
		"invalid argument: unknown flag: --definitely-not-a-real-option",
		"Run 'mothx --help' to see all commands.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%s", want, got)
		}
	}
}

func TestMistypedRootSubcommandSuggestsCommand(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serbe"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected mistyped command error")
	}
	got := err.Error()
	for _, want := range []string{
		`invalid argument: unknown command "serbe" for "mothx"`,
		"Did you mean serve?",
		"Run 'mothx --help' to see all commands.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%s", want, got)
		}
	}
}

func TestRootPrintMessageDoesNotRequireCommandSuggestion(t *testing.T) {
	var gotArgs []string

	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			gotArgs = args
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	cmd.SetArgs([]string{"-P", "explain", "this", "code"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}
	if want := []string{"explain", "this", "code"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", gotArgs, want)
	}
}

func TestRootArgsWithoutPrintAreUnknownCommand(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"explain"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	got := err.Error()
	for _, want := range []string{
		`invalid argument: unknown command "explain" for "mothx"`,
		"Run 'mothx --help' to see all commands.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q:\n%s", want, got)
		}
	}
}

func TestInputErrorDoesNotPrintFullHelp(t *testing.T) {
	cmd := newRootCommand(
		func([]string, runOptions) error {
			t.Fatal("unexpected root command execution")
			return nil
		},
		func(acp.RunOptions) error {
			t.Fatal("unexpected ACP command execution")
			return nil
		},
	)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"gatway"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected mistyped command error")
	}
	got := out.String()
	for _, notWant := range []string{
		"Available Commands:",
		"Usage:",
		"Flags:",
	} {
		if strings.Contains(got, notWant) {
			t.Fatalf("output should not include full help section %q:\n%s", notWant, got)
		}
	}
}

func TestInitRunEnvironmentPrintsPprofAddressInTerminal(t *testing.T) {
	// Let the OS pick a free port so the test never collides with a real
	// pprof listener, and reset the debug env knobs to a known state.
	t.Setenv(debugpprof.AddrEnv, "127.0.0.1:0")
	t.Setenv("VIBECODING_DEBUG", "")
	t.Setenv(provider.DebugLogOnlyEnv, "")

	origVerbose := config.Verbose
	origDebugEnabled := debugEnabled
	defer func() {
		config.Verbose = origVerbose
		debugEnabled = origDebugEnabled
	}()

	// Capture stderr, which is where the one-time pprof address line goes.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stderr = w

	// TUI entry point: debug on, print mode off.
	initRunEnvironment(runOptions{debug: true, print: false})

	_ = w.Close()
	os.Stderr = origStderr
	out, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "pprof listening on http://") {
		t.Fatalf("expected pprof address printed to terminal in TUI mode, got: %q", got)
	}
	// Continuous provider debug output must still be kept out of the TUI view.
	if v := os.Getenv(provider.DebugLogOnlyEnv); v != "1" {
		t.Fatalf("%s = %q, want \"1\" so streaming debug lines stay in debug.log", provider.DebugLogOnlyEnv, v)
	}
}
