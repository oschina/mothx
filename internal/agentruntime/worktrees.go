package agentruntime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/worktree"
)

// Worktree lifecycle event types. They describe the managed worktree resource,
// never a run terminal state.
const (
	WorktreeEventPending = "pending"
	WorktreeEventReady   = "ready"
	WorktreeEventFailed  = "failed"
	WorktreeEventRemoved = "removed"
)

// WorktreeEvent is a canonical worktree lifecycle event. Adapters project it
// onto their own wire format.
type WorktreeEvent struct {
	Type      string
	ID        string
	Name      string
	Branch    string
	Directory string
	Message   string
}

// WorktreeEventSink receives canonical worktree lifecycle events.
type WorktreeEventSink interface {
	EmitWorktreeEvent(event WorktreeEvent)
}

// WorktreeEventSinkFunc adapts a function to WorktreeEventSink.
type WorktreeEventSinkFunc func(WorktreeEvent)

func (f WorktreeEventSinkFunc) EmitWorktreeEvent(event WorktreeEvent) { f(event) }

// WorktreeManagerOptions configures a WorktreeManager.
type WorktreeManagerOptions struct {
	// SessionDir is the shared sessions.db root that owns the worktree registry.
	SessionDir string
	// Root overrides the default worktree root (<data-dir>/worktrees).
	Root string
	// BranchPrefix is the default branch prefix ("mothx" when empty).
	BranchPrefix string
	// Git overrides the git mechanism (tests).
	Git *worktree.Manager
	// EventSink receives lifecycle events. Optional.
	EventSink WorktreeEventSink
	// DefaultStartCommand is applied to a worktree whose request does not
	// specify one. Empty means no start command runs.
	DefaultStartCommand string
	// SandboxOptions is the sandbox policy used to execute a worktree start
	// command. Start commands never run outside the sandbox.
	SandboxOptions sandbox.Options
	// SandboxLevel is the resolved sandbox level for start commands. Nil means
	// LevelNone (direct execution).
	SandboxLevel *sandbox.Level
}

// CreateWorktreeRequest describes an on-demand worktree creation.
type CreateWorktreeRequest struct {
	BaseCwd      string
	Name         string
	Detached     bool
	StartCommand string
	ProjectID    string
}

// WorktreeManager is the single Runtime owner of managed worktree orchestration:
// it resolves the repository root, plans a unique worktree, registers it,
// materializes it, authorizes its directory, emits canonical events and keeps
// the registry reconciled with git. Git mechanics live in internal/worktree;
// SQL lives in internal/dao behind internal/session.
type WorktreeManager struct {
	sessionDir          string
	git                 *worktree.Manager
	eventSink           WorktreeEventSink
	defaultStartCommand string
	sandboxOptions      sandbox.Options
	sandboxLevel        *sandbox.Level

	mu         sync.Mutex
	authorized map[string]struct{}
	locks      map[string]*sync.Mutex
	wg         sync.WaitGroup
}

// NewWorktreeManager builds a WorktreeManager.
func NewWorktreeManager(opts WorktreeManagerOptions) (*WorktreeManager, error) {
	if strings.TrimSpace(opts.SessionDir) == "" {
		return nil, fmt.Errorf("worktree manager requires a session directory")
	}
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = filepath.Join(platform.DataDir(), "worktrees")
	}
	git := opts.Git
	if git == nil {
		git = worktree.NewManager(root, opts.BranchPrefix)
	}
	return &WorktreeManager{
		sessionDir:          opts.SessionDir,
		git:                 git,
		eventSink:           opts.EventSink,
		defaultStartCommand: strings.TrimSpace(opts.DefaultStartCommand),
		sandboxOptions:      opts.SandboxOptions,
		sandboxLevel:        opts.SandboxLevel,
		authorized:          make(map[string]struct{}),
		locks:               make(map[string]*sync.Mutex),
	}, nil
}

// Root returns the directory managed worktrees live under.
func (m *WorktreeManager) Root() string { return m.git.Root() }

// NewDefaultWorktreeManager builds a WorktreeManager from shared settings:
// default worktree root, the configured branch prefix/start command and the
// resolved sandbox level for start commands. Adapter entry points use it so
// every host shares one construction path instead of repeating options.
func NewDefaultWorktreeManager(settings *config.Settings) (*WorktreeManager, error) {
	if settings == nil {
		return nil, fmt.Errorf("worktree manager requires settings")
	}
	if !settings.IsWorktreeEnabled() {
		return nil, fmt.Errorf("worktrees are disabled")
	}
	level := settings.Sandbox.EffectiveLevel()
	return NewWorktreeManager(WorktreeManagerOptions{
		SessionDir:          settings.GetSessionDir(),
		BranchPrefix:        settings.WorktreeBranchPrefix(),
		DefaultStartCommand: settings.WorktreeStartCommand(),
		SandboxOptions:      settings.Sandbox.Options(),
		SandboxLevel:        &level,
	})
}

