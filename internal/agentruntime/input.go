package agentruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// AttachmentKind identifies the media classes supported by Runtime input and
// artifact delivery. Provider attachments are normalized into this store only
// when they become concrete files.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentFile  AttachmentKind = "file"
	AttachmentAudio AttachmentKind = "audio"
	AttachmentVideo AttachmentKind = "video"
)

// artifactIngress is the private-store handoff used only by publish_artifact
// and authorized provider attachment resolvers. User input uses InputIngress.
type artifactIngress struct {
	Origin    string
	Reference string
	Kind      AttachmentKind
	Filename  string
	MediaType string
	SizeHint  int64
	Open      func(context.Context) (artifactStream, error)
}

type artifactStream struct {
	Reader      io.ReadCloser
	Filename    string
	MediaType   string
	ContentSize int64
}

// SessionAttachment is the canonical persisted attachment record. The content
// itself lives under StorageKey and is never embedded in a session entry.
type SessionAttachment struct {
	ID         string
	SessionID  string
	RunID      string
	Origin     string
	Kind       AttachmentKind
	Filename   string
	MediaType  string
	Bytes      int64
	SHA256     string
	StorageKey string
	Status     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

func parseTimestamp(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// AttachmentPolicy contains the local resource limits for accepted media.
// These are reliability limits for a self-hosted service, not a moderation or
// multi-tenant authorization policy.
type AttachmentPolicy struct {
	MaxImageBytes int64
	MaxFileBytes  int64
	Retention     time.Duration
}

func DefaultAttachmentPolicy() AttachmentPolicy {
	return AttachmentPolicy{
		MaxImageBytes: 20 << 20,
		MaxFileBytes:  50 << 20,
		Retention:     7 * 24 * time.Hour,
	}
}

// AttachmentService owns attachment storage and its session-backed records.
// Platform adapters provide only the authenticated Open function.
type AttachmentService struct {
	sessionDir string
	policy     AttachmentPolicy
}

func NewAttachmentService(sessionDir string, policy AttachmentPolicy) (*AttachmentService, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("attachment session directory is required")
	}
	if policy.MaxImageBytes <= 0 || policy.MaxFileBytes <= 0 {
		return nil, fmt.Errorf("attachment size limits must be positive")
	}
	if policy.Retention <= 0 {
		return nil, fmt.Errorf("attachment retention must be positive")
	}
	return &AttachmentService{sessionDir: filepath.Clean(sessionDir), policy: policy}, nil
}

func (s *AttachmentService) Policy() AttachmentPolicy {
	if s == nil {
		return AttachmentPolicy{}
	}
	return s.policy
}

// acceptArtifact copies a published/generated object into Runtime-private
// storage. User inputs must never call this path.
func (s *AttachmentService) acceptArtifact(ctx context.Context, sessionID, runID string, ingress artifactIngress) (SessionAttachment, error) {
	if s == nil {
		return SessionAttachment{}, fmt.Errorf("attachment service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return SessionAttachment{}, fmt.Errorf("attachment session ID is required")
	}
	if ingress.Open == nil {
		return SessionAttachment{}, fmt.Errorf("attachment source is not readable")
	}
	if ingress.Kind != AttachmentImage && ingress.Kind != AttachmentFile && ingress.Kind != AttachmentAudio && ingress.Kind != AttachmentVideo {
		return SessionAttachment{}, fmt.Errorf("unsupported attachment kind %q", ingress.Kind)
	}
	// Best-effort expiry cleanup belongs to the single Runtime-owned store. A
	// cleanup hiccup must not make a trusted self-hosted user's new attachment
	// unusable; its durable write below remains the authoritative operation.
	_, _ = s.CleanupExpired(ctx)
	// Expiry only sees rows the database still has. The throttled directory pass
	// beside it is what reclaims storage whose rows are gone - a failed intake, a
	// deleted session, or an archived database - so the private store cannot grow
	// without bound between maintenance runs.
	reconcileArtifactStorageOpportunistic(s.sessionDir, s.policy)

	maxBytes := s.policy.MaxFileBytes
	if ingress.Kind == AttachmentImage {
		maxBytes = s.policy.MaxImageBytes
	}
	if ingress.SizeHint > maxBytes {
		return SessionAttachment{}, fmt.Errorf("attachment exceeds %d bytes", maxBytes)
	}

	attachmentID := session.GenerateID()
	if err := validatePathComponent(sessionID); err != nil {
		return SessionAttachment{}, err
	}
	if err := validatePathComponent(attachmentID); err != nil {
		return SessionAttachment{}, err
	}
	dir := filepath.Join(s.sessionDir, "artifacts", attachmentID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return SessionAttachment{}, fmt.Errorf("create attachment directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return SessionAttachment{}, fmt.Errorf("create attachment temporary file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	stream, err := ingress.Open(ctx)
	if err != nil {
		cleanup()
		return SessionAttachment{}, fmt.Errorf("open %s attachment: %w", ingress.Origin, err)
	}
	reader := stream.Reader
	if reader == nil {
		cleanup()
		return SessionAttachment{}, fmt.Errorf("open %s attachment: empty reader", ingress.Origin)
	}
	defer reader.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(reader, maxBytes+1))
	if err != nil {
		cleanup()
		return SessionAttachment{}, fmt.Errorf("read attachment: %w", err)
	}
	if written > maxBytes {
		cleanup()
		return SessionAttachment{}, fmt.Errorf("attachment exceeds %d bytes", maxBytes)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return SessionAttachment{}, fmt.Errorf("sync attachment: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return SessionAttachment{}, fmt.Errorf("close attachment: %w", err)
	}

	filename := ingress.Filename
	if strings.TrimSpace(filename) == "" {
		filename = stream.Filename
	}
	filename = sanitizeAttachmentFilename(filename)
	mediaType := strings.TrimSpace(ingress.MediaType)
	if mediaType == "" {
		mediaType = strings.TrimSpace(stream.MediaType)
	}
	detectedType, detectErr := detectAttachmentMediaType(tmpName)
	if ingress.Kind == AttachmentImage {
		if detectErr != nil || !strings.HasPrefix(strings.ToLower(detectedType), "image/") {
			_ = os.Remove(tmpName)
			if detectErr != nil {
				return SessionAttachment{}, fmt.Errorf("detect image attachment: %w", detectErr)
			}
			return SessionAttachment{}, fmt.Errorf("image attachment has detected media type %q", detectedType)
		}
		// Use the bytes-derived type for image input instead of trusting an
		// event's filename or content-type hint.
		mediaType = detectedType
	} else if mediaType == "" && detectErr == nil {
		mediaType = detectedType
	}

	storageKey := filepath.ToSlash(filepath.Join("artifacts", attachmentID, "content"))
	finalPath := filepath.Join(dir, "content")
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return SessionAttachment{}, fmt.Errorf("commit attachment: %w", err)
	}

	now := time.Now().UTC()
	record := SessionAttachment{
		ID: attachmentID, SessionID: sessionID, RunID: runID,
		Origin: ingress.Origin, Kind: ingress.Kind, Filename: filename,
		MediaType: mediaType, Bytes: written, SHA256: hex.EncodeToString(hash.Sum(nil)),
		StorageKey: storageKey, Status: "accepted", CreatedAt: now,
		ExpiresAt: now.Add(s.policy.Retention),
	}
	if err := session.WriteRootDatabase(ctx, s.sessionDir, func(tx *dao.Tx) error {
		return dao.NewAttachmentDAO(nil).Insert(ctx, tx, &dao.AttachmentRecord{ID: record.ID, SessionID: record.SessionID, RunID: record.RunID,
			Origin: record.Origin, Kind: string(record.Kind), Filename: record.Filename, MediaType: record.MediaType,
			Bytes: record.Bytes, SHA256: record.SHA256, StorageKey: record.StorageKey, Status: record.Status,
			CreatedAt: record.CreatedAt.Format(time.RFC3339Nano), ExpiresAt: record.ExpiresAt.Format(time.RFC3339Nano), Metadata: "{}"})
	}); err != nil {
		_ = os.Remove(finalPath)
		return SessionAttachment{}, fmt.Errorf("persist attachment: %w", err)
	}
	return record, nil
}

// Get returns one attachment record belonging to sessionID.
func (s *AttachmentService) Get(ctx context.Context, sessionID, attachmentID string) (SessionAttachment, error) {
	if s == nil {
		return SessionAttachment{}, fmt.Errorf("attachment service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return SessionAttachment{}, err
	}
	if err := validatePathComponent(attachmentID); err != nil {
		return SessionAttachment{}, err
	}
	var record SessionAttachment
	err := session.QueryRootDatabase(s.sessionDir, func(db *dao.Database) error {
		stored, err := dao.NewAttachmentDAO(db.Bun()).Find(ctx, sessionID, attachmentID)
		if err != nil {
			return err
		}
		record = SessionAttachment{ID: stored.ID, SessionID: stored.SessionID, RunID: stored.RunID, Origin: stored.Origin,
			Kind: AttachmentKind(stored.Kind), Filename: stored.Filename, MediaType: stored.MediaType, Bytes: stored.Bytes,
			SHA256: stored.SHA256, StorageKey: stored.StorageKey, Status: stored.Status,
			CreatedAt: parseTimestamp(stored.CreatedAt), ExpiresAt: parseTimestamp(stored.ExpiresAt)}
		return nil
	})
	if err != nil {
		return SessionAttachment{}, err
	}
	return record, nil
}

// Open returns the private content stream after checking session ownership and
// expiry. Callers must close the returned reader.
func (s *AttachmentService) Open(ctx context.Context, sessionID, attachmentID string) (SessionAttachment, io.ReadCloser, error) {
	record, err := s.Get(ctx, sessionID, attachmentID)
	if err != nil {
		return SessionAttachment{}, nil, err
	}
	if !record.ExpiresAt.IsZero() && time.Now().After(record.ExpiresAt) {
		return record, nil, fmt.Errorf("attachment %s has expired", attachmentID)
	}
	path, err := s.storagePath(record.StorageKey)
	if err != nil {
		return record, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return record, nil, fmt.Errorf("open attachment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return record, nil, fmt.Errorf("attachment %s is not a regular file", attachmentID)
	}
	if record.Bytes != info.Size() {
		return record, nil, fmt.Errorf("attachment %s failed integrity check: size mismatch", attachmentID)
	}
	file, err := os.Open(path)
	if err != nil {
		return record, nil, fmt.Errorf("open attachment: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return record, nil, fmt.Errorf("verify attachment %s: %w", attachmentID, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.TrimSpace(record.SHA256) {
		_ = file.Close()
		return record, nil, fmt.Errorf("attachment %s failed integrity check: hash mismatch", attachmentID)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return record, nil, fmt.Errorf("rewind attachment %s: %w", attachmentID, err)
	}
	return record, file, nil
}

// CleanupExpired expires and removes private attachment content whose TTL has
// elapsed. It is deliberately tolerant of an already-missing file: expiry is
// a durable Runtime state, while a subsequent invocation can retry a failed
// filesystem removal without involving any transport adapter.
func (s *AttachmentService) CleanupExpired(ctx context.Context) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("attachment service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	type expiredAttachment struct {
		id         string
		storageKey string
	}
	var expired []expiredAttachment
	err := session.WriteRootDatabase(ctx, s.sessionDir, func(tx *dao.Tx) error {
		records, err := dao.NewAttachmentDAO(nil).Expired(ctx, tx, now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		for _, record := range records {
			expired = append(expired, expiredAttachment{id: record.ID, storageKey: record.StorageKey})
		}
		return dao.NewAttachmentDAO(nil).MarkExpired(ctx, tx, now.Format(time.RFC3339Nano))
	})
	if err != nil {
		return 0, fmt.Errorf("expire attachments: %w", err)
	}
	for _, item := range expired {
		path, err := s.storagePath(item.storageKey)
		if err != nil {
			return len(expired), err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return len(expired), fmt.Errorf("remove expired attachment %s: %w", item.id, err)
		}
		// The attachment directory contains only the random content object once
		// Accept has committed it. Ignore a non-empty/missing parent so a retry
		// remains safe if a process was interrupted around the original write.
		_ = os.Remove(filepath.Dir(path))
	}
	return len(expired), nil
}

func (s *AttachmentService) storagePath(storageKey string) (string, error) {
	if strings.TrimSpace(storageKey) == "" {
		return "", fmt.Errorf("invalid attachment storage key")
	}
	path := filepath.Join(s.sessionDir, filepath.FromSlash(storageKey))
	rel, err := filepath.Rel(s.sessionDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid attachment storage key")
	}
	return path, nil
}

// SetStatus updates the Runtime-owned lifecycle state of an attachment. It is
// used for explicit state transitions such as accepted -> generated -> expired;
// adapters do not write attachment rows or statuses directly.
func (s *AttachmentService) SetStatus(ctx context.Context, sessionID, attachmentID, status string) error {
	if s == nil {
		return fmt.Errorf("attachment service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return err
	}
	if err := validatePathComponent(attachmentID); err != nil {
		return err
	}
	if status != "accepted" && status != "generated" && status != "expired" {
		return fmt.Errorf("invalid attachment status %q", status)
	}
	return session.WriteRootDatabase(ctx, s.sessionDir, func(tx *dao.Tx) error {
		changed, err := dao.NewAttachmentDAO(nil).SetStatus(ctx, tx, sessionID, attachmentID, status)
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("attachment %s not found for session", attachmentID)
		}
		return nil
	})
}

// AcceptProviderAttachment materializes a provider-declared output attachment
// into the same private session store used for inbound media. A URL or a
// filename alone is never an artifact: the provider must expose an authorized
// resolver for its opaque reference.
func (r *SessionRuntime) AcceptProviderAttachment(ctx context.Context, runID string, p provider.Provider, attachment provider.Attachment) (SessionAttachment, error) {
	if err := r.ensureOpen(); err != nil {
		return SessionAttachment{}, err
	}
	if p == nil {
		return SessionAttachment{}, fmt.Errorf("provider attachment resolver is required")
	}
	if err := provider.ValidateAttachmentReferenceForResolver(attachment.ProviderRef); err != nil {
		return SessionAttachment{}, err
	}
	kind := AttachmentKind(attachment.Kind)
	if kind != AttachmentImage && kind != AttachmentFile && kind != AttachmentAudio && kind != AttachmentVideo {
		return SessionAttachment{}, fmt.Errorf("provider attachment kind %q is not deliverable", attachment.Kind)
	}
	var (
		content provider.AttachmentContent
		err     error
	)
	if resolver, ok := p.(provider.AttachmentMetadataResolver); ok {
		content, err = resolver.ResolveAttachmentWithMetadata(ctx, attachment)
	} else if resolver, ok := p.(provider.AttachmentResolver); ok {
		content, err = resolver.ResolveAttachment(ctx, attachment.ProviderRef)
	} else {
		return SessionAttachment{}, fmt.Errorf("provider %q cannot resolve attachments", p.Name())
	}
	if err != nil {
		return SessionAttachment{}, fmt.Errorf("resolve provider attachment: %w", err)
	}
	if len(content.Data) == 0 {
		return SessionAttachment{}, fmt.Errorf("provider attachment is empty")
	}
	r.mu.RLock()
	service := r.Attachments
	sessionID := r.ID
	r.mu.RUnlock()
	if service == nil || sessionID == "" {
		return SessionAttachment{}, fmt.Errorf("attachment runtime is not bound to a session")
	}
	filename := content.Filename
	if filename == "" {
		filename = attachment.Name
	}
	mediaType := content.MediaType
	if mediaType == "" {
		mediaType = attachment.MediaType
	}
	record, err := service.acceptArtifact(ctx, sessionID, runID, artifactIngress{
		Origin: "provider:" + p.Name(), Reference: attachment.ProviderRef, Kind: kind,
		Filename: filename, MediaType: mediaType, SizeHint: int64(len(content.Data)),
		Open: func(context.Context) (artifactStream, error) {
			return artifactStream{Reader: io.NopCloser(bytes.NewReader(content.Data)), Filename: filename, MediaType: mediaType, ContentSize: int64(len(content.Data))}, nil
		},
	})
	if err != nil {
		return SessionAttachment{}, err
	}
	if err := service.SetStatus(ctx, sessionID, record.ID, "generated"); err != nil {
		return SessionAttachment{}, err
	}
	record.Status = "generated"
	return record, nil
}

func validatePathComponent(value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsRune(value, filepath.Separator) {
		return fmt.Errorf("invalid attachment path component")
	}
	return nil
}

func sanitizeAttachmentFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	if value == "." || value == ".." || value == "" {
		return "attachment"
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 255 {
			break
		}
	}
	if b.Len() == 0 {
		return "attachment"
	}
	return b.String()
}

func detectAttachmentMediaType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}
