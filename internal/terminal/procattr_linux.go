//go:build linux

package terminal

import (
	"os/exec"
	"syscall"
)

// configureProcAttr arms parent-death signalling for the PTY child. A detached
// terminal is intentionally not killed when its WebSocket client disconnects,
// but if the serve process itself disappears the shell must not be orphaned
// holding the workspace cwd. creack/pty enables Setsid/Setctty on the same
// SysProcAttr, so the child remains a session leader and process-group kill
// keeps working; Pdeathsig only adds a kernel-level parent check.
func configureProcAttr(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
