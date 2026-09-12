package channels

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/messaging"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/serve/hooks"
	openaiapi "github.com/startvibecoding/mothx/internal/serve/openaiapi"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/workflow"
)

func TestChannelRunState(t *testing.T) {
	if got := channelRunState(nil); got != agentruntime.RunStateCompleted {
		t.Fatalf("nil error state = %q, want %q", got, agentruntime.RunStateCompleted)
	}
	if got := channelRunState(context.Canceled); got != agentruntime.RunStateCancelled {
		t.Fatalf("canceled state = %q, want %q", got, agentruntime.RunStateCancelled)
	}
	if got := channelRunState(context.DeadlineExceeded); got != agentruntime.RunStateTimedOut {
		t.Fatalf("deadline state = %q, want %q", got, agentruntime.RunStateTimedOut)
	}
	if got := channelRunState(errors.New("provider failed")); got != agentruntime.RunStateFailed {
		t.Fatalf("error state = %q, want %q", got, agentruntime.RunStateFailed)
	}
}

func TestEffectiveChannelModeDefaultsToYolo(t *testing.T) {
	if got := effectiveChannelMode("telegram", ""); got != agentruntime.ModeYolo {
		t.Fatalf("empty channel mode = %q, want %q", got, agentruntime.ModeYolo)
	}
	if got := effectiveChannelMode("telegram", agentruntime.ModeAgent); got != agentruntime.ModeAgent {
		t.Fatalf("explicit channel mode = %q, want %q", got, agentruntime.ModeAgent)
	}
}

type recordingChannelProvider struct {
	models     []*provider.Model
	calls      []provider.ChatParams
	background bool
}

// subAgentChannelProvider drives a real parent-agent tool call followed by a
// child-agent run, allowing the channel dispatcher integration test to verify
// the event forwarding path without a live provider.
type subAgentChannelProvider struct {
	model      *provider.Model
	mu         sync.Mutex
	calls      int
	errorChild bool
}

// Chat routes responses by request content rather than global call order: the
// parent follow-up call and the child agent's first call race with each other,
// so a shared call counter would nondeterministically hand the child response
// to the parent.
func (p *subAgentChannelProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	payload, _ := json.Marshal(params.Messages)
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		send := func(event provider.StreamEvent) bool {
			select {
			case ch <- event:
				return true
			case <-ctx.Done():
				return false
			}
		}
		switch {
		case strings.Contains(string(payload), "spawn-1"):
			// Parent follow-up call after the spawn tool result.
			send(provider.StreamEvent{Type: provider.StreamStart})
			send(provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "parent result"})
			send(provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"})
		case strings.Contains(string(payload), "inspect the project"):
			// The spawned child agent's provider call.
			send(provider.StreamEvent{Type: provider.StreamStart})
			if p.errorChild {
				send(provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("child provider failed")})
			} else {
				send(provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "child result"})
				send(provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"})
			}
		default:
			send(provider.StreamEvent{Type: provider.StreamStart})
			send(provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: "spawn-1", Name: "subagent_spawn", Arguments: []byte(`{"task":"inspect the project"}`),
			}})
			send(provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"})
		}
	}()
	return ch
}

func (p *subAgentChannelProvider) Name() string              { return "subagent-channel" }
func (p *subAgentChannelProvider) API() string               { return "openai-chat" }
func (p *subAgentChannelProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *subAgentChannelProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

func TestRunAgentForwardsRealSubAgentEventsWithChannelSessionID(t *testing.T) {
	for _, tc := range []struct {
		name       string
		errorChild bool
		wantType   agent.EventType
	}{
		{name: "done", wantType: agent.EventDone},
		{name: "error", errorChild: true, wantType: agent.EventError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			settings := config.DefaultSettings()
			settings.SessionDir = t.TempDir()
			cfg := DefaultConfig()
			cfg.WorkDir = workDir
			cfg.MultiAgent = true
			model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
			p := &subAgentChannelProvider{model: model, errorChild: tc.errorChild}
			d := &Dispatcher{
				cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
				security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: model, multiAgent: true,
				sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
			}
			type observed struct {
				sessionID string
				event     agent.Event
			}
			observedCh := make(chan observed, 32)
			d.SetSubAgentObserver(func(sessionID string, ev agent.Event) {
				observedCh <- observed{sessionID: sessionID, event: ev}
			})

			_, err := d.HandleMessage(context.Background(), messaging.InboundMessage{
				Platform: "wechat", UserID: "sender", ChatID: "channel-chat", Text: "delegate this",
			})
			if err != nil && !tc.errorChild {
				t.Fatalf("HandleMessage: %v", err)
			}
			sess, err := d.resolveSession("wechat", "channel-chat")
			if err != nil {
				t.Fatalf("resolve session: %v", err)
			}
			waitDeadline := time.After(2 * time.Second)
			var gotDone, gotError bool
			var seen []agent.EventType
			for !(gotDone || gotError) {
				select {
				case item := <-observedCh:
					if item.sessionID != sess.Manager.GetHeader().ID {
						t.Fatalf("observer session ID = %q, want canonical channel session %q", item.sessionID, sess.Manager.GetHeader().ID)
					}
					if item.event.AgentID == "" {
						t.Fatal("observer received event without child agent ID")
					}
					if tc.errorChild && item.event.Type == agent.EventError {
						if item.event.Error == nil || item.event.Error.Error() != "child provider failed" {
							t.Fatalf("child observer error = %v, want provider diagnostic", item.event.Error)
						}
					}
					seen = append(seen, item.event.Type)
					gotDone = gotDone || item.event.Type == agent.EventDone
					gotError = gotError || item.event.Type == agent.EventError
				case <-waitDeadline:
					t.Fatalf("timed out waiting for child %v event; observed=%v", tc.wantType, seen)
				}
			}
			if tc.errorChild && !gotError {
				t.Fatal("expected child error event")
			}
			if !tc.errorChild && !gotDone {
				t.Fatal("expected child done event")
			}
		})
	}
}

