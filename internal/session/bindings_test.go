package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateBoundSessionRebindsChannel(t *testing.T) {
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	const (
		channelType = "wechat"
		channelID   = "user-123"
	)

	old, err := CreateBound(workDir, sessionDir, channelType, channelID)
	if err != nil {
		t.Fatalf("create bound session: %v", err)
	}
	oldID := old.GetHeader().ID

	rotated, err := RotateBoundSession(sessionDir, channelType, channelID, oldID)
	if err != nil {
		t.Fatalf("rotate bound session: %v", err)
	}
	newID := rotated.GetHeader().ID
	if newID == oldID {
		t.Fatalf("rotated session reused old ID %q", oldID)
	}

	binding, err := FindBinding(sessionDir, channelType, channelID)
	if err != nil {
		t.Fatalf("find rotated binding: %v", err)
	}
	if binding == nil || binding.SessionID != newID {
		t.Fatalf("binding = %#v, want session %q", binding, newID)
	}

	oldBinding, err := FindBindingBySessionID(sessionDir, oldID)
	if err != nil {
		t.Fatalf("find old session binding: %v", err)
	}
	if oldBinding != nil {
		t.Fatalf("old binding = %#v, want no external binding", oldBinding)
	}
	if got := rotated.GetHeader().Cwd; got != workDir {
		t.Fatalf("rotated cwd = %q, want %q", got, workDir)
	}
}

func TestRotateBoundSessionRejectsUnavailableWorkDirWithoutRebinding(t *testing.T) {
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	old, err := CreateBound(workDir, sessionDir, "wechat", "user-456")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workDir); err != nil {
		t.Fatal(err)
	}
	if _, err := RotateBoundSession(sessionDir, "wechat", "user-456", old.GetHeader().ID); err == nil {
		t.Fatal("expected unavailable work directory error")
	}
	binding, err := FindBinding(sessionDir, "wechat", "user-456")
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.SessionID != old.GetHeader().ID {
		t.Fatalf("binding = %#v, want original session %q", binding, old.GetHeader().ID)
	}
}

func TestSetWorkDirRejectsRegularFileWithoutMutation(t *testing.T) {
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	mgr := New(workDir, sessionDir)
	if err := mgr.InitWithID("set-workdir-invalid"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetWorkDir(file); err == nil {
		t.Fatal("expected regular file error")
	}
	if got := mgr.GetHeader().Cwd; got != workDir {
		t.Fatalf("cwd after failed update = %q, want %q", got, workDir)
	}
}

func TestChannelToolsPersistForBoundSession(t *testing.T) {
	sessionDir := t.TempDir()
	mgr, err := CreateBound(t.TempDir(), sessionDir, "feishu", "chat-123")
	if err != nil {
		t.Fatalf("create bound session: %v", err)
	}
	sessionID := mgr.GetHeader().ID
	want := []ChannelToolConfig{
		{ToolName: "bash", Enabled: false},
		{ToolName: "read", Enabled: true},
	}
	if err := SetChannelTools(sessionDir, sessionID, want); err != nil {
		t.Fatalf("set channel tools: %v", err)
	}
	got, err := ListChannelTools(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("list channel tools: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tools, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
