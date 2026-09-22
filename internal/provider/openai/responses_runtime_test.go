package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestResponsesChatFallsBackToSynchronousWhenBackgroundCoordinatorIsUnavailable(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-test"}}, "data: [DONE]\n", bodyCh, nil)
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	for range p.Chat(context.Background(), provider.ChatParams{
		ModelID:  "responses-test",
		Messages: []provider.Message{provider.NewUserMessage("stay available")},
		Abort:    make(chan struct{}),
	}) {
	}
	var body map[string]any
	select {
	case raw := <-bodyCh:
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
	default:
		t.Fatal("no request body captured")
	}
	if body["background"] != nil {
		t.Fatalf("background = %#v, want omitted for synchronous fallback", body["background"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %#v, want true", body["stream"])
	}
}

func TestResponsesRequestDiagnosticsDescribeLocalFieldOmissions(t *testing.T) {
	temperature := 0.4
	opts := &provider.ResponseOptions{SuppressConversation: true}
	diagnostics := responsesRequestDiagnostics(provider.ChatParams{
		Temperature:     &temperature,
		TopP:            &temperature,
		ResponseOptions: opts,
	}, responsesRequest{Reasoning: &responsesReasoning{}})
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if diagnostics[0]["field"] != "temperature/top_p" || diagnostics[0]["reason"] != "reasoning_incompatible" {
		t.Fatalf("sampling diagnostic = %#v", diagnostics[0])
	}
	if diagnostics[1]["field"] != "conversation" || diagnostics[1]["reason"] != "remote_state_replay_fallback" {
		t.Fatalf("conversation diagnostic = %#v", diagnostics[1])
	}
}

func TestResponsesStreamEmitsHostedItemLifecycleEvents(t *testing.T) {
	bodyCh := make(chan string, 1)
	p := newMockOpenAIProvider(t, []*provider.Model{{ID: "responses-hosted"}}, string(readResponsesFixture(t, "hosted_lifecycle.sse")), bodyCh, nil)
	p.SetUseResponsesAPI(true)
	var events []provider.StreamEvent
	for event := range p.Chat(context.Background(), provider.ChatParams{ModelID: "responses-hosted", Messages: []provider.Message{provider.NewUserMessage("search")}}) {
		events = append(events, event)
	}
	var lifecycle []provider.HostedItem
	for _, event := range events {
		if event.Type == provider.StreamHostedItem && event.HostedItem != nil {
			lifecycle = append(lifecycle, *event.HostedItem)
		}
	}
	if len(lifecycle) != 2 || lifecycle[0].Status != "in_progress" || lifecycle[1].Status != "completed" || lifecycle[1].OutputIndex != 2 {
		t.Fatalf("hosted lifecycle = %#v (events=%#v)", lifecycle, events)
	}
}

func TestResponsesRunManagerStartGetAndCancel(t *testing.T) {
	postCount := 0
	cancelReceived := false

	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := NewProviderWithModels("test-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var response string
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			postCount++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode background request: %v", err)
			}
			if body["background"] != true {
				t.Fatalf("background request = %#v, want true", body)
			}
			if body["stream"] != false {
				t.Fatalf("background request stream = %#v, want false", body["stream"])
			}
			response = `{"id":"resp-1","status":"queued"}`
		case r.Method == http.MethodGet && r.URL.Path == "/v1/responses/resp-1":
			response = `{"id":"resp-1","status":"completed"}`
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses/resp-1/cancel":
			cancelReceived = true
			response = `{"id":"resp-1","status":"cancelling"}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(response)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	runs := p.NewResponsesRunManager(sessionDir)

	run, err := runs.Start(context.Background(), manager.GetHeader().ID, "turn-1", provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("run in background")},
		Abort:    make(chan struct{}),
	})
	if err != nil {
		t.Fatalf("start background run: %v", err)
	}
	if run == nil || run.ResponseID != "resp-1" || run.State != "queued" {
		t.Fatalf("started run = %#v", run)
	}
	got, err := runs.Get(context.Background(), manager.GetHeader().ID, run.LocalRunID)
	if err != nil {
		t.Fatalf("get background run: %v", err)
	}
	if got == nil || got.State != "completed" {
		t.Fatalf("queried run = %#v", got)
	}

	run, err = runs.Start(context.Background(), manager.GetHeader().ID, "turn-2", provider.ChatParams{
		ModelID:  "mock",
		Messages: []provider.Message{provider.NewUserMessage("cancel me")},
		Abort:    make(chan struct{}),
	})
	if err != nil {
		t.Fatalf("start second background run: %v", err)
	}
	if err := runs.Cancel(context.Background(), manager.GetHeader().ID, run.LocalRunID); err != nil {
		t.Fatalf("cancel background run: %v", err)
	}
	cancelled, err := session.GetResponseRun(sessionDir, manager.GetHeader().ID, run.LocalRunID)
	if err != nil {
		t.Fatalf("read cancelled run: %v", err)
	}
	if cancelled == nil || cancelled.State != "cancelling" || !cancelled.CancelRequested {
		t.Fatalf("cancelled run = %#v", cancelled)
	}
	if postCount != 2 || !cancelReceived {
		t.Fatalf("postCount=%d cancelReceived=%v", postCount, cancelReceived)
	}
}

func TestResponsesRunManagerStartUsesConfiguredRetryAndIdempotency(t *testing.T) {
	for _, tc := range []struct {
		name         string
		retryEnabled bool
		wantAttempts int
		wantError    bool
	}{
		{name: "enabled", retryEnabled: true, wantAttempts: 2},
		{name: "disabled", wantAttempts: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			manager := session.New(t.TempDir(), sessionDir)
			if err := manager.Init(); err != nil {
				t.Fatalf("init session: %v", err)
			}

			attempts := 0
			var idempotencyKey string
			p := NewProviderWithModels("test-key", "https://api.test/v1", []*provider.Model{{ID: "mock"}})
			p.SetHeaders(map[string]string{"Idempotency-Key": "configured-value-must-not-win"})
			p.SetRetryConfig(&provider.RetryConfig{Enabled: tc.retryEnabled, MaxRetries: 1, BaseDelayMs: 1})
			p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				gotKey := r.Header.Get("Idempotency-Key")
				if gotKey == "" {
					t.Fatal("background start request is missing Idempotency-Key")
				}
				if gotKey == "configured-value-must-not-win" {
					t.Fatal("background start request used configured header instead of stable run id")
				}
				if idempotencyKey == "" {
					idempotencyKey = gotKey
				} else if gotKey != idempotencyKey {
					t.Fatalf("Idempotency-Key changed across retries: %q != %q", gotKey, idempotencyKey)
				}
				status := http.StatusServiceUnavailable
				body := `{"error":"temporarily unavailable"}`
				if attempts > 1 {
					status = http.StatusOK
					body = `{"id":"resp-retried","status":"queued"}`
				}
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    r,
				}, nil
			})}

			run, err := p.NewResponsesRunManager(sessionDir).Start(context.Background(), manager.GetHeader().ID, "turn-retry", provider.ChatParams{
				ModelID: "mock", Messages: []provider.Message{provider.NewUserMessage("retry in background")}, Abort: make(chan struct{}),
			})
			if tc.wantError {
				if err == nil {
					t.Fatalf("Start() error = nil, run = %#v", run)
				}
			} else if err != nil || run == nil || run.ResponseID != "resp-retried" {
				t.Fatalf("Start() run = %#v, error = %v", run, err)
			}
			if attempts != tc.wantAttempts {
				t.Fatalf("attempts = %d, want %d", attempts, tc.wantAttempts)
			}
		})
	}
}

func TestArchiveBackgroundResponsePreservesUsageAndAttachments(t *testing.T) {
	sessionDir := t.TempDir()
	response := &responsesCompletedObject{
		ID:     "resp-archive",
		Status: "completed",
		Usage:  &responsesUsage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18},
		Output: []json.RawMessage{
			json.RawMessage(`{"id":"msg-archive","type":"message","content":[{"type":"output_text","annotations":[{"type":"url_citation","title":"OpenAI","url":"https://openai.com"}]}]}`),
		},
	}
	run := session.ResponseRun{SessionID: "session-archive", LocalRunID: "run-archive", LocalTurnID: "turn-archive", Provider: "openai", API: "openai-responses"}
	if err := archiveBackgroundResponse(sessionDir, run, response); err != nil {
		t.Fatalf("archive background response: %v", err)
	}
	turn, err := session.GetResponseTurn(sessionDir, run.SessionID, run.LocalTurnID)
	if err != nil || turn == nil {
		t.Fatalf("get archived response turn: turn=%#v err=%v", turn, err)
	}
	var summary struct {
		Usage       *provider.Usage       `json:"usage"`
		Attachments []provider.Attachment `json:"attachments"`
	}
	if err := json.Unmarshal(turn.ResponseSummary, &summary); err != nil {
		t.Fatalf("decode response summary: %v", err)
	}
	if summary.Usage == nil || summary.Usage.TotalTokens != 18 {
		t.Fatalf("summary usage = %#v", summary.Usage)
	}
	if len(summary.Attachments) != 1 || summary.Attachments[0].Kind != "citation" || summary.Attachments[0].URL != "https://openai.com" {
		t.Fatalf("summary attachments = %#v", summary.Attachments)
	}
}