func TestDispatcherToOpenAIExternalSubAgentIntegration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		errorChild bool
		wantStatus string
	}{
		{name: "done", wantStatus: "done"},
		{name: "error", errorChild: true, wantStatus: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(workDir, "inspect.txt"), []byte("fixture"), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			settings := config.DefaultSettings()
			settings.SessionDir = t.TempDir()
			cfg := DefaultConfig()
			cfg.WorkDir = workDir
			cfg.MultiAgent = true
			model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
			p := &subAgentChannelProvider{model: model, errorChild: tc.errorChild}
			d := &Dispatcher{
				cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
				security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: model, multiAgent: true,
				sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
			}
			srv := openaiapi.NewExternalSubAgentServer()
			d.SetSubAgentObserver(srv.PublishExternalSubAgentEvent)

			sess, err := d.resolveSession("wechat", "channel-chat")
			if err != nil {
				t.Fatalf("resolve session: %v", err)
			}
			sessionID := sess.Manager.GetHeader().ID
			events, cancel := srv.SubscribeSessionEvents(sessionID)
			defer cancel()
			_, runErr := d.HandleMessage(context.Background(), messaging.InboundMessage{
				Platform: "wechat", UserID: "sender", ChatID: "channel-chat", Text: "delegate this",
			})
			if runErr != nil && !tc.errorChild {
				t.Fatalf("HandleMessage: %v", runErr)
			}

			var agents []openaiapi.SessionSubAgentInfo
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				agents, err = srv.GetSessionSubAgents(sessionID)
				if err == nil && len(agents) > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("GetSessionSubAgents(%q): %v", sessionID, err)
			}
			if len(agents) != 1 || agents[0].Status != tc.wantStatus || agents[0].ID == "" {
				t.Fatalf("external agents = %#v, want one %q agent", agents, tc.wantStatus)
			}
			messages, err := srv.GetSessionSubAgentMessages(sessionID, agents[0].ID)
			if err != nil {
				t.Fatalf("GetSessionSubAgentMessages: %v", err)
			}
			if tc.errorChild {
				if len(messages) != 1 || messages[0].Role != "status" || !messages[0].IsError {
					t.Fatalf("error transcript = %#v", messages)
				}
			} else {
				roles := map[string]bool{}
				for _, message := range messages {
					roles[message.Role] = true
				}
				if len(messages) < 2 || !roles["assistant"] || !roles["status"] {
					t.Fatalf("done transcript = %#v", messages)
				}
			}

			seen := map[string]bool{}
			waitDeadline := time.After(2 * time.Second)
			wantEvents := 2
			if tc.errorChild {
				wantEvents = 1
			}
			for len(seen) < wantEvents {
				select {
				case ev := <-events:
					if ev.SessionID != sessionID {
						t.Fatalf("broker session ID = %q, want %q", ev.SessionID, sessionID)
					}
					if ev.Event == "transcript" {
						if item, ok := ev.Data.(openaiapi.TranscriptStreamEvent); ok {
							if item.Type == "subagent_status" && item.Message != nil && item.Message.Content == tc.wantStatus {
								seen[tc.wantStatus] = true
							}
							if item.Type == "assistant_delta" {
								seen["assistant"] = true
							}
						}
					}
					if ev.Event == "tool_event" {
						seen["tool"] = true
					}
				case <-waitDeadline:
					t.Fatalf("timed out waiting for broker events: %#v", seen)
				}
			}
		})
	}
}

func (p *recordingChannelProvider) ResponsesBackgroundEnabled() bool { return p.background }

func newRecordingChannelProvider() *recordingChannelProvider {
	return &recordingChannelProvider{
		models: []*provider.Model{{ID: "m1", Name: "Model 1", ContextWindow: 4096, MaxTokens: 1024}},
	}
}

