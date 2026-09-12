package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/startvibecoding/mothx/internal/a2a"
	"github.com/startvibecoding/mothx/internal/acp"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/contextfiles"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/debugpprof"
	"github.com/startvibecoding/mothx/internal/platform"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/serve"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/systeminit"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/tui"
	"github.com/startvibecoding/mothx/internal/update"
	appversion "github.com/startvibecoding/mothx/internal/version"
	"github.com/startvibecoding/mothx/internal/workflow"
)

var version = appversion.Current()

func main() {
	_ = platform.EnsureWindowsBusybox()
	rootCmd := newRootCommand(run, acp.Run)
	exitCode := 0
	if err := rootCmd.Execute(); err != nil {
		exitCode = 1
	}
	if err := session.CloseDatabases(); err != nil {
		fmt.Fprintf(os.Stderr, "close session databases: %v\n", err)
		exitCode = 1
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func newRootCommand(runFn func([]string, runOptions) error, acpRunFn func(acp.RunOptions) error) *cobra.Command {
	flags := &cliFlags{}

	rootCmd := newCLICommand(flags, runFn)
	rootCmd.AddCommand(newACPCommand(flags, acpRunFn))
	rootCmd.AddCommand(newServeCommand(flags))
	rootCmd.AddCommand(newA2ACommand())
	rootCmd.AddCommand(newDoctorCommand())
	rootCmd.AddCommand(newSystemInitCommand(runFn, &flags.provider, &flags.model))
	rootCmd.AddCommand(newStatsCommand())
	rootCmd.AddCommand(newSpeedtestCommand())
	rootCmd.AddCommand(newKnowledgeMCPCommand())
	installFriendlyFlagErrors(rootCmd)
	return rootCmd
}

type cliFlags struct {
	provider        string
	model           string
	mode            string
	thinking        string
	continueSession bool
	resume          string
	session         string
	sandbox         bool
	print           bool
	json            bool
	verbose         bool
	debug           bool
	expert          string
	multiAgent      bool
	delegate        bool
	workflows       bool
	cron            bool
	webSearch       bool
	browser         bool
	artifact        bool
	initServe       bool
	force           bool
	enableA2AMaster bool
	initA2AMaster   bool
	workDir         string
	serveConfig     string
	servePort       string
	serveWebUIDir   string
	serveUnsafe     bool
	lobsterMode     bool

	acpPermissionTimeout string
	acpQuestionTimeout   string
}

func newCLICommand(flags *cliFlags, runFn func([]string, runOptions) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:     cliUseName(),
		Aliases: []string{"vc"},
		Short:   "MothX - AI coding assistant",
		Long:    "MothX is an AI-powered coding assistant that runs in your terminal.\nSupports OpenAI and Anthropic APIs with sandboxed execution.",
		Version: version,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRootArgs(cmd, args, flags); err != nil {
				return err
			}
			return runCLICommand(args, flags, runFn)
		},
	}
	registerRootFlags(cmd.Flags(), flags)
	return cmd
}

func cliUseName() string {
	base := filepath.Base(os.Args[0])
	if strings.HasPrefix(base, "mothx") {
		return "mothx"
	}
	return "vibecoding"
}

func runCLICommand(args []string, flags *cliFlags, runFn func([]string, runOptions) error) error {
	if flags.initA2AMaster {
		path, err := a2a.InitA2AMasterConfig(flags.force)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Created a2a master config: %s\n", path)
		return nil
	}
	if flags.initServe {
		path, err := serve.InitConfig(flags.force)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Created serve config: %s\n", path)
		return nil
	}
	return runFn(args, flags.runOptions())
}

func newACPCommand(flags *cliFlags, acpRunFn func(acp.RunOptions) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Run the Agent Client Protocol server",
		Long:  "Run MothX as an ACP-compliant stdio agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := acpRunFn(flags.acpOptions())
			if acp.IsStartupError(err) {
				cmd.Root().SilenceErrors = true
				cmd.Root().SilenceUsage = true
			}
			return err
		},
	}
	registerACPFlags(cmd.Flags(), flags)
	return cmd
}

