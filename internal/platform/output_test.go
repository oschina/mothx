package platform

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeLegacyCodec models a DBCS code page for the byte-level tests. It is
// deliberately tiny: it only knows the GBK encodings used by the test data.
type fakeLegacyCodec struct {
	strict map[string]string
}

func (c fakeLegacyCodec) decodeStrict(p []byte) (string, bool) {
	s, ok := c.strict[string(p)]
	return s, ok
}

func (c fakeLegacyCodec) decode(p []byte) string {
	var b strings.Builder
	for i := 0; i < len(p); {
		if i+1 < len(p) {
			if s, ok := c.strict[string(p[i:i+2])]; ok {
				b.WriteString(s)
				i += 2
				continue
			}
		}
		if s, ok := c.strict[string(p[i:i+1])]; ok {
			b.WriteString(s)
			i++
			continue
		}
		b.WriteRune('?')
		i++
	}
	return b.String()
}

func gbkTestCodec() fakeLegacyCodec {
	return fakeLegacyCodec{strict: map[string]string{
		"\xc4\xe3": "你",
		"\xba\xc3": "好",
		"\xd0\xb4": "写",
	}}
}

type ambiguousGBKCodec struct {
	fakeLegacyCodec
}

func (c ambiguousGBKCodec) preferLegacyForValidUTF8(p []byte) bool {
	return string(p) == "\xd0\xb4"
}

func TestNormalizeProcessOutputKeepsValidUTF8(t *testing.T) {
	input := []byte("前缀 中文 suffix")
	if got := normalizeProcessOutput(input, gbkTestCodec()); got != string(input) {
		t.Fatalf("normalizeProcessOutput() = %q, want %q", got, input)
	}
}

func TestNormalizeProcessOutputDecodesLegacyRun(t *testing.T) {
	input := []byte("A\xc4\xe3\xba\xc3B")
	got := normalizeProcessOutput(input, gbkTestCodec())
	if got != "A你好B" {
		t.Fatalf("normalizeProcessOutput() = %q, want %q", got, "A你好B")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("normalizeProcessOutput() returned invalid UTF-8: %q", got)
	}
}

func TestNormalizeProcessOutputKeepsMixedUTF8AndLegacy(t *testing.T) {
	// "中文" is genuine UTF-8 and contains 3-byte sequences; the following
	// GBK bytes are not valid UTF-8.
	input := []byte("A中文\xc4\xe3\xba\xc3B")
	got := normalizeProcessOutput(input, gbkTestCodec())
	if got != "A中文你好B" {
		t.Fatalf("normalizeProcessOutput() = %q, want %q", got, "A中文你好B")
	}
}

func TestNormalizeProcessOutputTrustsValidTwoByteUTF8(t *testing.T) {
	// 0xD0 0xB4 is valid UTF-8 ("д") and also happens to be a plausible GBK
	// pair. A fully valid UTF-8 stream is intentionally treated as UTF-8.
	input := []byte{0xd0, 0xb4}
	if got := normalizeProcessOutput(input, gbkTestCodec()); got != "д" {
		t.Fatalf("normalizeProcessOutput() = %q, want %q", got, "д")
	}
}

func TestNormalizeProcessOutputAmbiguousUTF8PrefersLegacy(t *testing.T) {
	// GBK "写" (D0 B4) is also valid UTF-8 (Cyrillic "д"). A codec which can
	// resolve the ambiguity should win.
	input := []byte("A\xd0\xb4B")
	got := normalizeProcessOutput(input, ambiguousGBKCodec{gbkTestCodec()})
	if got != "A写B" {
		t.Fatalf("normalizeProcessOutput() = %q, want %q", got, "A写B")
	}
}

func TestNormalizeProcessOutputEmpty(t *testing.T) {
	if got := normalizeProcessOutput(nil, gbkTestCodec()); got != "" {
		t.Fatalf("normalizeProcessOutput(nil) = %q, want empty", got)
	}
}

func TestLegacyUnitLenDBCS(t *testing.T) {
	if got := legacyUnitLen([]byte{0xc4, 0xe3}, gbkTestCodec()); got != 2 {
		t.Fatalf("legacyUnitLen() = %d, want 2", got)
	}
}

func TestLegacyUnitLenSingleByte(t *testing.T) {
	codec := fakeLegacyCodec{strict: map[string]string{
		"\xd0": "D",
		"\xb4": "4",
	}}
	if got := legacyUnitLen([]byte{0xd0, 0xb4}, codec); got != 1 {
		t.Fatalf("legacyUnitLen() = %d, want 1", got)
	}
}
