package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/oschina/mothx/internal/provider"
)

// FailureClass identifies the stable, adapter-neutral category of a failed
// execution. Adapters render it differently, but must not infer it from an
// English provider error string.
type FailureClass string

const (
	FailureValidation  FailureClass = "validation"
	FailurePolicy      FailureClass = "policy"
	FailureTransient   FailureClass = "transient"
	FailureProvider    FailureClass = "provider"
	FailureTool        FailureClass = "tool"
	FailureTransport   FailureClass = "transport"
	FailureCancelled   FailureClass = "canceled"
	FailureIncomplete  FailureClass = "incomplete"
	FailurePersistence FailureClass = "persistence"
	FailureInternal    FailureClass = "internal"
)

// RunPhase indicates where a failure or retry occurred.
type RunPhase string

const (
	PhaseAdmission       RunPhase = "admission"
	PhaseModel           RunPhase = "model"
	PhaseContext         RunPhase = "context"
	PhaseTool            RunPhase = "tool"
	PhaseApproval        RunPhase = "approval"
	PhasePersistence     RunPhase = "persistence"
	PhaseTransport       RunPhase = "transport"
	PhaseTerminalization RunPhase = "terminalization"
)

// RetryMode states who may make progress after a failure. Automatic retry is
// owned by Agent Core/Runtime; adapters only project its progress. Reconcile
// means the caller must first discover whether a previous submission exists.
type RetryMode string

const (
	RetryNone             RetryMode = "none"
	RetryAutomatic        RetryMode = "automatic"
	RetryReconcile        RetryMode = "reconcile"
	RetryUser             RetryMode = "user"
	RetryDecisionRequired RetryMode = "decision_required"
)

// SideEffectState is deliberately conservative. A runtime must not replay an
// execution with unknown or mutating side effects without an explicit policy
// decision.
type SideEffectState string

const (
	SideEffectNone     SideEffectState = "none"
	SideEffectReadOnly SideEffectState = "read_only"
	SideEffectMutating SideEffectState = "mutating"
	SideEffectUnknown  SideEffectState = "unknown"
)

