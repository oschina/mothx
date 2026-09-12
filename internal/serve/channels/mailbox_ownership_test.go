package channels

import (
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/serve/hooks"
	"github.com/startvibecoding/mothx/internal/session"
)

// TestChannelSessionRespectsPartialSubAgentToolSelection guards the explicit
// per-tool switch on channels: re-pointing the sub-agent tools at the
// session-scoped manager must not resurrect a tool the user switched off. The
// team path stays authoritative separately.
func TestChannelSessionRespectsPartialSubAgentToolSelection(t *testing.T) {
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
	binding, err := session.CreateBound(workDir, settings.SessionDir, "wechat", "partial-tools")
	if err != nil {
		t.Fatal(err)
	}
	// Only the spawn tool is switched on; every other sub-agent tool is explicitly
	// off.
	selection := []session.ChannelToolConfig{{ToolName: "subagent_spawn", Enabled: true}}
	for _, name := range agent.SubAgentToolNames() {
		if name != "subagent_spawn" {
			selection = append(selection, session.ChannelToolConfig{ToolName: name, Enabled: false})
		}
	}
	if err := session.SetChannelTools(settings.SessionDir, binding.GetHeader().ID, selection); err != nil {
		t.Fatal(err)
	}
	sess, err := d.resolveSession("wechat", "partial-tools")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sess.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("explicitly enabled subagent_spawn is not registered")
	}
	if sess.AgentMgr == nil || sess.AgentMgr.Mailbox == nil {
		t.Fatal("session-scoped manager did not own the session mailbox")
	}
	for _, name := range agent.SubAgentToolNames() {
		if name == "subagent_spawn" {
			continue
		}
		if _, ok := sess.Registry.Get(name); ok {
			t.Fatalf("%s was explicitly disabled but the session registry kept it", name)
		}
	}
}

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
