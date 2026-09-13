package openaiapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

// deliveryAPI fixture: one session with a single delivery operation that can be
// left pending or marked with a chosen failure.
func deliveryAPIFixture(t *testing.T, failureCode, status string) (*Server, string) {
	t.Helper()
	srv := newTestServer(t)
	workDir := filepath.Dir(srv.settings.GetSessionDir())
	mgr := session.New(workDir, srv.settings.GetSessionDir())
	if err := mgr.InitWithID("serve-delivery-session"); err != nil {
		t.Fatal(err)
	}
	sessionDir := srv.settings.GetSessionDir()
	sessionID := mgr.GetHeader().ID
	started := time.Now().UTC()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "serve-delivery-run", SessionID: sessionID, Status: "completed",
		StartedAt: started, UpdatedAt: started, FinishedAt: &started,
	}); err != nil {
		t.Fatal(err)
	}
	plan := session.DeliveryPlan{
		Intent: session.DeliveryIntent{
			ID: "serve-delivery-intent", SessionID: sessionID, RunID: "serve-delivery-run",
			Platform: "wechat", TargetID: "chat", Status: "pending", CreatedAt: started, UpdatedAt: started,
		},
		Operations: []session.DeliveryOperation{{
			ID: "serve-delivery-op", IntentID: "serve-delivery-intent", OperationKey: "caption", OperationKind: "send_text",
			Sequence: 1, IdempotencyKey: "serve-delivery-op", PayloadDigest: "sha256:x", Status: "pending", CreatedAt: started, UpdatedAt: started,
		}},
	}
	if err := session.CreateDeliveryPlan(context.Background(), sessionDir, plan); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		claimed, err := session.ClaimDeliveryOperation(context.Background(), sessionDir, "serve-delivery-op", "serve-worker", time.Now().UTC(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := session.UpdateDeliveryOperation(context.Background(), sessionDir, "serve-delivery-op", "serve-worker", claimed.LeaseEpoch, status, "", "", nil, failureCode, nil); err != nil {
			t.Fatal(err)
		}
	}
	return srv, sessionID
}

func deliveryGET(t *testing.T, srv *Server, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	srv.HandleDeliveryFailuresAPI(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	body := map[string]any{}
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list response: %v", err)
		}
	}
	return recorder, body
}

func deliveryPOST(t *testing.T, srv *Server, payload string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	srv.HandleDeliveryRetryAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/deliveries/retry", strings.NewReader(payload)))
	body := map[string]any{}
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode retry response: %v", err)
		}
	}
	return recorder, body
}

// TestDeliveryFailuresAPIListsSessionFailures guards the WebUI projection: a
// transport-level failure is reported with the shared retryable verdict, a
// permanent one is not, and the session filter is honored.
func TestDeliveryFailuresAPIListsSessionFailures(t *testing.T) {
	srv, sessionID := deliveryAPIFixture(t, "transport_error", "failed")

	recorder, body := deliveryGET(t, srv, "/api/deliveries/failures?session_id="+sessionID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	deliveries, _ := body["deliveries"].([]any)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %#v, want one failure", body["deliveries"])
	}
	item, _ := deliveries[0].(map[string]any)
	for key, want := range map[string]any{
		"operationId": "serve-delivery-op",
		"platform":    "wechat",
		"status":      "failed",
		"failureCode": "transport_error",
		"retryable":   true,
	} {
		if got := item[key]; got != want {
			t.Fatalf("%s = %#v, want %#v", key, got, want)
		}
	}

	// A different session has no failures of its own.
	recorder, body = deliveryGET(t, srv, "/api/deliveries/failures?session_id=other-session")
	if recorder.Code != http.StatusOK {
		t.Fatalf("other session status = %d, want 200", recorder.Code)
	}
	if count, _ := body["count"].(float64); count != 0 {
		t.Fatalf("other session count = %v, want 0", body["count"])
	}

	// An invalid limit is rejected instead of silently clamped by the handler.
	recorder, _ = deliveryGET(t, srv, "/api/deliveries/failures?limit=-1")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("negative limit status = %d, want 400", recorder.Code)
	}
}

