package acp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/session"
)

// deliveryManageFixture builds a fixture server whose negotiated settings point
// at an isolated session database that already contains one failed delivery
// operation waiting for operator attention.
func deliveryManageFixture(t *testing.T) (*server, *syncedBuffer, string, string) {
	t.Helper()
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	mgr := session.New(workDir, sessionDir)
	if err := mgr.InitWithID("acp-delivery-session"); err != nil {
		t.Fatal(err)
	}
	sessionID := mgr.GetHeader().ID
	started := time.Now().UTC()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{ID: "acp-delivery-run", SessionID: sessionID, Status: "completed", StartedAt: started, UpdatedAt: started, FinishedAt: &started}); err != nil {
		t.Fatal(err)
	}
	plan := session.DeliveryPlan{
		Intent: session.DeliveryIntent{ID: "acp-delivery-intent", SessionID: sessionID, RunID: "acp-delivery-run", Platform: "wechat", TargetID: "chat", Status: "pending", CreatedAt: started, UpdatedAt: started},
		Operations: []session.DeliveryOperation{{
			ID: "acp-delivery-op", IntentID: "acp-delivery-intent", OperationKey: "caption", OperationKind: "send_text",
			Sequence: 1, IdempotencyKey: "acp-delivery-op", PayloadDigest: "sha256:x", Status: "pending", CreatedAt: started, UpdatedAt: started,
		}},
	}
	if err := session.CreateDeliveryPlan(context.Background(), sessionDir, plan); err != nil {
		t.Fatal(err)
	}
	claimed, err := session.ClaimDeliveryOperation(context.Background(), sessionDir, "acp-delivery-op", "acp-worker", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateDeliveryOperation(context.Background(), sessionDir, "acp-delivery-op", "acp-worker", claimed.LeaseEpoch, "failed", "", "", nil, "delivery_retries_exhausted", nil); err != nil {
		t.Fatal(err)
	}

	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, workDir)
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	srv.settings = settings
	return srv, output, sessionDir, sessionID
}

// TestManageDeliveriesListProjectsFailures guards the Desktop-facing retry
// entry: failed transport deliveries must be discoverable with an explicit
// retryable flag and no payload/provider internals.
func TestManageDeliveriesListProjectsFailures(t *testing.T) {
	srv, output, _, sessionID := deliveryManageFixture(t)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/deliveries/list", map[string]any{"sessionId": sessionID}))
	deliveries, ok := result["deliveries"].([]any)
	if !ok || len(deliveries) != 1 {
		t.Fatalf("deliveries = %#v, want one entry", result["deliveries"])
	}
	entry, _ := deliveries[0].(map[string]any)
	if entry["operationId"] != "acp-delivery-op" || entry["platform"] != "wechat" || entry["failureCode"] != "delivery_retries_exhausted" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry["retryable"] != true {
		t.Fatalf("retryable = %#v, want true for a transport-level failure", entry["retryable"])
	}
	for _, forbidden := range []string{"payload", "providerState", "transportContext"} {
		if _, exists := entry[forbidden]; exists {
			t.Fatalf("delivery projection leaked %q: %#v", forbidden, entry)
		}
	}
}

// TestManageDeliveriesRetryReopensAndClearsTheFailure guards the retry action:
// a successful retry removes the operation from the failure projection.
func TestManageDeliveriesRetryReopensAndClearsTheFailure(t *testing.T) {
	srv, output, _, sessionID := deliveryManageFixture(t)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 2, "mothx/manage/deliveries/retry", map[string]any{"operationId": "acp-delivery-op"}))
	if result["retried"] != true {
		t.Fatalf("retry result = %#v, want retried=true", result)
	}

	result = manageFixtureResult(t, callManageFixture(t, srv, output, 3, "mothx/manage/deliveries/list", map[string]any{"sessionId": sessionID}))
	if count, _ := result["count"].(float64); count != 0 {
		t.Fatalf("failure count after retry = %v, want 0", result["count"])
	}

	// Unknown operations and missing params are structured errors.
	message := callManageFixture(t, srv, output, 4, "mothx/manage/deliveries/retry", map[string]any{})
	if message["error"] == nil {
		t.Fatalf("retry without operationId = %#v, want an error", message)
	}
}
