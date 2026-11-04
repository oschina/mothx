package agentruntime

import (
	"strings"
	"testing"
)

func attachmentTestContract() SubagentTaskContract {
	return SubagentTaskContract{
		Objective: "inspect the supplied inputs", Role: SubagentRoleInvestigate,
		ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
		TargetProfile:        "helper",
		Attachments: []SubagentAttachment{
			{
				ID: "image-1", Filename: "screen.png", MediaType: "image/png",
				Kind: AttachmentImage, RelativePath: ".mothx/tmp/inputs/image-1/screen.png",
				Bytes: 4, Data: []byte("png!"),
			},
			{
				ID: "file-1", Filename: "notes.txt", MediaType: "text/plain",
				Kind: AttachmentFile, RelativePath: ".mothx/tmp/inputs/file-1/notes.txt",
				Bytes: 5, Data: []byte("hello"),
			},
		},
	}
}

func TestACPPromptContentNegotiatesImageAndEmbeddedResource(t *testing.T) {
	blocks := acpPromptContent(map[string]any{
		"promptCapabilities": map[string]any{"image": true, "embeddedContext": true},
	}, attachmentTestContract(), "/workspace")
	if len(blocks) != 3 {
		t.Fatalf("prompt blocks = %#v, want text + image + resource", blocks)
	}
	if blocks[0].Type != "text" || !strings.Contains(blocks[0].Text, "delivered as ACP image content") {
		t.Fatalf("text block = %#v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].MIMEType != "image/png" || blocks[1].Data == "" {
		t.Fatalf("image block = %#v", blocks[1])
	}
	if blocks[2].Type != "resource" || blocks[2].Resource == nil ||
		blocks[2].Resource.Text != "hello" || blocks[2].Resource.MIMEType != "text/plain" {
		t.Fatalf("resource block = %#v", blocks[2])
	}
	if blocks[2].Resource.URI != "urn:mothx:attachment:file-1" {
		t.Fatalf("resource URI = %q, want an opaque Runtime identity", blocks[2].Resource.URI)
	}
}

func TestACPPromptContentDisclosesUnsupportedAttachments(t *testing.T) {
	blocks := acpPromptContent(nil, attachmentTestContract(), "/workspace")
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("prompt blocks = %#v, want text-only fallback", blocks)
	}
	for _, want := range []string{
		"screen.png: not inlined",
		"notes.txt: not inlined",
		".mothx/tmp/inputs/image-1/screen.png",
		"session-relative path",
		`Runtime resource "image-1"`,
	} {
		if !strings.Contains(blocks[0].Text, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, blocks[0].Text)
		}
	}
}

func TestACPPromptContentNegotiatesAudio(t *testing.T) {
	contract := attachmentTestContract()
	contract.Attachments = []SubagentAttachment{{
		ID: "audio-1", Filename: "clip.wav", MediaType: "audio/wav",
		Kind: AttachmentAudio, RelativePath: ".mothx/tmp/inputs/audio-1/clip.wav",
		Bytes: 4, Data: []byte("wav!"),
	}}
	blocks := acpPromptContent(map[string]any{
		"promptCapabilities": map[string]any{"audio": true},
	}, contract, "/workspace")
	if len(blocks) != 2 || blocks[1].Type != "audio" || blocks[1].MIMEType != "audio/wav" {
		t.Fatalf("audio prompt blocks = %#v", blocks)
	}
}
