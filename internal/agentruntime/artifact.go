package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/tools"
)

// ArtifactCollector owns the generated artifacts registered during one
// Runtime run. It is created by SessionRuntime before Agent construction so
// publish_artifact participates in the frozen, canonical tool registry.
type ArtifactCollector struct {
	runtime  *SessionRuntime
	runID    string
	mu       sync.Mutex
	items    []SessionAttachment
	closed   bool
	observer func(SessionAttachment)
}

// SetObserver installs an optional adapter projection hook. The observer is
// called with each artifact record after it has been successfully copied and
// persisted, so projections never announce content that does not exist. It is
// additive and nil-safe: a nil observer removes any previously installed hook,
// and an observer panic is contained so it can never affect the registration
// flow that owns the durable artifact state.
func (c *ArtifactCollector) SetObserver(observer func(SessionAttachment)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.observer = observer
	c.mu.Unlock()
}

// notifyObserver projects one persisted artifact through the optional
// observer. The collector lock is not held while the observer runs, and a
// panic inside adapter projection code is deliberately recovered: the durable
// registration has already succeeded and must stay authoritative.
func (c *ArtifactCollector) notifyObserver(record SessionAttachment) {
	if c == nil {
		return
	}
	c.mu.Lock()
	observer := c.observer
	c.mu.Unlock()
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer(record)
}

// BeginArtifactCollection installs the Runtime-owned publication tool for one
// run. The caller must Close the collection after the Agent stream reaches a
// terminal state. It deliberately has no adapter-specific behavior.
func (r *SessionRuntime) BeginArtifactCollection(runID string) (*ArtifactCollector, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("artifact run ID is required")
	}
	r.mu.RLock()
	enabled := r.ArtifactEnabled
	service := r.Attachments
	registry := r.Registry
	sessionID := r.ID
	workDir := r.WorkDir
	r.mu.RUnlock()
	if !enabled {
		return nil, nil
	}
	if service == nil || registry == nil || sessionID == "" || workDir == "" {
		return nil, fmt.Errorf("artifact runtime is not bound to a session and registry")
	}
	collector := &ArtifactCollector{runtime: r, runID: runID}
	registry.Register(NewPublishArtifactTool(collector))
	return collector, nil
}

// Artifacts returns a stable copy of every artifact successfully copied to
// Runtime-owned private storage during this run.
func (c *ArtifactCollector) Artifacts() []SessionAttachment {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]SessionAttachment(nil), c.items...)
}

// Close removes this run's dynamic tool. Channel runs are serialized by their
// SessionRuntime lock; identity comparison still prevents an old collector
// from removing a newer run's tool if a caller closes late.
func (c *ArtifactCollector) Close() {
	if c == nil || c.runtime == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	c.runtime.mu.RLock()
	registry := c.runtime.Registry
	c.runtime.mu.RUnlock()
	if registry == nil {
		return
	}
	if current, ok := registry.Get("publish_artifact"); ok {
		if tool, ok := current.(*PublishArtifactTool); ok && tool.collector == c {
			registry.Remove("publish_artifact")
		}
	}
}

// Register copies a regular file from the Runtime work directory to private
// attachment storage. It refuses symlink escapes and persists only the copied
// content's generated attachment ID, never the source path.
func (c *ArtifactCollector) Register(ctx context.Context, sourcePath, filename, requestedKind string) (SessionAttachment, error) {
	if c == nil || c.runtime == nil {
		return SessionAttachment{}, fmt.Errorf("artifact collector is unavailable")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return SessionAttachment{}, fmt.Errorf("artifact collector is closed")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return SessionAttachment{}, fmt.Errorf("artifact path is required")
	}
	c.runtime.mu.RLock()
	service := c.runtime.Attachments
	sessionID := c.runtime.ID
	workDir := c.runtime.WorkDir
	c.runtime.mu.RUnlock()
	if service == nil || sessionID == "" || workDir == "" {
		return SessionAttachment{}, fmt.Errorf("artifact runtime is not bound to a session")
	}
	resolvedWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return SessionAttachment{}, fmt.Errorf("resolve artifact work directory: %w", err)
	}
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(resolvedWorkDir, sourcePath)
	}
	resolvedSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return SessionAttachment{}, fmt.Errorf("resolve artifact path: %w", err)
	}
	rel, err := filepath.Rel(resolvedWorkDir, resolvedSource)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return SessionAttachment{}, fmt.Errorf("artifact path must stay within the Runtime work directory")
	}
	info, err := os.Stat(resolvedSource)
	if err != nil {
		return SessionAttachment{}, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return SessionAttachment{}, fmt.Errorf("artifact path must be a regular file")
	}

	kind, mediaType, err := classifyArtifact(resolvedSource, requestedKind)
	if err != nil {
		return SessionAttachment{}, err
	}
	if strings.TrimSpace(filename) == "" {
		filename = filepath.Base(resolvedSource)
	}
	record, err := service.acceptArtifact(ctx, sessionID, c.runID, artifactIngress{
		Origin: "tool:publish_artifact", Reference: "runtime-artifact", Kind: kind,
		Filename: filename, MediaType: mediaType, SizeHint: info.Size(),
		Open: func(context.Context) (artifactStream, error) {
			file, err := os.Open(resolvedSource)
			if err != nil {
				return artifactStream{}, err
			}
			return artifactStream{Reader: file, Filename: filename, MediaType: mediaType, ContentSize: info.Size()}, nil
		},
	})
	if err != nil {
		return SessionAttachment{}, err
	}
	if err := service.SetStatus(ctx, sessionID, record.ID, "generated"); err != nil {
		return SessionAttachment{}, err
	}
	record.Status = "generated"
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return SessionAttachment{}, fmt.Errorf("artifact collector closed while registering artifact")
	}
	c.items = append(c.items, record)
	c.mu.Unlock()
	c.notifyObserver(record)
	return record, nil
}

