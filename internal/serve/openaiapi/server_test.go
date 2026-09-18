package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	browserfeature "github.com/startvibecoding/mothx/internal/browser"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/contextfiles"
	"github.com/startvibecoding/mothx/internal/provider"
	openaiprovider "github.com/startvibecoding/mothx/internal/provider/openai"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/workflow"
)

type recordingAPIProvider struct {
	models []*provider.Model
	calls  []provider.ChatParams
}

func newRecordingAPIProvider() *recordingAPIProvider {
	return &recordingAPIProvider{
		models: []*provider.Model{{ID: "m1", Name: "Model 1", ContextWindow: 32768, MaxTokens: 2048}},
	}
}

func (p *recordingAPIProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls = append(p.calls, provider.ChatParams{
		Messages:     append([]provider.Message(nil), params.Messages...),
		SystemPrompt: params.SystemPrompt,
	})
	ch := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.StreamStart}
		ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "ok"}
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}

func (p *recordingAPIProvider) Name() string              { return "recording-API" }
func (p *recordingAPIProvider) API() string               { return "openai-chat" }
func (p *recordingAPIProvider) Models() []*provider.Model { return p.models }
func (p *recordingAPIProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// --- Config tests ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Listen != "127.0.0.1:7872" {
		t.Errorf("default listen = %q, want 127.0.0.1:7872", cfg.Listen)
	}
	if cfg.DefaultMode != "yolo" {
		t.Errorf("default mode = %q, want yolo", cfg.DefaultMode)
	}
	if cfg.ToolVisibility.Mode != "content" {
		t.Errorf("default tool visibility = %q, want content", cfg.ToolVisibility.Mode)
	}
	if cfg.SystemPromptMode != "append" {
		t.Errorf("default system prompt mode = %q, want append", cfg.SystemPromptMode)
	}
	if cfg.RequestTimeoutSecs != 1800 {
		t.Errorf("default timeout = %d, want 1800", cfg.RequestTimeoutSecs)
	}
	if cfg.Sandbox.Enabled {
		t.Error("sandbox should be disabled by default")
	}
	if cfg.EnableArtifact {
		t.Error("artifact publishing should be disabled by default")
	}
}

func TestValidateListenSecurity(t *testing.T) {
	tests := []struct {
		name    string
		listen  string
		auth    AuthConfig
		unsafe  bool
		wantErr bool
	}{
		{"loopback without auth", "127.0.0.1:8080", AuthConfig{}, false, false},
		{"localhost without auth", "localhost:8080", AuthConfig{}, false, false},
		{"public without auth", "0.0.0.0:8080", AuthConfig{}, false, true},
		{"wildcard without auth", ":8080", AuthConfig{}, false, true},
		{"public with auth", "0.0.0.0:8080", AuthConfig{Enabled: true, Tokens: []string{"token"}}, false, false},
		{"public unsafe", "0.0.0.0:8080", AuthConfig{}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateListenSecurity(&Config{Listen: tt.listen, Auth: tt.auth}, tt.unsafe)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateListenSecurity() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetWorkDirPrefersDefaultWorkDir(t *testing.T) {
	cfg := &Config{DefaultWorkDir: "/tmp/default", WorkingDir: "/tmp/legacy"}
	if got := cfg.GetWorkDir(); got != "/tmp/default" {
		t.Fatalf("GetWorkDir() = %q, want /tmp/default", got)
	}

	cfg.DefaultWorkDir = ""
	if got := cfg.GetWorkDir(); got != "/tmp/legacy" {
		t.Fatalf("legacy GetWorkDir() = %q, want /tmp/legacy", got)
	}
}

func TestValidateWorkDir(t *testing.T) {
	tests := []struct {
		name    string
		allowed *[]string
		dir     string
		wantErr bool
	}{
		{"nil=no check", nil, "/any/path", false},
		{"empty=deny all", &[]string{}, "/any/path", true},
		{"exact match", &[]string{"/home/user/projects"}, "/home/user/projects", false},
		{"prefix match", &[]string{"/home/user/projects"}, "/home/user/projects/foo", false},
		{"evil prefix", &[]string{"/home/user/projects"}, "/home/user/projects-evil", true},
		{"no match", &[]string{"/opt/repos"}, "/home/user/projects", true},
		{"multi allowed", &[]string{"/opt/repos", "/home/user"}, "/home/user/foo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AllowedWorkDirs: tt.allowed}
			err := cfg.ValidateWorkDir(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWorkDir(%q) error = %v, wantErr = %v", tt.dir, err, tt.wantErr)
			}
		})
	}
}

func TestValidateWorkDirRejectsSymlinkEscape(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	link := filepath.Join(allowedDir, "outside")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	allowed := []string{allowedDir}
	cfg := &Config{AllowedWorkDirs: &allowed}

	if err := cfg.ValidateWorkDir(filepath.Join(link, "new-session")); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestSameWorkDirUsesWindowsPathSemantics(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path semantics only apply on windows")
	}
	if !sameWorkDir(`C:/Projects/MothX`, `c:\projects\mothx`) {
		t.Fatal("equivalent Windows paths should identify the same work directory")
	}
	if sameWorkDir(`C:/Projects/MothX`, `D://Projects/MothX`) {
		t.Fatal("different Windows drive letters must not identify the same work directory")
	}
}

func TestCloneConfig(t *testing.T) {
	allowed := []string{"/home/test", "/opt/repos"}
	cfg := &Config{
		Listen: ":8080",
		Auth: AuthConfig{
			Enabled: true,
			Tokens:  []string{"sk-a", "sk-b"},
		},
		CORS: CORSConfig{
			Enabled:      true,
			AllowOrigins: []string{"http://localhost:3000"},
		},
		AllowedWorkDirs: &allowed,
	}

	clone := cloneConfig(cfg)
	if clone == cfg {
		t.Fatal("cloneConfig returned the original pointer")
	}

	clone.Listen = ":9090"
	clone.Auth.Tokens[0] = "sk-mutated"
	clone.CORS.AllowOrigins[0] = "http://mutated"
	(*clone.AllowedWorkDirs)[0] = "/tmp/mutated"

	if cfg.Listen != ":8080" {
		t.Fatalf("original listen mutated: %q", cfg.Listen)
	}
	if got := cfg.Auth.Tokens[0]; got != "sk-a" {
		t.Fatalf("original auth tokens mutated: %q", got)
	}
	if got := cfg.CORS.AllowOrigins[0]; got != "http://localhost:3000" {
		t.Fatalf("original CORS origins mutated: %q", got)
	}
	if got := (*cfg.AllowedWorkDirs)[0]; got != "/home/test" {
		t.Fatalf("original allowedWorkDirs mutated: %q", got)
	}
}

func TestApplyRunOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = ":8080"
	cfg.EnableSubAgents = false
	cfg.EnableDelegate = false
	cfg.EnableWorkflows = false
	cfg.Sandbox.Enabled = false
	cfg.DefaultWorkDir = "/tmp/original"

	applyRunOverrides(cfg, RunOptions{
		Port:       "9090",
		WorkDir:    "/tmp/override",
		Sandbox:    true,
		MultiAgent: true,
		Delegate:   true,
		Workflows:  true,
		WebSearch:  true,
		Browser:    true,
		Artifact:   true,
		A2AMaster:  true,
	})

	if cfg.Listen != ":9090" {
		t.Fatalf("listen = %q, want :9090", cfg.Listen)
	}
	if cfg.DefaultWorkDir != "/tmp/override" || cfg.GetWorkDir() != "/tmp/override" {
		t.Fatalf("defaultWorkDir = %q, effective = %q, want /tmp/override", cfg.DefaultWorkDir, cfg.GetWorkDir())
	}
	if !cfg.Sandbox.Enabled {
		t.Fatal("sandbox should be enabled")
	}
	if !cfg.EnableSubAgents {
		t.Fatal("multi-agent should be enabled")
	}
	if !cfg.EnableDelegate {
		t.Fatal("delegate should be enabled")
	}
	if !cfg.EnableWorkflows {
		t.Fatal("workflows should be enabled")
	}
	if !cfg.EnableWebSearch {
		t.Fatal("web search should be enabled")
	}
	if !cfg.EnableBrowser {
		t.Fatal("browser should be enabled")
	}
	if !cfg.EnableArtifact {
		t.Fatal("artifact publishing should be enabled")
	}
	if !cfg.EnableA2AMaster {
		t.Fatal("A2A master should be enabled")
	}
}

func TestApplyRunOverrides_PortForms(t *testing.T) {
	tests := []struct {
		name string
		port string
		want string
	}{
		{name: "port only", port: "9090", want: ":9090"},
		{name: "colon port", port: ":9090", want: ":9090"},
		{name: "host port", port: "127.0.0.1:9090", want: "127.0.0.1:9090"},
		{name: "all interfaces host port", port: "0.0.0.0:9090", want: "0.0.0.0:9090"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			applyRunOverrides(cfg, RunOptions{Port: tt.port})
			if cfg.Listen != tt.want {
				t.Fatalf("listen = %q, want %q", cfg.Listen, tt.want)
			}
		})
	}
}

func TestApplyRunOverrides_UnsafeDisablesAuthAndExposesListen(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "127.0.0.1:9090"
	cfg.Auth = AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}

	applyRunOverrides(cfg, RunOptions{Unsafe: true})

	if cfg.Auth.Enabled || len(cfg.Auth.Tokens) != 0 {
		t.Fatalf("auth = %#v, want disabled with no tokens", cfg.Auth)
	}
	if cfg.Listen != "0.0.0.0:9090" {
		t.Fatalf("listen = %q, want 0.0.0.0:9090", cfg.Listen)
	}
}

func TestApplyRunOverrides_UnsafePreservesExternalListen(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = "192.0.2.10:9090"
	cfg.Auth = AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}

	applyRunOverrides(cfg, RunOptions{Unsafe: true})

	if cfg.Listen != "192.0.2.10:9090" {
		t.Fatalf("listen = %q, want 192.0.2.10:9090", cfg.Listen)
	}
}

func TestLoadRunConfig_UsesInMemoryConfigAndClones(t *testing.T) {
	allowed := []string{"/home/test"}
	original := &Config{
		Listen: ":8080",
		Auth: AuthConfig{
			Enabled: true,
			Tokens:  []string{"sk-a"},
		},
		CORS: CORSConfig{
			Enabled:      true,
			AllowOrigins: []string{"http://localhost:3000"},
		},
		AllowedWorkDirs: &allowed,
	}

	cfg, err := loadRunConfig(RunOptions{
		Config:     original,
		Port:       "9090",
		WorkDir:    "/tmp/work",
		Sandbox:    true,
		MultiAgent: true,
		Delegate:   true,
		Workflows:  true,
		WebSearch:  true,
		Browser:    true,
		Artifact:   true,
		A2AMaster:  true,
	})
	if err != nil {
		t.Fatalf("loadRunConfig: %v", err)
	}

	if cfg == original {
		t.Fatal("loadRunConfig returned original config pointer")
	}
	if cfg.Listen != ":9090" {
		t.Fatalf("listen = %q, want :9090", cfg.Listen)
	}
	if cfg.DefaultWorkDir != "/tmp/work" || cfg.GetWorkDir() != "/tmp/work" {
		t.Fatalf("defaultWorkDir = %q, effective = %q, want /tmp/work", cfg.DefaultWorkDir, cfg.GetWorkDir())
	}
	if !cfg.Sandbox.Enabled || !cfg.EnableSubAgents || !cfg.EnableDelegate || !cfg.EnableWorkflows ||
		!cfg.EnableWebSearch || !cfg.EnableBrowser || !cfg.EnableArtifact || !cfg.EnableA2AMaster {
		t.Fatal("expected overrides to be applied")
	}

	if original.Listen != ":8080" {
		t.Fatalf("original listen mutated: %q", original.Listen)
	}
	if original.DefaultWorkDir != "" || original.WorkingDir != "" {
		t.Fatalf("original workDir mutated: default=%q legacy=%q", original.DefaultWorkDir, original.WorkingDir)
	}
	if original.Sandbox.Enabled || original.EnableSubAgents || original.EnableDelegate || original.EnableWorkflows ||
		original.EnableWebSearch || original.EnableBrowser || original.EnableArtifact || original.EnableA2AMaster {
		t.Fatal("original config booleans mutated")
	}
}

func TestRegisterRoutes_DisableAPI(t *testing.T) {
	srv := newTestServer(t)
	mux := http.NewServeMux()

	registerRoutes(mux, srv, RunOptions{DisableAPI: true})

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200", w.Code)
	}

	req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/v1/chat/completions status = %d, want 404", w.Code)
	}

	req = httptest.NewRequest("GET", "/v1/models", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/v1/models status = %d, want 404", w.Code)
	}
}

type hijackableResponseWriter struct {
	header http.Header
	conn   net.Conn
	peer   net.Conn
}

func (w *hijackableResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *hijackableResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *hijackableResponseWriter) WriteHeader(statusCode int) {}

func (w *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.conn, w.peer = net.Pipe()
	rw := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, rw, nil
}

func TestLoggingResponseWriterSupportsHijack(t *testing.T) {
	base := &hijackableResponseWriter{}
	lw := &loggingResponseWriter{ResponseWriter: base, statusCode: http.StatusOK}

	conn, _, err := lw.Hijack()
	if err != nil {
		t.Fatalf("Hijack: %v", err)
	}
	defer conn.Close()
	if base.peer != nil {
		defer base.peer.Close()
	}
}

// --- Auth middleware tests ---

func TestAuthMiddleware_Disabled(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: false}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_EnabledWithoutTokensRejects(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: true}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// --- CORS middleware tests ---

