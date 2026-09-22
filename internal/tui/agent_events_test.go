package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestFormatTUIAttachmentSummary(t *testing.T) {
	tr := i18n.New(i18n.LanguageEN)
	a := &App{translator: tr}
	got := a.formatTUIAttachmentSummary([]provider.Attachment{
		{Kind: "citation", Name: "OpenAI", URL: "https://openai.com"},
		{Kind: "file", ProviderRef: "file_123"},
		{Kind: "citation", Name: "OpenAI", URL: "https://openai.com"},
	})
	for _, want := range []string{"Attachments:", "OpenAI: https://openai.com", "file: file_123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	}
	if strings.Count(got, "https://openai.com") != 1 {
		t.Fatalf("summary should deduplicate attachments: %q", got)
	}
}
