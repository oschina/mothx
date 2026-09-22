package wechat

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/oschina/mothx/internal/messaging"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestChunkTextKeepsUTF8Boundaries(t *testing.T) {
	chunks := chunkText("你好世界", 6)
	if got, want := len(chunks), 2; got != want {
		t.Fatalf("chunk count = %d, want %d: %#v", got, want, chunks)
	}
	if strings.Join(chunks, "") != "你好世界" {
		t.Fatalf("chunks lost text: %#v", chunks)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("invalid UTF-8 chunk: %q", chunk)
		}
	}
}

func TestReplySessionQueuesAfterTenMessages(t *testing.T) {
	var sent []string
	bot := NewBot(BotOptions{})
	bot.creds = &Credentials{UserID: "bot", BaseURL: "https://example.test", Token: "token"}
	bot.client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		sent = append(sent, string(body))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ret":0}`)), Header: make(http.Header), Request: r}, nil
	})}

	s := bot.newReplySession("user", "context")
	if err := s.Send(context.Background(), strings.Repeat("x", (wechatMessageTextLimit-replyFooterLen(0))*(wechatMaxRepliesPerMessage+1))); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := len(sent); got != wechatMaxRepliesPerMessage {
		t.Fatalf("sent messages = %d, want %d", got, wechatMaxRepliesPerMessage)
	}
	if !strings.Contains(sent[len(sent)-1], "剩余推送次数: 0次") {
		t.Fatalf("last message missing exhausted footer: %s", sent[len(sent)-1])
	}
	state := bot.pendingReply("user")
	state.mu.Lock()
	queued := len(state.chunks)
	state.mu.Unlock()
	if queued == 0 {
		t.Fatal("expected queued remainder")
	}
}

func TestInboundAttachmentsDownloadAndDecryptMedia(t *testing.T) {
	key := []byte("0123456789abcdef")
	plaintext := []byte("iLink attachment bytes")
	ciphertext, err := EncryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptAESECB: %v", err)
	}

	bot := NewBot(BotOptions{})
	bot.client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("encrypted_query_param"); got != "opaque-ref" {
			t.Fatalf("encrypted_query_param = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(ciphertext)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}

	// This is the locked wire fixture derived from the Tencent release. It
	// verifies the actual JSON names rather than relying on
	// a Go struct constructed directly by the test.
	fixture := fmt.Sprintf(`{
"message_id":42,"message_type":1,"item_list":[
  {"type":2,"image_item":{"media":{"encrypt_query_param":"opaque-ref","aes_key":"not-the-direct-key"},"aeskey":%q}},
  {"type":4,"file_item":{"media":{"encrypt_query_param":"opaque-ref","aes_key":%q},"file_name":"notes.txt","len":"23"}}
]}`, EncodeAESKeyHex(key), EncodeAESKeyBase64(key))
	var wire WireMessage
	if err := json.Unmarshal([]byte(fixture), &wire); err != nil {
		t.Fatalf("unmarshal iLink media fixture: %v", err)
	}

	attachments := bot.inboundAttachments(&wire)
	if got, want := len(attachments), 2; got != want {
		t.Fatalf("attachment count = %d, want %d", got, want)
	}
	if attachments[0].Kind != messaging.AttachmentImage || attachments[1].Kind != messaging.AttachmentFile {
		t.Fatalf("attachment kinds = %#v", []messaging.AttachmentKind{attachments[0].Kind, attachments[1].Kind})
	}
	if attachments[1].Filename != "notes.txt" || attachments[1].SizeHint != 23 {
		t.Fatalf("file metadata = %#v", attachments[1])
	}
	for _, attachment := range attachments {
		if strings.Contains(attachment.Reference, "opaque-ref") || strings.Contains(attachment.Reference, EncodeAESKeyHex(key)) {
			t.Fatalf("transport secret leaked into reference: %q", attachment.Reference)
		}
		stream, err := attachment.Open(context.Background())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		data, readErr := io.ReadAll(stream.Reader)
		closeErr := stream.Reader.Close()
		if readErr != nil {
			t.Fatalf("ReadAll: %v", readErr)
		}
		if closeErr != nil {
			t.Fatalf("Close: %v", closeErr)
		}
		if !bytes.Equal(data, plaintext) {
			t.Fatalf("decrypted media = %q, want %q", data, plaintext)
		}
	}
}

func TestInboundAttachmentsIncludesVoiceVideoAndQuotedMedia(t *testing.T) {
	plaintext := []byte("media")
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := EncryptAESECB(plaintext, key)
	if err != nil {
		t.Fatal(err)
	}
	bot := NewBot(BotOptions{})
	bot.client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(ciphertext)), Header: make(http.Header), Request: r}, nil
	})}
	fixture := fmt.Sprintf(`{"message_id":77,"item_list":[
{"type":3,"voice_item":{"media":{"encrypt_query_param":"voice-ref","aes_key":%q}}},
{"type":5,"video_item":{"media":{"encrypt_query_param":"video-ref","aes_key":%q},"file_name":"clip.mp4"}},
{"type":6,"ref_msg":{"item_list":[{"type":4,"file_item":{"media":{"encrypt_query_param":"quoted-ref","aes_key":%q},"file_name":"quoted.txt"}}]}}
]}`, EncodeAESKeyBase64(key), EncodeAESKeyBase64(key), EncodeAESKeyBase64(key))
	var wire WireMessage
	if err := json.Unmarshal([]byte(fixture), &wire); err != nil {
		t.Fatal(err)
	}
	attachments := bot.inboundAttachments(&wire)
	if len(attachments) != 3 {
		t.Fatalf("attachments = %#v, want voice/video/quoted file", attachments)
	}
	if attachments[0].Kind != messaging.AttachmentAudio || attachments[1].Kind != messaging.AttachmentVideo || attachments[2].Kind != messaging.AttachmentFile {
		t.Fatalf("attachment kinds = %#v", []messaging.AttachmentKind{attachments[0].Kind, attachments[1].Kind, attachments[2].Kind})
	}
	if attachments[0].MediaType != "audio/amr" || attachments[1].MediaType != "video/mp4" {
		t.Fatalf("media types = %q %q", attachments[0].MediaType, attachments[1].MediaType)
	}
	for _, attachment := range attachments {
		if strings.Contains(attachment.Reference, "voice-ref") || strings.Contains(attachment.Reference, EncodeAESKeyBase64(key)) {
			t.Fatalf("opaque media reference leaked: %q", attachment.Reference)
		}
		stream, err := attachment.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(stream.Reader)
		stream.Reader.Close()
		if readErr != nil || !bytes.Equal(data, plaintext) {
			t.Fatalf("media bytes = %q, err=%v", data, readErr)
		}
	}
}

