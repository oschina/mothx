package wechat

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/oschina/mothx/internal/messaging"
)

// The Runtime artifact store performs the authoritative policy check. This
// adapter-side bound only prevents an accidental unbounded read before the
// provider request is assembled.
const maxOutboundMediaBytes = 100 << 20

type wechatUploadState struct {
	UploadedMedia
	UploadParam   string `json:"upload_param,omitempty"`
	UploadFullURL string `json:"upload_full_url,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}

func (s wechatUploadState) raw() string {
	data, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func parseWechatUploadState(raw []byte) wechatUploadState {
	var state wechatUploadState
	if len(raw) == 0 || json.Unmarshal(raw, &state) != nil {
		return wechatUploadState{}
	}
	return state
}

func (b *Bot) sendMediaAttachment(ctx context.Context, attachment messaging.OutboundAttachment) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("wechat media bot is not configured")
	}
	if attachment.Kind != messaging.AttachmentImage && attachment.Kind != messaging.AttachmentVideo && attachment.Kind != messaging.AttachmentFile {
		return fmt.Errorf("unsupported outbound WeChat media kind %q", attachment.Kind)
	}

	state, err := b.uploadMediaAttachment(ctx, attachment)
	if err != nil {
		status := wechatUploadFailureStatus(err)
		if attachment.CompleteUpload != nil {
			attachment.CompleteUpload(ctx, status, state.FileKey, state.raw(), wechatErrorCode(err))
		} else if attachment.Complete != nil {
			attachment.Complete(ctx, status, "", wechatErrorCode(err))
		}
		return err
	}
	if attachment.CompleteUpload != nil {
		if stateErr := callCompleteUpload(ctx, attachment, state); stateErr != nil {
			return stateErr
		}
	}
	if attachment.PrepareSend != nil {
		if err := attachment.PrepareSend(ctx); err != nil {
			return err
		}
	}

	item, err := BuildMediaItem(wechatItemType(attachment.Kind), state.UploadedMedia, attachment.Filename)
	if err != nil {
		if attachment.CompleteSend != nil {
			attachment.CompleteSend(ctx, "failed", "", state.raw(), "media_item_invalid")
		} else if attachment.Complete != nil {
			attachment.Complete(ctx, "failed", "", "media_item_invalid")
		}
		return err
	}
	creds := b.credentials()
	if creds == nil {
		err := fmt.Errorf("not logged in")
		if attachment.CompleteSend != nil {
			attachment.CompleteSend(ctx, "failed", "", state.raw(), "not_logged_in")
		}
		return err
	}
	clientSeed := strings.TrimSpace(attachment.SendOperationID)
	if clientSeed == "" {
		clientSeed = attachment.ID
	}
	clientID := StableClientID(clientSeed)
	state.ClientID = clientID
	message := BuildMediaMessage(creds.UserID, attachment.TargetID, attachment.ReplyContext, attachment.RunID, clientID, item)
	if err := b.client.SendMessage(ctx, creds.BaseURL, creds.Token, message); err != nil {
		status := wechatSendFailureStatus(err)
		if attachment.CompleteSend != nil {
			attachment.CompleteSend(ctx, status, clientID, state.raw(), wechatErrorCode(err))
		} else if attachment.Complete != nil {
			attachment.Complete(ctx, status, clientID, wechatErrorCode(err))
		}
		return err
	}
	if attachment.CompleteSend != nil {
		attachment.CompleteSend(ctx, "delivered", clientID, state.raw(), "")
	} else if attachment.Complete != nil {
		attachment.Complete(ctx, "delivered", clientID, "")
	}
	return nil
}

// ExecuteDurableDelivery replays one Runtime-owned outbox operation after a
// process restart. The operation is already claimed by the Runtime worker;
// this method only performs the iLink call and returns the next checkpoint.
func (b *Bot) ExecuteDurableDelivery(ctx context.Context, request messaging.DurableDeliveryRequest) (messaging.DurableDeliveryResult, error) {
	if b == nil || b.client == nil {
		return messaging.DurableDeliveryResult{}, fmt.Errorf("wechat media bot is not configured")
	}
	creds := b.credentials()
	if creds == nil {
		return messaging.DurableDeliveryResult{Status: "retry_wait", FailureCode: "not_logged_in"}, nil
	}

	switch request.Operation.OperationKind {
	case "send_text", "send_fallback_text":
		if strings.TrimSpace(request.Caption) == "" {
			return messaging.DurableDeliveryResult{Status: "failed", FailureCode: "delivery_caption_missing"}, nil
		}
		clientID := StableClientID(request.Operation.IdempotencyKey)
		message := BuildTextMessageWithClientID(creds.UserID, request.Intent.TargetID,
			wechatReplyContext(request.Intent.TransportContext), request.Caption, clientID)
		if err := b.client.SendMessage(ctx, creds.BaseURL, creds.Token, message); err != nil {
			return messaging.DurableDeliveryResult{Status: wechatSendFailureStatus(err), ProviderMessageID: clientID, FailureCode: wechatErrorCode(err)}, nil
		}
		return messaging.DurableDeliveryResult{Status: "delivered", ProviderMessageID: clientID}, nil

	case "upload_artifact":
		kind := request.ArtifactKind
		if kind != messaging.AttachmentImage && kind != messaging.AttachmentVideo && kind != messaging.AttachmentFile {
			return messaging.DurableDeliveryResult{Status: "failed", FailureCode: "unsupported_media_kind"}, nil
		}
		attachment := messaging.OutboundAttachment{
			ID: request.Operation.ArtifactID, RunID: request.Intent.RunID, TargetID: request.Intent.TargetID,
			Kind: kind, Filename: request.ArtifactFilename, MediaType: request.ArtifactMediaType,
			UploadOperationID: request.Operation.ID, ProviderState: append([]byte(nil), request.Operation.ProviderState...),
			Open: request.OpenArtifact,
		}
		state, err := b.uploadMediaAttachment(ctx, attachment)
		if err != nil {
			return messaging.DurableDeliveryResult{Status: wechatUploadFailureStatus(err), ProviderAssetID: state.FileKey, ProviderState: []byte(state.raw()), FailureCode: wechatErrorCode(err)}, nil
		}
		return messaging.DurableDeliveryResult{Status: "uploaded", ProviderAssetID: state.FileKey, ProviderState: []byte(state.raw())}, nil

	case "send_artifact":
		if request.Dependency == nil {
			return messaging.DurableDeliveryResult{Status: "failed", FailureCode: "delivery_dependency_missing"}, nil
		}
		state := parseWechatUploadState(request.Dependency.ProviderState)
		if state.DownloadEncryptedParam == "" {
			return messaging.DurableDeliveryResult{Status: "retry_wait", ProviderState: []byte(state.raw()), FailureCode: "wechat_upload_checkpoint_missing"}, nil
		}
		item, err := BuildMediaItem(wechatItemType(request.ArtifactKind), state.UploadedMedia, request.ArtifactFilename)
		if err != nil {
			return messaging.DurableDeliveryResult{Status: "failed", ProviderState: []byte(state.raw()), FailureCode: "media_item_invalid"}, nil
		}
		clientID := StableClientID(request.Operation.IdempotencyKey)
		message := BuildMediaMessage(creds.UserID, request.Intent.TargetID,
			wechatReplyContext(request.Intent.TransportContext), request.Intent.RunID, clientID, item)
		if err := b.client.SendMessage(ctx, creds.BaseURL, creds.Token, message); err != nil {
			return messaging.DurableDeliveryResult{Status: wechatSendFailureStatus(err), ProviderMessageID: clientID, ProviderState: []byte(state.raw()), FailureCode: wechatErrorCode(err)}, nil
		}
		state.ClientID = clientID
		return messaging.DurableDeliveryResult{Status: "delivered", ProviderMessageID: clientID, ProviderState: []byte(state.raw())}, nil
	default:
		return messaging.DurableDeliveryResult{Status: "failed", FailureCode: "unsupported_delivery_operation"}, nil
	}
}

func wechatReplyContext(raw []byte) string {
	var transport struct {
		ReplyContext string `json:"replyContext"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &transport) != nil {
		return ""
	}
	return transport.ReplyContext
}

