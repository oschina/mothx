package serve

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/messaging"
	"github.com/oschina/mothx/internal/session"
)

type recoveryPlatform struct {
	mu       sync.Mutex
	requests []messaging.DurableDeliveryRequest
}

func (p *recoveryPlatform) Name() string { return "wechat" }

func (p *recoveryPlatform) Start(context.Context, messaging.MessageHandler) error { return nil }

func (p *recoveryPlatform) Stop() error { return nil }

func (p *recoveryPlatform) SendMessage(context.Context, string, string) error { return nil }

func (p *recoveryPlatform) IsConnected() bool { return true }

func (p *recoveryPlatform) ExecuteDurableDelivery(_ context.Context, request messaging.DurableDeliveryRequest) (messaging.DurableDeliveryResult, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return messaging.DurableDeliveryResult{Status: "delivered", ProviderMessageID: "provider-message"}, nil
}

func TestReconcileDurableDeliveriesReplaysFrozenCaption(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("recovery-session"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{ID: "recovery-run", SessionID: mgr.GetHeader().ID, Status: "completed", StartedAt: now, UpdatedAt: now, FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := session.CreateDeliveryPlan(t.Context(), sessionDir, session.DeliveryPlan{
		Intent:     session.DeliveryIntent{ID: "recovery-intent", SessionID: mgr.GetHeader().ID, RunID: "recovery-run", Platform: "wechat", TargetID: "chat", TransportContext: []byte(`{"caption":"resume this reply","replyContext":"ctx-1"}`), Status: "pending", CreatedAt: now, UpdatedAt: now},
		Operations: []session.DeliveryOperation{{ID: "recovery-op", OperationKey: "caption", OperationKind: "send_text", Sequence: 1, IdempotencyKey: "recovery-op", PayloadDigest: "sha256:caption", Status: "pending", CreatedAt: now, UpdatedAt: now}},
	}); err != nil {
		t.Fatal(err)
	}
	platform := &recoveryPlatform{}
	rt := &channelRuntime{sessionDir: sessionDir, platforms: NewPlatformSupervisor()}
	rt.platforms.Replace("wechat", platform)
	rt.reconcileDurableDeliveries(t.Context())
	operation, err := session.GetDeliveryOperation(t.Context(), sessionDir, "recovery-op")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "delivered" || operation.ProviderMessageID != "provider-message" {
		t.Fatalf("recovered operation = %#v", operation)
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.requests) != 1 || platform.requests[0].Caption != "resume this reply" || platform.requests[0].Intent.TargetID != "chat" {
		t.Fatalf("recovery request = %#v", platform.requests)
	}
}

func TestDeliveryIntentPayloadSelectsFallback(t *testing.T) {
	if got := agentruntime.DeliveryOperationText([]byte(`{"caption":"caption","fallback":"fallback"}`), "send_fallback_text"); got != "fallback" {
		t.Fatalf("fallback payload = %q", got)
	}
	if got := agentruntime.DeliveryOperationText([]byte(`{"caption":"caption","fallback":"fallback"}`), "send_text"); got != "caption" {
		t.Fatalf("caption payload = %q", got)
	}
	if got := agentruntime.DeliveryOperationText([]byte(`not-json`), "send_text"); got != "" {
		t.Fatalf("invalid payload = %q", got)
	}
}

func TestDeliveryRecoveryRequestRejectsMissingPlan(t *testing.T) {
	rt := &channelRuntime{sessionDir: t.TempDir()}
	_, err := rt.deliveryRecoveryRequest(context.Background(), session.DeliveryOperation{IntentID: "missing"})
	if !errors.Is(err, session.ErrDeliveryOperationAbsent) {
		t.Fatalf("missing plan error = %v", err)
	}
}
