package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketTransportLifecycleAndBinaryFrames(t *testing.T) {
	var binarySent atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var request Message
			if err := json.Unmarshal(frame, &request); err != nil {
				continue
			}
			switch request.Method {
			case "initialize":
				writeWSResponse(t, conn, request.ID, InitializeResult{
					ProtocolVersion: ProtocolVersion,
					AgentInfo:       Implementation{Name: "fake-ws", Version: "1"},
				})
			case "session/new":
				writeWSResponse(t, conn, request.ID, NewSessionResult{SessionID: "sess-ws"})
			case "session/prompt":
				_ = conn.WriteMessage(websocket.BinaryMessage, []byte("ignored-binary"))
				binarySent.Store(true)
				writeWSNotification(t, conn, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-ws","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`)
				writeWSResponse(t, conn, request.ID, PromptResult{StopReason: "end_turn"})
			default:
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`[{"jsonrpc":"2.0","id":1,"result":{}}]`))
			}
		}
	}))
	defer server.Close()

	client, err := StartWebSocket(context.Background(), RemoteConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil {
		t.Fatalf("StartWebSocket: %v", err)
	}
	defer client.Close()

	initialized, err := client.Initialize(context.Background(), InitializeParams{ClientInfo: Implementation{Name: "test", Version: "1"}})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initialized.AgentInfo.Name != "fake-ws" {
		t.Fatalf("AgentInfo = %#v", initialized.AgentInfo)
	}
	session, err := client.NewSession(context.Background(), NewSessionParams{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.SessionID != "sess-ws" {
		t.Fatalf("session = %#v", session)
	}
	prompt, err := client.Prompt(context.Background(), PromptParams{SessionID: session.SessionID, Prompt: []ContentBlock{{Type: "text", Text: "hi"}}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if prompt.StopReason != "end_turn" {
		t.Fatalf("prompt = %#v", prompt)
	}
	select {
	case notification := <-client.Notifications():
		if notification.Method != "session/update" {
			t.Fatalf("notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("session update was not delivered")
	}
	if !binarySent.Load() {
		t.Fatal("server never sent the binary frame")
	}
	// The client ignored the binary frame and still correlated the text response.
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWebSocketRejectsJSONRPCBatchesWithoutClosing(t *testing.T) {
	batchResponse := make(chan Message, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`[{"jsonrpc":"2.0","id":1,"result":{}}]`))
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message Message
			if err := json.Unmarshal(frame, &message); err != nil {
				continue
			}
			if message.Error != nil {
				batchResponse <- message
				continue
			}
			if message.Method == "initialize" {
				writeWSResponse(t, conn, message.ID, InitializeResult{ProtocolVersion: ProtocolVersion})
			}
		}
	}))
	defer server.Close()

	client, err := StartWebSocket(context.Background(), RemoteConfig{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil {
		t.Fatalf("StartWebSocket: %v", err)
	}
	defer client.Close()
	if _, err := client.Initialize(context.Background(), InitializeParams{}); err != nil {
		t.Fatalf("Initialize after batch: %v", err)
	}
	select {
	case response := <-batchResponse:
		if string(response.ID) != "null" || response.Error == nil || response.Error.Code != -32600 {
			t.Fatalf("batch rejection = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("batch rejection was not sent")
	}
}

func writeWSResponse(t *testing.T, conn *websocket.Conn, id json.RawMessage, result any) {
	t.Helper()
	encoded, err := json.Marshal(Message{JSONRPC: "2.0", ID: id, Result: mustJSON(result)})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func writeWSNotification(t *testing.T, conn *websocket.Conn, frame string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatalf("write notification: %v", err)
	}
}

type fakeHTTPACPServer struct {
	t *testing.T

	mu           sync.Mutex
	connection   chan []byte
	sessions     map[string]chan []byte
	reverse      chan Message
	reverseSniff chan http.Header
	deleted      atomic.Bool
	http2        atomic.Bool
	cookieSeen   atomic.Bool
}

func newFakeHTTPACPServer(t *testing.T) *fakeHTTPACPServer {
	return &fakeHTTPACPServer{
		t:            t,
		connection:   make(chan []byte, 32),
		sessions:     make(map[string]chan []byte),
		reverse:      make(chan Message, 4),
		reverseSniff: make(chan http.Header, 4),
	}
}

func (s *fakeHTTPACPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.ProtoMajor >= 2 {
			s.http2.Store(true)
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Error(w, "accept required", http.StatusNotAcceptable)
			return
		}
		if r.Header.Get("Acp-Connection-Id") == "" {
			http.Error(w, "connection required", http.StatusBadRequest)
			return
		}
		stream := s.connection
		if sessionID := r.Header.Get("Acp-Session-Id"); sessionID != "" {
			s.mu.Lock()
			stream = s.sessions[sessionID]
			s.mu.Unlock()
			if stream == nil {
				http.Error(w, "unknown session", http.StatusNotFound)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		for {
			select {
			case frame := <-stream:
				if _, err := w.Write([]byte("data: " + string(frame) + "\n\n")); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			case <-r.Context().Done():
				return
			}
		}
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodDelete:
		s.deleted.Store(true)
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *fakeHTTPACPServer) handlePost(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor >= 2 {
		s.http2.Store(true)
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "content type", http.StatusUnsupportedMediaType)
		return
	}
	if cookie, err := r.Cookie("acp-affinity"); err == nil && cookie.Value == "sticky" {
		s.cookieSeen.Store(true)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var message Message
	if err := json.Unmarshal(body, &message); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if message.Method == "initialize" {
		http.SetCookie(w, &http.Cookie{Name: "acp-affinity", Value: "sticky", Path: "/"})
		w.Header().Set("Acp-Connection-Id", "conn-1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      message.ID,
			Result:  mustJSON(InitializeResult{ProtocolVersion: ProtocolVersion, AgentInfo: Implementation{Name: "fake-http", Version: "1"}}),
		})
		return
	}
	if r.Header.Get("Acp-Connection-Id") != "conn-1" {
		http.Error(w, "connection header", http.StatusBadRequest)
		return
	}
	sessionID := DecodeSessionID(message.Params)
	if sessionID != "" && r.Header.Get("Acp-Session-Id") != sessionID {
		http.Error(w, "session header", http.StatusBadRequest)
		return
	}
	if message.Method == "" {
		s.reverseSniff <- r.Header.Clone()
		s.reverse <- message
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	switch message.Method {
	case "session/new":
		frame := mustJSON(Message{JSONRPC: "2.0", ID: message.ID, Result: mustJSON(NewSessionResult{SessionID: "sess-http"})})
		s.mu.Lock()
		s.sessions["sess-http"] = make(chan []byte, 32)
		s.mu.Unlock()
		s.connection <- frame
	case "session/prompt":
		sessionStream := s.sessionStream(sessionID)
		sessionStream <- []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-http","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"http hello"}}}}`)
		sessionStream <- mustJSON(Message{JSONRPC: "2.0", ID: message.ID, Result: mustJSON(PromptResult{StopReason: "end_turn"})})
	case "session/request_permission":
		// no-op: this is a client response path in the test
	default:
	}
}

