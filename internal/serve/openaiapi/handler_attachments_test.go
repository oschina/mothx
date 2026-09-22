package openaiapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	openaiprovider "github.com/oschina/mothx/internal/provider/openai"
)

type testAttachmentResolverProvider struct {
	provider.Provider
	content provider.AttachmentContent
}

func (p *testAttachmentResolverProvider) ResolveAttachment(context.Context, string) (provider.AttachmentContent, error) {
	return p.content, nil
}

func TestHandleAttachmentAPIAuthorizesArchivedFileReference(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/files/file_123/content" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("file body"))
	}))
	defer upstream.Close()
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := openaiprovider.NewProviderWithModels("test-key", upstream.URL+"/v1", []*provider.Model{{ID: "m1"}})
	srv.provider = p
	srv.model = p.GetModel("m1")
	sess, err := srv.getOrCreateSession("attachment-route", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sess.Manager.AppendMessage(provider.Message{Role: "assistant", Attachments: []provider.Attachment{{Kind: "file", ProviderRef: "file_123"}}}); err != nil {
		t.Fatalf("archive attachment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/attachments/file_123?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleAttachmentAPI(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "file body" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download response status=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("security headers = %v", w.Header())
	}
	missing := httptest.NewRecorder()
	srv.HandleAttachmentAPI(missing, httptest.NewRequest(http.MethodGet, "/api/attachments/file_other?session_id="+sess.ID, nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "not archived") {
		t.Fatalf("unarchived response status=%d body=%q", missing.Code, missing.Body.String())
	}
}

func TestHandleAttachmentAPIUsesContainerProvenance(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/containers/container_123/files/file_123/content" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}))
	defer upstream.Close()
	srv := newTestServer(t)
	defer srv.pool.Stop()
	p := openaiprovider.NewProviderWithModels("test-key", upstream.URL+"/v1", []*provider.Model{{ID: "m1"}})
	srv.provider = p
	srv.model = p.GetModel("m1")
	sess, err := srv.getOrCreateSession("container-attachment-route", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sess.Manager.AppendMessage(provider.Message{Role: "assistant", Attachments: []provider.Attachment{{
		Kind: "file", ProviderRef: "file_123", Metadata: map[string]any{"containerId": "container_123"},
	}}}); err != nil {
		t.Fatalf("archive attachment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/attachments/file_123?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleAttachmentAPI(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "a,b\n1,2\n" {
		t.Fatalf("container download status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestHandleAttachmentAPIRejectsInvalidReferencesBeforeArchiveLookup(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("invalid-attachment-route", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, ref := range []string{"../secret", "file/secret", "file\\nsecret", strings.Repeat("x", 129)} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/attachments/"+ref+"?session_id="+sess.ID, nil)
		srv.HandleAttachmentAPI(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ref %q status=%d body=%q", ref, w.Code, w.Body.String())
		}
	}
}

func TestAttachmentFilenameSanitizesPathAndControlCharacters(t *testing.T) {
	if got := attachmentFilename(`../report\"\n.csv`, "fallback"); got != ".._report__n.csv" {
		t.Fatalf("filename = %q", got)
	}
	if got := attachmentFilename("", "file_1"); got != "file_1" {
		t.Fatalf("fallback filename = %q", got)
	}
}

func TestHandleAttachmentAPIUsesOptionalNonOpenAIResolver(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	base := srv.provider
	srv.provider = &testAttachmentResolverProvider{
		Provider: base,
		content:  provider.AttachmentContent{Data: []byte("vendor content"), MediaType: "application/octet-stream", Filename: "vendor.bin"},
	}
	sess, err := srv.getOrCreateSession("vendor-attachment-route", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := sess.Manager.AppendMessage(provider.Message{Role: "assistant", Attachments: []provider.Attachment{{Kind: "file", ProviderRef: "vendor_file_1"}}}); err != nil {
		t.Fatalf("archive attachment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/attachments/vendor_file_1?session_id="+sess.ID, nil)
	w := httptest.NewRecorder()
	srv.HandleAttachmentAPI(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "vendor content" || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("vendor resolver response status=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
	}
}
