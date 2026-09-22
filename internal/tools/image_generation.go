package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

// ImageGenerationTool is a local tool backed by either the OpenAI Images API
// or the Responses API native image_generation hosted tool.
type imageGenerationResult struct {
	B64JSON string
	URL     string
}

type ImageGenerationTool struct {
	settings *config.Settings
	client   *http.Client
}

func NewImageGenerationTool(settings *config.Settings) *ImageGenerationTool {
	return &ImageGenerationTool{settings: settings, client: &http.Client{Timeout: 2 * time.Minute}}
}

func (t *ImageGenerationTool) Name() string { return "image_generation" }

func (t *ImageGenerationTool) Description() string {
	return "Generate an image from a text prompt using the configured image generation provider."
}

func (t *ImageGenerationTool) PromptSnippet() string {
	return "Generate images when the user requests an image"
}

func (t *ImageGenerationTool) PromptGuidelines() []string {
	return []string{"Use image_generation for image creation requests and return the generated image to the user."}
}

func (t *ImageGenerationTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Image generation prompt"},"size":{"type":"string","description":"Image size, for example 1024x1024"},"quality":{"type":"string","enum":["low","medium","high","auto"]},"n":{"type":"integer","minimum":1,"maximum":4}},"required":["prompt"]}`)
}

func (t *ImageGenerationTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	if t == nil || t.settings == nil || !t.settings.IsImageGenerationEnabled() {
		return ToolResult{}, fmt.Errorf("image_generation is disabled")
	}
	prompt, _ := params["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return ToolResult{}, fmt.Errorf("image_generation requires a non-empty prompt")
	}
	cfg := t.settings.EffectiveImageGeneration()
	apiType := strings.ToLower(strings.TrimSpace(cfg.APIType))
	if apiType == "" {
		apiType = "openai-images"
	}
	switch apiType {
	case "openai-images", "images", "openai-image":
		return t.executeImages(ctx, cfg, prompt, params)
	case "openai-responses", "responses":
		return t.executeResponses(ctx, cfg, prompt)
	default:
		return ToolResult{}, fmt.Errorf("unsupported image_generation apiType %q", cfg.APIType)
	}
}

func (t *ImageGenerationTool) executeImages(ctx context.Context, cfg config.ImageGenerationSettings, prompt string, params map[string]any) (ToolResult, error) {
	body := map[string]any{"model": cfg.Model, "prompt": prompt, "n": intParam(params, "n", 1), "response_format": "b64_json"}
	if body["model"] == "" {
		body["model"] = "gpt-image-1"
	}
	if value, ok := params["size"].(string); ok && strings.TrimSpace(value) != "" {
		body["size"] = value
	}
	if value, ok := params["quality"].(string); ok && strings.TrimSpace(value) != "" {
		body["quality"] = value
	}
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := t.postJSON(ctx, cfg, "/images/generations", body, &response); err != nil {
		return ToolResult{}, err
	}
	items := make([]imageGenerationResult, 0, len(response.Data))
	for _, item := range response.Data {
		items = append(items, imageGenerationResult{B64JSON: item.B64JSON, URL: item.URL})
	}
	return t.imageResult(ctx, cfg, items)
}

func (t *ImageGenerationTool) executeResponses(ctx context.Context, cfg config.ImageGenerationSettings, prompt string) (ToolResult, error) {
	model := cfg.Model
	if model == "" {
		model = "gpt-4.1"
	}
	body := map[string]any{
		"model": model,
		"input": prompt,
		"tools": []any{map[string]any{"type": "image_generation"}},
	}
	var response struct {
		Output []struct {
			Type   string `json:"type"`
			Result string `json:"result"`
		} `json:"output"`
	}
	if err := t.postJSON(ctx, cfg, "/responses", body, &response); err != nil {
		return ToolResult{}, err
	}
	items := make([]imageGenerationResult, 0, len(response.Output))
	for _, item := range response.Output {
		if item.Type == "image_generation_call" && item.Result != "" {
			items = append(items, imageGenerationResult{B64JSON: item.Result})
		}
	}
	return t.imageResult(ctx, cfg, items)
}

func (t *ImageGenerationTool) postJSON(ctx context.Context, cfg config.ImageGenerationSettings, path string, payload any, out any) error {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if operationID, ok := OperationIDFromContext(ctx); ok {
		// Providers that support idempotency can safely reuse the Runtime claim
		// when a local execution is retried after an interrupted process.
		req.Header.Set("Idempotency-Key", operationID)
	}
	if token := t.settings.ResolveImageGenerationToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("image_generation request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("image_generation request failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode image_generation response: %w", err)
	}
	return nil
}

func (t *ImageGenerationTool) imageResult(ctx context.Context, cfg config.ImageGenerationSettings, items []imageGenerationResult) (ToolResult, error) {
	if len(items) == 0 {
		return ToolResult{}, fmt.Errorf("image_generation response contained no generated image")
	}
	contents := make([]provider.ContentBlock, 0, len(items))
	for _, item := range items {
		data := item.B64JSON
		mime := "image/png"
		if data == "" && item.URL != "" {
			body, err := t.download(ctx, cfg, item.URL)
			if err != nil {
				return ToolResult{}, err
			}
			data = base64.StdEncoding.EncodeToString(body)
		}
		if data == "" {
			continue
		}
		contents = append(contents, provider.ContentBlock{Type: "image", Image: &provider.ImageContent{Data: data, MimeType: mime}})
	}
	if len(contents) == 0 {
		return ToolResult{}, fmt.Errorf("image_generation response contained no image data")
	}
	return ToolResult{Text: fmt.Sprintf("Generated %d image(s).", len(contents)), Contents: contents}, nil
}

func (t *ImageGenerationTool) download(ctx context.Context, cfg config.ImageGenerationSettings, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token := t.settings.ResolveImageGenerationToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download generated image failed: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

func intParam(params map[string]any, key string, fallback int) int {
	if value, ok := params[key].(float64); ok && int(value) > 0 {
		return int(value)
	}
	if value, ok := params[key].(int); ok && value > 0 {
		return value
	}
	return fallback
}
