package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// httpStatusOriginTimeout is Cloudflare's non-standard HTTP status for an
// upstream origin that did not respond before the proxy timeout.
const httpStatusOriginTimeout = 524

// RetryConfig controls automatic retry behavior for API calls.
type RetryConfig struct {
	Enabled     bool
	MaxRetries  int
	BaseDelayMs int
}

// IsRetryable determines whether an error or HTTP status code warrants a retry.
// Provider gateways frequently use 4xx for temporary quota, routing, and
// compatibility failures, so every HTTP 4xx/5xx response is retryable here.
// The provider retry budget still bounds the number of attempts.
func IsRetryable(err error, statusCode int) bool {
	// Permanent provider refusals (content inspection/moderation) are never
	// transient: the identical request fails again, so they must not consume
	// the retry budget even though they arrive as a 4xx.
	if IsContentRejectionError(err) {
		return false
	}

	// Check HTTP status codes
	if statusCode >= http.StatusBadRequest && statusCode < 600 {
		return true
	}

	if err == nil {
		return false
	}

	// Context cancellation is never retryable (user abort)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// For the HTTP client 30-minute timeout, this wraps as DeadlineExceeded.
		// However, user-initiated context cancellation also uses this.
		// We treat it as retryable only for the HTTP client timeout case,
		// which is distinguishable by the wrapped net.Error.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return true
		}
		return false
	}
	// A truncated HTTP/SSE response is retryable. JSON/SSE decoders
	// commonly return io.ErrUnexpectedEOF when the upstream closes the
	// connection before the final frame is complete.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Network-level transient errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true // timeouts, connection refused, etc.
	}

	// Connection reset, broken pipe, etc.
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}

	// DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Generic "server closed connection" type errors
	errStr := strings.ToLower(err.Error())
	if retryableHTTPStatusPattern.MatchString(errStr) {
		return true
	}
	if strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "internal_error") ||
		strings.Contains(errStr, "server_error") ||
		strings.Contains(errStr, "stream_read_error") ||
		strings.Contains(errStr, "responses stream failed") ||
		strings.Contains(errStr, "rate_limit") ||
		strings.Contains(errStr, "http 502") ||
		strings.Contains(errStr, "http 503") ||
		strings.Contains(errStr, "http 524") {
		return true
	}

	return false
}

var retryableHTTPStatusPattern = regexp.MustCompile(`(?:http|api error|status)\s*[:=]?\s*([45][0-9]{2})`)

// RetryDelay calculates the delay before the next retry attempt using
// exponential backoff with jitter, capped at 30 seconds.
func RetryDelay(attempt int, baseDelayMs int) time.Duration {
	if baseDelayMs <= 0 {
		baseDelayMs = 2000
	}
	delay := float64(baseDelayMs) * math.Pow(2, float64(attempt))
	if delay > 30000 {
		delay = 30000
	}
	return time.Duration(delay) * time.Millisecond
}

// FormatRetryMessage returns a user-visible message for a retry attempt.
func FormatRetryMessage(attempt, maxRetries int, delay time.Duration, err error) string {
	return fmt.Sprintf("Retrying (%d/%d): %s — waiting %s...",
		attempt+1, maxRetries, classifyRetryError(err), formatDelay(delay))
}

// RetryErrorDetail returns only the sanitized reason for a retryable error,
// without the "Retrying (n/m)" wrapper. The result is always single-line and
// length-bounded, so adapters can render it verbatim as a supplementary
// diagnostic. It returns "" for a nil error. This text is presentation-only:
// retry scheduling and control flow must never depend on it.
func RetryErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	return classifyRetryError(err)
}

// classifyRetryError maps an error to a single-line, bounded reason: a JSON
// error payload first, then known transport/HTTP classifications, then a
// truncated raw error fallback.
func classifyRetryError(err error) string {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	// Try to extract a more specific error message from JSON responses
	if msg := extractJSONErrorMessage(errStr); msg != "" {
		return msg
	}

	// Classify the error for a user-friendly message
	switch {
	case strings.Contains(errStr, "524"):
		return "origin timeout (HTTP 524)"
	case strings.Contains(strings.ToLower(errStr), "overloaded"):
		return "server overloaded"
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "DeadlineExceeded"):
		return "request timed out"
	case strings.Contains(errStr, "connection refused"):
		return "connection refused"
	case strings.Contains(errStr, "connection reset"):
		return "connection reset"
	case strings.Contains(errStr, "429"):
		return "rate limited (HTTP 429)"
	case strings.Contains(errStr, "500"):
		return "internal server error (HTTP 500)"
	case strings.Contains(errStr, "502"):
		return "bad gateway (HTTP 502)"
	case strings.Contains(errStr, "503"):
		return "service unavailable (HTTP 503)"
	case strings.Contains(errStr, "504"):
		return "gateway timeout (HTTP 504)"
	case strings.Contains(strings.ToLower(errStr), "stream_read_error"):
		return "upstream stream read error"
	case strings.Contains(errStr, "EOF"):
		return "connection closed unexpectedly"
	default:
		return fmt.Sprintf("error: %s", truncateErr(sanitizeRetryDetail(errStr), 80))
	}
}

// extractJSONErrorMessage attempts to extract the "message" field from a JSON
// error response. It handles common API error formats like OpenAI's error response.
func extractJSONErrorMessage(errStr string) string {
	// Look for JSON in the error string (e.g., "HTTP 400: {\"message\":...}")
	jsonStart := strings.Index(errStr, "{")
	if jsonStart == -1 {
		return ""
	}

	jsonStr := errStr[jsonStart:]
	var errResp struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &errResp); err != nil {
		return ""
	}

	// Check for OpenAI-style error.message field
	if errResp.Error.Message != "" {
		return truncateErr(sanitizeRetryDetail(errResp.Error.Message), 200)
	}

	// Check for top-level message field
	if errResp.Message != "" {
		return truncateErr(sanitizeRetryDetail(errResp.Message), 200)
	}

	return ""
}

// sanitizeRetryDetail collapses newlines and control characters into single
// spaces so retry diagnostics always render as one bounded line and cannot
// inject fake log/status lines into adapters.
func sanitizeRetryDetail(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			pendingSpace = true
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// truncateErr truncates an error string to maxLen bytes without splitting
// multi-byte runes.
func truncateErr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	cut := maxLen - 3
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// formatDelay formats a duration in a human-readable way.
func formatDelay(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