func TestCORSMiddleware_Enabled(t *testing.T) {
	handler := CORSMiddleware(CORSConfig{Enabled: true, AllowOrigins: []string{"http://example.com"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://example.com" {
		t.Errorf("CORS origin = %q, want http://example.com", got)
	}
}

func TestCORSMiddleware_MultipleOriginsEchoesRequestOrigin(t *testing.T) {
	handler := CORSMiddleware(CORSConfig{Enabled: true, AllowOrigins: []string{"http://a.example", "http://b.example"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://b.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://b.example" {
		t.Errorf("CORS origin = %q, want http://b.example", got)
	}
}

func TestCORSMiddleware_MultipleOriginsRejectsUnknownOrigin(t *testing.T) {
	handler := CORSMiddleware(CORSConfig{Enabled: true, AllowOrigins: []string{"http://a.example", "http://b.example"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS origin = %q, want empty", got)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	handler := CORSMiddleware(CORSConfig{Enabled: true}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

// --- Concurrency middleware tests ---

func TestConcurrencyMiddleware_NoLimit(t *testing.T) {
	handler := ConcurrencyMiddleware(0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// --- SessionPool tests ---

func TestSessionPool_PutGet(t *testing.T) {
	pool := NewSessionPool(0, 0)
	defer pool.Stop()

	sess := &APISession{ID: "sess-1", WorkDir: "/tmp", LastUsed: time.Now()}
	if err := pool.Put(sess); err != nil {
		t.Fatalf("put: %v", err)
	}
	got := pool.Get("sess-1")
	if got == nil || got.ID != "sess-1" {
		t.Error("expected to get session back")
	}
	if pool.Count() != 1 {
		t.Errorf("count = %d, want 1", pool.Count())
	}
}

func TestSessionPool_MaxSessions(t *testing.T) {
	pool := NewSessionPool(1, 0)
	defer pool.Stop()

	sess1 := &APISession{ID: "sess-1", LastUsed: time.Now()}
	if err := pool.Put(sess1); err != nil {
		t.Fatalf("put 1: %v", err)
	}
	sess2 := &APISession{ID: "sess-2", LastUsed: time.Now()}
	if err := pool.Put(sess2); err == nil {
		t.Error("expected pool full error")
	}
}

func TestSessionPool_Remove(t *testing.T) {
	pool := NewSessionPool(0, 0)
	defer pool.Stop()

	pool.Put(&APISession{ID: "sess-1", LastUsed: time.Now()})
	pool.Remove("sess-1")
	if pool.Get("sess-1") != nil {
		t.Error("session should be removed")
	}
}

func TestSessionPool_List(t *testing.T) {
	pool := NewSessionPool(0, 0)
	defer pool.Stop()

	pool.Put(&APISession{ID: "a", LastUsed: time.Now()})
	pool.Put(&APISession{ID: "b", LastUsed: time.Now()})
	ids := pool.List()
	if len(ids) != 2 {
		t.Errorf("list len = %d, want 2", len(ids))
	}
}

func TestGetOrCreateSessionRejectsDifferentWorkDirForPooledID(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	const id = "workdir-bound-session"
	first, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if first == nil {
		t.Fatal("created session is nil")
	}
	if _, err := srv.getOrCreateSession(id, t.TempDir()); err == nil {
		t.Fatal("getOrCreateSession with a different workDir succeeded, want error")
	}
}

func TestGetOrCreateSessionRehydratesBoundTeamExpert(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	const id = "webui-team-expert"
	bound := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := bound.InitWithID(id); err != nil {
		t.Fatalf("initialize persisted session: %v", err)
	}
	if err := bound.SetExpertBinding("software-company"); err != nil {
		t.Fatalf("bind team expert: %v", err)
	}

	// The server builds reusable resources before opening this Manager. Verify
	// the later BindSession path restores the expert package and creates a
	// session-scoped manager instead of leaving a plain WebUI session behind.
	sess, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("open expert session: %v", err)
	}
	if sess.Runtime == nil || !sess.Runtime.TeamExpertActive() {
		t.Fatalf("runtime expert binding = %+v, want active team", sess.Runtime)
	}
	if sess.AgentMgr == nil || sess.AgentMgr.Members == nil {
		t.Fatal("team expert session did not receive a session-scoped agent manager")
	}
	if _, ok := sess.AgentMgr.Members.Get("software-engineer"); !ok {
		t.Fatalf("team manager members = %v, missing software-engineer", sess.AgentMgr.Members.IDs())
	}
	if _, ok := sess.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("team expert session missing subagent_spawn")
	}
}

func TestAllocateSessionIDIsUniqueAndRemainsDelayed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	firstID, err := srv.AllocateSessionID()
	if err != nil {
		t.Fatalf("allocate first session ID: %v", err)
	}
	secondID, err := srv.AllocateSessionID()
	if err != nil {
		t.Fatalf("allocate second session ID: %v", err)
	}
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("allocated IDs = %q, %q", firstID, secondID)
	}
	if _, err := session.OpenByIDExact(srv.settings.GetSessionDir(), firstID); err == nil {
		t.Fatal("allocation should not persist a session before the first run")
	}

	sess, err := srv.getOrCreateSession(firstID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("materialize allocated session: %v", err)
	}
	if sess.ID != firstID || sess.Manager.GetHeader().ID != firstID {
		t.Fatalf("materialized session = %#v, want ID %q", sess, firstID)
	}
}

func TestAllocateSessionIDAllowsMissingIDWhenSessionDBExists(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	// The allocator must also work after the first session has created
	// sessions.db. In that case OpenByIDExact reports an unregistered ID rather
	// than a missing database, and the ID is still available for allocation.
	existing := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := existing.InitWithID("existing-session"); err != nil {
		t.Fatalf("init existing session: %v", err)
	}

	id, err := srv.AllocateSessionID()
	if err != nil {
		t.Fatalf("allocate session ID with existing database: %v", err)
	}
	if id == "" || id == "existing-session" {
		t.Fatalf("allocated ID = %q", id)
	}
	if _, err := session.OpenByIDExact(srv.settings.GetSessionDir(), id); err == nil {
		t.Fatal("allocation should remain delayed until the first run")
	}
}

func TestListActiveSessions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("s1"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatalf("append message: %v", err)
	}
	otherMgr := session.New("/tmp/history-only", srv.settings.GetSessionDir())
	if err := otherMgr.InitWithID("s3"); err != nil {
		t.Fatalf("init other session: %v", err)
	}
	if _, err := otherMgr.AppendMessage(provider.NewUserMessage("history only")); err != nil {
		t.Fatalf("append other message: %v", err)
	}
	older := time.Now().Add(-time.Minute)
	newer := time.Now()
	if err := srv.pool.Put(&APISession{ID: "s1", WorkDir: srv.cfg.GetWorkDir(), Manager: mgr, Mode: "agent", LastUsed: older}); err != nil {
		t.Fatalf("put s1: %v", err)
	}
	if err := srv.pool.Put(&APISession{ID: "s2", WorkDir: "/tmp/other", LastUsed: newer}); err != nil {
		t.Fatalf("put s2: %v", err)
	}

	sessions := srv.ListActiveSessions()
	if len(sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(sessions))
	}
	byID := make(map[string]ActiveSessionInfo)
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	if !byID["s1"].Active || byID["s1"].Mode != "agent" || byID["s1"].MessageCount != 1 {
		t.Fatalf("s1 details = %#v", byID["s1"])
	}
	if !byID["s2"].Active || byID["s2"].WorkDir != "/tmp/other" {
		t.Fatalf("s2 details = %#v", byID["s2"])
	}
	if byID["s3"].Active || byID["s3"].WorkDir != "/tmp/history-only" || byID["s3"].Preview != "history only" {
		t.Fatalf("s3 details = %#v", byID["s3"])
	}
}

func TestGetSessionMessagesReadsFromSessionDB(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("db-history"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	userMsg := provider.NewUserMessage("describe this")
	userMsg.Contents = []provider.ContentBlock{
		{Type: "text", Text: "describe this"},
		{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "aW1n", Bytes: 3}},
	}
	if _, err := mgr.AppendMessage(userMsg); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "ok"}})); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	messages, err := srv.GetSessionMessages("db-history")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "describe this" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if len(messages[0].Contents) != 2 || messages[0].Contents[1].Image == nil || messages[0].Contents[1].Image.Data != "aW1n" {
		t.Fatalf("first message contents = %#v", messages[0].Contents)
	}
	if messages[1].Role != "assistant" || messages[1].Content != "ok" {
		t.Fatalf("second message = %#v", messages[1])
	}
}
func TestGetSessionSubAgentsReturnsEmptyForPersistedInactiveSession(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("db-subagents"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatalf("append message: %v", err)
	}

	// Persisted but not loaded into the pool: no live sub-agents, empty list.
	agents, err := srv.GetSessionSubAgents("db-subagents")
	if err != nil {
		t.Fatalf("GetSessionSubAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("agents len = %d, want 0", len(agents))
	}

	msgs, err := srv.GetSessionSubAgentMessages("db-subagents", "agent-1")
	if err != nil {
		t.Fatalf("GetSessionSubAgentMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages len = %d, want 0", len(msgs))
	}

	// A session that does not exist anywhere still reports not found.
	if _, err := srv.GetSessionSubAgents("no-such-session"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSessionSubAgents unknown session err = %v, want ErrSessionNotFound", err)
	}
	if _, err := srv.GetSessionSubAgentMessages("no-such-session", "agent-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSessionSubAgentMessages unknown session err = %v, want ErrSessionNotFound", err)
	}
}

func TestGetSessionMessagesIncludesToolCallsAndCollapsedResults(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("tool-history"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("list files")); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{
		{Type: "text", Text: "I will inspect the tree."},
		{Type: "toolCall", ToolCall: &provider.ToolCallBlock{
			ID:        "call-1",
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"ls -la"}`),
		}},
	})
	if _, err := mgr.AppendMessage(assistant); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	fullOutput := "total 8\n-rw-r--r-- file.txt\n"
	if _, err := mgr.AppendMessage(provider.NewToolResultMessage("call-1", "bash", fullOutput, false)); err != nil {
		t.Fatalf("append tool result: %v", err)
	}

	messages, err := srv.GetSessionMessages("tool-history")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("messages len = %d, want 4: %#v", len(messages), messages)
	}
	if messages[1].Role != "assistant" || messages[1].Content != "I will inspect the tree." {
		t.Fatalf("assistant message = %#v", messages[1])
	}
	if messages[2].Role != "toolCall" || messages[2].ToolCallID != "call-1" || messages[2].ToolName != "bash" {
		t.Fatalf("tool call entry = %#v", messages[2])
	}
	if string(messages[2].Arguments) != `{"command":"ls -la"}` {
		t.Fatalf("tool call args = %s", messages[2].Arguments)
	}
	if messages[3].Role != "toolResult" || messages[3].Content != "" || !messages[3].HasDetail {
		t.Fatalf("tool result summary entry = %#v", messages[3])
	}
	if messages[3].Summary != "total 8" {
		t.Fatalf("tool result summary = %q", messages[3].Summary)
	}

	detail, err := srv.GetSessionToolResult("tool-history", "call-1")
	if err != nil {
		t.Fatalf("GetSessionToolResult: %v", err)
	}
	if detail.Content != fullOutput || detail.ToolName != "bash" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestGetSessionMessagesExtractsPlanToolCall(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("plan-history"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{
		{Type: "toolCall", ToolCall: &provider.ToolCallBlock{
			ID:   "plan-call",
			Name: "plan",
			Arguments: json.RawMessage(`{
				"title":"Ship WebUI plan",
				"steps":[
					{"title":"Read current UI","status":"done"},
					{"title":"Render todo card","status":"running"},
					{"title":"Build frontend","status":"pending"}
				],
				"note":"Keep output compact"
			}`),
		}},
	})
	if _, err := mgr.AppendMessage(assistant); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewToolResultMessage("plan-call", "plan", "Plan updated.", false)); err != nil {
		t.Fatalf("append tool result: %v", err)
	}

	messages, err := srv.GetSessionMessages("plan-history")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2: %#v", len(messages), messages)
	}
	if messages[0].Role != "toolCall" || messages[0].ToolName != "plan" || messages[0].Plan == nil {
		t.Fatalf("plan tool call = %#v", messages[0])
	}
	if messages[0].Plan.Title != "Ship WebUI plan" || messages[0].Plan.Note != "Keep output compact" {
		t.Fatalf("plan = %#v", messages[0].Plan)
	}
	if len(messages[0].Plan.Steps) != 3 || messages[0].Plan.Steps[1].Status != "running" {
		t.Fatalf("plan steps = %#v", messages[0].Plan.Steps)
	}
	if messages[1].Role != "toolResult" || messages[1].ToolName != "plan" {
		t.Fatalf("plan result = %#v", messages[1])
	}
}

func TestDeleteActiveSession(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("delete-me"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := srv.pool.Put(&APISession{ID: "delete-me", WorkDir: srv.cfg.GetWorkDir(), Manager: mgr, LastUsed: time.Now()}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	srv.defaultSessionIDs = map[string]string{srv.cfg.GetWorkDir(): "delete-me"}

	deleted, err := srv.DeleteActiveSession("delete-me")
	if err != nil {
		t.Fatalf("DeleteActiveSession: %v", err)
	}
	if !deleted {
		t.Fatal("session should be deleted")
	}
	if srv.pool.Get("delete-me") != nil {
		t.Fatal("session should be removed from pool")
	}
	if srv.defaultSessionIDs[srv.cfg.GetWorkDir()] != "" {
		t.Fatalf("default session ID was not cleared: %#v", srv.defaultSessionIDs)
	}
	sessions, err := session.ListForDir(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err != nil {
		t.Fatalf("ListForDir: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("persisted sessions = %d, want 0", len(sessions))
	}
}

func TestDeleteActiveSessionDeletesPersistedSession(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("persisted-delete"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatalf("append message: %v", err)
	}

	deleted, err := srv.DeleteActiveSession("persisted-delete")
	if err != nil {
		t.Fatalf("DeleteActiveSession: %v", err)
	}
	if !deleted {
		t.Fatal("persisted session should be deleted")
	}
	if _, err := session.OpenByIDExact(srv.settings.GetSessionDir(), "persisted-delete"); err == nil {
		t.Fatal("session should not exist after delete")
	}
}

func TestDeleteActiveSessionAmbiguousID(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	if err := srv.pool.Put(&APISession{ID: "same", WorkDir: "/tmp/a", LastUsed: time.Now()}); err != nil {
		t.Fatalf("put a: %v", err)
	}
	if err := srv.pool.Put(&APISession{ID: "same", WorkDir: "/tmp/b", LastUsed: time.Now()}); err != nil {
		t.Fatalf("put b: %v", err)
	}

	deleted, err := srv.DeleteActiveSession("same")
	if !errors.Is(err, ErrActiveSessionIDAmbiguous) {
		t.Fatalf("err = %v, want ErrActiveSessionIDAmbiguous", err)
	}
	if deleted {
		t.Fatal("ambiguous session should not be deleted")
	}
	if srv.pool.Count() != 2 {
		t.Fatalf("pool count = %d, want 2", srv.pool.Count())
	}
}

// --- parseMessages tests ---

func TestParseMessages(t *testing.T) {
	msgs := []RequestMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "explain main.go"},
	}
	lastUser, sysMsgs, history := parseMessages(msgs)
	if lastUser.Content != "explain main.go" {
		t.Errorf("lastUser = %q", lastUser.Content)
	}
	if len(sysMsgs) != 1 || sysMsgs[0] != "you are helpful" {
		t.Errorf("sysMsgs = %v", sysMsgs)
	}
	if len(history) != 2 { // "hello" and "hi there"
		t.Errorf("history len = %d, want 2", len(history))
	}
}

func TestParseMessages_NoUser(t *testing.T) {
	msgs := []RequestMessage{
		{Role: "system", Content: "test"},
	}
	lastUser, _, _ := parseMessages(msgs)
	if lastUser.Content != "" {
		t.Errorf("expected empty lastUser, got %q", lastUser.Content)
	}
}

func TestRequestMessageMultimodalContent(t *testing.T) {
	var msg RequestMessage
	body := `{"role":"user","content":[{"type":"text","text":"describe this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1n","detail":"auto"}}]}`
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Content != "describe this" {
		t.Fatalf("content = %q", msg.Content)
	}
	input, ingresses, err := requestRunInput(msg)
	if err != nil {
		t.Fatalf("requestRunInput: %v", err)
	}
	if input.Text != "describe this" || len(ingresses) != 1 || ingresses[0].Kind != agentruntime.AttachmentImage {
		t.Fatalf("runtime input = %#v, ingresses = %#v", input, ingresses)
	}
	stream, err := ingresses[0].Open(context.Background())
	if err != nil {
		t.Fatalf("open image ingress: %v", err)
	}
	data, readErr := io.ReadAll(stream.Reader)
	closeErr := stream.Reader.Close()
	if readErr != nil || closeErr != nil || stream.MediaType != "image/png" || string(data) != "img" {
		t.Fatalf("image ingress = %#v, data=%q, read=%v close=%v", stream, data, readErr, closeErr)
	}
}

func TestChatHandlerAcceptsImageResourceForTextOnlyModel(t *testing.T) {
	srv, p := newHistoryRecordingServer(t)
	defer srv.pool.Stop()
	p.models[0].Input = []string{"text"}

	body := fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"text","text":"describe"},{"type":"image_url","image_url":{"url":%q}}]}],"stream":false}`, testPNGDataURL)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
}

