package kit

import (
	"strings"
	"testing"
)

func TestTruncateOneLineCollapsesWhitespace(t *testing.T) {
	if got := TruncateOneLine("  hello\n\tworld  ", 40); got != "hello world" {
		t.Fatalf("collapsed = %q", got)
	}
}

func TestTruncateOneLineCountsRunes(t *testing.T) {
	got := TruncateOneLine(strings.Repeat("知", 10), 3)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 4 {
		t.Fatalf("truncated = %q (%d runes)", got, len([]rune(got)))
	}
	if !strings.HasPrefix(got, "知") {
		t.Fatalf("truncated prefix lost text: %q", got)
	}
}
