//go:build windows

package platform

import (
	"testing"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

func TestDecodeProcessOutputWindows(t *testing.T) {
	t.Setenv(outputEncodingEnv, "")

	// "你好" in GBK. These bytes are not valid UTF-8, so they are always
	// decoded with the process code page.
	gbkHello := []byte{0xc4, 0xe3, 0xba, 0xc3}
	got := DecodeProcessOutput(gbkHello)
	if !utf8.ValidString(got) {
		t.Fatalf("DecodeProcessOutput() returned invalid UTF-8: %q", got)
	}
	if windows.GetACP() == 936 && got != "你好" {
		t.Fatalf("DecodeProcessOutput() = %q, want %q", got, "你好")
	}

	// "写" in GBK (D0 B4) is also valid UTF-8 (Cyrillic "д"). On a
	// Simplified Chinese ACP the ambiguity heuristic must prefer "写".
	if windows.GetACP() == 936 {
		if got := DecodeProcessOutput([]byte{0xd0, 0xb4}); got != "写" {
			t.Fatalf("DecodeProcessOutput() = %q, want %q", got, "写")
		}
	}

	// Genuine UTF-8 CJK must pass through unchanged.
	if got := DecodeProcessOutput([]byte("中文")); got != "中文" {
		t.Fatalf("DecodeProcessOutput() = %q, want %q", got, "中文")
	}

	// The environment override can force UTF-8 decoding.
	t.Setenv(outputEncodingEnv, "utf-8")
	if got := DecodeProcessOutput(gbkHello); got != "\uFFFD" {
		t.Fatalf("DecodeProcessOutput(forced utf-8) = %q, want %q", got, "\uFFFD")
	}

	// A numeric override forces that code page.
	t.Setenv(outputEncodingEnv, "936")
	if windows.GetACP() == 936 {
		if got := DecodeProcessOutput(gbkHello); got != "你好" {
			t.Fatalf("DecodeProcessOutput(forced 936) = %q, want %q", got, "你好")
		}
	}
}
