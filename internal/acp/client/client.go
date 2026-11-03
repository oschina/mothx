// Package client implements the client side of ACP v1 over a stdio transport.
// It deliberately owns only JSON-RPC transport state. Runtime policy,
// decisions, child lifecycle, and process-tree containment belong to callers.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

const (
	// ProtocolVersion is the ACP major version implemented by this client.
	ProtocolVersion = 1
	// DefaultMaxMessageBytes bounds one NDJSON JSON-RPC frame from an external
	// Agent before Runtime-level output limits are applied.
	DefaultMaxMessageBytes = 10 << 20
)

// RPCError is the JSON-RPC error shape used by ACP.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("ACP RPC error %d: %s", e.Code, e.Message)
}

// Message is one JSON-RPC frame. A Method with an empty ID is a notification;
// a Method with an ID is a reverse request from the ACP Agent.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RequestHandler handles a reverse request made by the ACP Agent. It is
// invoked without the client's transport locks held.
type RequestHandler func(context.Context, Message) (any, *RPCError)

// Process is one started external Agent process. The launcher owns process
// containment and tree reaping; this package only uses its stdio pipes and
// terminal contract.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	// Wait blocks until the process (and its contained tree) has exited.
	Wait() error
	// Terminate kills the process tree. It is idempotent and safe after exit.
	Terminate()
}

// Launcher starts one configured executable. The Runtime installs a
// process-tree-contained implementation; tests may supply a local launcher.
// This package never launches a process itself.
type Launcher interface {
	Start(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer) (Process, error)
}

// Config configures one stdio ACP client process. Command is an argv vector;
// callers are responsible for profile authorization and process containment.
type Config struct {
	Command         []string
	Dir             string
	Env             []string
	MaxMessageBytes int
	Stderr          io.Writer
	HandleRequest   RequestHandler
	Launcher        Launcher
}

// Client owns one bidirectional JSON-RPC stdio connection.
type Client struct {
	ctx    context.Context
	cancel context.CancelFunc

	stdin   io.WriteCloser
	stdout  io.ReadCloser
	process Process

	writer io.Writer
	wmu    sync.Mutex

	mu      sync.Mutex
	pending map[string]chan Message
	nextID  uint64

	notifications chan Message
	handleRequest RequestHandler
	maxBytes      int

	done     chan struct{}
	doneOnce sync.Once
	errMu    sync.Mutex
	err      error
}

// Start launches one configured ACP Agent subprocess and begins consuming its
// NDJSON stdout. The subprocess must write only ACP messages to stdout.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	if len(cfg.Command) == 0 || cfg.Command[0] == "" {
		return nil, errors.New("ACP client command is required")
	}
	if cfg.Launcher == nil {
		return nil, errors.New("ACP client launcher is required")
	}
	commandCtx, cancel := context.WithCancel(ctx)
	process, err := cfg.Launcher.Start(commandCtx, cfg.Command, cfg.Dir, cfg.Env, cfg.Stderr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start ACP agent: %w", err)
	}
	if process == nil {
		cancel()
		return nil, errors.New("ACP client launcher returned no process")
	}
	stdin := process.Stdin()
	stdout := process.Stdout()
	if stdin == nil || stdout == nil {
		process.Terminate()
		cancel()
		return nil, errors.New("ACP client process has no stdio pipes")
	}

	client := newClient(commandCtx, cancel, stdin, stdout, process, cfg)
	go client.readLoop()
	go client.waitLoop()
	return client, nil
}

func newClient(ctx context.Context, cancel context.CancelFunc, stdin io.WriteCloser, stdout io.ReadCloser, process Process, cfg Config) *Client {
	maxBytes := cfg.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	return &Client{
		ctx:           ctx,
		cancel:        cancel,
		stdin:         stdin,
		stdout:        stdout,
		process:       process,
		writer:        stdin,
		pending:       make(map[string]chan Message),
		notifications: make(chan Message, 128),
		handleRequest: cfg.HandleRequest,
		maxBytes:      maxBytes,
		done:          make(chan struct{}),
	}
}

// Notifications returns Agent-originated ACP notifications in receive order.
// Consumers must treat all content as untrusted.
func (c *Client) Notifications() <-chan Message {
	if c == nil {
		return nil
	}
	return c.notifications
}

// Done closes when the transport/process reaches a terminal state.
func (c *Client) Done() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.done
}

