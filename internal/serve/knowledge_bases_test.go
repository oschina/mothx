package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/cron"
	"github.com/oschina/mothx/internal/session"
)

func TestKnowledgeBaseHandlersManageRuntimeOwnedIndexes(t *testing.T) {
	sessionDir := t.TempDir()
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "guide.md"), []byte("# Runtime knowledge\n\nThe knowledge index belongs to the shared runtime.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &channelRuntime{sessionDir: sessionDir}

	list := knowledgeBaseRequest(t, runtime, http.MethodGet, "/api/knowledge-bases", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"knowledgeBases":[]`) {
		t.Fatalf("initial list = %d: %s", list.Code, list.Body.String())
	}

	createBody := `{"knowledgeBase":{"name":"Docs","rootDir":` + quoteJSON(sourceDir) + `,"preprocessProfile":"documents","mode":"yolo","schedule":"manual","enabled":true}}`
	created := knowledgeBaseRequest(t, runtime, http.MethodPost, "/api/knowledge-bases", createBody)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	var view knowledgeBaseView
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.KnowledgeBase.ID == "" || view.KnowledgeBase.Schedule != "manual" || view.Status != "unindexed" {
		t.Fatalf("created view = %#v", view)
	}

	invalidIndexer := knowledgeBaseRequest(t, runtime, http.MethodPatch, "/api/knowledge-bases/"+view.KnowledgeBase.ID, `{"knowledgeBase":{"name":"Docs","rootDir":`+quoteJSON(sourceDir)+`,"preprocessProfile":"documents","provider":"openai","schedule":"manual","enabled":true}}`)
	if invalidIndexer.Code != http.StatusBadRequest || !strings.Contains(invalidIndexer.Body.String(), "provider and model") {
		t.Fatalf("invalid indexer update = %d: %s", invalidIndexer.Code, invalidIndexer.Body.String())
	}

	// Scans are admitted in the background: the POST returns immediately with a
	// running-job projection so a page reload can observe the scan in progress.
	scanned := knowledgeBaseRequest(t, runtime, http.MethodPost, "/api/knowledge-bases/"+view.KnowledgeBase.ID+"/scan", "{}")
	if scanned.Code != http.StatusOK {
		t.Fatalf("scan = %d: %s", scanned.Code, scanned.Body.String())
	}
	var scanView knowledgeBaseView
	if err := json.Unmarshal(scanned.Body.Bytes(), &scanView); err != nil {
		t.Fatal(err)
	}
	if scanView.KnowledgeBase.ID != view.KnowledgeBase.ID {
		t.Fatalf("scan view = %#v", scanView)
	}
	// Poll the management projection until the background scan commits its
	// snapshot; this mirrors how the WebUI tracks progress after a reload.
	deadline := time.Now().Add(10 * time.Second)
	for {
		scanView = knowledgeBaseView{}
		listed := knowledgeBaseRequest(t, runtime, http.MethodGet, "/api/knowledge-bases/"+view.KnowledgeBase.ID, "")
		if listed.Code != http.StatusOK {
			t.Fatalf("poll = %d: %s", listed.Code, listed.Body.String())
		}
		if err := json.Unmarshal(listed.Body.Bytes(), &scanView); err != nil {
			t.Fatal(err)
		}
		if scanView.Snapshot != nil && scanView.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan did not complete: %#v", scanView)
		}
		time.Sleep(20 * time.Millisecond)
	}
	view = scanView
	if view.Snapshot == nil || view.Status != "completed" || view.Snapshot.ChunkCount == 0 {
		t.Fatalf("scan view = %#v", view)
	}

	queried := knowledgeBaseRequest(t, runtime, http.MethodPost, "/api/knowledge-bases/"+view.KnowledgeBase.ID+"/query", `{"query":"shared runtime","limit":8}`)
	if queried.Code != http.StatusOK || !strings.Contains(queried.Body.String(), "guide.md") {
		t.Fatalf("query = %d: %s", queried.Code, queried.Body.String())
	}

	deleted := knowledgeBaseRequest(t, runtime, http.MethodDelete, "/api/knowledge-bases/"+view.KnowledgeBase.ID, "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete = %d: %s", deleted.Code, deleted.Body.String())
	}
}

func TestKnowledgeBaseHandlersRejectScheduledWebUIConfiguration(t *testing.T) {
	runtime := &channelRuntime{sessionDir: t.TempDir()}
	sourceDir := t.TempDir()
	request := knowledgeBaseRequest(t, runtime, http.MethodPost, "/api/knowledge-bases", `{"knowledgeBase":{"name":"Docs","rootDir":`+quoteJSON(sourceDir)+`,"preprocessProfile":"documents","schedule":"@daily","enabled":true}}`)
	if request.Code != http.StatusBadRequest || !strings.Contains(request.Body.String(), "scheduled knowledge base indexing") {
		t.Fatalf("scheduled create = %d: %s", request.Code, request.Body.String())
	}
}

func knowledgeBaseRequest(t *testing.T, runtime *channelRuntime, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	runtime.handleKnowledgeBases(response, req)
	return response
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// TestKnowledgeBaseCronJobsRouteThroughRuntimeHandler pins the shared-store
// contract: the serve scheduler claims the same namespaced jobs Desktop/ACP
// persists, and must run them through the Runtime index path instead of
// falling through to a bare agent prompt in the knowledge source directory.
func TestKnowledgeBaseCronJobsRouteThroughRuntimeHandler(t *testing.T) {
	sessionDir := t.TempDir()
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "guide.md"), []byte("# Guide\n\nScheduled scans reuse the runtime handler.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &channelRuntime{sessionDir: sessionDir}

	handled, response, err := runtime.runKnowledgeBaseCronJob(t.Context(), cron.CronJob{ID: "ordinary-job", Prompt: "do something"})
	if handled || response != "" || err != nil {
		t.Fatalf("foreign cron job = (%v, %q, %v), want scheduler fallthrough", handled, response, err)
	}

	handled, _, err = runtime.runKnowledgeBaseCronJob(t.Context(), cron.CronJob{ID: agentruntime.KnowledgeBaseCronJobID("missing")})
	if !handled || err == nil {
		t.Fatalf("missing base cron job = (%v, %v), want a handled failure", handled, err)
	}

	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Scheduled", RootDir: sourceDir, PreprocessProfile: "documents", Schedule: "daily", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handled, response, err = runtime.runKnowledgeBaseCronJob(t.Context(), cron.CronJob{ID: agentruntime.KnowledgeBaseCronJobID(base.ID)})
	if !handled || err != nil {
		t.Fatalf("knowledge cron job = (%v, %v)", handled, err)
	}
	if !strings.Contains(response, "indexed knowledge base "+base.ID) {
		t.Fatalf("cron response = %q", response)
	}
	reloaded, err := session.GetKnowledgeBase(t.Context(), sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(reloaded.ActiveSnapshotID) == "" {
		t.Fatalf("cron reindex left no active snapshot: %#v", reloaded)
	}
}
