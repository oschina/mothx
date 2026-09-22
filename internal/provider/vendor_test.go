package provider

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestResolveAdapterConfigExplicitVendor(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "deepseek",
		BaseURL: "https://example.com/v1",
		API:     "openai-chat",
	})
	if resolved.Vendor != "deepseek" {
		t.Fatalf("Vendor = %q, want deepseek", resolved.Vendor)
	}
	if resolved.ThinkingFormat != "deepseek" {
		t.Fatalf("ThinkingFormat = %q, want deepseek", resolved.ThinkingFormat)
	}
}

func TestResolveAdapterConfigExplicitVendorDefaultAPI(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor: "Anthropic",
	})
	if resolved.Vendor != "anthropic" {
		t.Fatalf("Vendor = %q, want anthropic", resolved.Vendor)
	}
	if resolved.API != "anthropic-messages" {
		t.Fatalf("API = %q, want anthropic-messages", resolved.API)
	}
}

func TestResolveAdapterConfigResponsesVendorsDefaultAPI(t *testing.T) {
	tests := []struct {
		baseURL string
		vendor  string
	}{
		{"https://api.openai.com/v1", "openai"},
		{"https://www.codeok.cc/v1", "codeok"},
		{"https://co.yes.vg/v1", "yescode"},
	}

	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			resolved := ResolveAdapterConfig(&config.ProviderConfig{BaseURL: tt.baseURL})
			if resolved.Vendor != tt.vendor {
				t.Fatalf("Vendor = %q, want %q", resolved.Vendor, tt.vendor)
			}
			if resolved.API != "openai-responses" {
				t.Fatalf("API = %q, want openai-responses", resolved.API)
			}
		})
	}
}

func TestResolveAdapterConfigBaseURLDetect(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://api.deepseek.com/anthropic",
		API:     "anthropic-messages",
	})
	if resolved.Vendor != "deepseek" {
		t.Fatalf("Vendor = %q, want deepseek", resolved.Vendor)
	}
	if resolved.ThinkingFormat != "deepseek" {
		t.Fatalf("ThinkingFormat = %q, want deepseek", resolved.ThinkingFormat)
	}
}

func TestResolveAdapterConfigPreservesExplicitThinkingFormat(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:         "deepseek",
		BaseURL:        "https://api.deepseek.com",
		API:            "openai-chat",
		ThinkingFormat: "openai",
	})
	if resolved.ThinkingFormat != "openai" {
		t.Fatalf("ThinkingFormat = %q, want explicit openai", resolved.ThinkingFormat)
	}
}

func TestResolveAdapterConfigGenericFallback(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
	})
	if resolved.Vendor != "" {
		t.Fatalf("Vendor = %q, want empty", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", resolved.API)
	}
}

func TestResolveAdapterConfigGoogleGemini(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/models",
	})
	if resolved.Vendor != "google-gemini" {
		t.Fatalf("Vendor = %q, want google-gemini", resolved.Vendor)
	}
	if resolved.API != "google-gemini" {
		t.Fatalf("API = %q, want google-gemini", resolved.API)
	}
}

func TestResolveAdapterConfigGoogleVertex(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models",
	})
	if resolved.Vendor != "google-vertex" {
		t.Fatalf("Vendor = %q, want google-vertex", resolved.Vendor)
	}
	if resolved.API != "google-vertex" {
		t.Fatalf("API = %q, want google-vertex", resolved.API)
	}
}

func TestResolveAdapterConfigExplicitVendorKimi(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "kimi",
		BaseURL: "https://api.kimi.com/coding",
		API:     "anthropic-messages",
	})
	if resolved.Vendor != "kimi" {
		t.Fatalf("Vendor = %q, want kimi", resolved.Vendor)
	}
	// kimi has no special thinkingFormat, should remain empty
	if resolved.ThinkingFormat != "" {
		t.Fatalf("ThinkingFormat = %q, want empty", resolved.ThinkingFormat)
	}
}

func TestResolveAdapterConfigExplicitVendorZai(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "zai",
		BaseURL: "https://api.z.ai/api/coding/paas/v4",
		API:     "openai-chat",
	})
	if resolved.Vendor != "zai" {
		t.Fatalf("Vendor = %q, want zai", resolved.Vendor)
	}
	if resolved.ThinkingFormat != "zai" {
		t.Fatalf("ThinkingFormat = %q, want zai", resolved.ThinkingFormat)
	}
}

