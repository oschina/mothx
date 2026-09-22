package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func newOnlineModelsTestApp() *App {
	return &App{
		settings:   config.DefaultSettings(),
		translator: i18n.New(i18n.LanguageEN),
		auth: authDialogState{
			Open:       true,
			View:       authViewModelList,
			ProviderID: "test-provider",
			Provider: providerEditState{
				API:     "openai-chat",
				BaseURL: "https://api.example.test/v1",
				APIKey:  "test-key",
			},
		},
	}
}

func TestStartFetchOnlineModelsRequiresBaseURLAndAPI(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.Provider.BaseURL = ""

	cmd := a.startFetchOnlineModels()
	if cmd != nil {
		t.Fatal("expected no command when Base URL is missing")
	}
	if a.auth.OnlineLoading {
		t.Fatal("OnlineLoading = true, want false")
	}
	if a.auth.Error == "" {
		t.Fatal("expected an error message when Base URL is missing")
	}

	a.auth.Provider.BaseURL = "https://api.example.test/v1"
	a.auth.Provider.API = ""
	if cmd := a.startFetchOnlineModels(); cmd != nil {
		t.Fatal("expected no command when API type is missing")
	}
}

func TestStartFetchOnlineModelsDiscoversFromModelsEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"online-model","name":"Online Model","context_length":64000,"max_output_tokens":4096,"input_modalities":["text","image"],"reasoning":true}]}`)
	}))
	defer upstream.Close()

	a := newOnlineModelsTestApp()
	a.auth.Provider.BaseURL = upstream.URL + "/v1"

	cmd := a.startFetchOnlineModels()
	if cmd == nil {
		t.Fatalf("expected fetch command, got error %q", a.auth.Error)
	}
	if !a.auth.OnlineLoading {
		t.Fatal("OnlineLoading = false, want true")
	}
	msg, ok := cmd().(onlineModelsLoadedMsg)
	if !ok {
		t.Fatalf("unexpected message type %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("fetch error: %v", msg.err)
	}

	a.handleOnlineModelsLoaded(msg)
	if a.auth.OnlineLoading {
		t.Fatal("OnlineLoading still true after result")
	}
	if a.auth.View != authViewModelsOnline {
		t.Fatalf("view = %v, want authViewModelsOnline", a.auth.View)
	}
	if len(a.auth.OnlineModels) != 1 || a.auth.OnlineModels[0].ID != "online-model" {
		t.Fatalf("online models = %#v", a.auth.OnlineModels)
	}
}

func TestHandleOnlineModelsLoadedErrorsStayInPlace(t *testing.T) {
	a := newOnlineModelsTestApp()

	a.handleOnlineModelsLoaded(onlineModelsLoadedMsg{providerID: "test-provider", err: fmt.Errorf("boom")})
	if a.auth.View != authViewModelList {
		t.Fatalf("view = %v, want authViewModelList", a.auth.View)
	}
	if !strings.Contains(a.auth.Error, "boom") {
		t.Fatalf("error = %q, want fetch failure containing boom", a.auth.Error)
	}

	a.auth.Error = ""
	a.handleOnlineModelsLoaded(onlineModelsLoadedMsg{providerID: "test-provider"})
	if a.auth.View != authViewModelList || a.auth.Error == "" {
		t.Fatalf("empty result should stay and set error: view=%v error=%q", a.auth.View, a.auth.Error)
	}
}

func TestHandleOnlineModelsLoadedIgnoresStaleProvider(t *testing.T) {
	a := newOnlineModelsTestApp()

	a.handleOnlineModelsLoaded(onlineModelsLoadedMsg{
		providerID: "other-provider",
		models:     []provider.DiscoveredModel{{ID: "m1"}},
	})
	if a.auth.View != authViewModelList || len(a.auth.OnlineModels) != 0 {
		t.Fatalf("stale result applied: view=%v models=%v", a.auth.View, a.auth.OnlineModels)
	}

	a.auth.Open = false
	a.handleOnlineModelsLoaded(onlineModelsLoadedMsg{
		providerID: "test-provider",
		models:     []provider.DiscoveredModel{{ID: "m1"}},
	})
	if len(a.auth.OnlineModels) != 0 {
		t.Fatal("result applied after dialog closed")
	}
}

func TestSelectOnlineModelTogglesDraftMembership(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineModels = []provider.DiscoveredModel{{
		ID:            "online-model",
		Name:          "Online Model",
		ContextWindow: 64000,
		MaxTokens:     4096,
		Input:         []string{"text", "image"},
		Reasoning:     true,
	}}
	a.pushAuthView(authViewModelsOnline)

	a.selectOnlineModel("online:online-model")
	me := a.auth.Models["online-model"]
	if me == nil {
		t.Fatalf("model not added to draft: %#v", a.auth.Models)
	}
	if me.Name != "Online Model" || me.ContextWindow != 64000 || me.MaxTokens != 4096 || !me.MaxTokensEdited || !me.Reasoning {
		t.Fatalf("discovered metadata not applied: %#v", me)
	}
	if len(me.Input) != 2 || me.Input[1] != "image" {
		t.Fatalf("input modalities = %#v", me.Input)
	}
	if len(a.auth.ModelOrder) != 1 || a.auth.ModelOrder[0] != "online-model" {
		t.Fatalf("model order = %#v", a.auth.ModelOrder)
	}
	if !a.isAuthModelAdded("online-model") {
		t.Fatal("isAuthModelAdded = false after add")
	}

	// Marker renders for added models.
	opts := a.authOnlineModelOptions()
	if !strings.HasPrefix(opts[0].Title, "✓") {
		t.Fatalf("added model marker missing: %q", opts[0].Title)
	}

	// Toggle again removes it.
	a.selectOnlineModel("online:online-model")
	if len(a.auth.Models) != 0 || len(a.auth.ModelOrder) != 0 {
		t.Fatalf("model not removed: models=%#v order=%#v", a.auth.Models, a.auth.ModelOrder)
	}
}

func TestSelectOnlineModelPrefersBuiltInDefaults(t *testing.T) {
	a := newOnlineModelsTestApp()
	// "xai" is a built-in provider with known model defaults.
	a.auth.ProviderID = "xai"
	a.auth.OnlineModels = []provider.DiscoveredModel{{ID: "grok-3", ContextWindow: 1}}
	a.pushAuthView(authViewModelsOnline)

	a.selectOnlineModel("online:grok-3")
	me := a.auth.Models["grok-3"]
	if me == nil {
		t.Fatalf("model not added: %#v", a.auth.Models)
	}
	if me.ContextWindow == 1 {
		t.Fatalf("built-in defaults should win over discovered metadata: %#v", me)
	}
}

func TestModelListOptionsIncludeFetchOnline(t *testing.T) {
	a := newOnlineModelsTestApp()
	opts := a.authModelListOptions()
	found := false
	for _, opt := range opts {
		if opt.Value == "fetchOnline" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fetchOnline option missing from model list: %#v", opts)
	}

	detail := a.authSettingsDetailOptions()
	found = false
	for _, opt := range detail {
		if opt.Value == "fetchOnline" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fetchOnline option missing from settings detail: %#v", detail)
	}
}

func TestDeleteSelectedOnlineModelRemovesDraftEntry(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineModels = []provider.DiscoveredModel{{ID: "online-model", Name: "Online Model"}}
	a.pushAuthView(authViewModelsOnline)
	a.selectOnlineModel("online:online-model")

	// First option is the added model.
	a.auth.Cursor = 0
	if !a.deleteSelectedOnlineModel() {
		t.Fatal("deleteSelectedOnlineModel = false")
	}
	if len(a.auth.Models) != 0 {
		t.Fatalf("model still present: %#v", a.auth.Models)
	}

	// Deleting a not-added model is a no-op.
	if a.deleteSelectedOnlineModel() {
		t.Fatal("deleteSelectedOnlineModel = true for not-added model")
	}
}

func TestFilterOnlineModelsRanksMatches(t *testing.T) {
	models := []provider.DiscoveredModel{
		{ID: "claude-sonnet-4", Name: "Claude Sonnet 4"},
		{ID: "claude-3-opus", Name: "Claude 3 Opus"},
		{ID: "gpt-4o-mini"},
		{ID: "sonnet-legacy"},
	}

	got := filterOnlineModels(models, "")
	if len(got) != len(models) {
		t.Fatalf("empty query indexes = %v, want all", got)
	}

	got = filterOnlineModels(models, "SONNET")
	want := []string{"sonnet-legacy", "claude-sonnet-4"}
	if len(got) != len(want) {
		t.Fatalf("matches = %v, want %d entries", got, len(want))
	}
	for i, idx := range got {
		if models[idx].ID != want[i] {
			t.Fatalf("match[%d] = %s, want %s (prefix ranks before substring, discovery order preserved)", i, models[idx].ID, want[i])
		}
	}

	got = filterOnlineModels(models, "gpt-4o-mini")
	if len(got) != 1 || models[got[0]].ID != "gpt-4o-mini" {
		t.Fatalf("exact match = %v", got)
	}

	if got := filterOnlineModels(models, "no-such-model"); len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestOnlineModelOptionsRespectSearch(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineModels = []provider.DiscoveredModel{
		{ID: "claude-sonnet-4"},
		{ID: "gpt-4o-mini"},
	}
	a.pushAuthView(authViewModelsOnline)

	a.auth.OnlineSearch = "claude"
	opts := a.authOnlineModelOptions()
	if len(opts) != 2 || opts[0].Value != "online:claude-sonnet-4" || opts[1].Value != "done" {
		t.Fatalf("filtered options = %#v", opts)
	}

	// The done entry stays reachable even without matches.
	a.auth.OnlineSearch = "zzz"
	opts = a.authOnlineModelOptions()
	if len(opts) != 1 || opts[0].Value != "done" {
		t.Fatalf("no-match options = %#v", opts)
	}
}

func TestOnlineModelSearchKeysFilterAndEsc(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineModels = []provider.DiscoveredModel{
		{ID: "claude-sonnet-4"},
		{ID: "gpt-4o-mini"},
	}
	a.pushAuthView(authViewModelsOnline)

	if handled, _ := a.handleAuthKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gpt")}); !handled {
		t.Fatal("runes not handled in online view")
	}
	if a.auth.OnlineSearch != "gpt" {
		t.Fatalf("OnlineSearch = %q, want gpt", a.auth.OnlineSearch)
	}
	opts := a.authOnlineModelOptions()
	if len(opts) != 2 || opts[0].Value != "online:gpt-4o-mini" {
		t.Fatalf("options after typing = %#v", opts)
	}

	if _, _ = a.handleAuthKey(tea.KeyMsg{Type: tea.KeyBackspace}); a.auth.OnlineSearch != "gp" {
		t.Fatalf("OnlineSearch after backspace = %q, want gp", a.auth.OnlineSearch)
	}

	// Esc with an active search clears the search instead of leaving the view.
	if _, _ = a.handleAuthKey(tea.KeyMsg{Type: tea.KeyEsc}); a.auth.OnlineSearch != "" || a.auth.View != authViewModelsOnline {
		t.Fatalf("Esc should clear search: search=%q view=%v", a.auth.OnlineSearch, a.auth.View)
	}
	if len(a.authOnlineModelOptions()) != 3 {
		t.Fatal("options not restored after clearing search")
	}

	// Esc without search pops back to the model list.
	if _, _ = a.handleAuthKey(tea.KeyMsg{Type: tea.KeyEsc}); a.auth.View != authViewModelList {
		t.Fatalf("view after second Esc = %v, want authViewModelList", a.auth.View)
	}
}

func TestHandleOnlineModelsLoadedResetsSearch(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineSearch = "stale"

	a.handleOnlineModelsLoaded(onlineModelsLoadedMsg{
		providerID: "test-provider",
		models:     []provider.DiscoveredModel{{ID: "m1"}},
	})
	if a.auth.OnlineSearch != "" {
		t.Fatalf("OnlineSearch = %q, want reset", a.auth.OnlineSearch)
	}
}

func TestDeleteSelectedOnlineModelRespectsSearchFilter(t *testing.T) {
	a := newOnlineModelsTestApp()
	a.auth.OnlineModels = []provider.DiscoveredModel{
		{ID: "claude-sonnet-4"},
		{ID: "gpt-4o-mini"},
	}
	a.pushAuthView(authViewModelsOnline)
	a.selectOnlineModel("online:claude-sonnet-4")
	a.selectOnlineModel("online:gpt-4o-mini")

	// Filter to gpt only; the cursor-0 entry is gpt-4o-mini, not claude.
	a.auth.OnlineSearch = "gpt"
	a.auth.Cursor = 0
	if !a.deleteSelectedOnlineModel() {
		t.Fatal("deleteSelectedOnlineModel = false")
	}
	if a.isAuthModelAdded("gpt-4o-mini") {
		t.Fatal("gpt-4o-mini should be removed")
	}
	if !a.isAuthModelAdded("claude-sonnet-4") {
		t.Fatal("claude-sonnet-4 should remain in draft")
	}
}
