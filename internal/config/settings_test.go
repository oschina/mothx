package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()

	if s.DefaultProvider != "deepseek-openai" {
		t.Errorf("expected default provider 'deepseek-openai', got '%s'", s.DefaultProvider)
	}

	if s.DefaultModel != "deepseek-v4-flash" {
		t.Errorf("expected default model 'deepseek-v4-flash', got '%s'", s.DefaultModel)
	}

	if s.DefaultMode != "yolo" {
		t.Errorf("expected default mode 'yolo', got '%s'", s.DefaultMode)
	}

	if s.Authored {
		t.Error("expected authored commits to be disabled by default")
	}
	if s.IsArtifactEnabled() || s.IsACPArtifactEnabled() {
		t.Fatal("artifact publishing must default to disabled for terminal and ACP runtimes")
	}
	*s.EnableArtifact = true
	if !s.IsArtifactEnabled() || s.IsACPArtifactEnabled() {
		t.Fatal("terminal artifact setting must not enable ACP artifacts")
	}
	*s.EnableArtifact = false
	*s.EnableACPArtifact = true
	if s.IsArtifactEnabled() || !s.IsACPArtifactEnabled() {
		t.Fatal("ACP artifact setting must not enable terminal artifacts")
	}

	if len(s.Providers) < 35 {
		t.Errorf("expected at least 35 providers, got %d", len(s.Providers))
	}

	if s.Providers["openai"] == nil {
		t.Fatal("expected default openai provider")
	}
	if s.Providers["openai"].MaxImagesPerRequest != 1500 {
		t.Fatalf("openai MaxImagesPerRequest = %d, want 1500", s.Providers["openai"].MaxImagesPerRequest)
	}
	if s.Providers["anthropic"] == nil {
		t.Fatal("expected default anthropic provider")
	}
	if s.Providers["xiaomi"] == nil {
		t.Fatal("expected default xiaomi provider")
	}
	if s.Providers["google-gemini"] == nil {
		t.Fatal("expected default google-gemini provider")
	}
	if s.Providers["google-vertex"] == nil {
		t.Fatal("expected default google-vertex provider")
	}

	for _, name := range []string{"openrouter", "openrouter-free-models", "minimax", "zai", "modelscope", "alibaba-standard", "alibaba-coding-plan", "alibaba-token-plan", "moark", "groq", "moonshotai", "xai", "together", "fireworks", "kimi-coding", "xiaomi-token-plan-cn"} {
		if s.Providers[name] == nil {
			t.Fatalf("expected default %s provider", name)
		}
	}
	if got := s.Providers["kimi-coding"].Headers["User-Agent"]; got != "opencode/1.17.18" {
		t.Fatalf("kimi-coding User-Agent = %q, want %q", got, "opencode/1.17.18")
	}
	for _, name := range []string{"openai", "codeok", "yescode"} {
		if got := s.Providers[name].Headers["User-Agent"]; got != "codex_cli_rs/0.144.4" {
			t.Fatalf("%s User-Agent = %q, want %q", name, got, "codex_cli_rs/0.144.4")
		}
	}
	kimiCoding := s.Providers["kimi-coding"]
	if kimiCoding.BaseURL != "https://api.kimi.com/coding/v1" || kimiCoding.API != "openai-chat" {
		t.Fatalf("kimi-coding endpoint = (%q, %q), want (%q, %q)", kimiCoding.BaseURL, kimiCoding.API, "https://api.kimi.com/coding/v1", "openai-chat")
	}
	if kimiCoding.ThinkingFormat != "kimi" {
		t.Fatalf("kimi-coding ThinkingFormat = %q, want kimi", kimiCoding.ThinkingFormat)
	}
	var k3 *ModelConfig
	for i := range kimiCoding.Models {
		if kimiCoding.Models[i].ID == "k3" {
			k3 = &kimiCoding.Models[i]
			break
		}
	}
	if k3 == nil || !k3.Reasoning || k3.ContextWindow != 1000000 {
		t.Fatalf("kimi-coding k3 = %#v, want reasoning model with 1M context", k3)
	}
	var k3_256k *ModelConfig
	for i := range kimiCoding.Models {
		if kimiCoding.Models[i].ID == "k3-256k" {
			k3_256k = &kimiCoding.Models[i]
			break
		}
	}
	if k3_256k == nil || !k3_256k.Reasoning || k3_256k.ContextWindow != 262144 || k3_256k.MaxTokens != 0 {
		t.Fatalf("kimi-coding k3-256k = %#v, want reasoning model with 256K context and 0 MaxTokens", k3_256k)
	}

	if s.DefaultThinkingLevel != "medium" {
		t.Errorf("expected thinking level 'medium', got '%s'", s.DefaultThinkingLevel)
	}
	if s.StatusLine.Enabled {
		t.Fatalf("expected statusLine disabled by default")
	}
	if s.StatusLine.Type != "command" {
		t.Fatalf("expected statusLine.type command, got %q", s.StatusLine.Type)
	}
	if s.StatusLine.TimeoutMs != 800 {
		t.Fatalf("expected statusLine.timeoutMs 800, got %d", s.StatusLine.TimeoutMs)
	}
	if s.StatusLine.Fallback != "builtin" {
		t.Fatalf("expected statusLine.fallback builtin, got %q", s.StatusLine.Fallback)
	}
	if s.WebSearch.Enabled == nil || *s.WebSearch.Enabled {
		t.Fatalf("expected web search to be disabled by default, got %#v", s.WebSearch.Enabled)
	}
	if s.WebSearch.Provider != "openai" || s.WebSearch.ProviderType != "openai-responses" {
		t.Fatalf("unexpected web search defaults: %#v", s.WebSearch)
	}
	if !s.Retry.Enabled || s.Retry.MaxRetries != 5 || s.Retry.BaseDelayMs != 3000 {
		t.Fatalf("unexpected retry defaults: %#v", s.Retry)
	}
	if s.WebSearch.Model != "" {
		t.Fatalf("expected empty web search model by default, got %q", s.WebSearch.Model)
	}
}

func TestAuthoredSettingRoundTrip(t *testing.T) {
	data, err := json.Marshal(&Settings{Authored: true})
	if err != nil {
		t.Fatalf("marshal authored setting: %v", err)
	}
	if string(data) == "{}" || !json.Valid(data) {
		t.Fatalf("authored setting was not serialized: %s", data)
	}

	var decoded Settings
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal authored setting: %v", err)
	}
	if !decoded.Authored {
		t.Fatal("authored setting did not round-trip as enabled")
	}

	disabled, err := json.Marshal(&Settings{})
	if err != nil {
		t.Fatalf("marshal disabled setting: %v", err)
	}
	if strings.Contains(string(disabled), `"authored"`) {
		t.Fatalf("disabled authored setting should remain omitted, got %s", disabled)
	}
}

func TestGetProviderConfig(t *testing.T) {
	s := DefaultSettings()

	// Test existing provider (openai format)
	pc := s.GetProviderConfig("deepseek-openai")
	if pc == nil {
		t.Fatal("expected provider config, got nil")
	}

	if pc.API != "openai-chat" {
		t.Errorf("expected API 'openai-chat', got '%s'", pc.API)
	}

	// Test non-existing provider
	pc = s.GetProviderConfig("nonexistent")
	if pc != nil {
		t.Errorf("expected nil, got provider config")
	}
}