func registerRootFlags(fs *pflag.FlagSet, flags *cliFlags) {
	registerSharedProviderFlags(fs, flags)
	fs.BoolVarP(&flags.continueSession, "continue", "c", false, "Continue most recent session")
	fs.StringVarP(&flags.resume, "resume", "r", "", "Resume session by ID or path")
	fs.StringVar(&flags.session, "session", "", "Use specific session file or ID")
	fs.StringVar(&flags.expert, "expert", "", "Bind an expert bundle to this session (switching an existing expert requires a fork)")
	fs.BoolVarP(&flags.print, "print", "P", false, "Print response and exit (non-interactive)")
	fs.BoolVar(&flags.json, "json", false, "Stream print-mode output as one JSON object per line (NDJSON) to stdout (requires -P)")
	registerSharedExecutionFlags(fs, flags, "Enable configured web search provider for this run")
	fs.BoolVar(&flags.initServe, "init-serve", false, "Create serve.json config template")
	fs.BoolVar(&flags.force, "force", false, "Force overwrite existing files (used with --init-*)")
	fs.BoolVar(&flags.cron, "cron", false, "Enable scheduled task management (cron tool)")
	fs.BoolVar(&flags.enableA2AMaster, "enable-a2a-master", false, "Enable A2A master mode (dispatch tasks to remote agents)")
	fs.BoolVar(&flags.initA2AMaster, "init-a2a-master-config", false, "Create a2a-list.json config template")
}

func registerACPFlags(fs *pflag.FlagSet, flags *cliFlags) {
	registerSharedProviderFlags(fs, flags)
	registerSharedExecutionFlags(fs, flags, "Enable configured web search provider for this ACP run")
	fs.StringVar(&flags.acpPermissionTimeout, "permission-timeout", "", "Approval decision timeout for ACP session/request_permission (Go duration, e.g. 30m; env MOTHX_ACP_PERMISSION_TIMEOUT; default 5m)")
	fs.StringVar(&flags.acpQuestionTimeout, "question-timeout", "", "Question decision timeout for ACP question requests (Go duration, e.g. 30m; env MOTHX_ACP_QUESTION_TIMEOUT; default 5m)")
}