// --- SSE writer tests ---

func TestSSEWriter_ContentDelta(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "sess-1")
	sse.WriteContentDelta("hello")
	body := w.Body.String()
	if !strings.Contains(body, `"content":"hello"`) {
		t.Errorf("body doesn't contain content delta: %s", body)
	}
	if !strings.HasPrefix(body, "data: ") {
		t.Error("SSE data should start with 'data: '")
	}
}

func TestSSEWriter_Done(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "sess-1")
	sse.WriteDone(&CompletionUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	body := w.Body.String()
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Errorf("missing finish_reason: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Error("missing [DONE] sentinel")
	}
}

func TestSSEWriter_Attachments(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "sess-1")
	sse.WriteAttachments([]provider.Attachment{{Kind: "citation", Name: "source", URL: "https://example.test/source"}})
	body := w.Body.String()
	if !strings.Contains(body, "event: attachments") || !strings.Contains(body, "https://example.test/source") {
		t.Fatalf("attachments SSE = %q", body)
	}
}

func TestSSEWriter_WriteError(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "sess-1")
	sse.WriteError("something broke")
	body := w.Body.String()

	if !strings.HasPrefix(body, "data: ") {
		t.Errorf("error must be a regular SSE data frame: %q", body)
	}
	if !strings.Contains(body, "\"message\":\"something broke\"") {
		t.Errorf("missing error message: %s", body)
	}
	if !strings.Contains(body, "\"type\":\"server_error\"") {
		t.Errorf("missing error type: %s", body)
	}
	if strings.Contains(body, "event:") || strings.Contains(body, "x_session_id") {
		t.Errorf("extension fields/events must not be emitted: %s", body)
	}
}

func TestSSEWriter_ToolStatusContent(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "")
	sse.WriteToolStatusContent("🔧 [read] main.go", "running")
	body := w.Body.String()
	if !strings.Contains(body, "[running]") {
		t.Errorf("missing status in content: %s", body)
	}
	if !strings.Contains(body, "read") {
		t.Errorf("missing tool name in content: %s", body)
	}
}

func TestSSEWriter_ToolStatusEvent(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "")
	sse.WriteToolStatusEvent(ToolStatusEvent{
		Tool:       "bash",
		ToolCallID: "call-1",
		Status:     "running",
		Args:       map[string]any{"command": "ls"},
	})
	body := w.Body.String()
	if !strings.Contains(body, "event: tool_status") {
		t.Errorf("missing tool_status event: %s", body)
	}
	if !strings.Contains(body, `"tool":"bash"`) {
		t.Errorf("missing tool name: %s", body)
	}
	if !strings.Contains(body, `"toolCallId":"call-1"`) {
		t.Errorf("missing tool call id: %s", body)
	}
}

func TestSSEWriter_HostedItemEvent(t *testing.T) {
	w := httptest.NewRecorder()
	sse := NewSSEWriter(w, "test-model", "sess-1")
	sse.WriteHostedItem(&HostedItemEvent{ID: "search-1", Type: "web_search_call", Status: "completed", OutputIndex: 2})
	body := w.Body.String()
	if !strings.Contains(body, "event: hosted_item") || !strings.Contains(body, `"id":"search-1"`) || !strings.Contains(body, `"outputIndex":2`) {
		t.Fatalf("hosted item SSE = %q", body)
	}
}

func TestHostedItemEventUsesSafeProjection(t *testing.T) {
	item := hostedItemEvent(&provider.HostedItem{ID: "search-1", Type: "web_search_call", Status: "completed", Metadata: map[string]any{
		"title": "Source", "secret": "should-not-be-live", "url": "https://example.test/private",
	}})
	if item == nil || item.Metadata["title"] != "Source" {
		t.Fatalf("safe hosted item = %#v", item)
	}
	if _, ok := item.Metadata["secret"]; ok {
		t.Fatalf("secret metadata leaked: %#v", item.Metadata)
	}
	if _, ok := item.Metadata["url"]; ok {
		t.Fatalf("url metadata leaked: %#v", item.Metadata)
	}
}

func TestStreamingResponsePublishesToolEventsToSessionStream(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	events, cancel := srv.getSessionStreamHub().subscribe("sess-1")
	defer cancel()
	eventCh := make(chan agent.Event, 3)
	eventCh <- agent.Event{Type: agent.EventToolCall, ToolCall: &provider.ToolCallBlock{ID: "call-1", Name: "bash"}, ToolArgs: map[string]any{"command": "pwd"}}
	eventCh <- agent.Event{Type: agent.EventToolExecutionEnd, ToolCallID: "call-1", ToolName: "bash", ToolResult: "ok\nmore"}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)

	srv.handleStreamingResponseWithAgent(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), eventCh, "test-model", &APISession{ID: "sess-1"}, nil, true)

	for _, want := range []struct{ status, summary string }{{"running", ""}, {"completed", "ok"}} {
		deadline := time.After(time.Second)
		for {
			select {
			case event := <-events:
				if event.Name != "tool_event" {
					continue
				}
				tool, ok := event.Data.(ToolStatusEvent)
				if !ok || tool.Tool != "bash" || tool.ToolCallID != "call-1" || tool.Status != want.status || tool.Summary != want.summary {
					t.Fatalf("tool event = %#v, want status=%q summary=%q", event.Data, want.status, want.summary)
				}
				goto next
			case <-deadline:
				t.Fatal("timed out waiting for tool event")
			}
		}
	next:
	}
}

func TestStreamingApprovalRequestReachesChatSSEAndResumesAgent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	sess, err := srv.getOrCreateSession("approval-sse", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(srv.cfg.GetWorkDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	runningAgent := agent.New(agent.Config{Mode: "agent"}, registry)
	beginApprovalTestRun(sess, "run_approval_sse", runningAgent)
	events, cancel := srv.getSessionStreamHub().subscribe(sess.ID)
	defer cancel()
	eventCh := make(chan agent.Event, 1)
	approvalResult := make(chan bool, 1)
	go func() {
		approvalResult <- runningAgent.RequestApproval(eventCh, "bash", map[string]any{"command": "go test ./..."})
	}()

	w := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		srv.handleStreamingResponseWithAgent(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), eventCh, "test-model", sess, runningAgent, true)
		close(streamDone)
	}()

	var request SessionApprovalRequest
	deadline := time.After(time.Second)
	for request.ApprovalID == "" {
		select {
		case event := <-events:
			if event.Name != "approval_request" {
				continue
			}
			var ok bool
			request, ok = event.Data.(SessionApprovalRequest)
			if !ok {
				t.Fatalf("approval event = %#v", event.Data)
			}
		case <-deadline:
			t.Fatal("timed out waiting for approval request")
		}
	}
	if _, err := srv.ResolveSessionApproval(sess.ID, request.ApprovalID, SessionApprovalResponse{Action: "approve_once"}); err != nil {
		t.Fatal(err)
	}
	select {
	case approved := <-approvalResult:
		if !approved {
			t.Fatal("agent received denied approval, want approved")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked agent did not resume after approval")
	}
	eventCh <- agent.Event{Type: agent.EventRunFinished, Status: agent.TaskSuccess, Done: true, StopReason: "stop"}
	close(eventCh)
	select {
	case <-streamDone:
	case <-time.After(time.Second):
		t.Fatal("chat stream did not complete after approval")
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: approval_request") {
		t.Fatalf("missing approval request in chat SSE: %s", body)
	}
	for _, want := range []string{`"approvalId":"` + request.ApprovalID + `"`, `"sessionId":"approval-sse"`, `"command":"go test ./..."`, "[DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("chat SSE missing %s: %s", want, body)
		}
	}
}

func TestSessionStreamDoneIncludesRunIdentity(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	events, cancel := srv.getSessionStreamHub().subscribe("sess-1")
	defer cancel()

	srv.publishSessionStreamDone("sess-1", "run-1", "canceled")
	select {
	case event := <-events:
		if event.Name != "done" {
			t.Fatalf("event name = %q, want done", event.Name)
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			t.Fatalf("event data type = %T", event.Data)
		}
		if data["sessionId"] != "sess-1" || data["runId"] != "run-1" || data["status"] != "canceled" {
			t.Fatalf("unexpected done event: %#v", data)
		}
		if data["timestamp"] == "" {
			t.Fatalf("missing done timestamp: %#v", data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for done event")
	}
}

// --- writeError / writeJSON tests ---

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad input", "invalid_request_error")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	var resp ErrorResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error.Message != "bad input" {
		t.Errorf("error message = %q", resp.Error.Message)
	}
}

// --- Health handler test ---

func TestHealthHandler(t *testing.T) {
	srv := &Server{
		version: "test",
		pool:    NewSessionPool(0, 0),
	}
	defer srv.pool.Stop()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp HealthResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "ok" {
		t.Errorf("status = %q", resp.Status)
	}
	if resp.Version != "test" {
		t.Errorf("version = %q", resp.Version)
	}
}

// --- Models handler test ---

func TestModelsHandler(t *testing.T) {
	mockP := provider.NewMockProvider("test", []*provider.Model{
		{ID: "m1", Name: "Model 1", Provider: "test", Input: []string{"text", "image"}},
		{ID: "m2", Name: "Model 2", Provider: "test"},
	}, nil)
	srv := &Server{
		provider: mockP,
	}
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.handleModels(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp ModelListResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Object != "list" {
		t.Errorf("object = %q", resp.Object)
	}
	if len(resp.Data) != 2 {
		t.Errorf("models = %d, want 2", len(resp.Data))
	}
	if len(resp.Data[0].Input) != 2 || resp.Data[0].Input[0] != "text" || resp.Data[0].Input[1] != "image" {
		t.Errorf("model input = %#v, want text/image", resp.Data[0].Input)
	}
	if resp.Data[0].Provider != "test" {
		t.Errorf("model provider = %q, want test", resp.Data[0].Provider)
	}
}

// --- Model catalog handler test ---

func TestModelCatalogHandler(t *testing.T) {
	settings := config.DefaultSettings()
	settings.Providers = map[string]*config.ProviderConfig{
		// Configured without explicit models: built-in preset models must still
		// be listed, matching what the TUI provider exposes.
		"anthropic": {APIKey: "fake-key"},
		"custom": {
			BaseURL: "https://example.com/v1",
			APIKey:  "fake-key",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "m1", Name: "M1", Input: []string{"text", "image"}}},
		},
	}
	srv := &Server{
		settings:     settings,
		providerName: "custom",
		model:        &provider.Model{ID: "m1", Provider: "custom"},
	}

	req := httptest.NewRequest("GET", "/api/models/catalog", nil)
	w := httptest.NewRecorder()
	srv.handleModelCatalog(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp ModelCatalogResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	if resp.DefaultProvider != "custom" || resp.DefaultModel != "m1" {
		t.Errorf("default = %q/%q, want custom/m1", resp.DefaultProvider, resp.DefaultModel)
	}
	// Shared factory ordering: anthropic (priority 60) before custom (100).
	if len(resp.Providers) != 2 || resp.Providers[0] != "anthropic" || resp.Providers[1] != "custom" {
		t.Fatalf("providers = %v, want [anthropic custom]", resp.Providers)
	}
	var sawPreset, sawCustom bool
	for _, m := range resp.Data {
		switch {
		case m.Provider == "anthropic" && m.ID == "claude-sonnet-4-5":
			sawPreset = true
		case m.Provider == "custom" && m.ID == "m1":
			sawCustom = true
			if len(m.Input) != 2 || m.Input[0] != "text" || m.Input[1] != "image" {
				t.Errorf("m1 input = %#v, want text/image", m.Input)
			}
		}
	}
	if !sawPreset {
		t.Error("built-in preset model missing from catalog")
	}
	if !sawCustom {
		t.Error("configured model missing from catalog")
	}

	// The active provider is listed even when it only exists as a preset.
	presetSrv := &Server{
		settings:     &config.Settings{},
		providerName: "anthropic",
		model:        &provider.Model{ID: "claude-sonnet-4-5", Provider: "anthropic"},
	}
	req = httptest.NewRequest("GET", "/api/models/catalog", nil)
	w = httptest.NewRecorder()
	presetSrv.handleModelCatalog(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preset status = %d, want 200", w.Code)
	}
	resp = ModelCatalogResponse{}
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Providers) != 1 || resp.Providers[0] != "anthropic" {
		t.Fatalf("preset providers = %v, want [anthropic]", resp.Providers)
	}
	if len(resp.Data) == 0 {
		t.Fatal("preset catalog has no models")
	}

	// Method guard.
	req = httptest.NewRequest("POST", "/api/models/catalog", nil)
	w = httptest.NewRecorder()
	srv.handleModelCatalog(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", w.Code)
	}
}

// --- Chat handler slash command test ---

func newTestServer(t *testing.T) *Server {
	t.Helper()
	cwd := t.TempDir()
	models := []*provider.Model{
		{ID: "m1", Name: "Model 1"},
	}
	mockP := provider.NewMockProvider("test", models, nil)

	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(cwd, "sessions")

	sbMgr := sandbox.NewManager(cwd)
	sbMgr.SetLevel(sandbox.LevelNone)

	skillsMgr := skills.NewManager(filepath.Join(cwd, "skills-global"), filepath.Join(cwd, "skills-project"))

	pool := NewSessionPool(0, 0)

	cfg := DefaultConfig()
	cfg.WorkingDir = cwd

	return &Server{
		cfg:        cfg,
		settings:   settings,
		version:    "test",
		provider:   mockP,
		model:      models[0],
		sandboxMgr: sbMgr,
		skillsMgr:  skillsMgr,
		pool:       pool,
	}
}

func newRecordingAPIServer(t *testing.T) (*Server, *recordingAPIProvider) {
	t.Helper()
	srv := newTestServer(t)
	p := newRecordingAPIProvider()
	srv.provider = p
	srv.model = p.models[0]
	return srv, p
}

