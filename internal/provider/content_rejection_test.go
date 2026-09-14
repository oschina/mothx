package provider

import (
	"errors"
	"net/http"
	"testing"
)

func TestIsContentRejectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"dashscope image inspection",
			errors.New(`API error 400: {"code":"InvalidParameter","message":"<400> InternalError.Algo.DataInspectionFailed: Input image data may contain inappropriate content.","request_id":"abc"}`),
			true,
		},
		{"inappropriate content", errors.New("Input image data may contain inappropriate content"), true},
		{"content policy", errors.New("Your request was blocked by our content policy"), true},
		{"content filter", errors.New("response rejected by content_filter"), true},
		{"ordinary bad request", errors.New(`API error 400: {"error":{"message":"invalid parameter: model"}}`), false},
		{"rate limited", errors.New("API error 429: rate limit exceeded"), false},
		{"context overflow", errors.New("maximum context length is 8192 tokens"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContentRejectionError(tc.err); got != tc.want {
				t.Fatalf("IsContentRejectionError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRetryableSkipsContentRejection(t *testing.T) {
	rejected := errors.New(`API error 400: {"message":"Input image data may contain inappropriate content"}`)
	if IsRetryable(rejected, http.StatusBadRequest) {
		t.Fatal("content rejection must not be retryable even as HTTP 400")
	}
	// An ordinary 400 keeps the historical retryable behavior.
	if !IsRetryable(errors.New(`API error 400: {"error":{"message":"invalid parameter"}}`), http.StatusBadRequest) {
		t.Fatal("ordinary 400 must stay retryable")
	}
}