// resolveACPTimeout resolves one ACP decision timeout. The explicit flag wins
// over the environment variable; invalid or non-positive values are ignored so
// the zero value lets acp.Run fall back to its documented defaults (5m for
// approvals, 5m for questions).
func resolveACPTimeout(flagValue, envKey string) time.Duration {
	for _, candidate := range []string{flagValue, os.Getenv(envKey)} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if parsed, err := time.ParseDuration(candidate); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func registerSharedProviderFlags(fs *pflag.FlagSet, flags *cliFlags) {
	fs.StringVarP(&flags.provider, "provider", "p", "", "Provider (openai, anthropic, or custom provider name)")
	fs.StringVarP(&flags.model, "model", "m", "", "Model ID")
	fs.StringVarP(&flags.mode, "mode", "M", "", "Mode (plan, agent, yolo, os)")
	fs.StringVarP(&flags.thinking, "thinking", "t", "", "Thinking level (off, minimal, low, medium, high, xhigh, max)")
}

func registerSharedExecutionFlags(fs *pflag.FlagSet, flags *cliFlags, webSearchUsage string) {
	fs.BoolVar(&flags.sandbox, "sandbox", false, "Enable sandbox (bwrap) for secure execution")
	fs.BoolVar(&flags.verbose, "verbose", false, "Verbose output")
	fs.BoolVar(&flags.debug, "debug", false, "Enable debug logging")
	fs.BoolVar(&flags.multiAgent, "multi-agent", false, "Enable multi-agent mode (sub-agent tools)")
	fs.BoolVar(&flags.delegate, "delegate", false, "Enable delegation mode (blocking single sub-agent tool)")
	fs.BoolVar(&flags.workflows, "workflows", false, "Enable workflow mode (JavaScript workflow tools)")
	fs.BoolVar(&flags.webSearch, "web-search", false, webSearchUsage)
	fs.BoolVar(&flags.browser, "browser", false, "Enable browser automation tool")
	fs.BoolVar(&flags.artifact, "artifact", false, "Enable generated artifact publishing")
}

func (f *cliFlags) runOptions() runOptions {
	return runOptions{
		provider:        f.provider,
		model:           f.model,
		mode:            f.mode,
		thinking:        f.thinking,
		continue_:       f.continueSession,
		resume:          f.resume,
		session:         f.session,
		sandbox:         f.sandbox,
		print:           f.print,
		json:            f.json,
		verbose:         f.verbose,
		debug:           f.debug,
		expert:          f.expert,
		multiAgent:      f.multiAgent,
		delegate:        f.delegate,
		workflows:       f.workflows,
		cron:            f.cron,
		webSearch:       f.webSearch,
		browser:         f.browser,
		artifact:        f.artifact,
		enableA2AMaster: f.enableA2AMaster,
	}
}

func (f *cliFlags) acpOptions() acp.RunOptions {
	return acp.RunOptions{
		Version:    version,
		Provider:   f.provider,
		Model:      f.model,
		Mode:       f.mode,
		Thinking:   f.thinking,
		Sandbox:    f.sandbox,
		Verbose:    f.verbose,
		Debug:      f.debug,
		MultiAgent: f.multiAgent,
		Delegate:   f.delegate,
		Workflows:  f.workflows,
		WebSearch:  f.webSearch,
		Browser:    f.browser,
		Artifact:   f.artifact,

		PermissionTimeout: resolveACPTimeout(f.acpPermissionTimeout, "MOTHX_ACP_PERMISSION_TIMEOUT"),
		QuestionTimeout:   resolveACPTimeout(f.acpQuestionTimeout, "MOTHX_ACP_QUESTION_TIMEOUT"),
	}
}

// newSystemInitCommand creates the `systeminit` subcommand which generates a
// project AGENTS.md non-interactively (CLI/print mode).
func newSystemInitCommand(runFn func([]string, runOptions) error, flagProvider, flagModel *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "systeminit [guidance...]",
		Short: "Generate a project AGENTS.md for AI agents",
		Long:  "Analyze the current project and write an AGENTS.md guide. Non-interactive; in the TUI use the /systeminit command for an interactive, question-driven setup.\n\nOptional trailing text is passed as extra guidance, e.g. `vibecoding systeminit write AGENTS.md in English`.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFn(nil, runOptions{
				provider:        *flagProvider,
				model:           *flagModel,
				print:           true,
				systemInit:      true,
				systemInitExtra: strings.Join(args, " "),
			})
		},
	}
	cmd.Flags().StringVarP(flagProvider, "provider", "p", "", "Provider (openai, anthropic, or custom provider name)")
	cmd.Flags().StringVarP(flagModel, "model", "m", "", "Model ID")
	return cmd
}

type runOptions struct {
	provider        string
	model           string
	mode            string
	thinking        string
	continue_       bool
	resume          string
	session         string
	sandbox         bool
	print           bool
	json            bool
	verbose         bool
	debug           bool
	expert          string
	multiAgent      bool
	delegate        bool
	workflows       bool
	cron            bool
	webSearch       bool
	browser         bool
	artifact        bool
	enableA2AMaster bool
	systemInit      bool
	systemInitExtra string
}

type contextFilesResult struct {
	context string
	info    string
}

type providerSelection struct {
	name          string
	modelID       string
	mode          string
	thinkingLevel string
}

type sessionSetup struct {
	manager *session.Manager
	info    string
}

