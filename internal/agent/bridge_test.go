package agent

import (
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/provider"
)

func TestChatParamsBridgePreservesModelID(t *testing.T) {
	pub := agentpkg.ChatParams{
		ModelID:      "kimi-2.5",
		SystemPrompt: "test",
		MaxTokens:    1234,
	}

	internal := ChatParamsFromPublic(pub)
	if internal.ModelID != "kimi-2.5" {
		t.Fatalf("internal ModelID = %q, want kimi-2.5", internal.ModelID)
	}

	back := ChatParamsToPublic(provider.ChatParams{
		ModelID:      "kimi-2.5",
		SystemPrompt: "test",
		MaxTokens:    1234,
	})
	if back.ModelID != "kimi-2.5" {
		t.Fatalf("public ModelID = %q, want kimi-2.5", back.ModelID)
	}
}

func TestToolResultImagesExtractionAndPublicProjection(t *testing.T) {
	contents := []provider.ContentBlock{
		{Type: "text", Text: "screenshot captured"},
		{Type: "image", Image: &provider.ImageContent{MimeType: "image/png", Data: "cG5nLWRhdGE="}},
		{Type: "image"}, // nil payload is skipped
		{Type: "image", Image: &provider.ImageContent{MimeType: "image/jpeg", Data: ""}}, // empty payload is skipped
	}
	images := toolResultImages(contents)
	if len(images) != 1 {
		t.Fatalf("toolResultImages = %#v, want exactly the populated image block", images)
	}
	if images[0].MimeType != "image/png" || images[0].Data != "cG5nLWRhdGE=" {
		t.Fatalf("extracted image = %#v, want the exact tool payload", images[0])
	}
	if extracted := toolResultImages(nil); extracted != nil {
		t.Fatalf("toolResultImages(nil) = %#v, want nil", extracted)
	}

	// The bridge must carry the payloads through to the public SDK event
	// unchanged so adapters (ACP tool_call_update images) can project them.
	public := EventToPublic(Event{
		Type:       EventToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "read",
		ToolResult: "screenshot captured",
		ToolImages: images,
	})
	if len(public.ToolImages) != 1 {
		t.Fatalf("public ToolImages = %#v, want the projected payload", public.ToolImages)
	}
	if public.ToolImages[0].MimeType != "image/png" || public.ToolImages[0].Data != "cG5nLWRhdGE=" {
		t.Fatalf("public image = %#v, want identical mime/base64 payload", public.ToolImages[0])
	}
	if empty := EventToPublic(Event{Type: EventToolExecutionEnd}); empty.ToolImages != nil {
		t.Fatalf("public ToolImages without payloads = %#v, want nil", empty.ToolImages)
	}
}

func TestContentBlockMediaRoundTrip(t *testing.T) {
	audio := provider.ContentBlock{Type: "audio", Audio: &provider.AudioContent{MimeType: "audio/wav", Format: "wav", Data: "YQ==", Bytes: 1}}
	pub := ContentBlockToPublic(audio)
	if pub.Audio == nil || pub.Audio.Data != "YQ==" || pub.Audio.Format != "wav" {
		t.Fatalf("public audio = %#v", pub.Audio)
	}
	if back := ContentBlockFromPublic(pub); back.Audio == nil || *back.Audio != *audio.Audio {
		t.Fatalf("internal audio = %#v", back.Audio)
	}
	video := provider.ContentBlock{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", Data: "Yg==", URL: "https://example.com/v.mp4", Bytes: 1}}
	pubVideo := ContentBlockToPublic(video)
	if pubVideo.Video == nil || pubVideo.Video.URL != "https://example.com/v.mp4" {
		t.Fatalf("public video = %#v", pubVideo.Video)
	}
	if back := ContentBlockFromPublic(pubVideo); back.Video == nil || *back.Video != *video.Video {
		t.Fatalf("internal video = %#v", back.Video)
	}
}
