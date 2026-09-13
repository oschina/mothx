package openaiapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	openaiprovider "github.com/startvibecoding/mothx/internal/provider/openai"
	serviceruntime "github.com/startvibecoding/mothx/internal/serve/runtime"
)

func TestSubmitExternalResponsesBackgroundUsesDurableCoordinator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			_, _ = w.Write([]byte(`{"id":"external-response","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/responses/external-response":
			_, _ = w.Write([]byte(`{"id":"external-response","status":"completed","output":[{"id":"external-message","type":"message","content":[{"type":"output_text","text":"external completion","annotations":[{"type":"url_citation","title":"OpenAI","url":"https://openai.com"}]}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	srv := newTestServer(t)
	defer srv.pool.Stop()
	model := &provider.Model{ID: "m1", Name: "Model 1", ContextWindow: 32768, MaxTokens: 2048}
	p := openaiprovider.NewProviderWithModels("test-key", upstream.URL+"/v1", []*provider.Model{model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatalf("set Responses config: %v", err)
	}
	srv.provider = p
	srv.model = model
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())

	sess, err := srv.getOrCreateSession("external-channel", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	completed := make(chan string, 1)
	runID, err := srv.SubmitExternalResponsesBackground(serviceruntime.BackgroundRequest{
		SessionID: sess.ID, WorkDir: sess.WorkDir, Platform: "wechat", Input: agentruntime.RunInput{Text: "run externally"},
		Progress: func(text string) { completed <- text },
	})
	if err != nil || runID == "" {
		t.Fatalf("submit external background: runID=%q err=%v", runID, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		run, getErr := agentruntime.GetDurableRun(t.Context(), srv.settings.GetSessionDir(), runID)
		if getErr != nil {
			t.Fatalf("get session run: %v", getErr)
		}
		if run != nil && run.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("external background run did not complete: %#v", run)
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case text := <-completed:
		if text != "external completion\n\nAttachments:\n- OpenAI: https://openai.com" {
			t.Fatalf("completion text = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("external completion callback was not invoked")
	}
	messages := sess.Manager.GetMessages()
	if len(messages) != 2 || messages[1].Role != "assistant" || messageText(messages[1]) != "external completion" {
		encoded, _ := json.Marshal(messages)
		t.Fatalf("session messages = %s", encoded)
	}
}

func TestChatCompletionsXBackgroundUsesDurableCoordinator(t *testing.T) {
	requestSeen := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode Responses request: %v", err)
			}
			requestSeen <- body
			_, _ = w.Write([]byte(`{"id":"chat-background-response","status":"queued"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/responses/chat-background-response":
			_, _ = w.Write([]byte(`{"id":"chat-background-response","status":"completed","output":[{"id":"chat-background-message","type":"message","content":[{"type":"output_text","text":"chat background complete"}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	srv := newTestServer(t)
	defer srv.pool.Stop()
	model := srv.model
	model.Compat = &provider.ModelCompat{DisableSamplingParams: config.BoolPtr(false)}
	p := openaiprovider.NewProviderWithModels("test-key", upstream.URL+"/v1", []*provider.Model{model})
	p.SetUseResponsesAPI(true)
	if err := p.SetResponsesConfig(config.ResponsesConfig{Background: config.BoolPtr(true)}); err != nil {
		t.Fatalf("set responses config: %v", err)
	}
	srv.provider = p
	srv.model = model
	srv.responsesRuns = p.NewResponsesRunManager(srv.settings.GetSessionDir())
	srv.runManager = NewRunManager(srv.settings.GetSessionDir())

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"system","content":"answer tersely"},{"role":"user","content":"hello"}],"temperature":0.2,"top_p":0.8,"max_tokens":123,"x_background":true}`))
	req.Header.Set("Idempotency-Key", "chat-background-key")
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("chat background status = %d, body = %s", w.Code, w.Body.String())
	}
	var accepted struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.RunID == "" {
		t.Fatalf("accepted response = %s, err=%v", w.Body.String(), err)
	}
	select {
	case body := <-requestSeen:
		if body["background"] != true || body["stream"] != false || body["max_output_tokens"] != float64(123) {
			t.Fatalf("Responses request controls = %#v", body)
		}
		if body["temperature"] != float64(0.2) || body["top_p"] != float64(0.8) {
			t.Fatalf("sampling controls = %#v", body)
		}
		if !strings.Contains(body["instructions"].(string), "answer tersely") {
			t.Fatalf("instructions = %#v", body["instructions"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background Responses request was not submitted")
	}
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m1","messages":[{"role":"user","content":"hello"}],"x_background":true}`))
	secondReq.Header.Set("Idempotency-Key", "chat-background-key")
	second := httptest.NewRecorder()
	srv.handleChatCompletions(second, secondReq)
	if second.Code != http.StatusAccepted || !strings.Contains(second.Body.String(), accepted.RunID) {
		t.Fatalf("idempotent chat background response = %d %s", second.Code, second.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := agentruntime.GetDurableRun(t.Context(), srv.settings.GetSessionDir(), accepted.RunID)
		if err != nil {
			t.Fatalf("get session run: %v", err)
		}
		if run != nil && run.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat background run did not complete: %#v", run)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
