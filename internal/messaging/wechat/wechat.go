package wechat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oschina/mothx/internal/messaging"
)

// Bot implements messaging.Platform for WeChat via the iLink protocol.
type Bot struct {
	client         *Client
	creds          *Credentials
	credPath       string
	autoTyping     bool
	connected      bool
	stopped        bool
	mu             sync.Mutex
	cancelPoll     context.CancelFunc
	contextTokens  sync.Map // map[userID]contextToken
	pendingReplies sync.Map // map[userID]*pendingReplyState
	cursor         string
	statusCallback func(connected bool)
	ready          chan error
}

const (
	wechatMaxRepliesPerMessage = 10
	wechatMessageTextLimit     = 4000
)

type pendingReplyState struct {
	mu           sync.Mutex
	chunks       []string
	lastProgress string
}

type replySession struct {
	bot          *Bot
	userID       string
	contextToken string
	remaining    int
}

func (b *Bot) pendingReply(userID string) *pendingReplyState {
	value, _ := b.pendingReplies.LoadOrStore(userID, &pendingReplyState{})
	return value.(*pendingReplyState)
}

func (b *Bot) newReplySession(userID, contextToken string) *replySession {
	return &replySession{bot: b, userID: userID, contextToken: contextToken, remaining: wechatMaxRepliesPerMessage}
}

func (s *replySession) Send(ctx context.Context, text string) error {
	return s.send(ctx, text, "")
}

// SendWithClientID sends a caption using deterministic IDs derived from the
// Runtime operation. Long captions still split into bounded provider messages,
// with one stable child ID per chunk.
func (s *replySession) SendWithClientID(ctx context.Context, text, operationID string) error {
	return s.send(ctx, text, operationID)
}

func (s *replySession) send(ctx context.Context, text, operationID string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	chunks := chunkText(text, wechatMessageTextLimit-replyFooterLen(0))
	for i, chunk := range chunks {
		if s.remaining == 0 {
			s.queue(chunks[i:])
			return nil
		}
		clientID := ""
		if strings.TrimSpace(operationID) != "" {
			clientID = StableClientID(fmt.Sprintf("%s:%d", operationID, i))
		}
		if err := s.bot.sendChunkWithClientID(ctx, s.userID, chunk, s.contextToken, s.remaining-1, clientID); err != nil {
			return err
		}
		s.remaining--
	}
	return nil
}

func (s *replySession) SendProgress(ctx context.Context, text string) error {
	state := s.bot.pendingReply(s.userID)
	state.mu.Lock()
	state.lastProgress = text
	state.mu.Unlock()
	return s.Send(ctx, text)
}
func (s *replySession) queue(chunks []string) {
	state := s.bot.pendingReply(s.userID)
	state.mu.Lock()
	state.chunks = append(state.chunks, chunks...)
	state.mu.Unlock()
}

func (b *Bot) sendMore(ctx context.Context, userID, contextToken string) error {
	state := b.pendingReply(userID)
	state.mu.Lock()
	chunks := append([]string(nil), state.chunks...)
	state.chunks = nil
	lastProgress := state.lastProgress
	state.lastProgress = ""
	state.mu.Unlock()

	s := b.newReplySession(userID, contextToken)
	if len(chunks) > 0 {
		for _, chunk := range chunks {
			if err := s.Send(ctx, chunk); err != nil {
				return err
			}
		}
		return nil
	}
	if lastProgress != "" {
		return s.Send(ctx, lastProgress)
	}
	return nil
}

func replyFooterLen(remaining int) int {
	if remaining == 0 {
		return len("\n\n剩余推送次数: 0次\n输入 /more 继续接收消息。")
	}
	return len(fmt.Sprintf("\n\n剩余推送次数: %d次", remaining))
}

func replyFooter(remaining int) string {
	if remaining == 0 {
		return "\n\n剩余推送次数: 0次\n输入 /more 继续接收消息。"
	}
	return fmt.Sprintf("\n\n剩余推送次数: %d次", remaining)
}

type BotOptions struct {
	CredPath   string
	AutoTyping bool
}

// NewBot creates a new WeChat bot.
func NewBot(opts BotOptions) *Bot {
	return &Bot{
		client:     NewClient(),
		credPath:   opts.CredPath,
		autoTyping: opts.AutoTyping,
		ready:      make(chan error, 1),
	}
}

