//go:build linux

package terminal

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestConfigureProcAttrArmsPdeathsig(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	configureProcAttr(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not initialized")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}

func TestConfigureProcAttrPreservesExistingAttrs(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	configureProcAttr(cmd)
	if !cmd.SysProcAttr.Setsid {
		t.Fatal("Setsid was cleared")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("Pdeathsig = %v, want SIGKILL", cmd.SysProcAttr.Pdeathsig)
	}
}

func TestTerminalCloseKillsProcessGroup(t *testing.T) {
	if !Supported() {
		t.Skip("PTY not supported")
	}
	m := New(Config{Shell: "/bin/sh", ScrollbackBytes: 1 << 12})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pid := term.pty.Pid()
	if pid <= 0 {
		t.Fatalf("pid = %d", pid)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-term.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not finish after close")
	}
	if got := term.State(); got != StateKilled {
		t.Fatalf("state = %q, want killed", got)
	}
}
