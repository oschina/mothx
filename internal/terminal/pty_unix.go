//go:build linux || darwin || freebsd || dragonfly || netbsd || openbsd || solaris

package terminal

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

func Supported() bool { return true }

type unixPTY struct {
	*os.File
	cmd *exec.Cmd
}

func (p *unixPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(p.File, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *unixPTY) Wait() (int, error) {
	err := p.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func (p *unixPTY) Pid() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func startPTY(cmd *exec.Cmd, cols, rows uint16) (PTY, error) {
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixPTY{File: ptmx, cmd: cmd}, nil
}

// requestStop asks the shell's process group to terminate. SIGHUP is the
// canonical "terminal hung up" signal: interactive shells (dash, bash) ignore
// SIGTERM but exit on SIGHUP, so sending SIGTERM would make every idle shell
// wait out the full grace period. CloseWithReason waits for the PTY to actually
// finish and only escalates to forceStop after the grace period, so a normal
// shell closes immediately. The direct process signal covers the tiny window
// before the child has completed setsid and become its own process-group
// leader.
func requestStop(p PTY) {
	if p == nil {
		return
	}
	if pid := p.Pid(); pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		if process, ok := p.(*unixPTY); ok && process.cmd != nil && process.cmd.Process != nil {
			_ = process.cmd.Process.Signal(syscall.SIGHUP)
		}
	}
}

func forceStop(p PTY) {
	if p == nil {
		return
	}
	if pid := p.Pid(); pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	_ = p.Close()
}