func TestChannelRouteIDUsesConversationIDForMessagingChannels(t *testing.T) {
	tests := []struct {
		name string
		msg  messaging.InboundMessage
		want string
	}{
		{
			name: "feishu chat id",
			msg:  messaging.InboundMessage{Platform: "feishu", UserID: "ou_sender", ChatID: "oc_chat"},
			want: "oc_chat",
		},
		{
			name: "wechat chat id",
			msg:  messaging.InboundMessage{Platform: "wechat", UserID: "user", ChatID: "chat"},
			want: "chat",
		},
		{
			name: "fallback user id",
			msg:  messaging.InboundMessage{Platform: "ws", UserID: "user", ChatID: "chat"},
			want: "user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelRouteID(tt.msg); got != tt.want {
				t.Fatalf("channelRouteID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleMessageNewRotatesWechatAndFeishuBoundSessions(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		userID   string
		chatID   string
		routeID  string
	}{
		{
			name:     "wechat uses chat id",
			platform: "wechat",
			userID:   "wechat-sender",
			chatID:   "wechat-chat",
			routeID:  "wechat-chat",
		},
		{
			name:     "feishu uses chat id instead of open id",
			platform: "feishu",
			userID:   "ou_sender",
			chatID:   "oc_chat",
			routeID:  "oc_chat",
		},
		{
			name:     "wechat falls back to user id",
			platform: "wechat",
			userID:   "wechat-user-only",
			routeID:  "wechat-user-only",
		},
		{
			name:     "feishu falls back to open id",
			platform: "feishu",
			userID:   "ou_user-only",
			routeID:  "ou_user-only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			workDir := t.TempDir()
			old, err := session.CreateBound(workDir, sessionDir, tt.platform, tt.routeID)
			if err != nil {
				t.Fatalf("create bound session: %v", err)
			}

			d := &Dispatcher{
				cfg:           DefaultConfig(),
				sessionDir:    sessionDir,
				sessions:      make(map[string]*ChannelSession),
				identityLocks: session.NewIdentityLocks(),
			}
			response, err := d.HandleMessage(context.Background(), messaging.InboundMessage{
				Platform: tt.platform,
				UserID:   tt.userID,
				ChatID:   tt.chatID,
				Text:     "/new",
			})
			if err != nil {
				t.Fatalf("HandleMessage(/new): %v", err)
			}
			if !strings.Contains(response, "New session created") {
				t.Fatalf("response = %q, want success", response)
			}

			binding, err := session.FindBinding(sessionDir, tt.platform, tt.routeID)
			if err != nil {
				t.Fatalf("find rotated binding: %v", err)
			}
			if binding == nil {
				t.Fatalf("binding for route %q was removed", tt.routeID)
			}
			if binding.SessionID == old.GetHeader().ID {
				t.Fatalf("binding still points to old session %q", binding.SessionID)
			}
			if binding.ChannelID != tt.routeID {
				t.Fatalf("binding channel ID = %q, want canonical route %q", binding.ChannelID, tt.routeID)
			}

			// A differing sender ID is intentional for the chat-ID cases: this
			// verifies the command passed through HandleMessage's canonical route
			// normalization rather than relying on the raw sender ID.
			if tt.chatID != "" && tt.userID != tt.routeID {
				rawBinding, findErr := session.FindBinding(sessionDir, tt.platform, tt.userID)
				if findErr != nil {
					t.Fatalf("find raw sender binding: %v", findErr)
				}
				if rawBinding != nil {
					t.Fatalf("command unexpectedly rotated sender binding %q instead of chat binding %q", tt.userID, tt.routeID)
				}
			}
		})
	}
}

func TestFormatAttachmentSummary(t *testing.T) {
	got := FormatAttachmentSummary([]provider.Attachment{
		{Kind: "citation", Name: "OpenAI", URL: "https://openai.com"},
		{Kind: "file", ProviderRef: "file_123"},
		{Kind: "citation", Name: "OpenAI", URL: "https://openai.com"},
	})
	for _, want := range []string{"Attachments:", "OpenAI: https://openai.com", "file: file_123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	}
	if strings.Count(got, "https://openai.com") != 1 {
		t.Fatalf("summary should deduplicate attachments: %q", got)
	}
}

func TestChannelDeliveryTextProjectionUsesPerOperationPayloads(t *testing.T) {
	controller := newChannelDeliveryController(t.TempDir())
	intent := agentruntime.DeliveryIntentPlan{
		ID: "intent-text-projection", RunID: "run-text-projection", TargetID: "chat",
		TransportContext: json.RawMessage(`{"caption":"caption","fallback":"fallback"}`),
	}
	caption := controller.textProjection(session.DeliveryOperation{ID: "caption-op", OperationKind: "send_text"}, intent, agentruntime.DeliveryOperationText(intent.TransportContext, "send_text"))
	fallback := controller.textProjection(session.DeliveryOperation{ID: "fallback-op", OperationKind: "send_fallback_text", DependsOn: "caption-op"}, intent, agentruntime.DeliveryOperationText(intent.TransportContext, "send_fallback_text"))
	if caption.Text != "caption" || fallback.Text != "fallback" {
		t.Fatalf("text projection payloads = %q, %q", caption.Text, fallback.Text)
	}
	if caption.ID == fallback.ID || caption.RunID != fallback.RunID || caption.TargetID != fallback.TargetID {
		t.Fatalf("text projection identity = %#v / %#v", caption, fallback)
	}
}

func TestHandleDeliveryMaterializesChannelImageThroughRuntime(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	p := newRecordingChannelProvider()
	p.models[0].Input = []string{"text", "image"}
	p.models[0].ContextWindow = 32768
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl8P6sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	response, err := d.HandleDelivery(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "sender", ChatID: "conversation", MessageID: "message-1", Text: "what is shown?",
		Attachments: []messaging.PlatformAttachment{{
			Reference: "wechat:opaque-media-reference", Kind: messaging.AttachmentImage,
			Open: func(context.Context) (messaging.AttachmentStream, error) {
				return messaging.AttachmentStream{Reader: io.NopCloser(bytes.NewReader(png))}, nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("HandleDelivery: %v", err)
	}
	if response.Text != "ok" {
		t.Fatalf("response text = %q, want ok", response.Text)
	}
	if len(p.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(p.calls))
	}
	foundManifest := false
	for _, message := range p.calls[0].Messages {
		if strings.Contains(message.Content, ".mothx/tmp/inputs/") {
			foundManifest = true
		}
		for _, content := range message.Contents {
			if content.Image != nil {
				t.Fatalf("channel image was sent directly to provider: %#v", p.calls[0].Messages)
			}
			if strings.Contains(content.Text, ".mothx/tmp/inputs/") {
				foundManifest = true
			}
		}
	}
	if !foundManifest {
		t.Fatalf("provider messages did not contain the Runtime path manifest: %#v", p.calls[0].Messages)
	}
	paths, err := filepath.Glob(filepath.Join(workDir, ".mothx", "tmp", "inputs", "*", "*.png"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("materialized image paths = %v, %v", paths, err)
	}
	stored, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(stored, png) {
		t.Fatalf("materialized image = %d bytes, %v", len(stored), err)
	}
	if strings.Contains(fmt.Sprintf("%#v", p.calls[0].Messages), "wechat:opaque-media-reference") {
		t.Fatalf("opaque platform reference leaked into provider message: %#v", p.calls[0].Messages)
	}
}

func TestHandleDeliveryProjectsRuntimePublishedArtifact(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "report.txt"), []byte("generated report"), 0600); err != nil {
		t.Fatalf("write artifact fixture: %v", err)
	}
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.Artifact = true
	model := &provider.Model{ID: "artifact-model", ContextWindow: 32768, MaxTokens: 1024}
	p := &artifactPublishingChannelProvider{model: model}
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: model, artifact: true,
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	response, err := d.HandleDelivery(context.Background(), messaging.InboundMessage{
		Platform: "feishu", UserID: "ou_sender", ChatID: "oc_chat", Text: "create a report",
	})
	if err != nil {
		t.Fatalf("HandleDelivery: %v", err)
	}
	if response.Text != "report is ready" || len(response.Attachments) != 1 {
		t.Fatalf("delivery response = %#v", response)
	}
	attachment := response.Attachments[0]
	if attachment.Kind != messaging.AttachmentFile || attachment.Filename != "report.txt" || attachment.Open == nil || attachment.Complete == nil {
		t.Fatalf("native attachment = %#v", attachment)
	}
	reader, err := attachment.Open(context.Background())
	if err != nil {
		t.Fatalf("open projected attachment: %v", err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || string(data) != "generated report" {
		t.Fatalf("projected artifact content = %q, read=%v close=%v", data, readErr, closeErr)
	}
	if response.TextDelivery == nil || response.TextDelivery.Prepare == nil || response.TextDelivery.Complete == nil {
		t.Fatalf("text delivery projection = %#v", response.TextDelivery)
	}
	if err := response.TextDelivery.Prepare(context.Background()); err != nil {
		t.Fatalf("claim caption delivery: %v", err)
	}
	response.TextDelivery.Complete(context.Background(), "delivered", "om_feishu_caption", "")
	attachment.Complete(context.Background(), "delivered", "om_feishu_media", "")
	var status string
	if err := session.QueryRootDatabase(settings.SessionDir, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT status FROM delivery_intents WHERE platform = 'feishu'`).Scan(&status)
	}); err != nil {
		t.Fatalf("query canonical delivery intent: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("canonical delivery status = %q, want delivered", status)
	}
	var legacyCount int
	if err := session.QueryRootDatabase(settings.SessionDir, func(db *dao.Database) error {
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM attachment_deliveries WHERE platform = 'feishu'`).Scan(&legacyCount)
	}); err != nil {
		t.Fatalf("query legacy delivery rows: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("normal channel path created %d legacy delivery row(s)", legacyCount)
	}
}

func (p *recordingChannelProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages:     append([]provider.Message(nil), params.Messages...),
		SystemPrompt: params.SystemPrompt,
	})
	ch := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "ok"}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return ch
}

func (p *recordingChannelProvider) Name() string              { return "recording-channel" }
func (p *recordingChannelProvider) API() string               { return "openai-chat" }
func (p *recordingChannelProvider) Models() []*provider.Model { return p.models }
func (p *recordingChannelProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

type artifactPublishingChannelProvider struct {
	model *provider.Model
	calls int
}

func (p *artifactPublishingChannelProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls++
	call := p.calls
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		if call == 1 {
			ch <- provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &provider.ToolCallBlock{
				ID: "publish-report", Name: "publish_artifact", Arguments: []byte(`{"path":"report.txt"}`),
			}}
			ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "tool_calls"}
			return
		}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "report is ready"}
		ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return ch
}

