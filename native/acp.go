package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "json-rpc error"
	}
	return fmt.Sprintf("%s (%d)", e.Message, e.Code)
}

// incoming is one line of the ACP stdio channel: a response, a notification,
// or a request the agent sends to its client.
type incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m incoming) hasID() bool {
	return len(m.ID) > 0 && !bytes.Equal(bytes.TrimSpace(m.ID), []byte("null"))
}

type pendingCall struct {
	ch chan callResult
}

type callResult struct {
	result json.RawMessage
	err    error
}

// acpClient owns one `mothx acp` child process and speaks ACP v1 over its
// stdio in newline-delimited JSON.
type acpClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]pendingCall
	nextID  int64
	closed  bool

	handler  func(incoming)
	onStderr func(string)
	onExit   func(error)

	stderrMu sync.Mutex
	stderr   []string
}

func newACPClient(handler func(incoming), onStderr func(string), onExit func(error)) *acpClient {
	return &acpClient{
		pending:  make(map[string]pendingCall),
		handler:  handler,
		onStderr: onStderr,
		onExit:   onExit,
	}
}

// start launches the ACP process. The caller must have resolved binary to an
// executable path.
func (c *acpClient) start(binary, dir string, env []string) error {
	cmd := exec.Command(binary, "acp")
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin

	go c.readLoop(stdout)
	go c.readStderr(stderr)
	go func() {
		waitErr := cmd.Wait()
		var err error
		if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
			err = waitErr
		}
		cause := err
		if cause == nil {
			cause = errors.New("process exited")
		}
		c.mu.Lock()
		alreadyClosed := c.closed
		c.closed = true
		for key, p := range c.pending {
			p.ch <- callResult{err: fmt.Errorf("ACP process exited: %w", cause)}
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if !alreadyClosed && c.onExit != nil {
			c.onExit(err)
		}
	}()
	return nil
}

func (c *acpClient) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg incoming
		if err := json.Unmarshal(line, &msg); err != nil {
			c.addStderr("non-JSON ACP stdout: " + string(line[:min(len(line), 400)]))
			continue
		}
		if msg.Method != "" {
			if c.handler != nil {
				c.handler(msg)
			}
			continue
		}
		key := idKey(msg.ID)
		c.mu.Lock()
		p, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.mu.Unlock()
		if !ok {
			continue
		}
		if msg.Error != nil {
			p.ch <- callResult{err: msg.Error}
		} else {
			p.ch <- callResult{result: msg.Result}
		}
	}
	if err := scanner.Err(); err != nil {
		c.addStderr("ACP stdout reader: " + err.Error())
	}
}

func (c *acpClient) readStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		c.addStderr(line)
		if c.onStderr != nil {
			c.onStderr(line)
		}
	}
}

func (c *acpClient) addStderr(line string) {
	c.stderrMu.Lock()
	c.stderr = append(c.stderr, line)
	if len(c.stderr) > 80 {
		c.stderr = c.stderr[len(c.stderr)-80:]
	}
	c.stderrMu.Unlock()
}

// stderrTail returns recent diagnostics for the connection error card.
func (c *acpClient) stderrTail(n int) []string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if n <= 0 || n > len(c.stderr) {
		n = len(c.stderr)
	}
	out := make([]string, n)
	copy(out, c.stderr[len(c.stderr)-n:])
	return out
}

// call sends a request and waits for its response. It may be called from any
// goroutine; the UI must run it off the main thread.
func (c *acpClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("ACP runtime is not connected")
	}
	c.nextID++
	id := c.nextID
	key := "n:" + strconv.FormatInt(id, 10)
	p := pendingCall{ch: make(chan callResult, 1)}
	c.pending[key] = p
	c.mu.Unlock()

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeJSON(req); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}
	res := <-p.ch
	return res.result, res.err
}

// notify sends a notification, which has no response.
func (c *acpClient) notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSON(msg)
}

// respond answers a request the agent sent to this client.
func (c *acpClient) respond(id json.RawMessage, result any) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
	return c.writeJSON(msg)
}

