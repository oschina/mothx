package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/oschina/mothx/internal/provider"
)

const maxResolvedAttachmentBytes = 32 << 20

// ResolveAttachment downloads a file reference through the configured OpenAI
// API endpoint. It is intentionally optional and never turns arbitrary URLs
// into a proxy; the Serve layer authorizes the reference against the archive.
func (p *Provider) ResolveAttachment(ctx context.Context, ref string) (provider.AttachmentContent, error) {
	return p.resolveAttachment(ctx, ref, "")
}

// ResolveAttachmentWithMetadata uses the Code Interpreter container file
// endpoint when archived provenance contains both container and file identity.
// Ordinary refs continue through the Files API.
func (p *Provider) ResolveAttachmentWithMetadata(ctx context.Context, attachment provider.Attachment) (provider.AttachmentContent, error) {
	containerID, _ := attachment.Metadata["containerId"].(string)
	return p.resolveAttachment(ctx, attachment.ProviderRef, containerID)
}

func (p *Provider) resolveAttachment(ctx context.Context, ref, containerID string) (provider.AttachmentContent, error) {
	if p == nil || p.client == nil || p.apiKey == "" {
		return provider.AttachmentContent{}, fmt.Errorf("OpenAI attachment resolver is unavailable")
	}
	if err := provider.ValidateAttachmentReferenceForResolver(ref); err != nil {
		return provider.AttachmentContent{}, err
	}
	endpoint := strings.TrimRight(p.baseURL, "/") + "/files/" + url.PathEscape(ref) + "/content"
	if containerID != "" {
		if err := provider.ValidateAttachmentReferenceForResolver(containerID); err == nil {
			endpoint = strings.TrimRight(p.baseURL, "/") + "/containers/" + url.PathEscape(containerID) + "/files/" + url.PathEscape(ref) + "/content"
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return provider.AttachmentContent{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return provider.AttachmentContent{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return provider.AttachmentContent{}, fmt.Errorf("attachment download failed with HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxResolvedAttachmentBytes {
		return provider.AttachmentContent{}, fmt.Errorf("attachment exceeds %d bytes", maxResolvedAttachmentBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResolvedAttachmentBytes+1))
	if err != nil {
		return provider.AttachmentContent{}, err
	}
	if len(data) > maxResolvedAttachmentBytes {
		return provider.AttachmentContent{}, fmt.Errorf("attachment exceeds %d bytes", maxResolvedAttachmentBytes)
	}
	mediaType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return provider.AttachmentContent{Data: data, MediaType: mediaType, Filename: ref}, nil
}
