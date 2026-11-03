package agentruntime

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	acpclient "github.com/oschina/mothx/internal/acp/client"
)

// acpProcessLauncher is the Runtime-owned process launcher for external ACP
// workers. It starts the configured argv without a shell and gives the
// platform-specific state full ownership of the process tree, so cancellation
// reaps descendants and not only the ACP parent.
type acpProcessLauncher struct{}

func (acpProcessLauncher) Start(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer) (acpclient.Process, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("ACP worker command is required")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append([]string(nil), env...)
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	state := newACPProcessState()
	if err := state.configure(cmd); err != nil {
		state.release()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		state.release()
		return nil, fmt.Errorf("ACP worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		state.release()
		return nil, fmt.Errorf("ACP worker stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		state.release()
		return nil, fmt.Errorf("start ACP worker: %w", err)
	}
	if err := state.started(cmd); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		state.terminate(cmd)
		state.release()
		return nil, fmt.Errorf("contain ACP worker process tree: %w", err)
	}
	process := &acpOSProcess{cmd: cmd, stdin: stdin, stdout: stdout, state: state}
	if ctx != nil {
		process.stopContext = context.AfterFunc(ctx, process.Terminate)
	}
	return process, nil
}

type acpOSProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	state  *acpProcessState

	terminateOnce sync.Once
	releaseOnce   sync.Once
	stopContext   func() bool

	stateMu sync.Mutex
	exited  bool
}

func (p *acpOSProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *acpOSProcess) Stdout() io.ReadCloser { return p.stdout }

func (p *acpOSProcess) Wait() error {
	err := p.cmd.Wait()
	p.stateMu.Lock()
	p.exited = true
	p.stateMu.Unlock()
	p.release()
	return err
}

// Terminate kills the whole process tree. It is idempotent and safe after the
// process already exited.
func (p *acpOSProcess) Terminate() {
	p.terminateOnce.Do(func() {
		p.stateMu.Lock()
		defer p.stateMu.Unlock()
		// After Wait the process group id may already be recycled; never signal it.
		if p.exited || p.cmd == nil || p.cmd.Process == nil {
			return
		}
		p.state.terminate(p.cmd)
	})
}

func (p *acpOSProcess) release() {
	p.releaseOnce.Do(func() {
		if p.stopContext != nil {
			p.stopContext()
		}
		if p.state != nil {
			p.state.release()
		}
	})
}