func TestGetModelConfig(t *testing.T) {
	s := DefaultSettings()

	// Test existing model
	mc := s.GetModelConfig("deepseek-openai", "deepseek-v4-flash")
	if mc == nil {
		t.Fatal("expected model config, got nil")
	}

	if mc.Name != "DeepSeek V4 Flash" {
		t.Errorf("expected name 'DeepSeek V4 Flash', got '%s'", mc.Name)
	}

	// Test non-existing model
	mc = s.GetModelConfig("deepseek-openai", "nonexistent")
	if mc != nil {
		t.Errorf("expected nil, got model config")
	}

	// Test non-existing provider
	mc = s.GetModelConfig("nonexistent", "model")
	if mc != nil {
		t.Errorf("expected nil, got model config")
	}
}

func TestMoarkModelMaxTokens(t *testing.T) {
	s := DefaultSettings()
	want := map[string]int{
		"glm-5.1":                131072,
		"qwen3.5-flash":          65536,
		"qwen3.6-flash":          65536,
		"qwen3.6-plus":           65536,
		"deepseek-v4-pro":        384000,
		"deepseek-v4-pro-0813":   0,
		"qwen3.7-max":            65536,
		"qwen3.8-max":            0,
		"qwen3.8-max-0902":       131072,
		"qwen3.8-27b":            0,
		"glm-5.3":                131072,
		"glm-5.3-flash":          131072,
		"ernie-5.0-thinking":     65536,
		"kimi-k2.5":              262144,
		"kimi-k2.6":              262144,
		"kimi-k2.7-code":         262144,
		"kimi-k3":                262144,
		"glm-5":                  32768,
		"qwen3.7-plus":           65536,
		"minimax-m2.7":           131072,
		"minimax-m3":             128000,
		"mimo-v2.5-pro":          131072,
		"gemma-4-26b-a4b-it":     32768,
		"deepseek-v4-flash":      384000,
		"deepseek-v4-flash-0731": 0,
		"deepseek-v4.1-flash":    0,
		"step-3.7-flash":         16384,
		"qwen3.8-flash":          0,
	}

	moark := s.Providers["moark"]
	if moark == nil {
		t.Fatal("expected moark provider")
	}
	if moark.MaxImagesPerRequest != 5 {
		t.Fatalf("moark MaxImagesPerRequest = %d, want 5", moark.MaxImagesPerRequest)
	}
	if s.Providers["gitee"].MaxImagesPerRequest != 5 {
		t.Fatalf("gitee MaxImagesPerRequest = %d, want 5", s.Providers["gitee"].MaxImagesPerRequest)
	}
	if len(moark.Models) != len(want) {
		t.Fatalf("moark models = %d, want %d", len(moark.Models), len(want))
	}
	for _, model := range moark.Models {
		wantMaxTokens, ok := want[model.ID]
		if !ok {
			t.Fatalf("unexpected moark model %q", model.ID)
		}
		if model.MaxTokens != wantMaxTokens {
			t.Fatalf("moark %s MaxTokens = %d, want %d", model.ID, model.MaxTokens, wantMaxTokens)
		}
	}
}

func TestGiteeMoarkQwen3827BDefaults(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"gitee", "moark"} {
		model := s.GetModelConfig(providerName, "qwen3.8-27b")
		if model == nil {
			t.Fatalf("%s missing qwen3.8-27b", providerName)
		}
		if !model.Reasoning || model.ContextWindow != 1000000 {
			t.Fatalf("%s qwen3.8-27b = %#v, want reasoning model with 1M context", providerName, model)
		}
		wantInput := []string{"text", "image", "video"}
		if len(model.Input) != len(wantInput) {
			t.Fatalf("%s qwen3.8-27b input = %#v, want %#v", providerName, model.Input, wantInput)
		}
		for i := range wantInput {
			if model.Input[i] != wantInput[i] {
				t.Fatalf("%s qwen3.8-27b input = %#v, want %#v", providerName, model.Input, wantInput)
			}
		}
		if model.MaxTokens != 0 || model.MaxTokensWasSet() {
			t.Fatalf("%s qwen3.8-27b maxTokens = %d, explicitly set = %v; want 0, false", providerName, model.MaxTokens, model.MaxTokensWasSet())
		}
	}
}

func TestGiteeMoarkQwen38FlashDefaults(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"gitee", "moark"} {
		model := s.GetModelConfig(providerName, "qwen3.8-flash")
		if model == nil {
			t.Fatalf("%s missing qwen3.8-flash", providerName)
		}
		if !model.Reasoning || model.ContextWindow != 1000000 {
			t.Fatalf("%s qwen3.8-flash = %#v, want reasoning model with 1M context", providerName, model)
		}
		wantInput := []string{"text", "image"}
		if len(model.Input) != len(wantInput) {
			t.Fatalf("%s qwen3.8-flash input = %#v, want %#v", providerName, model.Input, wantInput)
		}
		for i := range wantInput {
			if model.Input[i] != wantInput[i] {
				t.Fatalf("%s qwen3.8-flash input = %#v, want %#v", providerName, model.Input, wantInput)
			}
		}
		if model.MaxTokens != 0 || model.MaxTokensWasSet() {
			t.Fatalf("%s qwen3.8-flash maxTokens = %d, explicitly set = %v; want 0, false", providerName, model.MaxTokens, model.MaxTokensWasSet())
		}
	}
}

func TestGiteeMoarkQwen38Max0902Defaults(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"gitee", "moark"} {
		model := s.GetModelConfig(providerName, "qwen3.8-max-0902")
		if model == nil {
			t.Fatalf("%s missing qwen3.8-max-0902", providerName)
		}
		if !model.Reasoning || model.ContextWindow != 1000000 {
			t.Fatalf("%s qwen3.8-max-0902 = %#v, want reasoning model with 1M context", providerName, model)
		}
		wantInput := []string{"text", "image"}
		if len(model.Input) != len(wantInput) {
			t.Fatalf("%s qwen3.8-max-0902 input = %#v, want %#v", providerName, model.Input, wantInput)
		}
		for i := range wantInput {
			if model.Input[i] != wantInput[i] {
				t.Fatalf("%s qwen3.8-max-0902 input = %#v, want %#v", providerName, model.Input, wantInput)
			}
		}
		if model.MaxTokens != 131072 {
			t.Fatalf("%s qwen3.8-max-0902 maxTokens = %d, want 131072", providerName, model.MaxTokens)
		}
	}
}

