package provider

import (
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/config"
)

func TestProviderRegistryRegisterAndCreate(t *testing.T) {
	r := NewProviderRegistry()

	r.Register("test", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("test", []*Model{
			{ID: "m1", Name: "Model 1"},
		}, nil), nil
	})

	if !r.Has("test") {
		t.Error("expected 'test' to be registered")
	}
	if r.Has("nonexistent") {
		t.Error("expected 'nonexistent' to not be registered")
	}

	p, err := r.Create("test", &config.ProviderConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "test" {
		t.Errorf("expected 'test', got %q", p.Name())
	}
}

func TestProviderRegistryCreateNotFound(t *testing.T) {
	r := NewProviderRegistry()
	_, err := r.Create("nonexistent", &config.ProviderConfig{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProviderRegistryList(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("a", func(cfg *config.ProviderConfig) (Provider, error) { return nil, nil })
	r.Register("b", func(cfg *config.ProviderConfig) (Provider, error) { return nil, nil })

	names := r.List()
	if len(names) != 2 {
		t.Errorf("expected 2, got %d", len(names))
	}
}

func TestVendorFromBaseURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://api.ant-ling.com", "ant-ling"},
		{"https://api.anthropic.com/v1/messages", "anthropic"},
		{"https://api.deepseek.com", "deepseek"},
		{"https://api.deepseek.com/anthropic", "deepseek"},
		{"https://api.cerebras.ai/v1", "cerebras"},
		{"https://router.huggingface.co/v1", "huggingface"},
		{"https://api.xiaomimimo.com/v1", "xiaomi"},
		{"https://api.moonshot.cn/v1", "kimi"},
		{"https://api.kimi.com/coding", "kimi"},
		{"https://api.moonshot.ai/v1", "moonshotai"},
		{"https://integrate.api.nvidia.com/v1", "nvidia"},
		{"https://api.openai.com/v1/chat/completions", "openai"},
		{"https://opencode.ai/v1", "opencode"},
		{"https://api.z.ai/api/coding/paas/v4", "zai"},
		{"https://open.bigmodel.cn/api/coding/paas/v4", "zai"},
		{"https://api.minimaxi.com/anthropic", "minimax"},
		{"https://ark.cn-beijing.volces.com/api", "volcengine"},
		{"https://aip.baidubce.com/rpc", "qianfan"},
		{"https://dashscope.aliyuncs.com/api", "bailian"},
		{"https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", "bailian"},
		{"https://coding.dashscope.aliyuncs.com/v1", "bailian"},
		{"https://ai.gitee.com/v1", "gitee"},
		{"https://openrouter.ai/api/v1", "openrouter"},
		{"https://api.together.xyz/v1", "together"},
		{"https://api.groq.com/openai", "groq"},
		{"https://api.fireworks.ai/inference", "fireworks"},
		{"https://generativelanguage.googleapis.com/v1beta/models", "google-gemini"},
		{"https://aiplatform.googleapis.com/v1/projects/test/locations/global/publishers/google/models", "google-vertex"},
		{"https://ai-gateway.vercel.sh/v1", "vercel-ai-gateway"},
		{"https://api.x.ai/v1", "xai"},
		{"https://www.codeok.cc/v1", "codeok"},
		{"https://co.yes.vg/v1", "yescode"},
		{"https://apihub.agnes-ai.com/v1", "agnes"},
		{"https://api.agnes-ai.cn/v1", "agnes"},
		{"https://unknown.example.com/v1", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := VendorFromBaseURL(tt.url)
		if got != tt.expected {
			t.Errorf("VendorFromBaseURL(%q) = %q, want %q", tt.url, got, tt.expected)
		}
	}
}

func TestResolveProviderExplicitVendor(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("myvendor", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("myvendor", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	p, err := ResolveProvider(&config.ProviderConfig{
		Vendor: "myvendor",
		API:    "openai-chat",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "myvendor" {
		t.Errorf("expected 'myvendor', got %q", p.Name())
	}
}

func TestResolveProviderAutoDetect(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("deepseek", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("deepseek", nil, nil), nil
	})
	r.Register("openai-chat", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-chat", nil, nil), nil
	})
	r.Register("anthropic-messages", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("anthropic-messages", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	p, err := ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://api.deepseek.com",
		API:     "openai-chat",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "deepseek" {
		t.Errorf("expected 'deepseek', got %q", p.Name())
	}
}

func TestResolveProviderFallback(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("openai-chat", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-chat", nil, nil), nil
	})
	r.Register("openai-responses", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-responses", nil, nil), nil
	})
	r.Register("anthropic-messages", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("anthropic-messages", nil, nil), nil
	})
	r.Register("google-gemini", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-gemini", nil, nil), nil
	})
	r.Register("google-vertex", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-vertex", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	p, err := ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
		API:     "openai-chat",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai-chat" {
		t.Errorf("expected 'openai-chat', got %q", p.Name())
	}

	p, err = ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
		API:     "anthropic-messages",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic-messages" {
		t.Errorf("expected 'anthropic-messages', got %q", p.Name())
	}

	p, err = ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
		API:     "openai-responses",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai-responses" {
		t.Errorf("expected 'openai-responses', got %q", p.Name())
	}
}

