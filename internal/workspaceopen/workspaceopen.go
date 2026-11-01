// Package workspaceopen is the ONE owner of external-launch policy for a
// session working directory. Serve and (later) ACP are thin projections:
// they resolve the root from their canonical session source, apply their
// transport gating, and call this package. The launchers run on the machine
// hosting the process with that process's user privileges — the same trust
// class as the interactive terminal — so availability follows the terminal
// policy (loopback listener, or auth-enabled non-loopback with a real token),
// never a weaker one. Nothing here goes through a shell string: every action
// is an allowlisted argv whose target was already contained by
// internal/workspacefiles.
package workspaceopen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/oschina/mothx/internal/workspacefiles"
)

// ErrUnavailable reports that no launcher for the requested action exists
// on this host (or no graphical session is present for GUI launchers).
var ErrUnavailable = errors.New("launcher unavailable")

// Action enumerates the allowlisted launch kinds.
type Action string

const (
	ActionReveal   Action = "reveal"
	ActionTerminal Action = "terminal"
	ActionEditor   Action = "editor"
)

// Editor names the supported external editors. The wire values match the
// binary names so clients can gate menu entries on Openers output.
type Editor string

const (
	EditorVSCode   Editor = "code"
	EditorVSCodium Editor = "codium"
	EditorCursor   Editor = "cursor"
)

// Openers is the capability projection that gates front-end menus.
type Openers struct {
	Reveal           bool     `json:"reveal"`
	SystemTerminal   bool     `json:"systemTerminal"`
	EditorCandidates []string `json:"editorCandidates"`
}

// OpenerPolicy mirrors EffectiveTerminalEnabled: launching local programs is
// allowed under exactly the same listener/auth conditions as the PTY dock.
type OpenerPolicy interface {
	TerminalEnabled() bool
}

// ErrRootUnavailable reports a session whose canonical work directory cannot
// be resolved. Adapters map it to their own not-found wire shape.
var ErrRootUnavailable = errors.New("workspace root unavailable")

// RootResolver supplies the ONE canonical working-directory resolution for a
// session. Serve implements it through activeSessionManager/GetSessionWorkDir;
// ACP will implement it through its sessionRuntime + agentruntime.ValidateWorkDir.
// Keeping this an interface is the migration seam: adapters must not grow a
// second resolver or accept client-supplied absolute roots.
type RootResolver interface {
	// ResolveRoot returns the session's canonical work directory, or "" plus
	// ErrRootUnavailable when the session/root is unknown or fails validation.
	ResolveRoot(ctx context.Context, sessionID string) (string, error)
}

// Request is the transport-neutral input for one launch. Target is ALWAYS
// relative to the resolved root; absolute paths are rejected by containment.
type Request struct {
	SessionID string
	Action    Action
	Target    string
	Editor    Editor
}

// Response records the planned argv (without secrets) and result of a launch.
type Response struct {
	Opened bool     `json:"opened"`
	Action Action   `json:"action"`
	Argv   []string `json:"-"`
}

// Service launches allowlisted actions inside one resolved root. A service
// instance is created once per process by each adapter with its own
// RootResolver and policy; it holds no mutable state.
type Service struct {
	roots  RootResolver
	policy OpenerPolicy
}

// New returns the shared launcher service bound to one adapter's canonical
// root resolver and enablement policy.
func New(roots RootResolver, policy OpenerPolicy) *Service {
	return &Service{roots: roots, policy: policy}
}

// currentGOOS is a variable so opener-table tests stay platform independent.
var currentGOOS = runtime.GOOS

// hasGraphicalSession guards the Unix launchers: without DISPLAY or a Wayland
// session no file manager/terminal/editor window can appear, and a failed
// launch is indistinguishable from a cancel (same policy as the directory
// picker).
func hasGraphicalSession() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

var editorCommands = []string{"code", "codium", "cursor"}

var darwinEditorApps = map[string][]string{
	"code":   {"Visual Studio Code", "Code"},
	"codium": {"VSCodium"},
	"cursor": {"Cursor"},
}

// ProbeOpeners reports which external launchers this host can actually run
// right now. It performs no launches.
func ProbeOpeners() Openers {
	out := Openers{EditorCandidates: []string{}}
	switch currentGOOS {
	case "darwin":
		out.Reveal = true // /usr/bin/open is always present
		out.SystemTerminal = true
	case "windows":
		out.Reveal = true // explorer.exe is always present
		out.SystemTerminal = lookupAny("wt.exe", "wt")
	default:
		if hasGraphicalSession() {
			out.Reveal = lookupAny("xdg-open", "gio")
			out.SystemTerminal = unixTerminalLauncher() != ""
		}
	}
	for _, editor := range editorCommands {
		if editorAvailable(editor) {
			out.EditorCandidates = append(out.EditorCandidates, editor)
		}
	}
	return out
}