// TestDeliveryFailuresAPIMarksPermanentFailuresNotRetryable guards the verdict
// the WebUI renders: a permanent platform failure must not look retryable.
func TestDeliveryFailuresAPIMarksPermanentFailuresNotRetryable(t *testing.T) {
	srv, _ := deliveryAPIFixture(t, "unsupported_media_kind", "failed")

	recorder, body := deliveryGET(t, srv, "/api/deliveries/failures")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	deliveries, _ := body["deliveries"].([]any)
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %#v, want one failure", body["deliveries"])
	}
	item, _ := deliveries[0].(map[string]any)
	if item["retryable"] != false {
		t.Fatalf("retryable = %#v, want false for a permanent failure", item["retryable"])
	}
}

// TestDeliveryRetryAPIReopensOnlyFailedTransportOperations guards the WebUI
// operator entry against its own projection.
func TestDeliveryRetryAPIReopensOnlyFailedTransportOperations(t *testing.T) {
	srv, _ := deliveryAPIFixture(t, "transport_error", "failed")

	recorder, body := deliveryPOST(t, srv, `{"operationId":"serve-delivery-op"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	if body["retried"] != true {
		t.Fatalf("retried = %#v, want true", body["retried"])
	}
	operation, err := session.GetDeliveryOperation(context.Background(), srv.settings.GetSessionDir(), "serve-delivery-op")
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != "retry_wait" {
		t.Fatalf("operation status = %q, want retry_wait after a successful retry", operation.Status)
	}

	// A permanent failure is refused and stays failed.
	permanentSrv, _ := deliveryAPIFixture(t, "unsupported_media_kind", "failed")
	recorder, _ = deliveryPOST(t, permanentSrv, `{"operationId":"serve-delivery-op"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("permanent failure status = %d, want 409", recorder.Code)
	}
	permanent, err := session.GetDeliveryOperation(context.Background(), permanentSrv.settings.GetSessionDir(), "serve-delivery-op")
	if err != nil {
		t.Fatal(err)
	}
	if permanent.Status != "failed" || permanent.FailureCode != "unsupported_media_kind" {
		t.Fatalf("permanent operation = %#v, want the original failure", permanent)
	}

	// A pending (in-flight) operation must not be clobbered back into retry_wait.
	pendingSrv, _ := deliveryAPIFixture(t, "", "")
	recorder, _ = deliveryPOST(t, pendingSrv, `{"operationId":"serve-delivery-op"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("pending operation status = %d, want 409", recorder.Code)
	}

	// An unknown operation is reported as missing.
	recorder, _ = deliveryPOST(t, srv, `{"operationId":"missing-operation"}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown operation status = %d, want 404", recorder.Code)
	}
}

// TestDeliveryAPIRoutesAreRegistered guards the mux wiring: the WebUI calls the
// endpoints by path, so a missing registration would only surface in the browser.
func TestDeliveryAPIRoutesAreRegistered(t *testing.T) {
	srv, sessionID := deliveryAPIFixture(t, "transport_error", "failed")
	mux := http.NewServeMux()
	registerRoutes(mux, srv, RunOptions{})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/deliveries/failures?session_id="+sessionID, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list route status = %d, want 200 (%s)", recorder.Code, recorder.Body.String())
	}
	retryRecorder := httptest.NewRecorder()
	mux.ServeHTTP(retryRecorder, httptest.NewRequest(http.MethodPost, "/api/deliveries/retry", strings.NewReader(`{"operationId":"serve-delivery-op"}`)))
	if retryRecorder.Code != http.StatusOK {
		t.Fatalf("retry route status = %d, want 200 (%s)", retryRecorder.Code, retryRecorder.Body.String())
	}
}

// TestDeliveryAPIMethodAndPayloadValidation guards the endpoint contract.
func TestDeliveryAPIMethodAndPayloadValidation(t *testing.T) {
	srv, _ := deliveryAPIFixture(t, "transport_error", "failed")

	listRecorder := httptest.NewRecorder()
	srv.HandleDeliveryFailuresAPI(listRecorder, httptest.NewRequest(http.MethodPost, "/api/deliveries/failures", nil))
	if listRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("list POST status = %d, want 405", listRecorder.Code)
	}
	retryRecorder := httptest.NewRecorder()
	srv.HandleDeliveryRetryAPI(retryRecorder, httptest.NewRequest(http.MethodGet, "/api/deliveries/retry", nil))
	if retryRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retry GET status = %d, want 405", retryRecorder.Code)
	}
	for _, payload := range []string{`{}`, `{"operationId":"   "}`, `not-json`} {
		recorder, _ := deliveryPOST(t, srv, payload)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("payload %q status = %d, want 400", payload, recorder.Code)
		}
	}
}