type runtimeSetup struct {
	agentManager  *agent.AgentManager
	runtime       *agentruntime.SessionRuntime
	cronStore     cron.CronStore
	cronScheduler *cron.Scheduler
	allow         *config.AllowConfig
	cleanup       func()
}

func run(args []string, opts runOptions) error {
	initRunEnvironment(opts)

	settings, settingsMeta, err := config.LoadSettingsWithMeta()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	applyRuntimeSettings(settings, opts)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	ruleContent := contextfiles.LoadRuleFile(cwd)
	contextFiles := loadContextFiles(cwd, settings, ruleContent)
	selection := resolveProviderSelection(settings, opts)
	args, opts, selection = applySystemInit(args, opts, selection)

	p, model, err := createProvider(settings, selection.name, selection.modelID)
	if err != nil {
		return err
	}

	sessionSetup, err := setupSession(cwd, settings, opts)
	if err != nil {
		return err
	}

	sessionID := ""
	if sessionSetup.manager != nil && sessionSetup.manager.GetHeader() != nil {
		sessionID = sessionSetup.manager.GetHeader().ID
	}
	runtime, err := setupAgentRuntime(context.Background(), p, selection.name, model, settings, opts, sessionSetup.manager, sessionID, cwd)
	if err != nil {
		return err
	}
	defer runtime.cleanup()
	sharedRuntime := runtime.runtime
	registry := sharedRuntime.Registry
	sbMgr := sharedRuntime.SandboxMgr
	sbInfo := sandbox.FormatSandboxInfo(sbMgr.GetActive())
	extraContext := sharedRuntime.ExtraContext
	ruleContent = sharedRuntime.RuleContent
	skillsMgr := sharedRuntime.SkillsMgr

	if opts.print {
		startUpdateCheck(settings, func(notice string) {
			fmt.Fprintln(os.Stderr, notice)
		})
		return runPrint(args, p, selection.name, model, selection.mode, provider.ThinkingLevel(selection.thinkingLevel), settings, registry, sessionSetup.manager, extraContext, ruleContent, opts.multiAgent, opts.delegate, opts.workflows, opts.json, runtime.agentManager, sharedRuntime)
	}

	return runInteractive(runInteractiveConfig{
		provider:         p,
		providerKey:      selection.name,
		model:            model,
		settings:         settings,
		settingsMeta:     settingsMeta,
		session:          sessionSetup.manager,
		sessionInfo:      sessionSetup.info,
		registry:         registry,
		sandboxInfo:      sbInfo,
		extraContext:     extraContext,
		ruleContent:      ruleContent,
		contextFilesInfo: contextFiles.info,
		skillsManager:    skillsMgr,
		mode:             selection.mode,
		opts:             opts,
		runtime:          runtime,
	})
}

func initRunEnvironment(opts runOptions) {
	// Set Windows console to UTF-8 so CJK IME works correctly.
	if err := initConsole(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: init console: %v\n", err)
	}

	debugEnabled = opts.debug
	if debugEnabled && opts.print {
		fmt.Fprintf(os.Stderr, "[DEBUG] Debug logging enabled\n")
	}

	config.Verbose = opts.verbose || opts.debug
	if opts.debug {
		_ = os.Setenv("VIBECODING_DEBUG", "1")
		if !opts.print {
			// Keep continuous provider debug output out of the TUI view;
			// everything still lands in debug.log.
			_ = os.Setenv(provider.DebugLogOnlyEnv, "1")
		}
		// Runs before the TUI starts, so the one-time pprof address line
		// stays in terminal scrollback without disturbing the Bubble Tea view.
		debugpprof.StartForDebug(os.Stderr)
	}
}

func applyRuntimeSettings(settings *config.Settings, opts runOptions) {
	if opts.webSearch {
		settings.WebSearch.Enabled = config.BoolPtr(true)
	}
}

