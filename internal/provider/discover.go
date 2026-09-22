package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// defaultDiscoverTimeout bounds a single model-discovery request.
const defaultDiscoverTimeout = 30 * time.Second

// discoverMaxResponseBytes caps how much of a /models response body is read.
const discoverMaxResponseBytes = 8 << 20

// DiscoveredModel is a model entry normalized from a provider /models listing
// endpoint. It is intentionally separate from config.ModelConfig because
// discovery results are drafts until a user explicitly adds them.
type DiscoveredModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Input         []string `json:"input,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
}

// DiscoverModelsOptions describes how to reach a provider /models endpoint.
type DiscoverModelsOptions struct {
	API         string
	BaseURL     string
	APIKey      string // literal key or a ${ENV_VAR} reference
	HTTPProxy   string
	ForceHTTP11 bool
	Headers     map[string]string
	Timeout     time.Duration // zero means defaultDiscoverTimeout
}

// ModelsEndpoint derives the absolute /models URL from a provider base URL.
func ModelsEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("baseUrl must be an absolute http(s) URL")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/models") {
		path += "/models"
	}
	u.Path = path
	return u.String(), nil
}

// ResolveSecretRef expands ${ENV_VAR} references against the environment and
// returns any other value unchanged.
func ResolveSecretRef(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		return os.Getenv(value[2 : len(value)-1])
	}
	return value
}

// ApplyDiscoveryAuthHeaders sets API-type specific auth headers plus any custom
// headers on a discovery request.
func ApplyDiscoveryAuthHeaders(req *http.Request, api, apiKey string, headers map[string]string) {
	api = strings.ToLower(strings.TrimSpace(api))
	switch {
	case strings.HasPrefix(api, "anthropic"):
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case strings.HasPrefix(api, "google"):
		if strings.HasPrefix(apiKey, "ya29.") || strings.HasPrefix(apiKey, "gya29.") {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		} else {
			req.Header.Set("x-goog-api-key", apiKey)
		}
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")
	ApplyHeaders(req, headers)
}

// FetchDiscoveredModels issues a GET against endpoint using client and parses
// the response. Errors never include the upstream response body: providers can
// return credentials, private diagnostics, or arbitrary HTML there, and the
// HTTP status alone is enough to explain a discovery failure.
func FetchDiscoveredModels(ctx context.Context, client *http.Client, endpoint, api, apiKey string, headers map[string]string) ([]DiscoveredModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	ApplyDiscoveryAuthHeaders(request, api, apiKey, headers)
	resp, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, discoverMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read models response: %v", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models endpoint returned HTTP %d", resp.StatusCode)
	}
	models, err := ParseDiscoveredModels(body)
	if err != nil {
		return nil, fmt.Errorf("parse models response: %v", err)
	}
	return models, nil
}

// DiscoverModels validates the base URL, builds an HTTP client honoring proxy
// and HTTP/1.1 options, and fetches the provider /models listing.
func DiscoverModels(ctx context.Context, opts DiscoverModelsOptions) ([]DiscoveredModel, error) {
	endpoint, err := ModelsEndpoint(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDiscoverTimeout
	}
	client, err := NewHTTPClientWithOptions(timeout, HTTPClientOptions{ProxyURL: opts.HTTPProxy, ForceHTTP11: opts.ForceHTTP11})
	if err != nil {
		return nil, fmt.Errorf("configure HTTP client: %v", err)
	}
	return FetchDiscoveredModels(ctx, client, endpoint, opts.API, ResolveSecretRef(opts.APIKey), opts.Headers)
}

// ParseDiscoveredModels normalizes a /models response body from the common
// provider envelope shapes ({"data": [...]}, {"models": [...]}, or a bare
// array) into DiscoveredModel entries.
func ParseDiscoveredModels(body []byte) ([]DiscoveredModel, error) {
	var envelope struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		var raw []json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		envelope.Data = raw
	}
	items := envelope.Data
	if len(items) == 0 {
		items = envelope.Models
	}
	result := make([]DiscoveredModel, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, raw := range items {
		var item struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			DisplayName   string   `json:"displayName"`
			ContextWindow int      `json:"contextWindow"`
			ContextLength int      `json:"context_length"`
			MaxTokens     int      `json:"maxTokens"`
			MaxOutput     int      `json:"max_output_tokens"`
			MaxTokensAlt  int      `json:"max_tokens"`
			Input         []string `json:"input"`
			InputAlt      []string `json:"input_modalities"`
			Reasoning     *bool    `json:"reasoning"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		id := normalizeDiscoveredModelID(item.ID)
		if id == "" {
			id = normalizeDiscoveredModelID(item.Name)
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		preset := config.PresetModelConfig("", id, nil)
		name := strings.TrimSpace(item.Name)
		if name == "" || normalizeDiscoveredModelID(name) == id {
			name = strings.TrimSpace(item.DisplayName)
		}
		if name == "" {
			name = preset.Name
		}
		input := append([]string(nil), item.Input...)
		if len(input) == 0 {
			input = append([]string(nil), item.InputAlt...)
		}
		if len(input) == 0 {
			input = config.CloneStringSlice(preset.Input)
		}
		contextWindow := item.ContextWindow
		if contextWindow == 0 {
			contextWindow = item.ContextLength
		}
		if contextWindow == 0 {
			contextWindow = preset.ContextWindow
		}
		maxTokens := item.MaxTokens
		if maxTokens == 0 {
			maxTokens = item.MaxOutput
		}
		if maxTokens == 0 {
			maxTokens = item.MaxTokensAlt
		}
		if maxTokens == 0 {
			maxTokens = preset.MaxTokens
		}
		reasoning := preset.Reasoning
		if item.Reasoning != nil {
			reasoning = *item.Reasoning
		}
		result = append(result, DiscoveredModel{ID: id, Name: name, ContextWindow: contextWindow, MaxTokens: maxTokens, Input: input, Reasoning: reasoning})
	}
	return result, nil
}

func normalizeDiscoveredModelID(value string) string {
	value = strings.TrimSpace(value)
	if marker := strings.LastIndex(value, "/models/"); marker >= 0 {
		value = value[marker+len("/models/"):]
	} else {
		value = strings.TrimPrefix(value, "models/")
	}
	return strings.TrimSpace(value)
}