func (p *artifactPublishingChannelProvider) Name() string { return "artifact-publishing" }
func (p *artifactPublishingChannelProvider) API() string  { return "openai-chat" }
func (p *artifactPublishingChannelProvider) Models() []*provider.Model {
	return []*provider.Model{p.model}
}
func (p *artifactPublishingChannelProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

type failingChannelProvider struct {
	model *provider.Model
}

func (p *failingChannelProvider) Chat(context.Context, provider.ChatParams) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 3)
	ch <- provider.StreamEvent{Type: provider.StreamStart}
	ch <- provider.StreamEvent{Type: provider.StreamRetry, RetryAttempt: 1, RetryMax: 5, RetryAfterMS: 1000, Error: fmt.Errorf("Retrying (1/5): server overloaded — waiting 1s...")}
	ch <- provider.StreamEvent{Type: provider.StreamError, Error: fmt.Errorf("upstream returned HTTP 522")}
	close(ch)
	return ch
}
func (p *failingChannelProvider) Name() string              { return "failing-channel" }
func (p *failingChannelProvider) API() string               { return "openai-chat" }
func (p *failingChannelProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *failingChannelProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

func TestHandleMessagePersistsChannelFailureEvent(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""),
		provider: &failingChannelProvider{model: model}, model: model,
		sessions: make(map[string]*ChannelSession),
	}
	var progress []string
	_, err := d.HandleMessage(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "failure-user", Text: "继续",
		ProgressFunc: func(message string) { progress = append(progress, message) },
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 522") {
		t.Fatalf("HandleMessage error = %v, want provider diagnostic", err)
	}
	if len(progress) != 1 || progress[0] != "↻ Retrying (1/5); waiting 1s..." {
		t.Fatalf("progress = %#v, want structured retry notice", progress)
	}
	if strings.Contains(progress[0], "server overloaded") {
		t.Fatalf("progress = %#v, must not render provider retry detail", progress)
	}
	sess, err := d.resolveSession("wechat", "failure-user")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	events, err := session.ListSessionRunEvents(settings.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	var failedEvent *session.SessionRunEvent
	for i := range events {
		if events[i].EventType == "failed" && events[i].Status == "failed" {
			failedEvent = &events[i]
			break
		}
	}
	if failedEvent == nil || !strings.Contains(string(failedEvent.Data), "HTTP 522") {
		t.Fatalf("run events = %#v, want provider diagnostic", events)
	}
	run, err := session.GetSessionRun(settings.SessionDir, failedEvent.RunID)
	if err != nil || run == nil {
		t.Fatalf("get persisted run: %v, %#v", err, run)
	}
	if !strings.Contains(string(run.ErrorInfo), "HTTP 522") {
		t.Fatalf("run error info = %s, want provider diagnostic", run.ErrorInfo)
	}
}

