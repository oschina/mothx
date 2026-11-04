package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

const (
	defaultACPTerminalOutputBytes = 1 << 20
	maxACPTerminalOutputBytes     = 4 << 20
	maxACPTerminalEnvEntries      = 64
	maxACPTerminalEnvBytes        = 64 << 10
	maxACPTerminalArgs            = 256
	maxACPTerminalArgBytes        = 64 << 10
	maxACPTerminals               = 16
)

// acpTerminalManager owns the client-side terminal handles for one external
// child turn. It is deliberately an execution projection, not a second shell
// tool: commands use the shared workspace resolution and sandbox, and the
// profile must explicitly opt in with terminal access.
type acpTerminalManager struct {
	registry *tools.Registry
	sandbox  sandbox.Sandbox
	workDir  string
	baseEnv  []string

	mu        sync.Mutex
	next      int
	terminals map[string]*acpTerminal
	closed    bool
}

type acpTerminal struct {
	id     string
	cmd    *exec.Cmd
	cancel context.CancelFunc
	output *acpTerminalBuffer
	done   chan struct{}

	mu       sync.Mutex
	exitCode *int
	waitErr  error
}

type acpTerminalBuffer struct {
	mu        sync.Mutex
	limit     int
	data      []byte
	truncated bool
}

func newACPTerminalBuffer(limit int) *acpTerminalBuffer {
	if limit <= 0 {
		limit = defaultACPTerminalOutputBytes
	}
	if limit > maxACPTerminalOutputBytes {
		limit = maxACPTerminalOutputBytes
	}
	return &acpTerminalBuffer{limit: limit}
}

func (b *acpTerminalBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		b.truncated = true
		return len(p), nil
	}
	if overflow := len(b.data) + len(p) - b.limit; overflow > 0 {
		b.data = append(b.data[:0], b.data[overflow:]...)
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *acpTerminalBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return boundedACPUTF8Tail(b.data, b.limit), b.truncated
}

// boundedACPUTF8Tail applies ACP's terminal-output rule: retention is bounded
// by bytes, truncation starts at the beginning, and the retained string is
// always valid UTF-8 even when the first boundary split a rune.
func boundedACPUTF8Tail(data []byte, limit int) string {
	if limit <= 0 {
		return ""
	}
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

func newACPTerminalManager(registry *tools.Registry, workDir string, baseEnv []string) *acpTerminalManager {
	if registry == nil {
		return nil
	}
	return &acpTerminalManager{
		registry:  registry,
		sandbox:   registry.GetSandbox(),
		workDir:   workDir,
		baseEnv:   append([]string(nil), baseEnv...),
		terminals: make(map[string]*acpTerminal),
	}
}

func isACPTerminalMethod(method string) bool {
	switch method {
	case "terminal/create", "terminal/output", "terminal/wait_for_exit", "terminal/kill", "terminal/release":
		return true
	default:
		return false
	}
}

func (m *acpTerminalManager) Handle(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
	if m == nil {
		return nil, &acpclient.RPCError{Code: -32601, Message: "terminal access is not available"}
	}
	if message.Method == "terminal/create" {
		return m.create(ctx, message)
	}
	request, ok := acpclient.DecodeTerminalRefRequest(message)
	if !ok {
		return nil, &acpclient.RPCError{Code: -32602, Message: "invalid terminal request"}
	}
	terminal := m.get(request.TerminalID)
	if terminal == nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: "terminal not found"}
	}
	switch message.Method {
	case "terminal/output":
		output, truncated := terminal.output.snapshot()
		return acpclient.TerminalOutputResult{
			Output: output, Truncated: truncated, ExitStatus: terminal.status(),
		}, nil
	case "terminal/wait_for_exit":
		select {
		case <-terminal.done:
			return terminal.statusOrEmpty(), nil
		case <-ctx.Done():
			return nil, &acpclient.RPCError{Code: -32001, Message: "terminal wait canceled"}
		}
	case "terminal/kill":
		terminal.kill()
		return map[string]any{}, nil
	case "terminal/release":
		m.remove(request.TerminalID)
		terminal.kill()
		return map[string]any{}, nil
	}
	return nil, &acpclient.RPCError{Code: -32601, Message: "terminal method not found"}
}

