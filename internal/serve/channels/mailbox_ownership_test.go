package channels

import (
	"testing"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/serve/hooks"
	"github.com/startvibecoding/mothx/internal/session"
)

// TestChannelSessionToolsShareTheSessionMailbox guards the N8 wiring at the
// channel boundary: the session-scoped manager owns the session mailbox, so a
// member question queued through the very manager that owns the sub-agent tools
// is visible on the same mailbox the lead's build path drains. Attaching those
// tools to the dispatcher-wide manager instead would leave member questions with
// no wake path (and subagent_wait blind) in ordinary channel multi-agent mode.
func TestChannelSessionToolsShareTheSessionMailbox(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.MultiAgent = true
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		multiAgent: true, sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	sess, err := d.resolveSession("wechat", "mailbox-owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sess.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("multi-agent channel session did not register subagent_spawn")
	}
	if sess.AgentMgr == nil {
		t.Fatal("channel session has no session-scoped agent manager")
	}
	if sess.AgentMgr.Mailbox == nil || sess.Runtime == nil || sess.AgentMgr.Mailbox != sess.Runtime.Mailbox {
		t.Fatal("sub-agent tools and the lead do not share one session mailbox")
	}
	sess.AgentMgr.NotifyMemberQuestion("member-1", "Engineer", "question-member-1-1", "Which environment?", nil)
	if !sess.AgentMgr.Mailbox.HasPending() {
		t.Fatal("member question was dropped before the lead could see it")
	}
}