func TestHandleMessageRecoversStaleLocalRunBeforeDurableAdmission(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""),
		provider: &failingChannelProvider{model: model}, model: model,
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	sess, err := d.resolveSession("wechat", "stale-run-user")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if err := session.SaveSessionRun(settings.SessionDir, session.SessionRun{
		ID: "stale-wechat-run", SessionID: sess.ID, WorkDir: workDir,
		Source: "wechat", Model: model.ID, Mode: agentruntime.ModeYolo, Status: "running",
		StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save stale run: %v", err)
	}

	_, err = d.HandleMessage(context.Background(), messaging.InboundMessage{
		Platform: "wechat", UserID: "stale-run-user", Text: "继续",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 522") {
		t.Fatalf("HandleMessage error = %v, want provider error rather than active-run constraint", err)
	}
	stale, err := session.GetSessionRun(settings.SessionDir, "stale-wechat-run")
	if err != nil || stale == nil || stale.Status != "failed" {
		t.Fatalf("stale run = %#v, err=%v", stale, err)
	}
	events, err := session.ListSessionRunEvents(settings.SessionDir, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.RunID == "stale-wechat-run" && event.EventType == "recovered" && event.Status == "failed" {
			return
		}
	}
	t.Fatalf("run events = %#v, want stale-run recovery event", events)
}

func TestApplySettingsRefreshesChannelProviderRetryConfig(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()

	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	settings.DefaultProvider = "retry-test"
	settings.DefaultModel = "m1"
	settings.Retry = config.RetrySettings{Enabled: false, MaxRetries: 1, BaseDelayMs: 1}
	settings.Providers = map[string]*config.ProviderConfig{
		"retry-test": {
			APIKey: "test-key", BaseURL: upstream.URL, API: "openai-chat",
			Models: []config.ModelConfig{{ID: "m1", Name: "M1"}},
		},
	}
	cfg := DefaultConfig()
	cfg.WorkDir = t.TempDir()
	cfg.DefaultProvider = "retry-test"
	cfg.DefaultModel = "m1"
	d, err := NewDispatcher(cfg, settings, "test", nil, nil)
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	defer d.Close()

	next := *settings
	next.Retry = config.RetrySettings{Enabled: true, MaxRetries: 1, BaseDelayMs: 1}
	if err := d.ApplySettings(&next); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	runtime := d.runtimeSnapshot()
	for range runtime.provider.Chat(context.Background(), provider.ChatParams{
		ModelID: "m1", Messages: []provider.Message{provider.NewUserMessage("retry")}, Abort: make(chan struct{}),
	}) {
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("provider attempts = %d, want 2 after settings refresh", got)
	}
}

func TestHandleMessageDelegatesBackgroundRunBeforeLocalAgentLoop(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	model := &provider.Model{ID: "m1", ContextWindow: 32768, MaxTokens: 1024}
	var got BackgroundRequest
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""),
		provider: &recordingChannelProvider{models: []*provider.Model{model}, background: true}, model: model,
		sessions: make(map[string]*ChannelSession),
	}
	d.SetBackgroundSubmitter(func(req BackgroundRequest) (string, error) {
		if req.IdempotencyKey != "channel:wechat:background-user:event-1" {
			t.Fatalf("idempotency key = %q", req.IdempotencyKey)
		}
		got = req
		return "responses-run-1", nil
	})
	response, err := d.HandleMessage(context.Background(), messaging.InboundMessage{Platform: "wechat", UserID: "background-user", MessageID: "event-1", Text: "run remotely"})
	if err != nil {
		t.Fatalf("HandleMessage error = %v", err)
	}
	if !strings.Contains(response, "responses-run-1") || got.Input.Text != "run remotely" || got.Platform != "wechat" || got.SessionID == "" || got.RunID == "" {
		t.Fatalf("response=%q request=%#v", response, got)
	}
}

func TestCancelChannelSessionRunAbortsActiveRun(t *testing.T) {
	sess := &ChannelSession{ID: "channel-cancel-user"}
	d := &Dispatcher{
		sessionDir: t.TempDir(),
		sessions:   map[string]*ChannelSession{sess.ID: sess},
	}
	ctx := beginChannelStopTestRun(t, d, sess, "channel-run", nil)
	if !d.CancelChannelSessionRun(sess.ID) {
		t.Fatal("CancelChannelSessionRun returned false for active run")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("active channel context was not cancelled")
	}
}

func beginChannelStopTestRun(t *testing.T, d *Dispatcher, sess *ChannelSession, runID string, runningAgent *agent.Agent) context.Context {
	t.Helper()
	mgr := session.New(t.TempDir(), d.sessionDir)
	if err := mgr.InitWithID(sess.ID); err != nil {
		t.Fatalf("init channel test session: %v", err)
	}
	sess.Manager = mgr
	guard, err := session.AcquireExecutionAdmission(d.sessionDir, sess.ID)
	if err != nil {
		t.Fatalf("acquire channel test admission: %v", err)
	}
	execution := &agentruntime.ExecutionRuntime{}
	execution.SetRunStore(agentruntime.RunStore{SessionDir: d.sessionDir})
	startedAt := time.Now()
	ctx, err := execution.BeginDurable(t.Context(), agentruntime.DurableRun{
		ID: runID, SessionID: sess.ID, WorkDir: mgr.GetHeader().Cwd, Source: "channel:wechat",
		Model: "test", Mode: "yolo", Status: string(agentruntime.RunStateRunning), StartedAt: startedAt,
	}, agentruntime.RunEvent{EventType: "started", Timestamp: startedAt})
	if err != nil {
		guard.Release()
		t.Fatalf("begin channel test run: %v", err)
	}
	if runningAgent != nil {
		execution.SetAgent(runningAgent)
	}
	sess.Execution = execution
	sess.runStateMu.Lock()
	sess.runID = runID
	sess.runAgent = runningAgent
	sess.runStateMu.Unlock()
	t.Cleanup(func() {
		if activeID, active := execution.Active(); active && activeID == runID {
			_ = execution.FinishDurable(runID, agentruntime.RunStateCancelled, "test cleanup", agentruntime.RunEvent{EventType: "finished"})
		}
		guard.Release()
	})
	return ctx
}

func TestResolveSessionCronOnlyDoesNotExposeSubAgentTools(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.MultiAgent = false
	cfg.Cron.Enabled = true

	store := cron.NewSQLiteCronStore(t.TempDir())
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg:        cfg,
		settings:   settings,
		allow:      &config.AllowConfig{},
		sessionDir: settings.SessionDir,
		security:   NewSecurity(cfg),
		hooksMgr:   hooks.NewManager("", ""),
		provider:   p,
		model:      p.models[0],
		cronStore:  store,
		sessions:   make(map[string]*ChannelSession),
	}

	if d.EnsureAgentManager() == nil {
		t.Fatal("cron should be able to initialize an agent manager without multi-agent")
	}
	sess, err := d.resolveSession("ws", "test-user")
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if _, ok := sess.Registry.Get("cron"); !ok {
		t.Fatal("cron-only session should expose cron tool")
	}
	if sess.ID != sess.Manager.GetHeader().ID {
		t.Fatalf("channel session ID = %q, want canonical session ID %q", sess.ID, sess.Manager.GetHeader().ID)
	}
	if sess.Runtime == nil || sess.Runtime.Manager != sess.Manager || sess.Runtime.Registry != sess.Registry {
		t.Fatalf("channel session is not backed by shared runtime: %#v", sess.Runtime)
	}
	if sess.ID == sessionKey("ws", "test-user") {
		t.Fatal("channel session ID must not be the routing key")
	}
	for _, name := range agent.SubAgentToolNames() {
		if _, ok := sess.Registry.Get(name); ok {
			t.Fatalf("cron-only session should not expose %s", name)
		}
	}
}

