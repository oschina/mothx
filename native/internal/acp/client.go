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
		pending:  make(map[string]pendingCall),
		handler:  handler,
		onStderr: onStderr,
		onExit:   onExit,
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

func (c *Client) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
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
		c.addStderr("ACP stdout reader: " + err.Error())
	}
}

func (c *Client) readStderr(r io.Reader) {
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
		return nil, errors.New("ACP runtime is not connected")
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
		return errors.New("ACP runtime is not connected")
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// close ends the child's stdin so it can flush sessions and exit. A delayed
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
