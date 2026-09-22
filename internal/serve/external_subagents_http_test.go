package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oschina/mothx/internal/agent"
	openaiapi "github.com/oschina/mothx/internal/serve/openaiapi"
)

func TestExternalSubAgentHistoryIsAvailableThroughServeHTTPRoutes(t *testing.T) {
	api := openaiapi.NewExternalSubAgentServer()

	rt := &channelRuntime{cfg: DefaultConfig()}
	mux := http.NewServeMux()
	rt.routes("")(api, mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	for _, tc := range []struct {
		name       string
		sessionID  string
		terminal   agent.Event
		wantStatus string
		wantError  bool
	}{
		{
			name:       "done",
			sessionID:  "channel-done",
			terminal:   agent.Event{Type: agent.EventDone, AgentID: "child-done"},
			wantStatus: "done",
		},
		{
			name:      "error",
			sessionID: "channel-error",
			terminal: agent.Event{
				Type: agent.EventError, AgentID: "child-error", Error: errors.New("child failed"),
			},
			wantStatus: "error",
			wantError:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api.PublishExternalSubAgentEvent(tc.sessionID, agent.Event{
				Type: agent.EventTextDelta, AgentID: tc.terminal.AgentID, TextDelta: "child output",
			})
			api.PublishExternalSubAgentEvent(tc.sessionID, tc.terminal)

			resp, err := http.Get(httpServer.URL + "/api/sessions/" + tc.sessionID + "/subagents")
			if err != nil {
				t.Fatalf("GET subagents: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("subagents status = %d, want 200", resp.StatusCode)
			}
			var agents struct {
				Subagents []openaiapi.SessionSubAgentInfo `json:"subagents"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
				t.Fatalf("decode subagents: %v", err)
			}
			if len(agents.Subagents) != 1 {
				t.Fatalf("subagents = %#v", agents.Subagents)
			}
			got := agents.Subagents[0]
			if got.ID != string(tc.terminal.AgentID) || got.Status != tc.wantStatus || got.Active || (got.Error != "") != tc.wantError {
				t.Fatalf("subagent JSON = %#v", got)
			}

			resp, err = http.Get(httpServer.URL + "/api/sessions/" + tc.sessionID + "/subagents/" + string(tc.terminal.AgentID) + "/messages")
			if err != nil {
				t.Fatalf("GET subagent messages: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("subagent messages status = %d, want 200", resp.StatusCode)
			}
			var messages struct {
				Messages []openaiapi.SessionMessageEntry `json:"messages"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
				t.Fatalf("decode subagent messages: %v", err)
			}
			if len(messages.Messages) != 2 {
				t.Fatalf("messages JSON = %#v", messages.Messages)
			}
			terminal := messages.Messages[1]
			if terminal.Role != "status" || terminal.Content != tc.wantStatus || terminal.IsError != tc.wantError {
				t.Fatalf("terminal message JSON = %#v", terminal)
			}
		})
	}
}
