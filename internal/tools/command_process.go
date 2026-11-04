package tools

import (
	"os/exec"
	"strings"
	"time"
)

// ConfigureCommandProcess applies the same non-interactive stdio, process-group
// containment, and cancellation behavior used by the built-in shell tools. It
// is shared by Runtime-owned projection bridges that must not invent a second
// command lifecycle.
func ConfigureCommandProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	setSysProcAttr(cmd)
	if cmd.Stdin == nil {
		cmd.Stdin = strings.NewReader("")
	}
	cmd.Env = nonInteractiveEnv(cmd.Env)
	cmd.Cancel = func() error {
		return killCommandProcess(cmd)
	}
	// If the shell exits while a background child still holds stdio, don't wait
	// forever; force-close copied pipes after a short grace.
	cmd.WaitDelay = 100 * time.Millisecond
}

// KillCommandProcess terminates a command and its descendants using the same
// platform containment path as the built-in shell tools.
func KillCommandProcess(cmd *exec.Cmd) error {
	return killCommandProcess(cmd)
}