func (s *fakeHTTPACPServer) sessionStream(sessionID string) chan []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := s.sessions[sessionID]
	if stream == nil {
		stream = make(chan []byte, 32)
		s.sessions[sessionID] = stream
	}
	return stream
}

func TestHTTPTransportLifecycleSSEAndCookies(t *testing.T) {
	fake := newFakeHTTPACPServer(t)
	server := httptest.NewUnstartedServer(fake)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client, err := StartHTTP(context.Background(), RemoteConfig{
		URL:        server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer client.Close()

	initialized, err := client.Initialize(context.Background(), InitializeParams{ClientInfo: Implementation{Name: "test", Version: "1"}})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initialized.AgentInfo.Name != "fake-http" {
		t.Fatalf("AgentInfo = %#v", initialized.AgentInfo)
	}
	session, err := client.NewSession(context.Background(), NewSessionParams{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.SessionID != "sess-http" {
		t.Fatalf("session = %#v", session)
	}
	if err := client.SessionOpened(context.Background(), session.SessionID); err != nil {
		t.Fatalf("SessionOpened: %v", err)
	}
	prompt, err := client.Prompt(context.Background(), PromptParams{SessionID: session.SessionID, Prompt: []ContentBlock{{Type: "text", Text: "hi"}}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if prompt.StopReason != "end_turn" {
		t.Fatalf("prompt = %#v", prompt)
	}
	select {
	case notification := <-client.Notifications():
		if notification.Method != "session/update" {
			t.Fatalf("notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("session update was not delivered over SSE")
	}
	if !fake.http2.Load() {
		t.Fatal("streamable HTTP did not use HTTP/2")
	}
	if !fake.cookieSeen.Load() {
		t.Fatal("cookie from initialize was not sent on later requests")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for !fake.deleted.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fake.deleted.Load() {
		t.Fatal("DELETE did not terminate the connection")
	}
}

func TestHTTPTransportReverseResponseCarriesSessionHeader(t *testing.T) {
	fake := newFakeHTTPACPServer(t)
	server := httptest.NewUnstartedServer(fake)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client, err := StartHTTP(context.Background(), RemoteConfig{
		URL:        server.URL,
		HTTPClient: server.Client(),
		HandleRequest: func(context.Context, Message) (any, *RPCError) {
			return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer client.Close()
	if _, err := client.Initialize(context.Background(), InitializeParams{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	session, err := client.NewSession(context.Background(), NewSessionParams{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := client.SessionOpened(context.Background(), session.SessionID); err != nil {
		t.Fatalf("SessionOpened: %v", err)
	}
	stream := fake.sessionStream(session.SessionID)
	stream <- mustJSON(Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage("99"),
		Method:  "session/request_permission",
		Params:  json.RawMessage(`{"sessionId":"sess-http","toolCallId":"tc-1","options":[]}`),
	})
	select {
	case response := <-fake.reverse:
		if string(response.ID) != "99" || !strings.Contains(string(response.Result), "cancelled") {
			t.Fatalf("reverse response = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("reverse response was not posted")
	}
	select {
	case header := <-fake.reverseSniff:
		if header.Get("Acp-Connection-Id") != "conn-1" || header.Get("Acp-Session-Id") != "sess-http" {
			t.Fatalf("reverse response headers = %#v", header)
		}
	case <-time.After(time.Second):
		t.Fatal("reverse response headers were not captured")
	}
}

func TestHTTPTransportCompleteRequestReleasesOutboundSession(t *testing.T) {
	transport := &httpTransport{outboundSessions: map[string]string{"7": "sess-a"}}
	transport.CompleteRequest(json.RawMessage("7"))
	if _, ok := transport.outboundSessions["7"]; ok {
		t.Fatal("outbound session mapping was not released after response correlation")
	}
}

// requestTrackingTransport mirrors the HTTP transport's request-id -> session
// map so the client-level correlation contract can be tested without HTTP.
type requestTrackingTransport struct {
	mu             sync.Mutex
	sessions       map[string]string
	requests       chan Message
	cancelSessions []string
	inbound        chan []byte
	closed         chan struct{}
	closeOnce      sync.Once
}

func newRequestTrackingTransport() *requestTrackingTransport {
	return &requestTrackingTransport{
		sessions: make(map[string]string),
		requests: make(chan Message, 16),
		inbound:  make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (t *requestTrackingTransport) Send(_ context.Context, sessionID string, frame []byte) error {
	var message Message
	if err := json.Unmarshal(frame, &message); err != nil {
		return err
	}
	if sessionID == "" {
		sessionID = DecodeSessionID(message.Params)
	}
	t.mu.Lock()
	if message.Method == "$/cancel_request" && sessionID == "" {
		sessionID = t.sessions[requestIDFromCancel(message.Params)]
		t.cancelSessions = append(t.cancelSessions, sessionID)
	}
	if message.Method != "" && len(message.ID) > 0 && sessionID != "" {
		if key, ok := canonicalRPCID(message.ID); ok {
			t.sessions[key] = sessionID
		}
	}
	t.mu.Unlock()
	t.requests <- message
	return nil
}

func (t *requestTrackingTransport) Receive() ([]byte, error) {
	select {
	case frame := <-t.inbound:
		return frame, nil
	case <-t.closed:
		return nil, io.EOF
	}
}

func (t *requestTrackingTransport) SessionOpened(context.Context, string) error { return nil }
func (t *requestTrackingTransport) SessionClosed(string)                        {}

func (t *requestTrackingTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}

func (t *requestTrackingTransport) CompleteRequest(id json.RawMessage) {
	key, ok := canonicalRPCID(id)
	if !ok {
		return
	}
	t.mu.Lock()
	delete(t.sessions, key)
	t.mu.Unlock()
}

func (t *requestTrackingTransport) session(id string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[id]
}

func TestClientReleasesOutboundSessionOnlyAfterCorrelatedResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := newRequestTrackingTransport()
	client := newClientWithTransport(ctx, cancel, transport, Config{})
	go client.readLoop()
	defer client.Close()

	callDone := make(chan error, 1)
	go func() {
		callDone <- client.Call(ctx, "session/prompt", map[string]string{"sessionId": "sess-a"}, nil)
	}()
	var request Message
	select {
	case request = <-transport.requests:
	case <-time.After(time.Second):
		t.Fatal("prompt request was not sent")
	}
	key, ok := canonicalRPCID(request.ID)
	if !ok {
		t.Fatalf("request id = %q", request.ID)
	}
	if got := transport.session(key); got != "sess-a" {
		t.Fatalf("outbound session mapping = %q, want sess-a", got)
	}
	// A reverse request may reuse the same JSON-RPC id; it must not release the
	// outbound mapping before the outbound response is correlated.
	transport.inbound <- mustJSON(Message{
		JSONRPC: "2.0",
		ID:      request.ID,
		Method:  "fs/read_text_file",
		Params:  json.RawMessage(`{"sessionId":"sess-b","path":"/tmp/a"}`),
	})
	select {
	case <-transport.requests:
	case <-time.After(time.Second):
		t.Fatal("reverse request response was not sent")
	}
	if got := transport.session(key); got != "sess-a" {
		t.Fatalf("outbound mapping changed by a colliding reverse request: %q", got)
	}
	transport.inbound <- mustJSON(Message{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  json.RawMessage(`{"stopReason":"end_turn"}`),
	})
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Call did not complete")
	}
	if got := transport.session(key); got != "" {
		t.Fatalf("outbound session mapping = %q after response, want released", got)
	}
}

func TestClientCancellationKeepsSessionMappingUntilCancelIsSent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := newRequestTrackingTransport()
	client := newClientWithTransport(ctx, cancel, transport, Config{})
	go client.readLoop()
	defer client.Close()

	callCtx, stopCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		callDone <- client.Call(callCtx, "session/prompt", map[string]string{"sessionId": "sess-a"}, nil)
	}()
	var request Message
	select {
	case request = <-transport.requests:
	case <-time.After(time.Second):
		t.Fatal("prompt request was not sent")
	}
	stopCall()
	select {
	case message := <-transport.requests:
		if message.Method != "$/cancel_request" {
			t.Fatalf("second outbound message = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel notification was not sent")
	}
	<-callDone
	transport.mu.Lock()
	cancelSessions := append([]string(nil), transport.cancelSessions...)
	transport.mu.Unlock()
	if len(cancelSessions) != 1 || cancelSessions[0] != "sess-a" {
		t.Fatalf("cancel header sessions = %#v, want [sess-a]", cancelSessions)
	}
	key, _ := canonicalRPCID(request.ID)
	if got := transport.session(key); got != "" {
		t.Fatalf("outbound session mapping = %q after cancel, want released", got)
	}
}
