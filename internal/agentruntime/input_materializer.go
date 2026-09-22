package agentruntime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	_ "golang.org/x/image/webp"
)

// InputIngress is the ephemeral adapter-to-Runtime handoff for one input
// resource. Reference and transport credentials are never persisted.
type InputIngress struct {
	Origin        string
	EventID       string
	ItemIndex     int
	Reference     string
	Kind          AttachmentKind
	FilenameHint  string
	MediaTypeHint string
	SizeHint      int64
	Open          func(context.Context) (InputStream, error)
}

// InputStream is the authenticated one-shot stream supplied by an adapter.
type InputStream struct {
	Reader      io.ReadCloser
	Filename    string
	MediaType   string
	ContentSize int64
}

// PreparedInput is the opaque resource reference carried by an input
// submission after Runtime has materialized and persisted it.
type PreparedInput struct {
	ResourceID   string
	Kind         AttachmentKind
	RelativePath string
	Filename     string
	MediaType    string
	Bytes        int64
}

// InputSubmission is the only user-input contract consumed by SessionRuntime.
// IdempotencyKey carries the caller's submission identity. Resource item
// idempotency is enforced here; durable submission reservation and existing-Run
// reuse are handled by the Runtime admission layer.
type InputSubmission struct {
	Text                    string
	Resources               []PreparedInput
	KnowledgeBaseReferences []KnowledgeBaseReference
	KnowledgeCapsules       []KnowledgeCapsule
	IdempotencyKey          string
}

// ResourceIDs returns the canonical Runtime resource IDs in submission order.
// The slice is copied so callers cannot mutate the submission's ownership
// facts while durable admission is assembling its transaction.
func (s InputSubmission) ResourceIDs() []string {
	ids := make([]string, 0, len(s.Resources))
	for _, resource := range s.Resources {
		if resource.ResourceID != "" {
			ids = append(ids, resource.ResourceID)
		}
	}
	return ids
}

// RunInput remains a source-compatible name while adapters migrate to the
// canonical InputSubmission name. It is an alias, not a second input model.
type RunInput = InputSubmission

// InputResource is the canonical persisted input file record.
type InputResource struct {
	ID           string
	SessionID    string
	RunID        string
	Origin       string
	EventID      string
	ItemIndex    int
	ItemKey      string
	Kind         AttachmentKind
	Filename     string
	MediaType    string
	Bytes        int64
	SHA256       string
	RelativePath string
	Status       string
	CreatedAt    time.Time
}

func (r InputResource) Prepared() PreparedInput {
	return PreparedInput{
		ResourceID: r.ID, Kind: r.Kind, RelativePath: r.RelativePath,
		Filename: r.Filename, MediaType: r.MediaType, Bytes: r.Bytes,
	}
}

// InputPolicy contains reliability limits applied before an input file enters
// the project workspace. It does not select file value or parse documents.
type InputPolicy struct {
	MaxImageBytes  int64
	MaxFileBytes   int64
	MaxImagePixels int64
	// MaxInlineMediaBytes caps the audio/video payload BuildUserMessage may
	// inline into a provider request. Larger media stay workspace files with a
	// manifest note because OpenAI-compatible base64 media inputs are limited
	// (Qwen-Omni accepts at most a 10MB base64 payload).
	MaxInlineMediaBytes int64
	DraftMaxAge         time.Duration
}

func DefaultInputPolicy() InputPolicy {
	return InputPolicy{MaxImageBytes: 20 << 20, MaxFileBytes: 50 << 20, MaxImagePixels: 40_000_000, MaxInlineMediaBytes: 7 << 20, DraftMaxAge: 24 * time.Hour}
}

// InputMaterializer owns project-relative input files and their session-backed
// records. Artifact bytes deliberately use a different private store.
type InputMaterializer struct {
	sessionDir string
	workDir    string
	policy     InputPolicy
	keyMu      sync.Mutex
	itemKeyKey []byte
}

