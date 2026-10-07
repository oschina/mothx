//go:build windows

package session

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows SO_REUSEADDR differs from the Unix option: it permits binding a port
// already bound by another socket, so a malicious local process could hijack
// the bus port and swallow wake-ups. The bus is advisory only, so the worst
// case is a missed wake-up degrading to durable SQLite polling; no ownership
// or content decision ever depends on it.
func runtimeLeaseBusListenerControl(_ string, _ string, raw syscall.RawConn) error {
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return socketErr
}

func runtimeLeaseBusSenderControl(_ string, _ string, raw syscall.RawConn) error {
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return socketErr
}
