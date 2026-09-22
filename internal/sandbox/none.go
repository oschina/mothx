package sandbox

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/oschina/mothx/internal/platform"
)

// NoneSandbox executes commands without any sandbox restrictions.
type NoneSandbox struct{}

// NewNoneSandbox creates a new no-op sandbox.
func NewNoneSandbox() *NoneSandbox {
	return &NoneSandbox{}
}

// WrapCommand returns a plain command without any sandbox restrictions.
// It inherits the full parent environment and overlays opts.EnvVars on top.
func (s *NoneSandbox) WrapCommand(ctx context.Context, shell, cmd string, opts ExecOpts) *exec.Cmd {
	c := exec.CommandContext(ctx, shell, platform.ShellArgs(shell, cmd)...)

	if opts.WorkDir != "" {
		c.Dir = opts.WorkDir
	}

	// Inherit full parent environment, then overlay opts.EnvVars.
	env := os.Environ()
	for k, v := range opts.EnvVars {
		prefix := k + "="
		replaced := false
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = k + "=" + v
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, k+"="+v)
		}
	}
	c.Env = env

	return c
}

// IsAvailable always returns true.
func (s *NoneSandbox) IsAvailable() bool {
	return true
}

// Name returns "none".
func (s *NoneSandbox) Name() string {
	return "none"
}

// Level returns LevelNone.
func (s *NoneSandbox) Level() Level {
	return LevelNone
}
