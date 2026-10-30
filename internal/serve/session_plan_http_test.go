package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/serve/openaiapi"
)

type planUpdateSessionManager struct {
	*fakeActiveSessionManager
	planID   string
	plan     openaiapi.SessionTaskPlan
	planErr  error
	returned *openaiapi.SessionMessageEntry
}

func (f *planUpdateSessionManager) UpdateSessionPlan(_ context.Context, id string, plan openaiapi.SessionTaskPlan) (openaiapi.SessionTaskPlan, *openaiapi.SessionMessageEntry, error) {
	if f.planErr != nil {
		return openaiapi.SessionTaskPlan{}, nil, f.planErr
	}
	f.planID = id
	f.plan = plan
	if f.returned == nil {
		f.returned = &openaiapi.SessionMessageEntry{Role: "toolCall", ToolCallID: "ui-plan-test", ToolName: "plan", Plan: &plan}
	}
	return plan, f.returned, nil
}

func TestSessionPlanHTTPRoute(t *testing.T) {
	manager := &planUpdateSessionManager{fakeActiveSessionManager: &fakeActiveSessionManager{}}
	rt := &channelRuntime{}
	handler := rt.handleSessionByID(manager)

	body := `{"title":"Edited plan","steps":[{"title":"First","status":"done"},{"title":"Second","status":"pending"}],"note":"from UI"}`
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/s1/plan", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if manager.planID != "s1" || manager.plan.Title != "Edited plan" || len(manager.plan.Steps) != 2 {
		t.Fatalf("forwarded plan = %#v (session %q)", manager.plan, manager.planID)
	}
	var response struct {
		SessionID string                         `json:"sessionId"`
		Plan      openaiapi.SessionTaskPlan      `json:"plan"`
		Entry     *openaiapi.SessionMessageEntry `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SessionID != "s1" || response.Entry == nil || response.Entry.Plan == nil {
		t.Fatalf("response = %#v", response)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/sessions/s1/plan", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestSessionPlanHTTPErrorMapping(t *testing.T) {
	manager := &planUpdateSessionManager{fakeActiveSessionManager: &fakeActiveSessionManager{}}
	rt := &channelRuntime{}
	handler := rt.handleSessionByID(manager)
	body := `{"steps":[{"title":"First","status":"pending"}]}`

	manager.planErr = openaiapi.ErrSessionPlanBusy
	req := httptest.NewRequest(http.MethodPut, "/api/sessions/s1/plan", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("busy status = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	manager.planErr = errors.Join(openaiapi.ErrSessionPlanInvalid, errors.New("step 1 is empty"))
	req = httptest.NewRequest(http.MethodPut, "/api/sessions/s1/plan", strings.NewReader(body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	manager.planErr = openaiapi.ErrSessionNotFound
	req = httptest.NewRequest(http.MethodPut, "/api/sessions/missing/plan", strings.NewReader(body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
