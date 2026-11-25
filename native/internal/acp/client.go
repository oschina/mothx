package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "json-rpc error"
	}
	return fmt.Sprintf("%s (%d)", e.Message, e.Code)
}

// ErrStreamFailed is the cause reported through onExit when the ACP stdout
// stream becomes unreadable while the child may still be running, for example
// a line longer than the reader accepts. The stream cannot be resumed past
// such a line, so the connection fails like a crash and its owner restarts it.
var ErrStreamFailed = errors.New("ACP stream failed")

// errNotConnected is returned once the connection is closed or has failed.
var errNotConnected = errors.New("ACP runtime is not connected")

// acpMaxLineBytes bounds one ACP stdio line. The Runtime rejects a request
// over 10 MiB and caps an attachment fetch at the same raw size, whose base64
// form is about 13.4 MiB, so 64 MiB keeps ample headroom. A longer line is a
// protocol violation: the reader fails the connection instead of silently
// stopping and leaving a live child that nothing listens to.
const acpMaxLineBytes = 64 << 20

// acpMaxStderrLineBytes bounds one diagnostics line; stderr is only shown in
// the connection error card.
const acpMaxStderrLineBytes = 1 << 20

// acpDrainGrace bounds how long an exited child's last lines may take to
// drain. A pipe that some other process inherited would never reach EOF.
const acpDrainGrace = 2 * time.Second

// Incoming is one line of the ACP stdio channel: a response, a notification,
// or a request the agent sends to its client.
type Incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

func (m Incoming) HasID() bool {
	return len(m.ID) > 0 && !bytes.Equal(bytes.TrimSpace(m.ID), []byte("null"))
}

type pendingCall struct {
	ch chan callResult
}

type callResult struct {
	result json.RawMessage
	err    error
}

// Client owns one `mothx acp` child process and speaks ACP v1 over its
// stdio in newline-delimited JSON.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	// maxLineBytes is the largest stdout line the reader accepts before it
	// fails the connection. Tests lower it to exercise that path cheaply.
	maxLineBytes int

	// stdout and readDone belong to the reader goroutine: readDone closes once
	// it has returned, which is how the exit path knows the pipe was drained.
	stdout   io.ReadCloser
	readDone chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]pendingCall
	nextID  int64
	closed  bool

	handler  func(Incoming)
	onStderr func(string)
	onExit   func(error)

	stderrMu sync.Mutex
	stderr   []string
}

func NewClient(handler func(Incoming), onStderr func(string), onExit func(error)) *Client {
	return &Client{
		pending:      make(map[string]pendingCall),
		handler:      handler,
		onStderr:     onStderr,
		onExit:       onExit,
		maxLineBytes: acpMaxLineBytes,
		readDone:     make(chan struct{}),
	}
}

// start launches the ACP process. The caller must have resolved binary to an
// executable path.
func (c *Client) Start(binary, dir string, env []string) error {
	cmd := exec.Command(binary, "acp")
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	// stdout and stderr use pipes this client owns rather than cmd.StdoutPipe:
	// cmd.Wait closes those read ends as soon as the process exits, which races
	// the readers and drops the last lines a child writes before exiting. With
	// owned pipes the readers see every byte through EOF.
	stdout, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutWrite.Close()
		return err
	}
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stdoutWrite.Close()
		_ = stderr.Close()
		_ = stderrWrite.Close()
		return err
	}
	// The child inherited its own copies. Closing ours is what makes the
	// readers see EOF once the child exits.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout

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
		// The child is gone, but the lines it wrote last may still be in the
		// pipe. Let the reader reach EOF before pending calls are failed, so a
		// response written just before the exit is still delivered. A process
		// that inherited the pipe would keep EOF from ever coming, so the drain
		// is bounded and the pipe is closed instead.
		select {
		case <-c.readDone:
		case <-time.After(acpDrainGrace):
			_ = stdout.Close()
		}
		c.mu.Lock()
		alreadyClosed := c.closed
		c.closed = true
		for key, p := range c.pending {
			p.ch <- callResult{err: fmt.Errorf("ACP process exited: %w", cause)}
			delete(c.pending, key)
		}
		c.mu.Unlock()
		// A stream failure already reported the exit; the kill that follows it
		// must not report a second one.
		if !alreadyClosed && c.onExit != nil {
			c.onExit(err)
		}
	}()
	return nil
}

