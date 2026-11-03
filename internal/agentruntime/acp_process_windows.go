//go:build windows

package agentruntime

import (
	"fmt"
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
	// Start suspended so the process cannot spawn a descendant before it is
	// assigned to the Job Object. started() resumes it after assignment.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
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
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return err
	}
	return resumeSuspendedProcess(uint32(cmd.Process.Pid))
}

// resumeSuspendedProcess resumes the initial thread of a CREATE_SUSPENDED
// process after it has joined the Job Object. os/exec does not expose the
// primary thread handle, so resolve it through a thread snapshot rather than
// resuming the process through an undocumented ntdll entry point.
func resumeSuspendedProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot ACP worker threads: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			continue
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if resumeErr != nil {
			return fmt.Errorf("resume ACP worker thread: %w", resumeErr)
		}
		return nil
	}
	return fmt.Errorf("no initial thread found for suspended ACP worker %d", pid)
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
