package agentruntime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

// acpWorkerWorkspace is the path/sandbox projection for one external child. A
// child running in the session workspace reuses the session registry; a child
// in a Runtime-resolved worktree gets an isolated view rooted at that worktree.
type acpWorkerWorkspace struct {
	registry *tools.Registry
	workDir  string
	isolated bool
}

func (r *SessionRuntime) acpWorkspaceFor(workDir string) (*acpWorkerWorkspace, error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" || !filepath.IsAbs(workDir) {
		return nil, fmt.Errorf("external ACP worker requires an absolute working directory")
	}
	workDir = filepath.Clean(workDir)

	r.mu.RLock()
	registry := r.Registry
	sandboxManager := r.SandboxMgr
	settings := r.resourceSettings
	closed := r.closed
	r.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("agent runtime is closed")
	}
	if registry == nil {
		// Headless/test runtimes may not attach a registry. The worker can still
		// run, but client-owned filesystem and terminal bridges stay unavailable.
		return &acpWorkerWorkspace{workDir: workDir}, nil
	}
	if filepath.Clean(registry.GetWorkDir()) == workDir {
		return &acpWorkerWorkspace{registry: registry, workDir: workDir}, nil
	}

	level := sandbox.LevelNone
	if sandboxManager != nil {
		if active := sandboxManager.GetActive(); active != nil {
			level = active.Level()
		}
	}
	options := sandbox.Options{}
	if settings != nil {
		options = settings.Sandbox.Options()
	}
	manager := sandbox.NewManagerWithOptions(workDir, options)
	if err := manager.SetLevel(level); err != nil {
		return nil, fmt.Errorf("prepare isolated worker sandbox: %w", err)
	}
	view := registry.WorkspaceView(workDir, manager.GetActive(), nil)
	if view == nil {
		return nil, fmt.Errorf("prepare isolated worker workspace")
	}
	return &acpWorkerWorkspace{registry: view, workDir: workDir, isolated: true}, nil
}

func (w *acpWorkerWorkspace) registryOr(fallback *tools.Registry) *tools.Registry {
	if w != nil && w.registry != nil {
		return w.registry
	}
	return fallback
}

func (w *acpWorkerWorkspace) additionalDirectories(fallback []string) []string {
	if w != nil && w.isolated {
		return nil
	}
	return append([]string(nil), fallback...)
}
