// Package agentruntime contains front-end-neutral execution policy primitives.
package agentruntime

import (
	"fmt"
	"strings"

	"github.com/startvibecoding/mothx/internal/session"
)

// RuntimeSource identifies the runtime that owns a session's execution policy.
type RuntimeSource string

// Source is retained as a concise compatibility alias for RuntimeSource.
type Source = RuntimeSource

const (
	SourceUnknown RuntimeSource = ""
	SourceTUI     RuntimeSource = "tui"
	SourceWebUI   RuntimeSource = "webui"
	SourceWeChat  RuntimeSource = "wechat"
	SourceFeishu  RuntimeSource = "feishu"
	SourceACP     RuntimeSource = "acp"
	SourceCLI     RuntimeSource = "cli"
	SourceCron    RuntimeSource = "cron"
)

const (
	ModePlan  = "plan"
	ModeAgent = "agent"
	ModeYolo  = "yolo"
	ModeOS    = "os"
)

// ExecutionPolicy describes the mode semantics shared by all adapters for one run.
type ExecutionPolicy struct {
	Source      RuntimeSource
	DefaultMode string
}

// Policy is retained as a concise compatibility alias for ExecutionPolicy.
type Policy = ExecutionPolicy

// ModeResolver applies an execution policy consistently at every adapter boundary.
type ModeResolver struct {
	Policy ExecutionPolicy
}

// Resolve returns the effective mode for the resolver's policy.
func (r ModeResolver) Resolve(sessionMode, requestedMode string) (string, error) {
	return r.Policy.ResolveMode(sessionMode, requestedMode)
}

// SourceFromChannelType maps persisted channel bindings to a runtime source.
func SourceFromChannelType(channelType string) RuntimeSource {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case string(SourceWeChat):
		return SourceWeChat
	case string(SourceFeishu):
		return SourceFeishu
	default:
		return SourceUnknown
	}
}

// SourceFromSessionHeader derives policy ownership from persisted session identity.
func SourceFromSessionHeader(header *session.Header) RuntimeSource {
	if header == nil {
		return SourceUnknown
	}
	return SourceFromChannelType(header.ChannelType)
}

// HasForcedMode reports whether a source has a non-overridable execution mode.
func (p Policy) HasForcedMode() bool {
	return p.Source == SourceWeChat || p.Source == SourceFeishu
}

// WaitsForMembers reports whether a source keeps a conversational lead's run
// open for its still-running members (see agent.ComposeFollowUps). Only sources
// with an attentive human in the loop qualify: a member's completion or question
// can be answered within the same run. On headless or asynchronous sources
// (CLI, cron, messaging channels) member notifications are delivered at the
// next iteration or the next run instead, so a single run never blocks for
// minutes unattended. A bound expert team always waits regardless of source.
func (s RuntimeSource) WaitsForMembers() bool {
	switch s {
	case SourceTUI, SourceWebUI, SourceACP:
		return true
	default:
		return false
	}
}

// ForcedMode returns the source-mandated mode, if any.
func (p Policy) ForcedMode() string {
	if p.HasForcedMode() {
		return ModeYolo
	}
	return ""
}

// ResolveMode returns the one effective mode for UI display, agent construction,
// run records, approvals, and recovery. Bound WeChat and Feishu sessions always
// execute in yolo mode; a request or persisted capability cannot downgrade them.
func (p Policy) ResolveMode(sessionMode, requestedMode string) (string, error) {
	requestedMode = strings.TrimSpace(requestedMode)
	// A source-forced mode is the security invariant. Ignore malformed or
	// conflicting adapter hints rather than allowing a fallback to leak an
	// unvalidated mode into an execution path.
	if forced := p.ForcedMode(); forced != "" {
		return forced, nil
	}
	if requestedMode != "" && !IsValidMode(requestedMode) {
		return "", fmt.Errorf("invalid mode %q", requestedMode)
	}
	sessionMode = strings.TrimSpace(sessionMode)
	if sessionMode != "" && !IsValidMode(sessionMode) {
		return "", fmt.Errorf("invalid mode %q", sessionMode)
	}
	if requestedMode != "" {
		return requestedMode, nil
	}
	if sessionMode != "" {
		return sessionMode, nil
	}
	defaultMode := strings.TrimSpace(p.DefaultMode)
	if defaultMode == "" {
		defaultMode = ModeYolo
	}
	if !IsValidMode(defaultMode) {
		return "", fmt.Errorf("invalid default mode %q", defaultMode)
	}
	return defaultMode, nil
}

// IsValidMode reports whether mode is one of the public execution modes.
func IsValidMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case ModePlan, ModeAgent, ModeYolo, ModeOS:
		return true
	default:
		return false
	}
}

// ResolveUnattendedMode derives the execution mode for unattended derived
// runs such as ESM role sub-agents. Unattended runs must never stop on
// interactive approval, so only os is inherited from the session mode and
// every other session mode (plan/agent/yolo/empty/unknown) falls back to
// yolo. Hard high-risk-command protections remain mode-independent.
func ResolveUnattendedMode(sessionMode string) string {
	if strings.TrimSpace(sessionMode) == ModeOS {
		return ModeOS
	}
	return ModeYolo
}
