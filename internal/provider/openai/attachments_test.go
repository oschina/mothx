package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestResolveAttachmentDownloadsAuthorizedProviderFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/files/file_123/content" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("downloaded"))
	}))
	defer server.Close()
	p := NewProviderWithModels("test-key", server.URL+"/v1", []*provider.Model{{ID: "test"}})
	content, err := p.ResolveAttachment(context.Background(), "file_123")
	if err != nil || string(content.Data) != "downloaded" || content.MediaType != "text/plain" {
		t.Fatalf("content=%#v err=%v", content, err)
	}
	if _, err := p.ResolveAttachment(context.Background(), "../secret"); err == nil {
		t.Fatal("invalid attachment reference was accepted")
	}
}

func TestResolveAttachmentWithMetadataUsesCodeInterpreterContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/containers/container_123/files/file_123/content" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}))
	defer server.Close()
	p := NewProviderWithModels("test-key", server.URL+"/v1", []*provider.Model{{ID: "test"}})
	content, err := p.ResolveAttachmentWithMetadata(context.Background(), provider.Attachment{
		Kind: "file", ProviderRef: "file_123", Metadata: map[string]any{"containerId": "container_123"},
	})
	if err != nil || content.MediaType != "text/csv" || string(content.Data) != "a,b\n1,2\n" {
		t.Fatalf("content=%#v err=%v", content, err)
	}
}
