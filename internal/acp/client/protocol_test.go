package client

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestDecodeSessionUpdateAndPermissionRequest(t *testing.T) {
	updateMessage := Message{Method: "session/update", Params: json.RawMessage(`{
		"sessionId":"remote",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}
	}`)}
	update, ok := DecodeSessionUpdate(updateMessage)
	if !ok || update.SessionUpdate != "agent_message_chunk" || update.Content == nil || update.Content.Text != "hello" {
		t.Fatalf("decoded update = %#v, ok=%v", update, ok)
	}

	permissionMessage := Message{Method: "session/request_permission", Params: json.RawMessage(`{
		"sessionId":"remote",
		"toolCall":{"toolCallId":"tool-1","title":"write file","kind":"edit","rawInput":{"path":"a.go"}},
		"options":[
			{"optionId":"allow-once","name":"Allow once","kind":"allow_once"},
			{"optionId":"allow-always","name":"Always","kind":"allow_always"},
			{"optionId":"reject-once","name":"Reject","kind":"reject_once"}
		]
	}`)}
	request, ok := DecodePermissionRequest(permissionMessage)
	if !ok || request.SessionID != "remote" || request.ToolCall.ToolCallID != "tool-1" || len(request.Options) != 3 {
		t.Fatalf("decoded permission request = %#v, ok=%v", request, ok)
	}
	if request.Options[0].Kind != "allow_once" || request.Options[2].OptionID != "reject-once" {
		t.Fatalf("permission options = %#v", request.Options)
	}

	if _, ok := DecodeSessionUpdate(Message{Method: "session/prompt"}); ok {
		t.Fatal("non-update method decoded as a session update")
	}
	if _, ok := DecodePermissionRequest(Message{Method: "session/update"}); ok {
		t.Fatal("non-permission method decoded as a permission request")
	}
}

func TestPermissionResultShapes(t *testing.T) {
	selected, err := json.Marshal(PermissionSelected("allow-once"))
	if err != nil {
		t.Fatal(err)
	}
	if string(selected) != `{"outcome":{"outcome":"selected","optionId":"allow-once"}}` {
		t.Fatalf("selected result = %s", selected)
	}
	cancelled, err := json.Marshal(PermissionCancelled())
	if err != nil {
		t.Fatal(err)
	}
	if string(cancelled) != `{"outcome":{"outcome":"cancelled"}}` {
		t.Fatalf("cancelled result = %s", cancelled)
	}
}

func TestCloseSessionTreatsUnknownSessionAsSuccessful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()
	client := newClient(ctx, cancel, clientWrite, clientRead, nil, Config{})
	go client.readLoop()

	go func() {
		line, err := bufio.NewReader(serverRead).ReadBytes('\n')
		if err != nil {
			return
		}
		var request Message
		if err := json.Unmarshal(line, &request); err != nil {
			return
		}
		_, _ = io.WriteString(serverWrite, `{"jsonrpc":"2.0","id":`+string(request.ID)+`,"error":{"code":-32002,"message":"Resource not found"}}`+"\n")
	}()
	if err := client.CloseSession(context.Background(), "already-closed"); err != nil {
		t.Fatalf("CloseSession unknown session: %v", err)
	}
	_ = serverWrite.Close()
	_ = client.Close()
}

func TestStartRequiresLauncher(t *testing.T) {
	if _, err := Start(context.Background(), Config{Command: []string{"/bin/true"}}); err == nil {
		t.Fatal("Start accepted a config without a launcher")
	}
}
