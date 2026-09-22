package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vbclient "github.com/startvibecoding/vibe-browser/pkg/client"
	vbprotocol "github.com/startvibecoding/vibe-browser/pkg/protocol"

	"github.com/oschina/mothx/internal/imageproc"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tools"
)

const (
	ToolName              = "browser"
	SkillName             = "vibe-browser"
	defaultViewportWidth  = 1920
	defaultViewportHeight = 1080
)

func RegisterTool(registry *tools.Registry) {
	if registry == nil {
		return
	}
	registry.Register(NewTool(registry))
}

func RemoveTool(registry *tools.Registry) {
	if registry == nil {
		return
	}
	registry.Remove(ToolName)
}

func IsToolRegistered(registry *tools.Registry) bool {
	if registry == nil {
		return false
	}
	_, ok := registry.Get(ToolName)
	return ok
}

type Tool struct {
	registry *tools.Registry
	mu       sync.Mutex
	client   *vbclient.Client
}

func NewTool(registry *tools.Registry) *Tool {
	return &Tool{registry: registry}
}

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Description() string {
	return "Control a Chromium-family browser through the vibe-browser SDK. Use action=open/navigate/snapshot/click/fill/type/press/screenshot/etc."
}

func (t *Tool) PromptSnippet() string {
	return "Control a browser through vibe-browser when browser support is enabled"
}

func (t *Tool) PromptGuidelines() []string {
	return []string{
		"Use browser snapshot before interacting so selectors/refs are grounded in the current page",
		"After click/fill/press/navigation, wait for a selector/text/url or take another snapshot before reporting success",
		"Use screenshot with outputPath for visual verification artifacts",
	}
}

