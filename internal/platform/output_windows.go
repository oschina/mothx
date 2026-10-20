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
// tools write UTF-8. A fully valid UTF-8 stream is normally trusted as UTF-8;
// otherwise legacy-code-page runs are decoded with the Windows conversion API.
//
// Some two-byte legacy sequences are also valid UTF-8 (GBK "写" is D0 B4,
// which decodes as Cyrillic "д" under UTF-8). When the ANSI code page is a
// CJK code page, an ambiguous valid-UTF-8 stream is treated as legacy text if
// decoding it with that code page yields CJK characters.
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

	codec := newLegacyTextCodec()
	if codec == nil {
		return strings.ToValidUTF8(string(data), "\uFFFD")
	}
	if utf8.Valid(data) {
		if c, ok := codec.(ambiguousUTF8Codec); ok && c.preferLegacyForValidUTF8(data) {
			return codec.decode(data)
		}
		return string(data)
	}
	return normalizeProcessOutput(data, codec)
}

func newLegacyTextCodec() legacyTextCodec {
	cp := windows.GetACP()
	if cp == 0 || cp == cpUTF8 {
		return nil
	}
	return winLegacyTextCodec{codePage: cp}
}

type winLegacyTextCodec struct {
	codePage uint32
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

// preferLegacyForValidUTF8 resolves the two-byte ambiguity between a legacy CJK
// code page and UTF-8. It only second-guesses a valid UTF-8 stream when that
// stream looks like GBK bytes misread as Cyrillic/Greek/Hebrew-style text and
// the legacy decoding produces CJK text.
func (c winLegacyTextCodec) preferLegacyForValidUTF8(data []byte) bool {
	if !isCJKCodePage(c.codePage) || !hasNonASCII(data) {
		return false
	}

	cjkUTF8, suspiciousUTF8 := countOutputScripts(string(data))
	if cjkUTF8 > 0 && cjkUTF8 >= suspiciousUTF8 {
		return false
	}
	if suspiciousUTF8 == 0 {
		return false
	}

	legacy, ok := c.decodeStrict(data)
	if !ok {
		return false
	}
	cjkLegacy, _ := countOutputScripts(legacy)
	return cjkLegacy > 0 && cjkLegacy >= suspiciousUTF8
}

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
