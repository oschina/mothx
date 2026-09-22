package openaiapi

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestBoundChannelSessionAlwaysResolvesYolo(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	bound, err := session.CreateBound(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir(), "wechat", "mode-policy-user")
	if err != nil {
		t.Fatalf("CreateBound: %v", err)
	}
	sess, err := srv.getOrCreateSession(bound.GetHeader().ID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if sess.Runtime == nil || sess.Runtime.Registry != sess.Registry || sess.Runtime.Manager != sess.Manager {
		t.Fatalf("restored session is not backed by shared runtime: %#v", sess.Runtime)
	}
	if sess.Mode != "yolo" {
		t.Fatalf("restored bound session mode = %q, want yolo", sess.Mode)
	}

	for _, requested := range []string{"", "plan", "agent", "yolo", "os"} {
		got, err := srv.resolveSessionMode(sess, requested)
		if err != nil {
			t.Fatalf("resolveSessionMode(%q): %v", requested, err)
		}
		if got != "yolo" {
			t.Fatalf("resolveSessionMode(%q) = %q, want yolo", requested, got)
		}
	}

	mode := "agent"
	caps, err := srv.PatchSessionCapabilities(sess.ID, SessionCapabilityPatch{Mode: &mode})
	if err != nil {
		t.Fatalf("PatchSessionCapabilities: %v", err)
	}
	if caps.Mode != "yolo" || sess.Mode != "yolo" {
		t.Fatalf("bound mode patch = caps %q, session %q; both want yolo", caps.Mode, sess.Mode)
	}
}

func TestUnboundSessionStillUsesAPIDefaultMode(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.cfg.DefaultMode = "agent"

	sess, err := srv.getOrCreateSession("local-mode-policy", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if sess.Runtime == nil {
		t.Fatal("new API session is not backed by a shared runtime")
	}
	got, err := srv.resolveSessionMode(sess, "")
	if err != nil {
		t.Fatalf("resolveSessionMode: %v", err)
	}
	if got != "agent" {
		t.Fatalf("unbound effective mode = %q, want agent", got)
	}
}
