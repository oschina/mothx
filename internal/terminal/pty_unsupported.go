//go:build !linux && !darwin && !freebsd && !dragonfly && !netbsd && !openbsd && !solaris && !windows

package terminal

import (
	"os/exec"
)

func Supported() bool { return false }

func startPTY(cmd *exec.Cmd, cols, rows uint16) (PTY, error) {
	return nil, ErrUnsupported
}

func requestStop(p PTY) {
	if p != nil {
		_ = p.Close()
	}
}

func forceStop(p PTY) {
	if p != nil {
		_ = p.Close()
	}
}