func callCompleteUpload(ctx context.Context, attachment messaging.OutboundAttachment, state wechatUploadState) error {
	attachment.CompleteUpload(ctx, "uploaded", state.FileKey, state.raw(), "")
	return nil
}

func (b *Bot) uploadMediaAttachment(ctx context.Context, attachment messaging.OutboundAttachment) (wechatUploadState, error) {
	state := parseWechatUploadState(attachment.ProviderState)
	if state.FileKey == "" {
		seed := strings.TrimSpace(attachment.UploadOperationID)
		if seed == "" {
			seed = attachment.ID
		}
		state.FileKey = stableFileKey(seed)
	}
	reader, err := openOutboundArtifact(ctx, attachment)
	if err != nil {
		return state, err
	}
	defer reader.Close()
	plaintext, err := io.ReadAll(io.LimitReader(reader, maxOutboundMediaBytes+1))
	if err != nil {
		return state, fmt.Errorf("read WeChat artifact: %w", err)
	}
	if int64(len(plaintext)) > maxOutboundMediaBytes {
		return state, fmt.Errorf("WeChat artifact exceeds %d-byte adapter bound", maxOutboundMediaBytes)
	}
	state.RawSize = int64(len(plaintext))
	state.CiphertextSize = int64((len(plaintext)/16 + 1) * 16)
	if state.AESKeyHex == "" {
		key, keyErr := GenerateAESKey()
		if keyErr != nil {
			return state, keyErr
		}
		state.AESKeyHex = hex.EncodeToString(key)
	}
	key, err := DecodeAESKey(state.AESKeyHex)
	if err != nil {
		return state, err
	}
	if attachment.ProgressUpload != nil {
		attachment.ProgressUpload(ctx, "uploading", state.FileKey, state.raw(), "")
	}
	if state.DownloadEncryptedParam == "" {
		checksum := md5.Sum(plaintext)
		request := GetUploadURLRequest{
			FileKey: state.FileKey, MediaType: wechatUploadType(attachment.Kind), ToUserID: attachment.TargetID,
			RawSize: state.RawSize, RawFileMD5: hex.EncodeToString(checksum[:]), FileSize: state.CiphertextSize,
			NoNeedThumb: true, AESKey: state.AESKeyHex,
		}
		creds := b.credentials()
		if creds == nil {
			return state, fmt.Errorf("not logged in")
		}
		if state.UploadParam == "" && state.UploadFullURL == "" {
			response, uploadErr := b.client.GetUploadURL(ctx, creds.BaseURL, creds.Token, request)
			if uploadErr != nil {
				return state, uploadErr
			}
			state.UploadParam, state.UploadFullURL = strings.TrimSpace(response.UploadParam), strings.TrimSpace(response.UploadFullURL)
			if state.UploadParam == "" && state.UploadFullURL == "" {
				return state, fmt.Errorf("getuploadurl returned no upload URL")
			}
			if attachment.ProgressUpload != nil {
				attachment.ProgressUpload(ctx, "uploading", state.FileKey, state.raw(), "")
			}
		}
		param, uploadErr := b.client.UploadCDN(ctx, state.UploadFullURL, state.UploadParam, state.FileKey, plaintext, key)
		if uploadErr != nil {
			return state, uploadErr
		}
		state.DownloadEncryptedParam = param
	}
	return state, nil
}

