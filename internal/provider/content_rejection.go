package provider

import "strings"

// contentRejectionPatterns matches provider error messages that indicate a
// permanent content-policy refusal rather than a transient failure. These are
// commonly raised when an image (or other content) fails the provider's
// content-inspection/moderation filter, for example DashScope's
// "InternalError.Algo.DataInspectionFailed: Input image data may contain
// inappropriate content." Retrying the identical request can never succeed, so
// these must not consume the retry budget.
//
// Patterns are matched case-insensitively as substrings. Keep the list specific
// so ordinary 4xx request errors are not misclassified as permanent refusals.
var contentRejectionPatterns = []string{
	"datainspectionfailed",
	"data inspection",
	"input image data may contain",
	"inappropriate content",
	"content policy",
	"content moderation",
	"content_filter",
	"content filter",
	"prohibited content",
	"flagged as sensitive",
}

// IsContentRejectionError reports whether err is a permanent provider refusal
// caused by content inspection/moderation. Such failures are terminal for the
// offending content: automatic retries and stream-continuation retries must not
// re-send it.
func IsContentRejectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range contentRejectionPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}