func TestGiteeMoarkGLM53FlashDefaults(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"gitee", "moark"} {
		model := s.GetModelConfig(providerName, "glm-5.3-flash")
		if model == nil {
			t.Fatalf("%s missing glm-5.3-flash", providerName)
		}
		if !model.Reasoning || model.ContextWindow != 1000000 {
			t.Fatalf("%s glm-5.3-flash = %#v, want reasoning model with 1M context", providerName, model)
		}
		wantInput := []string{"text", "image"}
		if len(model.Input) != len(wantInput) {
			t.Fatalf("%s glm-5.3-flash input = %#v, want %#v", providerName, model.Input, wantInput)
		}
		for i := range wantInput {
			if model.Input[i] != wantInput[i] {
				t.Fatalf("%s glm-5.3-flash input = %#v, want %#v", providerName, model.Input, wantInput)
			}
		}
		if model.MaxTokens != 131072 {
			t.Fatalf("%s glm-5.3-flash maxTokens = %d, want 131072", providerName, model.MaxTokens)
		}
	}
}

func TestGiteeMoarkDeepSeekV41FlashDefaults(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"gitee", "moark"} {
		model := s.GetModelConfig(providerName, "deepseek-v4.1-flash")
		if model == nil {
			t.Fatalf("%s missing deepseek-v4.1-flash", providerName)
		}
		if !model.Reasoning || model.ContextWindow != 1000000 {
			t.Fatalf("%s deepseek-v4.1-flash = %#v, want reasoning model with 1M context", providerName, model)
		}
		wantInput := []string{"text", "image"}
		if len(model.Input) != len(wantInput) {
			t.Fatalf("%s deepseek-v4.1-flash input = %#v, want %#v", providerName, model.Input, wantInput)
		}
		for i := range wantInput {
			if model.Input[i] != wantInput[i] {
				t.Fatalf("%s deepseek-v4.1-flash input = %#v, want %#v", providerName, model.Input, wantInput)
			}
		}
		if model.MaxTokens != 0 || model.MaxTokensWasSet() {
			t.Fatalf("%s deepseek-v4.1-flash maxTokens = %d, explicitly set = %v; want 0, false", providerName, model.MaxTokens, model.MaxTokensWasSet())
		}
	}
}

func TestVolcenginePlanModelsUseSharedMaxTokens(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"volcengine-agentplan", "volcengine-codingplan"} {
		p := s.Providers[providerName]
		if p == nil {
			t.Fatalf("expected %s provider", providerName)
		}
		for _, model := range p.Models {
			if model.ID == "glm-5.3" || model.ID == "glm-5.3-flash" {
				if model.MaxTokens != 0 {
					t.Fatalf("%s %s MaxTokens = %d, want 0 (provider default)", providerName, model.ID, model.MaxTokens)
				}
				continue
			}
			if model.MaxTokens != 100000 {
				t.Fatalf("%s %s MaxTokens = %d, want 100000", providerName, model.ID, model.MaxTokens)
			}
		}
	}
}

func TestVolcengineGLMModelsUseOneMillionContextAndProviderDefaultMaxTokens(t *testing.T) {
	s := DefaultSettings()
	for _, providerName := range []string{"volcengine", "volcengine-agentplan", "volcengine-codingplan"} {
		for _, modelID := range []string{"glm-5.3", "glm-5.3-flash"} {
			model := s.GetModelConfig(providerName, modelID)
			if model == nil {
				t.Fatalf("%s %s is missing", providerName, modelID)
			}
			if model.ContextWindow != 1000000 {
				t.Fatalf("%s %s ContextWindow = %d, want 1000000", providerName, modelID, model.ContextWindow)
			}
			if model.MaxTokens != 0 {
				t.Fatalf("%s %s MaxTokens = %d, want 0", providerName, modelID, model.MaxTokens)
			}
			if !model.Reasoning {
				t.Fatalf("%s %s Reasoning = false, want true", providerName, modelID)
			}
			wantInput := []string{"text"}
			if modelID == "glm-5.3-flash" {
				wantInput = []string{"text", "image"}
			}
			if len(model.Input) != len(wantInput) {
				t.Fatalf("%s %s Input = %#v, want %#v", providerName, modelID, model.Input, wantInput)
			}
			for i := range wantInput {
				if model.Input[i] != wantInput[i] {
					t.Fatalf("%s %s Input = %#v, want %#v", providerName, modelID, model.Input, wantInput)
				}
			}
		}
	}
}
func TestVolcengineAgentPlanKimiK3Context(t *testing.T) {
	s := DefaultSettings()
	model := s.GetModelConfig("volcengine-agentplan", "kimi-k3")
	if model == nil || model.ContextWindow != 1000000 {
		t.Fatalf("volcengine-agentplan kimi-k3 = %#v, want 1M context", model)
	}
}

func TestDefaultAgnesProviders(t *testing.T) {
	s := DefaultSettings()
	wantEndpoints := map[string]string{
		"agnes":    "https://apihub.agnes-ai.com/v1",
		"agnes-cn": "https://api.agnes-ai.cn/v1",
	}
	wantModels := map[string]struct {
		contextWindow int
		maxTokens     int
	}{
		"agnes-2.5-flash": {200000, 0},
		"agnes-2.5-pro":   {256000, 0},
		"agnes-3.0-flash": {512000, 65535},
	}

	for name, baseURL := range wantEndpoints {
		provider := s.Providers[name]
		if provider == nil {
			t.Fatalf("expected default %s provider", name)
		}
		if provider.Vendor != "agnes" {
			t.Fatalf("%s vendor = %q, want agnes", name, provider.Vendor)
		}
		if provider.BaseURL != baseURL || provider.API != "openai-chat" {
			t.Fatalf("%s endpoint = (%q, %q), want (%q, openai-chat)", name, provider.BaseURL, provider.API, baseURL)
		}
		if len(provider.Models) != len(wantModels) {
			t.Fatalf("%s model count = %d, want %d", name, len(provider.Models), len(wantModels))
		}
		for modelID, want := range wantModels {
			model := s.GetModelConfig(name, modelID)
			if model == nil {
				t.Fatalf("%s missing model %q", name, modelID)
			}
			if model.ContextWindow != want.contextWindow {
				t.Fatalf("%s %s context window = %d, want %d", name, modelID, model.ContextWindow, want.contextWindow)
			}
			if model.MaxTokens != want.maxTokens {
				t.Fatalf("%s %s maxTokens = %d, want %d", name, modelID, model.MaxTokens, want.maxTokens)
			}
			if want.maxTokens == 0 && model.MaxTokensWasSet() {
				t.Fatalf("%s %s must omit maxTokens so the provider default applies", name, modelID)
			}
			if !model.Reasoning {
				t.Fatalf("%s %s must support reasoning", name, modelID)
			}
			if !slices.Contains(model.Input, "text") || !slices.Contains(model.Input, "image") {
				t.Fatalf("%s %s input = %v, want text+image", name, modelID, model.Input)
			}
		}
	}
}