// Ready returns the one-shot startup result for the current Bot instance.
func (b *Bot) Ready() <-chan error { return b.ready }

func (b *Bot) signalReady(err error) {
	select {
	case b.ready <- err:
	default:
	}
}

// --- messaging.Platform implementation ---

func (b *Bot) Name() string { return "wechat" }

func (b *Bot) IsConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.connected
}

func (b *Bot) SetStatusCallback(callback func(connected bool)) {
	b.mu.Lock()
	b.statusCallback = callback
	b.mu.Unlock()
}

// setConnected reports the health of the iLink receive loop rather than the
// mere presence of local credentials. This keeps channel status truthful when
// a token exists but getupdates cannot be established.
func (b *Bot) setConnected(connected bool) {
	b.mu.Lock()
	changed := b.connected != connected
	b.connected = connected
	callback := b.statusCallback
	b.mu.Unlock()
	if changed && callback != nil {
		callback(connected)
	}
}

// finishReceiveLoop is the single shutdown path for both an explicit Stop and
// a parent context cancellation. It is intentionally idempotent: a platform
// replacement may call Stop while the receive loop is observing cancellation.
func (b *Bot) finishReceiveLoop() {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.stopped = true
	cancel := b.cancelPoll
	creds := b.creds
	wasConnected := b.connected
	b.connected = false
	callback := b.statusCallback
	b.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if wasConnected && callback != nil {
		callback(false)
	}
	if creds == nil || b.client == nil {
		return
	}
	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), defaultNotificationTimeout)
	defer notifyCancel()
	if err := b.client.NotifyStop(notifyCtx, creds.BaseURL, creds.Token); err != nil {
		log.Printf("[wechat] notify stop failed: %v", err)
	}
}