func TestClearSessionRebindsRuntimeToFreshManager(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("clear-rebind", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	old := sess.Manager
	if err := srv.clearSession(sess, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("clearSession: %v", err)
	}
	if sess.Manager == old {
		t.Fatal("clearSession retained the deleted manager")
	}
	if sess.Runtime == nil || sess.Runtime.Manager != sess.Manager {
		t.Fatalf("runtime manager = %p, want fresh manager %p", sess.Runtime.Manager, sess.Manager)
	}
	if got := sess.Manager.GetMessages(); len(got) != 0 {
		t.Fatalf("fresh session messages = %#v, want none", got)
	}
}

func TestCloneModelCopiesMutableFields(t *testing.T) {
	model := &provider.Model{
		ID:     "m1",
		Input:  []string{"text"},
		Compat: &provider.ModelCompat{ThinkingFormat: "anthropic"},
	}

	clone := cloneModel(model)
	clone.Input[0] = "image"
	clone.Compat.ThinkingFormat = "deepseek"

	if model.Input[0] != "text" {
		t.Fatalf("original input mutated: %v", model.Input)
	}
	if model.Compat.ThinkingFormat != "anthropic" {
		t.Fatalf("original compat mutated: %s", model.Compat.ThinkingFormat)
	}
}

func TestChatHandler_EmptyMessages(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	body := `{"messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestChatHandler_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// --- Commands tests ---

func TestCommands_UnknownCommand(t *testing.T) {
	srv := newTestServer(t)
	result := srv.handleCommand(nil, "/foobar")
	if result == nil {
		t.Fatal("expected result for unknown command")
	}
	if !result.Error {
		t.Error("expected error=true for unknown command")
	}
}

func TestCommands_NotACommand(t *testing.T) {
	srv := newTestServer(t)
	result := srv.handleCommand(nil, "hello world")
	if result != nil {
		t.Error("non-command should return nil")
	}
}

func TestCommands_Status(t *testing.T) {
	srv := newTestServer(t)
	sess := &APISession{ID: "test-sess", WorkDir: "/tmp", Mode: "agent"}
	result := srv.cmdStatus(sess)
	if result == nil {
		t.Fatal("expected result")
	}
	if !strings.Contains(result.Message, "AGENT") {
		t.Errorf("status should show mode, got %q", result.Message)
	}
	if !strings.Contains(result.Message, "test-sess") {
		t.Errorf("status should show session ID, got %q", result.Message)
	}
}

func TestCommands_CompactNoSession(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdCompact(nil)
	if result == nil {
		t.Fatal("expected result")
	}
	if !result.Error {
		t.Error("expected error for nil session")
	}
}

func TestCommands_CompactTooShort(t *testing.T) {
	srv := newTestServer(t)
	// Create a session with less than 2 messages
	sess := &APISession{ID: "test-sess", WorkDir: "/tmp"}
	mgr := session.New(t.TempDir(), t.TempDir())
	mgr.Init()
	sess.Manager = mgr
	result := srv.cmdCompact(sess)
	if result == nil {
		t.Fatal("expected result")
	}
	if !result.Error {
		t.Error("expected compaction error for empty conversation")
	}
	if !strings.Contains(result.Message, "no messages to compact") {
		t.Errorf("expected no messages error, got %q", result.Message)
	}
}

func TestCommands_CompactRunsImmediately(t *testing.T) {
	srv, _ := newRecordingAPIServer(t)
	srv.settings.Compaction.KeepRecentTokens = 1
	sess := &APISession{ID: "test-sess", WorkDir: t.TempDir()}
	mgr := session.New(sess.WorkDir, t.TempDir())
	mgr.Init()
	// Append enough history so there is an older turn to summarize.
	mgr.AppendMessage(provider.NewUserMessage("old hello"))
	mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "old hi"}}))
	mgr.AppendMessage(provider.NewUserMessage("recent hello"))
	mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "recent hi"}}))
	sess.Manager = mgr

	result := srv.cmdCompact(sess)
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Error {
		t.Errorf("unexpected error: %s", result.Message)
	}
	if sess.ForceCompact {
		t.Error("ForceCompact should not be set for immediate compaction")
	}
	if !strings.Contains(result.Message, "compacted") {
		t.Errorf("expected compaction confirmation, got %q", result.Message)
	}
	replay := mgr.GetReplayState()
	if len(replay.Messages) == 0 || !replay.Messages[0].SystemInjected {
		t.Fatalf("expected compacted summary in replay, got %#v", replay.Messages)
	}
}

func TestCommands_RuleCreatesFile(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	sess := &APISession{ID: "test-sess", WorkDir: workDir}

	result := srv.cmdRule(sess, []string{"/rule"})
	if result == nil {
		t.Fatal("expected result")
	}
	if result.Error {
		t.Fatalf("unexpected error: %s", result.Message)
	}
	rulePath := contextfiles.RuleFilePath(workDir)
	data, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read rule file: %v", err)
	}
	if string(data) != sess.RuleContent {
		t.Fatal("session RuleContent does not match file content")
	}
	if !strings.Contains(sess.RuleContent, "Never use sudo") {
		t.Fatalf("unexpected rule content: %q", sess.RuleContent)
	}
}

func TestCommands_RulePreservesExistingUnlessForced(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	rulePath := contextfiles.RuleFilePath(workDir)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0755); err != nil {
		t.Fatalf("mkdir rule dir: %v", err)
	}
	if err := os.WriteFile(rulePath, []byte("custom rule"), 0644); err != nil {
		t.Fatalf("write rule file: %v", err)
	}
	sess := &APISession{ID: "test-sess", WorkDir: workDir}

	result := srv.cmdRule(sess, []string{"/rule"})
	if result == nil || result.Error {
		t.Fatalf("unexpected result: %#v", result)
	}
	if sess.RuleContent != "custom rule" {
		t.Fatalf("RuleContent = %q", sess.RuleContent)
	}
	data, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read rule file: %v", err)
	}
	if string(data) != "custom rule" {
		t.Fatalf("rule overwritten without force: %q", string(data))
	}

	result = srv.cmdRule(sess, []string{"/rule", "force"})
	if result == nil || result.Error {
		t.Fatalf("unexpected force result: %#v", result)
	}
	data, err = os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read forced rule file: %v", err)
	}
	if string(data) != sess.RuleContent || !strings.Contains(string(data), "Treat repository files") {
		t.Fatalf("force did not write default rule: %q", string(data))
	}
}

func TestCommands_CompactForcesSummaryOnlyWhenOnlyRecentContext(t *testing.T) {
	srv, _ := newRecordingAPIServer(t)
	sess := &APISession{ID: "test-sess", WorkDir: t.TempDir()}
	mgr := session.New(sess.WorkDir, t.TempDir())
	mgr.Init()
	mgr.AppendMessage(provider.NewUserMessage("hello"))
	mgr.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "hi"}}))
	sess.Manager = mgr

	result := srv.cmdCompact(sess)
	if result == nil {
		t.Fatal("expected result")
	}
	if sess.ForceCompact {
		t.Fatal("ForceCompact should not be set for immediate compaction")
	}
	if result.Error {
		t.Fatalf("unexpected error: %s", result.Message)
	}
	replay := mgr.GetReplayState()
	if len(replay.Messages) != 1 || !replay.Messages[0].SystemInjected {
		t.Fatalf("expected summary-only replay, got %#v", replay.Messages)
	}
}

// --- Tool format tests ---

func TestFormatToolExpanded_Read(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "read",
		Args:   map[string]any{"path": "main.go"},
		Status: "completed",
		Result: "package main\n\nfunc main() {}\n",
	}
	text := formatToolExpanded(tc)
	// Markdown header
	if !strings.Contains(text, "🔧 read: main.go") {
		t.Errorf("missing markdown header: %s", text)
	}
	// Code fence with language
	if !strings.Contains(text, "```go\n") {
		t.Errorf("missing go code fence: %s", text)
	}
	if !strings.Contains(text, "package main") {
		t.Errorf("missing result content: %s", text)
	}
	// Closing fence
	if !strings.Contains(text, "\n```") {
		t.Errorf("missing closing fence: %s", text)
	}
}

func TestFormatToolExpanded_Bash(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "bash",
		Args:   map[string]any{"command": "go test ./..."},
		Status: "completed",
		Result: "ok  pkg 0.5s\n",
	}
	text := formatToolExpanded(tc)
	if !strings.Contains(text, "🔧 bash: go test ./...") {
		t.Errorf("missing markdown header: %s", text)
	}
	if !strings.Contains(text, "```bash\n") {
		t.Errorf("missing bash code fence: %s", text)
	}
	if !strings.Contains(text, "ok  pkg") {
		t.Errorf("missing stdout: %s", text)
	}
}

func TestFormatToolExpanded_EditWithDiff(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "edit",
		Args:   map[string]any{"path": "main.go"},
		Status: "completed",
		Diff:   &tools.FileDiff{Path: "main.go", Added: 2, Deleted: 1, Unified: "+func new1() {}\n-func old() {}\n"},
	}
	text := formatToolExpanded(tc)
	if !strings.Contains(text, "```diff\n") {
		t.Errorf("missing diff code fence: %s", text)
	}
	if !strings.Contains(text, "+func new1") {
		t.Errorf("missing diff content: %s", text)
	}
}

func TestFormatToolExpanded_Error(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "bash",
		Args:   map[string]any{"command": "false"},
		Status: "failed",
		Error:  fmt.Errorf("exit code 1"),
	}
	text := formatToolExpanded(tc)
	if !strings.Contains(text, "Error: exit code 1") {
		t.Errorf("missing error: %s", text)
	}
}

func TestFormatToolExpanded_ReadJSON(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "read",
		Args:   map[string]any{"path": "package.json"},
		Status: "completed",
		Result: `{"name": "test"}`,
	}
	text := formatToolExpanded(tc)
	if !strings.Contains(text, "```json\n") {
		t.Errorf("should use json fence for .json file: %s", text)
	}
}

func TestFormatToolExpanded_GrepPlain(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "grep",
		Args:   map[string]any{"pattern": "TODO", "path": "./src"},
		Status: "completed",
		Result: "src/main.go:10: // TODO fix this\n",
	}
	text := formatToolExpanded(tc)
	// grep should use plain text fence (no language)
	if !strings.Contains(text, "```\n") {
		t.Errorf("grep should use plain code fence: %s", text)
	}
}

func TestFormatToolRunning(t *testing.T) {
	text := formatToolRunning("read", map[string]any{"path": "main.go"})
	if !strings.Contains(text, "\u23f3") {
		t.Errorf("missing hourglass: %s", text)
	}
	if !strings.Contains(text, "read") {
		t.Errorf("missing tool name: %s", text)
	}
}

func TestInferCodeLang(t *testing.T) {
	tests := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"bash", nil, "bash"},
		{"read", map[string]any{"path": "main.go"}, "go"},
		{"read", map[string]any{"path": "app.py"}, "python"},
		{"read", map[string]any{"path": "style.css"}, "css"},
		{"read", map[string]any{"path": "Makefile"}, "makefile"},
		{"read", map[string]any{"path": "Dockerfile"}, "dockerfile"},
		{"read", map[string]any{"path": "data.json"}, "json"},
		{"grep", map[string]any{"pattern": "x"}, ""},
		{"ls", nil, ""},
	}
	for _, tt := range tests {
		got := inferCodeLang(tt.tool, tt.args)
		if got != tt.want {
			t.Errorf("inferCodeLang(%q, %v) = %q, want %q", tt.tool, tt.args, got, tt.want)
		}
	}
}

func TestToolKeyArg(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"read path", "read", map[string]any{"path": "main.go"}, "main.go"},
		{"bash command", "bash", map[string]any{"command": "ls -la"}, "ls -la"},
		{"grep", "grep", map[string]any{"pattern": "TODO", "path": "src/"}, "TODO src/"},
		{"nil args", "read", nil, ""},
		{"unknown tool", "foo", map[string]any{"name": "bar"}, "bar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolKeyArg(tt.tool, tt.args)
			if got != tt.want {
				t.Errorf("toolKeyArg(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

// --- Collapsed mode tests ---

func TestFormatToolCollapsed_Read(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "read",
		Args:   map[string]any{"path": "main.go"},
		Status: "completed",
		Result: "package main\n\nfunc main() {}\n",
	}
	text := formatToolCollapsed(tc)
	if !strings.Contains(text, "read") {
		t.Errorf("missing tool name: %s", text)
	}
	if !strings.Contains(text, "main.go") {
		t.Errorf("missing path: %s", text)
	}
	if !strings.Contains(text, "✅") {
		t.Errorf("missing success marker: %s", text)
	}
	// Should NOT contain the file content
	if strings.Contains(text, "package main") {
		t.Errorf("collapsed should not contain file content: %s", text)
	}
	if strings.Contains(text, "```") {
		t.Errorf("collapsed should not contain code fences: %s", text)
	}
}

func TestFormatToolCollapsed_EditShowsDiff(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "edit",
		Args:   map[string]any{"path": "main.go"},
		Status: "completed",
		Diff:   &tools.FileDiff{Path: "main.go", Added: 1, Deleted: 1, Unified: "+new line\n-old line\n"},
	}
	text := formatToolCollapsed(tc)
	// edit with diff should always show the diff even in collapsed mode
	if !strings.Contains(text, "```diff") {
		t.Errorf("collapsed edit should show diff fence: %s", text)
	}
	if !strings.Contains(text, "+new line") {
		t.Errorf("collapsed edit should show diff content: %s", text)
	}
}

func TestFormatToolCollapsed_ErrorAlwaysShown(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "bash",
		Args:   map[string]any{"command": "false"},
		Status: "failed",
		Error:  fmt.Errorf("exit code 1"),
	}
	text := formatToolCollapsed(tc)
	if !strings.Contains(text, "Error: exit code 1") {
		t.Errorf("collapsed error should always show: %s", text)
	}
}

func TestFormatToolCollapsed_BashNoOutput(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "bash",
		Args:   map[string]any{"command": "go test ./..."},
		Status: "completed",
		Result: "ok  pkg 0.5s\n",
	}
	text := formatToolCollapsed(tc)
	if !strings.Contains(text, "✅") {
		t.Errorf("missing success marker: %s", text)
	}
	if strings.Contains(text, "ok  pkg") {
		t.Errorf("collapsed bash should not show stdout: %s", text)
	}
}

// --- Dispatcher test ---

func TestFormatToolResult_Dispatches(t *testing.T) {
	tc := &toolCallInfo{
		Name:   "read",
		Args:   map[string]any{"path": "main.go"},
		Status: "completed",
		Result: "package main\n",
	}

	collapsed := formatToolResult(tc, "collapsed")
	expanded := formatToolResult(tc, "expanded")

	if strings.Contains(collapsed, "```go") {
		t.Error("collapsed should not have code fence")
	}
	if !strings.Contains(expanded, "```go") {
		t.Error("expanded should have code fence")
	}
}

func TestToolVisibility_DefaultDetail(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.GetToolDetail() != "collapsed" {
		t.Errorf("default detail = %q, want collapsed", cfg.GetToolDetail())
	}
}

// --- CORS middleware disabled test ---

func TestCORSMiddleware_Disabled(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CORSMiddleware(CORSConfig{Enabled: false}, inner)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// CORS headers should NOT be set
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("CORS origin should be empty, got %q", got)
	}
}

func TestCORSMiddleware_DefaultOrigins(t *testing.T) {
	handler := CORSMiddleware(CORSConfig{Enabled: true}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q, want *", got)
	}
}

func TestAPISecurityWarning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Listen = ":8080"
	cfg.DefaultMode = "yolo"
	cfg.Auth.Enabled = false
	if got := apiSecurityWarning(cfg); got == "" {
		t.Fatal("expected warning for public yolo API without auth")
	}

	cfg.Listen = "127.0.0.1:8080"
	if got := apiSecurityWarning(cfg); got != "" {
		t.Fatalf("warning for loopback = %q, want empty", got)
	}

	cfg.Listen = ":8080"
	cfg.Auth.Enabled = true
	if got := apiSecurityWarning(cfg); got == "" {
		t.Fatal("expected warning when auth is enabled without tokens")
	}

	cfg.Auth.Tokens = []string{"sk-test"}
	if got := apiSecurityWarning(cfg); got != "" {
		t.Fatalf("warning with auth = %q, want empty", got)
	}
}

// --- Concurrency middleware at capacity test ---

func TestConcurrencyMiddleware_AtCapacity(t *testing.T) {
	blocking := make(chan struct{})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocking // block until released
		w.WriteHeader(http.StatusOK)
	})
	handler := ConcurrencyMiddleware(1, inner)

	// Fill the single slot
	go func() {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}()

	// Give goroutine time to start
	time.Sleep(20 * time.Millisecond)

	// Second request should be rejected
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w.Code)
	}

	// Release the blocking goroutine
	close(blocking)
}

// --- Auth with non-Bearer prefix ---

func TestAuthMiddleware_NonBearerPrefix(t *testing.T) {
	handler := AuthMiddleware(AuthConfig{Enabled: true, Tokens: []string{"sk-test"}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// --- extractBearerToken tests ---

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name string
		auth string
		want string
	}{
		{"empty", "", ""},
		{"bearer", "Bearer sk-test", "sk-test"},
		{"bearer with spaces", "Bearer  sk-test ", "sk-test"},
		{"basic", "Basic dXNlcjpwYXNz", ""},
		{"no prefix", "sk-test", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			got := extractBearerToken(req)
			if got != tt.want {
				t.Errorf("extractBearerToken(%q) = %q, want %q", tt.auth, got, tt.want)
			}
		})
	}
}

