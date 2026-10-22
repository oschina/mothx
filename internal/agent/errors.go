package agent

import "errors"

// ErrAgentStuck marks a run stopped by the loop guard because consecutive
// tool-only turns produced no text or reasoning output. Runtime error
// classification preserves this reason instead of collapsing it into a generic
// incomplete outcome.
var ErrAgentStuck = errors.New("agent appears stuck")