// Create resolves the repository root of BaseCwd, registers and materializes a
// new worktree, authorizes its directory synchronously and populates it
// asynchronously. The returned projection is pending until population
// completes (a ready/failed event follows).
func (m *WorktreeManager) Create(ctx context.Context, req CreateWorktreeRequest) (session.Worktree, error) {
	repoRoot, err := m.git.RepoRoot(ctx, req.BaseCwd)
	if err != nil {
		return session.Worktree{}, err
	}
	unlock := m.lockRepo(repoRoot)
	defer unlock()

	info, err := m.git.Plan(ctx, repoRoot, req.Name, req.Detached)
	if err != nil {
		return session.Worktree{}, err
	}

	startCommand := strings.TrimSpace(req.StartCommand)
	if startCommand == "" {
		startCommand = m.defaultStartCommand
	}

	record, err := session.CreateWorktreeRecord(m.sessionDir, session.Worktree{
		ID:             session.GenerateID(),
		Name:           info.Name,
		Branch:         info.Branch,
		Directory:      info.Directory,
		RepositoryRoot: repoRoot,
		ProjectID:      req.ProjectID,
		StartCommand:   startCommand,
		Status:         session.WorktreeStatusPending,
	})
	if err != nil {
		return session.Worktree{}, err
	}

	// Synchronous materialization of the directory (fast) so the directory
	// exists before it is authorized and before the caller may open a session
	// in it. The slower checkout runs asynchronously.
	if err := m.git.Add(ctx, repoRoot, info); err != nil {
		_ = session.UpdateWorktreeStatus(m.sessionDir, record.ID, session.WorktreeStatusFailed, err.Error())
		m.emit(WorktreeEvent{Type: WorktreeEventFailed, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory, Message: err.Error()})
		return session.Worktree{}, err
	}
	m.authorize(info.Directory)
	m.emit(WorktreeEvent{Type: WorktreeEventPending, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory})

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		// Serialize population with remove/reset of the same directory (in-process
		// and across processes) so a concurrent removal cannot delete a directory
		// mid-checkout.
		release, err := m.lockDir(info.Directory)
		if err != nil {
			_ = session.UpdateWorktreeStatus(m.sessionDir, record.ID, session.WorktreeStatusFailed, err.Error())
			m.emit(WorktreeEvent{Type: WorktreeEventFailed, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory, Message: err.Error()})
			return
		}
		defer release()
		m.populate(record, info)
	}()
	return record, nil
}

func (m *WorktreeManager) populate(record session.Worktree, info worktree.Info) {
	// Population is Runtime-owned background work; it must not inherit the
	// request context, which is cancelled when the caller returns.
	ctx := context.Background()
	if err := m.git.Populate(ctx, info); err != nil {
		_ = session.UpdateWorktreeStatus(m.sessionDir, record.ID, session.WorktreeStatusFailed, err.Error())
		m.emit(WorktreeEvent{Type: WorktreeEventFailed, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory, Message: err.Error()})
		return
	}
	if err := m.runStartCommand(ctx, info.Directory, record.StartCommand); err != nil {
		_ = session.UpdateWorktreeStatus(m.sessionDir, record.ID, session.WorktreeStatusFailed, err.Error())
		m.emit(WorktreeEvent{Type: WorktreeEventFailed, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory, Message: err.Error()})
		return
	}
	_ = session.UpdateWorktreeStatus(m.sessionDir, record.ID, session.WorktreeStatusReady, "")
	m.emit(WorktreeEvent{Type: WorktreeEventReady, ID: record.ID, Name: info.Name, Branch: info.Branch, Directory: info.Directory})
}

