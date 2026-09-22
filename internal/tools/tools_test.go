package tools

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/imageproc"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
)

func TestOperationIDContext(t *testing.T) {
	ctx := ContextWithOperationID(context.Background(), "tool:stable-1")
	if got, ok := OperationIDFromContext(ctx); !ok || got != "tool:stable-1" {
		t.Fatalf("operation ID = %q, ok=%t", got, ok)
	}
	if _, ok := OperationIDFromContext(context.Background()); ok {
		t.Fatal("operation ID unexpectedly present on empty context")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

type wrappingTestSandbox struct{}

type capturingTestSandbox struct {
	opts sandbox.ExecOpts
}

func (s *capturingTestSandbox) WrapCommand(ctx context.Context, shell, cmd string, opts sandbox.ExecOpts) *exec.Cmd {
	s.opts = opts
	return exec.CommandContext(ctx, "true")
}

func (*capturingTestSandbox) IsAvailable() bool { return true }
func (*capturingTestSandbox) Name() string      { return "capturing-test" }
func (*capturingTestSandbox) Level() sandbox.Level {
	return sandbox.LevelStandard
}

func (wrappingTestSandbox) WrapCommand(ctx context.Context, shell, cmd string, opts sandbox.ExecOpts) *exec.Cmd {
	c := exec.CommandContext(ctx, shell, platform.ShellArgs(shell, cmd)...)
	c.Dir = opts.WorkDir
	c.Env = os.Environ()
	return c
}

func (wrappingTestSandbox) IsAvailable() bool { return true }
func (wrappingTestSandbox) Name() string      { return "test" }
func (wrappingTestSandbox) Level() sandbox.Level {
	return sandbox.LevelStandard
}

func TestNewRegistry(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)

	if r.GetWorkDir() != "/tmp" {
		t.Errorf("expected workdir '/tmp', got '%s'", r.GetWorkDir())
	}
}

func TestRegisterAndGet(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)

	tool := NewReadTool(r)
	r.Register(tool)

	// Get existing tool
	got, ok := r.Get("read")
	if !ok {
		t.Fatal("expected to get 'read' tool")
	}

	if got.Name() != "read" {
		t.Errorf("expected name 'read', got '%s'", got.Name())
	}

	// Get non-existing tool
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("expected not to get 'nonexistent' tool")
	}
}

func TestRegisterDefaults(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	expectedTools := []string{"read", "write", "edit", "insert", "bash", "jobs", "kill", "grep", "find", "ls", "plan"}

	for _, name := range expectedTools {
		_, ok := r.Get(name)
		if !ok {
			t.Errorf("expected to get '%s' tool", name)
		}
	}
}

func TestRegisterDefaultsWithPlanToolDisabled(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaultsWithPlanTool(false)

	if _, ok := r.Get("plan"); ok {
		t.Fatal("expected plan tool to be disabled")
	}
}

func TestModeTools(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	// Plan mode - only read-only tools
	planTools := r.ModeTools("plan")
	planToolNames := make(map[string]bool)
	for _, tool := range planTools {
		planToolNames[tool.Name] = true
	}

	if !planToolNames["read"] {
		t.Error("expected 'read' in plan mode")
	}

	if !planToolNames["grep"] {
		t.Error("expected 'grep' in plan mode")
	}

	if planToolNames["write"] {
		t.Error("expected no 'write' in plan mode")
	}
	if !planToolNames["plan"] {
		t.Error("expected 'plan' in plan mode")
	}

	if planToolNames["bash"] {
		t.Error("expected no 'bash' in plan mode")
	}

	// Agent mode - all tools
	agentTools := r.ModeTools("agent")
	if len(agentTools) != 11 {
		t.Errorf("expected 11 tools in agent mode, got %d", len(agentTools))
	}
}

func TestPlanToolExecute(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewPlanTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"title": "Ship feature",
		"steps": []any{
			map[string]any{"title": "Read code", "status": "done"},
			map[string]any{"title": "Implement change", "status": "running"},
		},
		"note": "Keep scope small",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Plan == nil {
		t.Fatal("expected structured plan")
	}
	if result.Plan.Title != "Ship feature" {
		t.Fatalf("plan title = %q, want Ship feature", result.Plan.Title)
	}
	if len(result.Plan.Steps) != 2 || result.Plan.Steps[1].Status != "running" {
		t.Fatalf("plan steps = %#v", result.Plan.Steps)
	}
	if !strings.Contains(result.Text, "[running] Implement change") {
		t.Fatalf("expected formatted plan text, got: %s", result.Text)
	}
}

func TestReadTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewReadTool(r)

	if tool.Name() != "read" {
		t.Errorf("expected name 'read', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}

	if tool.Parameters() == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestReadToolExecute(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("Hello, World!"), 0644)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewReadTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
}

func TestReadToolImage(t *testing.T) {
	tmpDir := t.TempDir()

	tmpFile := filepath.Join(tmpDir, "test.png")
	writeTestPNG(t, tmpFile, 1, 1)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewReadTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.png",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have text description
	if result.Text == "" {
		t.Error("expected non-empty text result")
	}
	if !strings.Contains(result.Text, "Image file") {
		t.Errorf("expected 'Image file' in text, got '%s'", result.Text)
	}

	// Should have rich contents with image block
	if len(result.Contents) != 2 {
		t.Fatalf("expected 2 content blocks (text + image), got %d", len(result.Contents))
	}
	if result.Contents[0].Type != "text" {
		t.Errorf("expected first block type 'text', got '%s'", result.Contents[0].Type)
	}
	if result.Contents[1].Type != "image" {
		t.Errorf("expected second block type 'image', got '%s'", result.Contents[1].Type)
	}
	if result.Contents[1].Image == nil {
		t.Fatal("expected non-nil image content")
	}
	if result.Contents[1].Image.MimeType != "image/png" {
		t.Errorf("expected mime type 'image/png', got '%s'", result.Contents[1].Image.MimeType)
	}
	if result.Contents[1].Image.Data == "" {
		t.Error("expected non-empty base64 data")
	}
	if result.Contents[1].Image.Width != 1 || result.Contents[1].Image.Height != 1 {
		t.Fatalf("image size = %dx%d, want 1x1", result.Contents[1].Image.Width, result.Contents[1].Image.Height)
	}
	if result.Contents[1].Image.OriginalWidth != 1 || result.Contents[1].Image.OriginalHeight != 1 {
		t.Fatalf("original size = %dx%d, want 1x1", result.Contents[1].Image.OriginalWidth, result.Contents[1].Image.OriginalHeight)
	}
	if result.Contents[1].Image.Detail != "auto" {
		t.Fatalf("detail = %q, want auto", result.Contents[1].Image.Detail)
	}
}

func TestReadToolImageResize(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.png")
	writeTestPNG(t, tmpFile, 200, 100)

	r := NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	tool := NewReadTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":        "large.png",
		"maxLongEdge": float64(50),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	image := result.Contents[1].Image
	if image.Width != 50 || image.Height != 25 {
		t.Fatalf("image size = %dx%d, want 50x25", image.Width, image.Height)
	}
	if image.OriginalWidth != 200 || image.OriginalHeight != 100 {
		t.Fatalf("original size = %dx%d, want 200x100", image.OriginalWidth, image.OriginalHeight)
	}
	if !strings.Contains(result.Text, "original: 200x100") || !strings.Contains(result.Text, "sent: 50x25") {
		t.Fatalf("description missing resize details: %s", result.Text)
	}
}

func TestReadToolImagePolicyUsesRegistryHint(t *testing.T) {
	r := NewRegistry(t.TempDir(), sandbox.NewNoneSandbox())
	r.SetImageHint(imageproc.Hint{
		ProviderID: "amazon-bedrock",
		ModelID:    "anthropic.claude-sonnet-4-5-20250929-v1:0",
	})
	tool := NewReadTool(r)

	policy, err := tool.imageReadPolicy(map[string]any{"imageMode": "detail"})
	if err != nil {
		t.Fatalf("imageReadPolicy() error = %v", err)
	}
	if policy.MaxFileBytes != 4<<20 {
		t.Fatalf("MaxFileBytes = %d, want %d", policy.MaxFileBytes, 4<<20)
	}
	if policy.MaxOutputBytes != 3<<20 {
		t.Fatalf("MaxOutputBytes = %d, want %d", policy.MaxOutputBytes, 3<<20)
	}
}

func TestReadToolImageCrop(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "crop.png")
	writeTestPNG(t, tmpFile, 100, 80)

	r := NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	tool := NewReadTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "crop.png",
		"crop": map[string]any{
			"x":      float64(10),
			"y":      float64(12),
			"width":  float64(40),
			"height": float64(20),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	image := result.Contents[1].Image
	if image.Width != 40 || image.Height != 20 {
		t.Fatalf("image size = %dx%d, want 40x20", image.Width, image.Height)
	}
	if !image.Cropped || image.CropX != 10 || image.CropY != 12 || image.CropWidth != 40 || image.CropHeight != 20 {
		t.Fatalf("crop meta = %+v, want 40x20+10+12", image)
	}
	if !strings.Contains(result.Text, "crop: 40x20+10+12") {
		t.Fatalf("description missing crop details: %s", result.Text)
	}
}

func TestReadToolImageModes(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "modes.png")
	writeTestPNG(t, tmpFile, 2400, 1200)

	r := NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	tool := NewReadTool(r)

	tests := []struct {
		mode     string
		wantW    int
		wantH    int
		wantMode string
	}{
		{mode: "fast", wantW: 1024, wantH: 512, wantMode: "fast"},
		{mode: "detail", wantW: 2048, wantH: 1024, wantMode: "detail"},
		{mode: "raw", wantW: 2400, wantH: 1200, wantMode: "raw"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), map[string]any{
				"path":      "modes.png",
				"imageMode": tt.mode,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			image := result.Contents[1].Image
			if image.Width != tt.wantW || image.Height != tt.wantH {
				t.Fatalf("image size = %dx%d, want %dx%d", image.Width, image.Height, tt.wantW, tt.wantH)
			}
			if image.Detail != tt.wantMode {
				t.Fatalf("detail = %q, want %q", image.Detail, tt.wantMode)
			}
		})
	}
}

