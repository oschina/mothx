package desktop

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

// The Runtime rejects a request line over 10 MiB with an error that carries no
// id, so a prompt above that size would never be answered. The embedded
// attachment budget must leave room for the prompt text inside that line.
func TestEmbeddedAttachmentBudgetFitsTheRuntimeRequestLimit(t *testing.T) {
	const runtimeRequestLimit = 10 << 20
	encoded := base64.StdEncoding.EncodedLen(nativeMaxEmbeddedAttachment)
	if encoded <= 0 || encoded > runtimeRequestLimit-(512<<10) {
		t.Fatalf("a full attachment budget encodes to %d bytes; it must leave 512 KiB of the %d byte request line for the prompt",
			encoded, runtimeRequestLimit)
	}
}

func TestEmbeddedAttachmentBytesTotalsOnlyEmbeddedFiles(t *testing.T) {
	attachments := []attachment{
		{Name: "link.txt", Path: "/work/link.txt", Size: 9000},
		{Name: "a.bin", Embedded: true, Size: 3000},
		{Name: "b.bin", Embedded: true, Size: 4000},
	}
	if got := embeddedAttachmentBytes(attachments); got != 7000 {
		t.Fatalf("embeddedAttachmentBytes = %d, want 7000", got)
	}
}

// A prompt the Runtime cannot accept must not clear the composer and leave the
// task waiting for an answer that never comes.
func TestSendRefusesAttachmentsOverTheEmbeddedBudget(t *testing.T) {
	a := testApp()
	a.setClient(newACPClient(nil, nil, nil))
	a.currentSession = "s1"
	a.attachments = []attachment{
		{Name: "a.bin", Embedded: true, Size: nativeMaxEmbeddedAttachment - 1000, Data: "eA=="},
		{Name: "b.bin", Embedded: true, Size: 2000, Data: "eA=="},
	}
	a.draft = "  keep this draft  "
	before := len(a.messages)

	a.send()

	if a.draft != "  keep this draft  " {
		t.Fatalf("draft = %q, want it kept", a.draft)
	}
	if len(a.attachments) != 2 {
		t.Fatalf("attachments = %d, want them kept", len(a.attachments))
	}
	if !strings.Contains(a.status, "内嵌附件总大小超出上限") {
		t.Fatalf("status = %q, want the budget explanation", a.status)
	}
	if len(a.messages) != before || a.running || a.creatingSession {
		t.Fatalf("an over-budget prompt must not start a send: messages=%d running=%v creating=%v",
			len(a.messages), a.running, a.creatingSession)
	}
}

// Exactly at the budget is still accepted: the guard must not be off by one.
func TestEmbeddedAttachmentBytesAcceptsTheBudgetExactly(t *testing.T) {
	attachments := []attachment{{Name: "a.bin", Embedded: true, Size: nativeMaxEmbeddedAttachment}}
	if embeddedAttachmentBytes(attachments) != nativeMaxEmbeddedAttachment {
		t.Fatal("a budget-sized attachment total must be allowed")
	}
	attachments = append(attachments, attachment{Name: "b.bin", Embedded: true, Size: 1})
	if embeddedAttachmentBytes(attachments) <= nativeMaxEmbeddedAttachment {
		t.Fatal("one byte over the budget must be refused")
	}
}

// Tool output is mostly Chinese; a byte cut in the middle of a character used
// to leave an invalid byte sequence at the end of the displayed text.
func TestTruncateKeepsTextValidUTF8(t *testing.T) {
	text := strings.Repeat("中", 7000) // 21000 bytes
	got := truncate(text, 20000)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: tail %q", got[len(got)-6:])
	}
	if want := strings.Repeat("中", 6666) + "\n…"; got != want {
		t.Fatalf("truncate kept %d bytes, want %q", len(got), want)
	}
	if got := truncate("short", 20000); got != "short" {
		t.Fatalf("short text = %q, want it unchanged", got)
	}
}
