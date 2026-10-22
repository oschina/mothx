package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/browser"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func TestSessionRuntimeRefreshRejectsUnknownActiveSkillWithoutReplacingContext(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false

	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: workDir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer runtime.Close()
	before := runtime.ExtraContext
	if err := runtime.RefreshResources(settings, RefreshOptions{ActiveSkills: map[string]bool{"missing": true}}); err == nil || !strings.Contains(err.Error(), "skill not found") {
		t.Fatalf("RefreshResources unknown skill error = %v, want skill not found", err)
	}
	if runtime.ExtraContext != before {
		t.Fatal("failed refresh replaced runtime context")
	}
}

func TestLoadContextResourcesUsesBuiltInBrowserSkillWithoutProjectWrite(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false

	resources, err := LoadContextResources(settings, workDir, false, true)
	if err != nil {
		t.Fatalf("LoadContextResources: %v", err)
	}
	projectSkillsDir := filepath.Join(workDir, ".skills")
	if _, err := os.Stat(projectSkillsDir); !os.IsNotExist(err) {
		t.Fatalf("runtime created project skills directory %s: %v", projectSkillsDir, err)
	}
	if resources.SkillsMgr == nil {
		t.Fatal("runtime did not load skills")
	}
	skill := resources.SkillsMgr.Get(browser.SkillName)
	if skill == nil || skill.Source != "builtin" {
		t.Fatalf("browser skill = %#v, want built-in skill", skill)
	}
	if !strings.Contains(resources.ExtraContext, "## Active Skill: "+browser.SkillName) {
		t.Fatalf("browser skill is not active in runtime context:\n%s", resources.ExtraContext)
	}
}

func TestSessionRuntimeSynchronizeCoreTools(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: workDir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer runtime.Close()

	runtime.SynchronizeCoreTools(true, true)
	if _, browserEnabled, webSearchEnabled := runtime.CapabilitySnapshot(); !browserEnabled || !webSearchEnabled {
		t.Fatalf("capability snapshot = (%v, %v), want (true, true)", browserEnabled, webSearchEnabled)
	}
	if _, ok := runtime.Registry.Get("browser"); !ok {
		t.Fatal("browser tool was not registered")
	}
	if _, ok := runtime.Registry.Get("web_search"); !ok {
		t.Fatal("web_search tool was not registered")
	}
	if _, ok := runtime.Registry.Get("web_fetch"); !ok {
		t.Fatal("web_fetch tool was not registered")
	}
	runtime.SynchronizeCoreTools(false, false)
	if _, browserEnabled, webSearchEnabled := runtime.CapabilitySnapshot(); browserEnabled || webSearchEnabled {
		t.Fatalf("capability snapshot = (%v, %v), want (false, false)", browserEnabled, webSearchEnabled)
	}
	if _, ok := runtime.Registry.Get("browser"); ok {
		t.Fatal("browser tool was not removed")
	}
	if _, ok := runtime.Registry.Get("web_search"); ok {
		t.Fatal("web_search tool was not removed")
	}
	if _, ok := runtime.Registry.Get("web_fetch"); ok {
		t.Fatal("web_fetch tool was not removed")
	}
}

func TestSessionRuntimeRefreshResourceSettingsUpdatesCoreTools(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: workDir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer runtime.Close()

	if err := runtime.ConfigureCapabilities(false, false, true); err != nil {
		t.Fatalf("ConfigureCapabilities: %v", err)
	}
	if _, ok := runtime.Registry.Get("web_fetch"); !ok {
		t.Fatal("web_fetch was not registered for the DeepSeek backend")
	}

	// A settings refresh must rebuild settings-backed tools: Yuanbao is
	// search-only, so web_fetch has to disappear without rebuilding the
	// session or touching its capability toggles.
	next := *settings
	next.WebSearch.Enabled = config.BoolPtr(true)
	next.WebSearch.Backend = config.WebSearchBackendYuanbao
	runtime.RefreshResourceSettings(&next)
	if _, ok := runtime.Registry.Get("web_search"); !ok {
		t.Fatal("web_search was not registered after settings refresh")
	}
	if _, ok := runtime.Registry.Get("web_fetch"); ok {
		t.Fatal("web_fetch must be removed after switching to the Yuanbao backend")
	}
}

func TestBuilderAppliesRegistryHooks(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		WorkDir: t.TempDir(),
		RegistryHooks: []RegistryHook{func(runtime *SessionRuntime) error {
			runtime.Registry.Register(testRegistryTool{})
			return nil
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer runtime.Close()
	if _, ok := runtime.Registry.Get("runtime_test_hook"); !ok {
		t.Fatal("registry hook tool was not registered")
	}

	_, err = (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		WorkDir: t.TempDir(), RegistryHooks: []RegistryHook{func(*SessionRuntime) error { return errors.New("hook failure") }},
	})
	if err == nil || !strings.Contains(err.Error(), "hook failure") {
		t.Fatalf("Build hook error = %v, want hook failure", err)
	}
}

func TestRegistryHookCanReadRuntimeState(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	workDir := t.TempDir()
	type buildResult struct {
		runtime *SessionRuntime
		err     error
	}
	done := make(chan buildResult, 1)
	go func() {
		runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
			WorkDir: workDir,
			RegistryHooks: []RegistryHook{func(runtime *SessionRuntime) error {
				_ = SubAgentToolsEnabled(runtime, false)
				return nil
			}},
		})
		done <- buildResult{runtime: runtime, err: err}
	}()
	select {
	case result := <-done:
		if result.runtime != nil {
			defer result.runtime.Close()
		}
		if result.err != nil {
			t.Fatalf("Build: %v", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registry hook deadlocked while reading Runtime state")
	}
}

type testRegistryTool struct{}

func (testRegistryTool) Name() string                { return "runtime_test_hook" }
func (testRegistryTool) Description() string         { return "test hook" }
func (testRegistryTool) PromptSnippet() string       { return "test hook" }
func (testRegistryTool) PromptGuidelines() []string  { return nil }
func (testRegistryTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (testRegistryTool) Execute(context.Context, map[string]any) (tools.ToolResult, error) {
	return tools.ToolResult{}, nil
}

func TestBuilderBuildsSharedSessionResources(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false

	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{
		ID:      "runtime-test",
		Source:  SourceWebUI,
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer runtime.Close()

	if runtime.Registry == nil || runtime.SandboxMgr == nil || runtime.SkillsMgr == nil {
		t.Fatalf("incomplete runtime: %#v", runtime)
	}
	if _, ok := runtime.Registry.Get("bash"); !ok {
		t.Fatal("default tools were not registered")
	}
	if runtime.Source != SourceWebUI || runtime.ID != "runtime-test" || runtime.WorkDir != workDir {
		t.Fatalf("runtime identity = %#v", runtime)
	}
}
