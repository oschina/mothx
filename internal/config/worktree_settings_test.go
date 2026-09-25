package config

import (
	"testing"

	"github.com/oschina/mothx/internal/sandbox"
)

func TestWorktreeSettingsDefaultsAndAccessors(t *testing.T) {
	defaults := DefaultSettings()
	if !defaults.IsWorktreeEnabled() {
		t.Fatal("default worktree setting must be enabled")
	}
	if defaults.WorktreeBranchPrefix() != "mothx" {
		t.Fatalf("default branch prefix = %q, want mothx", defaults.WorktreeBranchPrefix())
	}
	if defaults.WorktreeStartCommand() != "" {
		t.Fatalf("default start command = %q, want empty", defaults.WorktreeStartCommand())
	}

	off := false
	s := &Settings{Worktree: WorktreeSettings{Enabled: &off, BranchPrefix: "  wt  ", StartCommand: " bun install "}}
	if s.IsWorktreeEnabled() {
		t.Fatal("explicit false must disable worktrees")
	}
	if s.WorktreeBranchPrefix() != "wt" {
		t.Fatalf("branch prefix = %q, want trimmed wt", s.WorktreeBranchPrefix())
	}
	if s.WorktreeStartCommand() != "bun install" {
		t.Fatalf("start command = %q, want trimmed", s.WorktreeStartCommand())
	}

	var nilSettings *Settings
	if !nilSettings.IsWorktreeEnabled() {
		t.Fatal("nil settings must default to enabled")
	}
	if nilSettings.WorktreeBranchPrefix() != "mothx" || nilSettings.WorktreeStartCommand() != "" {
		t.Fatal("nil settings must return documented defaults")
	}
}

func TestWorktreeESMAndSandboxLevelDefaults(t *testing.T) {
	if DefaultSettings().WorktreeESMEnabled() {
		t.Fatal("ESM worktrees must default to off")
	}
	var nilSettings *Settings
	if nilSettings.WorktreeESMEnabled() {
		t.Fatal("nil settings must keep ESM off")
	}

	sandboxSettings := SandboxSettings{Enabled: false}
	if sandboxSettings.EffectiveLevel() != sandbox.LevelNone {
		t.Fatalf("disabled sandbox level = %v, want none", sandboxSettings.EffectiveLevel())
	}
	sandboxSettings = SandboxSettings{Enabled: true}
	if sandboxSettings.EffectiveLevel() != sandbox.LevelStandard {
		t.Fatalf("default enabled level = %v, want standard", sandboxSettings.EffectiveLevel())
	}
	sandboxSettings.Level = "strict"
	if sandboxSettings.EffectiveLevel() != sandbox.LevelStrict {
		t.Fatalf("strict level = %v, want strict", sandboxSettings.EffectiveLevel())
	}
}
