package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func webTestSettings(baseURL, token string) *config.Settings {
	settings := config.DefaultSettings()
	settings.WebSearch.Enabled = config.BoolPtr(true)
	settings.WebSearch.BaseURL = baseURL
	settings.WebSearch.Token = token
	return settings
}

func TestWebSearchToolCallsDeepSeekSearch(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"rerank_score":2.4,"title":"DeepSeek V4","url":"https://example.com/v4","snippet":"snippet","summary":"summary text","content":"page body","site_name":"Example","date":"2026-06-01T00:00:00+00:00","authority":3}]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(webTestSettings(server.URL, "test-key"))
	result, err := tool.Execute(context.Background(), map[string]any{
		"query":        "DeepSeek V4",
		"count":        float64(5),
		"need_content": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/v1/tools/search" {
		t.Fatalf("path = %q, want /v1/tools/search", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotBody["query"] != "DeepSeek V4" || gotBody["count"] != float64(5) || gotBody["need_content"] != true {
		t.Fatalf("request body = %#v", gotBody)
	}
	for _, want := range []string{"DeepSeek V4", "https://example.com/v4", "summary text", "page body", "Example"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result text missing %q:\n%s", want, result.Text)
		}
	}
}

func TestWebSearchToolHandlesBaseURLWithV1(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(webTestSettings(server.URL+"/v1", "test-key"))
	result, err := tool.Execute(context.Background(), map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/v1/tools/search" {
		t.Fatalf("path = %q, want /v1/tools/search", gotPath)
	}
	if !strings.Contains(result.Text, "No web results") {
		t.Fatalf("result = %q, want no-results message", result.Text)
	}
}

func TestWebFetchToolCallsDeepSeekFetch(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"title":"Example article","content":"# Example article\n\nBody."}`))
	}))
	defer server.Close()

	tool := NewWebFetchTool(webTestSettings(server.URL, "test-key"))
	result, err := tool.Execute(context.Background(), map[string]any{"url": "https://example.com/article"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/v1/tools/fetch" {
		t.Fatalf("path = %q, want /v1/tools/fetch", gotPath)
	}
	if gotBody["url"] != "https://example.com/article" {
		t.Fatalf("request body = %#v", gotBody)
	}
	for _, want := range []string{"Example article", "Source: https://example.com/article", "Body."} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result text missing %q:\n%s", want, result.Text)
		}
	}
}

func TestWebToolsValidateInputsAndConfiguration(t *testing.T) {
	if _, err := NewWebSearchTool(webTestSettings("https://api.deepseek.com", "key")).Execute(context.Background(), map[string]any{"query": "   "}); err == nil {
		t.Fatal("expected empty query error")
	}
	if _, err := NewWebSearchTool(webTestSettings("https://api.deepseek.com", "key")).Execute(context.Background(), map[string]any{"query": "x", "count": float64(51)}); err == nil {
		t.Fatal("expected count range error")
	}
	if _, err := NewWebFetchTool(webTestSettings("https://api.deepseek.com", "key")).Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("expected non-http url error")
	}
	if _, err := NewWebFetchTool(webTestSettings("https://api.deepseek.com", "key")).Execute(context.Background(), map[string]any{"url": "https://user:pass@example.com"}); err == nil {
		t.Fatal("expected credential url error")
	}
	settings := config.DefaultSettings()
	settings.WebSearch.Provider = "missing-provider"
	settings.WebSearch.BaseURL = "https://api.deepseek.com"
	settings.WebSearch.Token = ""
	settings.Providers = map[string]*config.ProviderConfig{}
	if _, err := NewWebSearchTool(settings).Execute(context.Background(), map[string]any{"query": "x"}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want not-configured error", err)
	}
}

func TestWebSearchToolSurfacesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"'count' must be between 1 and 50","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`))
	}))
	defer server.Close()

	_, err := NewWebSearchTool(webTestSettings(server.URL, "test-key")).Execute(context.Background(), map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 50") {
		t.Fatalf("error = %v, want API message", err)
	}
}

func TestWebSearchToolCallsYuanbaoSearchPro(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"Response":{"Pages":["{\"passage\":\"三星堆的由来正文\",\"score\":0.897802,\"date\":\"2024-06-07 19:00:51\",\"title\":\"三星堆名字的由来\",\"url\":\"https://example.com/sanxingdui\",\"site\":\"网易\",\"images\":[],\"favicon\":\"https://example.com/favicon.ico\"}"],"Query":"三星堆的由来","RequestId":"req-1"}}`))
	}))
	defer server.Close()

	settings := webTestSettings(server.URL, "wsa-key")
	settings.WebSearch.Backend = config.WebSearchBackendYuanbao
	tool := NewWebSearchTool(settings)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "三星堆的由来"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotPath != "/SearchPro" {
		t.Fatalf("path = %q, want /SearchPro", gotPath)
	}
	if gotAuth != "Bearer wsa-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotBody["Query"] != "三星堆的由来" {
		t.Fatalf("request body = %#v", gotBody)
	}
	for _, want := range []string{"三星堆名字的由来", "https://example.com/sanxingdui", "网易", "三星堆的由来正文"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("result text missing %q:\n%s", want, result.Text)
		}
	}
}

func TestWebSearchToolSurfacesYuanbaoError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"UnauthorizedOperation","Message":"invalid api key"}}}`))
	}))
	defer server.Close()

	settings := webTestSettings(server.URL, "bad-key")
	settings.WebSearch.Backend = config.WebSearchBackendYuanbao
	_, err := NewWebSearchTool(settings).Execute(context.Background(), map[string]any{"query": "x"})
	if err == nil || !strings.Contains(err.Error(), "UnauthorizedOperation") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v, want Yuanbao error code/message", err)
	}
}
