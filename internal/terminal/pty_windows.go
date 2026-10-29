//go:build windows

package terminal

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

func Supported() bool { return conpty.IsConPtyAvailable() }

type conPTY struct {
	pty       *conpty.ConPty
	pid       int
	job       windows.Handle
	closeOnce sync.Once
	closeErr  error
}

func (p *conPTY) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *conPTY) Write(b []byte) (int, error) { return p.pty.Write(b) }

// Close is called from requestStop, CloseWithReason, and finish. The upstream
// ConPTY Close closes raw handles and is not idempotent, so guard it here; a
// second close could otherwise release an unrelated handle that Windows has
// already reused. Closing the job after the ConPTY kills descendants that the
// root shell left behind (for example a background build).
func (p *conPTY) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.pty.Close()
		if p.job != 0 {
			_ = windows.CloseHandle(p.job)
			p.job = 0
		}
	})
	return p.closeErr
}
func (p *conPTY) Resize(cols, rows uint16) error {
	return p.pty.Resize(int(cols), int(rows))
}
func (p *conPTY) Wait() (int, error) {
	code, err := p.pty.Wait(context.Background())
	return int(code), err
}
func (p *conPTY) Pid() int { return p.pid }

func startPTY(cmd *exec.Cmd, cols, rows uint16) (PTY, error) {
	if !conpty.IsConPtyAvailable() {
		return nil, ErrUnsupported
	}
	commandLine := ""
	if cmd != nil {
		commandLine = windows.ComposeCommandLine(cmd.Args)
	}
	if strings.TrimSpace(commandLine) == "" {
		return nil, ErrUnsupported
	}
	options := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(int(cols), int(rows)),
	}
	if cmd != nil && cmd.Dir != "" {
		options = append(options, conpty.ConPtyWorkDir(cmd.Dir))
	}
	if cmd != nil && len(cmd.Env) > 0 {
		options = append(options, conpty.ConPtyEnv(cmd.Env))
	}
	pty, err := conpty.Start(commandLine, options...)
	if err != nil {
		return nil, err
	}
	return &conPTY{pty: pty, pid: pty.Pid(), job: killOnCloseJob(pty.Pid())}, nil
}

// killOnCloseJob assigns the shell to a job with KILL_ON_JOB_CLOSE so closing
// the terminal also terminates descendants that survived the root process.
// Assignment can be denied (for example when the serve process itself runs in
// a restrictive job); in that case the terminal still works and Close remains
// the ConPTY-level termination. This is best-effort by design and must never
// fail terminal creation.
func killOnCloseJob(pid int) windows.Handle {
	if pid <= 0 {
		return 0
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		_ = windows.CloseHandle(job)
		return 0
	}
	return job
}

func requestStop(p PTY) {
	if p == nil {
		return
	}
	// Closing the ConPTY terminates the child process; there is no Unix signal
	// equivalent to send first.
	_ = p.Close()
}

func forceStop(p PTY) {
	if p == nil {
		return
	}
	_ = p.Close()
}
