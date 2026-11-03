//go:build windows

package agentruntime

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// acpProcessState owns the Windows Job Object that contains the external ACP
// worker and every descendant it starts. Kill-on-close guarantees that killing
// or losing the job reaps the whole tree.
type acpProcessState struct {
	mu  sync.Mutex
	job windows.Handle
}

func newACPProcessState() *acpProcessState { return &acpProcessState{} }

func (s *acpProcessState) configure(cmd *exec.Cmd) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	s.job = job
	return nil
}

func (s *acpProcessState) started(cmd *exec.Cmd) error {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job == 0 || cmd == nil || cmd.Process == nil {
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(job, process)
}

func (s *acpProcessState) terminate(cmd *exec.Cmd) {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		return
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (s *acpProcessState) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != 0 {
		_ = windows.CloseHandle(s.job)
		s.job = 0
	}
}
