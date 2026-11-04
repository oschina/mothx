// Package client implements the client side of ACP v1 over stdio, WebSocket,
// or streamable HTTP/SSE. It deliberately owns only JSON-RPC transport state.
// Runtime policy, decisions, child lifecycle, and process-tree containment
// belong to callers.
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
	// NotificationHandler, when set, receives notifications synchronously in
	// stdout order before a following JSON-RPC response is dispatched. This is
	// the multi-session routing path: a handler must enqueue quickly rather
	// than perform slow rendering. When nil, Notifications() is used.
	NotificationHandler func(Message)
	Launcher            Launcher
}

// Client owns one bidirectional JSON-RPC ACP connection.
type Client struct {
	ctx    context.Context
	cancel context.CancelFunc

	transport frameTransport
	wmu       sync.Mutex
	closing   atomic.Bool

	mu      sync.Mutex
	pending map[string]chan Message
	// reverse maps an in-flight Agent-to-Client request ID to its cancel
	// function. ACP's $/cancel_request is scoped to one request ID, so it must
	// not cancel the transport or unrelated reverse requests.
	reverse map[string]*reverseCall
	// inboundSessions maps a reverse request ID to its ACP session so a
	// streamable HTTP response carries the required Acp-Session-Id header even
	// though a JSON-RPC response body has no params.
	inboundSessions map[string]string
	nextID          uint64

	notifications chan Message
	handleRequest RequestHandler
	handleNotify  func(Message)
	maxBytes      int

	done     chan struct{}
	doneOnce sync.Once
	errMu    sync.Mutex
	err      error
}

type reverseCall struct {
	cancel context.CancelFunc
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
	return newClientWithTransport(ctx, cancel, newStdioTransport(stdin, stdout, process, maxBytes), cfg)
}

