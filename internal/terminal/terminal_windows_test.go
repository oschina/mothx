//go:build windows

package terminal

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestConPTYRoundTrip is the real-Windows runner gate for T1. It exercises the
// full ConPTY lifecycle through Manager: start, input, output, resize, and
// exit. It skips on Windows versions older than 10.0.1809 where ConPTY is
// unavailable; a skipped run is not evidence, so CI must report skips.
func TestConPTYRoundTrip(t *testing.T) {
	if !Supported() {
		t.Skip("ConPTY is not available on this Windows build")
	}
	m := New(Config{Shell: "cmd.exe", ScrollbackBytes: 1 << 16})
	defer m.Shutdown()
	term, err := m.Create("session", t.TempDir(), 80, 24)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sub, err := term.Attach()
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer term.Detach(sub)
	if err := term.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := term.Write([]byte("echo mothx-conpty-ok\r\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	deadline := time.After(15 * time.Second)
	var out strings.Builder
	for {
		select {
		case chunk := <-sub.Output():
			out.Write(chunk)
			if strings.Contains(out.String(), "mothx-conpty-ok") {
				if err := term.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				select {
				case <-term.Done():
					return
				case <-deadline:
					t.Fatalf("terminal did not finish after close: %q", out.String())
				}
			}
		case <-sub.Stop():
			t.Fatalf("terminal stopped before output: %q (%s)", out.String(), sub.Reason())
		case <-deadline:
			t.Fatalf("timed out waiting for ConPTY output: %q", out.String())
		}
	}
}

// TestConPTYCloseIsIdempotent guards the Windows wrapper against the upstream
// ConPTY Close, which releases raw handles and must not run twice. Terminal
// shutdown calls PTY.Close from more than one path.
func TestConPTYCloseIsIdempotent(t *testing.T) {
	if !Supported() {
		t.Skip("ConPTY is not available on this Windows build")
	}
	cmd := exec.Command("cmd.exe")
	cmd.Env = shellEnv()
	p, err := startPTY(cmd, 80, 24)
	if err != nil {
		t.Fatalf("startPTY: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
