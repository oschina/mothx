package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// suppressESMCoordinator keeps the background ESM coordinator from starting so a
// control test does not race its objective loop for the session's runtime lease.
// The coordinator's own behavior has dedicated tests; these tests pin the
// control API's contract, which must be deterministic.
func suppressESMCoordinator(srv *Server) {
	srv.esmCoordinator = &esmCoordinator{closed: true}
}

// seedESMObjective persists an objective directly through the store, without the
// HTTP create path's coordinator, so the following control assertions start from
// a quiet session.
func seedESMObjective(t *testing.T, srv *Server, id, objective string) {
	t.Helper()
	if _, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.esmStore().Create(context.Background(), id, objective); err != nil {
		t.Fatal(err)
	}
}

func TestHandleESMAPICreateAcceptsLegacyPayload(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	id := "webui-esm"
	if _, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/esm", strings.NewReader(`{"objective":"run tests","tokenBudget":100}`))
	rec := httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created ESMSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "active" || created.Objective != "run tests" {
		t.Fatalf("created=%#v", created)
	}
	// tokenBudget is no longer part of the ESM contract; legacy payloads must
	// be accepted and ignored.
	if created.TokensUsed != 0 || created.TimeUsedMS != 0 {
		t.Fatalf("usage fields should start at zero: %#v", created)
	}
}

func TestHandleESMAPIControlLifecycle(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	suppressESMCoordinator(srv)
	id := "webui-esm-control"
	seedESMObjective(t, srv, id, "run tests")

	current, err := srv.GetESM(id)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/esm/guidance", strings.NewReader(`{"guidance":"prioritize focused tests","version":"`+current.Version+`"}`))
	rec := httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("guidance status=%d body=%s", rec.Code, rec.Body.String())
	}
	var guided ESMSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &guided); err != nil {
		t.Fatal(err)
	}
	if len(guided.Guidance) != 1 || guided.Guidance[0].Guidance != "prioritize focused tests" {
		t.Fatalf("guided=%#v", guided.Guidance)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/esm/pause", nil)
	rec = httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/esm/resume", nil)
	rec = httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/sessions/"+id+"/esm", nil)
	rec = httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cleared ESMSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if cleared.Status != "none" {
		t.Fatalf("cleared=%#v", cleared)
	}
}

func TestESMLifecycleControlsRejectActiveForegroundRun(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	suppressESMCoordinator(srv)
	const id = "webui-esm-active-control"
	seedESMObjective(t, srv, id, "finish the objective")
	sess, err := srv.pool.getExact(id)
	if err != nil || sess == nil {
		t.Fatalf("session = %#v, err = %v", sess, err)
	}
	sess.SetRunning(true)
	t.Cleanup(func() { sess.SetRunning(false) })

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/esm/pause", nil)
	rec := httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("active-run pause HTTP status=%d body=%s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
	}

	if _, err := srv.PauseESM(id); !errors.Is(err, ErrESMControlRequiresIdle) {
		t.Fatalf("PauseESM error = %v, want ErrESMControlRequiresIdle", err)
	}
	if _, err := srv.ResumeESM(id); !errors.Is(err, ErrESMControlRequiresIdle) {
		t.Fatalf("ResumeESM error = %v, want ErrESMControlRequiresIdle", err)
	}
	if err := srv.ClearESM(id); !errors.Is(err, ErrESMControlRequiresIdle) {
		t.Fatalf("ClearESM error = %v, want ErrESMControlRequiresIdle", err)
	}
	obj, err := srv.esmStore().Get(context.Background(), id)
	if err != nil || obj.Status != "active" {
		t.Fatalf("lifecycle control changed active objective: obj=%#v err=%v", obj, err)
	}
}

func TestHandleESMAPIRejectsStaleVersion(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	id := "webui-esm-version"
	if _, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir()); err != nil {
		t.Fatal(err)
	}
	created, err := srv.CreateESM(id, "old")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+id+"/esm", strings.NewReader(`{"objective":"new","version":"stale"}`))
	rec := httptest.NewRecorder()
	srv.HandleESMAPI(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s created=%#v", rec.Code, rec.Body.String(), created)
	}
}