func loadContextFiles(cwd string, settings *config.Settings, ruleContent string) contextFilesResult {
	cfResult := &contextfiles.LoadResult{}
	var contextStr string
	if settings.ContextFiles.Enabled {
		cfResult = contextfiles.LoadContextFiles(cwd, config.ConfigDir(), settings.ContextFiles.ExtraFiles)
		contextStr = contextfiles.BuildContextString(cfResult)
	}
	return contextFilesResult{
		context: contextStr,
		info:    formatContextFilesInfo(cfResult, cwd, ruleContent),
	}
}

func formatContextFilesInfo(result *contextfiles.LoadResult, cwd string, ruleContent string) string {
	var sb strings.Builder
	sb.WriteString("📄 Loaded context files:\n")
	for _, f := range result.GlobalFiles {
		sb.WriteString(fmt.Sprintf("  ✓ %s (global)\n", f.Name))
	}
	for _, f := range result.ParentFiles {
		sb.WriteString(fmt.Sprintf("  ✓ %s (parent: %s)\n", f.Name, filepath.Dir(f.Path)))
	}
	for _, f := range result.ProjectFiles {
		sb.WriteString(fmt.Sprintf("  ✓ %s (project)\n", f.Name))
	}
	appendRuleFileInfo(&sb, cwd, ruleContent)
	return sb.String()
}

func appendRuleFileInfo(sb *strings.Builder, cwd string, ruleContent string) {
	path := contextfiles.RuleFilePath(cwd)
	if _, err := os.Stat(path); err == nil {
		if strings.TrimSpace(ruleContent) == "" {
			sb.WriteString(fmt.Sprintf("  ⚠ %s (project rules, empty)\n", contextfiles.RuleFile))
			return
		}
		sb.WriteString(fmt.Sprintf("  ✓ %s (project rules)\n", contextfiles.RuleFile))
		return
	} else if os.IsNotExist(err) {
		sb.WriteString(fmt.Sprintf("  ! %s not found (run /rule to create default project rules)\n", contextfiles.RuleFile))
		return
	} else {
		sb.WriteString(fmt.Sprintf("  ! %s unavailable: %v\n", contextfiles.RuleFile, err))
	}
}

func resolveProviderSelection(settings *config.Settings, opts runOptions) providerSelection {
	selection := providerSelection{
		name:          opts.provider,
		modelID:       opts.model,
		mode:          opts.mode,
		thinkingLevel: opts.thinking,
	}
	if selection.name == "" {
		selection.name = settings.DefaultProvider
	}
	if selection.modelID == "" && opts.provider == "" {
		selection.modelID = settings.DefaultModel
	}
	if selection.mode == "" {
		selection.mode = settings.DefaultMode
	}
	if selection.mode == "" {
		selection.mode = "yolo"
	}
	if selection.thinkingLevel == "" {
		selection.thinkingLevel = settings.DefaultThinkingLevel
	}
	return selection
}

func applySystemInit(args []string, opts runOptions, selection providerSelection) ([]string, runOptions, providerSelection) {
	// /systeminit on the CLI is non-interactive: force print mode and yolo so
	// the agent can write AGENTS.md without prompting for approval.
	if !opts.systemInit {
		return args, opts, selection
	}
	selection.mode = "yolo"
	opts.print = true
	args = []string{systeminit.Prompt(false, opts.systemInitExtra)}
	return args, opts, selection
}

