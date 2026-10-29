//go:build !linux

package terminal

import "os/exec"

// configureProcAttr is a no-op outside Linux. Parent-death signalling is a
// Linux-specific SysProcAttr field; other platforms rely on process-group
// termination and PTY close when the serve process shuts a terminal down.
func configureProcAttr(cmd *exec.Cmd) {}
