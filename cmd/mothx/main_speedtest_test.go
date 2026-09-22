package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

func TestCollectSpeedtestTargetsIncludesConfiguredTextBenchmarkModels(t *testing.T) {
	settings := &config.Settings{
		DefaultProvider: "custom-empty",
		DefaultModel:    "fallback-model",
		Providers: map[string]*config.ProviderConfig{
			"configured": {
				APIKey: "sk-test",
				Models: []config.ModelConfig{
					{ID: "text-model", Input: []string{"text"}},
					{ID: "multimodal-model", Input: []string{"text", "image"}},
				},
			},
			"custom-empty": {
				APIKey: "sk-test",
			},
			"placeholder": {
				APIKey: "${MISSING_API_KEY}",
				Models: []config.ModelConfig{{ID: "skipped"}},
			},
		},
	}

	targets := collectSpeedtestTargets(settings, &speedtestFlags{})
	got := make([]string, 0, len(targets))
	for _, target := range targets {
		got = append(got, target.Provider+"/"+target.ModelID)
	}
	want := []string{
		"configured/multimodal-model",
		"configured/text-model",
		"custom-empty/fallback-model",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
}

func TestCollectSpeedtestTargetsAppliesProviderAndModelFilters(t *testing.T) {
	settings := &config.Settings{
		Providers: map[string]*config.ProviderConfig{
			"p1": {APIKey: "sk-test", Models: []config.ModelConfig{{ID: "m1"}, {ID: "m2"}}},
			"p2": {APIKey: "sk-test", Models: []config.ModelConfig{{ID: "m2"}}},
		},
	}

	targets := collectSpeedtestTargets(settings, &speedtestFlags{provider: "p1", model: "m2"})
	if len(targets) != 1 {
		t.Fatalf("len(targets) = %d, want 1", len(targets))
	}
	if targets[0].Provider != "p1" || targets[0].ModelID != "m2" {
		t.Fatalf("target = %#v, want p1/m2", targets[0])
	}
}

func TestSortSpeedtestResultsOrdersSuccessesByTokensPerSecond(t *testing.T) {
	results := []speedtestResult{
		{Target: speedtestTarget{Provider: "p1", ModelID: "slow"}, TokensPerSecond: 12},
		{Target: speedtestTarget{Provider: "p1", ModelID: "failed"}, Error: errors.New("boom")},
		{Target: speedtestTarget{Provider: "p2", ModelID: "fast"}, TokensPerSecond: 42},
	}

	sortSpeedtestResults(results)
	got := []string{
		results[0].Target.Provider + "/" + results[0].Target.ModelID,
		results[1].Target.Provider + "/" + results[1].Target.ModelID,
		results[2].Target.Provider + "/" + results[2].Target.ModelID,
	}
	want := []string{"p2/fast", "p1/slow", "p1/failed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
}

func TestAverageSpeedtestResultsAveragesSuccessfulRuns(t *testing.T) {
	results := averageSpeedtestResults([]speedtestResult{
		{Target: speedtestTarget{Provider: "p", ModelID: "m"}, TokensPerSecond: 10, NetworkLatency: 10 * time.Millisecond, FirstTokenLatency: 100 * time.Millisecond, TotalDuration: time.Second, OutputTokens: 100},
		{Target: speedtestTarget{Provider: "p", ModelID: "m"}, TokensPerSecond: 20, NetworkLatency: 20 * time.Millisecond, FirstTokenLatency: 200 * time.Millisecond, TotalDuration: 2 * time.Second, OutputTokens: 200},
		{Target: speedtestTarget{Provider: "p", ModelID: "m"}, Error: errors.New("temporary failure")},
	})
	if results.Error != nil {
		t.Fatalf("result error = %v", results.Error)
	}
	if results.TokensPerSecond != 15 || results.NetworkLatency != 15*time.Millisecond || results.OutputTokens != 150 {
		t.Fatalf("averaged result = %#v", results)
	}
}
func TestRunSpeedtestRequestMeasuresFirstTokenAndUsage(t *testing.T) {
	model := &provider.Model{ID: "model-a", Name: "Model A", MaxTokens: 32}
	p := &speedtestFakeProvider{
		models: []*provider.Model{model},
		events: []speedtestFakeEvent{
			{event: provider.StreamEvent{Type: provider.StreamStart}},
			{delay: time.Millisecond, event: provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "hello "}},
			{delay: time.Millisecond, event: provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "world"}},
			{event: provider.StreamEvent{Type: provider.StreamUsage, Usage: &provider.Usage{Output: 4}}},
			{event: provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}},
		},
	}

	result := runSpeedtestRequest(context.Background(), p, model, speedtestTarget{Provider: "fake", ModelID: "model-a"}, speedtestRequestOptions{
		Prompt:        "test prompt",
		MaxTokens:     64,
		ThinkingLevel: provider.ThinkingOff,
	})

	if result.Error != nil {
		t.Fatalf("result error = %v", result.Error)
	}
	if result.OutputTokens != 4 || result.EstimatedTokens {
		t.Fatalf("tokens = %d estimated=%v, want 4 false", result.OutputTokens, result.EstimatedTokens)
	}
	if result.FirstTokenLatency <= 0 {
		t.Fatalf("first token latency = %v, want > 0", result.FirstTokenLatency)
	}
	if result.TokensPerSecond <= 0 {
		t.Fatalf("tokens/sec = %v, want > 0", result.TokensPerSecond)
	}
	if p.params.ModelID != "model-a" {
		t.Fatalf("ModelID = %q, want model-a", p.params.ModelID)
	}
	if p.params.MaxTokens != 32 {
		t.Fatalf("MaxTokens = %d, want capped model max 32", p.params.MaxTokens)
	}
	if len(p.params.Messages) != 1 || p.params.Messages[0].Content != "test prompt" {
		t.Fatalf("messages = %#v, want prompt message", p.params.Messages)
	}
}

type speedtestFakeEvent struct {
	delay time.Duration
	event provider.StreamEvent
}

type speedtestFakeProvider struct {
	models []*provider.Model
	events []speedtestFakeEvent
	params provider.ChatParams
}

func (p *speedtestFakeProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.params = params
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		for _, item := range p.events {
			if item.delay > 0 {
				select {
				case <-ctx.Done():
					ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
					return
				case <-time.After(item.delay):
				}
			}
			select {
			case <-ctx.Done():
				ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
				return
			case ch <- item.event:
			}
		}
	}()
	return ch
}

func (p *speedtestFakeProvider) Name() string {
	return "fake"
}

func (p *speedtestFakeProvider) API() string {
	return "mock"
}

func (p *speedtestFakeProvider) Models() []*provider.Model {
	return p.models
}

func (p *speedtestFakeProvider) GetModel(id string) *provider.Model {
	for _, model := range p.models {
		if model.ID == id {
			return model
		}
	}
	return nil
}
