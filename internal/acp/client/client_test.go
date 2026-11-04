package client

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientCallAndNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{})
	go client.readLoop()

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)
		line, err := bufio.NewReader(serverRead).ReadBytes('\n')
		if err != nil {
			serverDone <- err
			return
		}
		var request Message
		if err := json.Unmarshal(line, &request); err != nil {
			serverDone <- err
			return
		}
		if request.Method != "echo" {
			serverDone <- &RPCError{Code: -32601, Message: "unexpected method"}
			return
		}
		_, err = io.WriteString(serverWrite, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"child"}}`+"\n")
		if err == nil {
			_, err = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":`+string(request.ID)+`,"result":{"ok":true}}`+"\n")
		}
		serverDone <- err
	}()

	var result struct {
		OK bool `json:"ok"`
	}
	if err := client.Call(context.Background(), "echo", map[string]string{"value": "hello"}, &result); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.OK {
		t.Fatal("Call result did not decode")
	}
	select {
	case notification := <-client.Notifications():
		if notification.Method != "session/update" {
			t.Fatalf("notification method = %q", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification was not delivered")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
	_ = serverWrite.Close()
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestClientHandlesReverseRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{
		HandleRequest: func(_ context.Context, request Message) (any, *RPCError) {
			if request.Method != "session/request_permission" {
				return nil, &RPCError{Code: -32601, Message: "unexpected"}
			}
			return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
		},
	})
	go client.readLoop()

	go func() {
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{"sessionId":"child"}}`+"\n")
	}()
	line, err := bufio.NewReader(serverRead).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read reverse response: %v", err)
	}
	var response Message
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode reverse response: %v", err)
	}
	if string(response.ID) != "7" || !strings.Contains(string(response.Result), "cancelled") {
		t.Fatalf("reverse response = %#v", response)
	}
	_ = serverWrite.Close()
	_ = client.Close()
}

func TestClientCancelsMatchingReverseRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	handlerStarted := make(chan struct{})
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{
		HandleRequest: func(ctx context.Context, request Message) (any, *RPCError) {
			close(handlerStarted)
			<-ctx.Done()
			return map[string]any{"outcome": map[string]string{"outcome": "selected"}}, nil
		},
	})
	go client.readLoop()

	go func() {
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":7,"method":"session/request_permission","params":{"sessionId":"child"}}`+"\n")
		<-handlerStarted
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":7}}`+"\n")
	}()
	line, err := bufio.NewReader(serverRead).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read cancelled reverse response: %v", err)
	}
	var response Message
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode reverse response: %v", err)
	}
	if string(response.ID) != "7" || response.Error == nil || response.Error.Code != -32800 {
		t.Fatalf("cancelled reverse response = %#v", response)
	}
	_ = serverWrite.Close()
	_ = client.Close()
}

func TestClientIgnoresStaleCancelRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{
		HandleRequest: func(context.Context, Message) (any, *RPCError) {
			return map[string]any{"ok": true}, nil
		},
	})
	go client.readLoop()

	go func() {
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":{"stale":true}}}`+"\n")
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":9,"method":"fs/read_text_file","params":{"sessionId":"child"}}`+"\n")
	}()
	line, err := bufio.NewReader(serverRead).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read reverse response: %v", err)
	}
	var response Message
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode reverse response: %v", err)
	}
	if string(response.ID) != "9" || response.Error != nil {
		t.Fatalf("stale cancel affected a later request: %#v", response)
	}
	_ = serverWrite.Close()
	_ = client.Close()
}

func TestClientDistinguishesStringAndNumericRequestIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	handlerStarted := make(chan struct{})
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{
		HandleRequest: func(ctx context.Context, request Message) (any, *RPCError) {
			if string(request.ID) == "7" {
				close(handlerStarted)
				<-ctx.Done()
				return nil, nil
			}
			return map[string]any{"ok": true}, nil
		},
	})
	go client.readLoop()

	_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":7,"method":"terminal/output","params":{"sessionId":"child"}}`+"\n")
	<-handlerStarted
	_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":"7","method":"terminal/output","params":{"sessionId":"child"}}`+"\n")

	reader := bufio.NewReader(serverRead)
	responses := map[string]Message{}
	for len(responses) < 1 {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read reverse response: %v", err)
		}
		var response Message
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("decode reverse response: %v", err)
		}
		responses[string(response.ID)] = response
	}
	if response := responses[`"7"`]; response.Error != nil {
		t.Fatalf("string id was cancelled by numeric-id cancel: %#v", response)
	}
	_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":7}}`+"\n")
	for len(responses) < 2 {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read cancelled reverse response: %v", err)
		}
		var response Message
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("decode reverse response: %v", err)
		}
		responses[string(response.ID)] = response
	}
	if response := responses["7"]; response.Error == nil || response.Error.Code != -32800 {
		t.Fatalf("numeric id was not cancelled: %#v", response)
	}
	_ = serverWrite.Close()
	_ = client.Close()
}

