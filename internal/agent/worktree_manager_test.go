package agent

import (
	"fmt"
	"strings"
	"testing"
)

type worktreeProviderFunc func(baseCwd string, spec WorktreeSpec) (string, error)

func (f worktreeProviderFunc) ResolveWorktree(baseCwd string, spec WorktreeSpec) (string, error) {
	return f(baseCwd, spec)
}

func TestAgentManagerCreateRequiresWorktreeProvider(t *testing.T) {
	m := NewAgentManager(nil)
	_, err := m.Create(AgentOptions{ParentID: "missing", Worktree: &WorktreeSpec{}})
	if err == nil || !strings.Contains(err.Error(), "no worktree provider") {
		t.Fatalf("err = %v, want a missing-provider error", err)
	}
}

func TestAgentManagerCreateResolvesRequestedWorktree(t *testing.T) {
	var gotBase string
	var gotSpec WorktreeSpec
	called := false
	m := NewAgentManager(nil)
	m.SetWorktreeProvider(worktreeProviderFunc(func(baseCwd string, spec WorktreeSpec) (string, error) {
		called = true
		gotBase = baseCwd
		gotSpec = spec
		return "/repo/.wt/child", nil
	}))

	// The parent does not exist, so Create fails after resolution; that is
	// enough to prove the provider ran before the factory.
	_, err := m.Create(AgentOptions{ParentID: "missing", WorkDir: "/repo", Worktree: &WorktreeSpec{Name: "child"}})
	if !called {
		t.Fatal("worktree provider was not called for an explicit request")
	}
	if gotBase != "/repo" || gotSpec.Name != "child" {
		t.Fatalf("provider got base=%q spec=%+v", gotBase, gotSpec)
	}
	if err == nil {
		t.Fatal("expected the parent lookup to fail")
	}
}

func TestAgentManagerPerChildPolicyTriggersWorktree(t *testing.T) {
	called := false
	m := NewAgentManager(nil)
	m.SetWorktreeProvider(worktreeProviderFunc(func(baseCwd string, spec WorktreeSpec) (string, error) {
		called = true
		return "/repo/.wt/auto", nil
	}))
	m.SetWorktreePerChild(true)

	_, _ = m.Create(AgentOptions{ParentID: "missing", WorkDir: "/repo"})
	if !called {
		t.Fatal("per-child policy must request a worktree without an explicit spec")
	}
}

func TestAgentManagerOptionalWorktreeFallsBackToWorkspace(t *testing.T) {
	m := NewAgentManager(nil)
	m.SetWorktreeProvider(worktreeProviderFunc(func(baseCwd string, spec WorktreeSpec) (string, error) {
		return "", fmt.Errorf("not a git repository")
	}))

	// Optional: keep the inherited workspace and continue into the factory.
	called := false
	m.SetWorktreeProvider(worktreeProviderFunc(func(baseCwd string, spec WorktreeSpec) (string, error) {
		called = true
		return "", fmt.Errorf("not a git repository")
	}))
	_, err := m.Create(AgentOptions{
		ParentID: "missing", WorkDir: "/repo", IsSubAgent: true,
		Worktree: &WorktreeSpec{Optional: true},
	})
	if !called {
		t.Fatal("optional request must still consult the provider")
	}
	if err == nil || strings.Contains(err.Error(), "resolve sub-agent worktree") {
		t.Fatalf("optional failure must not surface as a worktree error, got %v", err)
	}
}

func TestAgentManagerMandatoryWorktreeFailureIsReported(t *testing.T) {
	m := NewAgentManager(nil)
	m.SetWorktreeProvider(worktreeProviderFunc(func(string, WorktreeSpec) (string, error) {
		return "", fmt.Errorf("not a git repository")
	}))
	_, err := m.Create(AgentOptions{ParentID: "missing", WorkDir: "/repo", IsSubAgent: true, Worktree: &WorktreeSpec{Optional: false}})
	if err == nil || !strings.Contains(err.Error(), "resolve sub-agent worktree") {
		t.Fatalf("err = %v, want a worktree resolution error", err)
	}
}
