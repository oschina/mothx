//go:build !windows

package agentruntime

import (
	"os/exec"
	"syscall"
)

// acpProcessState contains no platform resources on Unix: the process group is
// carried by the child's SysProcAttr.
type acpProcessState struct{}

func newACPProcessState() *acpProcessState { return &acpProcessState{} }

func (s *acpProcessState) configure(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return nil
}

func (s *acpProcessState) started(cmd *exec.Cmd) error { return nil }

// terminate sends SIGKILL to the child's process group, reaping descendants a
// shell or test runner may have started.
func (s *acpProcessState) terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

func (s *acpProcessState) release() {}
