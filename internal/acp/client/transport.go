package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// frameTransport carries complete ACP JSON-RPC frames. SessionOpened and
// SessionClosed are transport hints for the streamable HTTP profile, which
// needs one long-lived GET stream per session; stdio and WebSocket ignore them.
type frameTransport interface {
	Send(ctx context.Context, sessionID string, frame []byte) error
	Receive() ([]byte, error)
	SessionOpened(ctx context.Context, sessionID string) error
	SessionClosed(sessionID string)
	Close() error
}

// requestCompletionTransport releases per-request routing state after the
// client has authoritatively correlated an inbound response with an outbound
// request. JSON-RPC request IDs are scoped per direction, so a reverse request
// may legitimately reuse an outbound ID; only the response correlation, not
// the frame shape alone, proves the outbound call finished.
type requestCompletionTransport interface {
	CompleteRequest(id json.RawMessage)
}

type stdioTransport struct {
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	process Process

	reader *bufio.Reader
	max    int

	closeOnce sync.Once
}

func newStdioTransport(stdin io.WriteCloser, stdout io.ReadCloser, process Process, maxBytes int) *stdioTransport {
	return &stdioTransport{
		stdin: stdin, stdout: stdout, process: process,
		reader: bufio.NewReader(stdout), max: maxBytes,
	}
}

func (t *stdioTransport) Send(_ context.Context, _ string, frame []byte) error {
	encoded := make([]byte, 0, len(frame)+1)
	encoded = append(encoded, frame...)
	encoded = append(encoded, '\n')
	if _, err := t.stdin.Write(encoded); err != nil {
		return err
	}
	return nil
}

func (t *stdioTransport) Receive() ([]byte, error) {
	return readFrame(t.reader, t.max)
}

func (t *stdioTransport) SessionOpened(context.Context, string) error { return nil }
func (t *stdioTransport) SessionClosed(string)                        {}

func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.stdout != nil {
			_ = t.stdout.Close()
		}
		if t.process != nil {
			t.process.Terminate()
		}
	})
	return nil
}

func (t *stdioTransport) Wait() error {
	if t.process == nil {
		return nil
	}
	return t.process.Wait()
}

type frameQueue struct {
	mu     sync.Mutex
	frames [][]byte
	err    error
	wake   chan struct{}
	closed bool
}

func newFrameQueue() *frameQueue {
	return &frameQueue{wake: make(chan struct{}, 1)}
}

func (q *frameQueue) push(frame []byte) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.frames = append(q.frames, frame)
	q.mu.Unlock()
	q.signal()
}

func (q *frameQueue) fail(err error) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	if err == nil {
		err = io.EOF
	}
	q.err = err
	q.mu.Unlock()
	q.signal()
}

func (q *frameQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.frames = nil
	q.mu.Unlock()
	q.signal()
}

func (q *frameQueue) next(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			frame := q.frames[0]
			q.frames[0] = nil
			q.frames = q.frames[1:]
			q.mu.Unlock()
			return frame, nil
		}
		if q.closed {
			err := q.err
			q.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return nil, err
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.wake:
		}
	}
}

