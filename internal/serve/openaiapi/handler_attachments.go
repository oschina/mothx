package openaiapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// HandleAttachmentAPI serves an archived provider file reference through an
// optional provider resolver. The session archive is the authorization list;
// arbitrary provider IDs and URLs are never proxied.
// GET /api/attachments/{providerRef}?session_id={sessionID}
func (s *Server) HandleAttachmentAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ref, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/attachments/"))
	if err != nil || !strings.HasPrefix(r.URL.Path, "/api/attachments/") || ref == "" {
		writeError(w, http.StatusBadRequest, "attachment reference required", "invalid_request_error")
		return
	}
	if err := provider.ValidateAttachmentReferenceForResolver(ref); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment reference", "invalid_request_error")
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" || s == nil || s.settings == nil {
		writeError(w, http.StatusBadRequest, "session_id is required", "invalid_request_error")
		return
	}
	if _, found, err := s.findSessionWorkDir(sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	} else if !found {
		writeError(w, http.StatusNotFound, "session not found", "not_found")
		return
	}
	archived, found := s.archivedFileAttachment(sessionID, ref)
	if !found {
		writeError(w, http.StatusNotFound, "attachment is not archived for this session", "not_found")
		return
	}
	s.mu.RLock()
	activeProvider := s.provider
	s.mu.RUnlock()
	metadataResolver, hasMetadataResolver := activeProvider.(provider.AttachmentMetadataResolver)
	resolver, hasResolver := activeProvider.(provider.AttachmentResolver)
	if !hasMetadataResolver && !hasResolver {
		writeError(w, http.StatusNotImplemented, "the active provider cannot download attachments", "capability_error")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var content provider.AttachmentContent
	if hasMetadataResolver {
		content, err = metadataResolver.ResolveAttachmentWithMetadata(ctx, archived)
	} else {
		content, err = resolver.ResolveAttachment(ctx, ref)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("download attachment: %v", err), "upstream_error")
		return
	}
	mediaType := attachmentMediaType(content.MediaType, content.Data)
	filename := attachmentFilename(content.Filename, ref)
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content.Data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Data)
}

func attachmentMediaType(raw string, data []byte) string {
	mediaType := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
	if mediaType == "" || !strings.Contains(mediaType, "/") || strings.ContainsAny(mediaType, "\r\n") {
		return http.DetectContentType(data)
	}
	return mediaType
}

func attachmentFilename(raw, fallback string) string {
	name := strings.TrimSpace(raw)
	name = strings.NewReplacer("\\", "_", "/", "_", "\r", "", "\n", "", "\"", "", "\x00", "").Replace(name)
	if name == "" || name == "." || name == ".." {
		name = fallback
	}
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}

func (s *Server) archivedFileAttachment(sessionID, ref string) (provider.Attachment, bool) {
	if s == nil || s.settings == nil || sessionID == "" || ref == "" {
		return provider.Attachment{}, false
	}
	var after int64
	for {
		messages, err := session.ListSessionMessagesAfter(s.settings.GetSessionDir(), sessionID, after, 500)
		if err != nil {
			return provider.Attachment{}, false
		}
		for _, item := range messages {
			for _, attachment := range item.Message.Attachments {
				if attachment.Kind == "file" && attachment.ProviderRef == ref {
					return attachment, true
				}
			}
			if item.Seq > after {
				after = item.Seq
			}
		}
		if len(messages) < 500 {
			return provider.Attachment{}, false
		}
	}
}
