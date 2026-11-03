package client

import (
	"context"
	"io"
	"os/exec"
)

// testLauncher is a test-only stdio launcher. Production callers must pass the
// Runtime-owned process-tree launcher.
type testLauncher struct{}

func (testLauncher) Start(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer) (Process, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append([]string(nil), env...)
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &testProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

type testProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *testProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *testProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *testProcess) Wait() error           { return p.cmd.Wait() }
func (p *testProcess) Terminate() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}