func TestResolveAdapterConfigBaseURLDetectKimi(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"https://api.moonshot.cn/v1"},
		{"https://api.kimi.com/coding"},
	}
	for _, tt := range tests {
		resolved := ResolveAdapterConfig(&config.ProviderConfig{
			BaseURL: tt.url,
		})
		if resolved.Vendor != "kimi" {
			t.Fatalf("VendorFromBaseURL(%q) = %q, want kimi", tt.url, resolved.Vendor)
		}
	}
}

func TestResolveAdapterConfigBaseURLDetectZai(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"https://api.z.ai/api/coding/paas/v4"},
		{"https://open.bigmodel.cn/api/coding/paas/v4"},
	}
	for _, tt := range tests {
		resolved := ResolveAdapterConfig(&config.ProviderConfig{
			BaseURL: tt.url,
		})
		if resolved.Vendor != "zai" {
			t.Fatalf("VendorFromBaseURL(%q) = %q, want zai", tt.url, resolved.Vendor)
		}
		if resolved.ThinkingFormat != "zai" {
			t.Fatalf("ThinkingFormat(%q) = %q, want zai", tt.url, resolved.ThinkingFormat)
		}
	}
}

func TestVendorFromBaseURLDetectsXiaomiTokenPlan(t *testing.T) {
	got := VendorFromBaseURL("https://token-plan-cn.xiaomimimo.com/v1")
	if got != "xiaomi-token-plan-cn" {
		t.Fatalf("VendorFromBaseURL = %q, want xiaomi-token-plan-cn", got)
	}
}

func TestVendorFromBaseURLDetectsGoogleAdapters(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://generativelanguage.googleapis.com/v1beta/models", "google-gemini"},
		{"https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models", "google-vertex"},
	}

	for _, tt := range tests {
		got := VendorFromBaseURL(tt.url)
		if got != tt.expected {
			t.Errorf("VendorFromBaseURL(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestResolveAdapterConfigBaseURLDetectAgnes(t *testing.T) {
	tests := []struct {
		url    string
		vendor string
	}{
		{"https://apihub.agnes-ai.com/v1", "agnes"},
		{"https://api.agnes-ai.cn/v1", "agnes"},
	}
	for _, tt := range tests {
		resolved := ResolveAdapterConfig(&config.ProviderConfig{BaseURL: tt.url, API: "openai-chat"})
		if resolved.Vendor != tt.vendor {
			t.Fatalf("ResolveAdapterConfig(%q).Vendor = %q, want %q", tt.url, resolved.Vendor, tt.vendor)
		}
		if resolved.API != "openai-chat" {
			t.Fatalf("ResolveAdapterConfig(%q).API = %q, want openai-chat", tt.url, resolved.API)
		}
	}
}

func TestResolveAdapterConfigExplicitVendorAgnes(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "Agnes",
		BaseURL: "https://apihub.agnes-ai.com/v1",
	})
	if resolved.Vendor != "agnes" {
		t.Fatalf("Vendor = %q, want agnes", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", resolved.API)
	}
}

func TestResolveAdapterConfigExplicitVendorAMDRadeon(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "amd-radeon",
		BaseURL: "https://developer.amd.com.cn/radeon/api/v1",
		API:     "openai-chat",
	})
	if resolved.Vendor != "amd-radeon" {
		t.Fatalf("Vendor = %q, want amd-radeon", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", resolved.API)
	}
}

func TestResolveAdapterConfigBaseURLDetectAMDRadeon(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://developer.amd.com.cn/radeon/api/v1",
	})
	if resolved.Vendor != "amd-radeon" {
		t.Fatalf("Vendor = %q, want amd-radeon", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", resolved.API)
	}
}

func TestResolveAdapterConfigBaseURLDetectBai(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		BaseURL: "https://api.b.ai/v1",
	})
	if resolved.Vendor != "bai" {
		t.Fatalf("ResolveAdapterConfig(api.b.ai).Vendor = %q, want bai", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("ResolveAdapterConfig(api.b.ai).API = %q, want openai-chat", resolved.API)
	}
}

func TestResolveAdapterConfigExplicitVendorBai(t *testing.T) {
	resolved := ResolveAdapterConfig(&config.ProviderConfig{
		Vendor:  "Bai",
		BaseURL: "https://api.b.ai/v1",
		API:     "openai-chat",
	})
	if resolved.Vendor != "bai" {
		t.Fatalf("Vendor = %q, want bai", resolved.Vendor)
	}
	if resolved.API != "openai-chat" {
		t.Fatalf("API = %q, want openai-chat", resolved.API)
	}
}