func (m *acpTerminalManager) create(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
	request, ok := acpclient.DecodeTerminalCreateRequest(message)
	if !ok {
		return nil, &acpclient.RPCError{Code: -32602, Message: "invalid terminal/create request"}
	}
	if len(request.Args) > maxACPTerminalArgs {
		return nil, &acpclient.RPCError{Code: -32602, Message: "terminal/create has too many arguments"}
	}
	argBytes := len(request.Command)
	for _, arg := range request.Args {
		argBytes += len(arg)
	}
	if argBytes > maxACPTerminalArgBytes {
		return nil, &acpclient.RPCError{Code: -32602, Message: "terminal/create command is too large"}
	}
	env, envErr := m.terminalEnv(request.Env)
	if envErr != nil {
		return nil, &acpclient.RPCError{Code: -32602, Message: envErr.Error()}
	}
	cwd, err := m.terminalCwd(request.Cwd)
	if err != nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: err.Error()}
	}
	shell := platform.DefaultShell()
	commandLine := quoteACPArgs(shell, append([]string{request.Command}, request.Args...))
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := m.command(runCtx, shell, commandLine, cwd, env)
	if cmd == nil {
		cancel()
		return nil, &acpclient.RPCError{Code: -32001, Message: "terminal command could not be constructed"}
	}
	outputLimit := request.OutputByteLimit
	if outputLimit <= 0 || outputLimit > maxACPTerminalOutputBytes {
		outputLimit = defaultACPTerminalOutputBytes
	}
	output := newACPTerminalBuffer(int(outputLimit))
	cmd.Stdout = output
	cmd.Stderr = output
	terminal := &acpTerminal{
		cmd: cmd, cancel: cancel, output: output, done: make(chan struct{}),
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return nil, &acpclient.RPCError{Code: -32001, Message: "terminal manager is closed"}
	}
	if len(m.terminals) >= maxACPTerminals {
		m.mu.Unlock()
		cancel()
		return nil, &acpclient.RPCError{Code: -32001, Message: "too many live terminals"}
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		cancel()
		return nil, &acpclient.RPCError{Code: -32001, Message: fmt.Sprintf("start terminal command: %v", err)}
	}
	m.next++
	terminal.id = "mothx-terminal-" + strconv.Itoa(m.next)
	m.terminals[terminal.id] = terminal
	m.mu.Unlock()

	go terminal.wait()
	return acpclient.TerminalCreateResult{TerminalID: terminal.id}, nil
}

func (m *acpTerminalManager) terminalCwd(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return m.workDir, nil
	}
	if !filepath.IsAbs(filepath.FromSlash(raw)) {
		return "", fmt.Errorf("terminal/create cwd must be absolute")
	}
	cwd, err := m.registry.ResolvePath(filepath.FromSlash(raw))
	if err != nil {
		return "", fmt.Errorf("terminal/create cwd is outside the session workspace roots")
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("terminal/create cwd is not a directory")
	}
	return cwd, nil
}

func (m *acpTerminalManager) terminalEnv(extra []acpclient.TerminalEnvVar) ([]string, error) {
	if len(extra) > maxACPTerminalEnvEntries {
		return nil, fmt.Errorf("terminal/create has too many environment entries")
	}
	env := append([]string(nil), m.baseEnv...)
	total := 0
	for _, item := range env {
		total += len(item)
	}
	for _, item := range extra {
		if !validACPEventName(item.Name) {
			return nil, fmt.Errorf("terminal/create has an invalid environment name")
		}
		if strings.IndexByte(item.Value, 0) >= 0 {
			return nil, fmt.Errorf("terminal/create environment value contains NUL")
		}
		entry := item.Name + "=" + item.Value
		total += len(entry)
		if total > maxACPTerminalEnvBytes {
			return nil, fmt.Errorf("terminal/create environment is too large")
		}
		env = setACPEnvValue(env, item.Name, item.Value)
	}
	return env, nil
}