func (q *frameQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func validateFrameSize(frame []byte, maxBytes int) error {
	if maxBytes <= 0 {
		return errors.New("ACP inbound frame limit must be positive")
	}
	if len(frame) > maxBytes {
		return errFrameTooLarge(maxBytes)
	}
	return nil
}

type errFrameTooLarge int

func (e errFrameTooLarge) Error() string {
	return fmt.Sprintf("ACP inbound frame exceeds %d bytes", int(e))
}

// RemoteConfig configures one ACP connection that does not own a local
// process. ACP's remote transport RFD uses the same JSON-RPC lifecycle over a
// WebSocket or streamable HTTP/SSE endpoint. Profile URLs are
// administrator-selected; the client never receives an endpoint from model
// output.
type RemoteConfig struct {
	URL                 string
	MaxMessageBytes     int
	HandleRequest       RequestHandler
	NotificationHandler func(Message)
	// HTTPClient is optional. Tests use it to inject the HTTP/2-capable
	// httptest client; production construction adds a cookie jar and HTTP/2.
	HTTPClient *http.Client
}

func (cfg RemoteConfig) maxBytes() int {
	if cfg.MaxMessageBytes > 0 {
		return cfg.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

func newRemoteHTTPClient(existing *http.Client) *http.Client {
	if existing == nil {
		existing = &http.Client{}
	}
	client := *existing
	if client.Jar == nil {
		if jar, err := cookiejar.New(nil); err == nil {
			client.Jar = jar
		}
	}
	if client.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		transport.ResponseHeaderTimeout = 30 * time.Second
		client.Transport = transport
	}
	return &client
}

// websocketTransport is the first-class WebSocket profile from the ACP remote
// transport RFD: one GET upgrade on the same endpoint, then one JSON-RPC
// message per text frame. Binary frames are ignored.
type websocketTransport struct {
	conn *websocket.Conn
	max  int

	closeOnce sync.Once
}

func dialWebSocket(ctx context.Context, cfg RemoteConfig) (*websocketTransport, error) {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("parse ACP WebSocket URL: %w", err)
	}
	if endpoint.Scheme != "ws" && endpoint.Scheme != "wss" {
		return nil, fmt.Errorf("ACP WebSocket URL must use ws:// or wss://")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("ACP WebSocket URL requires a host")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create ACP cookie jar: %w", err)
	}
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 30 * time.Second,
		Jar:              jar,
	}
	conn, response, err := dialer.DialContext(ctx, endpoint.String(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("connect ACP WebSocket: %w", err)
	}
	conn.SetReadLimit(int64(cfg.maxBytes()))
	return &websocketTransport{conn: conn, max: cfg.maxBytes()}, nil
}

func (t *websocketTransport) Send(_ context.Context, _ string, frame []byte) error {
	if t == nil || t.conn == nil {
		return errors.New("ACP WebSocket transport is closed")
	}
	if err := validateFrameSize(frame, t.max); err != nil {
		return err
	}
	return t.conn.WriteMessage(websocket.TextMessage, frame)
}

func (t *websocketTransport) Receive() ([]byte, error) {
	for {
		kind, frame, err := t.conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if kind != websocket.TextMessage {
			continue
		}
		if err := validateFrameSize(frame, t.max); err != nil {
			return nil, err
		}
		return frame, nil
	}
}

func (t *websocketTransport) SessionOpened(context.Context, string) error { return nil }
func (t *websocketTransport) SessionClosed(string)                        {}

func (t *websocketTransport) Close() error {
	if t == nil {
		return nil
	}
	t.closeOnce.Do(func() {
		if t.conn != nil {
			_ = t.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			_ = t.conn.Close()
		}
	})
	return nil
}

// httpTransport implements the streamable HTTP profile from the ACP remote
// transport RFD. initialize is a synchronous 200 response; every other POST is
// accepted with 202 and its JSON-RPC response arrives on an SSE GET stream.
type httpTransport struct {
	endpoint string
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc
	queue    *frameQueue
	max      int

	mu               sync.Mutex
	connectionID     string
	connectionStream bool
	streams          map[string]context.CancelFunc
	outboundSessions map[string]string
	closed           bool
}

func newHTTPTransport(ctx context.Context, cfg RemoteConfig) (*httpTransport, error) {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, fmt.Errorf("parse ACP HTTP URL: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("ACP HTTP URL must use http:// or https://")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("ACP HTTP URL requires a host")
	}
	transportCtx, cancel := context.WithCancel(ctx)
	return &httpTransport{
		endpoint:         endpoint.String(),
		client:           newRemoteHTTPClient(cfg.HTTPClient),
		ctx:              transportCtx,
		cancel:           cancel,
		queue:            newFrameQueue(),
		max:              cfg.maxBytes(),
		streams:          make(map[string]context.CancelFunc),
		outboundSessions: make(map[string]string),
	}, nil
}

func (t *httpTransport) Send(ctx context.Context, sessionID string, frame []byte) error {
	trimmed := bytes.TrimSpace(frame)
	if len(trimmed) == 0 {
		return errors.New("ACP HTTP frame is empty")
	}
	if trimmed[0] == '[' {
		return errors.New("ACP does not support JSON-RPC batches")
	}
	var message Message
	if err := json.Unmarshal(trimmed, &message); err != nil {
		return fmt.Errorf("decode outbound ACP frame: %w", err)
	}
	if sessionID == "" {
		sessionID = DecodeSessionID(message.Params)
	}
	if message.Method == "initialize" {
		return t.initialize(ctx, trimmed)
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("ACP HTTP transport is closed")
	}
	connectionID := t.connectionID
	if message.Method == "$/cancel_request" && sessionID == "" {
		sessionID = t.outboundSessions[requestIDFromCancel(message.Params)]
	}
	// Only outbound requests establish the request-id -> session mapping used
	// by $/cancel_request. Reverse-request responses also carry an ID (and may
	// collide with an outbound ID), but they are not cancelable outbound calls.
	if message.Method != "" && len(message.ID) > 0 && sessionID != "" {
		if key, ok := canonicalRPCID(message.ID); ok {
			t.outboundSessions[key] = sessionID
		}
	}
	t.mu.Unlock()
	if connectionID == "" {
		return errors.New("ACP HTTP connection is not initialized")
	}
	if sessionID != "" {
		if err := t.openSessionStream(sessionID); err != nil {
			return err
		}
	}
	if err := t.post(ctx, connectionID, sessionID, trimmed); err != nil {
		if message.Method != "" {
			t.CompleteRequest(message.ID)
		}
		return err
	}
	return nil
}

func (t *httpTransport) initialize(ctx context.Context, frame []byte) error {
	response, body, err := t.postRaw(ctx, "", "", frame)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ACP HTTP initialize returned %s: %s", response.Status, body)
	}
	if strings.HasPrefix(strings.ToLower(t.endpoint), "https://") && response.ProtoMajor < 2 {
		return fmt.Errorf("ACP streamable HTTP requires HTTP/2 for https endpoints, got %s", response.Proto)
	}
	connectionID := strings.TrimSpace(response.Header.Get("Acp-Connection-Id"))
	if connectionID == "" {
		var envelope struct {
			Result struct {
				ConnectionID string `json:"connectionId"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			connectionID = strings.TrimSpace(envelope.Result.ConnectionID)
		}
	}
	if connectionID == "" {
		return errors.New("ACP HTTP initialize response is missing Acp-Connection-Id")
	}
	t.mu.Lock()
	t.connectionID = connectionID
	t.mu.Unlock()
	if err := t.openConnectionStream(); err != nil {
		return err
	}
	if len(body) > 0 {
		t.queue.push(body)
	}
	return nil
}

func (t *httpTransport) post(ctx context.Context, connectionID, sessionID string, frame []byte) error {
	response, body, err := t.postRaw(ctx, connectionID, sessionID, frame)
	if err != nil {
		return err
	}
	switch response.StatusCode {
	case http.StatusOK:
		if len(body) > 0 {
			t.queue.push(body)
		}
		return nil
	case http.StatusAccepted:
		return nil
	default:
		if len(body) > 0 {
			return fmt.Errorf("ACP HTTP POST returned %s: %s", response.Status, body)
		}
		return fmt.Errorf("ACP HTTP POST returned %s", response.Status)
	}
}

func (t *httpTransport) postRaw(ctx context.Context, connectionID, sessionID string, frame []byte) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(frame))
	if err != nil {
		return nil, nil, fmt.Errorf("create ACP HTTP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if connectionID != "" {
		request.Header.Set("Acp-Connection-Id", connectionID)
	}
	if sessionID != "" {
		request.Header.Set("Acp-Session-Id", sessionID)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("send ACP HTTP request: %w", err)
	}
	defer response.Body.Close()
	body, err := readBoundedBody(response.Body, t.max)
	if err != nil {
		return nil, nil, err
	}
	return response, body, nil
}

func (t *httpTransport) Receive() ([]byte, error) {
	return t.queue.next(context.Background())
}

func (t *httpTransport) SessionOpened(ctx context.Context, sessionID string) error {
	return t.openSessionStream(sessionID)
}

func (t *httpTransport) openConnectionStream() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("ACP HTTP transport is closed")
	}
	if t.connectionStream {
		t.mu.Unlock()
		return nil
	}
	connectionID := t.connectionID
	t.mu.Unlock()
	if connectionID == "" {
		return errors.New("ACP HTTP connection is not initialized")
	}
	response, err := t.openSSE(t.ctx, connectionID, "")
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = response.Body.Close()
		return errors.New("ACP HTTP transport is closed")
	}
	t.connectionStream = true
	t.mu.Unlock()
	go t.readSSE(response.Body, "")
	return nil
}

func (t *httpTransport) openSessionStream(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("ACP HTTP transport is closed")
	}
	if _, exists := t.streams[sessionID]; exists {
		t.mu.Unlock()
		return nil
	}
	connectionID := t.connectionID
	streamCtx, cancel := context.WithCancel(t.ctx)
	t.streams[sessionID] = cancel
	t.mu.Unlock()
	if connectionID == "" {
		t.removeStream(sessionID)
		return errors.New("ACP HTTP connection is not initialized")
	}
	response, err := t.openSSE(streamCtx, connectionID, sessionID)
	if err != nil {
		t.removeStream(sessionID)
		return err
	}
	go t.readSSE(response.Body, sessionID)
	return nil
}

func (t *httpTransport) openSSE(ctx context.Context, connectionID, sessionID string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, t.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create ACP SSE request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Acp-Connection-Id", connectionID)
	if sessionID != "" {
		request.Header.Set("Acp-Session-Id", sessionID)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open ACP SSE stream: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		body, _ := readBoundedBody(response.Body, 2048)
		return nil, fmt.Errorf("ACP SSE stream returned %s: %s", response.Status, body)
	}
	return response, nil
}

func (t *httpTransport) readSSE(body io.ReadCloser, sessionID string) {
	defer body.Close()
	reader := bufio.NewReader(body)
	var event []byte
	flush := func() bool {
		if len(event) == 0 {
			return true
		}
		frame := append([]byte(nil), event...)
		event = event[:0]
		if err := validateFrameSize(frame, t.max); err != nil {
			t.fail(err)
			return false
		}
		t.queue.push(frame)
		return true
	}
	for {
		raw, readErr := readSSELine(reader, t.max)
		if len(raw) > 0 {
			line := strings.TrimRight(string(raw), "\r\n")
			switch {
			case line == "":
				if !flush() {
					return
				}
			case strings.HasPrefix(line, ":"):
			default:
				field, value, found := strings.Cut(line, ":")
				if found && field == "data" {
					value = strings.TrimPrefix(value, " ")
					if len(event) > 0 {
						event = append(event, '\n')
					}
					if len(event)+len(value) > t.max {
						t.fail(errFrameTooLarge(t.max))
						return
					}
					event = append(event, value...)
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, context.Canceled) && !t.isClosed() {
				t.fail(fmt.Errorf("read ACP SSE stream: %w", readErr))
				return
			}
			if !flush() {
				return
			}
			if !t.isClosed() && errors.Is(readErr, io.EOF) {
				// There is no stream resumption in v1, so a closed GET stream
				// means future responses on that scope can never arrive.
				if sessionID != "" {
					t.fail(fmt.Errorf("ACP session SSE stream closed for %s", sessionID))
				} else {
					t.fail(errors.New("ACP connection SSE stream closed"))
				}
			}
			return
		}
	}
}

// readSSELine accepts lines longer than bufio.Reader's buffer without treating
// ErrBufferFull as a transport failure, while still bounding allocation.
func readSSELine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > limit {
			return nil, errFrameTooLarge(limit)
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func (t *httpTransport) SessionClosed(sessionID string) {
	t.removeStream(sessionID)
}

func (t *httpTransport) removeStream(sessionID string) {
	t.mu.Lock()
	cancel := t.streams[sessionID]
	delete(t.streams, sessionID)
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *httpTransport) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	connectionID := t.connectionID
	cancels := make([]context.CancelFunc, 0, len(t.streams))
	for id, cancel := range t.streams {
		cancels = append(cancels, cancel)
		delete(t.streams, id)
	}
	t.mu.Unlock()

	if connectionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.endpoint, nil)
		if err == nil {
			request.Header.Set("Acp-Connection-Id", connectionID)
			if response, err := t.client.Do(request); err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				_ = response.Body.Close()
			}
		}
		cancel()
	}
	for _, cancel := range cancels {
		cancel()
	}
	t.cancel()
	t.queue.close()
	return nil
}

func (t *httpTransport) fail(err error) {
	if t == nil || t.isClosed() {
		return
	}
	t.queue.fail(err)
}

func (t *httpTransport) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func readBoundedBody(reader io.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxMessageBytes
	}
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, io.LimitReader(reader, int64(limit)+1)); err != nil {
		return nil, fmt.Errorf("read ACP HTTP response: %w", err)
	}
	if buffer.Len() > limit {
		return nil, errFrameTooLarge(limit)
	}
	return bytes.TrimSpace(buffer.Bytes()), nil
}

func requestIDFromCancel(params json.RawMessage) string {
	var envelope struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if len(params) == 0 || json.Unmarshal(params, &envelope) != nil {
		return ""
	}
	key, _ := canonicalRPCID(envelope.RequestID)
	return key
}

// CompleteRequest releases the request-id -> session mapping once the client
// has correlated the matching response. The mapping exists only so a
// client-side $/cancel_request can choose the right Acp-Session-Id header.
func (t *httpTransport) CompleteRequest(id json.RawMessage) {
	key, ok := canonicalRPCID(id)
	if !ok {
		return
	}
	t.mu.Lock()
	delete(t.outboundSessions, key)
	t.mu.Unlock()
}

// Compile-time checks that every transport implements the same contract.
var (
	_ frameTransport = (*stdioTransport)(nil)
	_ frameTransport = (*websocketTransport)(nil)
	_ frameTransport = (*httpTransport)(nil)
)
