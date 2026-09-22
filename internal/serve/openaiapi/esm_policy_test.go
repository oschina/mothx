package openaiapi

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestESMBoundSessionUsesPersistedRuntimePolicy(t *testing.T) {
	server := newTestServer(t)
	defer server.pool.Stop()

	bound, err := session.CreateBound(server.cfg.GetWorkDir(), server.settings.GetSessionDir(), "wechat", "esm-policy-user")
	if err != nil {
		t.Fatalf("CreateBound: %v", err)
	}
	sess, err := server.getOrCreateSession(bound.GetHeader().ID, server.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	sess.Mode = "agent"
	source, mode, err := server.resolveESMRuntimePolicy(sess)
	if err != nil {
		t.Fatalf("resolveESMRuntimePolicy: %v", err)
	}
	if source != "wechat" || mode != "yolo" {
		t.Fatalf("ESM source/mode = %q/%q, want wechat/yolo", source, mode)
	}
}
