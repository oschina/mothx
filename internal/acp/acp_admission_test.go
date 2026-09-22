package acp

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestAcquirePromptAdmissionRecoversDurableOrphan(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	now := time.Now()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "existing-run", SessionID: mgr.GetHeader().ID, Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create active run: %v", err)
	}

	s := &server{settings: &config.Settings{SessionDir: sessionDir}}
	release, err := s.acquirePromptAdmission(&sessionRuntime{id: mgr.GetHeader().ID})
	if err != nil {
		t.Fatalf("admission error = %v", err)
	}
	defer release()
	run, err := session.GetSessionRun(sessionDir, "existing-run")
	if err != nil || run == nil || run.Status != "failed" {
		t.Fatalf("recovered run = %#v, err=%v", run, err)
	}
}

func TestAcquirePromptAdmissionHoldsSharedRuntimeLock(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	s := &server{settings: &config.Settings{SessionDir: sessionDir}}
	rt := &sessionRuntime{id: mgr.GetHeader().ID}
	release, err := s.acquirePromptAdmission(rt)
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	defer release()
	if _, err := s.acquirePromptAdmission(rt); err != errACPActiveSessionRun {
		t.Fatalf("second admission error = %v, want %v", err, errACPActiveSessionRun)
	}
	if rt.cancel != nil {
		t.Fatal("admission unexpectedly installed a local cancel function")
	}
}
