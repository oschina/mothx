package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/imageproc"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/skills"
)

type operationIDContextKey struct{}

// ContextWithOperationID attaches the Runtime-owned stable operation ID to a
// tool invocation. External tools may pass it to an idempotency-aware target.
func ContextWithOperationID(ctx context.Context, operationID string) context.Context {
	if operationID == "" {
		return ctx
	}
	return context.WithValue(ctx, operationIDContextKey{}, operationID)
}

// OperationIDFromContext extracts the stable operation ID, when the Runtime
// was able to claim a durable tool execution record.
func OperationIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(operationIDContextKey{}).(string)
	return value, ok && value != ""
}

// writeFileAtomic writes data to path atomically using a temporary file and rename.
// It preserves the existing file's permissions if the file already exists.
func writeFileAtomic(path string, data []byte) error {
	// Determine target permissions: preserve existing or use default
	perm := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	// Create temp file in the same directory for atomic rename
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// ToolResult represents the result of a tool execution.
// It can contain plain text and optional rich content blocks (e.g. images).
type ToolResult struct {
	Text     string                  // Plain text result (always populated for display/logging)
	Contents []provider.ContentBlock // Rich content blocks (text + images) for the LLM
	Diff     *FileDiff               // Optional structured file diff for UI/reporting
	Plan     *TaskPlan               // Optional structured task plan for UI/reporting
	Insert   *InsertResult           // Optional structured result for the insert tool
}

// InsertResult describes a structural insertion independently of its human-readable text.
type InsertResult struct {
	Path          string
	Changed       bool
	DryRun        bool
	InsertedBytes int
	Position      string
	Line          int
	Offset        int64
	Deduped       bool
}

// FileDiff describes a file change produced by a write-like tool.
type FileDiff struct {
	Path         string
	Added        int
	Deleted      int
	AddedLines   []int
	DeletedLines []int
	Unified      string
	// OldText and NewText retain the complete file contents for protocol
	// projections that require a semantic diff rather than a display patch.
	// OldText is nil when the write created a previously absent file.
	OldText   *string
	NewText   string
	Truncated bool
}

// TaskPlan describes a structured task plan emitted by the plan tool.
type TaskPlan struct {
	Title string
	Steps []PlanStep
	Note  string
}

// PlanStep describes one step in a task plan.
type PlanStep struct {
	Title  string
	Status string
}

// NewTextToolResult creates a plain text tool result.
func NewTextToolResult(text string) ToolResult {
	return ToolResult{Text: text}
}

// NewDiffToolResult creates a text tool result with structured diff metadata.
func NewDiffToolResult(text string, diff *FileDiff) ToolResult {
	return ToolResult{Text: text, Diff: diff}
}

// NewInsertToolResult creates a tool result with insert metadata and optional diff.
func NewInsertToolResult(text string, diff *FileDiff, result *InsertResult) ToolResult {
	return ToolResult{Text: text, Diff: diff, Insert: result}
}

func NewPlanToolResult(text string, plan *TaskPlan) ToolResult {
	return ToolResult{Text: text, Plan: plan}
}

// NewImageToolResult creates a tool result that includes an image.
// text is the human-readable description, mimeType and base64Data are the image payload.
func NewImageToolResult(text, mimeType, base64Data string) ToolResult {
	return NewImageToolResultWithContent(text, provider.ImageContent{MimeType: mimeType, Data: base64Data})
}

// NewImageToolResultWithContent creates a tool result with a fully populated image payload.
func NewImageToolResultWithContent(text string, image provider.ImageContent) ToolResult {
	return ToolResult{
		Text: text,
		Contents: []provider.ContentBlock{
			{Type: "text", Text: text},
			{Type: "image", Image: &image},
		},
	}
}

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the tool's name.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// PromptSnippet returns a short one-line description for the system prompt's Available tools section.
	PromptSnippet() string

	// PromptGuidelines returns guideline bullets for the system prompt's Guidelines section.
	PromptGuidelines() []string

	// Parameters returns the JSON Schema for the tool's parameters.
	Parameters() json.RawMessage

	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}

// ExecutionTimeoutProvider lets a tool override the agent's default execution
// timeout. The bool reports whether an override is provided. A non-positive
// duration disables the agent-level deadline while preserving parent
// cancellation.
type ExecutionTimeoutProvider interface {
	ExecutionTimeout(params map[string]any) (time.Duration, bool)
}

// ToolDefinition converts a Tool to a provider.ToolDefinition.
func ToolDefinition(t Tool) provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}

// Registry manages available tools.
type Registry struct {
	mu             sync.RWMutex
	tools          map[string]Tool
	order          []string
	sandbox        sandbox.Sandbox
	workDir        string
	jobManager     *JobManager
	skillsMgr      *skills.Manager
	fileLocks      *FileLockManager
	imageHint      imageproc.Hint
	envVars        map[string]string
	additionalDirs []string
}