// Err returns the terminal transport/process error, if any.
func (c *Client) Err() error {
	if c == nil {
		return errors.New("nil ACP client")
	}
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Call sends one JSON-RPC request and waits for its correlated response.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if c == nil {
		return errors.New("nil ACP client")
	}
	if method == "" {
		return errors.New("ACP method is required")
	}
	id := json.RawMessage(fmt.Sprintf("%d", atomic.AddUint64(&c.nextID, 1)))
	key := string(id)
	responseCh := make(chan Message, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return c.terminalError()
	default:
	}
	c.pending[key] = responseCh
	c.mu.Unlock()
	defer c.removePending(key)

	encodedParams, err := encodeParams(params)
	if err != nil {
		return err
	}
	if err := c.write(Message{JSONRPC: "2.0", ID: id, Method: method, Params: encodedParams}); err != nil {
		return err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return response.Error
		}
		if result != nil && len(response.Result) > 0 && string(response.Result) != "null" {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("decode ACP %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		_ = c.Notify("$/cancel_request", map[string]json.RawMessage{"requestId": id})
		return ctx.Err()
	case <-c.done:
		return c.terminalError()
	}
}

// Notify sends one JSON-RPC notification.
func (c *Client) Notify(method string, params any) error {
	if c == nil {
		return errors.New("nil ACP client")
	}
	if method == "" {
		return errors.New("ACP method is required")
	}
	encodedParams, err := encodeParams(params)
	if err != nil {
		return err
	}
	return c.write(Message{JSONRPC: "2.0", Method: method, Params: encodedParams})
}

// Close cancels the process context, closes pipes, and waits for the read and
// process loops to report their terminal state. Runtime owns graceful ACP
// session cancellation before calling Close.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.cancel()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
	if c.process != nil {
		c.process.Terminate()
	}
	<-c.done
	// Close is best-effort teardown initiated by the owner. Transport errors
	// that occurred before teardown remain observable through Err; an error
	// caused by closing our own pipe is not a second failure.
	return nil
}

func (c *Client) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		line, err := readFrame(reader, c.maxBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// A cleanly exited Agent closes stdout after completing its final
				// request. Callers still treat a missing prompt response as an
				// error because its pending Call sees the terminal connection.
				c.finish(nil)
			} else {
				c.finish(err)
			}
			return
		}
		var message Message
		if err := json.Unmarshal(line, &message); err != nil {
			c.finish(fmt.Errorf("decode ACP frame: %w", err))
			return
		}
		if message.JSONRPC != "2.0" {
			c.finish(fmt.Errorf("unsupported JSON-RPC version %q", message.JSONRPC))
			return
		}
		c.dispatch(message)
	}
}

func (c *Client) waitLoop() {
	if c.process == nil {
		return
	}
	if err := c.process.Wait(); err != nil {
		c.finish(fmt.Errorf("ACP agent exited: %w", err))
	}
}

func (c *Client) dispatch(message Message) {
	if message.Method != "" {
		if len(message.ID) == 0 {
			select {
			case c.notifications <- message:
			case <-c.done:
			}
			return
		}
		go c.handleReverseRequest(message)
		return
	}
	if len(message.ID) == 0 {
		c.finish(errors.New("ACP response missing id"))
		return
	}
	key := string(message.ID)
	c.mu.Lock()
	responseCh, ok := c.pending[key]
	c.mu.Unlock()
	if !ok {
		// A late response to a locally cancelled request is safe to ignore.
		return
	}
	responseCh <- message
}

func (c *Client) handleReverseRequest(message Message) {
	if c.handleRequest == nil {
		_ = c.respond(message.ID, nil, &RPCError{Code: -32601, Message: "method not found"})
		return
	}
	result, rpcErr := c.handleRequest(c.ctx, message)
	_ = c.respond(message.ID, result, rpcErr)
}

func (c *Client) respond(id json.RawMessage, result any, rpcErr *RPCError) error {
	encodedResult, err := encodeParams(result)
	if err != nil {
		return err
	}
	return c.write(Message{JSONRPC: "2.0", ID: id, Result: encodedResult, Error: rpcErr})
}

func (c *Client) write(message Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return c.terminalError()
	default:
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode ACP message: %w", err)
	}
	if len(encoded) > c.maxBytes {
		return fmt.Errorf("ACP outbound frame exceeds %d bytes", c.maxBytes)
	}
	encoded = append(encoded, '\n')
	if _, err := c.writer.Write(encoded); err != nil {
		return fmt.Errorf("write ACP message: %w", err)
	}
	return nil
}

func (c *Client) removePending(key string) {
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *Client) finish(err error) {
	c.doneOnce.Do(func() {
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		c.mu.Lock()
		c.pending = make(map[string]chan Message)
		c.mu.Unlock()
		close(c.notifications)
		close(c.done)
	})
}

func (c *Client) terminalError() error {
	if err := c.Err(); err != nil {
		return err
	}
	return errors.New("ACP client closed")
}

func encodeParams(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode ACP params: %w", err)
	}
	return encoded, nil
}

func readFrame(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	line, err := reader.ReadBytes('\n')
	if len(line) > maxBytes {
		return nil, fmt.Errorf("ACP inbound frame exceeds %d bytes", maxBytes)
	}
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("empty ACP frame")
	}
	line = bytes.TrimSuffix(line[:len(line)-1], []byte{'\r'})
	if len(line) == 0 {
		return nil, errors.New("empty ACP frame")
	}
	return line, nil
}