func classifyArtifact(path, requestedKind string) (AttachmentKind, string, error) {
	requestedKind = strings.ToLower(strings.TrimSpace(requestedKind))
	mediaType, err := detectAttachmentMediaType(path)
	if err != nil {
		return "", "", fmt.Errorf("detect artifact media type: %w", err)
	}
	isImage := strings.HasPrefix(strings.ToLower(mediaType), "image/")
	switch requestedKind {
	case "", "auto":
		if isImage {
			return AttachmentImage, mediaType, nil
		}
		return AttachmentFile, mediaType, nil
	case string(AttachmentImage):
		if !isImage {
			return "", "", fmt.Errorf("artifact requested as image but detected %q", mediaType)
		}
		return AttachmentImage, mediaType, nil
	case string(AttachmentFile):
		return AttachmentFile, mediaType, nil
	case string(AttachmentAudio):
		if !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
			return "", "", fmt.Errorf("artifact requested as audio but detected %q", mediaType)
		}
		return AttachmentAudio, mediaType, nil
	case string(AttachmentVideo):
		if !strings.HasPrefix(strings.ToLower(mediaType), "video/") {
			return "", "", fmt.Errorf("artifact requested as video but detected %q", mediaType)
		}
		return AttachmentVideo, mediaType, nil
	default:
		return "", "", fmt.Errorf("unsupported artifact kind %q", requestedKind)
	}
}

// PublishArtifactTool is the only Agent-facing way to declare a local file as
// a delivery artifact. It copies the file before reporting success, so later
// Agent writes cannot silently mutate a file that is about to be delivered.
type PublishArtifactTool struct {
	collector *ArtifactCollector
}

func NewPublishArtifactTool(collector *ArtifactCollector) *PublishArtifactTool {
	return &PublishArtifactTool{collector: collector}
}

func (t *PublishArtifactTool) Name() string { return "publish_artifact" }

func (t *PublishArtifactTool) Description() string {
	return "Publish a regular file that you created in the current working directory as a generated attachment. Use this only after the file is complete. The file is copied to Runtime-managed storage and may be delivered by the active channel."
}

func (t *PublishArtifactTool) PromptSnippet() string {
	return "Publish a completed work-directory file as a channel artifact"
}

func (t *PublishArtifactTool) PromptGuidelines() []string {
	return []string{"When you create a file intended for the user, call publish_artifact with its work-directory-relative path. Do not claim a file is deliverable merely because you mentioned its path."}
}

func (t *PublishArtifactTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to a completed regular file, relative to the current working directory"},"filename":{"type":"string","description":"Optional user-facing attachment filename"},"kind":{"type":"string","enum":["auto","image","file","audio","video"],"description":"Optional attachment kind; auto detects an image from its bytes"}},"required":["path"]}`)
}

func (t *PublishArtifactTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t == nil || t.collector == nil {
		return tools.ToolResult{}, fmt.Errorf("artifact publisher is unavailable")
	}
	path, _ := params["path"].(string)
	filename, _ := params["filename"].(string)
	kind, _ := params["kind"].(string)
	record, err := t.collector.Register(ctx, path, filename, kind)
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(fmt.Sprintf("Published generated %s %q as attachment %s.", record.Kind, record.Filename, record.ID)), nil
}

var _ tools.Tool = (*PublishArtifactTool)(nil)