func TestResolveProviderUnknownAPI(t *testing.T) {
	orig := globalRegistry
	globalRegistry = NewProviderRegistry()
	defer func() { globalRegistry = orig }()

	_, err := ResolveProvider(&config.ProviderConfig{
		API: "unknown-api",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported API type") {
		t.Fatalf("error = %v, want unsupported API type", err)
	}
}

func TestResolveProviderUnregisteredVendorUsesAPI(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("openai-chat", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-chat", nil, nil), nil
	})
	r.Register("openai-responses", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-responses", nil, nil), nil
	})
	r.Register("anthropic-messages", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("anthropic-messages", nil, nil), nil
	})
	r.Register("google-gemini", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-gemini", nil, nil), nil
	})
	r.Register("google-vertex", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-vertex", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	cases := []struct {
		name string
		cfg  *config.ProviderConfig
		want string
	}{
		{"openai-chat", &config.ProviderConfig{Vendor: "unregistered", API: "openai-chat"}, "openai-chat"},
		{"openai-responses", &config.ProviderConfig{Vendor: "unregistered", API: "openai-responses"}, "openai-responses"},
		{"anthropic-messages", &config.ProviderConfig{Vendor: "unregistered", API: "anthropic-messages"}, "anthropic-messages"},
		{"google-gemini", &config.ProviderConfig{Vendor: "unregistered", API: "google-gemini"}, "google-gemini"},
		{"google-vertex", &config.ProviderConfig{Vendor: "unregistered", API: "google-vertex"}, "google-vertex"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ResolveProvider(tt.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Name() != tt.want {
				t.Fatalf("provider = %q, want %q", p.Name(), tt.want)
			}
		})
	}
}

func TestResolveProviderVendorPriorityOverAPIFallback(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("openai", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai", nil, nil), nil
	})
	r.Register("openai-responses", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("openai-responses", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	p, err := ResolveProvider(&config.ProviderConfig{
		Vendor: "openai",
		API:    "openai-responses",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected vendor provider 'openai', got %q", p.Name())
	}
}

func TestResolveProviderGoogleFallback(t *testing.T) {
	r := NewProviderRegistry()
	r.Register("google-gemini", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-gemini", nil, nil), nil
	})
	r.Register("google-vertex", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("google-vertex", nil, nil), nil
	})
	orig := globalRegistry
	globalRegistry = r
	defer func() { globalRegistry = orig }()

	p, err := ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
		API:     "google-gemini",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "google-gemini" {
		t.Errorf("expected 'google-gemini', got %q", p.Name())
	}

	p, err = ResolveProvider(&config.ProviderConfig{
		BaseURL: "https://unknown.example.com/v1",
		API:     "google-vertex",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "google-vertex" {
		t.Errorf("expected 'google-vertex', got %q", p.Name())
	}
}

func TestGlobalRegistry(t *testing.T) {
	Register("global_test", func(cfg *config.ProviderConfig) (Provider, error) {
		return NewMockProvider("global_test", nil, nil), nil
	})

	names := ListProviders()
	found := false
	for _, n := range names {
		if n == "global_test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'global_test' in list")
	}

	p, err := CreateProvider("global_test", &config.ProviderConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "global_test" {
		t.Errorf("expected 'global_test', got %q", p.Name())
	}
}