func TestReadFrameRejectsOversizedAndEncodeRejectsUnsupportedParams(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("12345\n"))
	if _, err := readFrame(reader, 4); err == nil {
		t.Fatal("oversized frame was accepted")
	}
	if _, err := encodeParams(func() {}); err == nil {
		t.Fatal("unsupported params were silently encoded")
	}
}

// A frame larger than the configured limit must be rejected without first
// buffering the whole line: ReadBytes would allocate the entire payload before
// the caller could check it.
func TestReadFrameBoundsMemoryBeforeReadingWholeLine(t *testing.T) {
	source := &countingReader{r: strings.NewReader(strings.Repeat("x", 1<<20) + "\n")}
	if _, err := readFrame(bufio.NewReader(source), 1024); err == nil {
		t.Fatal("oversized frame was accepted")
	}
	if source.n > 8*1024 {
		t.Fatalf("read %d bytes before rejecting a 1 MiB frame; the limit was applied after buffering", source.n)
	}
}

func TestReadFrameAcceptsFramesLargerThanTheReaderBuffer(t *testing.T) {
	payload := strings.Repeat("y", 8*1024)
	frame, err := readFrame(bufio.NewReader(strings.NewReader(payload+"\n")), 8*1024+1)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if string(frame) != payload {
		t.Fatalf("frame length = %d, want %d", len(frame), len(payload))
	}
}

// The notification channel is deliberately left open: closing it while a
// transport reader may be sending is a panic. Consumers use Done to stop.
func TestNotificationsStayOpenAfterFinish(t *testing.T) {
	client := newClient(context.Background(), func() {}, nopWriteCloser{Writer: io.Discard}, io.NopCloser(strings.NewReader("")), nil, Config{})
	client.finish(nil)

	select {
	case _, ok := <-client.Notifications():
		if !ok {
			t.Fatal("Notifications channel was closed on transport finish")
		}
	case <-time.After(20 * time.Millisecond):
	}

	// A notification observed after termination must not panic or be queued for
	// a consumer that may already have stopped.
	client.dispatch(Message{JSONRPC: "2.0", Method: "session/update"})
	select {
	case <-client.Notifications():
		t.Fatal("notification was queued after the transport terminated")
	default:
	}
}

type countingReader struct {
	r io.Reader
	n int
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += n
	return n, err
}

func TestStartRunsACPHandshakeAndPromptOverRealStdio(t *testing.T) {
	client, err := Start(context.Background(), Config{
		Command:  []string{os.Args[0], "-test.run=TestACPHelperProcess", "--"},
		Env:      append(os.Environ(), "MOTHX_ACP_TEST_HELPER=1"),
		Launcher: testLauncher{},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}()

	initialized, err := client.Initialize(context.Background(), InitializeParams{ClientInfo: Implementation{Name: "test", Version: "1"}})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initialized.AgentInfo.Name != "helper" {
		t.Fatalf("AgentInfo = %#v", initialized.AgentInfo)
	}
	session, err := client.NewSession(context.Background(), NewSessionParams{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if session.SessionID != "helper-session" {
		t.Fatalf("session = %#v", session)
	}
	prompt, err := client.Prompt(context.Background(), PromptParams{SessionID: session.SessionID, Prompt: []ContentBlock{{Type: "text", Text: "hello"}}})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if prompt.StopReason != "end_turn" {
		t.Fatalf("prompt = %#v", prompt)
	}
	if err := client.CloseSession(context.Background(), session.SessionID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("MOTHX_ACP_TEST_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var request Message
		if err := json.Unmarshal(line, &request); err != nil {
			os.Exit(2)
		}
		var result any
		switch request.Method {
		case "initialize":
			result = InitializeResult{ProtocolVersion: ProtocolVersion, AgentInfo: Implementation{Name: "helper", Version: "1"}}
		case "session/new":
			result = NewSessionResult{SessionID: "helper-session"}
		case "session/prompt":
			_, _ = writer.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"helper-session","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}}}}` + "\n")
			_ = writer.Flush()
			result = PromptResult{StopReason: "end_turn"}
		case "session/close":
			result = map[string]any{}
		default:
			result = map[string]any{}
		}
		response, _ := json.Marshal(Message{JSONRPC: "2.0", ID: request.ID, Result: mustJSON(result)})
		_, _ = writer.Write(append(response, '\n'))
		_ = writer.Flush()
		if request.Method == "session/close" {
			os.Exit(0)
		}
	}
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