// --- SessionPool advanced tests ---

func TestSessionPool_ReplaceSameID(t *testing.T) {
	pool := NewSessionPool(1, 0)
	defer pool.Stop()

	sess1 := &APISession{ID: "sess-1", WorkDir: "/tmp/a", LastUsed: time.Now()}
	if err := pool.Put(sess1); err != nil {
		t.Fatalf("put 1: %v", err)
	}

	// Replace same ID within the same workDir should succeed even at max capacity.
	sess1v2 := &APISession{ID: "sess-1", WorkDir: "/tmp/a", LastUsed: time.Now()}
	if err := pool.Put(sess1v2); err != nil {
		t.Fatalf("replace same ID should succeed: %v", err)
	}

	got := pool.Get("sess-1")
	if got.WorkDir != "/tmp/a" {
		t.Errorf("workdir = %q, want /tmp/a", got.WorkDir)
	}
}

func TestSessionPool_EvictIdle(t *testing.T) {
	pool := NewSessionPool(0, 50*time.Millisecond)
	defer pool.Stop()

	sess := &APISession{ID: "sess-1", LastUsed: time.Now()}
	pool.Put(sess)
	// Manually backdate LastUsed after Put (which calls Touch)
	sess.LastUsed = time.Now().Add(-time.Hour)

	pool.evictIdle()

	if pool.Get("sess-1") != nil {
		t.Error("idle session should be evicted")
	}
}

func TestSessionPool_EvictIdleKeepsFresh(t *testing.T) {
	pool := NewSessionPool(0, time.Hour)
	defer pool.Stop()

	sess := &APISession{ID: "sess-1", LastUsed: time.Now()}
	pool.Put(sess)

	pool.evictIdle()

	if pool.Get("sess-1") == nil {
		t.Error("fresh session should not be evicted")
	}
}

func TestSessionPool_EvictIdleKeepsPinnedSession(t *testing.T) {
	pool := NewSessionPool(0, 50*time.Millisecond)
	defer pool.Stop()

	sess := &APISession{ID: "sess-1", LastUsed: time.Now()}
	if err := pool.Put(sess); err != nil {
		t.Fatalf("put session: %v", err)
	}
	sess.lastUsedMu.Lock()
	sess.LastUsed = time.Now().Add(-time.Hour)
	sess.lastUsedMu.Unlock()
	if !pool.Pin(sess) {
		t.Fatal("pin session")
	}
	defer pool.Unpin(sess)

	pool.evictIdle()
	if got := pool.Get("sess-1"); got != sess {
		t.Fatal("pinned session should not be evicted")
	}
}

func TestSessionPool_ConcurrentTouchAndInspection(t *testing.T) {
	pool := NewSessionPool(0, time.Hour)
	defer pool.Stop()
	sess := &APISession{ID: "sess-1"}
	if err := pool.Put(sess); err != nil {
		t.Fatalf("put session: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1_000; i++ {
			sess.Touch()
		}
	}()
	for i := 0; i < 1_000; i++ {
		_ = pool.listDetails()
		pool.evictIdle()
	}
	wg.Wait()
}

func TestPoolFullError_Error(t *testing.T) {
	e := &PoolFullError{Max: 5}
	if e.Error() != "session pool is at capacity" {
		t.Errorf("error = %q", e.Error())
	}
}

// --- parseMessages advanced tests ---

func TestParseMessages_MultipleSystem(t *testing.T) {
	msgs := []RequestMessage{
		{Role: "system", Content: "sys1"},
		{Role: "system", Content: "sys2"},
		{Role: "user", Content: "hello"},
	}
	lastUser, sysMsgs, history := parseMessages(msgs)
	if lastUser.Content != "hello" {
		t.Errorf("lastUser = %q", lastUser.Content)
	}
	if len(sysMsgs) != 2 {
		t.Errorf("sysMsgs len = %d, want 2", len(sysMsgs))
	}
	if len(history) != 0 {
		t.Errorf("history len = %d, want 0", len(history))
	}
}

func TestParseMessages_SingleUser(t *testing.T) {
	msgs := []RequestMessage{
		{Role: "user", Content: "only message"},
	}
	lastUser, sysMsgs, history := parseMessages(msgs)
	if lastUser.Content != "only message" {
		t.Errorf("lastUser = %q", lastUser.Content)
	}
	if len(sysMsgs) != 0 {
		t.Errorf("sysMsgs len = %d", len(sysMsgs))
	}
	if len(history) != 0 {
		t.Errorf("history len = %d", len(history))
	}
}

// --- convertHistoryMessages tests ---

func TestConvertHistoryMessages(t *testing.T) {
	msgs := []RequestMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "system", Content: "ignored"},
	}
	result := convertHistoryMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("result len = %d, want 2", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("result[0].Role = %q", result[0].Role)
	}
	if result[1].Role != "assistant" {
		t.Errorf("result[1].Role = %q", result[1].Role)
	}
}

func TestConvertHistoryMessages_Empty(t *testing.T) {
	result := convertHistoryMessages(nil)
	if len(result) != 0 {
		t.Errorf("result len = %d, want 0", len(result))
	}
}

// --- resolveToolEvent tests ---

func TestResolveToolEvent_FromTopLevel(t *testing.T) {
	ev := agent.Event{
		ToolName:   "read",
		ToolCallID: "call-1",
	}
	name, callID := resolveToolEvent(ev)
	if name != "read" {
		t.Errorf("name = %q", name)
	}
	if callID != "call-1" {
		t.Errorf("callID = %q", callID)
	}
}

func TestResolveToolEvent_FallbackToToolCall(t *testing.T) {
	ev := agent.Event{
		ToolCall: &provider.ToolCallBlock{
			ID:   "call-2",
			Name: "bash",
		},
	}
	name, callID := resolveToolEvent(ev)
	if name != "bash" {
		t.Errorf("name = %q", name)
	}
	if callID != "call-2" {
		t.Errorf("callID = %q", callID)
	}
}

func TestResolveToolEvent_TopLevelTakesPrecedence(t *testing.T) {
	ev := agent.Event{
		ToolName:   "read",
		ToolCallID: "call-1",
		ToolCall: &provider.ToolCallBlock{
			ID:   "call-2",
			Name: "bash",
		},
	}
	name, callID := resolveToolEvent(ev)
	if name != "read" {
		t.Errorf("name = %q, want read", name)
	}
	if callID != "call-1" {
		t.Errorf("callID = %q, want call-1", callID)
	}
}

// --- Commands: mode/model/sessions edge cases ---

func TestCommands_ModeInvalid(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdMode(nil, []string{"/mode", "invalid"})
	if !result.Error {
		t.Error("expected error for invalid mode")
	}
}

func TestCommands_ModeShowCurrent(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdMode(nil, []string{"/mode"})
	if result.Error {
		t.Error("unexpected error")
	}
	if !strings.Contains(result.Message, "YOLO") {
		t.Errorf("expected current mode YOLO, got %q", result.Message)
	}
}

func TestCommands_ModeShowSessionOverride(t *testing.T) {
	srv := newTestServer(t)
	sess := &APISession{ID: "s1", Mode: "plan"}
	result := srv.cmdMode(sess, []string{"/mode"})
	if !strings.Contains(result.Message, "PLAN") {
		t.Errorf("expected PLAN, got %q", result.Message)
	}
}

func TestCommands_ModelNotFound(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdModel([]string{"/model", "nonexistent"})
	if !result.Error {
		t.Error("expected error for unknown model")
	}
}

func TestCommands_ModelShowCurrent(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdModel([]string{"/model"})
	if result.Error {
		t.Error("unexpected error")
	}
	if !strings.Contains(result.Message, "Model 1") {
		t.Errorf("expected Model 1, got %q", result.Message)
	}
}

func TestCommands_SessionsList(t *testing.T) {
	srv := newTestServer(t)
	workDir := srv.cfg.GetWorkDir()
	srv.pool.Put(&APISession{ID: "s1", WorkDir: workDir, LastUsed: time.Now()})
	srv.pool.Put(&APISession{ID: "s2", WorkDir: workDir, LastUsed: time.Now()})

	result := srv.cmdSessions([]string{"/sessions"})
	if result.Error {
		t.Error("unexpected error")
	}
	if !strings.Contains(result.Message, "s1") || !strings.Contains(result.Message, "s2") {
		t.Errorf("expected both session IDs, got %q", result.Message)
	}
}

func TestCommands_SessionsEmpty(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSessions([]string{"/sessions"})
	if !strings.Contains(result.Message, "No active sessions") {
		t.Errorf("expected no sessions message, got %q", result.Message)
	}
}

func TestCommands_SessionsDelete(t *testing.T) {
	srv := newTestServer(t)
	current := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := current.Init(); err != nil {
		t.Fatalf("init current session: %v", err)
	}
	target := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := target.Init(); err != nil {
		t.Fatalf("init target session: %v", err)
	}
	result := srv.cmdSessionsForSession(&APISession{ID: current.GetHeader().ID, Manager: current}, []string{"/sessions", "del", target.GetHeader().ID[:8]})
	if result.Error {
		t.Error("unexpected error")
	}
	if sessions, err := session.ListForDir(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir()); err != nil {
		t.Fatalf("list sessions: %v", err)
	} else if len(sessions) != 1 {
		t.Fatalf("expected 1 session remaining, got %d", len(sessions))
	}
	if srv.pool.Get(target.GetHeader().ID) != nil {
		t.Error("deleted session should not remain in pool")
	}
}

func TestCommands_SessionsDeleteNotFound(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSessions([]string{"/sessions", "del", "nonexistent"})
	if !result.Error {
		t.Error("expected error for missing session")
	}
}

func TestCommands_SessionsDeleteMissingID(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSessions([]string{"/sessions", "del"})
	if !result.Error {
		t.Error("expected error for missing ID")
	}
}

func TestGetOrCreateSessionConcurrentDefaultReuse(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := srv.getOrCreateSession("", srv.cfg.GetWorkDir())
			if err != nil {
				errCh <- err
				return
			}
			if sess == nil || sess.ID == "" {
				errCh <- fmt.Errorf("missing session")
				return
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent getOrCreateSession failed: %v", err)
		}
	}
	if srv.pool.Count() != 1 {
		t.Fatalf("pool count = %d, want 1", srv.pool.Count())
	}
}

func TestCommands_SessionsUnknownSubcmd(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSessions([]string{"/sessions", "badcmd"})
	if !result.Error {
		t.Error("expected error for unknown subcmd")
	}
}

func TestCommands_StatusNoSession(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdStatus(nil)
	if !result.Error {
		t.Error("expected error for nil session")
	}
}

func TestCommands_SkillNoManager(t *testing.T) {
	srv := newTestServer(t)
	srv.skillsMgr = nil
	result := srv.cmdSkill(nil, []string{"/skill", "test"})
	if !result.Error {
		t.Error("expected error when no skills manager")
	}
}

func TestCommands_SkillNotFound(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSkill(nil, []string{"/skill", "nonexistent"})
	if !result.Error {
		t.Error("expected error for unknown skill")
	}
}

func TestSkillHubSessionActivationReloadsSkillContext(t *testing.T) {
	srv := newTestServer(t)
	workDir := srv.cfg.GetWorkDir()
	skillDir := filepath.Join(skills.ProjectSkillDirs(workDir)[0], "market-demo")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Market Demo\n\nUse the marketplace workflow."), 0644); err != nil {
		t.Fatal(err)
	}
	state, err := srv.RefreshSkillHubSession("skillhub-session", workDir, "market-demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID != "skillhub-session" || len(state.ActiveSkills) != 1 || state.ActiveSkills[0] != "market-demo" {
		t.Fatalf("session state = %#v", state)
	}
	sess := srv.pool.GetForWorkDir(workDir, "skillhub-session")
	if sess == nil || !strings.Contains(sess.ExtraContext, "## Active Skill: market-demo") || !strings.Contains(sess.ExtraContext, "Use the marketplace workflow.") {
		t.Fatalf("session context was not refreshed: %#v", sess)
	}
	if sess.Registry == nil || sess.SkillsMgr == nil || sess.SkillsMgr.Get("market-demo") == nil {
		t.Fatal("session skill resources were not refreshed")
	}
}

func TestCommandsSkillActivatesCurrentSession(t *testing.T) {
	srv := newTestServer(t)
	workDir := srv.cfg.GetWorkDir()
	skillDir := filepath.Join(skills.ProjectSkillDirs(workDir)[0], "command-demo")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Command Demo"), 0644); err != nil {
		t.Fatal(err)
	}
	sess, err := srv.getOrCreateSession("command-session", workDir)
	if err != nil {
		t.Fatal(err)
	}
	result := srv.cmdSkill(sess, []string{"/skill", "command-demo"})
	if result.Error || !sess.ActiveSkills["command-demo"] || !strings.Contains(sess.ExtraContext, "## Active Skill: command-demo") {
		t.Fatalf("activation result=%#v active=%#v context=%q", result, sess.ActiveSkills, sess.ExtraContext)
	}
}

func TestResolveSkillHubWorkDirUsesAllowlist(t *testing.T) {
	srv := newTestServer(t)
	allowedRoot := t.TempDir()
	allowedWorkDir := filepath.Join(allowedRoot, "project")
	if err := os.MkdirAll(allowedWorkDir, 0755); err != nil {
		t.Fatal(err)
	}
	allowed := []string{allowedRoot}
	srv.cfg.AllowedWorkDirs = &allowed
	if got, err := srv.ResolveSkillHubWorkDir("", allowedWorkDir); err != nil || got != allowedWorkDir {
		t.Fatalf("allowed workDir = %q, %v", got, err)
	}
	outside := t.TempDir()
	if _, err := srv.ResolveSkillHubWorkDir("", outside); err == nil {
		t.Fatal("outside workDir was accepted")
	}
}

func TestCommands_SkillsEmpty(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdSkills(nil)
	if !strings.Contains(result.Message, "No skills found") {
		t.Errorf("expected no skills message, got %q", result.Message)
	}
}

func TestAPISessionCreatesAndActivatesWorkflowSkillForWorkDir(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	srv.cfg.EnableWorkflows = true

	sess, err := srv.getOrCreateSession("workflow-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if sess == nil {
		t.Fatal("expected session")
	}
	skillPath := filepath.Join(workDir, ".skills", workflow.SkillName, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected workflow skill at %s: %v", skillPath, err)
	}
	if sess.SkillsMgr == nil || sess.SkillsMgr.Get(workflow.SkillName) == nil {
		t.Fatal("expected session skills manager to load workflow skill")
	}
	if !strings.Contains(sess.ExtraContext, "## Active Skill: "+workflow.SkillName) {
		t.Fatalf("expected workflow skill to be active in session context")
	}
}

func TestAPISessionUsesBuiltInBrowserSkillWithoutProjectWrite(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	srv.cfg.EnableBrowser = true

	sess, err := srv.getOrCreateSession("browser-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if sess == nil {
		t.Fatal("expected session")
	}
	projectSkillsDir := filepath.Join(workDir, ".skills")
	if _, err := os.Stat(projectSkillsDir); !os.IsNotExist(err) {
		t.Fatalf("browser session created project skills directory %s: %v", projectSkillsDir, err)
	}
	if sess.SkillsMgr == nil || sess.SkillsMgr.Get(browserfeature.SkillName) == nil || sess.SkillsMgr.Get(browserfeature.SkillName).Source != "builtin" {
		t.Fatal("expected session skills manager to load browser skill")
	}
	if !strings.Contains(sess.ExtraContext, "## Active Skill: "+browserfeature.SkillName) {
		t.Fatalf("expected browser skill to be active in session context")
	}
	if _, ok := sess.Registry.Get(browserfeature.ToolName); !ok {
		t.Fatal("expected browser tool to be registered")
	}
}