func (c *Client) readLoop(r io.ReadCloser) {
	defer close(c.readDone)
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), c.maxLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg Incoming
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
		// A closed pipe is the bounded drain above ending on purpose; the exit
		// path reports the real cause.
		if errors.Is(err, os.ErrClosed) {
			return
		}
		c.addStderr("ACP stdout reader: " + err.Error())
		c.fail(err)
	}
}

func (c *Client) readStderr(r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), acpMaxStderrLineBytes)
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

func (c *Client) addStderr(line string) {
	c.stderrMu.Lock()
	c.stderr = append(c.stderr, line)
	if len(c.stderr) > 80 {
		c.stderr = c.stderr[len(c.stderr)-80:]
	}
	c.stderrMu.Unlock()
}

// stderrTail returns recent diagnostics for the connection error card.
func (c *Client) StderrTail(n int) []string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	if n <= 0 || n > len(c.stderr) {
		n = len(c.stderr)
	}
	out := make([]string, n)
	copy(out, c.stderr[len(c.stderr)-n:])
	return out
}

// defaultRequestTimeout bounds ordinary ACP requests. A wedged child must fail
// a management/load call visibly instead of leaving a panel loading forever. A
// prompt is the only request whose Runtime work has no safe client-side
// deadline, so it keeps the unbounded path.
const defaultRequestTimeout = 5 * time.Minute

var unboundedACPMethods = map[string]struct{}{
	"session/prompt": {},
}

// requestTimeout reports the deadline for one method. The bool is false for
// requests that intentionally have no client-side deadline.
func requestTimeout(method string) (time.Duration, bool) {
	if _, ok := unboundedACPMethods[method]; ok {
		return 0, false
	}
	return defaultRequestTimeout, true
}

// call sends a request and waits for its response. It may be called from any
// goroutine; the UI must run it off the main thread.
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	timeout, bounded := requestTimeout(method)
	if !bounded {
		return c.CallContext(context.Background(), method, params)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.CallContext(ctx, method, params)
}

// CallContext is Call with a caller-owned deadline. It is used for requests
// such as initialize where a wedged child must fail visibly instead of blocking
// a worker forever; long-running prompts keep the unbounded Call.
func (c *Client) CallContext(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errNotConnected
	}
	c.nextID++
	id := c.nextID
	key := "n:" + strconv.FormatInt(id, 10)
	p := pendingCall{ch: make(chan callResult, 1)}
	c.pending[key] = p
	c.mu.Unlock()

	dropPending := func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}

	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if err := c.writeJSON(req); err != nil {
		dropPending()
		return nil, err
	}
	select {
	case res := <-p.ch:
		return res.result, res.err
	case <-ctx.Done():
		dropPending()
		return nil, ctx.Err()
	}
}

// notify sends a notification, which has no response.
func (c *Client) Notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSON(msg)
}

// respond answers a request the agent sent to this client.
func (c *Client) Respond(id json.RawMessage, result any) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
	return c.writeJSON(msg)
}

// respondError answers a reverse request with a JSON-RPC error.
func (c *Client) RespondError(id json.RawMessage, code int, message string) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": message}}
	return c.writeJSON(msg)
}

func (c *Client) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return errNotConnected
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// Closed reports whether the connection has been closed or has failed.
func (c *Client) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Close ends the child's stdin so it can flush sessions and exit. A delayed
// kill makes sure a wedged process cannot outlive the app.
func (c *Client) Close() {
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
	c.stopChild()
}

// fail tears the connection down after a transport failure that leaves the
// stream unusable while the child may still be running. It does what a crash
// would: pending calls fail, the child is asked to stop and is killed if it
// lingers, and onExit reports the cause exactly once.
func (c *Client) fail(cause error) {
	streamErr := fmt.Errorf("%w: %w", ErrStreamFailed, cause)
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
		p.ch <- callResult{err: streamErr}
	}
	c.stopChild()
	if c.onExit != nil {
		c.onExit(streamErr)
	}
}

// stopChild closes the child's stdin so it can flush sessions and exit, and
// kills it after a grace period so a wedged process cannot outlive the app.
func (c *Client) stopChild() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	cmd := c.cmd
	if cmd != nil && cmd.Process != nil {
		// Kill through the same *os.Process that cmd.Wait owns. A delayed
		// os.FindProcess(pid).Kill() could target an unrelated process if the
		// PID was reused after the child was reaped; Process.Kill reports
		// ErrProcessDone instead of signalling a bare PID.
		go func(process *os.Process) {
			time.Sleep(2 * time.Second)
			_ = process.Kill()
		}(cmd.Process)
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
