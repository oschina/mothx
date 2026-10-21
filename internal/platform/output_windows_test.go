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

	// A genuine UTF-8 Cyrillic word is not legacy text: its two-byte sequences
	// are only accidentally valid GBK, and re-decoding it yields unrelated
	// Han characters ("Привет" -> "袩褉懈胁械褌").
	if got := DecodeProcessOutput([]byte("Привет")); got != "Привет" {
		t.Fatalf("DecodeProcessOutput() = %q, want %q", got, "Привет")
	}

	// A stream mixing genuine UTF-8 CJK with ambiguous bytes stays UTF-8 as a
	// whole; the CJK reading proves the producer writes UTF-8.
	if got := DecodeProcessOutput([]byte("中文 Привет")); got != "中文 Привет" {
		t.Fatalf("DecodeProcessOutput() = %q, want %q", got, "中文 Привет")
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

func TestPreferLegacyForValidUTF8Windows(t *testing.T) {
	// Code page 936 is always available through the conversion API, so this
	// test does not depend on the machine's ANSI code page.
	t.Setenv(outputEncodingEnv, "")

	plain := winLegacyTextCodec{codePage: 936}
	if !plain.preferLegacyForValidUTF8([]byte{0xd0, 0xb4}) {
		t.Fatal("a lone ambiguous pair (GBK 写) should prefer legacy")
	}
	if plain.preferLegacyForValidUTF8([]byte("Привет")) {
		t.Fatal("a genuine UTF-8 Cyrillic run must not be re-decoded as legacy")
	}

	// Stream-level UTF-8 evidence suppresses the flip even for a lone pair.
	withCJK := winLegacyTextCodec{codePage: 936, stream: streamSignals{hasCJK: true, hasMultiByteUTF8: true}}
	if withCJK.preferLegacyForValidUTF8([]byte{0xd0, 0xb4}) {
		t.Fatal("a stream containing genuine UTF-8 CJK must not be re-decoded as legacy")
	}
}
