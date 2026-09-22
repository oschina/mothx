package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/util"
)

// limitedBuffer wraps bytes.Buffer with a max size limit.
type limitedBuffer struct {
	buf     bytes.Buffer
	maxSize int
	dropped int
}

func newLimitedBuffer(maxSize int) *limitedBuffer {
	return &limitedBuffer{maxSize: maxSize}
}

func (lb *limitedBuffer) Write(p []byte) (n int, err error) {
	if lb.buf.Len()+len(p) > lb.maxSize {
		keep := lb.maxSize - lb.buf.Len()
		if keep > 0 {
			lb.buf.Write(p[:keep])
		}
		lb.dropped += len(p) - keep
		return len(p), nil
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) Bytes() []byte {
	if lb.dropped > 0 {
		trail := fmt.Sprintf("\n... (truncated %d bytes)", lb.dropped)
		lb.buf.WriteString(trail)
		lb.dropped = 0
	}
	return lb.buf.Bytes()
}

// BashTool executes shell commands.
type BashTool struct {
	registry   *Registry
	jobManager *JobManager
}

// NewBashTool creates a new bash tool with a new JobManager.
func NewBashTool(r *Registry) *BashTool {
	return &BashTool{
		registry:   r,
		jobManager: NewJobManager(),
	}
}

// NewBashToolWithJM creates a new bash tool with an existing JobManager.
func NewBashToolWithJM(r *Registry, jm *JobManager) *BashTool {
	return &BashTool{
		registry:   r,
		jobManager: jm,
	}
}

// GetJobManager returns the job manager for background processes.
func (t *BashTool) GetJobManager() *JobManager {
	return t.jobManager
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	if platform.IsWindows() {
		return "Execute a shell command (BusyBox first, PowerShell fallback). Use this for short commands, validation, and builds. The command runs in the current working directory. Sync runs default to 45s, max 600s. For long-running services like servers and watchers, use async=true."
	}
	return "Execute a bash command. Use this for short commands, validation, and builds. The command runs in the current working directory. Sync runs default to 45s, max 600s. For long-running services like servers and watchers, use async=true."
}

func (t *BashTool) PromptSnippet() string {
	return "Execute shell commands when dedicated tools are insufficient"
}

func (t *BashTool) PromptGuidelines() []string {
	guidelines := []string{
		"Prefer read/ls/grep/find tools over bash for file inspection and exploration",
		"Use bash for short commands, validation, and builds; use async=true for long-running services like servers, watchers, and dev servers",
		"For network probes and commands that may hang, set timeout explicitly",
		"Examples that often need explicit timeout: curl, wget, npm install, go test, docker logs",
	}
	if platform.IsWindows() {
		guidelines = append(guidelines, "On Windows, bash uses embedded BusyBox first and falls back to PowerShell if BusyBox is unavailable")
	}
	return guidelines
}

func (t *BashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (default 45, max 600). Set to 0 for no tool-level deadline."
			},
			"async": {
				"type": "boolean",
				"description": "Run command in background (for long-running services like servers). Returns immediately with a job ID. Use 'jobs' tool to check status."
			}
		},
		"required": ["command"]
	}`)
}

func (t *BashTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return ToolResult{}, fmt.Errorf("command is required")
	}

	// Check for async mode
	async, _ := params["async"].(bool)

	// Auto-detect async if command ends with &
	command = strings.TrimSpace(command)
	if strings.HasSuffix(command, "&") && !async {
		async = true
		command = strings.TrimSpace(strings.TrimSuffix(command, "&"))
	}

	timeout := t.defaultTimeout(params)

	// For async commands, use a background context (no timeout unless specified)
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if async {
		cmdCtx, cancel = context.WithCancel(context.Background())
	} else {
		if timeout > 0 {
			cmdCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		} else {
			cmdCtx, cancel = ctx, func() {}
		}
	}

	// Get platform-specific shell
	shell := platform.DefaultShell()
	if !platform.IsWindows() {
		if s := os.Getenv("SHELL"); s != "" {
			if isValidShell(s) {
				shell = s
			}
		}
	}

	workDir := t.registry.GetWorkDir()

	env := os.Environ()
	for key, value := range t.executionEnvVars() {
		env = appendEnvValue(env, key, value)
	}

	sb := t.registry.GetSandbox()
	if platform.IsWindows() {
		shells := t.windowsShellCandidates()
		for i, candidate := range shells {
			cmd, runtimeLabel := t.buildWindowsCommand(cmdCtx, sb, candidate, command, workDir, env, timeout)
			result, err, launchErr := t.runCommand(cmd, command, workDir, async, cancel, runtimeLabel, nil)
			if launchErr && i < len(shells)-1 {
				continue
			}
			return result, err
		}
		return ToolResult{}, fmt.Errorf("no shell candidates available")
	}

	var cmd *exec.Cmd
	var cleanup func()
	if sb != nil && sb.IsAvailable() {
		opts := sandbox.ExecOpts{WorkDir: workDir, Timeout: timeout, EnvVars: t.executionEnvVars()}
		additionalDirs := t.registry.GetAdditionalDirectories()
		if sb.Level() == sandbox.LevelStrict {
			opts.ReadOnlyPaths = additionalDirs
		} else {
			opts.WritablePaths = additionalDirs
		}
		if sandbox.GitAccessFromContext(ctx) {
			gitSB, supported := sb.(sandbox.GitAccessSandbox)
			if !supported {
				return ToolResult{}, fmt.Errorf("sandbox backend cannot grant one-shot Git access")
			}
			cmd = gitSB.WrapCommandWithGitAccess(cmdCtx, shell, command, opts)
		} else {
			cmd = sb.WrapCommand(cmdCtx, shell, command, opts)
		}
		t.configureCommand(cmd)
		if cleaner, ok := sb.(sandbox.CommandCleanupProvider); ok {
			cleanup = func() { cleaner.CleanupCommand(cmd) }
		}
	} else {
		cmd = t.buildCommand(cmdCtx, shell, command, workDir, env)
	}
	result, err, _ := t.runCommand(cmd, command, workDir, async, cancel, runtimeForShell(shell), cleanup)
	return result, err
}

func (t *BashTool) executionEnvVars() map[string]string {
	vars := t.registry.EnvVars()
	for key, value := range config.LoadEnv().List() {
		vars[key] = value
	}
	return vars
}

func appendEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func (t *BashTool) buildCommand(ctx context.Context, shell, command, workDir string, env []string) *exec.Cmd {
	args := platform.ShellArgs(shell, command)
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = workDir
	cmd.Env = env
	t.configureCommand(cmd)
	return cmd
}

func (t *BashTool) configureCommand(cmd *exec.Cmd) {
	// Bash tools are non-interactive. Prevent subprocesses from stealing the
	// TUI terminal for auth prompts, and make cancellation kill descendants.
	setSysProcAttr(cmd)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = nonInteractiveEnv(cmd.Env)
	cmd.Cancel = func() error {
		return killCommandProcess(cmd)
	}
	// If the shell exits while a background child still holds stdio,
	// don't wait forever; give it 100ms then force-close copied pipes.
	cmd.WaitDelay = 100 * time.Millisecond
}

func (t *BashTool) windowsShellCandidates() []string {
	if busyboxPath, ok := platform.WindowsBusyboxPath(); ok {
		return []string{busyboxPath, "powershell.exe"}
	}
	return []string{"powershell.exe"}
}

func (t *BashTool) buildWindowsCommand(ctx context.Context, sb sandbox.Sandbox, shell, command, workDir string, env []string, timeout time.Duration) (*exec.Cmd, string) {
	if sb != nil && sb.IsAvailable() {
		opts := sandbox.ExecOpts{
			WorkDir: workDir,
			Timeout: timeout,
			EnvVars: t.executionEnvVars(),
		}
		additionalDirs := t.registry.GetAdditionalDirectories()
		if sb.Level() == sandbox.LevelStrict {
			opts.ReadOnlyPaths = additionalDirs
		} else {
			opts.WritablePaths = additionalDirs
		}
		if shell != "powershell.exe" {
			if busyboxPath, ok := platform.WindowsBusyboxPath(); ok {
				opts.EnvVars["PATH"] = prefixPathValue(os.Getenv("PATH"), filepath.Dir(busyboxPath))
			}
		}
		return sb.WrapCommand(ctx, shell, command, opts), runtimeForShell(shell)
	}

	if shell != "powershell.exe" {
		cmd := exec.CommandContext(ctx, shell, "sh", "-c", command)
		cmd.Dir = workDir
		cmd.Env = env
		t.configureCommand(cmd)
		return cmd, runtimeForShell(shell)
	}

	cmd := exec.CommandContext(ctx, shell, platform.ShellArgs(shell, command)...)
	cmd.Dir = workDir
	cmd.Env = env
	t.configureCommand(cmd)
	return cmd, runtimeForShell(shell)
}

func (t *BashTool) runCommand(cmd *exec.Cmd, command, workDir string, async bool, cancel context.CancelFunc, runtimeLabel string, cleanup func()) (ToolResult, error, bool) {
	if async {
		const maxJobOutput = 1000000 // 1 MB limit per stream
		stdout := newLimitedBuffer(maxJobOutput)
		stderr := newLimitedBuffer(maxJobOutput)
		cmd.Stdout = stdout
		cmd.Stderr = stderr

		if err := cmd.Start(); err != nil {
			if cleanup != nil {
				cleanup()
			}
			if cancel != nil {
				cancel()
			}
			return ToolResult{}, err, isLaunchError(err)
		}

		job := t.jobManager.AddJob(cmd, command, cancel)
		go func() {
			defer func() {
				if cleanup != nil {
					cleanup()
				}
			}()
			err := cmd.Wait()
			if errors.Is(err, exec.ErrWaitDelay) {
				err = nil
			}
			job.MarkDone(stdout.Bytes(), stderr.Bytes(), err)
		}()
		return NewTextToolResult(fmt.Sprintf("[runtime]\n%s\n[command]\n%s\nUse 'jobs' tool to check status or 'kill' to stop.", runtimeLabel, command)), nil, false
	}

	const maxSyncOutput = 1 << 20 // 1 MB per stream
	stdout := newLimitedBuffer(maxSyncOutput)
	stderr := newLimitedBuffer(maxSyncOutput)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if cleanup != nil {
		cleanup()
	}
	if err != nil && isLaunchError(err) {
		return ToolResult{}, err, true
	}

	stdoutStr := strings.TrimRight(string(stdout.Bytes()), "\n")
	stderrStr := string(stderr.Bytes())
	stderrStr = strings.TrimRight(stderrStr, "\n")
	if stdoutStr == "" {
		stdoutStr = "(no output)"
	}
	if stderrStr == "" {
		stderrStr = "(no output)"
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	var result strings.Builder
	result.WriteString("[runtime]\n")
	result.WriteString(runtimeLabel)
	result.WriteString("\n")
	result.WriteString("[command]\n")
	result.WriteString(command)
	result.WriteString("\n[cwd]\n")
	result.WriteString(workDir)
	result.WriteString("\n[stdout]\n")
	result.WriteString(stdoutStr)
	result.WriteString("\n[stderr]\n")
	result.WriteString(stderrStr)
	result.WriteString("\n[exit_code]\n")
	result.WriteString(fmt.Sprintf("%d", exitCode))

	const maxOutput = 50000
	resultStr := result.String()
	if len(resultStr) > maxOutput {
		prefix := util.TruncateString(resultStr, maxOutput)
		truncated := len(resultStr) - len(prefix)
		resultStr = prefix + fmt.Sprintf("\n... (truncated %d bytes)", truncated)
	}

	if err != nil {
		if errors.Is(err, exec.ErrWaitDelay) {
			return NewTextToolResult(resultStr), nil, false
		}
		if _, ok := err.(*exec.ExitError); ok {
			return NewTextToolResult(resultStr), nil, false
		}
		return ToolResult{}, fmt.Errorf("command failed: %w\n%s", err, resultStr), false
	}

	return NewTextToolResult(resultStr), nil, false
}

func (t *BashTool) defaultTimeout(params map[string]any) time.Duration {
	if async, _ := params["async"].(bool); async {
		if v, ok := timeoutSecondsParam(params); ok {
			return clampTimeout(v)
		}
		return 0
	}
	if v, ok := timeoutSecondsParam(params); ok {
		return clampTimeout(v)
	}
	return 45 * time.Second
}

func timeoutSecondsParam(params map[string]any) (float64, bool) {
	v, ok := params["timeout"]
	if !ok {
		return 0, false
	}
	seconds, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return seconds, true
}

func clampTimeout(seconds float64) time.Duration {
	if seconds < 0 {
		return 45 * time.Second
	}
	if seconds > 600 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}

// ExecutionTimeout lets bash align the agent-level tool deadline with the
// runtime behavior exposed by Execute.
func (t *BashTool) ExecutionTimeout(params map[string]any) (time.Duration, bool) {
	timeout := t.defaultTimeout(params)
	return timeout, true
}

func prefixPathValue(pathValue, dir string) string {
	if dir == "" {
		return pathValue
	}
	if pathValue == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + pathValue
}

func nonInteractiveEnv(env []string) []string {
	env = setEnvDefault(env, "GIT_TERMINAL_PROMPT", "0")
	env = setEnvDefault(env, "GIT_ASKPASS", "true")
	env = setEnvDefault(env, "SSH_ASKPASS", "true")
	env = setEnvDefault(env, "SSH_ASKPASS_REQUIRE", "never")
	env = setEnvDefault(env, "SUDO_ASKPASS", "true")
	return env
}

func setEnvDefault(env []string, key, value string) []string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return env
		}
	}
	return append(env, prefix+value)
}

func runtimeForShell(shell string) string {
	switch {
	case strings.Contains(strings.ToLower(shell), "busybox"):
		return "busybox"
	case strings.Contains(strings.ToLower(shell), "powershell"):
		return "powershell"
	case strings.Contains(strings.ToLower(shell), "cmd"):
		return "cmd"
	default:
		return filepath.Base(shell)
	}
}

func isLaunchError(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr)
}

// SetTool is an interface for tools that need sandbox updates.
type SetTool interface {
	SetSandbox(sb sandbox.Sandbox)
}

// validShellNames is the allowlist of known shell binaries.
var validShellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
}

// isValidShell checks whether the given path is a known shell binary.
func isValidShell(path string) bool {
	name := filepath.Base(path)
	if !validShellNames[name] {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