func NewInputMaterializer(sessionDir, workDir string, policy InputPolicy) (*InputMaterializer, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("input session directory is required")
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("input work directory is required")
	}
	if policy.MaxImageBytes <= 0 || policy.MaxFileBytes <= 0 || policy.MaxImagePixels <= 0 {
		return nil, fmt.Errorf("input resource limits must be positive")
	}
	if policy.MaxInlineMediaBytes <= 0 {
		policy.MaxInlineMediaBytes = DefaultInputPolicy().MaxInlineMediaBytes
	}
	if policy.DraftMaxAge <= 0 {
		policy.DraftMaxAge = 24 * time.Hour
	}
	return &InputMaterializer{
		sessionDir: filepath.Clean(sessionDir), workDir: filepath.Clean(workDir), policy: policy,
	}, nil
}

// Prepare streams one resource into .mothx/tmp/inputs and persists its
// canonical metadata. Stable platform items are idempotent across retries and
// concurrent deliveries.
func (m *InputMaterializer) Prepare(ctx context.Context, sessionID, runID string, ingress InputIngress) (InputResource, error) {
	if m == nil {
		return InputResource{}, fmt.Errorf("input materializer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return InputResource{}, err
	}
	if ingress.Open == nil {
		return InputResource{}, fmt.Errorf("input source is not readable")
	}
	if ingress.Kind != AttachmentImage && ingress.Kind != AttachmentFile && ingress.Kind != AttachmentAudio && ingress.Kind != AttachmentVideo {
		return InputResource{}, fmt.Errorf("unsupported input kind %q", ingress.Kind)
	}
	itemKey, err := m.itemKey(ingress)
	if err != nil {
		return InputResource{}, err
	}
	if itemKey != "" {
		existing, err := m.getByItemKey(ctx, sessionID, itemKey)
		if err == nil {
			return existing, nil
		}
		if err != dao.ErrNoRows {
			return InputResource{}, err
		}
	}

	maxBytes := m.policy.MaxFileBytes
	if ingress.Kind == AttachmentImage {
		maxBytes = m.policy.MaxImageBytes
	}
	if ingress.SizeHint > maxBytes {
		return InputResource{}, fmt.Errorf("input exceeds %d bytes", maxBytes)
	}

	root, err := m.inputRoot()
	if err != nil {
		return InputResource{}, err
	}
	resourceID := session.GenerateID()
	if err := validatePathComponent(resourceID); err != nil {
		return InputResource{}, err
	}
	dir := filepath.Join(root, resourceID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return InputResource{}, fmt.Errorf("create input resource directory: %w", err)
	}
	removeResource := func() { _ = os.RemoveAll(dir) }
	tmp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		removeResource()
		return InputResource{}, fmt.Errorf("create input temporary file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		removeResource()
	}

	stream, err := ingress.Open(ctx)
	if err != nil {
		cleanup()
		return InputResource{}, fmt.Errorf("open %s input: %w", ingress.Origin, err)
	}
	if stream.Reader == nil {
		cleanup()
		return InputResource{}, fmt.Errorf("open %s input: empty reader", ingress.Origin)
	}
	defer stream.Reader.Close()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(stream.Reader, maxBytes+1))
	if err != nil {
		cleanup()
		return InputResource{}, fmt.Errorf("read input: %w", err)
	}
	if written > maxBytes {
		cleanup()
		return InputResource{}, fmt.Errorf("input exceeds %d bytes", maxBytes)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return InputResource{}, fmt.Errorf("sync input: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeResource()
		return InputResource{}, fmt.Errorf("close input: %w", err)
	}

	detectedType, err := detectInputMediaType(tmpName)
	if err != nil {
		removeResource()
		return InputResource{}, fmt.Errorf("detect input media type: %w", err)
	}
	mediaType := detectedType
	if ingress.Kind == AttachmentImage {
		if !strings.HasPrefix(strings.ToLower(detectedType), "image/") {
			removeResource()
			return InputResource{}, fmt.Errorf("image input has detected media type %q", detectedType)
		}
		if err := m.inspectImage(tmpName); err != nil {
			removeResource()
			return InputResource{}, err
		}
		mediaType = detectedType
	} else if detectedType == "application/octet-stream" && strings.TrimSpace(ingress.MediaTypeHint) != "" {
		// Preserve a trusted transport hint only when content sniffing cannot
		// identify the format (for example an AMR voice payload).
		mediaType = strings.TrimSpace(ingress.MediaTypeHint)
	}
	kind := normalizedInputKind(ingress.Kind, mediaType)
	if kind == AttachmentAudio || kind == AttachmentVideo {
		prefix := string(kind) + "/"
		if !strings.HasPrefix(strings.ToLower(mediaType), prefix) {
			removeResource()
			return InputResource{}, fmt.Errorf("%s input has detected media type %q", kind, mediaType)
		}
	}
	filename := strings.TrimSpace(ingress.FilenameHint)
	if filename == "" {
		filename = strings.TrimSpace(stream.Filename)
	}
	filename = canonicalInputFilename(filename, mediaType)
	finalPath := filepath.Join(dir, filename)
	if err := os.Rename(tmpName, finalPath); err != nil {
		removeResource()
		return InputResource{}, fmt.Errorf("commit input: %w", err)
	}
	relativePath, err := filepath.Rel(m.workDir, finalPath)
	if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		removeResource()
		return InputResource{}, fmt.Errorf("input path escaped Runtime work directory")
	}
	now := time.Now().UTC()
	// The resource row is intentionally staged first. Durable admission binds
	// it to a Run in the same transaction as the intent, Run row, turn boundary,
	// and started event; a failed admission therefore cannot leave an attached
	// resource pointing at a nonexistent Run.
	status := "prepared"
	record := InputResource{
		ID: resourceID, SessionID: sessionID, RunID: "", Origin: strings.TrimSpace(ingress.Origin),
		EventID: strings.TrimSpace(ingress.EventID), ItemIndex: ingress.ItemIndex, ItemKey: itemKey,
		Kind: kind, Filename: filename, MediaType: mediaType, Bytes: written,
		SHA256: hex.EncodeToString(hash.Sum(nil)), RelativePath: filepath.ToSlash(relativePath),
		Status: status, CreatedAt: now,
	}
	err = session.WriteRootDatabase(ctx, m.sessionDir, func(tx *dao.Tx) error {
		if err := dao.NewInputResourceDAO(nil).Insert(ctx, tx, &dao.InputResourceRecord{
			ID: record.ID, SessionID: record.SessionID, RunID: record.RunID, Origin: record.Origin,
			EventID: record.EventID, ItemIndex: record.ItemIndex, ItemKey: record.ItemKey, Kind: string(record.Kind),
			Filename: record.Filename, MediaType: record.MediaType, Bytes: record.Bytes, SHA256: record.SHA256,
			RelativePath: record.RelativePath, Status: record.Status, CreatedAt: record.CreatedAt.Format(time.RFC3339Nano), Metadata: "{}",
		}); err != nil {
			return err
		}
		data, marshalErr := json.Marshal(map[string]any{
			"kind": record.Kind, "filename": record.Filename, "mediaType": record.MediaType,
			"bytes": record.Bytes, "sha256": record.SHA256, "relativePath": record.RelativePath,
		})
		if marshalErr != nil {
			return marshalErr
		}
		return session.AppendInputResourceEventTx(tx, session.InputResourceEvent{
			ID: "input-resource-" + record.ID + "-prepared", SessionID: record.SessionID,
			ResourceID: record.ID, EventType: "input_resource_prepared", Status: record.Status,
			Timestamp: record.CreatedAt, Data: data,
		})
	})
	if err == nil {
		return record, nil
	}
	removeResource()
	if itemKey != "" {
		existing, lookupErr := m.getByItemKey(ctx, sessionID, itemKey)
		if lookupErr == nil {
			return existing, nil
		}
	}
	return InputResource{}, fmt.Errorf("persist input resource: %w", err)
}