func (t *Tool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "description": "Browser action: open, navigate, back, forward, reload, snapshot, click, dblclick, hover, focus, fill, type, press, scroll, click_at, dblclick_at, move_mouse, drag, check, uncheck, select, get_text, get_html, get_value, get_attr, get_url, get_title, is_visible, is_enabled, is_checked, eval, wait_ms, wait_for_selector, wait_for_text, wait_for_url, screenshot, set_viewport, set_geolocation, set_offline, set_headers, cookies_get, cookies_clear, set_cookie, tab_new, tab_close, close"},
    "url": {"type": "string"},
    "selector": {"type": "string", "description": "CSS selector or ref from snapshot"},
    "value": {"type": "string"},
    "text": {"type": "string"},
    "key": {"type": "string"},
    "attr": {"type": "string"},
    "maxBytes": {"type": "integer", "description": "Max bytes for get_html output. 0 disables truncation and returns the full document. Omitted: library default (~50KB cap)."},
    "maxChars": {"type": "integer", "description": "Max characters for get_html output. 0 means no character limit. Only meaningful for get_html."},
    "expression": {"type": "string"},
    "outputPath": {"type": "string", "description": "Project-relative path for screenshot output"},
    "format": {"type": "string", "enum": ["png", "jpeg", "webp"]},
    "quality": {"type": "integer"},
    "imageMode": {"type": "string", "enum": ["auto", "fast", "detail", "raw"], "description": "Image processing mode for returned screenshots. Defaults to detail."},
    "maxLongEdge": {"type": "integer", "description": "Optional maximum long edge in pixels for returned screenshot resizing"},
    "fullPage": {"type": "boolean"},
    "interactive": {"type": "boolean"},
    "compact": {"type": "boolean"},
    "depth": {"type": "integer"},
    "urls": {"type": "boolean"},
    "width": {"type": "integer"},
    "height": {"type": "integer"},
    "viewportWidth": {"type": "integer", "description": "Initial browser viewport width. Defaults to 1920."},
    "viewportHeight": {"type": "integer", "description": "Initial browser viewport height. Defaults to 1080."},
    "ms": {"type": "integer"},
    "deltaX": {"type": "number"},
    "deltaY": {"type": "number"},
    "x": {"type": "number", "description": "Viewport x coordinate in CSS pixels (click_at, dblclick_at, move_mouse, scroll with coords)."},
    "y": {"type": "number", "description": "Viewport y coordinate in CSS pixels (click_at, dblclick_at, move_mouse, scroll with coords)."},
    "startX": {"type": "number", "description": "Drag start x (drag)."},
    "startY": {"type": "number", "description": "Drag start y (drag)."},
    "endX": {"type": "number", "description": "Drag end x (drag)."},
    "endY": {"type": "number", "description": "Drag end y (drag)."},
    "steps": {"type": "integer", "description": "Number of intermediate steps for drag (0 = instant)."},
    "waitUntil": {"type": "string", "description": "Navigation load state to wait for: load, domcontentloaded, networkidle (open, navigate)."},
    "clipX": {"type": "number", "description": "Screenshot clip origin x (screenshot)."},
    "clipY": {"type": "number", "description": "Screenshot clip origin y (screenshot)."},
    "clipWidth": {"type": "number", "description": "Screenshot clip width (screenshot)."},
    "clipHeight": {"type": "number", "description": "Screenshot clip height (screenshot)."},
    "name": {"type": "string", "description": "Cookie name (set_cookie)."},
    "domain": {"type": "string", "description": "Cookie domain (set_cookie)."},
    "path": {"type": "string", "description": "Cookie path (set_cookie)."},
    "httpOnly": {"type": "boolean", "description": "Cookie httpOnly flag (set_cookie)."},
    "secure": {"type": "boolean", "description": "Cookie secure flag (set_cookie)."},
    "sameSite": {"type": "string", "description": "Cookie sameSite policy: Strict, Lax, None (set_cookie)."},
    "expires": {"type": "number", "description": "Cookie expiry as epoch seconds (set_cookie)."},
    "latitude": {"type": "number"},
    "longitude": {"type": "number"},
    "accuracy": {"type": "number"},
    "offline": {"type": "boolean"},
    "headers": {"type": "object", "additionalProperties": {"type": "string"}},
    "targetId": {"type": "string"},
    "headless": {"type": "boolean"},
    "browser": {"type": "string", "enum": ["chrome", "chromium", "brave", "edge", "chrome-canary"]},
    "cdpUrl": {"type": "string"},
    "executablePath": {"type": "string"},
    "daemon": {"type": "boolean"},
    "session": {"type": "string"}
  },
  "required": ["action"]
}`)
}

func (t *Tool) ExecutionTimeout(params map[string]any) (time.Duration, bool) {
	return 2 * time.Minute, true
}

func (t *Tool) Execute(ctx context.Context, params map[string]any) (result tools.ToolResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = tools.ToolResult{}
			err = fmt.Errorf("%v", recovered)
		}
	}()

	t.mu.Lock()
	defer t.mu.Unlock()

	action := strings.ToLower(strings.TrimSpace(stringParam(params, "action")))
	if action == "" {
		return tools.ToolResult{}, fmt.Errorf("action is required")
	}
	if action == "close" {
		if t.client != nil {
			_ = t.client.Close()
			t.client = nil
		}
		return tools.NewTextToolResult("browser closed"), nil
	}

	c, err := t.ensureClient(ctx, params)
	if err != nil {
		return tools.ToolResult{}, err
	}

	switch action {
	case "open":
		if url := stringParam(params, "url"); url != "" {
			var navErr error
			if w := stringParam(params, "waitUntil"); w != "" {
				navErr = c.NavigateWith(ctx, url, w)
			} else {
				navErr = c.Navigate(ctx, url)
			}
			if navErr != nil {
				return tools.ToolResult{}, navErr
			}
		}
		return t.pageSummary(ctx, c, "browser opened")
	case "navigate":
		url := requireString(params, "url")
		var navErr error
		if w := stringParam(params, "waitUntil"); w != "" {
			navErr = c.NavigateWith(ctx, url, w)
		} else {
			navErr = c.Navigate(ctx, url)
		}
		if navErr != nil {
			return tools.ToolResult{}, navErr
		}
		return t.pageSummary(ctx, c, "navigated")
	case "back":
		return textErr("went back", c.Back(ctx))
	case "forward":
		return textErr("went forward", c.Forward(ctx))
	case "reload":
		return textErr("reloaded", c.Reload(ctx))
	case "snapshot":
		s, err := c.SnapshotWithOptions(ctx, &vbprotocol.SnapshotOptions{
			Selector:    stringParam(params, "selector"),
			Interactive: boolParam(params, "interactive"),
			Compact:     boolParam(params, "compact"),
			Depth:       intParam(params, "depth"),
			URLs:        boolParam(params, "urls"),
		})
		return tools.NewTextToolResult(s), err
	case "click":
		return textErr("clicked", c.Click(ctx, requireString(params, "selector")))
	case "dblclick":
		return textErr("double-clicked", c.DoubleClick(ctx, requireString(params, "selector")))
	case "hover":
		return textErr("hovered", c.Hover(ctx, requireString(params, "selector")))
	case "focus":
		return textErr("focused", c.Focus(ctx, requireString(params, "selector")))
	case "fill":
		return textErr("filled", c.Fill(ctx, requireString(params, "selector"), requireString(params, "value")))
	case "type":
		return textErr("typed", c.Type(ctx, requireString(params, "selector"), requireString(params, "text")))
	case "press":
		return textErr("pressed", c.Press(ctx, requireString(params, "key")))
	case "scroll":
		// When x/y are provided, scroll at viewport coordinates (ScrollAt);
		// otherwise scroll the active element (legacy Scroll behavior).
		if x, ok := floatParamOK(params, "x"); ok {
			y, _ := floatParamOK(params, "y")
			return textErr("scrolled at", c.ScrollAt(ctx, x, y, floatParam(params, "deltaX"), floatParam(params, "deltaY")))
		}
		return textErr("scrolled", c.Scroll(ctx, floatParam(params, "deltaX"), floatParam(params, "deltaY")))
	// Coordinate-based mouse actions (vibe-browser v0.1.5+). x/y are
	// viewport coordinates in CSS pixels.
	case "click_at":
		return textErr("clicked at", c.ClickAt(ctx, floatParam(params, "x"), floatParam(params, "y")))
	case "dblclick_at":
		return textErr("double-clicked at", c.DoubleClickAt(ctx, floatParam(params, "x"), floatParam(params, "y")))
	case "move_mouse":
		return textErr("moved mouse", c.MoveMouse(ctx, floatParam(params, "x"), floatParam(params, "y")))
	case "drag":
		return textErr("dragged", c.Drag(ctx, floatParam(params, "startX"), floatParam(params, "startY"), floatParam(params, "endX"), floatParam(params, "endY"), intParam(params, "steps")))
	case "set_cookie":
		return textErr("cookie set", c.SetCookie(ctx, cookieFromParams(params)))
	case "check":
		return textErr("checked", c.Check(ctx, requireString(params, "selector")))
	case "uncheck":
		return textErr("unchecked", c.Uncheck(ctx, requireString(params, "selector")))
	case "select":
		return textErr("selected", c.Select(ctx, requireString(params, "selector"), requireString(params, "value")))
	case "get_text":
		return stringValue(ctx, c.GetText, requireString(params, "selector"))
	case "get_html":
		selector := requireString(params, "selector")
		// vibe-browser v0.1.5+ caps HTML at ~50KB by default to protect
		// downstream LLM context. Callers can override per call:
		// maxBytes=0 returns the full document, maxBytes/maxChars>0 apply
		// custom caps. When neither is present we use the library default.
		if opts := htmlOptionsFromParams(params); opts != nil {
			return valueResult(c.GetHTMLWithOptions(ctx, selector, opts))
		}
		return stringValue(ctx, c.GetHTML, selector)
	case "get_value":
		return stringValue(ctx, c.GetValue, requireString(params, "selector"))
	case "get_attr":
		return stringPairValue(ctx, c.GetAttr, requireString(params, "selector"), requireString(params, "attr"))
	case "get_url":
		return valueResult(c.URL(ctx))
	case "get_title":
		return valueResult(c.Title(ctx))
	case "is_visible":
		return valueResult(c.IsVisible(ctx, requireString(params, "selector")))
	case "is_enabled":
		return valueResult(c.IsEnabled(ctx, requireString(params, "selector")))
	case "is_checked":
		return valueResult(c.IsChecked(ctx, requireString(params, "selector")))
	case "eval":
		return valueResult(c.Eval(ctx, requireString(params, "expression")))
	case "wait_ms":
		return textErr("waited", c.WaitMS(ctx, intParam(params, "ms")))
	case "wait_for_selector":
		return textErr("selector appeared", c.WaitForSelector(ctx, requireString(params, "selector")))
	case "wait_for_text":
		return textErr("text appeared", c.WaitForText(ctx, requireString(params, "text")))
	case "wait_for_url":
		return textErr("url matched", c.WaitForURL(ctx, requireString(params, "url")))
	case "screenshot":
		return t.screenshot(ctx, c, params)
	case "set_viewport":
		return textErr("viewport set", c.SetViewport(ctx, intParam(params, "width"), intParam(params, "height")))
	case "set_geolocation":
		return textErr("geolocation set", c.SetGeolocation(ctx, floatParam(params, "latitude"), floatParam(params, "longitude"), floatParam(params, "accuracy")))
	case "set_offline":
		return textErr("offline state set", c.SetOffline(ctx, boolParam(params, "offline")))
	case "set_headers":
		return textErr("headers set", c.SetHeaders(ctx, stringMapParam(params, "headers")))
	case "cookies_get":
		return valueResult(c.GetCookies(ctx))
	case "cookies_clear":
		return textErr("cookies cleared", c.ClearCookies(ctx))
	case "tab_new":
		return valueResult(c.NewTab(ctx, stringParam(params, "url")))
	case "tab_close":
		return textErr("tab closed", c.CloseTab(ctx, requireString(params, "targetId")))
	default:
		return tools.ToolResult{}, fmt.Errorf("unknown browser action: %s", action)
	}
}

func (t *Tool) ensureClient(ctx context.Context, params map[string]any) (*vbclient.Client, error) {
	if t.client != nil && t.client.IsConnected() {
		return t.client, nil
	}
	opts := clientOptions(params)
	var c *vbclient.Client
	var err error
	if boolParam(params, "daemon") {
		c, err = vbclient.Connect(ctx, opts)
	} else {
		c, err = vbclient.Open(ctx, opts)
	}
	if err != nil {
		return nil, err
	}
	t.client = c
	return c, nil
}

func clientOptions(params map[string]any) *vbclient.Options {
	opts := &vbclient.Options{
		CDPURL:          firstNonEmpty(stringParam(params, "cdpUrl"), os.Getenv("VIBE_BROWSER_CDP_URL")),
		Session:         firstNonEmpty(stringParam(params, "session"), os.Getenv("VIBE_BROWSER_SESSION")),
		ExecutablePath:  firstNonEmpty(stringParam(params, "executablePath"), os.Getenv("CHROME_PATH")),
		DaemonSocketDir: os.Getenv("VIBE_BROWSER_SOCKET_DIR"),
		Launch: &vbprotocol.LaunchOptions{
			Headless:       true,
			ViewportWidth:  intParamDefault(params, "viewportWidth", defaultViewportWidth),
			ViewportHeight: intParamDefault(params, "viewportHeight", defaultViewportHeight),
		},
	}
	browserName := firstNonEmpty(stringParam(params, "browser"), os.Getenv("VIBE_BROWSER_BROWSER"))
	if browserName != "" {
		opts.Browser = vbprotocol.BrowserType(browserName)
		opts.Launch.Browser = opts.Browser
	}
	opts.Launch.ExecutablePath = opts.ExecutablePath
	if headless, ok := boolParamOK(params, "headless"); ok {
		opts.Launch.Headless = headless
	}
	return opts
}

func (t *Tool) screenshot(ctx context.Context, c *vbclient.Client, params map[string]any) (tools.ToolResult, error) {
	format := stringParam(params, "format")
	if format == "" {
		format = "png"
	}
	data, err := c.ScreenshotWithOptions(ctx, &vbprotocol.ScreenshotOptions{
		Format:     format,
		Quality:    intParam(params, "quality"),
		FullPage:   boolParam(params, "fullPage"),
		Selector:   stringParam(params, "selector"),
		ClipX:      floatParam(params, "clipX"),
		ClipY:      floatParam(params, "clipY"),
		ClipWidth:  floatParam(params, "clipWidth"),
		ClipHeight: floatParam(params, "clipHeight"),
	})
	if err != nil {
		return tools.ToolResult{}, err
	}
	if outputPath := stringParam(params, "outputPath"); outputPath != "" {
		resolved, err := t.registry.ResolvePath(outputPath)
		if err != nil {
			return tools.ToolResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
			return tools.ToolResult{}, err
		}
		if err := os.WriteFile(resolved, data, 0644); err != nil {
			return tools.ToolResult{}, err
		}
		return tools.NewTextToolResult(fmt.Sprintf("screenshot saved: %s", resolved)), nil
	}
	return t.screenshotToolResult(data, params)
}

func (t *Tool) screenshotToolResult(data []byte, params map[string]any) (tools.ToolResult, error) {
	policy := t.screenshotImagePolicy(params)
	result, err := imageproc.PrepareBytes(data, policy)
	if err != nil {
		return tools.ToolResult{}, fmt.Errorf("process screenshot: %w", err)
	}
	image := provider.ImageContent{
		Data:           base64.StdEncoding.EncodeToString(result.Data),
		MimeType:       result.MimeType,
		Width:          result.Meta.Width,
		Height:         result.Meta.Height,
		Bytes:          result.Meta.Bytes,
		OriginalWidth:  result.Meta.OriginalWidth,
		OriginalHeight: result.Meta.OriginalHeight,
		OriginalBytes:  result.Meta.OriginalBytes,
		Detail:         result.Meta.Detail,
		Scale:          result.Meta.Scale,
	}
	return tools.NewImageToolResultWithContent(browserScreenshotDescription(result), image), nil
}

func (t *Tool) screenshotImagePolicy(params map[string]any) imageproc.Policy {
	mode := imageproc.ModeDetail
	if v := stringParam(params, "imageMode"); v != "" {
		mode = imageproc.NormalizeMode(v)
	}
	policy := imageproc.DefaultPolicy(mode)
	if t.registry != nil {
		policy = t.registry.ImagePolicy(mode)
	}
	if v := intParam(params, "maxLongEdge"); v > 0 {
		policy.MaxLongEdge = v
	}
	return policy
}

func browserScreenshotDescription(result imageproc.Result) string {
	original := fmt.Sprintf("%dx%d %s", result.Meta.OriginalWidth, result.Meta.OriginalHeight, formatBytes(result.Meta.OriginalBytes))
	sent := fmt.Sprintf("%dx%d %s %s", result.Meta.Width, result.Meta.Height, formatBytes(result.Meta.Bytes), result.MimeType)
	if result.Meta.Resized || result.Meta.Transcoded || result.Meta.OriginalBytes != result.Meta.Bytes {
		return fmt.Sprintf("[Browser screenshot, original: %s, sent: %s, mode: %s]", original, sent, result.Meta.Detail)
	}
	return fmt.Sprintf("[Browser screenshot, %s, mode: %s]", sent, result.Meta.Detail)
}

func formatBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	kb := float64(n) / unit
	if kb < unit {
		return fmt.Sprintf("%.1fKB", kb)
	}
	return fmt.Sprintf("%.1fMB", kb/unit)
}

func (t *Tool) pageSummary(ctx context.Context, c *vbclient.Client, prefix string) (tools.ToolResult, error) {
	title, _ := c.Title(ctx)
	url, _ := c.URL(ctx)
	return tools.NewTextToolResult(strings.TrimSpace(fmt.Sprintf("%s\nTitle: %s\nURL: %s", prefix, title, url))), nil
}

func textErr(text string, err error) (tools.ToolResult, error) {
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(text), nil
}

func stringValue(ctx context.Context, fn func(context.Context, string) (string, error), arg string) (tools.ToolResult, error) {
	return valueResult(fn(ctx, arg))
}

func stringPairValue(ctx context.Context, fn func(context.Context, string, string) (string, error), a string, b string) (tools.ToolResult, error) {
	return valueResult(fn(ctx, a, b))
}

func valueResult(v any, err error) (tools.ToolResult, error) {
	if err != nil {
		return tools.ToolResult{}, err
	}
	switch val := v.(type) {
	case string:
		return tools.NewTextToolResult(val), nil
	case bool:
		return tools.NewTextToolResult(fmt.Sprintf("%v", val)), nil
	default:
		data, marshalErr := json.MarshalIndent(val, "", "  ")
		if marshalErr != nil {
			return tools.NewTextToolResult(fmt.Sprintf("%v", val)), nil
		}
		return tools.NewTextToolResult(string(data)), nil
	}
}

func requireString(params map[string]any, key string) string {
	v := stringParam(params, key)
	if v == "" {
		panic(fmt.Sprintf("%s is required", key))
	}
	return v
}

func stringParam(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func boolParam(params map[string]any, key string) bool {
	v, _ := boolParamOK(params, key)
	return v
}

func boolParamOK(params map[string]any, key string) (bool, bool) {
	if v, ok := params[key].(bool); ok {
		return v, true
	}
	return false, false
}

func intParam(params map[string]any, key string) int {
	switch v := params[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func intParamDefault(params map[string]any, key string, defaultValue int) int {
	if value := intParam(params, key); value > 0 {
		return value
	}
	return defaultValue
}

func floatParam(params map[string]any, key string) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func stringMapParam(params map[string]any, key string) map[string]string {
	out := map[string]string{}
	switch raw := params[key].(type) {
	case map[string]string:
		for k, v := range raw {
			out[k] = v
		}
	case map[string]any:
		for k, v := range raw {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// htmlOptionsFromParams builds vibe-browser HTMLOptions from tool params.
// Returns nil when neither maxBytes nor maxChars is present, so the library
// default (~50KB cap) applies. An explicit maxBytes=0 disables truncation
// ("return everything"); a positive value caps the byte count.
func htmlOptionsFromParams(params map[string]any) *vbprotocol.HTMLOptions {
	_, hasBytes := params["maxBytes"]
	_, hasChars := params["maxChars"]
	if !hasBytes && !hasChars {
		return nil
	}
	return &vbprotocol.HTMLOptions{
		MaxBytes: intParam(params, "maxBytes"),
		MaxChars: intParam(params, "maxChars"),
	}
}

// floatParamOK is like floatParam but also reports whether the key was
// present, so callers can distinguish "0" (explicit) from "omitted".
func floatParamOK(params map[string]any, key string) (float64, bool) {
	switch v := params[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case json.Number:
		f, _ := v.Float64()
		return f, true
	default:
		return 0, false
	}
}

// cookieFromParams builds a protocol.Cookie from flat tool params.
func cookieFromParams(params map[string]any) vbprotocol.Cookie {
	return vbprotocol.Cookie{
		Name:     stringParam(params, "name"),
		Value:    stringParam(params, "value"),
		Domain:   stringParam(params, "domain"),
		Path:     stringParam(params, "path"),
		Expires:  floatParam(params, "expires"),
		HTTPOnly: boolParam(params, "httpOnly"),
		Secure:   boolParam(params, "secure"),
		SameSite: stringParam(params, "sameSite"),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
