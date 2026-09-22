package skillhub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestConfiguredClientsUseURLAndBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/search" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer server.Close()
	clients := ClientsForSettings(config.SkillHubSettings{Markets: []config.SkillHubMarketSettings{{ID: string(MarketSkillHub), APIURL: server.URL, Enabled: true, APIToken: "secret"}}})
	if len(clients) != 1 {
		t.Fatalf("clients = %d", len(clients))
	}
	if _, err := clients[0].Search(context.Background(), SearchQuery{Query: "fixture"}); err != nil {
		t.Fatal(err)
	}
}

func TestClawHubFileContentFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/files/SKILL.md") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"content":"# Fixture Skill"}`)
	}))
	defer server.Close()
	content, err := NewClawHubClient(server.URL, nil).FileContent(context.Background(), SkillID{Market: MarketClawHub, ID: "org/demo"}, "1.0.0", "SKILL.md")
	if err != nil || content != "# Fixture Skill" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}
