package tui

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

func TestSetRuntimeSynchronizesTUIResourceAliases(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	mgr := session.New(workDir, sessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(workDir, nil)
	sandboxMgr := sandbox.NewManager(workDir)
	skillsMgr := skills.NewManager(t.TempDir(), t.TempDir())
	runtime := &agentruntime.SessionRuntime{
		ID: mgr.GetHeader().ID, Source: agentruntime.SourceTUI, WorkDir: workDir,
		Manager: mgr, Registry: registry, SandboxMgr: sandboxMgr, SkillsMgr: skillsMgr,
		ExtraContext: "runtime context", RuleContent: "runtime rules",
	}
	app := &App{}
	app.SetRuntime(runtime)
	if app.runtime != runtime || app.session != mgr || app.registry != registry || app.skillsMgr != skillsMgr {
		t.Fatalf("runtime aliases not synchronized: %#v", app)
	}
	if app.extraContext != "runtime context" || app.baseExtraContext != "runtime context" || app.ruleContent != "runtime rules" {
		t.Fatalf("runtime context not synchronized: extra=%q base=%q rules=%q", app.extraContext, app.baseExtraContext, app.ruleContent)
	}
}
