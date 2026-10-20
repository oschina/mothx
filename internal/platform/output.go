package platform

import (
	"strings"
	"unicode/utf8"
)

// legacyTextCodec decodes bytes in a legacy (non-UTF-8) process code page.
//
// The interface exists so the mixed-stream normalization logic can be tested
// without the Windows API. decodeStrict reports failure for any byte sequence
// that is not valid in the code page; decode is lenient and must still return
// valid UTF-8.
type legacyTextCodec interface {
	decodeStrict([]byte) (string, bool)
	decode([]byte) string
}

// ambiguousUTF8Codec is implemented by codecs which can decide whether a
// fully valid UTF-8 run is more likely to be legacy-code-page text. Some
// two-byte legacy sequences are genuinely ambiguous: for example GBK "写"
// (D0 B4) is also valid UTF-8 and decodes to Cyrillic "д".
type ambiguousUTF8Codec interface {
	preferLegacyForValidUTF8([]byte) bool
}

// normalizeProcessOutput converts captured child-process bytes into valid UTF-8.
//
// Windows child processes choose their stdout/stderr encoding independently.
// When stdout/stderr is a pipe, many runtimes use the system ANSI code page
// (for example GBK/CP936) rather than UTF-8, while others (Go, Rust, Node.js,
// most modern CLIs) write UTF-8. A single command can even mix both. Fixing
// that by injecting a language-specific environment variable only covers one
// runtime at a time, so the capture boundary normalizes the bytes instead.
//
// A stream which is entirely valid UTF-8 is trusted as UTF-8. Otherwise each
// maximal non-ASCII run is decoded as follows: a run which is entirely valid
// UTF-8 stays UTF-8; in a mixed run, maximal valid UTF-8 stretches containing
// a 3- or 4-byte sequence stay UTF-8 (a strong signal for real UTF-8), and the
// remaining bytes are decoded with the legacy code page. This mirrors the
// Windows console autodetection in agentbusybox.
func normalizeProcessOutput(data []byte, codec legacyTextCodec) string {
	if len(data) == 0 {
		return ""
	}

	var b strings.Builder
	b.Grow(len(data))
	pos := 0
	for pos < len(data) {
		if data[pos] < utf8.RuneSelf {
			b.WriteByte(data[pos])
			pos++
			continue
		}

		end := pos
		for end < len(data) && data[end] >= utf8.RuneSelf {
			end++
		}
		writeDecodedRun(&b, data[pos:end], codec)
		pos = end
	}
	return b.String()
}

func writeDecodedRun(b *strings.Builder, run []byte, codec legacyTextCodec) {
	// A run which is entirely valid UTF-8 is almost always genuine UTF-8,
	// except for the genuinely ambiguous two-byte case.
	if utf8.Valid(run) {
		if c, ok := codec.(ambiguousUTF8Codec); ok && c.preferLegacyForValidUTF8(run) {
			b.WriteString(codec.decode(run))
			return
		}
		b.Write(run)
		return
	}

	legacyStart := 0
	pos := 0
	for pos < len(run) {
		n, strong := utf8StretchLen(run[pos:])
		if n > 0 && strong {
			if pos > legacyStart {
				b.WriteString(codec.decode(run[legacyStart:pos]))
			}
			b.Write(run[pos : pos+n])
			pos += n
			legacyStart = pos
			continue
		}
		pos += legacyUnitLen(run[pos:], codec)
	}
	if legacyStart < len(run) {
		b.WriteString(codec.decode(run[legacyStart:]))
	}
}

// utf8StretchLen returns the length of the maximal strictly valid UTF-8
// prefix of p and whether it contains a 3- or 4-byte sequence.
func utf8StretchLen(p []byte) (n int, strong bool) {
	for n < len(p) {
		r, size := utf8.DecodeRune(p[n:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		if size >= 3 {
			strong = true
		}
		n += size
	}
	return n, strong
}

// legacyUnitLen returns the length of the legacy code page unit at p[0].
// DBCS code pages use one or two bytes; single-byte code pages always use one.
func legacyUnitLen(p []byte, codec legacyTextCodec) int {
	if len(p) >= 2 {
		if s, ok := codec.decodeStrict(p[:2]); ok && utf8.RuneCountInString(s) == 1 {
			return 2
		}
	}
	return 1
}
