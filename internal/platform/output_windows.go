//go:build windows

package platform

import (
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

const (
	cpUTF8            = 65001
	mbErrInvalidChars = 0x00000008
	outputEncodingEnv = "MOTHX_WIN_OUTPUT_ENCODING"
)

// DecodeProcessOutput converts captured child-process output into valid UTF-8.
//
// Windows runtimes disagree about the encoding they use when stdout/stderr is a
// pipe: Python, Java, the C runtime and shell builtins commonly use the system
// ANSI code page (for example GBK/CP936), while Go, Rust, Node.js and many CLI
// tools write UTF-8. Fully valid UTF-8 runs are normally trusted as UTF-8;
// invalid bytes are decoded with the Windows conversion API.
//
// Some two-byte legacy sequences are also valid UTF-8 (GBK "写" is D0 B4,
// which decodes as Cyrillic "д" under UTF-8). When the ANSI code page is a CJK
// code page, that ambiguity is resolved per non-ASCII run instead of for the
// whole captured block, and only when the stream carries no stronger UTF-8
// signal (see preferLegacyForValidUTF8): a stream whose UTF-8 reading contains
// CJK characters or any 3/4-byte sequence is never second-guessed. Real text
// such as Cyrillic "Привет" therefore stays UTF-8 rather than being re-decoded
// into unrelated Han characters.
//
// Set MOTHX_WIN_OUTPUT_ENCODING=utf-8 to disable legacy decoding, or set it to
// a Windows code page number (for example 936) to force that code page.
func DecodeProcessOutput(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	mode := strings.ToLower(strings.TrimSpace(os.Getenv(outputEncodingEnv)))
	switch mode {
	case "", "auto":
		// Auto-detect below.
	case "utf-8", "utf8", "65001":
		return strings.ToValidUTF8(string(data), "\uFFFD")
	default:
		if cp, err := strconv.ParseUint(mode, 10, 32); err == nil && cp > 0 {
			if cp == cpUTF8 {
				return strings.ToValidUTF8(string(data), "\uFFFD")
			}
			return winLegacyTextCodec{codePage: uint32(cp)}.decode(data)
		}
		// Unknown value: fall back to auto-detection.
	}

	cp := windows.GetACP()
	if cp == 0 || cp == cpUTF8 {
		return strings.ToValidUTF8(string(data), "\uFFFD")
	}
	cjk, multiByte := utf8StreamSignals(data)
	codec := winLegacyTextCodec{
		codePage: cp,
		stream:   streamSignals{hasCJK: cjk, hasMultiByteUTF8: multiByte},
	}
	return normalizeProcessOutput(data, codec)
}

type winLegacyTextCodec struct {
	codePage uint32
	stream   streamSignals
}

func (c winLegacyTextCodec) decodeStrict(p []byte) (string, bool) {
	return c.decodeWithFlags(p, mbErrInvalidChars)
}

func (c winLegacyTextCodec) decode(p []byte) string {
	if s, ok := c.decodeWithFlags(p, 0); ok {
		return s
	}
	return strings.ToValidUTF8(string(p), "\uFFFD")
}

func (c winLegacyTextCodec) decodeWithFlags(p []byte, flags uint32) (string, bool) {
	if len(p) == 0 {
		return "", true
	}

	n, err := windows.MultiByteToWideChar(c.codePage, flags, &p[0], int32(len(p)), nil, 0)
	if err != nil || n <= 0 {
		return "", false
	}

	buf := make([]uint16, n)
	written, err := windows.MultiByteToWideChar(c.codePage, flags, &p[0], int32(len(p)), &buf[0], n)
	if err != nil || written <= 0 {
		return "", false
	}
	return string(utf16.Decode(buf[:written])), true
}

// streamSignals carries whole-stream evidence that captured output is genuine
// UTF-8, which disables legacy-code-page second-guessing for every run.
type streamSignals struct {
	// hasCJK reports a CJK character in the UTF-8 reading of the stream.
	hasCJK bool
	// hasMultiByteUTF8 reports a valid 3- or 4-byte UTF-8 sequence anywhere in
	// the stream.
	hasMultiByteUTF8 bool
}

// preferLegacyForValidUTF8 resolves the two-byte ambiguity between a legacy CJK
// code page and UTF-8 for one maximal non-ASCII run whose bytes are valid
// UTF-8. It only second-guesses such a run when the stream carries no stronger
// UTF-8 signal and the legacy reading is clearly CJK text:
//
//   - Any 3/4-byte UTF-8 sequence, or any CJK character in the UTF-8 reading,
//     is proof of genuine UTF-8 (legacy CJK bytes misread as UTF-8 only ever
//     form two-byte sequences), so the stream is never re-decoded.
//   - Only short runs qualify. For n adjacent Han characters to look like valid
//     UTF-8 at all, every pair must land in the narrow overlap of the two
//     encodings; that coincidence shrinks exponentially with length, so a long
//     run is far more likely real Cyrillic/Greek than Chinese misread.
//   - The legacy decode must succeed and yield CJK text for every rune.
func (c winLegacyTextCodec) preferLegacyForValidUTF8(run []byte) bool {
	if c.stream.hasCJK || c.stream.hasMultiByteUTF8 || !isCJKCodePage(c.codePage) || !hasNonASCII(run) {
		return false
	}

	cjkUTF8, suspiciousUTF8 := countOutputScripts(string(run))
	if cjkUTF8 > 0 || suspiciousUTF8 == 0 || suspiciousUTF8 > ambiguousLegacyRuneLimit {
		return false
	}

	legacy, ok := c.decodeStrict(run)
	if !ok {
		return false
	}
	cjkLegacy, _ := countOutputScripts(legacy)
	return cjkLegacy > 0 && cjkLegacy >= suspiciousUTF8
}

// ambiguousLegacyRuneLimit is the longest run of two-byte sequences that is
// still eligible for legacy re-decoding. Longer runs are trusted as UTF-8.
const ambiguousLegacyRuneLimit = 2

func isCJKCodePage(cp uint32) bool {
	switch cp {
	case 932, 936, 949, 950, 54936:
		return true
	default:
		return false
	}
}

func hasNonASCII(data []byte) bool {
	for _, b := range data {
		if b >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

func countOutputScripts(s string) (cjk, suspicious int) {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			unicode.Is(unicode.Hangul, r):
			cjk++
		case isLegacyMisreadRune(r):
			suspicious++
		}
	}
	return cjk, suspicious
}

// isLegacyMisreadRune reports whether r commonly results from decoding GBK
// (or another DBCS CJK code page) bytes as UTF-8.
func isLegacyMisreadRune(r rune) bool {
	switch {
	case r >= 0x0370 && r <= 0x03ff: // Greek
		return true
	case r >= 0x0400 && r <= 0x052f: // Cyrillic
		return true
	case r >= 0x0530 && r <= 0x058f: // Armenian
		return true
	case r >= 0x0590 && r <= 0x05ff: // Hebrew
		return true
	case r >= 0x0600 && r <= 0x06ff: // Arabic
		return true
	case r >= 0x0700 && r <= 0x074f: // Syriac
		return true
	case r >= 0x0750 && r <= 0x077f: // Arabic Supplement
		return true
	case r >= 0x0780 && r <= 0x07bf: // Thaana
		return true
	default:
		return false
	}
}