// Start begins long-poll message receiving. Blocks until ctx is cancelled.
func (b *Bot) Start(ctx context.Context, handler messaging.MessageHandler) error {
	// Load credentials
	creds, err := LoadCredentials(b.credPath)
	if err != nil || creds == nil {
		if err == nil {
			err = fmt.Errorf("wechat: no credentials found at %s", b.credPath)
		}
		b.signalReady(err)
		return fmt.Errorf("wechat: no credentials found at %s", b.credPath)
	}

	b.mu.Lock()
	b.creds = creds
	b.cursor = creds.GetUpdatesBuf
	b.connected = false
	b.stopped = false
	pollCtx, cancel := context.WithCancel(ctx)
	b.cancelPoll = cancel
	b.mu.Unlock()
	defer b.finishReceiveLoop()

	// iLink tracks bot online state independently from credential issuance.
	// Continue polling when this best-effort notification fails (matching the
	// Tencent reference implementation), but never report the channel healthy
	// until a getupdates request itself succeeds.
	if err := b.client.NotifyStart(pollCtx, creds.BaseURL, creds.Token); err != nil && pollCtx.Err() == nil {
		log.Printf("[wechat] notify start failed: %v", err)
	}
	b.signalReady(nil)

	log.Printf("[wechat] Long-poll loop started (user: %s)", creds.UserID)
	retryDelay := time.Second
	pollTimeout := defaultLongPollTimeout

	for {
		select {
		case <-pollCtx.Done():
			log.Printf("[wechat] Long-poll loop stopped")
			return nil
		default:
		}

		b.mu.Lock()
		currentCreds := b.creds
		cursor := b.cursor
		b.mu.Unlock()
		if currentCreds == nil {
			return fmt.Errorf("wechat: receive loop lost credentials")
		}

		updates, err := b.client.GetUpdatesWithTimeout(pollCtx, currentCreds.BaseURL, currentCreds.Token, cursor, pollTimeout)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil
			}

			b.setConnected(false)
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsSessionExpired() {
				log.Printf("[wechat] Session expired — re-login required")
				if clearErr := ClearCredentials(b.credPath); clearErr != nil {
					log.Printf("[wechat] clear stale credentials: %v", clearErr)
				}
				b.contextTokens = sync.Map{}
				b.mu.Lock()
				b.cursor = ""
				b.mu.Unlock()
				// Try re-login
				newCreds, loginErr := Login(pollCtx, b.client, LoginOptions{
					CredPath: b.credPath,
					Force:    true,
				})
				if loginErr != nil {
					log.Printf("[wechat] Re-login failed: %v", loginErr)
					if sleepErr := sleepCtx(pollCtx, retryDelay); sleepErr != nil {
						return nil
					}
					continue
				}
				b.mu.Lock()
				b.creds = newCreds
				b.cursor = newCreds.GetUpdatesBuf
				b.mu.Unlock()
				if notifyErr := b.client.NotifyStart(pollCtx, newCreds.BaseURL, newCreds.Token); notifyErr != nil && pollCtx.Err() == nil {
					log.Printf("[wechat] notify start after re-login failed: %v", notifyErr)
				}
				retryDelay = time.Second
				pollTimeout = defaultLongPollTimeout
				continue
			}

			log.Printf("[wechat] Poll error: %v", err)
			if sleepErr := sleepCtx(pollCtx, retryDelay); sleepErr != nil {
				return nil
			}
			if retryDelay < 10*time.Second {
				retryDelay *= 2
			}
			continue
		}

		b.setConnected(true)
		if timeout, ok := longPollTimeout(updates.LongPollingTimeoutMS); ok {
			pollTimeout = timeout
		}
		if updates.GetUpdatesBuf != "" && updates.GetUpdatesBuf != cursor {
			b.persistCursor(currentCreds, updates.GetUpdatesBuf)
		}
		retryDelay = time.Second

		for _, rawMsg := range updates.Msgs {
			var wire WireMessage
			if err := json.Unmarshal(rawMsg, &wire); err != nil {
				continue
			}

			// Remember context tokens
			b.rememberContext(&wire)

			// Only process user messages
			if wire.MessageType != MessageTypeUser {
				continue
			}

			text := extractText(wire.ItemList)
			attachments := b.inboundAttachments(&wire)
			if text == "" && len(attachments) == 0 {
				continue
			}
			messageID := wireMessageID(&wire)
			if messageID == "" {
				// Some iLink events omit message_id but still carry a stable
				// sequence/create-time identity. Keep that synthetic ID for
				// Runtime admission deduplication; ReplyContext remains the
				// provider reply token and is never replaced by this value.
				messageID = wireMessageIdentity(&wire)
			}

			msg := messaging.InboundMessage{
				Platform:     "wechat",
				ChatID:       wire.FromUserID,
				UserID:       wire.FromUserID,
				MessageID:    messageID,
				Text:         text,
				Timestamp:    time.UnixMilli(wire.CreateTimeMs),
				ReplyContext: wire.ContextToken,
				Attachments:  attachments,
			}

			// Show typing indicator
			if b.autoTyping {
				go b.sendTyping(pollCtx, wire.FromUserID)
			}

			// Handle message
			go func(m messaging.InboundMessage, ct string) {
				runCtx := context.WithoutCancel(pollCtx)
				reply := b.newReplySession(m.UserID, ct)
				if strings.TrimSpace(m.Text) == "/more" {
					if err := b.sendMore(runCtx, m.UserID, ct); err != nil {
						log.Printf("[wechat] More send error for %s: %v", m.UserID, err)
					}
					return
				}

				// Create progress buffer: max 7 progress lines per batch, reserve 3 for summary
				progressBuf := messaging.NewProgressBuffer(7, func(text string) {
					if err := reply.SendProgress(runCtx, text); err != nil {
						log.Printf("[wechat] Progress send error: %v", err)
					}
				})
				m.ProgressFunc = func(text string) {
					progressBuf.Add(text)
				}

				response, err := handler(runCtx, m)

				// Flush remaining progress lines before final summary
				progressBuf.Flush()

				if err != nil {
					log.Printf("[wechat] Handler error for %s: %v", m.UserID, err)
					response.Text = "⚠️ Error: " + err.Error()
				}
				textDeliveryBlocked := false
				textDeliveries := response.TextDeliveries
				if len(textDeliveries) == 0 && response.TextDelivery != nil {
					textDeliveries = []messaging.OutboundText{*response.TextDelivery}
				}
				if len(textDeliveries) > 0 {
					for _, delivery := range textDeliveries {
						text := delivery.Text
						if text == "" {
							text = response.Text
						}
						if delivery.Prepare != nil {
							if prepareErr := delivery.Prepare(runCtx); prepareErr != nil {
								log.Printf("[wechat] text delivery claim failed: %v", prepareErr)
								textDeliveryBlocked = true
								continue
							}
						}
						sendText := reply.Send
						if delivery.ID != "" {
							deliveryID := delivery.ID
							sendText = func(ctx context.Context, text string) error {
								return reply.SendWithClientID(ctx, text, deliveryID)
							}
						}
						if sendErr := sendText(runCtx, text); sendErr != nil {
							log.Printf("[wechat] Send error for %s: %v", m.UserID, sendErr)
							if delivery.Complete != nil {
								delivery.Complete(runCtx, wechatSendFailureStatus(sendErr), "", "send_text_failed")
							}
						} else {
							log.Printf("[wechat] Message sent to %s successfully (len=%d)", m.UserID, len(text))
							if delivery.Complete != nil {
								delivery.Complete(runCtx, "delivered", "", "")
							}
						}
					}
				} else if response.Text != "" {
					if sendErr := reply.Send(runCtx, response.Text); sendErr != nil {
						log.Printf("[wechat] Send error for %s: %v", m.UserID, sendErr)
					} else {
						log.Printf("[wechat] Message sent to %s successfully (len=%d)", m.UserID, len(response.Text))
					}
				} else {
					log.Printf("[wechat] Empty response for %s, not sending", m.UserID)
				}
				if textDeliveryBlocked {
					if b.autoTyping {
						b.stopTyping(runCtx, m.UserID)
					}
					return
				}
				for _, attachment := range response.Attachments {
					if attachment.Prepare != nil {
						if prepareErr := attachment.Prepare(runCtx); prepareErr != nil {
							log.Printf("[wechat] media delivery claim failed: %v", prepareErr)
							if attachment.CompleteUpload != nil {
								attachment.CompleteUpload(runCtx, "failed", "", "{}", "delivery_claim_failed")
							} else if attachment.Complete != nil {
								attachment.Complete(runCtx, "failed", "", "delivery_claim_failed")
							}
							continue
						}
					}
					if attachment.SendOperationID != "" || attachment.CompleteSend != nil {
						if sendErr := b.sendMediaAttachment(runCtx, attachment); sendErr != nil {
							log.Printf("[wechat] media delivery failed for %s: %v", attachment.ID, sendErr)
						}
					} else if attachment.Complete != nil {
						attachment.Complete(runCtx, "unsupported", "", "platform_media_unsupported")
					}
				}
				// Stop typing
				if b.autoTyping {
					b.stopTyping(runCtx, m.UserID)
				}
			}(msg, wire.ContextToken)
		}
	}
}