func setupSession(cwd string, settings *config.Settings, opts runOptions) (sessionSetup, error) {
	sessionDir := settings.GetSessionDir()
	switch {
	case opts.continue_:
		if opts.print {
			sess, err := session.ContinueRecent(cwd, sessionDir)
			if err != nil {
				return sessionSetup{}, fmt.Errorf("continue session: %w", err)
			}
			return sessionSetup{manager: sess, info: continuingSessionInfo(sess)}, nil
		}
		sessions, err := session.ListForDir(cwd, sessionDir)
		if err != nil {
			return sessionSetup{}, fmt.Errorf("continue session: %w", err)
		}
		if len(sessions) == 0 {
			return sessionSetup{}, nil
		}
		sess, err := session.ContinueRecent(cwd, sessionDir)
		if err != nil {
			return sessionSetup{}, fmt.Errorf("continue session: %w", err)
		}
		return sessionSetup{manager: sess, info: continuingSessionInfo(sess)}, nil
	case opts.session != "":
		sess, err := session.OpenByPathOrID(cwd, sessionDir, opts.session)
		if err != nil {
			return sessionSetup{}, fmt.Errorf("open session: %w", err)
		}
		return sessionSetup{manager: sess, info: fmt.Sprintf("📂 Opened session: %s", sess.GetHeader().ID)}, nil
	case opts.resume != "":
		sess, err := session.OpenByPathOrID(cwd, sessionDir, opts.resume)
		if err != nil {
			return sessionSetup{}, fmt.Errorf("resume session: %w", err)
		}
		return sessionSetup{manager: sess, info: fmt.Sprintf("📂 Resumed session: %s", sess.GetHeader().ID)}, nil
	default:
		if !opts.print && !opts.cron && strings.TrimSpace(opts.expert) == "" {
			return sessionSetup{}, nil
		}
		sess, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: sessionDir})
		if err != nil {
			return sessionSetup{}, fmt.Errorf("init session: %w", err)
		}
		return sessionSetup{manager: sess}, nil
	}
}

func continuingSessionInfo(sess *session.Manager) string {
	if sess.GetHeader() == nil {
		return ""
	}
	info := fmt.Sprintf("📂 Continuing session: %s", sess.GetHeader().ID)
	if messages := sess.GetMessages(); len(messages) > 0 {
		info += fmt.Sprintf(" (%d messages)", len(messages))
	}
	return info
}

func registerA2AMasterTool(registry *tools.Registry, opts runOptions) error {
	if !opts.enableA2AMaster {
		return nil
	}

	a2aListPath := a2a.ProjectAgentListConfigPath()
	if _, err := os.Stat(a2aListPath); err != nil {
		a2aListPath = a2a.AgentListConfigPath()
	}
	a2aListCfg, err := a2a.LoadAgentList(a2aListPath)
	if err != nil {
		return fmt.Errorf("load a2a-list.json: %w", err)
	}
	a2aMgr := a2a.NewA2AManager(a2aListCfg)
	registry.Register(tools.NewA2ADispatchTool(&a2aDispatcherAdapter{mgr: a2aMgr}))
	if opts.verbose {
		fmt.Fprintf(os.Stderr, "A2A master mode enabled: %d agents loaded from %s\n", len(a2aMgr.List()), a2aListPath)
	}
	return nil
}

