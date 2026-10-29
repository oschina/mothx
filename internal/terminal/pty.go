package terminal

import "io"

// PTY is the platform-neutral handle for one pseudo-terminal child process.
// Unix uses creack/pty; Windows uses ConPTY. Implementations own the process
// wait semantics so Terminal never needs platform-specific process handling.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	Wait() (int, error)
	Pid() int
}