func openOutboundArtifact(ctx context.Context, attachment messaging.OutboundAttachment) (io.ReadCloser, error) {
	if attachment.Open == nil {
		return nil, fmt.Errorf("WeChat attachment %s is not readable", attachment.ID)
	}
	return attachment.Open(ctx)
}

func (b *Bot) credentials() *Credentials {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.creds == nil {
		return nil
	}
	copy := *b.creds
	return &copy
}

func wechatItemType(kind messaging.AttachmentKind) MessageItemType {
	switch kind {
	case messaging.AttachmentImage:
		return ItemImage
	case messaging.AttachmentVideo:
		return ItemVideo
	default:
		return ItemFile
	}
}

func wechatUploadType(kind messaging.AttachmentKind) UploadMediaType {
	switch kind {
	case messaging.AttachmentImage:
		return UploadMediaImage
	case messaging.AttachmentVideo:
		return UploadMediaVideo
	default:
		return UploadMediaFile
	}
}

func stableFileKey(operationID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(operationID)))
	return hex.EncodeToString(digest[:16])
}

func wechatUploadFailureStatus(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 && apiErr.HTTPStatus != 429 {
		return "failed"
	}
	return "retry_wait"
}

func wechatSendFailureStatus(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		return "failed"
	}
	// A timeout or connection loss after sendmessage may have delivered the
	// message; the Runtime must diagnose it instead of blindly retrying.
	return "uncertain"
}

func wechatErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.ErrCode != 0 {
		return fmt.Sprintf("ilink_%d", apiErr.ErrCode)
	}
	return "transport_error"
}
