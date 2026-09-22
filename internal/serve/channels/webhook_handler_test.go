package channels

import (
	"context"
	"testing"

	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/serve/webhook"
)

func TestWebhookHandlerRequiresMultiAgent(t *testing.T) {
	d := &Dispatcher{agentMgr: nil}
	h := NewWebhookHandler(d, nil)

	route := webhook.RouteConfig{Path: "/test", Skill: "test"}
	err := h.HandleWebhookEvent(nil, route, []byte(`{}`))
	if err == nil {
		t.Error("expected error when agentMgr is nil")
	}
}

func TestWebhookHandlerDeliverResultUsesTarget(t *testing.T) {
	platform := &mockPlatform{}
	h := NewWebhookHandler(nil, map[string]messaging.Platform{
		"feishu": platform,
	})

	h.deliverResult("feishu", "chat_123", "done")

	if platform.chatID != "chat_123" {
		t.Fatalf("chatID = %q, want chat_123", platform.chatID)
	}
	if platform.text != "done" {
		t.Fatalf("text = %q, want done", platform.text)
	}
}

func TestWebhookHandlerDeliverResultRequiresTarget(t *testing.T) {
	platform := &mockPlatform{}
	h := NewWebhookHandler(nil, map[string]messaging.Platform{
		"feishu": platform,
	})

	h.deliverResult("feishu", "", "done")

	if platform.called {
		t.Fatal("expected SendMessage not to be called without delivery target")
	}
}

type mockPlatform struct {
	called bool
	chatID string
	text   string
}

func (p *mockPlatform) Name() string { return "mock" }

func (p *mockPlatform) Start(ctx context.Context, handler messaging.MessageHandler) error { return nil }

func (p *mockPlatform) Stop() error { return nil }

func (p *mockPlatform) SendMessage(ctx context.Context, chatID string, text string) error {
	p.called = true
	p.chatID = chatID
	p.text = text
	return nil
}

func (p *mockPlatform) IsConnected() bool { return true }