func TestAESECBDecryptReaderRejectsTruncatedCiphertext(t *testing.T) {
	key, err := GenerateAESKey()
	if err != nil {
		t.Fatalf("GenerateAESKey: %v", err)
	}
	reader, err := newAESECBDecryptReadCloser(io.NopCloser(strings.NewReader("truncated")), key)
	if err != nil {
		t.Fatalf("newAESECBDecryptReadCloser: %v", err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("ReadAll error = %v, want ciphertext block error", err)
	}
}

func TestOutboundMediaUsesLockedCDNAndMessageContract(t *testing.T) {
	plaintext := []byte("generated artifact")
	var uploadKey []byte
	var mu sync.Mutex
	var calls []struct {
		method string
		path   string
		body   []byte
	}
	bot := NewBot(BotOptions{})
	bot.creds = &Credentials{UserID: "bot-user", BaseURL: "https://ilink.test", Token: "token"}
	bot.client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		calls = append(calls, struct {
			method string
			path   string
			body   []byte
		}{r.Method, r.URL.Path, body})
		mu.Unlock()
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			var request map[string]any
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			if request["media_type"] != float64(1) || request["to_user_id"] != "target-user" || request["no_need_thumb"] != true {
				return nil, fmt.Errorf("unexpected getuploadurl request: %#v", request)
			}
			var keyErr error
			uploadKey, keyErr = DecodeAESKey(fmt.Sprint(request["aeskey"]))
			if keyErr != nil {
				return nil, keyErr
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ret":0,"upload_param":"signed-upload"}`)), Header: make(http.Header), Request: r}, nil
		case "/c2c/upload":
			decoded, decryptErr := DecryptAESECB(body, uploadKey)
			if decryptErr != nil || !bytes.Equal(decoded, plaintext) {
				return nil, fmt.Errorf("CDN ciphertext decrypt = %q, err=%v", decoded, decryptErr)
			}
			header := make(http.Header)
			header.Set("x-encrypted-param", "signed-download")
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: header, Request: r}, nil
		case "/ilink/bot/sendmessage":
			var request struct {
				Msg struct {
					ToUserID     string        `json:"to_user_id"`
					ContextToken string        `json:"context_token"`
					RunID        string        `json:"run_id"`
					ClientID     string        `json:"client_id"`
					ItemList     []MessageItem `json:"item_list"`
				} `json:"msg"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			if request.Msg.ToUserID != "target-user" || request.Msg.ContextToken != "frozen-context" || request.Msg.RunID != "run-1" || len(request.Msg.ItemList) != 1 || request.Msg.ItemList[0].Type != ItemImage {
				return nil, fmt.Errorf("unexpected sendmessage request: %#v", request.Msg)
			}
			if request.Msg.ClientID != StableClientID("send-op") {
				return nil, fmt.Errorf("client_id = %q, want stable operation ID", request.Msg.ClientID)
			}
			media := request.Msg.ItemList[0].ImageItem
			if media == nil || media.Media == nil || media.Media.EncryptQueryParam != "signed-download" || media.MidSize != int64((len(plaintext)/16+1)*16) {
				return nil, fmt.Errorf("unexpected image item: %#v", media)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ret":0}`)), Header: make(http.Header), Request: r}, nil
		default:
			return nil, fmt.Errorf("unexpected request path %s", r.URL.Path)
		}
	})}

	var phases []string
	attachment := messaging.OutboundAttachment{
		ID: "artifact-1", RunID: "run-1", TargetID: "target-user", ReplyContext: "frozen-context",
		UploadOperationID: "upload-op", SendOperationID: "send-op", Kind: messaging.AttachmentImage,
		Filename: "image.png", Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(plaintext)), nil
		},
		ProgressUpload: func(_ context.Context, status, assetID, state, failure string) {
			phases = append(phases, "progress:"+status+":"+assetID+":"+failure)
			if !json.Valid([]byte(state)) {
				t.Fatalf("invalid progress provider state: %s", state)
			}
		},
		CompleteUpload: func(_ context.Context, status, assetID, state, failure string) {
			phases = append(phases, "upload:"+status+":"+assetID+":"+failure)
			if !json.Valid([]byte(state)) {
				t.Fatalf("invalid upload provider state: %s", state)
			}
		},
		PrepareSend: func(context.Context) error {
			phases = append(phases, "prepare-send")
			return nil
		},
		CompleteSend: func(_ context.Context, status, messageID, state, failure string) {
			phases = append(phases, "send:"+status+":"+messageID+":"+failure)
			if !json.Valid([]byte(state)) {
				t.Fatalf("invalid send provider state: %s", state)
			}
		},
	}
	if err := bot.sendMediaAttachment(context.Background(), attachment); err != nil {
		t.Fatalf("sendMediaAttachment: %v", err)
	}
	if len(phases) != 5 || phases[0] != "progress:uploading:"+stableFileKey("upload-op")+":" || phases[1] != phases[0] || phases[2] != "upload:uploaded:"+stableFileKey("upload-op")+":" || phases[3] != "prepare-send" || !strings.HasPrefix(phases[4], "send:delivered:"+StableClientID("send-op")+":") {
		t.Fatalf("phase callbacks = %#v", phases)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || calls[0].path != "/ilink/bot/getuploadurl" || calls[1].path != "/c2c/upload" || calls[2].path != "/ilink/bot/sendmessage" {
		t.Fatalf("HTTP calls = %#v", calls)
	}
	checksum := md5.Sum(plaintext)
	var uploadRequest map[string]any
	if err := json.Unmarshal(calls[0].body, &uploadRequest); err != nil {
		t.Fatal(err)
	}
	if uploadRequest["rawfilemd5"] != fmt.Sprintf("%x", checksum[:]) {
		t.Fatalf("rawfilemd5 = %#v", uploadRequest["rawfilemd5"])
	}
}

func TestBuildMediaItemSupportsVideoAndFile(t *testing.T) {
	uploaded := UploadedMedia{DownloadEncryptedParam: "download", AESKeyHex: "30313233343536373839616263646566", RawSize: 23, CiphertextSize: 32}
	video, err := BuildMediaItem(ItemVideo, uploaded, "clip.mp4")
	if err != nil || video.VideoItem == nil || video.VideoItem.VideoSize != 32 || video.VideoItem.FileName != "clip.mp4" {
		t.Fatalf("video item = %#v, err=%v", video, err)
	}
	file, err := BuildMediaItem(ItemFile, uploaded, "notes.txt")
	if err != nil || file.FileItem == nil || file.FileItem.Len != "23" || file.FileItem.FileName != "notes.txt" {
		t.Fatalf("file item = %#v, err=%v", file, err)
	}
}

var _ messaging.Platform = (*Bot)(nil)