func validACPEventName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func setACPEnvValue(env []string, name, value string) []string {
	prefix := name + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func (m *acpTerminalManager) command(ctx context.Context, shell, commandLine, cwd string, env []string) *exec.Cmd {
	var cmd *exec.Cmd
	if m.sandbox != nil && m.sandbox.IsAvailable() && m.sandbox.Level() != sandbox.LevelNone {
		opts := sandbox.ExecOpts{WorkDir: cwd, EnvVars: acpEnvMap(env)}
		additionalDirs := m.registry.GetAdditionalDirectories()
		if m.sandbox.Level() == sandbox.LevelStrict {
			opts.ReadOnlyPaths = additionalDirs
		} else {
			opts.WritablePaths = additionalDirs
		}
		cmd = m.sandbox.WrapCommand(ctx, shell, commandLine, opts)
	} else {
		cmd = exec.CommandContext(ctx, shell, platform.ShellArgs(shell, commandLine)...)
		cmd.Dir = cwd
		// NoneSandbox deliberately inherits the full MothX environment. The
		// terminal bridge must honor the external worker's restricted profile
		// environment instead, so the no-sandbox path sets it explicitly.
		cmd.Env = env
	}
	tools.ConfigureCommandProcess(cmd)
	return cmd
}

func acpEnvMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	return values
}

func quoteACPArgs(shell string, argv []string) string {
	name := strings.ToLower(filepath.Base(shell))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	quote := quotePOSIXArg
	switch {
	case strings.HasPrefix(name, "powershell") || name == "pwsh":
		quote = quotePowerShellArg
	case name == "cmd":
		quote = quoteCmdArg
	}
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, quote(arg))
	}
	return strings.Join(quoted, " ")
}

func quotePOSIXArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func quotePowerShellArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}

func quoteCmdArg(arg string) string {
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}

func (m *acpTerminalManager) get(id string) *acpTerminal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminals[id]
}

func (m *acpTerminalManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.terminals, id)
}

// Close terminates every live terminal and stops accepting new handles. It is
// idempotent and is called at the end of the child turn.
func (m *acpTerminalManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	terminals := make([]*acpTerminal, 0, len(m.terminals))
	for _, terminal := range m.terminals {
		terminals = append(terminals, terminal)
	}
	m.terminals = nil
	m.mu.Unlock()
	for _, terminal := range terminals {
		terminal.kill()
	}
}

func (t *acpTerminal) wait() {
	err := t.cmd.Wait()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	code := -1
	if t.cmd.ProcessState != nil {
		code = t.cmd.ProcessState.ExitCode()
	}
	t.mu.Lock()
	t.exitCode = &code
	t.waitErr = err
	t.mu.Unlock()
	close(t.done)
}

func (t *acpTerminal) status() *acpclient.TerminalExitStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exitCode == nil {
		return nil
	}
	code := *t.exitCode
	return &acpclient.TerminalExitStatus{ExitCode: &code}
}

func (t *acpTerminal) statusOrEmpty() acpclient.TerminalExitStatus {
	if status := t.status(); status != nil {
		return *status
	}
	return acpclient.TerminalExitStatus{}
}

func (t *acpTerminal) kill() {
	if t == nil {
		return
	}
	t.mu.Lock()
	finished := t.exitCode != nil
	t.mu.Unlock()
	if finished {
		return
	}
	if t.cancel != nil {
		t.cancel()
	}
	select {
	case <-t.done:
	case <-time.After(2 * time.Second):
		_ = tools.KillCommandProcess(t.cmd)
		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
		}
	}
}
