// Package agentruntime contains front-end-neutral execution policy primitives.
package agentruntime

import (
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
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
	// ModeRouter is a lead-only orchestration mode. Its exact tool surface is
	// resolved by the Runtime and excludes local mutation/process tools.
	ModeRouter = "router"
)

// ExecutionPolicy describes the mode semantics shared by all adapters for one run.
type ExecutionPolicy struct {
	Source      RuntimeSource
	DefaultMode string
	// IterationBudget governs model-requested iteration renewal for this run's
	// conversational lead. It is resolved once together with the source/mode
	// policy so agent construction, run records, and UI projections share one
	// budget. The zero value resolves to the Runtime defaults when a run is built
	// (see ResolveIterationBudget); renewal is a lead-only capability, so
	// sub-agents and team members keep their capability ceiling.
	IterationBudget agent.IterationBudgetPolicy
}

// Policy is retained as a concise compatibility alias for ExecutionPolicy.
type Policy = ExecutionPolicy

// ResolveIterationBudget returns the normalized iteration-budget policy for a run
// whose soft iteration limit is soft. It is the single owner of the budget
// defaults (the main loop pairs a 400 soft limit with a 600 hard ceiling; a
// supplied soft limit keeps the conventional 2x ceiling, with two renewals, a
// minimum interval of soft/10, and a 16h wall clock). Adapters only supply the
// soft limit (MaxIterations/MaxTurns) and may pre-set IterationBudget to override
// the defaults. The result is always enabled.
func ResolveIterationBudget(policy agent.IterationBudgetPolicy, soft int) agent.IterationBudgetPolicy {
	return policy.Normalize(soft)
}

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
	// Router is a legal session mode but never a legal *default*: the default
	// applies to a session that has no activated ACP worker yet, so it cannot
	// resolve to Router. Treating it as Router fails Agent construction for every
	// new session with no product surface to repair it. Do not widen to yolo
	// either — Router is read-only, so the fail-safe projection of that intent is
	// the read-only plan mode.
	if defaultMode == ModeRouter {
		return ModePlan, nil
	}
	return defaultMode, nil
}

// IsValidMode reports whether mode is one of the public execution modes.
func IsValidMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case ModePlan, ModeAgent, ModeYolo, ModeOS, ModeRouter:
		return true
	default:
		return false
	}
}

// ResolveUnattendedMode derives the execution mode for unattended derived
// runs such as ESM role sub-agents. Unattended runs must never stop on
// interactive approval, so os and router preserve their intentionally narrow
// tool surfaces; every other session mode (plan/agent/yolo/empty/unknown)
// falls back to yolo. Hard high-risk-command protections remain
// mode-independent.
func ResolveUnattendedMode(sessionMode string) string {
	switch strings.TrimSpace(sessionMode) {
	case ModeOS:
		return ModeOS
	case ModeRouter:
		return ModeRouter
	}
	return ModeYolo
}
