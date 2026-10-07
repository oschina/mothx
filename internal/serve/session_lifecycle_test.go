package serve

import (
	"context"
	"fmt"
	"testing"
	"time"

	channels "github.com/oschina/mothx/internal/serve/channels"
	"github.com/oschina/mothx/internal/session"
)

type lifecycleTestSessions struct {
	deletedID string
	deleted   bool
	err       error
}

func (s *lifecycleTestSessions) DeleteActiveSession(id string) (bool, error) {
	s.deletedID = id
	return s.deleted, s.err
}

func TestSessionLifecycleDeleteRejectsBoundSession(t *testing.T) {
	sessionDir := t.TempDir()
	mgr, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "identity-1")
	if err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleTestSessions{deleted: true}
	service := NewSessionLifecycleService(fake, nil, sessionDir, nil)
	deleted, err := service.Delete(context.Background(), mgr.GetHeader().ID)
	if deleted || fake.deletedID != "" {
		t.Fatal("bound delete reached the persistence primitive")
	}
	conflict, ok := err.(*lifecycleConflict)
	if !ok || conflict.Code != "session_bound" {
		t.Fatalf("error = %#v, want session_bound conflict", err)
	}
}

func TestSessionLifecycleDeleteRejectsRuntimeLockedSession(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("locked-session"); err != nil {
		t.Fatal(err)
	}
	release := session.LockRuntime(sessionDir, "locked-session")
	defer release()
	fake := &lifecycleTestSessions{deleted: true}
	service := NewSessionLifecycleService(fake, nil, sessionDir, nil)
	deleted, err := service.Delete(context.Background(), "locked-session")
	if deleted || fake.deletedID != "" {
		t.Fatal("runtime-locked delete reached the persistence primitive")
	}
	conflict, ok := err.(*lifecycleConflict)
	if !ok || conflict.Code != "session_running" {
		t.Fatalf("error = %#v, want session_running conflict", err)
	}
}

func TestSessionLifecycleDeletePreservesStateWhenPoolDeleteFails(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("delete-failure"); err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleTestSessions{deleted: false, err: fmt.Errorf("pool delete failed")}
	service := NewSessionLifecycleService(fake, nil, sessionDir, nil)
	if deleted, err := service.Delete(context.Background(), "delete-failure"); deleted || err == nil {
		t.Fatalf("delete result = %v/%v, want failure", deleted, err)
	}
	if _, err := session.OpenByIDExact(sessionDir, "delete-failure"); err != nil {
		t.Fatalf("session disappeared after pool failure: %v", err)
	}
}

func TestSessionLifecycleRotateUsesSharedBindingBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	old, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "rotate-identity")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &channels.Dispatcher{}
	service := NewSessionLifecycleService(nil, dispatcher, sessionDir, session.NewIdentityLocks())
	var eventType string
	var eventData map[string]any
	service.SetEventPublisher(func(kind string, data any) {
		eventType = kind
		eventData, _ = data.(map[string]any)
	})
	if err := service.Rotate(context.Background(), "wechat", "rotate-identity", false); err != nil {
		t.Fatal(err)
	}
	binding, err := session.FindBinding(sessionDir, "wechat", "rotate-identity")
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.SessionID == old.GetHeader().ID {
		t.Fatalf("rotated binding = %#v, old = %s", binding, old.GetHeader().ID)
	}
	if eventType != "binding_changed" || eventData["fromSessionId"] != old.GetHeader().ID {
		t.Fatalf("rotation event = %s %#v", eventType, eventData)
	}
}

func TestSessionLifecycleRotateForcePastBusyRun(t *testing.T) {
	sessionDir := t.TempDir()
	old, err := session.CreateBound(t.TempDir(), sessionDir, "wechat", "force-rotate")
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &channels.Dispatcher{}
	service := NewSessionLifecycleService(nil, dispatcher, sessionDir, session.NewIdentityLocks())

	release := session.LockRuntime(sessionDir, old.GetHeader().ID)

	// Without force, a busy runtime lock is a conflict.
	if err := service.Rotate(context.Background(), "wechat", "force-rotate", false); err == nil {
		t.Fatal("expected session_running conflict for busy session")
	}
	binding, _ := session.FindBinding(sessionDir, "wechat", "force-rotate")
	if binding == nil || binding.SessionID != old.GetHeader().ID {
		t.Fatal("non-forced rotate must not touch a busy binding")
	}

	// With force, the rotation waits for the lock and then proceeds.
	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()
	if err := service.Rotate(context.Background(), "wechat", "force-rotate", true); err != nil {
		t.Fatalf("forced rotate: %v", err)
	}
	binding, _ = session.FindBinding(sessionDir, "wechat", "force-rotate")
	if binding == nil || binding.SessionID == old.GetHeader().ID {
		t.Fatal("forced rotate did not rebind the identity")
	}
}

// TestSessionLifecycleDeleteRecoversOrphanRun pins the unified ownership
// policy: deleting a session whose previous owner crashed (active run row, no
// live lease) must reconcile the orphan through the shared Runtime recovery
// instead of failing with a permanent session_running conflict.
func TestSessionLifecycleDeleteRecoversOrphanRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("orphan-delete"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := session.SaveSessionRun(sessionDir, session.SessionRun{
		ID: "orphan-run", SessionID: "orphan-delete", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	fake := &lifecycleTestSessions{deleted: true}
	service := NewSessionLifecycleService(fake, nil, sessionDir, nil)
	deleted, err := service.Delete(context.Background(), "orphan-delete")
	if err != nil || !deleted {
		t.Fatalf("delete with orphan run = %v/%v, want success via shared recovery", deleted, err)
	}
	if fake.deletedID != "orphan-delete" {
		t.Fatalf("deleted id = %q, want orphan-delete", fake.deletedID)
	}
	run, err := session.GetSessionRun(sessionDir, "orphan-run")
	if err != nil || run == nil || run.Status != "failed" {
		t.Fatalf("orphan run = %#v, err=%v, want terminalized failed", run, err)
	}
}