func TestRoutedProviderModelMaxTokensAreExplicit(t *testing.T) {
	s := DefaultSettings()
	wantByProvider := map[string]map[string]int{
		"openrouter-free-models": {
			"tencent/hy3": 38106,
		},
		"minimax": {
			"MiniMax-M3":             128000,
			"MiniMax-M2.7":           131072,
			"MiniMax-M2.7-highspeed": 131072,
			"MiniMax-M2.5":           131072,
			"MiniMax-M2.5-highspeed": 131072,
		},
		"modelscope": {
			"deepseek-ai/DeepSeek-V4-Flash-0731":         384000,
			"deepseek-ai/DeepSeek-V4-Pro":                384000,
			"deepseek-ai/DeepSeek-V4-Pro-0813":           384000,
			"MedAIBase/AntAngelMed":                      16384,
			"meituan-longcat/LongCat-Flash-Lite":         32768,
			"MiniMax/MiniMax-M1-80k":                     80000,
			"MiniMax/MiniMax-M3":                         128000,
			"mistralai/Mistral-Large-Instruct-2407":      32768,
			"MusePublic/Qwen-Image-Edit":                 16384,
			"opencompass/CompassJudger-1-32B-Instruct":   4096,
			"OpenGVLab/InternVL3_5-241B-A28B":            16384,
			"PaddlePaddle/ERNIE-4.5-0.3B-PT":             65536,
			"PaddlePaddle/ERNIE-4.5-21B-A3B-PT":          65536,
			"PaddlePaddle/ERNIE-4.5-300B-A47B-PT":        65536,
			"PaddlePaddle/ERNIE-4.5-VL-28B-A3B-PT":       65536,
			"Qwen/Qwen-Image-Edit":                       16384,
			"Qwen/Qwen3-14B":                             38912,
			"Qwen/Qwen3-235B-A22B":                       38912,
			"Qwen/Qwen3-235B-A22B-Instruct-2507":         65536,
			"Qwen/Qwen3-235B-A22B-Thinking-2507":         81920,
			"Qwen/Qwen3-30B-A3B":                         38912,
			"Qwen/Qwen3-30B-A3B-Thinking-2507":           81920,
			"Qwen/Qwen3-4B":                              38912,
			"Qwen/Qwen3-8B":                              38912,
			"Qwen/Qwen3-Coder-30B-A3B-Instruct":          65536,
			"Qwen/Qwen3-Next-80B-A3B-Instruct":           65536,
			"Qwen/Qwen3-Next-80B-A3B-Thinking":           81920,
			"Qwen/Qwen3-VL-235B-A22B-Instruct":           32768,
			"Qwen/Qwen3-VL-8B-Instruct":                  32768,
			"Qwen/Qwen3-VL-8B-Thinking":                  40960,
			"Qwen/Qwen3.5-122B-A10B":                     81920,
			"Qwen/Qwen3.5-27B":                           81920,
			"Qwen/Qwen3.5-35B-A3B":                       81920,
			"Qwen/Qwen3.5-397B-A17B":                     130000,
			"Qwen/Qwen3.8-27B":                           131072,
			"Shanghai_AI_Laboratory/Intern-S1":           32768,
			"Shanghai_AI_Laboratory/Intern-S1-mini":      32768,
			"Shanghai_AI_Laboratory/Intern-S2-Preview":   32768,
			"stepfun-ai/Step-3.5-Flash":                  32768,
			"stepfun-ai/Step-3.7-Flash":                  32768,
			"Tencent-Hunyuan/Hy3":                        131072,
			"XGenerationLab/XiYanSQL-QwenCoder-32B-2412": 16384,
			"XGenerationLab/XiYanSQL-QwenCoder-32B-2504": 16384,
			"ZhipuAI/GLM-4.7-Flash":                      131072,
			"ZhipuAI/GLM-5.2":                            131072,
		},
		"gitee": {
			"glm-5.1":                131072,
			"qwen3.6-plus":           65536,
			"deepseek-v4-pro":        384000,
			"deepseek-v4-pro-0813":   0,
			"qwen3.7-max":            65536,
			"qwen3.8-max":            0,
			"qwen3.8-max-0902":       131072,
			"qwen3.8-27b":            0,
			"glm-5.3":                131072,
			"glm-5.3-flash":          131072,
			"kimi-k2.7-code":         262144,
			"kimi-k3":                262144,
			"glm-5":                  32768,
			"qwen3.7-plus":           65536,
			"minimax-m2.7":           131072,
			"minimax-m3":             128000,
			"deepseek-v4-flash":      384000,
			"deepseek-v4-flash-0731": 0,
			"deepseek-v4.1-flash":    0,
			"qwen3.8-flash":          0,
		},
		"alibaba-standard": {
			"qwen3.6-plus":      65536,
			"qwen3.7-plus":      65536,
			"qwen3.7-max":       65536,
			"glm-5.1":           131072,
			"deepseek-v4-pro":   384000,
			"deepseek-v4-flash": 384000,
		},
		"alibaba-coding-plan": {
			"qwen3.5-plus":         65536,
			"qwen3.6-plus":         65536,
			"qwen3.7-plus":         65536,
			"glm-5":                32768,
			"kimi-k2.5":            262144,
			"MiniMax-M2.5":         131072,
			"qwen3-coder-plus":     65536,
			"qwen3-coder-next":     65536,
			"qwen3-max-2026-01-23": 65536,
			"glm-4.7":              131072,
		},
		"alibaba-token-plan": {
			"qwen3.6-plus":      65536,
			"qwen3.7-max":       65536,
			"qwen3.6-flash":     65536,
			"deepseek-v4-pro":   384000,
			"deepseek-v4-flash": 384000,
			"deepseek-v3.2":     65536,
			"kimi-k2.6":         262144,
			"kimi-k2.5":         262144,
			"glm-5.1":           131072,
			"glm-5":             32768,
			"MiniMax-M2.5":      131072,
		},
	}

	for providerName, wantModels := range wantByProvider {
		provider := s.Providers[providerName]
		if provider == nil {
			t.Fatalf("expected %s provider", providerName)
		}
		gotModels := map[string]int{}
		for _, model := range provider.Models {
			gotModels[model.ID] = model.MaxTokens
		}
		for modelID, wantMaxTokens := range wantModels {
			got, ok := gotModels[modelID]
			if !ok {
				t.Fatalf("%s missing model %q", providerName, modelID)
			}
			if got != wantMaxTokens {
				t.Fatalf("%s %s MaxTokens = %d, want %d", providerName, modelID, got, wantMaxTokens)
			}
			if got == 8192 {
				t.Fatalf("%s %s still uses placeholder MaxTokens 8192", providerName, modelID)
			}
		}
	}
}

func TestResolveConfigJSONExplicitZeroValuesOverrideDefaults(t *testing.T) {
	var runtime Settings
	data := []byte(`{
		"providers": {
			"longcat": {
				"vendor": "",
				"baseUrl": "",
				"headers": {},
				"models": [
					{
						"id": "LongCat-2.0",
						"reasoning": false,
						"input": [],
						"cost": null
					}
				]
			}
		}
	}`)
	if err := json.Unmarshal(data, &runtime); err != nil {
		t.Fatalf("unmarshal runtime: %v", err)
	}

	pc := ResolveProviderConfig("longcat", &runtime)
	if pc.Vendor != "" {
		t.Fatalf("Vendor = %q, want explicit empty override", pc.Vendor)
	}
	if pc.BaseURL != "" {
		t.Fatalf("BaseURL = %q, want explicit empty override", pc.BaseURL)
	}
	if pc.Headers == nil || len(pc.Headers) != 0 {
		t.Fatalf("Headers = %#v, want explicit empty map", pc.Headers)
	}
	if len(pc.Models) != 1 {
		t.Fatalf("Models = %d, want explicit one-model configuration", len(pc.Models))
	}

	mc := ResolveModelConfig("longcat", "LongCat-2.0", &runtime)
	if mc == nil {
		t.Fatal("expected model config")
	}
	if mc.Reasoning {
		t.Fatal("Reasoning = true, want explicit false override")
	}
	if mc.Input == nil || len(mc.Input) != 0 {
		t.Fatalf("Input = %#v, want explicit empty slice", mc.Input)
	}
	if mc.Cost != nil {
		t.Fatalf("Cost = %#v, want explicit null override", mc.Cost)
	}
	if mc.ContextWindow == 0 {
		t.Fatal("ContextWindow lost builtin default")
	}
}