// Stop gracefully stops the bot.
func (b *Bot) Stop() error {
	b.finishReceiveLoop()
	return nil
}

func longPollTimeout(milliseconds int64) (time.Duration, bool) {
	if milliseconds <= 0 || milliseconds > int64((24*time.Hour)/time.Millisecond) {
		return 0, false
	}
	return time.Duration(milliseconds) * time.Millisecond, true
}

func (b *Bot) persistCursor(creds *Credentials, cursor string) {
	if b == nil || creds == nil || cursor == "" {
		return
	}
	b.mu.Lock()
	if b.creds != creds {
		b.mu.Unlock()
		return
	}
	b.cursor = cursor
	creds.GetUpdatesBuf = cursor
	b.mu.Unlock()
	if err := SaveCredentials(creds, b.credPath); err != nil {
		log.Printf("[wechat] save getupdates cursor: %v", err)
	}
}

// SendMessage sends a text message to a user.
func (b *Bot) SendMessage(ctx context.Context, chatID string, text string) error {
	ct, ok := b.contextTokens.Load(chatID)
	if !ok {
		return fmt.Errorf("no context_token for user %s", chatID)
	}
	return b.sendText(ctx, chatID, text, ct.(string))
}

// --- Internal ---

func (b *Bot) sendText(ctx context.Context, userID, text, contextToken string) error {
	return b.newReplySession(userID, contextToken).Send(ctx, text)
}

func (b *Bot) sendChunk(ctx context.Context, userID, text, contextToken string, remaining int) error {
	b.mu.Lock()
	creds := b.creds
	b.mu.Unlock()

	if creds == nil {
		return fmt.Errorf("not logged in")
	}
	return b.sendChunkWithClientID(ctx, userID, text, contextToken, remaining, "")
}