func setupAgentRuntime(ctx context.Context, p provider.Provider, providerName string, model *provider.Model, settings *config.Settings, opts runOptions, sessionMgr *session.Manager, sessionID, workDir string) (runtimeSetup, error) {
	level := sandbox.LevelNone
	if opts.sandbox || settings.Sandbox.Enabled {
		level = sandbox.LevelStandard
		if settings.Sandbox.Level == "strict" {
			level = sandbox.LevelStrict
		}
	}
	hooks := []agentruntime.RegistryHook{func(runtime *agentruntime.SessionRuntime) error {
		if !opts.print {
			runtime.Registry.Register(tools.NewQuestionTool(runtime.Registry))
		}
		return registerA2AMasterTool(runtime.Registry, opts)
	}}
	sharedRuntime, err := (agentruntime.Builder{Settings: settings, SandboxLevel: level}).Build(ctx, agentruntime.BuildOptions{
		ID: sessionID, Source: agentruntime.SourceTUI, WorkDir: workDir, Manager: sessionMgr,
		Workflows: opts.workflows, Browser: opts.browser,
		ArtifactEnabled: opts.artifact || settings.IsArtifactEnabled(), RegistryHooks: hooks,
	})
	if err != nil {
		return runtimeSetup{}, err
	}
	if expertID := strings.TrimSpace(opts.expert); expertID != "" {
		if sessionMgr == nil {
			sharedRuntime.Close()
			return runtimeSetup{}, fmt.Errorf("expert binding requires a session")
		}
		if err := sharedRuntime.SetExpert(expertID); err != nil {
			sharedRuntime.Close()
			return runtimeSetup{}, fmt.Errorf("bind expert %q: %w", expertID, err)
		}
	}
	if err := sharedRuntime.SandboxMgr.FallbackError(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: sandbox unavailable; using direct execution: %v\n", err)
	}

	allow := config.LoadAllow()
	agentMgr, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: sharedRuntime, Provider: p, Model: model, Settings: settings,
		ProviderName: providerName, Allow: allow, MultiAgentEnabled: true,
		DelegateEnabled: opts.delegate, WorkflowsEnabled: opts.workflows,
	})
	if err != nil {
		sharedRuntime.Close()
		return runtimeSetup{}, err
	}
	runtime := runtimeSetup{
		agentManager: agentMgr,
		runtime:      sharedRuntime,
		allow:        allow,
		cleanup: func() {
			// The Runtime owns cancellation and MCP cleanup. Bound the wait so a
			// provider loop that ignores cancellation cannot block CLI teardown
			// indefinitely.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sharedRuntime.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: agent runtime shutdown: %v\n", err)
			}
		},
	}
	registry := sharedRuntime.Registry
	// Resolve the Runtime-owned capability once for this adapter setup. Registry
	// hooks are deliberately re-entrant with Runtime reads, but the snapshot
	// still keeps tool registration tied to the fully assembled session state.
	subAgentToolsEnabled := agentruntime.SubAgentToolsEnabled(sharedRuntime, opts.multiAgent)
	if err := sharedRuntime.ApplyRegistryHooks([]agentruntime.RegistryHook{func(runtime *agentruntime.SessionRuntime) error {
		if subAgentToolsEnabled {
			agent.RegisterSubAgentTools(runtime.Registry, agentMgr)
		}
		if opts.delegate {
			agent.RegisterDelegateSubAgentTool(runtime.Registry, agentMgr)
		}
		if opts.workflows {
			workflow.RegisterTools(runtime.Registry, agentMgr, nil)
		}
		return nil
	}}); err != nil {
		sharedRuntime.Close()
		return runtimeSetup{}, err
	}
	if opts.cron {
		globalStore := cron.NewSQLiteCronStore(settings.GetSessionDir())
		runtime.cronStore = cron.NewSessionScopedStoreWithWorkDir(globalStore, sessionID, workDir)
		runtime.cronScheduler = cron.NewSchedulerWithSessionDir(globalStore, agentMgr, 30*time.Second, settings.GetSessionDir())
		runtime.cronScheduler.Start()
		registry.Register(cron.NewCronTool(runtime.cronStore, runtime.cronScheduler))
		closeRuntime := runtime.cleanup
		runtime.cleanup = func() {
			runtime.cronScheduler.Stop()
			closeRuntime()
		}
	}
	logRuntimeModes(opts)
	return runtime, nil
}

func logRuntimeModes(opts runOptions) {
	if !opts.verbose {
		return
	}
	if opts.multiAgent {
		fmt.Fprintf(os.Stderr, "Multi-agent mode enabled\n")
	}
	if opts.delegate {
		fmt.Fprintf(os.Stderr, "Delegate mode enabled\n")
	}
	if opts.workflows {
		fmt.Fprintf(os.Stderr, "Workflow mode enabled\n")
	}
	if opts.cron {
		fmt.Fprintf(os.Stderr, "Cron mode enabled\n")
	}
}

type runInteractiveConfig struct {
	provider         provider.Provider
	providerKey      string // user-configured settings.json key (e.g. "xiaomi")
	model            *provider.Model
	settings         *config.Settings
	settingsMeta     config.LoadMeta
	session          *session.Manager
	sessionInfo      string
	registry         *tools.Registry
	sandboxInfo      string
	extraContext     string
	ruleContent      string
	contextFilesInfo string
	skillsManager    *skills.Manager
	mode             string
	opts             runOptions
	runtime          runtimeSetup
}

