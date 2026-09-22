package openaiapi

import "github.com/oschina/mothx/internal/agentruntime"

// pendingDecisionIDsForRun returns the Runtime-owned pending decision identity
// for one run. Payload maps remain the compatibility source for protocol data;
// this helper makes the Runtime decision set authoritative for identity.
func (s *Server) pendingDecisionIDsForRun(sess *APISession, runID string) map[string]agentruntime.DecisionKind {
	result := make(map[string]agentruntime.DecisionKind)
	if sess == nil || runID == "" {
		return result
	}
	if sess.Decisions != nil {
		for _, request := range sess.Decisions.Pending() {
			if request.RunID == runID {
				result[request.ID] = request.Kind
			}
		}
	}
	return result
}
