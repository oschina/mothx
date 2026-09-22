package browser

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
	vbprotocol "github.com/startvibecoding/vibe-browser/pkg/protocol"
)

func TestBuiltInBrowserSkillIsDiscoverable(t *testing.T) {
	manager := skills.NewManager("", "")
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	skill := manager.Get(SkillName)
	if skill == nil || skill.Source != "builtin" {
		t.Fatalf("built-in browser skill = %#v", skill)
	}
	context := manager.BuildSkillContext(SkillName)
	for _, want := range []string{
		"# Vibe Browser",
		"`browser` tool",
		"`snapshot`",
		"`screenshot`",
		"Never claim a UI state changed until you verify it",
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("skill content missing %q", want)
		}
	}
}

func TestRegisterAndRemoveBrowserTool(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)

	RegisterTool(registry)
	if !IsToolRegistered(registry) {
		t.Fatal("expected browser tool to be registered")
	}

	RemoveTool(registry)
	if IsToolRegistered(registry) {
		t.Fatal("expected browser tool to be removed")
	}
}

func TestScreenshotToolResultProcessesImage(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir(), nil)
	tool := NewTool(registry)

	result, err := tool.screenshotToolResult(testPNG(t, 200, 100), map[string]any{
		"maxLongEdge": float64(50),
	})
	if err != nil {
		t.Fatalf("screenshotToolResult() error = %v", err)
	}
	if len(result.Contents) != 2 || result.Contents[1].Image == nil {
		t.Fatalf("contents = %#v, want text + image", result.Contents)
	}
	image := result.Contents[1].Image
	if image.Width != 50 || image.Height != 25 {
		t.Fatalf("image size = %dx%d, want 50x25", image.Width, image.Height)
	}
	if image.OriginalWidth != 200 || image.OriginalHeight != 100 {
		t.Fatalf("original size = %dx%d, want 200x100", image.OriginalWidth, image.OriginalHeight)
	}
	if image.Detail != "detail" {
		t.Fatalf("detail = %q, want detail", image.Detail)
	}
	if !strings.Contains(result.Text, "Browser screenshot") || !strings.Contains(result.Text, "original: 200x100") {
		t.Fatalf("description = %q, want screenshot resize details", result.Text)
	}
}

func TestClientOptionsDefaultLaunchViewport(t *testing.T) {
	opts := clientOptions(map[string]any{})
	if opts.Launch == nil {
		t.Fatal("Launch options are nil")
	}
	if opts.Launch.ViewportWidth != defaultViewportWidth {
		t.Fatalf("ViewportWidth = %d, want %d", opts.Launch.ViewportWidth, defaultViewportWidth)
	}
	if opts.Launch.ViewportHeight != defaultViewportHeight {
		t.Fatalf("ViewportHeight = %d, want %d", opts.Launch.ViewportHeight, defaultViewportHeight)
	}
	if !opts.Launch.Headless {
		t.Fatal("default launch should remain headless")
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 180, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buf.Bytes()
}

func TestClientOptionsAllowsViewportAndHeadlessOverride(t *testing.T) {
	opts := clientOptions(map[string]any{
		"viewportWidth":  float64(1366),
		"viewportHeight": float64(768),
		"headless":       false,
	})
	if opts.Launch == nil {
		t.Fatal("Launch options are nil")
	}
	if opts.Launch.ViewportWidth != 1366 {
		t.Fatalf("ViewportWidth = %d, want 1366", opts.Launch.ViewportWidth)
	}
	if opts.Launch.ViewportHeight != 768 {
		t.Fatalf("ViewportHeight = %d, want 768", opts.Launch.ViewportHeight)
	}
	if opts.Launch.Headless {
		t.Fatal("headless override was not honored")
	}
}

func TestHTMLOptionsFromParams(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   *vbprotocol.HTMLOptions
	}{
		{"neither present -> library default", map[string]any{"selector": "body"}, nil},
		{"maxBytes=0 opts out of truncation", map[string]any{"maxBytes": 0}, &vbprotocol.HTMLOptions{MaxBytes: 0}},
		{"maxBytes positive", map[string]any{"maxBytes": 100000}, &vbprotocol.HTMLOptions{MaxBytes: 100000}},
		{"maxChars only", map[string]any{"maxChars": 5000}, &vbprotocol.HTMLOptions{MaxChars: 5000}},
		{"maxBytes=0 plus maxChars", map[string]any{"maxBytes": 0, "maxChars": 100}, &vbprotocol.HTMLOptions{MaxBytes: 0, MaxChars: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlOptionsFromParams(tt.params)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("htmlOptionsFromParams() = %v, want %v", got, tt.want)
			}
			if got != nil && *got != *tt.want {
				t.Errorf("htmlOptionsFromParams() = %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

func TestFloatParamOK(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   float64
		ok     bool
	}{
		{"absent", map[string]any{}, 0, false},
		{"explicit zero", map[string]any{"x": 0}, 0, true},
		{"int", map[string]any{"x": 100}, 100, true},
		{"float64", map[string]any{"x": 12.5}, 12.5, true},
		{"json.Number", map[string]any{"x": json.Number("3.14")}, 3.14, true},
		{"non-numeric", map[string]any{"x": "nope"}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := floatParamOK(tt.params, "x")
			if ok != tt.ok {
				t.Fatalf("floatParamOK ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("floatParamOK = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCookieFromParams(t *testing.T) {
	params := map[string]any{
		"name":     "session",
		"value":    "abc",
		"domain":   "example.com",
		"path":     "/",
		"httpOnly": true,
		"secure":   true,
		"sameSite": "Lax",
		"expires":  1700000000,
	}
	c := cookieFromParams(params)
	if c.Name != "session" || c.Value != "abc" || c.Domain != "example.com" || c.Path != "/" {
		t.Errorf("basic fields wrong: %+v", c)
	}
	if !c.HTTPOnly || !c.Secure {
		t.Errorf("flags wrong: httpOnly=%v secure=%v", c.HTTPOnly, c.Secure)
	}
	if c.SameSite != "Lax" {
		t.Errorf("sameSite = %q, want Lax", c.SameSite)
	}
	if c.Expires != 1700000000 {
		t.Errorf("expires = %v, want 1700000000", c.Expires)
	}
}