// NewRegistry creates a new tool registry.
func NewRegistry(workDir string, sb sandbox.Sandbox) *Registry {
	return &Registry{
		tools:      make(map[string]Tool),
		workDir:    workDir,
		sandbox:    sb,
		jobManager: NewJobManager(),
		fileLocks:  DefaultFileLockManager(),
		envVars:    config.LoadEnv().List(),
	}
}

// RegistryConfig configures a Registry instance.
type RegistryConfig struct {
	WorkDir        string
	Sandbox        sandbox.Sandbox
	ToolFilter     []string          // optional: only register these tools (empty = all)
	SkillsMgr      *skills.Manager   // optional: skills manager for skill_ref tool
	EnablePlanTool *bool             // optional: defaults to true when nil
	FileLocks      *FileLockManager  // optional: defaults to process-wide manager
	ImageHint      imageproc.Hint    // optional: provider/model hint for image preprocessing
	EnvVars        map[string]string // extra environment variables for bash/skills
}

// NewRegistryWithConfig creates a Registry with the given config.
func NewRegistryWithConfig(cfg RegistryConfig) *Registry {
	fileLocks := cfg.FileLocks
	if fileLocks == nil {
		fileLocks = DefaultFileLockManager()
	}
	r := &Registry{
		tools:      make(map[string]Tool),
		workDir:    cfg.WorkDir,
		sandbox:    cfg.Sandbox,
		jobManager: NewJobManager(),
		skillsMgr:  cfg.SkillsMgr,
		fileLocks:  fileLocks,
		imageHint:  cfg.ImageHint,
		envVars:    copyEnvVars(cfg.EnvVars),
	}
	enablePlanTool := true
	if cfg.EnablePlanTool != nil {
		enablePlanTool = *cfg.EnablePlanTool
	}
	if r.envVars == nil {
		r.envVars = config.LoadEnv().List()
	}
	if len(cfg.ToolFilter) == 0 {
		r.RegisterDefaultsWithPlanTool(enablePlanTool)
	} else {
		r.RegisterFiltered(cfg.ToolFilter)
	}
	return r
}

func copyEnvVars(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// EnvVars returns the extra environment variables for command execution.
func (r *Registry) EnvVars() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return copyEnvVars(r.envVars)
}

func (r *Registry) SetImageHint(h imageproc.Hint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.imageHint = h
}

// ImagePolicy returns the image preprocessing policy for the current registry context.
func (r *Registry) ImagePolicy(mode imageproc.Mode) imageproc.Policy {
	r.mu.RLock()
	hint := r.imageHint
	r.mu.RUnlock()
	return imageproc.PolicyForHint(hint, mode)
}

// JobManager returns the registry's per-instance job manager.
func (r *Registry) JobManager() *JobManager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.jobManager
}

// FileLocks returns the registry's in-memory file lock manager.
func (r *Registry) FileLocks() *FileLockManager {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fileLocks
}

func (r *Registry) acquireFileLock(ctx context.Context, path, owner string) (func(), error) {
	mgr := r.FileLocks()
	if mgr == nil {
		return func() {}, nil
	}
	return mgr.Acquire(ctx, path, owner)
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := t.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Remove removes a tool by name. No-op if not found.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		delete(r.tools, name)
		// Also remove from order
		for i, n := range r.order {
			if n == name {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
}

// All returns all registered tools in order.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Tool
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

// Definitions returns tool definitions for all registered tools.
func (r *Registry) Definitions() []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var defs []provider.ToolDefinition
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			defs = append(defs, ToolDefinition(t))
		}
	}
	return defs
}

// GetSandbox returns the registry's sandbox.
func (r *Registry) GetSandbox() sandbox.Sandbox {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sandbox
}

// GetWorkDir returns the registry's working directory.
func (r *Registry) GetWorkDir() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workDir
}

// SetAdditionalDirectories grants this session's canonical workspace roots to
// path resolution and command sandbox projection.
func (r *Registry) SetAdditionalDirectories(directories []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.additionalDirs = append([]string(nil), directories...)
}

func (r *Registry) GetAdditionalDirectories() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.additionalDirs...)
}

// ResolvePath resolves a user-provided path to an absolute path constrained to the work directory.
func (r *Registry) ResolvePath(path string) (string, error) {
	r.mu.RLock()
	workDir := r.workDir
	additionalDirs := append([]string(nil), r.additionalDirs...)
	r.mu.RUnlock()

	// Expand ~ (only ~/ prefix, not arbitrary ~user)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = home
	} else if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}

	// Convert relative paths to absolute within workDir
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}

	// Clean to resolve .. segments
	path = filepath.Clean(path)

	// Validate: path must stay within the base or a session-granted root.
	workDir = filepath.Clean(workDir)
	rel, err := filepath.Rel(workDir, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path, nil
	}
	for _, root := range additionalDirs {
		rel, relErr := filepath.Rel(filepath.Clean(root), path)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return path, nil
		}
	}
	return "", fmt.Errorf("path %s escapes session workspace roots", path)
}