// respondError answers a reverse request with a JSON-RPC error.
func (c *acpClient) respondError(id json.RawMessage, code int, message string) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}}
	return c.writeJSON(msg)
}

func (c *acpClient) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return errors.New("ACP runtime is not connected")
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// close ends the child's stdin so it can flush sessions and exit. A delayed
// kill makes sure a wedged process cannot outlive the app.
func (c *acpClient) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = make(map[string]pendingCall)
	c.mu.Unlock()
	for _, p := range pending {
		p.ch <- callResult{err: errors.New("ACP runtime closed")}
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	cmd := c.cmd
	if cmd != nil && cmd.Process != nil {
		go func(pid int) {
			time.Sleep(2 * time.Second)
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}(cmd.Process.Pid)
	}
}

func idKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return "s:" + t
	case float64:
		return "n:" + strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// launchBinaryOverride is the executable named by the Native shell's own
// launch arguments (--mothx-bin). It is presentation-neutral launch state: ACP
// never sees it, no settings file stores it, and it is only resolved once at
// startup. It intentionally outranks MOTHX_BIN because an explicit shell
// argument is more specific than the inherited environment.
var launchBinaryOverride string

// runtimeBinaryPreference is the shell's persisted binary choice. Like the
// launch flag it is client launch state: ACP and settings.json never own it.
var runtimeBinaryPreference string

func getRuntimeBinaryPreference() string {
	runtimeBinaryPersistMu.RLock()
	defer runtimeBinaryPersistMu.RUnlock()
	return runtimeBinaryPreference
}

func setRuntimeBinaryPreference(path string, persist bool) {
	runtimeBinaryPersistMu.Lock()
	runtimeBinaryPreference = strings.TrimSpace(path)
	runtimeBinaryPersistMu.Unlock()
	if persist {
		if app := activeNativeApp; app != nil {
			app.saveNativePreferences()
		}
	}
}

var activeNativeApp *nativeApp

// setLaunchBinaryOverride records the shell's binary choice. It is called once
// from main before any ACP connection is attempted.
func setLaunchBinaryOverride(path string) {
	launchBinaryOverride = strings.TrimSpace(path)
}

// parseLaunchBinaryArg extracts --mothx-bin from the shell arguments. It
// accepts both "--mothx-bin PATH" and "--mothx-bin=PATH" and never consumes
// another flag as a value.
func parseLaunchBinaryArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--mothx-bin=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--mothx-bin="))
			return value, value != ""
		}
		if arg != "--mothx-bin" {
			continue
		}
		if i+1 >= len(args) {
			return "", false
		}
		value := strings.TrimSpace(args[i+1])
		if value == "" || strings.HasPrefix(value, "--") {
			return "", false
		}
		return value, true
	}
	return "", false
}

// resolveMothxBinary finds the mothx executable this app drives. An explicit
// --mothx-bin launch argument wins; then MOTHX_BIN; then PATH; then the
// repository's bin/ directory, so running the app from a checkout works
// without setup.
func resolveMothxBinary() string {
	bin, _ := resolveMothxBinarySource()
	return bin
}

// resolveMothxBinarySource returns the executable and the launch-time source
// that selected it. The settings Runtime page renders this verbatim instead of
// pretending the choice is a persistent Native preference.
func resolveMothxBinarySource() (bin, source string) {
	if launchBinaryOverride != "" {
		if abs, err := filepath.Abs(launchBinaryOverride); err == nil {
			return abs, "启动参数 --mothx-bin"
		}
		return launchBinaryOverride, "启动参数 --mothx-bin"
	}
	if preference := getRuntimeBinaryPreference(); preference != "" {
		if abs, err := filepath.Abs(preference); err == nil {
			return abs, "Native 设置"
		}
		return preference, "Native 设置"
	}
	if p := os.Getenv("MOTHX_BIN"); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, "MOTHX_BIN"
		}
		return p, "MOTHX_BIN"
	}
	if p, err := exec.LookPath("mothx"); err == nil {
		return p, "PATH"
	}
	candidates := []string{"bin/mothx", "../bin/mothx", "../../bin/mothx", "../../../bin/mothx"}
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return abs, "仓库 bin/"
		}
	}
	return "", ""
}
