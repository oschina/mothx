package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func TestAttachSessionResourcesUsesManagerIdentity(t *testing.T) {
	workDir := t.TempDir()
	mgr := session.New(workDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	runtime, err := AttachSessionResources(AttachedResources{
		Source: SourceACP, WorkDir: workDir, Manager: mgr, Registry: registry,
	})
	if err != nil {
		t.Fatalf("AttachSessionResources: %v", err)
	}
	if runtime.ID != mgr.GetHeader().ID || runtime.Manager != mgr || runtime.Registry != registry {
		t.Fatalf("attached runtime = %#v", runtime)
	}
}

func TestAttachSessionResourcesRejectsIncompleteOwnership(t *testing.T) {
	if _, err := AttachSessionResources(AttachedResources{WorkDir: t.TempDir()}); err == nil {
		t.Fatal("AttachSessionResources succeeded without manager and registry")
	}
}