func TestAPISessionAppliesPerSessionToolOptions(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer os.Chdir(oldCwd)
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".mothx"), 0755); err != nil {
		t.Fatalf("mkdir .mothx: %v", err)
	}
	a2aList := `{"agents":[{"name":"reviewer","url":"http://localhost:8093"}]}`
	if err := os.WriteFile(filepath.Join(workDir, ".mothx", "a2a-list.json"), []byte(a2aList), 0644); err != nil {
		t.Fatalf("write a2a list: %v", err)
	}

	sess, err := srv.getOrCreateSession("tool-options-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}

	enabled := true
	if err := srv.applySessionToolOptions(sess, &SessionToolOptions{
		WebSearch:  &enabled,
		Browser:    &enabled,
		A2AMaster:  &enabled,
		Delegate:   &enabled,
		MultiAgent: &enabled,
		Workflows:  &enabled,
	}, ""); err != nil {
		t.Fatalf("apply enable options: %v", err)
	}
	for _, name := range []string{"browser", "a2a_dispatch", "delegate_subagent", "subagent_spawn", "workflow_run"} {
		if _, ok := sess.Registry.Get(name); !ok {
			t.Fatalf("expected %s tool to be registered", name)
		}
	}
	if !srv.settingsForSession(sess).IsWebSearchEnabled() {
		t.Fatal("expected per-session web search to be enabled")
	}

	disabled := false
	if err := srv.applySessionToolOptions(sess, &SessionToolOptions{
		WebSearch:  &disabled,
		Browser:    &disabled,
		A2AMaster:  &disabled,
		Delegate:   &disabled,
		MultiAgent: &disabled,
		Workflows:  &disabled,
	}, ""); err != nil {
		t.Fatalf("apply disable options: %v", err)
	}
	for _, name := range []string{"browser", "a2a_dispatch", "delegate_subagent", "subagent_spawn", "workflow_run"} {
		if _, ok := sess.Registry.Get(name); ok {
			t.Fatalf("expected %s tool to be removed", name)
		}
	}
	if srv.settingsForSession(sess).IsWebSearchEnabled() {
		t.Fatal("expected per-session web search to be disabled")
	}
}