func (b *Bot) sendChunkWithClientID(ctx context.Context, userID, text, contextToken string, remaining int, clientID string) error {
	b.mu.Lock()
	creds := b.creds
	b.mu.Unlock()
	if creds == nil {
		return fmt.Errorf("not logged in")
	}
	msg := BuildTextMessageWithClientID(creds.UserID, userID, contextToken, text+replyFooter(remaining), clientID)
	return b.client.SendMessage(ctx, creds.BaseURL, creds.Token, msg)
}

func (b *Bot) sendTyping(ctx context.Context, userID string) {
	ct, ok := b.contextTokens.Load(userID)
	if !ok {
		return
	}
	b.mu.Lock()
	creds := b.creds
	b.mu.Unlock()
	if creds == nil {
		return
	}
	config, err := b.client.GetConfig(ctx, creds.BaseURL, creds.Token, userID, ct.(string))
	if err != nil || config.TypingTicket == "" {
		return
	}
	b.client.SendTyping(ctx, creds.BaseURL, creds.Token, userID, config.TypingTicket, 1)
}

func (b *Bot) stopTyping(ctx context.Context, userID string) {
	ct, ok := b.contextTokens.Load(userID)
	if !ok {
		return
	}
	b.mu.Lock()
	creds := b.creds
	b.mu.Unlock()
	if creds == nil {
		return
	}
	config, err := b.client.GetConfig(ctx, creds.BaseURL, creds.Token, userID, ct.(string))
	if err != nil || config.TypingTicket == "" {
		return
	}
	b.client.SendTyping(ctx, creds.BaseURL, creds.Token, userID, config.TypingTicket, 2)
}

func (b *Bot) rememberContext(wire *WireMessage) {
	userID := wire.FromUserID
	if wire.MessageType == MessageTypeBot {
		userID = wire.ToUserID
	}
	if userID != "" && wire.ContextToken != "" {
		b.contextTokens.Store(userID, wire.ContextToken)
	}
}

func extractText(items []MessageItem) string {
	var parts []string
	var appendItem func(MessageItem)
	appendItem = func(item MessageItem) {
		if item.Type == ItemText && item.TextItem != nil {
			parts = append(parts, item.TextItem.Text)
		}
		if item.RefMsg != nil {
			for _, nested := range item.RefMsg.ItemList {
				appendItem(nested)
			}
			if item.RefMsg.MessageItem != nil {
				appendItem(*item.RefMsg.MessageItem)
			}
		}
	}
	for _, item := range items {
		appendItem(item)
	}
	return strings.Join(parts, "\n")
}