// runStartCommand executes a worktree start command through the shared sandbox
// policy. It never runs a bare shell outside the sandbox and is a no-op for an
// empty command.
func (m *WorktreeManager) runStartCommand(ctx context.Context, directory, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	sbMgr := sandbox.NewManagerWithOptions(directory, m.sandboxOptions)
	level := sandbox.LevelNone
	if m.sandboxLevel != nil {
		level = *m.sandboxLevel
	}
	if err := sbMgr.SetLevel(level); err != nil {
		return fmt.Errorf("worktree start command sandbox unavailable: %w", err)
	}
	sb := sbMgr.GetActive()
	if sb == nil {
		return fmt.Errorf("sandbox is unavailable for the worktree start command")
	}
	cmd := sb.WrapCommand(ctx, platform.DefaultShell(), command, sandbox.ExecOpts{WorkDir: directory})
	if cmd == nil {
		return fmt.Errorf("sandbox produced no command for the worktree start command")
	}
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if cleanup, ok := sb.(sandbox.CommandCleanupProvider); ok {
		cleanup.CleanupCommand(cmd)
	}
	if runErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("worktree start command failed: %v: %s", runErr, msg)
		}
		return fmt.Errorf("worktree start command failed: %v", runErr)
	}
	return nil
}

// List returns registered worktrees (optionally filtered), reconciled with git:
// rows git no longer knows are marked removed, and unregistered git worktrees
// of an explicitly requested repository are returned as read-only external
// items. A repository whose root is unavailable does not fail the whole list.
func (m *WorktreeManager) List(ctx context.Context, repositoryRoot, projectID string) ([]session.Worktree, error) {
	if root := strings.TrimSpace(repositoryRoot); root != "" {
		if resolved, err := m.git.RepoRoot(ctx, root); err == nil {
			repositoryRoot = resolved
		}
	}
	registered, err := session.ListWorktrees(m.sessionDir, repositoryRoot, projectID)
	if err != nil {
		return nil, err
	}
	scans := map[string]map[string]worktree.Info{}
	scan := func(root string) (map[string]worktree.Info, bool) {
		key := filepath.Clean(root)
		if cached, ok := scans[key]; ok {
			return cached, cached != nil
		}
		entries, listErr := m.git.List(ctx, key)
		if listErr != nil {
			scans[key] = nil
			return nil, false
		}
		dirs := make(map[string]worktree.Info, len(entries))
		for _, entry := range entries {
			dirs[filepath.Clean(entry.Directory)] = entry
		}
		scans[key] = dirs
		return dirs, true
	}

	known := make(map[string]struct{}, len(registered))
	out := make([]session.Worktree, 0, len(registered))
	for _, wt := range registered {
		known[filepath.Clean(wt.Directory)] = struct{}{}
		if dirs, ok := scan(wt.RepositoryRoot); ok {
			if _, present := dirs[filepath.Clean(wt.Directory)]; !present {
				if wt.Status != session.WorktreeStatusRemoved {
					_ = session.UpdateWorktreeStatus(m.sessionDir, wt.ID, session.WorktreeStatusRemoved, wt.Error)
				}
				continue
			}
		}
		out = append(out, wt)
	}

	if root := strings.TrimSpace(repositoryRoot); root != "" {
		if dirs, ok := scan(root); ok {
			for dir, entry := range dirs {
				if _, registered := known[dir]; registered {
					continue
				}
				out = append(out, session.Worktree{
					Name:           entry.Name,
					Branch:         entry.Branch,
					Directory:      dir,
					RepositoryRoot: filepath.Clean(root),
					Status:         session.WorktreeStatusReady,
					External:       true,
				})
			}
		}
	}
	return out, nil
}

// Remove deletes a registered worktree (by id or directory). It proves the
// directory is a worktree of the repository before deleting anything, then
// removes the registry row.
func (m *WorktreeManager) Remove(ctx context.Context, id, directory string) error {
	wt, err := m.resolve(ctx, id, directory)
	if err != nil {
		return err
	}
	unlock := m.lockRepo(wt.RepositoryRoot)
	defer unlock()
	releaseDir, err := m.lockDir(wt.Directory)
	if err != nil {
		return err
	}
	defer releaseDir()

	entries, err := m.git.List(ctx, wt.RepositoryRoot)
	if err != nil {
		return err
	}
	branch := wt.Branch
	found := false
	for _, entry := range entries {
		if filepath.Clean(entry.Directory) == filepath.Clean(wt.Directory) {
			found = true
			if entry.Branch != "" {
				branch = entry.Branch
			}
			break
		}
	}
	if !found {
		return &worktree.Error{Code: worktree.CodeRemoveFailed, Message: "directory is not a registered worktree of this repository"}
	}
	if err := m.git.Remove(ctx, wt.RepositoryRoot, wt.Directory, branch); err != nil {
		return err
	}
	m.deauthorize(wt.Directory)
	if wt.ID != "" {
		if err := session.DeleteWorktreeRecord(m.sessionDir, wt.ID); err != nil {
			return err
		}
	}
	m.emit(WorktreeEvent{Type: WorktreeEventRemoved, ID: wt.ID, Name: wt.Name, Branch: branch, Directory: wt.Directory})
	return nil
}