func TestPatchSessionRuntimeModeDoesNotSynchronizeTools(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	mgr := session.New(workDir, srv.settings.GetSessionDir())
	if err := mgr.InitWithID("mode-only-sess"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sess, err := srv.getOrCreateSession("mode-only-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	bashBefore, bashExists := sess.Registry.Get("bash")
	if !bashExists {
		t.Fatal("expected baseline bash tool")
	}
	mode := "agent"
	updated, err := srv.PatchSessionRuntime("mode-only-sess", SessionRuntimePatch{Mode: &mode})
	if err != nil {
		t.Fatalf("PatchSessionRuntime mode-only: %v", err)
	}
	if updated.Mode != mode || sess.Mode != mode {
		t.Fatalf("mode was not updated: runtime=%q session=%q", updated.Mode, sess.Mode)
	}
	bashAfter, bashExists := sess.Registry.Get("bash")
	if !bashExists || bashAfter != bashBefore {
		t.Fatal("mode-only patch must not re-register session tools")
	}
}

func TestPatchSessionRuntimeDisplayModePersistsAndReloads(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	mgr := session.New(workDir, srv.settings.GetSessionDir())
	if err := mgr.InitWithID("display-mode-sess"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	mode := "code"
	updated, err := srv.PatchSessionRuntime("display-mode-sess", SessionRuntimePatch{DisplayMode: &mode})
	if err != nil {
		t.Fatalf("patch display mode: %v", err)
	}
	if updated.DisplayMode != "code" {
		t.Fatalf("patched display mode = %q, want code", updated.DisplayMode)
	}
	loaded, err := srv.GetSessionRuntime("display-mode-sess")
	if err != nil {
		t.Fatalf("reload runtime: %v", err)
	}
	if loaded.DisplayMode != "code" {
		t.Fatalf("reloaded display mode = %q, want code", loaded.DisplayMode)
	}
}

func TestSessionCapabilitiesGetAndPatch(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()
	mgr := session.New(workDir, srv.settings.GetSessionDir())
	if err := mgr.InitWithID("caps-sess"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	initial, err := srv.GetSessionCapabilities("caps-sess")
	if err != nil {
		t.Fatalf("GetSessionCapabilities: %v", err)
	}
	if initial.Active || !initial.Persisted || initial.WorkDir != workDir {
		t.Fatalf("initial capabilities = %#v", initial)
	}

	mode := "agent"
	enabled := true
	updated, err := srv.PatchSessionCapabilities("caps-sess", SessionCapabilityPatch{
		Mode:         &mode,
		WebSearch:    &enabled,
		Browser:      &enabled,
		DelegateMode: &enabled,
		MultiAgent:   &enabled,
		Workflows:    &enabled,
	})
	if err != nil {
		t.Fatalf("PatchSessionCapabilities enable: %v", err)
	}
	if !updated.Active || updated.Mode != "agent" || !updated.Browser || !updated.DelegateMode || !updated.MultiAgent || !updated.Workflows || !updated.WebSearch {
		t.Fatalf("updated capabilities = %#v", updated)
	}
	sess := srv.pool.GetForWorkDir(workDir, "caps-sess")
	if sess == nil {
		t.Fatal("expected patched session to be active")
	}
	for _, name := range []string{"browser", "delegate_subagent", "subagent_spawn", "workflow_run"} {
		if _, ok := sess.Registry.Get(name); !ok {
			t.Fatalf("expected %s tool to be registered", name)
		}
	}
	if !srv.settingsForSession(sess).IsWebSearchEnabled() {
		t.Fatal("expected web search to be enabled for session")
	}

	disabled := false
	if _, err := srv.PatchSessionCapabilities("caps-sess", SessionCapabilityPatch{
		Browser:      &disabled,
		DelegateMode: &disabled,
		MultiAgent:   &disabled,
		Workflows:    &disabled,
		WebSearch:    &disabled,
	}); err != nil {
		t.Fatalf("PatchSessionCapabilities disable: %v", err)
	}
	for _, name := range []string{"browser", "delegate_subagent", "subagent_spawn", "workflow_run"} {
		if _, ok := sess.Registry.Get(name); ok {
			t.Fatalf("expected %s tool to be removed", name)
		}
	}
	if srv.settingsForSession(sess).IsWebSearchEnabled() {
		t.Fatal("expected web search to be disabled for session")
	}

	reEnabled := true
	if _, err := srv.PatchSessionCapabilities("caps-sess", SessionCapabilityPatch{
		Browser:      &reEnabled,
		DelegateMode: &reEnabled,
		MultiAgent:   &reEnabled,
		WebSearch:    &reEnabled,
	}); err != nil {
		t.Fatalf("PatchSessionCapabilities re-enable: %v", err)
	}
	srv.pool.RemoveByWorkDir(workDir, "caps-sess")
	persisted, err := srv.GetSessionCapabilities("caps-sess")
	if err != nil {
		t.Fatalf("GetSessionCapabilities after pool remove: %v", err)
	}
	if persisted.Active || persisted.RuntimeOnly || !persisted.Browser || !persisted.DelegateMode || !persisted.MultiAgent || !persisted.WebSearch {
		t.Fatalf("persisted capabilities = %#v", persisted)
	}
	restored, err := srv.getOrCreateSession("caps-sess", workDir)
	if err != nil {
		t.Fatalf("restore session: %v", err)
	}
	for _, name := range []string{"browser", "delegate_subagent", "subagent_spawn"} {
		if _, ok := restored.Registry.Get(name); !ok {
			t.Fatalf("expected restored %s tool to be registered", name)
		}
	}
}

func TestUsageEventDataIncludesCacheTokens(t *testing.T) {
	data := usageEventData(CompletionUsage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		CacheReadTokens:  75,
		CacheWriteTokens: 10,
	}, "")
	usage, ok := data["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage data = %#v", data["usage"])
	}
	if usage["cache_read_tokens"] != 75 || usage["cache_write_tokens"] != 10 {
		t.Fatalf("cache usage = %#v", usage)
	}
}

func TestWithContextUsageEventData(t *testing.T) {
	data := withContextUsageEventData(map[string]any{}, &ctxpkg.ContextUsage{
		TotalTokens:   25000,
		ContextWindow: 100000,
	})
	usage, ok := data["contextUsage"].(ctxpkg.ContextUsage)
	if !ok {
		t.Fatalf("context usage data = %#v", data["contextUsage"])
	}
	if usage.TotalTokens != 25000 || usage.ContextWindow != 100000 {
		t.Fatalf("context usage = %#v", usage)
	}
}

func TestCommands_Help(t *testing.T) {
	srv := newTestServer(t)
	result := srv.cmdHelp()
	for _, cmd := range []string{"/clear", "/mode", "/model", "/compact", "/workflows", "/help"} {
		if !strings.Contains(result.Message, cmd) {
			t.Errorf("help missing %s", cmd)
		}
	}
}

func TestCommands_WorkflowsCancelActiveRun(t *testing.T) {
	srv := newTestServer(t)
	active := workflow.DefaultActiveRegistry()
	canceled := false
	if err := active.Register("API-run", func() { canceled = true }); err != nil {
		t.Fatalf("register active workflow: %v", err)
	}
	defer active.Unregister("API-run")

	result := srv.cmdWorkflows([]string{"/workflows", "cancel", "API-run"})
	if result.Error {
		t.Fatalf("expected cancel success, got %q", result.Message)
	}
	if !canceled {
		t.Fatal("expected cancel function to be called")
	}
}

// --- Chat handler method-not-allowed test ---

func TestChatHandler_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	srv.handleChatCompletions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// --- Type helper tests ---

func TestNewCompletionID(t *testing.T) {
	id := newCompletionID()
	if !strings.HasPrefix(id, "chatcmpl-") {
		t.Errorf("id = %q, want chatcmpl- prefix", id)
	}
}

func TestNewCommandCompletionID(t *testing.T) {
	id := newCommandCompletionID()
	if !strings.HasPrefix(id, "chatcmpl-cmd-") {
		t.Errorf("id = %q, want chatcmpl-cmd- prefix", id)
	}
}

func TestStringPtr(t *testing.T) {
	p := stringPtr("test")
	if *p != "test" {
		t.Errorf("*p = %q", *p)
	}
}

func TestMarshalJSON(t *testing.T) {
	data := marshalJSON(map[string]string{"key": "val"})
	if !strings.Contains(string(data), "key") {
		t.Errorf("data = %s", data)
	}
}

// --- langFromPath extended tests ---

func TestLangFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"app.py", "python"},
		{"index.js", "javascript"},
		{"app.ts", "typescript"},
		{"comp.tsx", "tsx"},
		{"comp.jsx", "jsx"},
		{"main.rs", "rust"},
		{"app.rb", "ruby"},
		{"Main.java", "java"},
		{"main.c", "c"},
		{"main.h", "c"},
		{"main.cpp", "cpp"},
		{"main.cc", "cpp"},
		{"main.cs", "csharp"},
		{"main.swift", "swift"},
		{"main.kt", "kotlin"},
		{"script.sh", "bash"},
		{"script.bash", "bash"},
		{"script.zsh", "zsh"},
		{"script.ps1", "powershell"},
		{"query.sql", "sql"},
		{"index.html", "html"},
		{"style.css", "css"},
		{"style.scss", "scss"},
		{"data.json", "json"},
		{"config.yaml", "yaml"},
		{"config.yml", "yaml"},
		{"config.toml", "toml"},
		{"data.xml", "xml"},
		{"README.md", "markdown"},
		{"main.tf", "hcl"},
		{"main.lua", "lua"},
		{"main.php", "php"},
		{"main.pl", "perl"},
		{"main.ex", "elixir"},
		{"main.erl", "erlang"},
		{"main.hs", "haskell"},
		{"main.scala", "scala"},
		{"main.clj", "clojure"},
		{"main.vim", "vim"},
		{"schema.proto", "protobuf"},
		{"schema.graphql", "graphql"},
		{"config.ini", "ini"},
		{".env", "bash"},
		{"Makefile", "makefile"},
		{"Dockerfile", "dockerfile"},
		{"Gemfile", "ruby"},
		{"unknown.xyz", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := langFromPath(tt.path)
			if got != tt.want {
				t.Errorf("langFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- formatToolHeaderMD tests ---

func TestFormatToolHeaderMD(t *testing.T) {
	got := formatToolHeaderMD("read", map[string]any{"path": "main.go"})
	if got != "🔧 read: main.go" {
		t.Errorf("got %q", got)
	}
	got2 := formatToolHeaderMD("plan", nil)
	if got2 != "🔧 plan" {
		t.Errorf("got %q", got2)
	}
}

// --- formatToolHeader tests ---

func TestFormatToolHeader(t *testing.T) {
	got := formatToolHeader("bash", map[string]any{"command": "ls"})
	if got != "🔧 [bash] ls" {
		t.Errorf("got %q", got)
	}
	got2 := formatToolHeader("plan", nil)
	if got2 != "🔧 [plan]" {
		t.Errorf("got %q", got2)
	}
}

// --- toolKeyArg: bash long command truncation ---

func TestToolKeyArg_BashLongCommand(t *testing.T) {
	longCmd := strings.Repeat("a", 200)
	got := toolKeyArg("bash", map[string]any{"command": longCmd})
	if len(got) > 124 { // 120 + "..."
		t.Errorf("expected truncated, got len %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected ... suffix")
	}
}

// --- APISession Touch/Lock ---

func TestAPISession_Touch(t *testing.T) {
	sess := &APISession{ID: "s1"}
	sess.Touch()
	if sess.LastUsed.IsZero() {
		t.Error("expected non-zero LastUsed after Touch")
	}
}

func TestAPISession_LockUnlock(t *testing.T) {
	sess := &APISession{ID: "s1"}
	sess.Lock()
	sess.Unlock()
	// No panic = pass
}

// --- Default session ID tests ---

// TestRefreshSessionContextReRegistersSubAgentTools verifies that when
// refreshSessionContext replaces sess.AgentMgr (e.g. via setActiveSkillsLocked
// which the WebUI triggers on run submission when it sends skills),
// the sub-agent/delegate/workflow tools are re-registered with the new manager.
// Without this, tools keep referencing the old manager while the parent agent
// is registered into the new one, causing "parent agent not found" errors.
func TestRefreshSessionContextReRegistersSubAgentTools(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	workDir := t.TempDir()
	sess, err := srv.getOrCreateSession("refresh-mgr-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	sess.Lock()
	defer sess.Unlock()

	enabled := true
	if err := srv.applySessionToolOptions(sess, &SessionToolOptions{
		Delegate:   &enabled,
		MultiAgent: &enabled,
		Workflows:  &enabled,
	}, ""); err != nil {
		t.Fatalf("applySessionToolOptions: %v", err)
	}

	mgrBefore := sess.AgentMgr
	if mgrBefore == nil {
		t.Fatal("expected AgentMgr to be created after enabling delegate/multiAgent/workflows")
	}

	// Simulate the WebUI sending an empty skills array (which triggers
	// setActiveSkillsLocked → refreshSessionContext).
	if err := srv.setActiveSkillsLocked(sess, []string{}); err != nil {
		t.Fatalf("setActiveSkillsLocked: %v", err)
	}

	mgrAfter := sess.AgentMgr
	if mgrAfter == nil {
		t.Fatal("expected AgentMgr to still be non-nil after refreshSessionContext")
	}
	if mgrAfter == mgrBefore {
		t.Fatal("expected AgentMgr to be replaced by refreshSessionContext")
	}

	// The delegate_subagent and subagent_spawn tools must still be registered.
	// The fix in refreshSessionContext re-registers them with the new manager.
	for _, name := range []string{"delegate_subagent", "subagent_spawn", "subagent_status", "workflow_run"} {
		if _, ok := sess.Registry.Get(name); !ok {
			t.Fatalf("expected %s tool to be registered after refreshSessionContext", name)
		}
	}

	// Verify that a parent agent registered into the new manager is findable.
	// This is the core invariant: the tools must reference sess.AgentMgr so
	// that AgentManager.Get(parentID) succeeds when a sub-agent is spawned.
	testAgent := agent.NewAgentAdapter(agent.New(agent.Config{
		ID:        "agent-test-refresh",
		Mode:      "yolo",
		Provider:  srv.provider,
		Model:     srv.model,
		Settings:  srv.settings,
		Session:   sess.Manager,
		Workflows: true,
	}, sess.Registry))
	mgrAfter.Register(testAgent)
	if _, ok := mgrAfter.Get(testAgent.ID()); !ok {
		t.Fatal("expected parent agent to be findable in the new AgentMgr after Register")
	}
	// The old manager should NOT have the parent agent.
	if _, ok := mgrBefore.Get(testAgent.ID()); ok {
		t.Fatal("parent agent should not be in the old AgentMgr")
	}
}

func TestIsWebSearchAvailableFromSettings(t *testing.T) {
	srv := newTestServer(t)

	// Default: serve config off, settings off → not available.
	if srv.IsWebSearchAvailable() {
		t.Fatal("expected web search unavailable by default")
	}
	if srv.runtimeCapabilityAvailable("webSearch") {
		t.Fatal("expected runtimeCapabilityAvailable(webSearch) = false by default")
	}

	// Enable via settings.json (app-level) without serve config flag.
	srv.settings.WebSearch.Enabled = config.BoolPtr(true)
	if !srv.IsWebSearchAvailable() {
		t.Fatal("expected web search available when settings.json enables it")
	}
	if !srv.runtimeCapabilityAvailable("webSearch") {
		t.Fatal("expected runtimeCapabilityAvailable(webSearch) = true when settings enable it")
	}

	// Default session capabilities should reflect settings-based availability.
	caps := srv.defaultSessionCapabilities("", false, false)
	if !caps.WebSearch {
		t.Fatal("expected default session capabilities to have webSearch = true when settings enable it")
	}

	// Serve config flag should also make it available (independent of settings).
	srv.settings.WebSearch.Enabled = config.BoolPtr(false)
	srv.cfg.EnableWebSearch = true
	if !srv.IsWebSearchAvailable() {
		t.Fatal("expected web search available when serve config enables it")
	}
}

func TestCapabilityOverviewReportsResponsesModelProfile(t *testing.T) {
	srv := newTestServer(t)
	falseValue := false
	model := &provider.Model{ID: "responses-model", Compat: &provider.ModelCompat{
		SupportsBackground:         &falseValue,
		SupportsConversation:       &falseValue,
		SupportsPreviousResponseID: &falseValue,
		SupportedInclude:           []string{"reasoning.encrypted_content"},
	}}
	p := openaiprovider.NewProviderWithModels("key", "https://api.test/v1", []*provider.Model{model})
	p.SetUseResponsesAPI(true)
	srv.provider = p
	srv.model = model
	overview := srv.CapabilityOverview()
	if overview.Responses == nil {
		t.Fatal("Responses capability report is missing")
	}
	if overview.Responses.ModelID != model.ID || overview.Responses.SupportsBackground || overview.Responses.SupportsConversation || overview.Responses.SupportsPreviousResponse {
		t.Fatalf("Responses capability report = %#v", overview.Responses)
	}
	if overview.Responses.Provider != "openai" || overview.Responses.API != "openai-responses" {
		t.Fatalf("Responses provider profile = %#v", overview.Responses)
	}
	if !overview.Responses.SupportsAttachmentDownload {
		t.Fatal("Responses capability report should expose OpenAI attachment download")
	}
	if len(overview.Responses.SupportedInclude) != 1 || overview.Responses.SupportedInclude[0] != "reasoning.encrypted_content" {
		t.Fatalf("supported include = %#v", overview.Responses.SupportedInclude)
	}
	if len(overview.Responses.SupportedEvents) == 0 || len(overview.Responses.SupportedItems) == 0 || len(overview.Responses.AttachmentKinds) == 0 {
		t.Fatalf("codec capability lists missing: %#v", overview.Responses)
	}
	if len(overview.Responses.SupportedAnnotations) != 3 {
		t.Fatalf("supported annotations = %#v", overview.Responses.SupportedAnnotations)
	}
	if !overview.Responses.HostedTools["web_search"] || !overview.Responses.HostedTools["code_interpreter"] {
		t.Fatalf("known hosted capabilities missing: %#v", overview.Responses.HostedTools)
	}
	if !overview.Responses.HostedPolicies["code_interpreter"] {
		t.Fatalf("hosted policy capabilities missing: %#v", overview.Responses.HostedPolicies)
	}
	if !overview.AttachmentDownload {
		t.Fatal("provider-neutral attachment capability should be available")
	}
	overview.Responses.SupportedEvents[0] = "mutated-by-caller"
	if next := srv.CapabilityOverview(); next.Responses == nil || next.Responses.SupportedEvents[0] == "mutated-by-caller" {
		t.Fatal("capability report exposed mutable global codec state")
	}
	srv.provider = provider.NewMockProvider("anthropic", []*provider.Model{model}, nil)
	if generic := srv.CapabilityOverview(); generic.Responses != nil {
		t.Fatalf("non-Responses provider leaked capability report: %#v", generic.Responses)
	}
	if generic := srv.CapabilityOverview(); generic.AttachmentDownload {
		t.Fatal("provider without an attachment resolver advertised downloads")
	}
}

func TestGetOrCreateSessionWebSearchFromSettings(t *testing.T) {
	srv := newTestServer(t)
	workDir := t.TempDir()

	// Enable web search via settings.json only (serve config flag off).
	srv.settings.WebSearch.Enabled = config.BoolPtr(true)

	sess, err := srv.getOrCreateSession("ws-settings-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if !sess.WebSearch {
		t.Fatal("expected session WebSearch = true when settings.json enables it")
	}
	if !srv.settingsForSession(sess).IsWebSearchEnabled() {
		t.Fatal("expected settingsForSession to report web search enabled")
	}

	// Disable settings, enable via serve config only.
	srv.settings.WebSearch.Enabled = config.BoolPtr(false)
	srv.cfg.EnableWebSearch = true

	sess2, err := srv.getOrCreateSession("ws-serve-sess", workDir)
	if err != nil {
		t.Fatalf("getOrCreateSession() error = %v", err)
	}
	if !sess2.WebSearch {
		t.Fatal("expected session WebSearch = true when serve config enables it")
	}
}
func TestEventBroker_SubscribeThenPublishReceivedWithCorrectSeq(t *testing.T) {
	broker := NewEventBroker()
	events, cancel := broker.Subscribe("sess-1")
	defer cancel()

	// Publish should deliver the event with an incremental seq.
	broker.PublishToolEvent("sess-1", "run-1", map[string]any{"name": "test"})

	select {
	case ev := <-events:
		if ev.Seq != 1 {
			t.Fatalf("expected seq 1, got %d", ev.Seq)
		}
		if ev.Stream != "tool" || ev.Event != "tool_event" {
			t.Fatalf("unexpected event %s/%s", ev.Stream, ev.Event)
		}
	default:
		t.Fatal("expected event from broker")
	}
}

func TestEventBroker_ReplayBoundaryDeduplicatesSubscriber(t *testing.T) {
	broker := NewEventBroker()

	// Publish 5 events.
	for i := 0; i < 5; i++ {
		broker.PublishTranscriptEvent("sess-1", "run-1", map[string]any{"idx": i})
	}

	// Replay boundary is the seq of the last persisted event.
	boundary := broker.CurrentSeq("sess-1")
	if boundary != 5 {
		t.Fatalf("expected boundary 5, got %d", boundary)
	}

	// Subscribe after the events were published.
	events, cancel := broker.Subscribe("sess-1")
	defer cancel()

	// Publish one more event.
	broker.PublishTranscriptEvent("sess-1", "run-1", map[string]any{"idx": 5})

	// The forward loop should skip events with seq <= boundary and only deliver seq=6.
	received := false
Loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break Loop
			}
			if ev.Seq <= boundary {
				t.Errorf("received event with seq %d <= boundary %d — should have been skipped", ev.Seq, boundary)
			}
			if ev.Seq == 6 {
				received = true
			}
		default:
			break Loop
		}
	}
	if !received {
		t.Fatal("expected to receive event with seq 6")
	}
}

func TestServer_FinalizeRunIsIdempotent(t *testing.T) {
	cwd := t.TempDir()
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(cwd, "sessions")

	completionCalls := 0
	srv := &Server{
		cfg:        &Config{DefaultMode: "yolo"},
		settings:   settings,
		streamHub:  newSessionStreamHub(),
		runManager: NewRunManager(settings.GetSessionDir()),
		pool:       NewSessionPool(0, 0),
		runComplete: func(sessionID, runID, status, errMsg string) {
			completionCalls++
		},
	}
	srv.eventBroker = NewEventBroker()

	runID := "finalize-run-1"
	sess := &APISession{
		ID:      "sess-finalize-1",
		WorkDir: cwd,
		Manager: session.New(cwd, settings.GetSessionDir()),
	}
	if err := sess.Manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sess.beginRun(runID)

	// Create the run in the RunManager first (as the handler does).
	if err := srv.runManager.Create(session.SessionRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir,
		Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// First call: should succeed.
	srv.FinalizeRun(sess, runID, "completed", "")

	// Second call: should be a no-op because of sync.Once.
	// This must not panic, duplicate events, or corrupt state.
	srv.FinalizeRun(sess, runID, "completed", "")

	// Verify the run is now terminal in the DB.
	run, err := srv.runManager.Get(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run == nil {
		t.Fatal("run not found after finalization")
	}
	if run.Status != "completed" {
		t.Fatalf("expected status completed, got %q", run.Status)
	}
	if run.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
	if completionCalls != 1 {
		t.Fatalf("expected one completion callback, got %d", completionCalls)
	}
}

func TestSessionRunEventPersistenceAndReplayWithCursor(t *testing.T) {
	sessionDir := t.TempDir()

	// Save some run events.
	for i := 0; i < 3; i++ {
		_, err := session.SaveSessionRunEvent(sessionDir, session.SessionRunEvent{
			SessionID: "replay-sess",
			RunID:     "replay-run",
			EventType: "tool_event",
			Source:    "test",
			Status:    "running",
		})
		if err != nil {
			t.Fatalf("save run event %d: %v", i, err)
		}
	}

	// List events after cursor.
	events, err := session.ListSessionRunEventsAfter(sessionDir, "replay-sess", 0, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d: expected seq %d, got %d", i, i+1, e.Seq)
		}
	}

	// List after cursor 1.
	events, err = session.ListSessionRunEventsAfter(sessionDir, "replay-sess", 1, 10)
	if err != nil {
		t.Fatalf("list events after seq 1: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events after seq 1, got %d", len(events))
	}
}

func TestPublishExternalSessionUpdatePublishesTranscriptAndFailure(t *testing.T) {
	srv := newTestServer(t)
	srv.streamHub = newSessionStreamHub()
	srv.eventBroker = NewEventBroker()
	workDir := t.TempDir()
	mgr := session.New(workDir, srv.settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := mgr.GetHeader().ID
	if _, err := mgr.AppendMessage(provider.NewUserMessage("继续执行")); err != nil {
		t.Fatalf("append message: %v", err)
	}
	runID := "channel-test-run"
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sessionID, WorkDir: workDir, Source: "channel:wechat",
		Status: "failed", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save run: %v", err)
	}
	if _, err := session.SaveSessionRunEvent(srv.settings.GetSessionDir(), session.SessionRunEvent{
		SessionID: sessionID, RunID: runID, EventType: "failed", Source: "channel:wechat",
		Status: "failed", Data: json.RawMessage(`{"error":"upstream returned HTTP 522"}`),
	}); err != nil {
		t.Fatalf("save run event: %v", err)
	}

	events, cancel := srv.eventBroker.Subscribe(sessionID)
	defer cancel()
	srv.PublishExternalSessionUpdate(sessionID)
	var gotTranscript, gotFailure bool
	deadline := time.After(time.Second)
	for !gotTranscript || !gotFailure {
		select {
		case ev := <-events:
			switch ev.Stream {
			case "transcript":
				gotTranscript = true
			case "run":
				entry, ok := ev.Data.(SessionRunEventEntry)
				if ok && entry.Status == "failed" && entry.Data["error"] == "upstream returned HTTP 522" {
					gotFailure = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for external session events")
		}
	}
	if !gotTranscript || !gotFailure {
		t.Fatalf("external update events: transcript=%v failure=%v", gotTranscript, gotFailure)
	}
}

func TestRunExecutor_ProcessesEventTypes(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	runID := "exec-test-run"
	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: runID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	// Create a minimal agent config with a provider that won't be called.
	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	// Build a channel with various event types.
	eventCh := make(chan agent.Event, 10)
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: "hello"}
	eventCh <- agent.Event{Type: agent.EventToolCall, ToolName: "read", ToolArgs: map[string]any{"path": "/tmp/test"}, ToolCallID: "call-1"}
	eventCh <- agent.Event{Type: agent.EventToolExecutionEnd, ToolName: "read", ToolCallID: "call-1", ToolResult: "file content"}
	eventCh <- agent.Event{Type: agent.EventUsage, Usage: &provider.Usage{Input: 10, Output: 5}}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)

	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result == nil {
		t.Fatal("Execute() returned nil result")
	}
	if result.Status != "completed" {
		t.Fatalf("expected status completed, got %q", result.Status)
	}
	if result.Usage == nil {
		t.Fatal("expected usage to be set")
	}
	if result.Usage.PromptTokens != 10 {
		t.Fatalf("expected 10 prompt tokens, got %d", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 5 {
		t.Fatalf("expected 5 completion tokens, got %d", result.Usage.CompletionTokens)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "read" {
		t.Fatalf("expected tool name 'read', got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Status != "completed" {
		t.Fatalf("expected tool status 'completed', got %q", result.ToolCalls[0].Status)
	}
}

func TestRunExecutor_TextDeltaPublishedAsAssistantDelta(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "delta-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	events, unsubscribe := srv.eventBroker.Subscribe("sess-1")
	defer unsubscribe()

	eventCh := make(chan agent.Event, 3)
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: "hello"}
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: " world"}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)

	if _, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var deltas []string
	deadline := time.After(2 * time.Second)
	for len(deltas) < 2 {
		select {
		case ev, ok := <-events:
			if !ok || ev.Event != "transcript" {
				if !ok {
					t.Fatalf("subscription closed before receiving deltas, got %d", len(deltas))
				}
				continue
			}
			data, err := json.Marshal(ev.Data)
			if err != nil {
				t.Fatalf("marshal event data: %v", err)
			}
			var evt TranscriptStreamEvent
			if err := json.Unmarshal(data, &evt); err != nil {
				t.Fatalf("unmarshal transcript event: %v", err)
			}
			if evt.Type != "assistant_delta" {
				t.Fatalf("transcript event type = %q, want assistant_delta", evt.Type)
			}
			if evt.Message == nil || evt.Message.Role != "assistant" {
				t.Fatalf("transcript message = %#v, want assistant role", evt.Message)
			}
			deltas = append(deltas, evt.Message.Content)
		case <-deadline:
			t.Fatalf("timed out waiting for transcript deltas, got %d", len(deltas))
		}
	}
	if deltas[0] != "hello" || deltas[1] != " world" {
		t.Fatalf("deltas = %#v, want [hello, ' world']", deltas)
	}
}

func TestRunExecutorFinalizeDefersDurableDone(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()
	sess := &APISession{ID: "durable-finalize-session", WorkDir: "/tmp/test"}
	runID := "durable-finalize-run"
	sess.markDurableRun(runID)
	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir, Status: "running", StartedAt: time.Now(),
	})
	events, cancel := srv.eventBroker.Subscribe(sess.ID)
	defer cancel()

	executor.Finalize(sess, &RunResult{RunID: runID, SessionID: sess.ID, Status: "completed"})
	select {
	case event := <-events:
		t.Fatalf("durable finalize published %q before FinishDurable, want no event", event.Event)
	default:
	}
}

func TestRunExecutor_AttachmentsPublishedAsTranscriptEvent(t *testing.T) {
	srv := &Server{cfg: &Config{DefaultMode: "yolo"}, streamHub: newSessionStreamHub()}
	srv.eventBroker = NewEventBroker()
	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{ID: "attachment-run", SessionID: "sess-1", WorkDir: "/tmp/test", Status: "running", StartedAt: time.Now()})
	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}
	events, unsubscribe := srv.eventBroker.Subscribe("sess-1")
	defer unsubscribe()
	eventCh := make(chan agent.Event, 1)
	eventCh <- agent.Event{Type: agent.EventDone, Attachments: []provider.Attachment{{Kind: "citation", Name: "Source", URL: "https://example.test/source"}}}
	close(eventCh)
	if _, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	select {
	case event := <-events:
		if event.Event != "transcript" {
			t.Fatalf("event name = %q, want transcript", event.Event)
		}
		data, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		var transcript TranscriptStreamEvent
		if err := json.Unmarshal(data, &transcript); err != nil {
			t.Fatalf("decode transcript: %v", err)
		}
		if transcript.Type != "attachments" || transcript.Message == nil || len(transcript.Message.Attachments) != 1 || transcript.Message.Attachments[0].URL == "" {
			t.Fatalf("transcript = %#v", transcript)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attachment transcript event not published")
	}
}

func TestRunExecutorPersistsHostedItemLifecycleEvent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("hosted-lifecycle-run", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	run := &session.SessionRun{ID: "hosted-lifecycle-run-id", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "chat_completion", Mode: "agent", Status: "running", StartedAt: time.Now()}
	executor := NewRunExecutor(srv, srv.getEventBroker(), run)
	reg := tools.NewRegistry(sess.WorkDir, nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	eventCh := make(chan agent.Event, 2)
	eventCh <- agent.Event{Type: agent.EventHostedItem, HostedItem: &provider.HostedItem{ID: strings.Repeat("search-", 100), Type: "web_search_call", Status: "completed", OutputIndex: 1, Metadata: map[string]any{"title": "Source", "secret": "do-not-persist"}}}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)
	if _, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	found := false
	for _, item := range events {
		if item.EventType != "hosted_item" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(item.Data, &data); err != nil {
			t.Fatalf("decode hosted event: %v", err)
		}
		hosted, _ := data["hostedItem"].(map[string]any)
		if strings.HasSuffix(hosted["id"].(string), "...") && hosted["status"] == "completed" {
			metadata, _ := hosted["metadata"].(map[string]any)
			if _, leaked := metadata["secret"]; leaked {
				t.Fatalf("hosted metadata leaked secret: %#v", metadata)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("hosted lifecycle event not persisted: %#v", events)
	}
}

func TestRunExecutorPersistsResponsesStateTransition(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("responses-state-transition", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	executor := NewRunExecutor(srv, srv.getEventBroker(), &session.SessionRun{
		ID: "responses-state-run", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "chat_completion",
		Mode: "agent", Status: "running", StartedAt: time.Now(),
	})
	reg := tools.NewRegistry(sess.WorkDir, nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: srv.model}, reg)
	eventCh := make(chan agent.Event, 2)
	eventCh <- agent.Event{Type: agent.EventStatus, StatusMessage: "remote Responses lineage unavailable; retrying this turn from local replay", ResponseStateFailureClass: "expired"}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)
	if _, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "responses_state_transition" || events[0].Status != "retrying" || !strings.Contains(string(events[0].Data), `"failureClass":"expired"`) {
		t.Fatalf("state transition events = %#v", events)
	}
}

func TestRunExecutorPersistsFailedResponsesStateTransition(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("responses-state-failure", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	executor := NewRunExecutor(srv, srv.getEventBroker(), &session.SessionRun{
		ID: "responses-state-failure-run", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "chat_completion",
		Mode: "agent", Status: "running", StartedAt: time.Now(),
	})
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: srv.model}, tools.NewRegistry(sess.WorkDir, nil))
	eventCh := make(chan agent.Event, 1)
	eventCh <- agent.Event{Type: agent.EventError, Error: errors.New("forbidden"), ResponseStateFailureClass: "permission"}
	close(eventCh)
	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil || result.Status != "failed" {
		t.Fatalf("Execute() result=%#v err=%v", result, err)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "responses_state_transition" || events[0].Status != "failed" || !strings.Contains(string(events[0].Data), `"failureClass":"permission"`) {
		t.Fatalf("state failure events = %#v", events)
	}
}

func TestRunExecutor_ContextCancellation(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "cancel-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	// Send one event, then cancel the context and close the channel.
	// The executor checks ctx.Done() between events, so it will detect
	// cancellation on the next iteration after the event is processed.
	eventCh := make(chan agent.Event, 1)
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: "before cancel"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately, before the executor processes the event
	close(eventCh)

	result, err := executor.Execute(ctx, sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute() returned nil result")
	}
	if result.Status != "canceled" {
		t.Fatalf("expected status canceled, got %q", result.Status)
	}
}

func TestRunExecutor_SubAgentEventsSkippedForDone(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "sa-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	eventCh := make(chan agent.Event, 5)
	// Sub-agent done should be skipped, main agent done should terminate.
	eventCh <- agent.Event{Type: agent.EventDone, AgentID: "sub-agent-1"}
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: "main output"}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)

	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected status completed, got %q", result.Status)
	}
	// The sub-agent done event should have been skipped, so the main agent's
	// text delta should still have been processed and the main done should
	// have terminated the run.
}