func TestMergeModelConfigsKeepsBuiltinOnlyModels(t *testing.T) {
	builtin := []ModelConfig{
		{ID: "builtin-a", Name: "Builtin A", ContextWindow: 1000},
		{ID: "shared", Name: "Builtin Shared", ContextWindow: 1000},
	}
	runtime := []ModelConfig{{ID: "shared", Name: "Runtime Shared", MaxTokens: 2000}}
	merged := mergeModelConfigs(builtin, runtime)
	if len(merged) != 2 {
		t.Fatalf("merged models = %d, want 2", len(merged))
	}
	if merged[0].ID != "shared" || merged[0].Name != "Runtime Shared" {
		t.Fatalf("runtime override = %#v", merged[0])
	}
	if merged[1].ID != "builtin-a" || merged[1].ContextWindow != 1000 {
		t.Fatalf("builtin fallback = %#v", merged[1])
	}
}
func TestResolveProviderConfigPreservesFieldPresenceAcrossGlobalAndProject(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	configDir := filepath.Join(tmpDir, "config")
	t.Setenv("MOTHX_DIR", configDir)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(GlobalSettingsPath(), []byte(`{
		"providers": {
			"longcat": {"apiKey": "global-key"}
		}
	}`), 0600); err != nil {
		t.Fatalf("write global settings: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(ProjectSettingsPath()), 0700); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(ProjectSettingsPath(), []byte(`{
		"providers": {
			"longcat": {"baseUrl": "https://project.longcat.test"}
		}
	}`), 0600); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	pc := ResolveProviderConfig("longcat", s)
	if pc.APIKey != "global-key" {
		t.Fatalf("APIKey = %q, want global-key", pc.APIKey)
	}
	if pc.BaseURL != "https://project.longcat.test" {
		t.Fatalf("BaseURL = %q, want project override", pc.BaseURL)
	}
}

func TestConfigDir(t *testing.T) {
	// Test with env var
	t.Setenv("MOTHX_DIR", "")
	t.Setenv("MOTHX_DIR", "/tmp/test-mothx")
	dir := ConfigDir()
	if dir != "/tmp/test-mothx" {
		t.Errorf("expected '/tmp/test-mothx', got '%s'", dir)
	}
	t.Setenv("MOTHX_DIR", "")

	// Test default
	dir = ConfigDir()
	if dir == "" {
		t.Error("expected non-empty config dir")
	}
}

func TestLoadSettingsCreatesMothXConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})

	t.Setenv("HOME", tmpDir)
	t.Setenv("APPDATA", "")
	t.Setenv("MOTHX_DIR", "")

	if _, _, err := LoadSettingsWithMeta(); err != nil {
		t.Fatalf("load settings: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, ".mothx", "settings.json")); err != nil {
		t.Fatalf("expected .mothx settings file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".vibecoding")); !os.IsNotExist(err) {
		t.Fatalf("expected no .vibecoding directory, stat err=%v", err)
	}
}

func TestLoadSettingsDoesNotReadVibeCodingConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})

	legacyDir := filepath.Join(tmpDir, ".vibecoding")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatalf("mkdir legacy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "settings.json"), []byte(`{"defaultModel":"legacy-model"}`), 0600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	t.Setenv("HOME", tmpDir)
	t.Setenv("APPDATA", "")
	t.Setenv("MOTHX_DIR", "")
	t.Setenv("VIBECODING_DIR", legacyDir)

	settings, meta, err := LoadSettingsWithMeta()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if settings.DefaultModel == "legacy-model" {
		t.Fatal("settings loaded from the legacy .vibecoding directory")
	}
	wantPath := filepath.Join(tmpDir, ".mothx", "settings.json")
	if meta.GlobalSettingsPath != wantPath {
		t.Fatalf("global settings path = %q, want %q", meta.GlobalSettingsPath, wantPath)
	}
}

func TestGlobalSettingsPath(t *testing.T) {
	path := GlobalSettingsPath()
	if path == "" {
		t.Error("expected non-empty path")
	}

	if !contains(path, "settings.json") {
		t.Error("expected path to contain 'settings.json'")
	}
}

func TestProjectSettingsPath(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	path := ProjectSettingsPath()
	if path != filepath.Join(ProjectDirName, "settings.json") {
		t.Errorf("expected '%s/settings.json', got '%s'", ProjectDirName, path)
	}
}

func TestLoadSettings(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")

	// Write test settings
	settingsJSON := `{
		"providers": {
			"test": {
				"baseUrl": "https://api.test.com",
				"apiKey": "test-key",
				"api": "openai-chat",
				"models": [
					{
						"id": "test-model",
						"name": "Test Model",
						"contextWindow": 100000,
						"maxTokens": 4096
					}
				]
			}
		},
		"defaultProvider": "test",
		"defaultModel": "test-model"
	}`

	if err := os.WriteFile(settingsPath, []byte(settingsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Load settings
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	s := DefaultSettings()
	if err := json.Unmarshal(data, s); err != nil {
		t.Fatal(err)
	}

	if s.DefaultProvider != "test" {
		t.Errorf("expected provider 'test', got '%s'", s.DefaultProvider)
	}
	if s.WebSearch.Model != "" {
		t.Errorf("expected empty webSearch.model, got '%s'", s.WebSearch.Model)
	}
}

func TestLoadSettingsAppliesProjectOverridesAndEnv(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	configDir := filepath.Join(tmpDir, "config")
	if err := os.Setenv("MOTHX_DIR", configDir); err != nil {
		t.Fatalf("set MOTHX_DIR: %v", err)
	}
	if err := os.Setenv("VIBECODING_PROVIDER", "env-provider"); err != nil {
		t.Fatalf("set VIBECODING_PROVIDER: %v", err)
	}
	if err := os.Setenv("VIBECODING_MODEL", "env-model"); err != nil {
		t.Fatalf("set VIBECODING_MODEL: %v", err)
	}
	if err := os.Setenv("VIBECODING_MODE", "plan"); err != nil {
		t.Fatalf("set VIBECODING_MODE: %v", err)
	}
	if err := os.Setenv("VIBECODING_THINKING", "high"); err != nil {
		t.Fatalf("set VIBECODING_THINKING: %v", err)
	}
	defer func() {
		_ = os.Unsetenv("MOTHX_DIR")
		_ = os.Unsetenv("VIBECODING_PROVIDER")
		_ = os.Unsetenv("VIBECODING_MODEL")
		_ = os.Unsetenv("VIBECODING_MODE")
		_ = os.Unsetenv("VIBECODING_THINKING")
	}()

	if err := os.MkdirAll(filepath.Dir(ProjectSettingsPath()), 0700); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	projectSettings := `{
		"sessionDir": "./sessions",
		"providers": {
			"project-provider": {
				"baseUrl": "https://example.test",
				"api": "openai-chat",
				"models": [{"id": "project-model", "name": "Project Model"}]
			}
		},
		"contextFiles": {"enabled": false, "extraFiles": ["extra.md"]},
		"approval": {"bashWhitelist": ["go test "]}
	}`
	if err := os.WriteFile(ProjectSettingsPath(), []byte(projectSettings), 0600); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	if s.DefaultProvider != "env-provider" {
		t.Fatalf("DefaultProvider = %q, want env-provider", s.DefaultProvider)
	}
	if s.DefaultModel != "env-model" {
		t.Fatalf("DefaultModel = %q, want env-model", s.DefaultModel)
	}
	if s.DefaultMode != "plan" {
		t.Fatalf("DefaultMode = %q, want plan", s.DefaultMode)
	}
	if s.DefaultThinkingLevel != "high" {
		t.Fatalf("DefaultThinkingLevel = %q, want high", s.DefaultThinkingLevel)
	}
	if s.SessionDir != "./sessions" {
		t.Fatalf("SessionDir = %q, want ./sessions", s.SessionDir)
	}
	if s.GetProviderConfig("project-provider") == nil {
		t.Fatal("expected merged project provider")
	}
	if s.GetProviderConfig("deepseek-openai") == nil {
		t.Fatal("expected default provider to remain after project merge")
	}
	if s.ContextFiles.Enabled {
		t.Fatal("expected project contextFiles override to disable context files")
	}
	if len(s.ContextFiles.ExtraFiles) != 1 || s.ContextFiles.ExtraFiles[0] != "extra.md" {
		t.Fatalf("ExtraFiles = %#v, want extra.md", s.ContextFiles.ExtraFiles)
	}
	if len(s.Approval.BashWhitelist) != 1 || s.Approval.BashWhitelist[0] != "go test " {
		t.Fatalf("BashWhitelist = %#v, want go test", s.Approval.BashWhitelist)
	}
}

func TestDefaultSettingsConfirmBeforeWrite(t *testing.T) {
	s := DefaultSettings()
	if s.Approval.ConfirmBeforeWrite == nil || !*s.Approval.ConfirmBeforeWrite {
		t.Fatal("expected confirmBeforeWrite to be enabled by default")
	}
}

func TestDefaultSettingsEnablePlanTool(t *testing.T) {
	s := DefaultSettings()
	if s.EnablePlanTool == nil || !*s.EnablePlanTool {
		t.Fatal("expected enablePlanTool to be enabled by default")
	}
	if !s.IsPlanToolEnabled() {
		t.Fatal("expected IsPlanToolEnabled to return true by default")
	}
}

func TestResolveKey(t *testing.T) {
	s := &Settings{
		Providers: map[string]*ProviderConfig{
			"test": {
				APIKey: "test-api-key",
			},
		},
	}

	// Test direct key
	key := s.ResolveKey("test")
	if key != "test-api-key" {
		t.Errorf("expected 'test-api-key', got '%s'", key)
	}

	// Test env var
	os.Setenv("TEST_API_KEY", "env-key")
	s.Providers["env"] = &ProviderConfig{
		APIKey: "TEST_API_KEY",
	}
	key = s.ResolveKey("env")
	if key != "env-key" {
		t.Errorf("expected 'env-key', got '%s'", key)
	}
	os.Unsetenv("TEST_API_KEY")

	// Test missing key
	key = s.ResolveKey("nonexistent")
	if key != "" {
		t.Errorf("expected empty string, got '%s'", key)
	}
}

func TestResolveKeyFallsBackToBuiltinPresetEnvVar(t *testing.T) {
	// alibaba-standard's preset uses ${DASHSCOPE_API_KEY}, which does not match
	// the derived ALIBABA_STANDARD_API_KEY name.
	t.Setenv("DASHSCOPE_API_KEY", "preset-key")
	s := &Settings{}
	if got := s.ResolveKey("alibaba-standard"); got != "preset-key" {
		t.Fatalf("ResolveKey = %q, want preset-key", got)
	}
}

func TestResolveKeySkipsUnsetPresetPlaceholder(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "")
	t.Setenv("ALIBABA_STANDARD_API_KEY", "derived-key")
	s := &Settings{}
	if got := s.ResolveKey("alibaba-standard"); got != "derived-key" {
		t.Fatalf("ResolveKey = %q, want derived-key", got)
	}
}

func TestResolveKeySettingsEntryWinsOverPreset(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", "preset-key")
	s := &Settings{
		Providers: map[string]*ProviderConfig{
			"alibaba-standard": {APIKey: "settings-key"},
		},
	}
	if got := s.ResolveKey("alibaba-standard"); got != "settings-key" {
		t.Fatalf("ResolveKey = %q, want settings-key", got)
	}
}

func TestResolveProviderHeadersFallsBackToBuiltinPreset(t *testing.T) {
	s := &Settings{}
	headers := s.ResolveProviderHeaders("kimi-coding")
	if headers["User-Agent"] == "" {
		t.Fatalf("headers = %#v, want preset User-Agent", headers)
	}
}

func TestResolveProviderHeadersSettingsReplacePreset(t *testing.T) {
	s := &Settings{
		Providers: map[string]*ProviderConfig{
			"kimi-coding": {Headers: map[string]string{"X-Custom": "1"}},
		},
	}
	headers := s.ResolveProviderHeaders("kimi-coding")
	if headers["X-Custom"] != "1" {
		t.Fatalf("X-Custom = %q, want 1", headers["X-Custom"])
	}
	if _, ok := headers["User-Agent"]; ok {
		t.Fatalf("headers = %#v, want preset User-Agent replaced", headers)
	}
}

func TestResolveProviderHeaders(t *testing.T) {
	t.Setenv("CUSTOM_HEADER_VALUE", "env-header-value")
	s := &Settings{
		Providers: map[string]*ProviderConfig{
			"test": {
				Headers: map[string]string{
					"X-Static": "static-value",
					"X-Env":    "${CUSTOM_HEADER_VALUE}",
					" ":        "ignored",
				},
			},
		},
	}

	headers := s.ResolveProviderHeaders("test")
	if headers["X-Static"] != "static-value" {
		t.Fatalf("X-Static = %q, want static-value", headers["X-Static"])
	}
	if headers["X-Env"] != "env-header-value" {
		t.Fatalf("X-Env = %q, want env-header-value", headers["X-Env"])
	}
	if _, ok := headers[""]; ok {
		t.Fatal("expected empty header name to be ignored")
	}
	if got := s.ResolveProviderHeaders("missing"); got != nil {
		t.Fatalf("missing headers = %#v, want nil", got)
	}
}

func TestGetShell(t *testing.T) {
	s := &Settings{}

	// Test default
	shell := s.GetShell()
	if shell == "" {
		t.Error("expected non-empty shell")
	}

	// Test custom
	s.ShellPath = "/bin/zsh"
	shell = s.GetShell()
	if shell != "/bin/zsh" {
		t.Errorf("expected '/bin/zsh', got '%s'", shell)
	}
}

func TestGetSessionDir(t *testing.T) {
	s := &Settings{}

	// Test default
	dir := s.GetSessionDir()
	if dir == "" {
		t.Error("expected non-empty session dir")
	}

	// Test custom
	s.SessionDir = "/tmp/sessions"
	dir = s.GetSessionDir()
	if dir != "/tmp/sessions" {
		t.Errorf("expected '/tmp/sessions', got '%s'", dir)
	}

	// Test with tilde
	s.SessionDir = "~/sessions"
	dir = s.GetSessionDir()
	if dir == "" {
		t.Error("expected non-empty session dir")
	}
}

func TestGetGlobalSkillsDir(t *testing.T) {
	s := &Settings{}

	// Test default
	dir := s.GetGlobalSkillsDir()
	if dir == "" {
		t.Error("expected non-empty skills dir")
	}

	// Test custom
	s.SkillsDir = "/tmp/skills"
	dir = s.GetGlobalSkillsDir()
	if dir != "/tmp/skills" {
		t.Errorf("expected '/tmp/skills', got '%s'", dir)
	}
}

func TestDefaultSkillHubSettings(t *testing.T) {
	settings := DefaultSettings()
	if settings.SkillHub.DefaultMarket != "skillhub.cn" {
		t.Fatalf("default SkillHub market = %q", settings.SkillHub.DefaultMarket)
	}
	if settings.SkillHub.DefaultInstallScope != "project" {
		t.Fatalf("default SkillHub scope = %q", settings.SkillHub.DefaultInstallScope)
	}
	if len(settings.SkillHub.OfficialHandles) != 1 || settings.SkillHub.OfficialHandles[0] != DefaultSkillHubOfficialHandle {
		t.Fatalf("default SkillHub official handles = %#v", settings.SkillHub.OfficialHandles)
	}
}

func TestSaveGlobalSettings(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	os.Setenv("MOTHX_DIR", tmpDir)
	defer os.Unsetenv("MOTHX_DIR")

	s := DefaultSettings()
	s.DefaultProvider = "test"

	err := SaveGlobalSettings(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file was created
	settingsPath := filepath.Join(tmpDir, "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Error("expected settings file to exist")
	}

	// Load and verify
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	loaded := &Settings{}
	if err := json.Unmarshal(data, loaded); err != nil {
		t.Fatal(err)
	}

	if loaded.DefaultProvider != "test" {
		t.Errorf("expected provider 'test', got '%s'", loaded.DefaultProvider)
	}
}

func TestResolveKeyValue(t *testing.T) {
	// Test direct value
	key := resolveKeyValue("direct-key")
	if key != "direct-key" {
		t.Errorf("expected 'direct-key', got '%s'", key)
	}

	// Test env var
	os.Setenv("TEST_ENV_KEY", "env-value")
	key = resolveKeyValue("TEST_ENV_KEY")
	if key != "env-value" {
		t.Errorf("expected 'env-value', got '%s'", key)
	}
	os.Unsetenv("TEST_ENV_KEY")
}

func TestResolveKeyValueShellCommandRequiresOptIn(t *testing.T) {
	t.Setenv("VIBECODING_ALLOW_SHELL_CONFIG", "")
	if got := resolveKeyValue("!printf secret"); got != "!printf secret" {
		t.Fatalf("resolveKeyValue without opt-in = %q, want literal", got)
	}

	t.Setenv("VIBECODING_ALLOW_SHELL_CONFIG", "1")
	if got := resolveKeyValue("!printf secret"); got != "secret" {
		t.Fatalf("resolveKeyValue with opt-in = %q, want secret", got)
	}
}

func TestResolveModelConfigTracksUserSetMaxTokens(t *testing.T) {
	settings := DefaultSettings()
	settings.Providers["openai"] = &ProviderConfig{
		Models: []ModelConfig{{
			ID:        "gpt-4o",
			MaxTokens: 12345,
		}},
	}

	model := ResolveModelConfig("openai", "gpt-4o", settings)
	if model == nil {
		t.Fatal("ResolveModelConfig returned nil")
	}
	if !model.MaxTokensWasSet() {
		t.Fatal("MaxTokensWasSet = false, want true for runtime override")
	}
	if model.MaxTokens != 12345 {
		t.Fatalf("MaxTokens = %d, want 12345", model.MaxTokens)
	}

	builtin := DefaultModelConfig("openai", "gpt-4o")
	if builtin == nil {
		t.Fatal("DefaultModelConfig returned nil")
	}
	if builtin.MaxTokensWasSet() {
		t.Fatal("builtin MaxTokensWasSet = true, want false")
	}
}

func TestResolveModelConfigPreservesExplicitZeroMaxTokens(t *testing.T) {
	settings := DefaultSettings()
	if err := json.Unmarshal([]byte(`{"providers":{"openai":{"models":[{"id":"gpt-4o","maxTokens":0}]}}}`), settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	model := ResolveModelConfig("openai", "gpt-4o", settings)
	if model == nil {
		t.Fatal("ResolveModelConfig returned nil")
	}
	if !model.MaxTokensWasSet() || model.MaxTokens != 0 {
		t.Fatalf("maxTokens = %d, explicitly set = %v; want 0, true", model.MaxTokens, model.MaxTokensWasSet())
	}

	data, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal model JSON: %v", err)
	}
	if got := string(raw["maxTokens"]); got != "0" {
		t.Fatalf("maxTokens JSON = %q, want 0", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestNormalizeSamplingPtr(t *testing.T) {
	// nil in → nil out
	if NormalizeSamplingPtr(nil) != nil {
		t.Error("nil should return nil")
	}

	// zero in → nil out
	zero := 0.0
	if NormalizeSamplingPtr(&zero) != nil {
		t.Error("zero should return nil")
	}

	// positive in → clone out
	v := 0.7
	result := NormalizeSamplingPtr(&v)
	if result == nil || *result != 0.7 {
		t.Fatalf("0.7 should return clone pointing to 0.7, got %#v", result)
	}
	// Verify it's a clone, not the original
	v = 0.5
	if *result != 0.7 {
		t.Error("clone should be independent of original")
	}

	// negative zero? Go doesn't have -0.0 for float64 like IEEE, but let's test
	negZero := -0.0
	if NormalizeSamplingPtr(&negZero) != nil {
		t.Error("-0.0 should also return nil")
	}
}

func TestTUILangDefaultsAndOverrides(t *testing.T) {
	if got := DefaultSettings().TUILang; got != "auto" {
		t.Fatalf("default tuilang = %q, want auto", got)
	}

	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	t.Setenv("MOTHX_DIR", filepath.Join(tmpDir, "global"))

	if err := SaveGlobalSettingsPatch(map[string]any{"tuilang": "en", "theme": "dark"}); err != nil {
		t.Fatalf("save global patch: %v", err)
	}
	if err := os.MkdirAll(ProjectDirName, 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveProjectSettingsPatch(map[string]any{"tuilang": "zh"}); err != nil {
		t.Fatalf("save project patch: %v", err)
	}
	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if settings.TUILang != "zh" {
		t.Fatalf("effective tuilang = %q, want project override zh", settings.TUILang)
	}
	data, err := os.ReadFile(ProjectSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw["tuilang"] == nil {
		t.Fatalf("project sparse patch expanded unexpected keys: %s", data)
	}
}

func TestToolExecutionSettingsDefaultsAndSparsePatch(t *testing.T) {
	data, err := json.Marshal(Settings{})
	if err != nil {
		t.Fatalf("marshal zero settings: %v", err)
	}
	var sparse map[string]json.RawMessage
	if err := json.Unmarshal(data, &sparse); err != nil {
		t.Fatalf("decode marshaled zero settings: %v", err)
	}
	if _, ok := sparse["toolExecution"]; ok {
		t.Fatalf("zero settings unexpectedly serialized toolExecution: %s", data)
	}

	defaults := DefaultSettings()
	if defaults.ToolExecution.EffectiveMode() != "parallel" {
		t.Fatalf("default tool execution mode = %q, want parallel", defaults.ToolExecution.EffectiveMode())
	}
	if got := defaults.ToolExecution.EffectiveMaxConcurrency(); got != DefaultToolExecutionMaxConcurrency {
		t.Fatalf("default tool concurrency = %d, want %d", got, DefaultToolExecutionMaxConcurrency)
	}

	var decoded Settings
	if err := json.Unmarshal([]byte(`{"toolExecution":{"mode":"SEQUENTIAL","maxConcurrency":0}}`), &decoded); err != nil {
		t.Fatalf("decode tool execution settings: %v", err)
	}
	if decoded.ToolExecution.EffectiveMode() != "sequential" {
		t.Fatalf("decoded mode = %q, want sequential", decoded.ToolExecution.EffectiveMode())
	}
	if got := decoded.ToolExecution.EffectiveMaxConcurrency(); got != DefaultToolExecutionMaxConcurrency {
		t.Fatalf("zero concurrency = %d, want %d", got, DefaultToolExecutionMaxConcurrency)
	}

	t.Setenv("MOTHX_DIR", filepath.Join(t.TempDir(), "global"))
	if err := SaveGlobalSettingsPatch(map[string]any{
		"theme":         "dark",
		"toolExecution": map[string]any{"mode": "parallel", "maxConcurrency": 4},
	}); err != nil {
		t.Fatalf("save tool execution patch: %v", err)
	}
	settings, err := LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatalf("load patched settings: %v", err)
	}
	if settings.ToolExecution.EffectiveMode() != "parallel" || settings.ToolExecution.EffectiveMaxConcurrency() != 4 {
		t.Fatalf("patched tool execution = %#v", settings.ToolExecution)
	}
	if settings.Theme != "dark" {
		t.Fatalf("unrelated patched setting lost: theme=%q", settings.Theme)
	}
}

func TestResolveModelConfigPreservesCompatibilityFlags(t *testing.T) {
	parallel := false
	toolChoice := false
	settings := &Settings{Providers: map[string]*ProviderConfig{
		"custom": {
			Models: []ModelConfig{{
				ID: "model",
				Compat: &ModelCompat{
					SupportsParallelToolCalls: &parallel,
					SupportsToolChoice:        &toolChoice,
					SupportsHostedTools:       map[string]bool{"web_search": true},
					SupportedInclude:          []string{"reasoning.encrypted_content"},
				},
			}},
		},
	}}

	model := ResolveModelConfig("custom", "model", settings)
	if model == nil || model.Compat == nil {
		t.Fatal("resolved model compatibility is nil")
	}
	if model.Compat.SupportsParallelToolCalls == nil || *model.Compat.SupportsParallelToolCalls {
		t.Fatalf("supportsParallelToolCalls = %#v, want false", model.Compat.SupportsParallelToolCalls)
	}
	if model.Compat.SupportsToolChoice == nil || *model.Compat.SupportsToolChoice {
		t.Fatalf("supportsToolChoice = %#v, want false", model.Compat.SupportsToolChoice)
	}
	if !model.Compat.SupportsHostedTools["web_search"] || len(model.Compat.SupportedInclude) != 1 {
		t.Fatalf("resolved compatibility collections = %#v/%#v", model.Compat.SupportsHostedTools, model.Compat.SupportedInclude)
	}
	*model.Compat.SupportsParallelToolCalls = true
	*model.Compat.SupportsToolChoice = true
	model.Compat.SupportsHostedTools["file_search"] = true
	model.Compat.SupportedInclude[0] = "changed"
	if base := settings.Providers["custom"].Models[0].Compat; *base.SupportsParallelToolCalls || *base.SupportsToolChoice || base.SupportsHostedTools["file_search"] || base.SupportedInclude[0] != "reasoning.encrypted_content" {
		t.Fatal("resolved compatibility aliases original settings")
	}
}

func TestIsProjectDir(t *testing.T) {
	plain := t.TempDir()
	if IsProjectDir(plain) {
		t.Fatal("plain temporary directory should not be recognized as project")
	}
	if err := os.WriteFile(filepath.Join(plain, "go.mod"), []byte("module example.com/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !IsProjectDir(plain) {
		t.Fatal("directory containing go.mod should be recognized as project")
	}
}

// TestToolControlSettingsContract pins the settings.json paths that both the TUI
// /settings Responses form and the WebUI provider editor write, so a schema
// rename cannot silently desynchronize the two editors from persisted settings.
func TestToolControlSettingsContract(t *testing.T) {
	parallel := true
	choiceUnsupported := false
	parallelSupported := true
	s := &Settings{
		Providers: map[string]*ProviderConfig{
			"custom": {
				API: "openai-responses",
				Responses: ResponsesConfig{ToolControl: ResponsesToolControlConfig{
					Choice:   "required",
					Parallel: &parallel,
					MaxCalls: 3,
				}},
				Models: []ModelConfig{{ID: "m1", Compat: &ModelCompat{
					SupportsToolChoice:        &choiceUnsupported,
					SupportsParallelToolCalls: &parallelSupported,
				}}},
			},
		},
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	for _, want := range []string{
		`"toolControl":{"choice":"required","parallel":true,"maxCalls":3}`,
		`"supportsToolChoice":false`,
		`"supportsParallelToolCalls":true`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("serialized settings missing %s in %s", want, data)
		}
	}

	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	pc := loaded.Providers["custom"]
	if pc == nil {
		t.Fatal("provider custom missing after round trip")
	}
	if got := pc.Responses.ToolControl; got.Choice != "required" || got.Parallel == nil || !*got.Parallel || got.MaxCalls != 3 {
		t.Fatalf("responses.toolControl lost in round trip: %#v", got)
	}
	compat := pc.Models[0].Compat
	if compat == nil || compat.SupportsToolChoice == nil || *compat.SupportsToolChoice ||
		compat.SupportsParallelToolCalls == nil || !*compat.SupportsParallelToolCalls {
		t.Fatalf("compat tool flags lost in round trip: %#v", compat)
	}
}
