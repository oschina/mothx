package agent

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestSupportsInputUsesModelCapabilities(t *testing.T) {
	a := &Agent{config: AgentLoopConfig{Model: &provider.Model{ID: "omni", Input: []string{"text", "audio"}}}}
	if !a.supportsInput("audio") {
		t.Fatal("audio input should be supported")
	}
	if a.supportsInput("video") || a.supportsImages() {
		t.Fatal("unsupported modalities must not be claimed")
	}
	none := &Agent{}
	if none.supportsInput("audio") || none.supportsImages() {
		t.Fatal("nil model must not claim input support")
	}
}

func TestStripUnsupportedMediaRewritesOnlyUnsupportedBlocks(t *testing.T) {
	a := &Agent{config: AgentLoopConfig{Model: &provider.Model{ID: "text-model", Input: []string{"text"}}}}
	messages := []provider.Message{{
		Role: "user",
		Contents: []provider.ContentBlock{
			{Type: "text", Text: "watch this"},
			{Type: "video", Video: &provider.VideoContent{MimeType: "video/mp4", Data: "dmlkZW8="}},
		},
	}}
	out := a.stripUnsupportedMedia(messages)
	if len(out) != 1 || len(out[0].Contents) != 2 {
		t.Fatalf("rewritten = %#v", out)
	}
	if out[0].Contents[0].Type != "text" {
		t.Fatalf("text block = %#v", out[0].Contents[0])
	}
	if out[0].Contents[1].Type != "text" || !strings.Contains(out[0].Contents[1].Text, "[video unavailable:") {
		t.Fatalf("video placeholder = %#v", out[0].Contents[1])
	}
	if messages[0].Contents[1].Type != "video" || messages[0].Contents[1].Video == nil {
		t.Fatal("stripUnsupportedMedia mutated the persisted history")
	}

	a.config.Model = &provider.Model{ID: "omni", Input: []string{"text", "video"}}
	capable := a.stripUnsupportedMedia(messages)
	if capable[0].Contents[1].Type != "video" || capable[0].Contents[1].Video == nil {
		t.Fatalf("capable model must keep video blocks: %#v", capable[0].Contents[1])
	}
}
