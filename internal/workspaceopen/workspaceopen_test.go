package workspaceopen

import (
	"testing"
)

func TestRevealCommandTable(t *testing.T) {
	if got := RevealCommand("darwin", "/tmp/x", true); len(got) != 2 || got[0] != "open" || got[1] != "/tmp/x" {
		t.Errorf("darwin dir reveal = %v", got)
	}
	if got := RevealCommand("darwin", "/tmp/x/y.txt", false); got[1] != "-R" || got[2] != "/tmp/x/y.txt" {
		t.Errorf("darwin file reveal = %v", got)
	}
	if got := RevealCommand("windows", `C:\tmp\y.txt`, false); got[0] != "explorer.exe" || got[1] != `/select,C:\tmp\y.txt` {
		t.Errorf("windows file reveal = %v", got)
	}
}

func TestSystemTerminalCommandTable(t *testing.T) {
	if got := SystemTerminalCommand("darwin", "/tmp/x"); got[0] != "open" || got[2] != "Terminal" || got[3] != "/tmp/x" {
		t.Errorf("darwin terminal = %v", got)
	}
	if got := SystemTerminalCommand("linux", "/tmp/x"); got != nil {
		last := got[len(got)-1]
		if last != "/tmp/x" {
			t.Errorf("linux terminal argv must end with the plain directory: %v", got)
		}
	}
}

func TestEditorCommandRejectsUnknown(t *testing.T) {
	if got := EditorCommand("linux", "notepad", "/tmp/x"); got != nil {
		t.Errorf("unknown editor must be rejected: %v", got)
	}
	if got := EditorCommand("linux", "code", "/tmp/x"); len(got) != 2 || got[0] != "code" || got[1] != "/tmp/x" {
		t.Errorf("editor = %v", got)
	}
}

func TestPlanRejectsUnknownAction(t *testing.T) {
	svc := New(nil, nil)
	if _, err := svc.Plan(Action("nuke"), "/tmp/x", true, ""); err != ErrUnavailable {
		t.Errorf("unknown action = %v", err)
	}
	if _, err := svc.Plan(ActionReveal, "", true, ""); err != ErrUnavailable {
		t.Errorf("empty target = %v", err)
	}
}
