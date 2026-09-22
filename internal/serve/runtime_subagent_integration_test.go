package serve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/messaging"
	channels "github.com/oschina/mothx/internal/serve/channels"
	openaiapi "github.com/oschina/mothx/internal/serve/openaiapi"
)

func TestRunWiresSubAgentObserverThroughRealServeRuntime(t *testing.T) {
	configDir := t.TempDir()
	sessionDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	t.Setenv("MOTHX_CONFIG_DIR", configDir)
	if got := config.ConfigDir(); got != configDir {
		t.Fatalf("config dir = %q, want isolated temp dir %q", got, configDir)
	}

	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		var payload string
		switch call {
		case 1:
			payload = `{"id":"parent","choices":[{"delta":{"tool_calls":[{"index":0,"id":"spawn-1","type":"function","function":{"name":"subagent_spawn","arguments":"{\"task\":\"inspect\"}"}}]},"finish_reason":null}]}\n` +
				`{"id":"parent","choices":[{"delta":{},"finish_reason":"tool_calls"}]}\n`
		case 2:
			payload = `{"id":"child","choices":[{"delta":{"content":"child result"},"finish_reason":null}]}\n` +
				`{"id":"child","choices":[{"delta":{},"finish_reason":"stop"}]}\n`
		default:
			payload = `{"id":"parent-final","choices":[{"delta":{"content":"parent result"},"finish_reason":null}]}\n` +
				`{"id":"parent-final","choices":[{"delta":{},"finish_reason":"stop"}]}\n`
		}
		for _, line := range strings.Split(payload, "\\n") {
			if line != "" {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer provider.Close()

	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	settings.Providers["custom-test"] = &config.ProviderConfig{
		BaseURL: provider.URL,
		API:     "openai-chat",
		APIKey:  "test-key",
		Models:  []config.ModelConfig{{ID: "test-model"}},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	cfg := DefaultConfig()
	cfg.API.Listen = "127.0.0.1:0"
	cfg.API.Provider = "custom-test"
	cfg.API.Model = "test-model"
	cfg.API.DefaultWorkDir = workDir
	cfg.API.EnableSubAgents = true
	cfg.Features.MultiAgent = true
	cfg.Features.OpenAIAPI = false
	cfg.Features.WebUI = false
	cfg.WebUI.Enabled = false
	configPath := configDir + "/serve.json"
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save serve config: %v", err)
	}

	shutdown := make(chan struct{})
	ready := make(chan struct{})
	done := make(chan error, 1)
	var dispatcher *channels.Dispatcher
	var api *openaiapi.Server
	go func() {
		done <- Run(RunOptions{
			ConfigPath: configPath, Provider: "custom-test", Model: "test-model", WorkDir: workDir,
			MultiAgent: true, Unsafe: true, Shutdown: shutdown,
			OnReady: func(gotAPI *openaiapi.Server, gotDispatcher *channels.Dispatcher) {
				api, dispatcher = gotAPI, gotDispatcher
				close(ready)
			},
		}, "test")
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("serve Run exited before OnReady: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for real serve runtime OnReady")
	}
	if api == nil || dispatcher == nil {
		t.Fatal("OnReady did not receive real API and dispatcher")
	}

	if _, err := dispatcher.HandleMessage(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "real-runtime-user", ChatID: "real-runtime-chat", Text: "delegate this",
	}); err != nil {
		t.Fatalf("real dispatcher HandleMessage: %v", err)
	}
	channelSession := dispatcher.GetSession("channels/wechat/real-runtime-chat")
	if channelSession == nil || channelSession.Manager == nil {
		t.Fatal("real dispatcher did not create channel session")
	}
	sessionID := channelSession.Manager.GetHeader().ID
	deadline := time.Now().Add(5 * time.Second)
	var agents []openaiapi.SessionSubAgentInfo
	var err error
	for time.Now().Before(deadline) {
		// The observer forwards the child's running state before its done state;
		// poll until the final state arrives instead of asserting on the first
		// non-empty snapshot.
		agents, err = api.GetSessionSubAgents(sessionID)
		if err == nil && len(agents) == 1 && agents[0].Status == "done" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GetSessionSubAgents(%q): %v", sessionID, err)
	}
	if len(agents) != 1 || agents[0].Status != "done" {
		t.Fatalf("API sink sub-agents = %#v, want one done child", agents)
	}
	messages, err := api.GetSessionSubAgentMessages(sessionID, agents[0].ID)
	if err != nil {
		t.Fatalf("GetSessionSubAgentMessages: %v", err)
	}
	if len(messages) < 2 || messages[0].Role != "assistant" || messages[len(messages)-1].Role != "status" || messages[len(messages)-1].Content != "done" {
		t.Fatalf("API sink child messages = %#v", messages)
	}

	close(shutdown)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve Run shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for serve runtime shutdown")
	}
}
