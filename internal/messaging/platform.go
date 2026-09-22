// Package messaging defines the messaging platform abstraction for serve channels.
// Each platform (WeChat, Feishu, etc.) implements the Platform interface.
package messaging

import (
	"context"
	"io"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// Platform defines the interface that all messaging platform adapters must implement.
type Platform interface {
	// Name returns the platform identifier (e.g. "wechat", "feishu").
	Name() string
	// Start begins receiving messages. Blocks until ctx is cancelled or Stop is called.
	Start(ctx context.Context, handler MessageHandler) error
	// Stop gracefully shuts down the platform connection.
	Stop() error
	// SendMessage sends a text message to a specific chat.
	SendMessage(ctx context.Context, chatID string, text string) error
	// IsConnected reports whether the platform is currently connected.
	IsConnected() bool
}

// Readiness is implemented by transports that can distinguish successful
// startup from the long-running receive loop. Serve uses it during hot
// replacement so a failed candidate never takes down the healthy instance.
// Implementations that do not expose readiness retain the legacy immediate
// promotion behavior.
type Readiness interface {
	Ready() <-chan error
}

// MessageHandler is called for each incoming message. Its result is a
// platform-neutral delivery projection: transports render the text and, when
// their capability permits, execute its opaque media operations.
type MessageHandler func(ctx context.Context, msg InboundMessage) (MessageResponse, error)

// MessageResponse is a transport projection of one canonical Agent result.
// Text remains available to every platform; Attachments carry no local path or
// provider reference and can only be opened through Runtime-supplied closures.
type MessageResponse struct {
	Text         string
	TextDelivery *OutboundText
	// TextDeliveries is the ordered Runtime projection for caption and
	// fallback operations. TextDelivery remains the first-operation
	// compatibility field for older embedders.
	TextDeliveries []OutboundText
	Attachments    []OutboundAttachment
}

// OutboundText is the Runtime-owned caption/fallback operation projected to a
// transport. Prepare claims its durable operation(s) before network I/O;
// Complete reports the transport result through the Runtime fence.
type OutboundText struct {
	ID             string
	RunID          string
	TargetID       string
	ReplyMessageID string
	ReplyContext   string
	// Text is the exact payload for this durable operation. It lets a
	// transport preserve operation boundaries when MessageResponse.Text is a
	// compatibility summary containing more than one text operation.
	Text     string
	Prepare  func(context.Context) error
	Complete func(context.Context, string, string, string)
}

// OutboundAttachment is an already-authorized media delivery operation. The
// platform calls Open only while delivering and reports the terminal outcome
// through Complete so the Runtime-owned delivery record stays canonical.
type OutboundAttachment struct {
	ID                string
	RunID             string
	TargetID          string
	ReplyContext      string
	UploadOperationID string
	SendOperationID   string
	ProviderAssetID   string
	ProviderState     []byte
	Kind              AttachmentKind
	Filename          string
	MediaType         string
	Prepare           func(context.Context) error
	// ProgressUpload persists provider state while the upload operation keeps
	// its lease. It is called before the next provider phase.
	ProgressUpload func(context.Context, string, string, string, string)
	CompleteUpload func(context.Context, string, string, string, string)
	PrepareSend    func(context.Context) error
	CompleteSend   func(context.Context, string, string, string, string)
	Open           func(context.Context) (io.ReadCloser, error)
	// Complete remains for transports that perform upload and send in one
	// opaque call. New staged transports should use the phase callbacks above.
	Complete func(ctx context.Context, status, providerMessageID, failureCode string)
}

// DurableDeliveryRequest is the Runtime-owned durable outbox projection given
// to a platform when a process restart or background worker replays one
// operation. The platform receives only the frozen target/context and an
// authorized artifact reader; it never opens the session database itself.
type DurableDeliveryRequest struct {
	Intent            session.DeliveryIntent
	Operation         session.DeliveryOperation
	Dependency        *session.DeliveryOperation
	Caption           string
	ArtifactKind      AttachmentKind
	ArtifactFilename  string
	ArtifactMediaType string
	OpenArtifact      func(context.Context) (io.ReadCloser, error)
}

// DurableDeliveryResult is the transport result for one claimed operation.
// ProviderState is an opaque checkpoint that the Runtime persists before the
// next operation or retry. A send whose result is not trustworthy must return
// uncertain so recovery does not blindly duplicate it.
type DurableDeliveryResult struct {
	Status            string
	ProviderAssetID   string
	ProviderMessageID string
	ProviderState     []byte
	FailureCode       string
	NextAttemptAt     *time.Time
}

// DurableDeliveryExecutor is implemented by platforms with native durable
// delivery support. It is deliberately separate from Platform so lightweight
// or third-party adapters remain text-only without implementing recovery.
type DurableDeliveryExecutor interface {
	ExecuteDurableDelivery(context.Context, DurableDeliveryRequest) (DurableDeliveryResult, error)
}

// StatusCallbackSetter is an optional interface platforms can implement to receive
// connection status change notifications.
type StatusCallbackSetter interface {
	SetStatusCallback(callback func(connected bool))
}

// InboundMessage represents a message received from a messaging platform.
type InboundMessage struct {
	Platform string // "wechat", "feishu", etc.
	ChatID   string // Conversation/chat identifier
	UserID   string // Sender user ID
	// MessageID is an optional provider-native event/message identifier. It is
	// used only for durable background idempotency when a platform supplies one.
	MessageID string
	UserName  string    // Sender display name
	Text      string    // Message text content
	Timestamp time.Time // When the message was sent
	// Attachments contains opaque, platform-authenticated media references.
	// The channel dispatcher turns these into Runtime-owned attachments before
	// any Agent is constructed. Transport adapters must not put a public URL or
	// credential in Reference.
	Attachments []PlatformAttachment
	// ReplyContext is an opaque platform-owned value (for example WeChat's
	// context_token) captured at ingress and frozen by Runtime delivery plans.
	ReplyContext string

	// ProgressFunc is called to send intermediate progress updates during agent execution.
	// If nil, no progress updates are sent.
	ProgressFunc func(text string)
}

// AttachmentKind identifies the media class exposed by a transport. It stays
// transport-neutral and is converted to agentruntime.AttachmentKind only at
// the shared Runtime boundary.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentFile  AttachmentKind = "file"
	AttachmentAudio AttachmentKind = "audio"
	AttachmentVideo AttachmentKind = "video"
)

// AttachmentStream is a one-shot authenticated download supplied by a
// transport adapter. The Runtime owns copying it into its private session
// store and closes Reader after use.
type AttachmentStream struct {
	Reader      io.ReadCloser
	Filename    string
	MediaType   string
	ContentSize int64
}

// PlatformAttachment is an inbound transport reference, not a persisted
// attachment or provider input. Open must authenticate against the platform's
// own API and may only use the opaque Reference from the event that created
// this value.
type PlatformAttachment struct {
	Reference string
	Kind      AttachmentKind
	Filename  string
	MediaType string
	SizeHint  int64
	MessageID string
	Open      func(context.Context) (AttachmentStream, error)
}