func lookupAny(names ...string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func editorAvailable(name string) bool {
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	if currentGOOS == "darwin" {
		for _, app := range darwinEditorApps[name] {
			if _, err := os.Stat("/Applications/" + app + ".app"); err == nil {
				return true
			}
		}
	}
	return false
}

// unixTerminalLauncher returns the first known terminal emulator that can be
// started directly in a given working directory without a shell string.
func unixTerminalLauncher() string {
	for _, name := range []string{"gnome-terminal", "konsole", "xfce4-terminal", "kitty", "alacritty", "foot", "xterm"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// RevealCommand launches the platform file manager at (or revealing) target.
func RevealCommand(goos, target string, isDir bool) []string {
	switch goos {
	case "darwin":
		if isDir {
			return []string{"open", target}
		}
		return []string{"open", "-R", target}
	case "windows":
		if isDir {
			return []string{"explorer.exe", target}
		}
		return []string{"explorer.exe", "/select," + target}
	default:
		bin := "xdg-open"
		if _, err := exec.LookPath("xdg-open"); err != nil {
			if _, err := exec.LookPath("gio"); err == nil {
				bin = "gio"
			} else {
				return nil
			}
		}
		if isDir {
			return []string{bin, target}
		}
		return []string{bin, filepath.Dir(target)}
	}
}

// SystemTerminalCommand opens a new system terminal window in dir.
func SystemTerminalCommand(goos, dir string) []string {
	switch goos {
	case "darwin":
		return []string{"open", "-a", "Terminal", dir}
	case "windows":
		if lookupAny("wt.exe", "wt") {
			return []string{"wt", "-d", dir}
		}
		return nil
	default:
		switch bin := unixTerminalLauncher(); bin {
		case "":
			return nil
		case "gnome-terminal":
			return []string{bin, "--working-directory", dir}
		case "konsole":
			return []string{bin, "--new-tab", "--working-directory", dir}
		case "xfce4-terminal", "kitty", "alacritty", "foot":
			return []string{bin, "--working-directory", dir}
		case "xterm":
			return []string{bin, "+cd", dir}
		default:
			return nil
		}
	}
}

// EditorCommand opens the named editor at a directory or file.
func EditorCommand(goos, editor, target string) []string {
	switch editor {
	case "code", "codium", "cursor":
	default:
		return nil
	}
	if goos == "darwin" && editorAvailable(editor) {
		if _, err := exec.LookPath(editor); err != nil {
			for _, app := range darwinEditorApps[editor] {
				if _, err := os.Stat("/Applications/" + app + ".app"); err == nil {
					return []string{"open", "-a", app, target}
				}
			}
			return nil
		}
	}
	return []string{editor, target}
}

// Plan resolves an action plus a target already contained by the adapter
// (via workspacefiles.Resolve) into the concrete argv, or ErrUnavailable when
// no launcher exists. Adapters call this before spawning so they can
// log/audit the exact command without executing it.
func (s *Service) Plan(action Action, target string, isDir bool, editor Editor) ([]string, error) {
	if strings.TrimSpace(target) == "" {
		return nil, ErrUnavailable
	}
	switch action {
	case ActionReveal:
		if argv := RevealCommand(currentGOOS, target, isDir); argv != nil {
			return argv, nil
		}
	case ActionTerminal:
		dir := target
		if !isDir {
			dir = filepath.Dir(target)
		}
		if argv := SystemTerminalCommand(currentGOOS, dir); argv != nil {
			return argv, nil
		}
	case ActionEditor:
		if argv := EditorCommand(currentGOOS, string(editor), target); argv != nil {
			return argv, nil
		}
	default:
		return nil, ErrUnavailable
	}
	return nil, ErrUnavailable
}

// Open is the single entry point every adapter calls: it checks the gate,
// resolves the session root through the adapter's canonical resolver, applies
// workspacefiles containment, plans the allowlisted argv, and spawns it
// detached. It returns the planned argv for audit logging without exposing
// secrets (paths only).
func (s *Service) Open(ctx context.Context, req Request) (Response, error) {
	if s.policy != nil && !s.policy.TerminalEnabled() {
		return Response{}, ErrUnavailable
	}
	root, err := s.roots.ResolveRoot(ctx, strings.TrimSpace(req.SessionID))
	if err != nil || root == "" {
		return Response{}, ErrRootUnavailable
	}
	target, resolveErr := workspacefiles.Resolve(root, req.Target)
	if resolveErr != nil {
		return Response{}, resolveErr
	}
	info, statErr := os.Stat(target)
	if statErr != nil {
		return Response{}, ErrRootUnavailable
	}
	argv, planErr := s.Plan(req.Action, target, info.IsDir(), req.Editor)
	if planErr != nil {
		return Response{}, planErr
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if startErr := cmd.Start(); startErr != nil {
		return Response{}, startErr
	}
	return Response{Opened: true, Action: req.Action, Argv: argv}, nil
}
