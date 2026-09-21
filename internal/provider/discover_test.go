package provider

import (
	"testing"
)

func TestModelsEndpoint(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://api.example.test/v1", "https://api.example.test/v1/models"},
		{"https://api.example.test/v1/", "https://api.example.test/v1/models"},
		{"https://api.example.test/v1/models", "https://api.example.test/v1/models"},
	}
	for _, tt := range tests {
		got, err := ModelsEndpoint(tt.base)
		if err != nil {
			t.Errorf("ModelsEndpoint(%q): %v", tt.base, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ModelsEndpoint(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
	for _, base := range []string{"", "ftp://example.test/v1", "/relative"} {
		if _, err := ModelsEndpoint(base); err == nil {
			t.Errorf("ModelsEndpoint(%q) succeeded, want error", base)
		}
	}
}

func TestParseDiscoveredModels(t *testing.T) {
	got, err := ParseDiscoveredModels([]byte(`{"models":[{"name":"models/gemini-2.0-flash","displayName":"Gemini 2.0 Flash"},{"name":"models/gemini-2.0-flash"},{"id":"gpt-4o","context_length":128000,"max_output_tokens":4096,"input_modalities":["text","image"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	if got[0].ID != "gemini-2.0-flash" || got[0].Name != "Gemini 2.0 Flash" {
		t.Fatalf("unexpected Gemini model: %#v", got[0])
	}
	if got[0].ContextWindow != 1048576 || got[0].Reasoning || len(got[0].Input) != 2 || got[0].Input[1] != "image" {
		t.Fatalf("Gemini preset not applied: %#v", got[0])
	}
	if got[1].ID != "gpt-4o" || got[1].ContextWindow != 128000 || got[1].MaxTokens != 4096 || len(got[1].Input) != 2 {
		t.Fatalf("unexpected OpenAI model: %#v", got[1])
	}
}

func TestParseDiscoveredModelsBareArray(t *testing.T) {
	got, err := ParseDiscoveredModels([]byte(`[{"id":"a"},{"id":"b","reasoning":true},{"id":"a"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2 (deduplicated)", len(got))
	}
	if got[0].ID != "a" || !got[0].Reasoning || got[0].ContextWindow != 256000 {
		t.Fatalf("unexpected first model: %#v", got[0])
	}
	if got[1].ID != "b" || !got[1].Reasoning {
		t.Fatalf("unexpected second model: %#v", got[1])
	}
	if len(got[0].Input) != 1 || got[0].Input[0] != "text" {
		t.Fatalf("default input = %#v, want [text]", got[0].Input)
	}
}

func TestParseDiscoveredModelsExplicitMetadataOverridesPreset(t *testing.T) {
	got, err := ParseDiscoveredModels([]byte(`[{"id":"gpt-4o","contextWindow":42,"maxTokens":7,"input":["text"],"reasoning":false}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	model := got[0]
	if model.ContextWindow != 42 || model.MaxTokens != 7 || model.Reasoning || len(model.Input) != 1 || model.Input[0] != "text" {
		t.Fatalf("explicit discovery metadata was not preserved: %#v", model)
	}
}

func TestResolveSecretRef(t *testing.T) {
	t.Setenv("MOTHX_DISCOVER_TEST_KEY", "from-env")
	if got := ResolveSecretRef("${MOTHX_DISCOVER_TEST_KEY}"); got != "from-env" {
		t.Fatalf("ResolveSecretRef env reference = %q", got)
	}
	if got := ResolveSecretRef(" literal "); got != "literal" {
		t.Fatalf("ResolveSecretRef literal = %q", got)
	}
}