// Discard removes an unbound draft resource from the project input area while
// retaining a durable deleted record and canonical lifecycle event.
func (m *InputMaterializer) Discard(ctx context.Context, sessionID, resourceID string) error {
	return m.deleteResource(ctx, sessionID, resourceID, false)
}

// Delete explicitly removes a resource, including one already attached to a
// Run. The record remains as an audit/replay tombstone.
func (m *InputMaterializer) Delete(ctx context.Context, sessionID, resourceID string) error {
	return m.deleteResource(ctx, sessionID, resourceID, true)
}

func (m *InputMaterializer) deleteResource(ctx context.Context, sessionID, resourceID string, allowAttached bool) error {
	if m == nil {
		return fmt.Errorf("input materializer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return err
	}
	if err := validatePathComponent(resourceID); err != nil {
		return err
	}
	var relativePath, runID, status string
	err := session.WriteRootDatabase(ctx, m.sessionDir, func(tx *dao.Tx) error {
		record, err := dao.NewInputResourceDAO(nil).Find(ctx, tx, sessionID, resourceID)
		if err != nil {
			return err
		}
		relativePath, runID, status = record.RelativePath, record.RunID, record.Status
		if status == "deleted" {
			return nil
		}
		if runID != "" && !allowAttached {
			return fmt.Errorf("input resource %s is attached to Run %s", resourceID, runID)
		}
		if err := dao.NewInputResourceDAO(nil).UpdateStatus(ctx, tx, sessionID, resourceID, "deleted"); err != nil {
			return err
		}
		return session.AppendInputResourceEventTx(tx, session.InputResourceEvent{
			ID: "input-resource-" + resourceID + "-deleted", SessionID: sessionID,
			ResourceID: resourceID, RunID: runID, EventType: "input_resource_deleted",
			Status: "deleted", Data: json.RawMessage(`{"reason":"runtime_discard"}`),
		})
	})
	if err != nil {
		return err
	}
	if relativePath != "" {
		path, pathErr := m.resourcePath(relativePath)
		if pathErr != nil {
			return pathErr
		}
		if removeErr := os.RemoveAll(filepath.Dir(path)); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
	}
	return nil
}

// Cleanup removes expired unbound drafts, marks missing records, and leaves
// attached resources untouched. Filesystem deletion happens after the durable
// status transaction so a retry can safely finish interrupted cleanup.
func (m *InputMaterializer) Cleanup(ctx context.Context, sessionID string, now time.Time) (int, error) {
	if m == nil {
		return 0, fmt.Errorf("input materializer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	type candidate struct{ id, relativePath, runID, status string }
	var remove []candidate
	err := session.WriteRootDatabase(ctx, m.sessionDir, func(tx *dao.Tx) error {
		records, err := dao.NewInputResourceDAO(nil).List(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		for _, record := range records {
			item := candidate{id: record.ID, relativePath: record.RelativePath, runID: record.RunID, status: record.Status}
			path, pathErr := m.resourcePath(item.relativePath)
			statErr := pathErr
			if statErr == nil {
				_, statErr = os.Stat(path)
			}
			if statErr != nil && os.IsNotExist(statErr) && item.status != "deleted" && item.status != "missing" {
				if err := dao.NewInputResourceDAO(nil).UpdateStatus(ctx, tx, sessionID, item.id, "missing"); err != nil {
					return err
				}
				if err := session.AppendInputResourceEventTx(tx, session.InputResourceEvent{
					ID: "input-resource-" + item.id + "-missing", SessionID: sessionID, ResourceID: item.id,
					RunID: item.runID, EventType: "input_resource_missing", Status: "missing",
					Timestamp: now, Data: json.RawMessage(`{"reason":"file_missing"}`),
				}); err != nil {
					return err
				}
			}
			if item.status == "prepared" && item.runID == "" {
				createdAt, err := dao.NewInputResourceDAO(nil).CreatedAt(ctx, tx, sessionID, item.id)
				if err != nil {
					return err
				}
				created, _ := time.Parse(time.RFC3339Nano, createdAt)
				if !created.IsZero() && now.Sub(created) >= m.policy.DraftMaxAge {
					if err := dao.NewInputResourceDAO(nil).DeleteDraft(ctx, tx, sessionID, item.id); err != nil {
						return err
					}
					if err := session.AppendInputResourceEventTx(tx, session.InputResourceEvent{
						ID: "input-resource-" + item.id + "-deleted", SessionID: sessionID, ResourceID: item.id,
						EventType: "input_resource_deleted", Status: "deleted", Timestamp: now,
						Data: json.RawMessage(`{"reason":"draft_expired"}`),
					}); err != nil {
						return err
					}
					remove = append(remove, item)
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, item := range remove {
		path, pathErr := m.resourcePath(item.relativePath)
		if pathErr != nil {
			return len(remove), pathErr
		}
		if removeErr := os.RemoveAll(filepath.Dir(path)); removeErr != nil && !os.IsNotExist(removeErr) {
			return len(remove), removeErr
		}
	}
	return len(remove), nil
}

func (m *InputMaterializer) Get(ctx context.Context, sessionID, resourceID string) (InputResource, error) {
	if m == nil {
		return InputResource{}, fmt.Errorf("input materializer is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validatePathComponent(sessionID); err != nil {
		return InputResource{}, err
	}
	if err := validatePathComponent(resourceID); err != nil {
		return InputResource{}, err
	}
	var record InputResource
	err := session.QueryRootDatabase(m.sessionDir, func(db *dao.Database) error {
		stored, err := dao.NewInputResourceDAO(db.Bun()).Find(ctx, db.Bun(), sessionID, resourceID)
		if err != nil {
			return err
		}
		return mapInputResourceRecord(stored, &record)
	})
	return record, err
}

func (m *InputMaterializer) getByItemKey(ctx context.Context, sessionID, itemKey string) (InputResource, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var record InputResource
	err := session.QueryRootDatabase(m.sessionDir, func(db *dao.Database) error {
		stored, err := dao.NewInputResourceDAO(db.Bun()).FindByItemKey(ctx, sessionID, itemKey)
		if err != nil {
			return err
		}
		return mapInputResourceRecord(stored, &record)
	})
	return record, err
}

type rowScanner interface {
	Scan(...any) error
}

func mapInputResourceRecord(stored *dao.InputResourceRecord, record *InputResource) error {
	if stored == nil {
		return dao.ErrNoRows
	}
	record.ID, record.SessionID, record.RunID, record.Origin = stored.ID, stored.SessionID, stored.RunID, stored.Origin
	record.EventID, record.ItemIndex, record.ItemKey = stored.EventID, stored.ItemIndex, stored.ItemKey
	record.Kind, record.Filename, record.MediaType = AttachmentKind(stored.Kind), stored.Filename, stored.MediaType
	record.Bytes, record.SHA256, record.RelativePath, record.Status = stored.Bytes, stored.SHA256, stored.RelativePath, stored.Status
	record.CreatedAt, _ = time.Parse(time.RFC3339Nano, stored.CreatedAt)
	return nil
}

func (m *InputMaterializer) inputRoot() (string, error) {
	workDir, err := filepath.EvalSymlinks(m.workDir)
	if err != nil {
		return "", fmt.Errorf("resolve input work directory: %w", err)
	}
	root := filepath.Join(workDir, ".mothx", "tmp", "inputs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create input root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve input root: %w", err)
	}
	rel, err := filepath.Rel(workDir, resolvedRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("input root escaped Runtime work directory")
	}
	return resolvedRoot, nil
}

func (m *InputMaterializer) resourcePath(relativePath string) (string, error) {
	if strings.TrimSpace(relativePath) == "" || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("invalid input relative path")
	}
	path := filepath.Join(m.workDir, filepath.FromSlash(relativePath))
	rel, err := filepath.Rel(m.workDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid input relative path")
	}
	return path, nil
}

func (m *InputMaterializer) inspectImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("inspect image input: %w", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("inspect image input: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("inspect image input: invalid dimensions %dx%d", cfg.Width, cfg.Height)
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > m.policy.MaxImagePixels {
		return fmt.Errorf("image input has %d pixels (max %d)", pixels, m.policy.MaxImagePixels)
	}
	return nil
}

func (m *InputMaterializer) itemKey(ingress InputIngress) (string, error) {
	identity := strings.TrimSpace(ingress.EventID)
	if identity == "" {
		identity = strings.TrimSpace(ingress.Reference)
	}
	if identity == "" {
		return "", nil
	}
	key, err := m.installationKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.TrimSpace(ingress.Origin)))
	_, _ = mac.Write([]byte("\x00"))
	_, _ = mac.Write([]byte(identity))
	_, _ = mac.Write([]byte("\x00"))
	_, _ = mac.Write([]byte(strconv.Itoa(ingress.ItemIndex)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (m *InputMaterializer) installationKey() ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("input materializer is nil")
	}
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	if len(m.itemKeyKey) > 0 {
		return append([]byte(nil), m.itemKeyKey...), nil
	}
	path := filepath.Join(m.sessionDir, ".runtime-input-key")
	if err := os.MkdirAll(m.sessionDir, 0700); err != nil {
		return nil, fmt.Errorf("create Runtime key directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("generate Runtime input key: %w", err)
		}
		if _, err := file.Write(key); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("write Runtime input key: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("sync Runtime input key: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return nil, fmt.Errorf("close Runtime input key: %w", err)
		}
		m.itemKeyKey = key
		return append([]byte(nil), key...), nil
	}
	if !os.IsExist(err) {
		return nil, fmt.Errorf("open Runtime input key: %w", err)
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Runtime input key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("Runtime input key has invalid length %d", len(key))
	}
	m.itemKeyKey = append([]byte(nil), key...)
	return append([]byte(nil), key...), nil
}

func canonicalInputFilename(filename, mediaType string) string {
	filename = sanitizeAttachmentFilename(filename)
	extension := canonicalImageExtension(mediaType)
	if extension == "" {
		return filename
	}
	current := strings.ToLower(filepath.Ext(filename))
	if current == extension || (extension == ".jpg" && current == ".jpeg") {
		return filename
	}
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == "" || base == "." {
		base = "image"
	}
	return base + extension
}

func canonicalImageExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func detectInputMediaType(path string) (string, error) {
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
	detected := http.DetectContentType(buf[:n])
	if detected != "application/octet-stream" && detected != "application/zip" {
		return detected, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return detected, nil
	}
	if _, format, decodeErr := image.DecodeConfig(f); decodeErr == nil {
		switch strings.ToLower(format) {
		case "jpeg":
			return "image/jpeg", nil
		case "png":
			return "image/png", nil
		case "gif":
			return "image/gif", nil
		case "webp":
			return "image/webp", nil
		}
	}
	return detected, nil
}

// AcceptInput materializes every ephemeral resource and returns the canonical
// submission shape consumed by Agent Core.
func (r *SessionRuntime) AcceptInput(ctx context.Context, runID, text string, ingresses []InputIngress) (InputSubmission, error) {
	if err := r.ensureOpen(); err != nil {
		return InputSubmission{}, err
	}
	if len(ingresses) == 0 {
		return InputSubmission{Text: text}, nil
	}
	r.mu.RLock()
	inputs := r.Inputs
	sessionID := r.ID
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return InputSubmission{}, fmt.Errorf("input materializer is not bound to a session")
	}
	submission := InputSubmission{Text: text, Resources: make([]PreparedInput, 0, len(ingresses))}
	for index, ingress := range ingresses {
		if ingress.ItemIndex == 0 && index != 0 {
			ingress.ItemIndex = index
		}
		record, err := inputs.Prepare(ctx, sessionID, runID, ingress)
		if err != nil {
			r.DiscardInput(ctx, submission)
			return InputSubmission{}, err
		}
		submission.Resources = append(submission.Resources, record.Prepared())
	}
	return submission, nil
}

// PrepareInput stages one resource before a Run exists, as used by editors and
// clipboard UIs. The resulting ID/path is the only state an adapter retains.
func (r *SessionRuntime) PrepareInput(ctx context.Context, ingress InputIngress) (PreparedInput, error) {
	if err := r.ensureOpen(); err != nil {
		return PreparedInput{}, err
	}
	r.mu.RLock()
	inputs, sessionID := r.Inputs, r.ID
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return PreparedInput{}, fmt.Errorf("input materializer is not bound to a session")
	}
	record, err := inputs.Prepare(ctx, sessionID, "", ingress)
	if err != nil {
		return PreparedInput{}, err
	}
	return record.Prepared(), nil
}

// AttachPreparedInput validates staged Runtime resources and returns the
// canonical submission without copying or reconstructing adapter content.
func (r *SessionRuntime) AttachPreparedInput(ctx context.Context, text string, resources []PreparedInput) (InputSubmission, error) {
	if err := r.ensureOpen(); err != nil {
		return InputSubmission{}, err
	}
	r.mu.RLock()
	inputs, sessionID := r.Inputs, r.ID
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return InputSubmission{}, fmt.Errorf("input materializer is not bound to a session")
	}
	submission := InputSubmission{Text: text, Resources: append([]PreparedInput(nil), resources...)}
	for _, resource := range submission.Resources {
		if resource.ResourceID == "" {
			return InputSubmission{}, fmt.Errorf("prepared input resource ID is required")
		}
		record, err := inputs.Get(ctx, sessionID, resource.ResourceID)
		if err != nil {
			return InputSubmission{}, fmt.Errorf("resolve prepared input %s: %w", resource.ResourceID, err)
		}
		if record.Status == "deleted" || record.Status == "missing" {
			return InputSubmission{}, fmt.Errorf("prepared input %s is unavailable (status %s)", record.ID, record.Status)
		}
	}
	return submission, nil
}

// DiscardInput removes only unbound Runtime resources in a submission. It is
// intended for adapter admission failures and is safe to retry.
func (r *SessionRuntime) DiscardInput(ctx context.Context, input InputSubmission) {
	if r == nil {
		return
	}
	r.mu.RLock()
	inputs, sessionID := r.Inputs, r.ID
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return
	}
	for _, resource := range input.Resources {
		_ = inputs.Discard(ctx, sessionID, resource.ResourceID)
	}
}

// CleanupInputResources runs the Runtime-owned draft/missing reconciliation.
func (r *SessionRuntime) CleanupInputResources(ctx context.Context, now time.Time) (int, error) {
	if r == nil {
		return 0, fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	inputs, sessionID := r.Inputs, r.ID
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return 0, fmt.Errorf("input materializer is not bound to a session")
	}
	return inputs.Cleanup(ctx, sessionID, now)
}

// BuildUserMessage emits text plus a deterministic project-path manifest and,
// for models with native audio/video understanding, the inline media content
// blocks of eligible audio/video inputs. It never reads image or document bytes
// and never constructs provider image/file blocks: those stay workspace files
// the model inspects with tools. Inline media is the only user-input content
// conversion, so adapters never build provider media blocks.
func (r *SessionRuntime) BuildUserMessage(ctx context.Context, input InputSubmission) (provider.Message, error) {
	if err := r.ensureOpen(); err != nil {
		return provider.Message{}, err
	}
	knowledge := formatKnowledgeCapsules(input.KnowledgeCapsules)
	if len(input.Resources) == 0 {
		text := strings.TrimSpace(input.Text)
		if knowledge != "" {
			if text != "" {
				text += "\n\n"
			}
			text += knowledge
		}
		return provider.NewUserMessage(text), nil
	}
	r.mu.RLock()
	inputs := r.Inputs
	sessionID := r.ID
	model := r.Model
	r.mu.RUnlock()
	if inputs == nil || sessionID == "" {
		return provider.Message{}, fmt.Errorf("input materializer is not bound to a session")
	}
	records := make([]InputResource, 0, len(input.Resources))
	for _, prepared := range input.Resources {
		record, err := inputs.Get(ctx, sessionID, prepared.ResourceID)
		if err != nil {
			return provider.Message{}, fmt.Errorf("resolve input resource %s: %w", prepared.ResourceID, err)
		}
		records = append(records, record)
	}

	var mediaBlocks []provider.ContentBlock
	delivery := make(map[string]string, len(records))
	for _, record := range records {
		if record.Kind != AttachmentAudio && record.Kind != AttachmentVideo {
			continue
		}
		kind := string(record.Kind)
		if !model.SupportsInput(kind) {
			delivery[record.ID] = "workspace file only: the selected model does not support " + kind + " input"
			continue
		}
		data, err := inputs.readInlineData(record)
		if err != nil {
			delivery[record.ID] = "workspace file only: " + err.Error()
			continue
		}
		mediaBlocks = append(mediaBlocks, mediaContentBlock(record, data))
		delivery[record.ID] = "inline " + kind + " content attached to this message"
	}

	text := strings.TrimSpace(input.Text)
	manifest := inputs.buildManifest(records, delivery)
	if text != "" {
		text += "\n\n" + manifest
	} else {
		text = manifest
	}
	if knowledge != "" {
		text += "\n\n" + knowledge
	}
	if len(mediaBlocks) == 0 {
		return provider.NewUserMessage(text), nil
	}
	contents := make([]provider.ContentBlock, 0, 1+len(mediaBlocks))
	contents = append(contents, provider.ContentBlock{Type: "text", Text: text})
	contents = append(contents, mediaBlocks...)
	return provider.Message{Role: "user", Content: text, Contents: contents, Timestamp: time.Now()}, nil
}

// readInlineData reads an input resource payload for inline provider content.
// The inline cap keeps oversized recordings out of requests and persisted
// message entries; those stay workspace files with a manifest note.
func (m *InputMaterializer) readInlineData(record InputResource) ([]byte, error) {
	if record.Bytes > m.policy.MaxInlineMediaBytes {
		return nil, fmt.Errorf("media is %d bytes, above the %d-byte inline limit", record.Bytes, m.policy.MaxInlineMediaBytes)
	}
	path, err := m.resourcePath(record.RelativePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read media file: %w", err)
	}
	if int64(len(data)) > m.policy.MaxInlineMediaBytes {
		return nil, fmt.Errorf("media is %d bytes, above the %d-byte inline limit", len(data), m.policy.MaxInlineMediaBytes)
	}
	return data, nil
}

// mediaContentBlock converts one Runtime-owned input resource into the
// provider-neutral media content block carried by the user message.
func mediaContentBlock(record InputResource, data []byte) provider.ContentBlock {
	encoded := base64.StdEncoding.EncodeToString(data)
	switch record.Kind {
	case AttachmentAudio:
		return provider.ContentBlock{Type: "audio", Audio: &provider.AudioContent{
			MimeType: record.MediaType, Format: audioWireFormat(record.MediaType),
			Data: encoded, Bytes: len(data),
		}}
	default:
		return provider.ContentBlock{Type: "video", Video: &provider.VideoContent{
			MimeType: record.MediaType, Data: encoded, Bytes: len(data),
		}}
	}
}

// audioWireFormat maps an audio media type to the wire format name expected by
// OpenAI-compatible input_audio parts.
func audioWireFormat(mediaType string) string {
	base := strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	switch base {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	default:
		format := strings.TrimPrefix(strings.TrimPrefix(base, "audio/"), "x-")
		if format == "" {
			return "wav"
		}
		return format
	}
}

// normalizedInputKind derives the canonical input kind from the declared kind
// and the sniffed media type. Generic file inputs carrying audio/* or video/*
// payloads become media inputs so every adapter shares one normalization rule;
// image and explicit media kinds are preserved.
func normalizedInputKind(kind AttachmentKind, mediaType string) AttachmentKind {
	lower := strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case kind == AttachmentAudio || kind == AttachmentVideo || kind == AttachmentImage:
		return kind
	case strings.HasPrefix(lower, "audio/"):
		return AttachmentAudio
	case strings.HasPrefix(lower, "video/"):
		return AttachmentVideo
	default:
		return AttachmentFile
	}
}

func (m *InputMaterializer) buildManifest(records []InputResource, delivery map[string]string) string {
	var b strings.Builder
	b.WriteString("[Runtime-managed input files for this request]\n")
	for _, record := range records {
		status := "available"
		path, err := m.resourcePath(record.RelativePath)
		if err != nil {
			status = "invalid"
		} else if info, statErr := os.Stat(path); statErr != nil || !info.Mode().IsRegular() {
			status = "missing"
		}
		fmt.Fprintf(&b, "- path: %s\n  name: %s\n  mediaType: %s\n  bytes: %d\n  status: %s\n",
			record.RelativePath, record.Filename, record.MediaType, record.Bytes, status)
		if note := delivery[record.ID]; note != "" {
			fmt.Fprintf(&b, "  delivered: %s\n", note)
		}
	}
	b.WriteString("\nDecide whether a file needs inspection. Use read for files you choose to read,\n")
	b.WriteString("or use an appropriate available Skill/tool when specialized parsing is useful.\n")
	b.WriteString("Do not claim to have examined a file that you did not read.")
	return b.String()
}