func TestRefreshBindingRemovesCachedRoute(t *testing.T) {
	d := &Dispatcher{sessions: make(map[string]*ChannelSession)}
	key := sessionKey("wechat", "user-a")
	d.sessions[key] = &ChannelSession{ID: "session-1", Platform: "wechat", UserID: "user-a"}

	d.RefreshBinding("wechat", "user-a")
	if got := d.GetSession(key); got != nil {
		t.Fatalf("cached binding route was not removed: %#v", got)
	}
}

func TestSessionLeaseDefersInvalidatedEvictionUntilRelease(t *testing.T) {
	d := &Dispatcher{sessions: make(map[string]*ChannelSession)}
	key := sessionKey("wechat", "lease-user")
	sess := &ChannelSession{ID: "session-lease", Platform: "wechat", UserID: "lease-user"}
	d.sessions[key] = sess

	lease, ok := d.acquireSessionLease(key, "wechat", "lease-user", sess)
	if !ok {
		t.Fatal("acquireSessionLease failed")
	}
	d.mu.Lock()
	d.invalidateSessionLocked(key, sess)
	d.mu.Unlock()
	if d.GetSession(key) == nil {
		t.Fatal("invalidated session was evicted while a request was pending")
	}
	lease.release()
	if d.GetSession(key) != nil {
		t.Fatal("invalidated session was not evicted after the last lease released")
	}
}

func TestToolCatalogReportsRuntimeAvailability(t *testing.T) {
	d := &Dispatcher{cfg: DefaultConfig()}
	items := d.ToolCatalog("wechat")
	byName := make(map[string]ToolCatalogItem, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}
	// Runtime-dependent tool that cannot be enabled without its runtime: a2a is
	// unavailable (and unchecked) when the A2A master feature is disabled.
	a2aItem, ok := byName["a2a_dispatch"]
	if !ok {
		t.Fatalf("catalog missing %q", "a2a_dispatch")
	}
	if a2aItem.Available || a2aItem.Default || a2aItem.UnavailableReason == "" {
		t.Fatalf("catalog item %q = %#v, want unavailable with reason", "a2a_dispatch", a2aItem)
	}
	// The browser and multi-agent tools are always selectable; their feature
	// flags only decide the default checked state, so with the flags off they
	// are unchecked but still available.
	for _, name := range []string{"browser", "delegate_subagent", "subagent_spawn", "workflow_run"} {
		item, ok := byName[name]
		if !ok {
			t.Fatalf("catalog missing %q", name)
		}
		if !item.Available {
			t.Fatalf("catalog item %q available=%v, want selectable when feature flag is off", name, item.Available)
		}
		if item.Default {
			t.Fatalf("catalog item %q default=%v, want unchecked when feature flag is off", name, item.Default)
		}
	}
}

func TestToolCatalogMatchesResolvedRegistryContract(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	sess, err := d.resolveSession("ws", "tool-contract")
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, tool := range sess.Registry.All() {
		registered[tool.Name()] = true
	}
	for _, item := range d.ToolCatalog("ws") {
		if item.Available && item.Default && !registered[item.Name] {
			t.Errorf("catalog says %q is available/default but registry omitted it", item.Name)
		}
		if !item.Available && registered[item.Name] {
			t.Errorf("catalog says %q is unavailable but registry registered it", item.Name)
		}
	}
}

// TestToolCatalogMultiAgentFlagControlsDefaultOnly verifies that turning the
// multiAgent feature off keeps the multi-agent tools selectable (available),
// only flipping their default checked state. Explicitly enabling such a tool
// must still register it in the resolved session registry.
func TestToolCatalogMultiAgentFlagControlsDefaultOnly(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.MultiAgent = false // feature flag OFF
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}

	// Catalog: multi-agent tools must be available but unchecked by default.
	for _, item := range d.ToolCatalog("wechat") {
		switch item.Name {
		case "delegate_subagent", "subagent_spawn", "subagent_status", "subagent_send", "subagent_destroy", "subagent_wait", "subagent_answer",
			"workflow_lint", "workflow_run", "workflow_status", "workflow_cancel", "browser":
			if !item.Available {
				t.Errorf("catalog %q available=%v, want selectable even when multiAgent/browser is off", item.Name, item.Available)
			}
			if item.Default {
				t.Errorf("catalog %q default=%v, want unchecked when multiAgent/browser is off", item.Name, item.Default)
			}
		}
	}

	// Explicitly enabling a multi-agent tool must register it at runtime.
	binding, err := session.CreateBound(workDir, settings.SessionDir, "wechat", "ma-default-only")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetChannelTools(settings.SessionDir, binding.GetHeader().ID, []session.ChannelToolConfig{
		{ToolName: "subagent_spawn", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	sess, err := d.resolveSession("wechat", "ma-default-only")
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, tool := range sess.Registry.All() {
		registered[tool.Name()] = true
	}
	if !registered["subagent_spawn"] {
		t.Fatalf("subagent_spawn not registered despite explicit enable; got %v", registered)
	}
	if !registered["workflow_run"] {
		t.Fatalf("workflow_run not registered despite explicit enable")
	}
}

func TestResolveSessionBoundTeamExpertUsesSessionManager(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	settings.ContextFiles.Enabled = false
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	cfg.MultiAgent = false
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	bound, err := session.CreateBound(workDir, settings.SessionDir, "wechat", "team-manager")
	if err != nil {
		t.Fatalf("create bound session: %v", err)
	}
	if err := bound.SetExpertBinding("software-company"); err != nil {
		t.Fatalf("bind team expert: %v", err)
	}

	sess, err := d.resolveSession("wechat", "team-manager")
	if err != nil {
		t.Fatalf("resolve expert session: %v", err)
	}
	if sess.AgentMgr == nil || sess.AgentMgr.Members == nil {
		t.Fatal("team channel session did not receive a session-scoped agent manager")
	}
	if d.agentMgr != nil && sess.AgentMgr == d.agentMgr {
		t.Fatal("team channel session reused the dispatcher-wide agent manager")
	}
	if _, ok := sess.AgentMgr.Members.Get("software-engineer"); !ok {
		t.Fatalf("team manager members = %v, missing software-engineer", sess.AgentMgr.Members.IDs())
	}
	if _, ok := sess.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("team channel registry missing subagent_spawn")
	}
}

func TestToolCatalogEveryAvailableSelectionMatchesRegistry(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = workDir
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: cfg, settings: settings, allow: &config.AllowConfig{}, sessionDir: settings.SessionDir,
		security: NewSecurity(cfg), hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
		sessions: make(map[string]*ChannelSession), identityLocks: session.NewIdentityLocks(),
	}
	binding, err := session.CreateBound(workDir, settings.SessionDir, "wechat", "catalog-contract")
	if err != nil {
		t.Fatal(err)
	}
	selections := make([]session.ChannelToolConfig, 0)
	for _, item := range d.ToolCatalog("wechat") {
		selections = append(selections, session.ChannelToolConfig{ToolName: item.Name, Enabled: item.Available})
	}
	if err := session.SetChannelTools(settings.SessionDir, binding.GetHeader().ID, selections); err != nil {
		t.Fatal(err)
	}
	sess, err := d.resolveSession("wechat", "catalog-contract")
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, tool := range sess.Registry.All() {
		registered[tool.Name()] = true
	}
	for _, item := range d.ToolCatalog("wechat") {
		if registered[item.Name] != item.Available {
			t.Errorf("tool %q registered=%v, catalog available=%v", item.Name, registered[item.Name], item.Available)
		}
	}
}