// StartWebSocket connects one ACP Agent over the remote transport RFD's
// first-class WebSocket profile. The caller owns profile authorization and
// Runtime lifecycle; this function only establishes the JSON-RPC connection.
func StartWebSocket(ctx context.Context, cfg RemoteConfig) (*Client, error) {
	transport, err := dialWebSocket(ctx, cfg)
	if err != nil {
		return nil, err
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	client := newClientWithTransport(connectionCtx, cancel, transport, Config{
		MaxMessageBytes:     cfg.MaxMessageBytes,
		HandleRequest:       cfg.HandleRequest,
		NotificationHandler: cfg.NotificationHandler,
	})
	go func() {
		<-connectionCtx.Done()
		_ = transport.Close()
	}()
	go client.readLoop()
	return client, nil
}

// StartHTTP connects one ACP Agent over the remote transport RFD's streamable
// HTTP profile. The transport opens a connection-scoped SSE stream after
// initialize and one session-scoped stream per registered session.
func StartHTTP(ctx context.Context, cfg RemoteConfig) (*Client, error) {
	transport, err := newHTTPTransport(ctx, cfg)
	if err != nil {
		return nil, err
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	client := newClientWithTransport(connectionCtx, cancel, transport, Config{
		MaxMessageBytes:     cfg.MaxMessageBytes,
		HandleRequest:       cfg.HandleRequest,
		NotificationHandler: cfg.NotificationHandler,
	})
	go func() {
		<-connectionCtx.Done()
		_ = transport.Close()
	}()
	go client.readLoop()
	return client, nil
}

func newClientWithTransport(ctx context.Context, cancel context.CancelFunc, transport frameTransport, cfg Config) *Client {
	maxBytes := cfg.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	return &Client{
		ctx:             ctx,
		cancel:          cancel,
		transport:       transport,
		pending:         make(map[string]chan Message),
		reverse:         make(map[string]*reverseCall),
		inboundSessions: make(map[string]string),
		notifications:   make(chan Message, 128),
		handleRequest:   cfg.HandleRequest,
		handleNotify:    cfg.NotificationHandler,
		maxBytes:        maxBytes,
		done:            make(chan struct{}),
	}
}

// Notifications returns Agent-originated ACP notifications in receive order.
// Consumers must treat all content as untrusted.
//
// The channel is never closed: a send in progress must not race a close when
// the transport terminates. Consumers stop by selecting on Done, then drain any
// notifications already buffered.
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
	if err := c.writeContext(ctx, "", Message{JSONRPC: "2.0", ID: id, Method: method, Params: encodedParams}); err != nil {
		return err
	}
	handleResponse := func(response Message) error {
		if response.Error != nil {
			return response.Error
		}
		if result != nil && len(response.Result) > 0 && string(response.Result) != "null" {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("decode ACP %s result: %w", method, err)
			}
		}
		return nil
	}
	select {
	case response := <-responseCh:
		return handleResponse(response)
	case <-ctx.Done():
		_ = c.Notify("$/cancel_request", map[string]json.RawMessage{"requestId": id})
		c.completeRequest(id)
		return ctx.Err()
	case <-c.done:
		// The final response may have been dispatched just before stdout hit
		// EOF. Prefer it over the transport's terminal error.
		select {
		case response := <-responseCh:
			return handleResponse(response)
		default:
		}
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

// SessionOpened tells the transport that one remote session now exists. The
// streamable HTTP profile opens its session-scoped SSE stream here; other
// transports treat it as a no-op.
func (c *Client) SessionOpened(ctx context.Context, sessionID string) error {
	if c == nil || c.transport == nil {
		return errors.New("nil ACP client")
	}
	return c.transport.SessionOpened(ctx, sessionID)
}

// SessionClosed releases the transport resources attached to one remote
// session. It never closes the shared ACP connection.
func (c *Client) SessionClosed(sessionID string) {
	if c == nil || c.transport == nil {
		return
	}
	c.transport.SessionClosed(sessionID)
}

// Close cancels the connection context, closes the transport, and waits for
// the read/process loops to report their terminal state. Runtime owns graceful
// ACP session cancellation before calling Close.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closing.Store(true)
	c.cancel()
	if c.transport != nil {
		_ = c.transport.Close()
	}
	<-c.done
	// Close is best-effort teardown initiated by the owner. Transport errors
	// that occurred before teardown remain observable through Err; an error
	// caused by closing our own pipe is not a second failure.
	return nil
}

func (c *Client) readLoop() {
	for {
		line, err := c.transport.Receive()
		if err != nil {
			if c.closing.Load() {
				c.finish(nil)
				return
			}
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
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			// ACP does not use JSON-RPC batches. Reject the frame without
			// processing any entry and keep the connection usable.
			_ = c.write(Message{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &RPCError{Code: -32600, Message: "JSON-RPC batches are not supported"}})
			continue
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
	waiter, ok := c.transport.(interface{ Wait() error })
	if !ok {
		return
	}
	if err := waiter.Wait(); err != nil {
		c.finish(fmt.Errorf("ACP agent exited: %w", err))
	}
}

func (c *Client) dispatch(message Message) {
	if message.Method != "" {
		if len(message.ID) == 0 {
			if message.Method == "$/cancel_request" {
				c.cancelReverseRequest(message)
				return
			}
			if c.handleNotify != nil {
				c.handleNotify(message)
				return
			}
			select {
			case <-c.done:
				return
			default:
			}
			select {
			case c.notifications <- message:
			case <-c.done:
			}
			return
		}
		c.startReverseRequest(message)
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
	c.completeRequest(message.ID)
	responseCh <- message
}

func (c *Client) completeRequest(id json.RawMessage) {
	transport, ok := c.transport.(requestCompletionTransport)
	if !ok {
		return
	}
	transport.CompleteRequest(id)
}

// startReverseRequest registers a cancel handle before invoking the handler so
// an Agent can cancel it immediately with $/cancel_request. Registration is
// synchronous in the stdout read loop, which guarantees the handle exists
// before a following notification is dispatched.
func (c *Client) startReverseRequest(message Message) {
	key, ok := canonicalRPCID(message.ID)
	if !ok {
		_ = c.respond(message.ID, nil, &RPCError{Code: -32600, Message: "invalid request id"})
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	call := &reverseCall{cancel: cancel}
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		cancel()
		return
	default:
	}
	if _, exists := c.reverse[key]; exists {
		c.mu.Unlock()
		cancel()
		_ = c.respond(message.ID, nil, &RPCError{Code: -32600, Message: "duplicate in-flight request id"})
		return
	}
	c.reverse[key] = call
	if sessionID := DecodeSessionID(message.Params); sessionID != "" {
		c.inboundSessions[key] = sessionID
	}
	c.mu.Unlock()
	go c.handleReverseRequest(ctx, key, call, message)
}

func (c *Client) handleReverseRequest(ctx context.Context, key string, call *reverseCall, message Message) {
	defer func() {
		c.removeReverseRequest(key, call)
		call.cancel()
	}()
	if c.handleRequest == nil {
		_ = c.respond(message.ID, nil, &RPCError{Code: -32601, Message: "method not found"})
		return
	}
	result, rpcErr := c.handleRequest(ctx, message)
	if ctx.Err() != nil {
		// ACP requires the original request to receive either a valid result or
		// Request Cancelled after $/cancel_request. The handler may have returned
		// its own error while unwinding; normalize it to the protocol code.
		result = nil
		rpcErr = &RPCError{Code: -32800, Message: "Request cancelled"}
	}
	_ = c.respond(message.ID, result, rpcErr)
}

func (c *Client) cancelReverseRequest(message Message) {
	requestID, ok := DecodeCancelRequest(message)
	if !ok {
		return
	}
	c.mu.Lock()
	call := c.reverse[requestID]
	c.mu.Unlock()
	if call != nil {
		call.cancel()
	}
}

func (c *Client) removeReverseRequest(key string, call *reverseCall) {
	c.mu.Lock()
	if c.reverse[key] == call {
		delete(c.reverse, key)
	}
	delete(c.inboundSessions, key)
	c.mu.Unlock()
}

func (c *Client) respond(id json.RawMessage, result any, rpcErr *RPCError) error {
	encodedResult, err := encodeParams(result)
	if err != nil {
		return err
	}
	sessionID := ""
	if key, ok := canonicalRPCID(id); ok {
		c.mu.Lock()
		sessionID = c.inboundSessions[key]
		c.mu.Unlock()
	}
	return c.writeContext(c.ctx, sessionID, Message{JSONRPC: "2.0", ID: id, Result: encodedResult, Error: rpcErr})
}

func (c *Client) write(message Message) error {
	return c.writeContext(c.ctx, "", message)
}

func (c *Client) writeContext(ctx context.Context, sessionID string, message Message) error {
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
	if err := c.transport.Send(ctx, sessionID, encoded); err != nil {
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
		for key, call := range c.reverse {
			call.cancel()
			delete(c.reverse, key)
		}
		c.mu.Unlock()
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

// canonicalRPCID normalizes a JSON-RPC id without conflating a string id with a
// numerically equal id. JSON-RPC treats "7" and 7 as different request ids.
func canonicalRPCID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var id any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&id); err != nil || id == nil {
		return "", false
	}
	encoded, err := json.Marshal(id)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func readFrame(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("ACP inbound frame limit must be positive")
	}
	// ReadSlice returns the reader's internal buffer directly, so the frame is
	// copied only after its size is checked. ReadBytes would grow an unbounded
	// allocation before the limit could be enforced.
	var frame []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(frame)+len(fragment) > maxBytes {
			return nil, fmt.Errorf("ACP inbound frame exceeds %d bytes", maxBytes)
		}
		frame = append(frame, fragment...)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
	if len(frame) == 0 {
		return nil, errors.New("empty ACP frame")
	}
	frame = bytes.TrimSuffix(frame[:len(frame)-1], []byte{'\r'})
	if len(frame) == 0 {
		return nil, errors.New("empty ACP frame")
	}
	return frame, nil
}