// Reset returns a registered worktree to the repository's default branch.
func (m *WorktreeManager) Reset(ctx context.Context, id, directory string) error {
	wt, err := m.resolve(ctx, id, directory)
	if err != nil {
		return err
	}
	unlock := m.lockRepo(wt.RepositoryRoot)
	defer unlock()
	releaseDir, err := m.lockDir(wt.Directory)
	if err != nil {
		return err
	}
	defer releaseDir()

	if err := m.git.Reset(ctx, wt.RepositoryRoot, wt.Directory); err != nil {
		return err
	}
	if err := m.runStartCommand(ctx, wt.Directory, wt.StartCommand); err != nil {
		_ = session.UpdateWorktreeStatus(m.sessionDir, wt.ID, session.WorktreeStatusFailed, err.Error())
		m.emit(WorktreeEvent{Type: WorktreeEventFailed, ID: wt.ID, Name: wt.Name, Branch: wt.Branch, Directory: wt.Directory, Message: err.Error()})
		return err
	}
	if wt.ID != "" {
		_ = session.UpdateWorktreeStatus(m.sessionDir, wt.ID, session.WorktreeStatusReady, "")
	}
	m.emit(WorktreeEvent{Type: WorktreeEventReady, ID: wt.ID, Name: wt.Name, Branch: wt.Branch, Directory: wt.Directory})
	return nil
}

// AuthorizeDirectory marks a directory as a Runtime-granted workspace root so
// the shared workspace resolver can accept it as a session cwd.
func (m *WorktreeManager) AuthorizeDirectory(directory string) {
	m.authorize(directory)
}

// IsAuthorized reports whether a directory is a Runtime-granted worktree root.
func (m *WorktreeManager) IsAuthorized(directory string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.authorized[filepath.Clean(directory)]
	return ok
}