func TestBuildAgentLoadsReplayState(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()

	mgr := session.New(tmpDir, settings.SessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	oldUser := provider.NewUserMessage("old user context")
	oldAssistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant context"}})
	recentUser := provider.NewUserMessage("recent user context")
	recentAssistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent assistant context"}})
	_, _ = mgr.AppendMessage(oldUser)
	_, _ = mgr.AppendMessage(oldAssistant)
	recentUserID, _ := mgr.AppendMessage(recentUser)
	_, _ = mgr.AppendMessage(recentAssistant)
	_, _ = mgr.AppendCompaction("## Goal\ncompacted checkpoint", recentUserID, 100)

	registry := tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	d := &Dispatcher{
		cfg:      DefaultConfig(),
		settings: settings,
		hooksMgr: hooks.NewManager("", ""),
		provider: p,
		model:    p.models[0],
	}
	sess := &ChannelSession{
		ID:       "channels/ws/test-user",
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: registry,
		Mode:     "agent",
	}

	a, cleanup := d.buildAgent(context.Background(), sess, nil)
	defer cleanup(nil)

	for range a.Run(context.Background(), "continue") {
	}

	if len(p.calls) != 1 {
		t.Fatalf("provider call count = %d, want 1", len(p.calls))
	}

	foundSummary := false
	foundOldUser := false
	foundRecentUser := false
	for _, msg := range p.calls[0].Messages {
		if msg.SystemInjected && msg.Content == "## Goal\ncompacted checkpoint" {
			foundSummary = true
		}
		if msg.Content == oldUser.Content {
			foundOldUser = true
		}
		if msg.Content == recentUser.Content {
			foundRecentUser = true
		}
	}

	if !foundSummary {
		t.Fatal("channel agent did not replay compacted summary")
	}
	if foundOldUser {
		t.Fatal("channel agent still included pre-compaction old user message")
	}
	if !foundRecentUser {
		t.Fatal("channel agent lost recent user message from replay state")
	}
}

func TestBuildAgentInjectsChangedESMObjective(t *testing.T) {
	workDir := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(workDir, settings.SessionDir)
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	p := newRecordingChannelProvider()
	d := &Dispatcher{
		cfg: DefaultConfig(), settings: settings, sessionDir: settings.SessionDir,
		hooksMgr: hooks.NewManager("", ""), provider: p, model: p.models[0],
	}
	sess := &ChannelSession{
		ID: mgr.GetHeader().ID, Platform: "ws", UserID: "steering-user", WorkDir: workDir,
		Manager: mgr, Registry: tools.NewRegistry(workDir, sandbox.NewNoneSandbox()), Mode: "yolo",
	}
	if _, err := esm.NewStore(settings.SessionDir).Create(context.Background(), sess.ID, "finish the channel objective"); err != nil {
		t.Fatal(err)
	}

	a, cleanup := d.buildAgent(context.Background(), sess, nil)
	defer cleanup(nil)
	for range a.Run(context.Background(), "continue") {
	}
	if len(p.calls) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(p.calls))
	}
	for _, message := range p.calls[0].Messages {
		if message.SystemInjected && strings.Contains(message.Content, "finish the channel objective") {
			return
		}
	}
	t.Fatalf("channel provider messages missing ESM steering: %#v", p.calls[0].Messages)
}

func TestBuildAgentUsesCompactionSettings(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()
	settings.Compaction.KeepRecentTokens = 1

	mgr := session.New(tmpDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	_, _ = mgr.AppendMessage(provider.NewUserMessage("old user context"))
	_, _ = mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant context"}}))
	_, _ = mgr.AppendMessage(provider.NewUserMessage("recent user context"))
	_, _ = mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent assistant context"}}))

	d := &Dispatcher{
		cfg:      DefaultConfig(),
		settings: settings,
		hooksMgr: hooks.NewManager("", ""),
		provider: p,
		model:    p.models[0],
	}
	sess := &ChannelSession{
		ID:       "channels/ws/test-user",
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox()),
		Mode:     "agent",
	}

	a, cleanup := d.buildAgent(context.Background(), sess, nil)
	defer cleanup(nil)

	if !a.CanCompact() {
		t.Fatal("agent should use channel compaction keepRecent settings")
	}
}

