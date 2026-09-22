package title

import (
	"context"
	"errors"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

type recordingProvider struct {
	params provider.ChatParams
	events []provider.StreamEvent
}

func (p *recordingProvider) Chat(_ context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.params = params
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, event := range p.events {
		ch <- event
	}
	close(ch)
	return ch
}
func (p *recordingProvider) Name() string                       { return "test" }
func (p *recordingProvider) API() string                        { return "test-api" }
func (p *recordingProvider) Models() []*provider.Model          { return []*provider.Model{{ID: "model"}} }
func (p *recordingProvider) GetModel(id string) *provider.Model { return &provider.Model{ID: id} }

func TestGeneratorUsesCommonProviderInterfaceAndNormalizes(t *testing.T) {
	p := &recordingProvider{events: []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "  \"修复登录问题\n"},
		{Type: provider.StreamTextDelta, TextDelta: "并补充测试\"  "},
	}}
	name, err := (Generator{Provider: p, Model: &provider.Model{ID: "model"}}).Generate(context.Background(), []provider.Message{provider.NewUserMessage("请修复登录")})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if name != "修复登录问题 并补充测试" {
		t.Fatalf("name = %q", name)
	}
	if p.params.ModelID != "model" || len(p.params.Messages) != 2 {
		t.Fatalf("params = %#v", p.params)
	}
}

func TestGeneratorFallsBackWhenProviderFails(t *testing.T) {
	p := &recordingProvider{events: []provider.StreamEvent{{Type: provider.StreamError, Error: errors.New("provider down")}}}
	name, err := (Generator{Provider: p, Model: &provider.Model{ID: "model"}}).Generate(context.Background(), []provider.Message{provider.NewUserMessage("hi")})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if name != "hi" {
		t.Fatalf("name = %q, want fallback title", name)
	}
}

func TestNormalizeLimitsUnicodeTitle(t *testing.T) {
	got := Normalize("###" + "界" + "界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界界")
	if len([]rune(got)) != maxTitleRunes {
		t.Fatalf("rune length = %d, want %d", len([]rune(got)), maxTitleRunes)
	}
}