func TestReadToolBadImage(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "bad.png")
	if err := os.WriteFile(tmpFile, []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	tool := NewReadTool(r)

	_, err := tool.Execute(context.Background(), map[string]any{"path": "bad.png"})
	if err == nil || !strings.Contains(err.Error(), "cannot read image file") {
		t.Fatalf("err = %v, want cannot read image file", err)
	}
}

func TestReadToolImageTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.png")
	if err := os.WriteFile(tmpFile, make([]byte, imageproc.DefaultMaxFileBytes+1), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	tool := NewReadTool(r)

	_, err := tool.Execute(context.Background(), map[string]any{"path": "large.png"})
	if err == nil || !strings.Contains(err.Error(), "image file too large") {
		t.Fatalf("err = %v, want image file too large", err)
	}
}

func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestWriteTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewWriteTool(r)

	if tool.Name() != "write" {
		t.Errorf("expected name 'write', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestWriteToolExecute(t *testing.T) {
	tmpDir := t.TempDir()

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewWriteTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.txt",
		"content": "Hello, World!",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
	if result.Diff == nil {
		t.Fatal("expected structured diff")
	}
	if result.Diff.Added != 1 || result.Diff.Deleted != 0 {
		t.Fatalf("diff = +%d -%d, want +1 -0", result.Diff.Added, result.Diff.Deleted)
	}
	if !strings.Contains(result.Diff.Unified, "+Hello, World!") {
		t.Fatalf("expected unified diff to include added content, got: %s", result.Diff.Unified)
	}

	// Verify file was written
	content, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(content) != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got '%s'", string(content))
	}
}

func TestEditTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewEditTool(r)

	if tool.Name() != "edit" {
		t.Errorf("expected name 'edit', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestEditToolExecute(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("Hello, World!"), 0644)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewEditTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt",
		"edits": []any{
			map[string]any{
				"oldText": "World",
				"newText": "Go",
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
	if result.Diff == nil {
		t.Fatal("expected structured diff")
	}
	if result.Diff.Added != 1 || result.Diff.Deleted != 1 {
		t.Fatalf("diff = +%d -%d, want +1 -1", result.Diff.Added, result.Diff.Deleted)
	}
	if !strings.Contains(result.Text, "Diff: +1 -1") {
		t.Fatalf("expected diff summary in result text, got: %s", result.Text)
	}
	if !strings.Contains(result.Diff.Unified, "-Hello, World!") || !strings.Contains(result.Diff.Unified, "+Hello, Go!") {
		t.Fatalf("expected unified diff replacement, got: %s", result.Diff.Unified)
	}

	// Verify edit was applied
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(content) != "Hello, Go!" {
		t.Errorf("expected 'Hello, Go!', got '%s'", string(content))
	}
}

func TestBashTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	if tool.Name() != "bash" {
		t.Errorf("expected name 'bash', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestBashToolExecute(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
	if !strings.Contains(result.Text, "[runtime]\n") {
		t.Fatalf("expected runtime section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[command]\necho hello") {
		t.Fatalf("expected command section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[stdout]\nhello") {
		t.Fatalf("expected stdout section with command output, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[stderr]\n(no output)") {
		t.Fatalf("expected empty stderr section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[exit_code]\n0") {
		t.Fatalf("expected zero exit code, got: %s", result.Text)
	}
}

func TestBashToolExecuteStderrOnly(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo problem >&2",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "[stdout]\n(no output)") {
		t.Fatalf("expected empty stdout section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[stderr]\nproblem") {
		t.Fatalf("expected stderr section with output, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[exit_code]\n0") {
		t.Fatalf("expected zero exit code, got: %s", result.Text)
	}
}

func TestBashToolExecuteNoOutput(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "true",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "[stdout]\n(no output)") {
		t.Fatalf("expected empty stdout section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[stderr]\n(no output)") {
		t.Fatalf("expected empty stderr section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[exit_code]\n0") {
		t.Fatalf("expected zero exit code, got: %s", result.Text)
	}
}

func TestBashToolExecuteUsesNonInteractiveEnvironment(t *testing.T) {
	if platform.IsWindows() {
		t.Skip("shell env syntax differs on Windows")
	}
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "printf '%s:%s:%s:%s' \"$GIT_TERMINAL_PROMPT\" \"$GIT_ASKPASS\" \"$SSH_ASKPASS\" \"$SSH_ASKPASS_REQUIRE\"",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "[stdout]\n0:true:true:never") {
		t.Fatalf("expected non-interactive auth env, got: %s", result.Text)
	}
}

func TestNonInteractiveEnvKeepsUserValues(t *testing.T) {
	env := nonInteractiveEnv([]string{"GIT_ASKPASS=/custom/askpass"})
	got := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_ASKPASS=") {
			got = entry
		}
	}
	if got != "GIT_ASKPASS=/custom/askpass" {
		t.Fatalf("GIT_ASKPASS = %q, want existing value preserved", got)
	}
}

func TestBashToolExecuteProvidesEOFStdin(t *testing.T) {
	if platform.IsWindows() {
		t.Skip("shell read syntax differs on Windows")
	}
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "if read value; then echo got:$value; else echo eof; fi",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "[stdout]\neof") {
		t.Fatalf("expected stdin EOF, got: %s", result.Text)
	}
}

func TestBashToolExecutionTimeout(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	timeout, ok := tool.ExecutionTimeout(map[string]any{})
	if !ok {
		t.Fatal("expected timeout override to be provided")
	}
	if timeout != 45*time.Second {
		t.Fatalf("default timeout = %s, want 45s", timeout)
	}

	timeout, ok = tool.ExecutionTimeout(map[string]any{"timeout": float64(90)})
	if !ok {
		t.Fatal("expected explicit timeout override")
	}
	if timeout != 90*time.Second {
		t.Fatalf("timeout = %s, want 90s", timeout)
	}

	timeout, ok = tool.ExecutionTimeout(map[string]any{"timeout": float64(0)})
	if !ok {
		t.Fatal("expected zero timeout override")
	}
	if timeout != 0 {
		t.Fatalf("timeout = %s, want 0", timeout)
	}

	timeout, ok = tool.ExecutionTimeout(map[string]any{"async": true})
	if !ok {
		t.Fatal("expected async timeout override")
	}
	if timeout != 0 {
		t.Fatalf("async timeout = %s, want 0", timeout)
	}
}

func TestBashToolExecuteNonZeroExitCode(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo boom >&2; exit 7",
	})

	if err != nil {
		t.Fatalf("expected non-zero exit to be returned as tool output, got error: %v", err)
	}
	if !strings.Contains(result.Text, "[stderr]\nboom") {
		t.Fatalf("expected stderr section with output, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "[exit_code]\n7") {
		t.Fatalf("expected exit code 7, got: %s", result.Text)
	}
}

func TestBashToolSandboxCommandDoesNotWaitForBackgroundChildStdio(t *testing.T) {
	if platform.IsWindows() {
		t.Skip("shell background process syntax differs on Windows")
	}

	tmpDir := t.TempDir()
	r := NewRegistry(tmpDir, wrappingTestSandbox{})
	tool := NewBashTool(r)

	start := time.Now()
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "sh -c 'sleep 2; echo late' & echo started",
		"timeout": float64(0),
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("command waited for background child stdio: elapsed %s, result: %s", elapsed, result.Text)
	}
	if !strings.Contains(result.Text, "[stdout]\nstarted") {
		t.Fatalf("expected foreground output without waiting for background child, got: %s", result.Text)
	}
}

func TestBashToolWindowsSandboxCommandPassesEnvVars(t *testing.T) {
	sb := &capturingTestSandbox{}
	r := NewRegistryWithConfig(RegistryConfig{
		WorkDir: "/tmp",
		Sandbox: sb,
		EnvVars: map[string]string{"MOTHX_TEST_ENV": "value"},
	})
	tool := NewBashTool(r)

	tool.buildWindowsCommand(context.Background(), sb, "powershell.exe", "echo hello", "/tmp", os.Environ(), 120*time.Second)

	if sb.opts.EnvVars == nil {
		t.Fatal("expected sandbox command environment variables to be initialized")
	}
	if got := sb.opts.EnvVars["MOTHX_TEST_ENV"]; got != "value" {
		t.Fatalf("expected sandbox environment variable value, got %q", got)
	}
}

func TestBashToolWindowsBusyboxCommandUsesShC(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	cmd, runtimeLabel := tool.buildWindowsCommand(context.Background(), sb, "C:/Users/test/.mothx/bin/busybox64u.exe", "echo hello", "/tmp", os.Environ(), 120*time.Second)
	if runtimeLabel != "busybox" {
		t.Fatalf("expected runtime label busybox, got %q", runtimeLabel)
	}
	if len(cmd.Args) < 4 {
		t.Fatalf("expected busybox args, got %#v", cmd.Args)
	}
	if cmd.Args[1] != "sh" || cmd.Args[2] != "-c" || cmd.Args[3] != "echo hello" {
		t.Fatalf("expected busybox sh -c command, got %#v", cmd.Args)
	}
}

func TestBashToolAsync(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewBashTool(r)

	// Start async command
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "sleep 1",
		"async":   true,
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}

	// Check job was created
	jm := tool.GetJobManager()
	jobs := jm.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	if jobs[0].ID != 1 {
		t.Errorf("expected job ID 1, got %d", jobs[0].ID)
	}

	// Wait for job to finish
	time.Sleep(2 * time.Second)

	if !jobs[0].IsDone() {
		t.Error("expected job to be done")
	}
}

func TestJobsTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	bashTool := NewBashTool(r)
	jobsTool := NewJobsTool(r, bashTool)

	if jobsTool.Name() != "jobs" {
		t.Errorf("expected name 'jobs', got '%s'", jobsTool.Name())
	}

	// List jobs - should be empty
	result, err := jobsTool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "No background jobs." {
		t.Errorf("expected 'No background jobs.', got '%s'", result.Text)
	}
}

func TestKillTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	bashTool := NewBashTool(r)
	killTool := NewKillTool(r, bashTool)

	if killTool.Name() != "kill" {
		t.Errorf("expected name 'kill', got '%s'", killTool.Name())
	}

	// Try to kill non-existent job
	_, err := killTool.Execute(context.Background(), map[string]any{
		"jobId": float64(999),
	})
	if err == nil {
		t.Error("expected error for non-existent job")
	}
}

func TestGrepTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewGrepTool(r)

	if tool.Name() != "grep" {
		t.Errorf("expected name 'grep', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestGrepToolExecute(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("Hello, World!\nFoo bar\nHello again"), 0644)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewGrepTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "Hello",
		"path":    ".",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
}

func TestGrepToolExecuteIncludeGlob(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "one.go"), []byte("package main\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "two.txt"), []byte("Hello text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewGrepTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "Hello",
		"path":    ".",
		"include": "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "one.go") {
		t.Fatalf("expected .go match, got: %s", result.Text)
	}
	if strings.Contains(result.Text, "two.txt") {
		t.Fatalf("include filter should exclude two.txt, got: %s", result.Text)
	}
}

func TestGrepToolExecuteRespectsGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "kept.go"), []byte("func Kept() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ignored.go"), []byte("func Ignored() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewGrepTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "func",
		"path":    ".",
		"include": "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "kept.go") {
		t.Fatalf("expected kept.go, got: %s", result.Text)
	}
	if strings.Contains(result.Text, "ignored.go") {
		t.Fatalf("expected ignored.go to be excluded, got: %s", result.Text)
	}
}