func TestChannelHelpCommand(t *testing.T) {
	for _, platform := range []string{"wechat", "feishu"} {
		t.Run(platform, func(t *testing.T) {
			d := &Dispatcher{}
			reply, err := d.handleCommand(messaging.InboundMessage{Platform: platform, UserID: "test-user", Text: "/help"})
			if err != nil {
				t.Fatalf("handleCommand(/help): %v", err)
			}
			for _, command := range []string{"/new", "/clear", "/status", "/sessions", "/mode", "/compact", "/help"} {
				if !strings.Contains(reply, command) {
					t.Errorf("help reply missing %q: %q", command, reply)
				}
			}
		})
	}

	reply, err := (&Dispatcher{}).handleCommand(messaging.InboundMessage{Platform: "wechat", UserID: "test-user", Text: "/unknown"})
	if err != nil {
		t.Fatalf("handleCommand(/unknown): %v", err)
	}
	if !strings.Contains(reply, "/help") {
		t.Fatalf("unknown command reply should direct users to /help: %q", reply)
	}
}
func TestCompactCommandRunsImmediately(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()
	settings.Compaction.KeepRecentTokens = 1

	mgr := session.New(tmpDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	_, _ = mgr.AppendMessage(provider.NewUserMessage("old user context"))
	_, _ = mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old assistant context"}}))
	_, _ = mgr.AppendMessage(provider.NewUserMessage("recent user context"))
	_, _ = mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent assistant context"}}))

	sess := &ChannelSession{
		ID:       sessionKey("ws", "test-user"),
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox()),
		Mode:     "agent",
	}
	d := &Dispatcher{
		cfg:      DefaultConfig(),
		settings: settings,
		hooksMgr: hooks.NewManager("", ""),
		provider: p,
		model:    p.models[0],
		sessions: map[string]*ChannelSession{sess.ID: sess},
	}

	reply, err := d.handleCommand(messaging.InboundMessage{Platform: "ws", UserID: "test-user", Text: "/compact"})
	if err != nil {
		t.Fatalf("handleCommand() error = %v", err)
	}
	if !strings.Contains(reply, "compacted") {
		t.Fatalf("reply = %q, want compaction confirmation", reply)
	}
	if sess.ForceCompact {
		t.Fatal("ForceCompact should not be set for immediate compaction")
	}
	replay := mgr.GetReplayState()
	if len(replay.Messages) == 0 || !replay.Messages[0].SystemInjected {
		t.Fatalf("expected compacted summary in replay, got %#v", replay.Messages)
	}
}

func TestCompactCommandForcesSummaryOnlyWhenOnlyRecentContext(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()

	mgr := session.New(tmpDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	_, _ = mgr.AppendMessage(provider.NewUserMessage("hello"))
	_, _ = mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "hi"}}))

	sess := &ChannelSession{
		ID:       sessionKey("ws", "test-user"),
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox()),
		Mode:     "agent",
	}
	d := &Dispatcher{
		cfg:      DefaultConfig(),
		settings: settings,
		hooksMgr: hooks.NewManager("", ""),
		provider: p,
		model:    p.models[0],
		sessions: map[string]*ChannelSession{sess.ID: sess},
	}

	reply, err := d.handleCommand(messaging.InboundMessage{Platform: "ws", UserID: "test-user", Text: "/compact"})
	if err != nil {
		t.Fatalf("handleCommand() error = %v", err)
	}
	if sess.ForceCompact {
		t.Fatal("ForceCompact should not be set for immediate compaction")
	}
	if !strings.Contains(reply, "compacted") {
		t.Fatalf("reply = %q, want compaction confirmation", reply)
	}
	replay := mgr.GetReplayState()
	if len(replay.Messages) != 1 || !replay.Messages[0].SystemInjected {
		t.Fatalf("expected summary-only replay, got %#v", replay.Messages)
	}
}

func TestBuildAgentPromptFlagsFollowSessionRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()

	mgr := session.New(tmpDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}

	d := &Dispatcher{
		cfg:      DefaultConfig(),
		settings: settings,
		hooksMgr: hooks.NewManager("", ""),
		provider: p,
		model:    p.models[0],
	}
	manager := d.ensureAgentManager()
	if manager == nil {
		t.Fatal("ensureAgentManager returned nil")
	}

	reg := tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox())
	agent.RegisterSubAgentTools(reg, manager)
	agent.RegisterDelegateSubAgentTool(reg, manager)
	workflow.RegisterTools(reg, manager, nil)

	sess := &ChannelSession{
		ID:       "channels/ws/test-user",
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: reg,
		Mode:     "yolo",
	}

	a, cleanup := d.buildAgent(context.Background(), sess, nil)
	defer cleanup(nil)
	for range a.Run(context.Background(), "continue") {
	}

	if len(p.calls) != 1 {
		t.Fatalf("provider call count = %d, want 1", len(p.calls))
	}
	sp := p.calls[0].SystemPrompt
	for _, section := range []string{"## Sub-Agent Tools", "## Delegation Mode", "## Workflow Tools"} {
		if !strings.Contains(sp, section) {
			t.Errorf("system prompt missing %q although the corresponding tools are registered", section)
		}
	}
}

func TestBuildAgentPromptOmitsSubAgentSectionsWhenToolsAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	p := newRecordingChannelProvider()
	settings := config.DefaultSettings()

	mgr := session.New(tmpDir, t.TempDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}

	d := &Dispatcher{
		cfg:        DefaultConfig(),
		settings:   settings,
		hooksMgr:   hooks.NewManager("", ""),
		provider:   p,
		model:      p.models[0],
		multiAgent: true, // dispatcher flag alone must not inject prompt sections
	}

	sess := &ChannelSession{
		ID:       "channels/ws/test-user",
		Platform: "ws",
		UserID:   "test-user",
		WorkDir:  tmpDir,
		Manager:  mgr,
		Registry: tools.NewRegistry(tmpDir, sandbox.NewNoneSandbox()),
		Mode:     "yolo",
	}

	a, cleanup := d.buildAgent(context.Background(), sess, nil)
	defer cleanup(nil)
	for range a.Run(context.Background(), "continue") {
	}

	if len(p.calls) != 1 {
		t.Fatalf("provider call count = %d, want 1", len(p.calls))
	}
	sp := p.calls[0].SystemPrompt
	for _, section := range []string{"## Sub-Agent Tools", "## Delegation Mode", "## Workflow Tools"} {
		if strings.Contains(sp, section) {
			t.Errorf("system prompt contains %q although no sub-agent tools are registered", section)
		}
	}
}