// inboundAttachments translates only media references carried by an
// authenticated iLink getupdates event. Its Open closures retain the opaque
// CDN reference and AES key inside the WeChat transport boundary; the shared
// Runtime receives decrypted bytes and persists no transport secret.
func (b *Bot) inboundAttachments(wire *WireMessage) []messaging.PlatformAttachment {
	if b == nil || b.client == nil || wire == nil {
		return nil
	}
	result := make([]messaging.PlatformAttachment, 0, len(wire.ItemList))
	messageIdentity := wireMessageIdentity(wire)
	for index, item := range wire.ItemList {
		items := []struct {
			item  MessageItem
			index int
		}{{item: item, index: index}}
		if item.RefMsg != nil {
			for nestedIndex, nested := range item.RefMsg.ItemList {
				items = append(items, struct {
					item  MessageItem
					index int
				}{item: nested, index: index*1000 + nestedIndex + 1})
			}
			if item.RefMsg.MessageItem != nil {
				items = append(items, struct {
					item  MessageItem
					index int
				}{item: *item.RefMsg.MessageItem, index: index*1000 + len(item.RefMsg.ItemList) + 1})
			}
		}
		for _, candidate := range items {
			item := candidate.item
			attachmentIndex := candidate.index
			var (
				kind      messaging.AttachmentKind
				media     CDNMedia
				aesKey    string
				filename  string
				mediaType string
				sizeHint  int64
				ok        bool
			)
			switch item.Type {
			case ItemImage:
				if item.ImageItem == nil || item.ImageItem.Media == nil {
					continue
				}
				kind = messaging.AttachmentImage
				media = *item.ImageItem.Media
				aesKey = item.ImageItem.AESKey
				filename = fmt.Sprintf("image-%s", messageIdentity)
				mediaType = "image/png"
				ok = true
			case ItemVoice:
				if item.VoiceItem == nil || item.VoiceItem.Media == nil {
					continue
				}
				kind = messaging.AttachmentAudio
				media = *item.VoiceItem.Media
				filename = item.VoiceItem.FileName
				if filename == "" {
					filename = fmt.Sprintf("voice-%s.amr", messageIdentity)
				}
				mediaType = "audio/amr"
				ok = true
			case ItemFile:
				if item.FileItem == nil || item.FileItem.Media == nil {
					continue
				}
				kind = messaging.AttachmentFile
				media = *item.FileItem.Media
				filename = item.FileItem.FileName
				sizeHint = parseMediaSize(item.FileItem.Len)
				ok = true
			case ItemVideo:
				if item.VideoItem == nil || item.VideoItem.Media == nil {
					continue
				}
				kind = messaging.AttachmentVideo
				media = *item.VideoItem.Media
				filename = item.VideoItem.FileName
				if filename == "" {
					filename = fmt.Sprintf("video-%s.mp4", messageIdentity)
				}
				mediaType = "video/mp4"
				ok = true
			}
			if !ok {
				continue
			}
			client := b.client
			mediaCopy, aesKeyCopy := media, aesKey
			filenameCopy, mediaTypeCopy, sizeHintCopy := filename, mediaType, sizeHint
			result = append(result, messaging.PlatformAttachment{
				Reference: fmt.Sprintf("wechat:%s:%d", messageIdentity, attachmentIndex),
				Kind:      kind, Filename: filenameCopy, MediaType: mediaTypeCopy, SizeHint: sizeHintCopy,
				MessageID: wireMessageID(wire),
				Open: func(ctx context.Context) (messaging.AttachmentStream, error) {
					reader, err := client.OpenCDNMedia(ctx, mediaCopy, aesKeyCopy)
					if err != nil {
						return messaging.AttachmentStream{}, err
					}
					return messaging.AttachmentStream{Reader: reader, Filename: filenameCopy, MediaType: mediaTypeCopy, ContentSize: sizeHintCopy}, nil
				},
			})
		}
	}
	return result
}

func wireMessageID(wire *WireMessage) string {
	if wire == nil || wire.MessageID == 0 {
		return ""
	}
	return fmt.Sprintf("%d", wire.MessageID)
}

func wireMessageIdentity(wire *WireMessage) string {
	if wire == nil {
		return "unknown"
	}
	if wire.MessageID != 0 {
		return fmt.Sprintf("%d", wire.MessageID)
	}
	if wire.Seq != 0 {
		return fmt.Sprintf("seq-%d", wire.Seq)
	}
	if wire.CreateTimeMs != 0 {
		return fmt.Sprintf("time-%d", wire.CreateTimeMs)
	}
	// A few fixtures and older iLink responses omit every native event ID.
	// Hash only the non-secret envelope and item structure so retries still
	// share a stable identity without persisting context tokens or media keys.
	payload, err := json.Marshal(struct {
		FromUserID  string        `json:"fromUserId"`
		ToUserID    string        `json:"toUserId"`
		MessageType MessageType   `json:"messageType"`
		ItemList    []MessageItem `json:"itemList"`
	}{FromUserID: wire.FromUserID, ToUserID: wire.ToUserID, MessageType: wire.MessageType, ItemList: wire.ItemList})
	if err != nil {
		return "unknown"
	}
	digest := sha256.Sum256(payload)
	return "digest-" + hex.EncodeToString(digest[:8])
}

func parseMediaSize(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size < 0 {
		return 0
	}
	return size
}

func chunkText(text string, limit int) []string {
	if limit <= 0 || len(text) <= limit {
		return []string{text}
	}
	var chunks []string
	for len(text) > 0 {
		if len(text) <= limit {
			chunks = append(chunks, text)
			break
		}
		cut := limit
		if idx := strings.LastIndex(text[:limit], "\n\n"); idx > limit*3/10 {
			cut = idx + 2
		} else if idx := strings.LastIndex(text[:limit], "\n"); idx > limit*3/10 {
			cut = idx + 1
		}
		for cut > 0 && cut < len(text) && (text[cut]&0xc0) == 0x80 {
			cut--
		}
		if cut == 0 {
			_, size := utf8.DecodeRuneInString(text)
			cut = size
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

// Ensure Bot implements messaging.Platform at compile time.
var _ messaging.Platform = (*Bot)(nil)
var _ messaging.DurableDeliveryExecutor = (*Bot)(nil)