func runInteractive(cfg runInteractiveConfig) error {
	// Clear any pending stdin input (e.g., terminal color queries).
	clearStdin()

	app := tui.NewAppWithWorkflowsAndAllow(
		cfg.provider,
		cfg.model,
		cfg.settings,
		cfg.session,
		cfg.registry,
		cfg.sandboxInfo,
		cfg.extraContext,
		cfg.ruleContent,
		cfg.skillsManager,
		cfg.mode,
		cfg.opts.multiAgent,
		cfg.opts.delegate,
		cfg.opts.workflows,
		cfg.runtime.agentManager,
		cfg.runtime.cronStore,
		cfg.runtime.cronScheduler,
		cfg.providerKey,
		cfg.runtime.allow,
	)
	app.SetRuntime(cfg.runtime.runtime)
	if initialMsg := buildInitialMessage(cfg); initialMsg != "" {
		app.SetInitialMessage(initialMsg)
	}
	if cfg.settingsMeta.CreatedGlobalConfig {
		app.SetAutoOpenAuthDialog(true)
	}
	app.SetBrowserEnabled(cfg.opts.browser, cfg.opts.browser)
	p2 := tea.NewProgram(app, teaProgramOptions()...)
	app.SetProgram(p2)
	startUpdateCheck(cfg.settings, app.ShowUpdateNotice)
	if _, err := p2.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	if app.ReloadRequested() {
		cfg.runtime.cleanup()
		if err := session.CloseDatabases(); err != nil {
			return fmt.Errorf("close session databases before reload: %w", err)
		}
		return reexecFresh()
	}
	return nil
}

func buildInitialMessage(cfg runInteractiveConfig) string {
	parts := []string{}
	if cfg.contextFilesInfo != "" {
		parts = append(parts, cfg.contextFilesInfo)
	}
	if cfg.sessionInfo != "" {
		parts = append(parts, cfg.sessionInfo)
	}
	if cfg.settingsMeta.CreatedGlobalConfig {
		parts = append(parts, fmt.Sprintf("Created default config: %s\nOpening /auth to configure or confirm your provider token and model.", cfg.settingsMeta.GlobalSettingsPath))
	}
	return strings.Join(parts, "\n")
}

// reexecFresh restarts the program as a fresh process with a brand-new session
// (session-continuation flags are stripped). Used by the /reload command.
func reexecFresh() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("reload: locate executable: %w", err)
	}
	args := filterReloadArgs(os.Args[1:])
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("reload: %w", err)
	}
	os.Exit(0)
	return nil
}

// filterReloadArgs removes session-continuation flags so a reload starts fresh.
func filterReloadArgs(args []string) []string {
	out := make([]string, 0, len(args))
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch arg {
		case "-c", "--continue":
			continue
		case "-r", "--resume", "--session":
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--resume=") || strings.HasPrefix(arg, "--session=") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func startUpdateCheck(settings *config.Settings, notify func(string)) {
	if !settings.IsUpdateCheckEnabled() {
		return
	}
	update.CheckInBackground(version, notify)
}

// a2aDispatcherAdapter adapts a2a.A2AManager to tools.A2ADispatcher.
type a2aDispatcherAdapter struct {
	mgr *a2a.A2AManager
}

func (a *a2aDispatcherAdapter) List() []tools.AgentEntry {
	entries := a.mgr.List()
	result := make([]tools.AgentEntry, len(entries))
	for i, e := range entries {
		result[i] = tools.AgentEntry{Name: e.Name, URL: e.URL}
	}
	return result
}

func (a *a2aDispatcherAdapter) Dispatch(ctx context.Context, name, message string) (string, error) {
	return a.mgr.Dispatch(ctx, name, message)
}