func TestRunExecutor_ErrorEvent(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "err-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	eventCh := make(chan agent.Event, 2)
	eventCh <- agent.Event{Type: agent.EventError, Error: fmt.Errorf("test error")}
	close(eventCh)

	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("expected status failed, got %q", result.Status)
	}
	if result.Error != "test error" {
		t.Fatalf("expected provider error detail, got %q", result.Error)
	}
	if result.ErrorInfo == nil || result.ErrorInfo.Code != "run_failed" || result.ErrorInfo.Detail != "test error" {
		t.Fatalf("expected structured provider error info, got %#v", result.ErrorInfo)
	}
}

func TestRunExecutor_SubAgentErrorSkipped(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()

	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "sa-err-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})

	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	eventCh := make(chan agent.Event, 3)
	// Sub-agent error should be skipped, main agent done should terminate.
	eventCh <- agent.Event{Type: agent.EventError, AgentID: "sub-agent-1", Error: fmt.Errorf("sub error")}
	eventCh <- agent.Event{Type: agent.EventDone}
	close(eventCh)

	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected status completed, got %q — sub-agent error should be skipped", result.Status)
	}
}

func TestRunExecutor_RunFinishedStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     agent.TaskStatus
		runErr     error
		wantStatus string
	}{
		{"success", agent.TaskSuccess, nil, "completed"},
		{"incomplete", agent.TaskIncomplete, nil, "incomplete"},
		{"failed", agent.TaskFailed, fmt.Errorf("boom"), "failed"},
		{"canceled", agent.TaskCanceled, context.Canceled, "canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{
				cfg:       &Config{DefaultMode: "yolo"},
				streamHub: newSessionStreamHub(),
			}
			srv.eventBroker = NewEventBroker()
			executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
				ID: "rf-" + tc.name, SessionID: "sess-1", WorkDir: "/tmp/test",
				Status: "running", StartedAt: time.Now(),
			})
			reg := tools.NewRegistry("/tmp/test", nil)
			a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
			sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

			eventCh := make(chan agent.Event, 4)
			eventCh <- agent.Event{Type: agent.EventRunFinished, Status: tc.status, Error: tc.runErr, Done: true}
			// Legacy terminal events follow the canonical one and must not change
			// the outcome classification.
			if tc.status == agent.TaskFailed || tc.status == agent.TaskCanceled {
				eventCh <- agent.Event{Type: agent.EventError, Error: tc.runErr}
			} else {
				eventCh <- agent.Event{Type: agent.EventDone}
			}
			close(eventCh)

			result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, tc.wantStatus)
			}
		})
	}
}

func TestRunExecutor_ChannelClosedWithoutTerminalFails(t *testing.T) {
	srv := &Server{
		cfg:       &Config{DefaultMode: "yolo"},
		streamHub: newSessionStreamHub(),
	}
	srv.eventBroker = NewEventBroker()
	executor := NewRunExecutor(srv, srv.eventBroker, &session.SessionRun{
		ID: "no-terminal-run", SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(),
	})
	reg := tools.NewRegistry("/tmp/test", nil)
	a := agent.New(agent.Config{Provider: newRecordingAPIProvider(), Model: &provider.Model{ID: "test-model", ContextWindow: 32768, MaxTokens: 2048}}, reg)
	sess := &APISession{ID: "sess-1", WorkDir: "/tmp/test"}

	eventCh := make(chan agent.Event, 2)
	eventCh <- agent.Event{Type: agent.EventTextDelta, TextDelta: "partial output"}
	close(eventCh)

	result, err := executor.Execute(context.Background(), sess, a, eventCh, "test-model", "agent", false)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed for a stream without terminal event", result.Status)
	}
	if result.Error != "The run stopped before it could finish." {
		t.Fatalf("error = %q", result.Error)
	}
	if result.ErrorInfo == nil || result.ErrorInfo.Code != "event_stream_interrupted" || !result.ErrorInfo.Retryable {
		t.Fatalf("structured error = %#v", result.ErrorInfo)
	}
}

func TestRunManager_RecoverOrphanedRuns(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("sess-1"); err != nil {
		t.Fatal(err)
	}

	// Create an orphaned run (non-terminal status).
	orphanID := "orphan-1"
	if err := rm.Create(session.SessionRun{
		ID: orphanID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create orphan run: %v", err)
	}

	// Create a terminal run (should not be affected).
	doneID := "done-1"
	if err := rm.Create(session.SessionRun{
		ID: doneID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "completed", StartedAt: time.Now(), UpdatedAt: time.Now(),
		FinishedAt: timePtr(time.Now()),
	}); err != nil {
		t.Fatalf("Create done run: %v", err)
	}

	// Recover orphaned runs.
	if err := rm.RecoverOrphanedRuns(); err != nil {
		t.Fatalf("RecoverOrphanedRuns() error = %v", err)
	}

	// Verify orphaned run is now failed.
	orphan, err := rm.Get(orphanID)
	if err != nil {
		t.Fatalf("Get orphan run: %v", err)
	}
	if orphan.Status != "failed" {
		t.Fatalf("expected orphan status 'failed', got %q", orphan.Status)
	}

	// Verify terminal run is unchanged.
	done, err := rm.Get(doneID)
	if err != nil {
		t.Fatalf("Get done run: %v", err)
	}
	if done.Status != "completed" {
		t.Fatalf("expected done status 'completed', got %q", done.Status)
	}
}

func TestRunManager_RecoverOrphanedRunsExcept(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)
	for _, id := range []string{"sess-1", "sess-2"} {
		mgr := session.New(t.TempDir(), sessionDir)
		if err := mgr.InitWithID(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []session.SessionRun{
		{ID: "responses-run", SessionID: "sess-1", WorkDir: "/tmp/test", Source: "responses_background", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "local-run", SessionID: "sess-2", WorkDir: "/tmp/test", Source: "webui", Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now()},
	} {
		if err := rm.Create(run); err != nil {
			t.Fatalf("Create run: %v", err)
		}
	}
	if err := rm.RecoverOrphanedRunsExcept(func(run session.SessionRun) bool {
		return run.Source == "responses_background"
	}); err != nil {
		t.Fatalf("RecoverOrphanedRunsExcept() error = %v", err)
	}
	responsesRun, _ := rm.Get("responses-run")
	if responsesRun == nil || responsesRun.Status != "running" {
		t.Fatalf("responses run = %#v, want preserved running state", responsesRun)
	}
	localRun, _ := rm.Get("local-run")
	if localRun == nil || localRun.Status != "failed" {
		t.Fatalf("local run = %#v, want failed state", localRun)
	}
}

func TestRunManager_CancelRunInTerminalState(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)

	runID := "terminal-run"
	if err := rm.Create(session.SessionRun{
		ID: runID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "completed", StartedAt: time.Now(), UpdatedAt: time.Now(),
		FinishedAt: timePtr(time.Now()),
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	// Cancelling a terminal run should return false.
	if rm.Cancel(runID) {
		t.Fatal("Cancel() should return false for a terminal run")
	}

	// Verify the run status is unchanged.
	run, err := rm.Get(runID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("expected status 'completed', got %q", run.Status)
	}
}

func TestRunManager_CancelDBOnlyRun(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)

	runID := "db-only-run"
	// Create the run in DB but NOT in memory (no Attach).
	if err := rm.Create(session.SessionRun{
		ID: runID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	// Cancel should succeed even without in-memory cancel func.
	if !rm.Cancel(runID) {
		t.Fatal("Cancel() should return true for a DB-only run")
	}

	// Verify the run status is updated to cancelling.
	run, err := rm.Get(runID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run.Status != "cancelling" {
		t.Fatalf("expected status 'cancelling', got %q", run.Status)
	}
}

func TestRunManager_FinalizeOnceIdempotent(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)

	runID := "finalize-once-run"
	if err := rm.Create(session.SessionRun{
		ID: runID, SessionID: "sess-1", WorkDir: "/tmp/test",
		Status: "running", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create run: %v", err)
	}

	callCount := 0
	fn := func() { callCount++ }

	// First call should execute.
	if !rm.FinalizeOnce(runID, fn) {
		t.Fatal("FinalizeOnce() should return true on first call")
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	// Second call should be a no-op.
	if rm.FinalizeOnce(runID, fn) {
		t.Fatal("FinalizeOnce() should return false on second call")
	}
	if callCount != 1 {
		t.Fatalf("expected still 1 call, got %d", callCount)
	}
}

func TestRunManager_ActiveReturnsNilForNoActiveRun(t *testing.T) {
	sessionDir := t.TempDir()
	rm := NewRunManager(sessionDir)

	run, err := rm.Active("nonexistent-session")
	if err != nil {
		t.Fatalf("Active() error = %v", err)
	}
	if run != nil {
		t.Fatal("Active() should return nil for session with no runs")
	}
}