// ErrorInfo is the durable, adapter-neutral description of an execution
// failure. Message is the user-facing description and Detail preserves the
// provider diagnostic that caused it. Detail is bounded and redacted before
// persistence so failures remain useful without turning session history into
// a credentials sink.
type ErrorInfo struct {
	Code            string          `json:"code,omitempty"`
	Type            string          `json:"type,omitempty"`
	FailureClass    FailureClass    `json:"failureClass,omitempty"`
	Phase           RunPhase        `json:"phase,omitempty"`
	MessageKey      string          `json:"messageKey,omitempty"`
	Message         string          `json:"message,omitempty"`
	Detail          string          `json:"detail,omitempty"`
	RetryMode       RetryMode       `json:"retryMode,omitempty"`
	Retryable       bool            `json:"retryable,omitempty"`
	RetryAfterMS    int             `json:"retryAfterMs,omitempty"`
	Attempt         int             `json:"attempt,omitempty"`
	MaxAttempts     int             `json:"maxAttempts,omitempty"`
	SideEffectState SideEffectState `json:"sideEffectState,omitempty"`
	PartialOutput   bool            `json:"partialOutput,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	IntentID        string          `json:"intentId,omitempty"`
	RequestID       string          `json:"requestId,omitempty"`
}

// RetryInfo is a non-terminal progress record. It is persisted as a run event
// so reconnecting adapters can render the same automatic retry state.
type RetryInfo struct {
	Attempt      int      `json:"attempt,omitempty"`
	MaxAttempts  int      `json:"maxAttempts,omitempty"`
	Phase        RunPhase `json:"phase,omitempty"`
	ReasonCode   string   `json:"reasonCode,omitempty"`
	RetryAfterMS int      `json:"retryAfterMs,omitempty"`
	Continue     bool     `json:"continue,omitempty"`
	MessageKey   string   `json:"messageKey,omitempty"`
	Message      string   `json:"message,omitempty"`
}

// ErrorClassificationOptions adds execution facts that cannot be derived from
// an error value alone. The defaults are intentionally safe for callers that
// have not observed tools or output yet.
type ErrorClassificationOptions struct {
	Code            string
	Type            string
	Phase           RunPhase
	Message         string
	Detail          string
	MessageKey      string
	HTTPStatus      int
	RetryAfterMS    int
	Attempt         int
	MaxAttempts     int
	SideEffectState SideEffectState
	PartialOutput   bool
	RunID           string
	IntentID        string
	RequestID       string
}

// ClassifyError converts a raw failure into the shared durable contract. It
// intentionally has no adapter/UI dependencies, and only marks automatic
// retries safe before output or side effects exist.
func ClassifyError(err error, opts ErrorClassificationOptions) ErrorInfo {
	info := ErrorInfo{
		Code:            strings.TrimSpace(opts.Code),
		Type:            strings.TrimSpace(opts.Type),
		Phase:           opts.Phase,
		MessageKey:      strings.TrimSpace(opts.MessageKey),
		Message:         safeMessage(err, opts.Message),
		Detail:          diagnosticMessage(err, opts.Detail),
		RetryAfterMS:    opts.RetryAfterMS,
		Attempt:         opts.Attempt,
		MaxAttempts:     opts.MaxAttempts,
		SideEffectState: opts.SideEffectState,
		PartialOutput:   opts.PartialOutput,
		RunID:           opts.RunID,
		IntentID:        opts.IntentID,
		RequestID:       opts.RequestID,
	}
	if info.SideEffectState == "" {
		info.SideEffectState = SideEffectNone
	}
	if info.Phase == "" {
		info.Phase = PhaseModel
	}

	switch {
	case errors.Is(err, context.Canceled):
		return applyErrorDefaults(info, "run_cancelled", "canceled", FailureCancelled, RetryUser, false, "run.error.cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return applyErrorDefaults(info, "run_timed_out", "timeout_error", FailureCancelled, retryModeForSafety(info), false, "run.error.timedOut")
	case provider.IsContextOverflowError(err):
		info.Phase = PhaseContext
		return applyErrorDefaults(info, "context_overflow", "context_error", FailureProvider, retryModeForSafety(info), false, "run.error.contextOverflow")
	case provider.IsContentRejectionError(err):
		// A permanent content-policy refusal. The Runtime strips the offending
		// content before reaching this classification; when it still surfaces, the
		// user must change the content (or accept that it cannot be sent).
		return applyErrorDefaults(info, "content_rejected", "content_error", FailurePolicy, RetryUser, false, "run.error.contentRejected")
	case provider.IsRetryable(err, opts.HTTPStatus):
		code, key := retryableErrorCode(err, opts.HTTPStatus)
		return applyErrorDefaults(info, code, "provider_error", FailureTransient, retryModeForSafety(info), true, key)
	}

	if info.Code == "" {
		info.Code = "run_failed"
	}
	if info.Type == "" {
		info.Type = "server_error"
	}
	if info.MessageKey == "" {
		info.MessageKey = "run.error.failed"
	}
	if info.RetryMode == "" {
		info.RetryMode = retryModeForSafety(info)
	}
	if info.RetryMode == RetryAutomatic {
		info.RetryMode = RetryUser
	}
	info.Retryable = info.RetryMode == RetryUser || info.RetryMode == RetryDecisionRequired
	if info.FailureClass == "" {
		info.FailureClass = FailureInternal
	}
	return info
}

func applyErrorDefaults(info ErrorInfo, code, typ string, class FailureClass, mode RetryMode, retryable bool, key string) ErrorInfo {
	if info.Code == "" {
		info.Code = code
	}
	if info.Type == "" {
		info.Type = typ
	}
	info.FailureClass = class
	if info.MessageKey == "" {
		info.MessageKey = key
	}
	info.RetryMode = mode
	info.Retryable = retryable || mode == RetryAutomatic || mode == RetryUser || mode == RetryDecisionRequired
	return info
}

func retryModeForSafety(info ErrorInfo) RetryMode {
	if info.PartialOutput || info.SideEffectState == SideEffectMutating || info.SideEffectState == SideEffectUnknown {
		return RetryDecisionRequired
	}
	return RetryAutomatic
}

func retryableErrorCode(err error, status int) (string, string) {
	if status == 429 || containsError(err, "429", "rate limit", "rate_limit") {
		return "rate_limited", "run.error.rateLimited"
	}
	if status >= 500 || containsStatus(err, 500, 599) || containsError(err, "overloaded", "server_error") {
		return "provider_unavailable", "run.error.providerUnavailable"
	}
	if (status >= 400 && status < 500) || containsStatus(err, 400, 499) {
		return "provider_request_failed", "run.error.providerRequestFailed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) || containsError(err, "connection", "dns", "eof") {
		return "network_unavailable", "run.error.networkUnavailable"
	}
	if containsError(err, "timeout", "deadline") {
		return "provider_timeout", "run.error.providerTimeout"
	}
	return "provider_interrupted", "run.error.providerInterrupted"
}

func containsStatus(err error, min, max int) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for status := min; status <= max; status++ {
		code := fmt.Sprintf("%d", status)
		if strings.Contains(message, "http "+code) || strings.Contains(message, "api error "+code) || strings.Contains(message, "status "+code) {
			return true
		}
	}
	return false
}

func containsError(err error, parts ...string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, part := range parts {
		if strings.Contains(message, strings.ToLower(part)) {
			return true
		}
	}
	return false
}

func safeMessage(err error, override string) string {
	if message := strings.TrimSpace(override); message != "" {
		return diagnosticMessage(nil, message)
	}
	if err == nil {
		return "The run could not be completed."
	}
	// Raw provider errors can include provider-specific details. The adapters
	// should localize MessageKey first; this fallback stays intentionally broad.
	if errors.Is(err, context.Canceled) {
		return "The run was cancelled."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The run timed out."
	}
	if diagnostic := diagnosticMessage(err, ""); diagnostic != "" {
		return diagnostic
	}
	return "The run could not be completed."
}

const maxDiagnosticLength = 4096

var sensitiveDiagnosticPattern = regexp.MustCompile(`(?i)(api[-_ ]?key|authorization|access[-_ ]?token|refresh[-_ ]?token|password|secret)\s*([:=])\s*([^\s,;]+)`)

// diagnosticMessage returns the actionable part of a provider error while
// bounding and redacting it before it enters durable run/session records.
// Provider responses are not trusted to omit credentials or unbounded bodies.
func diagnosticMessage(err error, override string) string {
	diagnostic := strings.TrimSpace(override)
	if diagnostic == "" && err != nil {
		diagnostic = strings.TrimSpace(err.Error())
	}
	if diagnostic == "" {
		return ""
	}
	diagnostic = sensitiveDiagnosticPattern.ReplaceAllString(diagnostic, "$1$2[redacted]")
	if len(diagnostic) > maxDiagnosticLength {
		diagnostic = diagnostic[:maxDiagnosticLength-3] + "..."
	}
	return diagnostic
}

// DisplayErrorMessage returns a truthful, concise message for adapters. It
// keeps an explicit category message when present, but never drops the
// provider detail that explains what actually failed.
func DisplayErrorMessage(info ErrorInfo) string {
	message := strings.TrimSpace(info.Message)
	detail := strings.TrimSpace(info.Detail)
	if detail == "" || strings.EqualFold(message, detail) {
		if message != "" {
			return message
		}
		return detail
	}
	if message == "" {
		return detail
	}
	return fmt.Sprintf("%s: %s", message, detail)
}
