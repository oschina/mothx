package agentruntime

import (
	"context"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestBindSessionRehydratesBoundExpertResources(t *testing.T) {
	workDir := t.TempDir()
	manager, err := CreateSession(CreateSessionOptions{WorkDir: workDir, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := manager.SetExpertBinding("frontend-developer"); err != nil {
		t.Fatalf("bind expert: %v", err)
	}

	// WebUI creates a reusable runtime before it has opened the persisted
	// session. The late binding must load both the expert identity and its
	// package skills through the same Runtime-owned resource path.
	runtime, err := testBuilder().Build(context.Background(), BuildOptions{WorkDir: workDir})
	if err != nil {
		t.Fatalf("build lazy runtime: %v", err)
	}
	defer runtime.Close()
	if runtime.Expert != nil {
		t.Fatal("unbound lazy runtime unexpectedly resolved an expert")
	}
	if runtime.SkillsMgr.Get("frontend-review") != nil {
		t.Fatal("unbound lazy runtime unexpectedly loaded expert package skill")
	}

	if err := runtime.BindSession(manager, SourceWebUI); err != nil {
		t.Fatalf("bind lazy runtime: %v", err)
	}
	binding, _ := runtime.ExpertState()
	if binding == nil || binding.ID != "frontend-developer" {
		t.Fatalf("bound expert = %+v, want frontend-developer", binding)
	}
	if skill := runtime.SkillsMgr.Get("frontend-review"); skill == nil || skill.Source != "expert" {
		t.Fatalf("bound expert skill = %+v, want expert package skill", skill)
	}

	if err := runtime.UnbindSession(); err != nil {
		t.Fatalf("unbind lazy runtime: %v", err)
	}
	if binding, _ := runtime.ExpertState(); binding != nil {
		t.Fatalf("expert remained after unbind: %+v", binding)
	}
	if skill := runtime.SkillsMgr.Get("frontend-review"); skill != nil {
		t.Fatalf("expert package skill remained after unbind: %+v", skill)
	}
}

func TestSessionRuntimeBindSessionUpdatesLazyIdentity(t *testing.T) {
	workDir := t.TempDir()
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Source: SourceTUI, WorkDir: workDir}
	if err := runtime.BindSession(manager, SourceTUI); err != nil {
		t.Fatal(err)
	}
	if runtime.Manager != manager || runtime.ID != manager.GetHeader().ID || runtime.WorkDir != manager.GetHeader().Cwd || runtime.Source != SourceTUI {
		t.Fatalf("runtime binding = %#v", runtime)
	}
}

func TestBindSessionMigratesLegacyTranscriptToEventStore(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy before bind")); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Source: SourceTUI, WorkDir: workDir}
	if err := runtime.BindSession(manager, SourceTUI); err != nil {
		t.Fatal(err)
	}
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var payloadRows int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> 'session'", manager.GetHeader().ID).Scan(&payloadRows); err != nil {
		t.Fatal(err)
	}
	if payloadRows != 0 {
		t.Fatalf("legacy payload rows after Runtime bind = %d, want 0", payloadRows)
	}
	header, err := dao.NewSessionDAO(db.Bun()).Header(context.Background(), "sessions", manager.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	if header.ContentStore != "event_store" {
		t.Fatalf("content store = %q, want event_store", header.ContentStore)
	}
	if messages := manager.GetMessages(); len(messages) != 1 || messages[0].Content != "legacy before bind" {
		t.Fatalf("migrated Runtime replay = %#v", messages)
	}
}

func TestBindSessionKeepsPreviousIdentityWhenPreparationFails(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := t.TempDir()
	first := session.New(workDir, sessionDir)
	if err := first.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Source: SourceTUI, WorkDir: workDir}
	if err := runtime.BindSession(first, SourceTUI); err != nil {
		t.Fatalf("bind initial session: %v", err)
	}

	invalid := session.New(workDir, sessionDir)
	if err := invalid.Init(); err != nil {
		t.Fatal(err)
	}
	if err := invalid.SetExpertBinding("does-not-exist"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.BindSession(invalid, SourceTUI); err == nil {
		t.Fatal("binding a session with an invalid expert unexpectedly succeeded")
	}
	if runtime.Manager != first || runtime.ID != first.GetHeader().ID || runtime.WorkDir != first.GetHeader().Cwd {
		t.Fatalf("runtime identity changed after failed binding: manager=%p id=%q cwd=%q", runtime.Manager, runtime.ID, runtime.WorkDir)
	}
}

func TestSessionRuntimeBindSessionRejectsClosedRuntime(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Source: SourceTUI}
	runtime.Close()
	if err := runtime.BindSession(manager, SourceTUI); err == nil {
		t.Fatal("BindSession on closed runtime unexpectedly succeeded")
	}
}