func TestGrepToolExecuteLimitsTotalResults(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 5; i++ {
		path := filepath.Join(tmpDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(path, []byte("match one\nmatch two\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewGrepTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":    "match",
		"path":       ".",
		"maxResults": float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	matches := 0
	for _, line := range strings.Split(result.Text, "\n") {
		if strings.Contains(line, "match") {
			matches++
		}
	}
	if matches != 3 {
		t.Fatalf("matches = %d, want 3; output:\n%s", matches, result.Text)
	}
	if !strings.Contains(result.Text, "truncated") {
		t.Fatalf("expected truncation notice, got:\n%s", result.Text)
	}
}

func TestGrepToolExecuteFallsBackToLiteralPattern(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("Hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewGrepTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "[",
		"path":    ".",
	})
	if err != nil {
		t.Fatalf("literal fallback should not fail: %v", err)
	}
	if !strings.Contains(result.Text, "(invalid regex; fell back to literal search)") {
		t.Fatalf("expected literal fallback notice, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "(no matches found)") {
		t.Fatalf("expected no matches, got: %s", result.Text)
	}
}

func TestFindTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewFindTool(r)

	if tool.Name() != "find" {
		t.Errorf("expected name 'find', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestFindToolExecute(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("Hello"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("package main"), 0644)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewFindTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt",
		"path":    ".",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
}

func TestFindToolExecuteMaxDepth(t *testing.T) {
	tmpDir := t.TempDir()
	nested := filepath.Join(tmpDir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested.go"), []byte("package nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewFindTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":  "*.go",
		"path":     ".",
		"maxDepth": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "root.go") {
		t.Fatalf("expected root.go, got: %s", result.Text)
	}
	if strings.Contains(result.Text, "nested.go") {
		t.Fatalf("maxDepth should exclude nested.go, got: %s", result.Text)
	}
}

func TestFindToolExecuteReturnsInvalidPathError(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("Hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewFindTool(r)

	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt",
		"path":    "missing",
	})
	if err == nil {
		t.Fatal("expected invalid path error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLsTool(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewLsTool(r)

	if tool.Name() != "ls" {
		t.Errorf("expected name 'ls', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestLsToolExecute(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("Hello"), 0644)
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	sb := sandbox.NewNoneSandbox()
	r := NewRegistry(tmpDir, sb)
	tool := NewLsTool(r)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": ".",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("expected non-empty result")
	}
}

func TestToolDefinition(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	tool := NewReadTool(r)

	def := ToolDefinition(tool)

	if def.Name != "read" {
		t.Errorf("expected name 'read', got '%s'", def.Name)
	}

	if def.Description == "" {
		t.Error("expected non-empty description")
	}

	if def.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestDefinitions(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	defs := r.Definitions()

	if len(defs) != 11 {
		t.Errorf("expected 11 definitions, got %d", len(defs))
	}
}

func TestAll(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()

	all := r.All()

	if len(all) != 11 {
		t.Errorf("expected 11 tools, got %d", len(all))
	}
}

// TestWriteFileAtomic_SuccessNoTmpFile verifies writeFileAtomic does not
// leave a temp file on success.
func TestWriteFileAtomic_SuccessNoTmpFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "output.txt")

	if err := writeFileAtomic(path, []byte("hello world")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want 'hello world'", string(data))
	}

	// Verify no .tmp-* files left
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestWriteFileAtomic_ErrorCleansUp verifies writeFileAtomic cleans up
// the temp file on write error.
func TestWriteFileAtomic_ErrorCleansUp(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "output.txt")

	// Write to a path where parent dir creation fails (file blocks mkdir)
	blocker := filepath.Join(tmpDir, "subdir")
	os.WriteFile(blocker, []byte("block"), 0644) // file, not dir

	err := writeFileAtomic(path, []byte("data"))
	if err == nil {
		t.Log("expected error writing to blocked path")
	}

	// No .tmp-* files should remain
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// --- QuestionTool tests ---

func TestQuestionToolMetadata(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	qt := NewQuestionTool(r)

	if qt.Name() != "question" {
		t.Errorf("name = %q, want 'question'", qt.Name())
	}
	if qt.Description() == "" {
		t.Error("expected non-empty description")
	}
	if qt.Parameters() == nil {
		t.Error("expected non-nil parameters")
	}
	if qt.PromptSnippet() == "" {
		t.Error("expected non-empty prompt snippet")
	}
	if len(qt.PromptGuidelines()) == 0 {
		t.Error("expected non-empty guidelines")
	}
}

func TestQuestionTool_InPlanAndAgentModes(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	r.RegisterDefaults()
	r.Register(NewQuestionTool(r))

	planTools := r.ModeTools("plan")
	planNames := make(map[string]bool)
	for _, td := range planTools {
		planNames[td.Name] = true
	}
	if !planNames["question"] {
		t.Error("expected 'question' in plan mode")
	}

	agentTools := r.ModeTools("agent")
	agentNames := make(map[string]bool)
	for _, td := range agentTools {
		agentNames[td.Name] = true
	}
	if !agentNames["question"] {
		t.Error("expected 'question' in agent mode")
	}

	osTools := r.ModeTools("os")
	if len(osTools) != 1 || osTools[0].Name != "bash" {
		t.Fatalf("OS mode tools = %#v, want only bash", osTools)
	}

	yoloTools := r.ModeTools("yolo")
	yoloNames := make(map[string]bool)
	for _, td := range yoloTools {
		yoloNames[td.Name] = true
	}
	if yoloNames["question"] {
		t.Error("did not expect 'question' in yolo mode")
	}
}

// mockAsker implements QuestionAsker for testing.
type mockAsker struct {
	lastQuestion string
	lastOptions  []string
	lastContext  string
	answer       string
}

func (m *mockAsker) AskQuestion(_ context.Context, question string, options []string, ctx string) string {
	m.lastQuestion = question
	m.lastOptions = options
	m.lastContext = ctx
	return m.answer
}

func TestQuestionTool_Execute(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	qt := NewQuestionTool(r)

	asker := &mockAsker{answer: "Option B"}
	ctx := ContextWithQuestionAsker(context.Background(), asker)

	result, err := qt.Execute(ctx, map[string]any{
		"question": "Which approach do you prefer?",
		"options":  []any{"Option A", "Option B", "Option C"},
		"context":  "We need to choose an architecture.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "Option B") {
		t.Errorf("result = %q, expected to contain 'Option B'", result.Text)
	}
	if asker.lastQuestion != "Which approach do you prefer?" {
		t.Errorf("question = %q", asker.lastQuestion)
	}
	if len(asker.lastOptions) != 3 {
		t.Errorf("options count = %d, want 3", len(asker.lastOptions))
	}
}

func TestQuestionTool_ExecuteMissingQuestion(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	qt := NewQuestionTool(r)

	ctx := ContextWithQuestionAsker(context.Background(), &mockAsker{})

	_, err := qt.Execute(ctx, map[string]any{
		"options": []any{"A"},
	})
	if err == nil {
		t.Fatal("expected error for missing question")
	}
}

func TestQuestionTool_ExecuteMissingAsker(t *testing.T) {
	sb := sandbox.NewNoneSandbox()
	r := NewRegistry("/tmp", sb)
	qt := NewQuestionTool(r)

	_, err := qt.Execute(context.Background(), map[string]any{
		"question": "Test?",
		"options":  []any{"A"},
	})
	if err == nil {
		t.Fatal("expected error for missing asker in context")
	}
}
