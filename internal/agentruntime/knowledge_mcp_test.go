package agentruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

const (
	knowledgeMCPTestHelperEnv        = "MOTHX_KNOWLEDGE_MCP_TEST_HELPER"
	knowledgeMCPTestSessionDirEnv    = "MOTHX_KNOWLEDGE_MCP_TEST_SESSION_DIR"
	knowledgeMCPTestKnowledgeBaseEnv = "MOTHX_KNOWLEDGE_MCP_TEST_KNOWLEDGE_BASE"
)

// TestKnowledgeMCPConnectsAsStandardTool doubles as a subprocess helper. The
// helper branch must write only JSON-RPC protocol messages to stdout.
func TestKnowledgeMCPConnectsAsStandardTool(t *testing.T) {
	if os.Getenv(knowledgeMCPTestHelperEnv) == "1" {
		handler, err := NewKnowledgeMCPHandler(os.Getenv(knowledgeMCPTestSessionDirEnv), []string{os.Getenv(knowledgeMCPTestKnowledgeBaseEnv)})
		if err != nil {
			os.Exit(2)
		}
		if err := mcp.ServeStdio(context.Background(), os.Stdin, os.Stdout, handler); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}

	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime.md"), []byte("# Runtime\n\nThe Runtime owns durable Runs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Runtime", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(t.TempDir(), nil)
	clients, err := mcp.ConnectServers(t.Context(), []mcp.ServerConfig{{
		Name:    "knowledge",
		Type:    "stdio",
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestKnowledgeMCPConnectsAsStandardTool$"},
		Env: []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{
			{Name: knowledgeMCPTestHelperEnv, Value: "1"},
			{Name: knowledgeMCPTestSessionDirEnv, Value: sessionDir},
			{Name: knowledgeMCPTestKnowledgeBaseEnv, Value: base.ID},
		},
	}}, registry, mcp.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer mcp.CloseClients(clients)

	var search tools.Tool
	for _, tool := range registry.All() {
		if strings.Contains(tool.Name(), knowledgeMCPToolName) {
			search = tool
			break
		}
	}
	if search == nil {
		t.Fatalf("registered tools = %#v, want standard MCP knowledge search tool", registry.All())
	}
	result, err := search.Execute(t.Context(), map[string]any{"knowledgeBaseId": base.ID, "query": "durable runtime"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"runtime.md", "chunkId", "snapshotId"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("MCP tool result %q missing %q", result.Text, want)
		}
	}
}

func TestKnowledgeMCPHandlerReturnsBoundedCitedSnapshotEvidence(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime.md"), []byte("# Runtime\n\nThe Runtime owns durable Runs.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Runtime", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	handler, err := NewKnowledgeMCPHandler(sessionDir, []string{base.ID})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := handler.ListTools(t.Context())
	if err != nil || len(tools) != 1 || tools[0].Name != knowledgeMCPToolName {
		t.Fatalf("tools = %#v (%v)", tools, err)
	}
	result, err := handler.CallTool(t.Context(), knowledgeMCPToolName, json.RawMessage(`{"knowledgeBaseId":"`+base.ID+`","query":"durable runtime"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "runtime.md") || !strings.Contains(result.Content[0].Text, "chunkId") {
		t.Fatalf("knowledge MCP result = %#v", result)
	}
	if _, err := handler.CallTool(t.Context(), knowledgeMCPToolName, json.RawMessage(`{"knowledgeBaseId":"not-configured","query":"runtime"}`)); err == nil {
		t.Fatal("unconfigured knowledge base must be rejected")
	}
}
