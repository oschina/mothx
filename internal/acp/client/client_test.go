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

func TestReadFrameRejectsOversizedAndEncodeRejectsUnsupportedParams(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("12345\n"))
	if _, err := readFrame(reader, 4); err == nil {
		t.Fatal("oversized frame was accepted")
	}
	if _, err := encodeParams(func() {}); err == nil {
		t.Fatal("unsupported params were silently encoded")
	}
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