// AuthorizedDirectories returns the sorted set of Runtime-granted worktree
// directories.
func (m *WorktreeManager) AuthorizedDirectories() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.authorized))
	for dir := range m.authorized {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

// Get returns the registered worktree projection for id, or a zero Worktree
// when it does not exist.
func (m *WorktreeManager) Get(ctx context.Context, id string) (session.Worktree, error) {
	return session.GetWorktreeByID(m.sessionDir, id)
}

// Wait blocks until in-flight asynchronous population completes. It exists for
// deterministic shutdown and tests.
func (m *WorktreeManager) Wait() { m.wg.Wait() }

// worktreeResolveTimeout bounds the wait for an on-demand worktree to become
// usable. It is a bounded setup wait, not a task deadline.
const worktreeResolveTimeout = 10 * time.Minute

// WaitForReady blocks until the worktree reaches a terminal lifecycle state and
// returns its projection. A failed/removed worktree is reported as an error so
// callers never start work in an unusable directory.
func (m *WorktreeManager) WaitForReady(ctx context.Context, id string) (session.Worktree, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		wt, err := session.GetWorktreeByID(m.sessionDir, id)
		if err != nil {
			return session.Worktree{}, err
		}
		switch wt.Status {
		case session.WorktreeStatusReady:
			return wt, nil
		case session.WorktreeStatusFailed, session.WorktreeStatusRemoved:
			msg := wt.Error
			if msg == "" {
				msg = "worktree is not ready"
			}
			return wt, &worktree.Error{Code: worktree.CodeCreateFailed, Message: msg}
		case "":
			return session.Worktree{}, &worktree.Error{Code: worktree.CodeCreateFailed, Message: fmt.Sprintf("worktree %q not found", id)}
		}
		select {
		case <-ctx.Done():
			return session.Worktree{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// ResolveOrCreate returns the directory of an existing registered worktree
// named key for baseCwd's repository, or creates one. It lets a workstream (for
// example an ESM objective's worker/critic/audit roles) share a single worktree.
func (m *WorktreeManager) ResolveOrCreate(ctx context.Context, baseCwd, key string) (string, error) {
	repoRoot, err := m.git.RepoRoot(ctx, baseCwd)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) != "" {
		existing, err := session.ListWorktrees(m.sessionDir, repoRoot, "")
		if err != nil {
			return "", err
		}
		for _, wt := range existing {
			if wt.Name != key || wt.Status == session.WorktreeStatusRemoved {
				continue
			}
			if wt.Status == session.WorktreeStatusReady {
				return wt.Directory, nil
			}
			if ready, werr := m.WaitForReady(ctx, wt.ID); werr == nil {
				return ready.Directory, nil
			}
		}
	}
	created, err := m.Create(ctx, CreateWorktreeRequest{BaseCwd: repoRoot, Name: key})
	if err != nil {
		return "", err
	}
	ready, err := m.WaitForReady(ctx, created.ID)
	if err != nil {
		return "", err
	}
	return ready.Directory, nil
}

// WorktreeProviderAdapter adapts the WorktreeManager to the internal/agent
// WorktreeProvider contract. It materializes (or reuses) a worktree and waits
// until it is usable before returning its directory.
type WorktreeProviderAdapter struct {
	mgr *WorktreeManager
}

func NewWorktreeProviderAdapter(mgr *WorktreeManager) *WorktreeProviderAdapter {
	return &WorktreeProviderAdapter{mgr: mgr}
}

// ResolveWorktree implements agent.WorktreeProvider.
func (p *WorktreeProviderAdapter) ResolveWorktree(baseCwd string, spec agent.WorktreeSpec) (string, error) {
	if p == nil || p.mgr == nil {
		return "", fmt.Errorf("worktree manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeResolveTimeout)
	defer cancel()
	if spec.Reuse {
		return p.mgr.ResolveOrCreate(ctx, baseCwd, spec.Name)
	}
	created, err := p.mgr.Create(ctx, CreateWorktreeRequest{BaseCwd: baseCwd, Name: spec.Name})
	if err != nil {
		return "", err
	}
	ready, err := p.mgr.WaitForReady(ctx, created.ID)
	if err != nil {
		return "", err
	}
	return ready.Directory, nil
}

func (m *WorktreeManager) resolve(ctx context.Context, id, directory string) (session.Worktree, error) {
	if strings.TrimSpace(id) != "" {
		wt, err := session.GetWorktreeByID(m.sessionDir, id)
		if err != nil {
			return session.Worktree{}, err
		}
		if wt.ID == "" {
			return session.Worktree{}, &worktree.Error{Code: worktree.CodeRemoveFailed, Message: fmt.Sprintf("worktree %q not found", id)}
		}
		return wt, nil
	}
	if strings.TrimSpace(directory) == "" {
		return session.Worktree{}, &worktree.Error{Code: worktree.CodeRemoveFailed, Message: "worktree id or directory is required"}
	}
	wt, err := session.GetWorktreeByDirectory(m.sessionDir, directory)
	if err != nil {
		return session.Worktree{}, err
	}
	if wt.ID != "" {
		return wt, nil
	}
	// Unregistered directory: derive the repository root so Remove can still
	// prove and delete a genuine external worktree.
	repoRoot, err := m.git.RepoRoot(ctx, directory)
	if err != nil {
		return session.Worktree{}, err
	}
	return session.Worktree{Directory: filepath.Clean(directory), RepositoryRoot: repoRoot}, nil
}

func (m *WorktreeManager) authorize(directory string) {
	m.mu.Lock()
	m.authorized[filepath.Clean(directory)] = struct{}{}
	m.mu.Unlock()
}

func (m *WorktreeManager) deauthorize(directory string) {
	m.mu.Lock()
	delete(m.authorized, filepath.Clean(directory))
	m.mu.Unlock()
}

// lockDir serializes operations on one worktree directory both within this
// process (in-process mutex) and across processes (advisory file lock).
func (m *WorktreeManager) lockDir(directory string) (func(), error) {
	clean := filepath.Clean(directory)
	inproc := m.lock("dir:" + clean)
	releaseFile, err := m.git.LockDir(context.Background(), clean)
	if err != nil {
		inproc()
		return nil, err
	}
	return func() {
		releaseFile()
		inproc()
	}, nil
}

func (m *WorktreeManager) lockRepo(repoRoot string) func() {
	return m.lock("repo:" + filepath.Clean(repoRoot))
}

// lock returns a function releasing the process-local mutex for key. Callers
// must always acquire the repo lock before the directory lock for the same
// operation, so the two lock classes cannot deadlock.
func (m *WorktreeManager) lock(key string) func() {
	m.mu.Lock()
	mu := m.locks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		m.locks[key] = mu
	}
	m.mu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (m *WorktreeManager) emit(event WorktreeEvent) {
	if m.eventSink == nil {
		return
	}
	m.eventSink.EmitWorktreeEvent(event)
}
