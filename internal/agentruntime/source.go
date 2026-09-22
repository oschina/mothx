package agentruntime

import (
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// SourceResolutionInput contains all source candidates available at a runtime
// boundary. Persisted binding and session header are authoritative for existing
// sessions; request source is only eligible for an unbound session.
type SourceResolutionInput struct {
	Binding       *session.Binding
	SessionHeader *session.Header
	Current       RuntimeSource
	Requested     RuntimeSource
}

// SourceResolution describes the effective source and any contradictory
// persisted identity discovered while resolving it.
type SourceResolution struct {
	Source      RuntimeSource
	Conflicted  bool
	Diagnostics []string
}

// SourceConflictError reports contradictory persisted/runtime policy identity.
// Adapter entry is supplied as Requested and therefore does not conflict with
// an authoritative binding or session header.
type SourceConflictError struct {
	Diagnostics []string
}

func (e *SourceConflictError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "runtime source conflict"
	}
	return "runtime source conflict: " + strings.Join(e.Diagnostics, "; ")
}

// ResolveSource applies the source precedence required by the Runtime boundary.
// A persisted binding wins over a session header, which wins over the current
// runtime source, which wins over a request source. Conflicting persisted
// values are reported instead of silently being discarded.
func ResolveSource(input SourceResolutionInput) SourceResolution {
	binding := SourceFromChannelType("")
	if input.Binding != nil {
		binding = SourceFromChannelType(input.Binding.ChannelType)
	}
	header := SourceFromSessionHeader(input.SessionHeader)

	result := SourceResolution{}
	if binding != SourceUnknown {
		result.Source = binding
	} else if header != SourceUnknown {
		result.Source = header
	} else if input.Current != SourceUnknown {
		result.Source = input.Current
	} else {
		result.Source = input.Requested
	}

	if binding != SourceUnknown && header != SourceUnknown && binding != header {
		result.Conflicted = true
		result.Diagnostics = append(result.Diagnostics,
			fmt.Sprintf("binding source %q conflicts with session header source %q", binding, header))
	}
	if binding != SourceUnknown && input.Current != SourceUnknown && binding != input.Current {
		result.Conflicted = true
		result.Diagnostics = append(result.Diagnostics,
			fmt.Sprintf("binding source %q conflicts with current runtime source %q", binding, input.Current))
	}
	if header != SourceUnknown && input.Current != SourceUnknown && header != input.Current {
		result.Conflicted = true
		result.Diagnostics = append(result.Diagnostics,
			fmt.Sprintf("session header source %q conflicts with current runtime source %q", header, input.Current))
	}
	if result.Source == SourceUnknown && !isKnownRequestedSource(input.Requested) {
		result.Source = SourceUnknown
	}
	return result
}

// ResolveSourceFromSession loads the persisted binding before applying the
// source precedence rules. It is intended for existing-session recovery paths.
func ResolveSourceFromSession(sessionDir, sessionID string, input SourceResolutionInput) (SourceResolution, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SourceResolution{}, fmt.Errorf("session ID is required")
	}
	if err := validateSourceCandidates(input); err != nil {
		return SourceResolution{}, err
	}
	binding, err := session.FindBindingBySessionID(sessionDir, sessionID)
	if err != nil {
		return SourceResolution{}, fmt.Errorf("find session binding: %w", err)
	}
	input.Binding = binding
	resolved := ResolveSource(input)
	if resolved.Conflicted {
		return resolved, &SourceConflictError{Diagnostics: append([]string(nil), resolved.Diagnostics...)}
	}
	return resolved, nil
}

func isKnownRequestedSource(source RuntimeSource) bool {
	switch source {
	case SourceTUI, SourceWebUI, SourceWeChat, SourceFeishu, SourceACP, SourceCLI, SourceCron, SourceUnknown:
		return true
	default:
		return false
	}
}

// PolicyForSource returns the default mode policy associated with a resolved
// source. Channel sources retain their forced yolo invariant.
func PolicyForSource(source RuntimeSource, defaultMode string) ExecutionPolicy {
	return ExecutionPolicy{Source: source, DefaultMode: strings.TrimSpace(defaultMode)}
}

// ResolvePolicy resolves source and then applies the mode policy to the same
// identity, preventing display and execution paths from selecting different
// sources or defaults.
func ResolvePolicy(input SourceResolutionInput, sessionMode, requestedMode, defaultMode string) (SourceResolution, string, error) {
	if err := validateSourceCandidates(input); err != nil {
		return SourceResolution{}, "", err
	}
	resolved := ResolveSource(input)
	if resolved.Conflicted {
		return resolved, "", &SourceConflictError{Diagnostics: append([]string(nil), resolved.Diagnostics...)}
	}
	if resolved.Source == SourceUnknown && input.Requested != SourceUnknown && !isKnownRequestedSource(input.Requested) {
		return resolved, "", fmt.Errorf("unknown runtime source %q", input.Requested)
	}
	mode, err := PolicyForSource(resolved.Source, defaultMode).ResolveMode(sessionMode, requestedMode)
	return resolved, mode, err
}

// ResolvePolicyFromSession loads authoritative binding state and resolves mode
// through the same policy used by live Runtime instances.
func ResolvePolicyFromSession(sessionDir, sessionID string, input SourceResolutionInput, sessionMode, requestedMode, defaultMode string) (SourceResolution, string, error) {
	resolved, err := ResolveSourceFromSession(sessionDir, sessionID, input)
	if err != nil {
		return resolved, "", err
	}
	if resolved.Source == SourceUnknown && input.Requested != SourceUnknown && !isKnownRequestedSource(input.Requested) {
		return resolved, "", fmt.Errorf("unknown runtime source %q", input.Requested)
	}
	mode, err := PolicyForSource(resolved.Source, defaultMode).ResolveMode(sessionMode, requestedMode)
	return resolved, mode, err
}

func validateSourceCandidates(input SourceResolutionInput) error {
	for name, source := range map[string]RuntimeSource{
		"current runtime": input.Current,
		"requested":       input.Requested,
	} {
		if source != SourceUnknown && !isKnownRequestedSource(source) {
			return fmt.Errorf("unknown %s source %q", name, source)
		}
	}
	return nil
}