// SetSandbox updates the sandbox used by tools.
func (r *Registry) SetSandbox(sb sandbox.Sandbox) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sandbox = sb
}

// builtinToolOrder lists the built-in tools in registration order. Full and
// filtered registration both derive from builtinToolFactories so adding a
// built-in tool changes exactly one list (ModeTools keeps its own mode policy).
var builtinToolOrder = []string{"read", "ls", "grep", "find", "plan", "write", "edit", "insert", "bash", "jobs", "kill", "skill_ref"}

// builtinToolFactories maps canonical built-in names to constructors. bash,
// jobs, and kill share one bash tool instance per registry.
func (r *Registry) builtinToolFactories() map[string]func() Tool {
	bashTool := NewBashToolWithJM(r, r.jobManager)
	factories := map[string]func() Tool{
		"read":   func() Tool { return NewReadTool(r) },
		"ls":     func() Tool { return NewLsTool(r) },
		"grep":   func() Tool { return NewGrepTool(r) },
		"find":   func() Tool { return NewFindTool(r) },
		"plan":   func() Tool { return NewPlanTool(r) },
		"write":  func() Tool { return NewWriteTool(r) },
		"edit":   func() Tool { return NewEditTool(r) },
		"insert": func() Tool { return NewInsertTool(r) },
		"bash":   func() Tool { return bashTool },
		"jobs":   func() Tool { return NewJobsTool(r, bashTool) },
		"kill":   func() Tool { return NewKillTool(r, bashTool) },
	}
	if r.skillsMgr != nil {
		factories["skill_ref"] = func() Tool { return NewSkillRefTool(r.skillsMgr) }
	}
	return factories
}

// RegisterDefaults registers all default tools.
func (r *Registry) RegisterDefaults() {
	r.RegisterDefaultsWithPlanTool(true)
}

// RegisterDefaultsWithPlanTool registers all default tools, optionally including the plan tool.
func (r *Registry) RegisterDefaultsWithPlanTool(enablePlanTool bool) {
	factories := r.builtinToolFactories()
	for _, name := range builtinToolOrder {
		if name == "plan" && !enablePlanTool {
			continue
		}
		if factory, ok := factories[name]; ok {
			r.Register(factory())
		}
	}
}

// RegisterFiltered registers only the specified tools by name.
func (r *Registry) RegisterFiltered(toolNames []string) {
	factories := r.builtinToolFactories()
	for _, name := range toolNames {
		if factory, ok := factories[name]; ok {
			r.Register(factory())
		}
	}
}

// ModeTools returns tool definitions appropriate for the given mode.
func (r *Registry) ModeTools(mode string) []provider.ToolDefinition {
	switch mode {
	case "plan":
		// Plan mode: read-only tools + any extras like question
		var defs []provider.ToolDefinition
		for _, t := range r.All() {
			switch t.Name() {
			case "read", "grep", "find", "ls", "plan":
				defs = append(defs, ToolDefinition(t))
			case "question":
				defs = append(defs, ToolDefinition(t))
			}
		}
		return defs
	case "agent":
		// Agent mode: all tools, including the interactive question tool.
		var defs []provider.ToolDefinition
		for _, t := range r.All() {
			defs = append(defs, ToolDefinition(t))
		}
		return defs
	case "os":
		// OS mode has YOLO execution permissions but exposes only bash.
		for _, t := range r.All() {
			if t.Name() == "bash" {
				return []provider.ToolDefinition{ToolDefinition(t)}
			}
		}
		return nil
	default:
		// YOLO (and unattended modes): all tools except question (interactive only)
		var defs []provider.ToolDefinition
		for _, t := range r.All() {
			if t.Name() == "question" {
				continue
			}
			defs = append(defs, ToolDefinition(t))
		}
		return defs
	}
}

// ToolSnippets returns prompt snippets for the given tool names.
func (r *Registry) ToolSnippets(toolNames []string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snippets := make(map[string]string)
	for _, name := range toolNames {
		if t, ok := r.tools[name]; ok {
			if snippet := t.PromptSnippet(); snippet != "" {
				snippets[name] = snippet
			}
		}
	}
	return snippets
}

// ToolGuidelines returns prompt guidelines for the given tool names.
func (r *Registry) ToolGuidelines(toolNames []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var guidelines []string
	seen := make(map[string]bool)
	for _, name := range toolNames {
		if t, ok := r.tools[name]; ok {
			for _, g := range t.PromptGuidelines() {
				if !seen[g] {
					seen[g] = true
					guidelines = append(guidelines, g)
				}
			}
		}
	}
	return guidelines
}
