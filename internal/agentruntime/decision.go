package agentruntime

import (
	"fmt"
	"sort"
	"sync"
)

// DecisionKind identifies an interactive decision that can pause a run.
type DecisionKind string

const (
	DecisionApproval DecisionKind = "approval"
	DecisionQuestion DecisionKind = "question"
)

// DecisionRequest is the adapter-neutral identity of a pending decision.
type DecisionRequest struct {
	ID        string
	RunID     string
	SessionID string
	Kind      DecisionKind
	Resolve   func(string) error
}

// DecisionResolution is the adapter-neutral result of resolving a decision.
type DecisionResolution struct {
	ID     string
	Kind   DecisionKind
	Status string
	Value  string
}

// DecisionService owns pending decision identity and first-response-wins
// semantics. Protocol-specific payloads and rendering remain in adapters.
type DecisionService struct {
	mu        sync.Mutex
	pending   map[string]DecisionRequest
	resolvers map[string]func(string) error
	resolving map[string]struct{}
	resolved  map[string]DecisionResolution
	// resolvedRequests retains identity for idempotent retries after commit.
	resolvedRequests map[string]DecisionRequest
}

// Rehydrate restores the latest pending durable decisions without binding
// protocol callbacks. Adapters may bind callbacks after reconnect/restore.
// Rehydration is idempotent for an already identical pending decision.
func (s *DecisionService) Rehydrate(records []DecisionRecord) ([]DecisionRequest, error) {
	if s == nil {
		return nil, fmt.Errorf("decision service is nil")
	}
	pending := ReplayDecisions(records)
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[string]DecisionRequest)
	}
	if s.resolvers == nil {
		s.resolvers = make(map[string]func(string) error)
	}
	if s.resolving == nil {
		s.resolving = make(map[string]struct{})
	}
	if s.resolved == nil {
		s.resolved = make(map[string]DecisionResolution)
	}
	if s.resolvedRequests == nil {
		s.resolvedRequests = make(map[string]DecisionRequest)
	}
	result := make([]DecisionRequest, 0, len(ids))
	for _, id := range ids {
		record := pending[id]
		request := DecisionRequest{ID: record.ID, RunID: record.RunID, SessionID: record.SessionID, Kind: record.Kind}
		if existing, ok := s.pending[id]; ok {
			if existing.RunID != request.RunID || existing.SessionID != request.SessionID || existing.Kind != request.Kind {
				return nil, fmt.Errorf("rehydrated decision conflicts with pending decision: %s", id)
			}
			result = append(result, existing)
			continue
		}
		s.pending[id] = request
		result = append(result, request)
	}
	return result, nil
}
func (s *DecisionService) Register(request DecisionRequest) error {
	if s == nil {
		return fmt.Errorf("decision service is nil")
	}
	if request.ID == "" || request.RunID == "" {
		return fmt.Errorf("decision ID and run ID are required")
	}
	if request.Kind != DecisionApproval && request.Kind != DecisionQuestion {
		return fmt.Errorf("unsupported decision kind: %s", request.Kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[string]DecisionRequest)
	}
	if s.resolvers == nil {
		s.resolvers = make(map[string]func(string) error)
	}
	if s.resolving == nil {
		s.resolving = make(map[string]struct{})
	}
	if s.resolved == nil {
		s.resolved = make(map[string]DecisionResolution)
	}
	if s.resolvedRequests == nil {
		s.resolvedRequests = make(map[string]DecisionRequest)
	}
	if _, ok := s.pending[request.ID]; ok {
		return fmt.Errorf("decision already pending: %s", request.ID)
	}
	if prior, ok := s.resolved[request.ID]; ok {
		return fmt.Errorf("decision was already resolved: %s (%s)", request.ID, prior.Status)
	}
	s.pending[request.ID] = request
	return nil
}

// Bind associates an adapter-owned resume callback with a registered decision.
// The callback must succeed before Resolve consumes the pending decision.
func (s *DecisionService) Bind(id string, resolve func(string) error) error {
	if s == nil {
		return fmt.Errorf("decision service is nil")
	}
	if id == "" || resolve == nil {
		return fmt.Errorf("decision ID and resolve callback are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[id]; !ok {
		return fmt.Errorf("decision is not pending: %s", id)
	}
	if s.resolvers == nil {
		s.resolvers = make(map[string]func(string) error)
	}
	s.resolvers[id] = resolve
	return nil
}

func (s *DecisionService) Resolve(resolution DecisionResolution) (DecisionRequest, error) {
	return s.resolveWith(resolution, nil)
}

// ResolveWith performs the adapter persistence commit before consuming the
// pending request. A callback or commit failure leaves the request retryable.
func (s *DecisionService) ResolveWith(resolution DecisionResolution, commit func(DecisionRequest) error) (DecisionRequest, error) {
	return s.resolveWith(resolution, commit)
}

// Discard removes a pending decision without resolving it. It is used when a
// caller has already recorded a terminal outcome in the durable ledger but the
// service could not commit that resolution: leaving the in-memory pending entry
// would let a reconnect project a decision the ledger says is finished.
//
// Discard never removes a resolved decision and never invokes a resolver.
func (s *DecisionService) Discard(id string) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	delete(s.pending, id)
	delete(s.resolvers, id)
	delete(s.resolving, id)
	s.mu.Unlock()
}

func (s *DecisionService) resolveWith(resolution DecisionResolution, commit func(DecisionRequest) error) (DecisionRequest, error) {
	if s == nil {
		return DecisionRequest{}, fmt.Errorf("decision service is nil")
	}
	if resolution.Status == "" {
		resolution.Status = "resolved"
	}
	s.mu.Lock()
	request, ok := s.pending[resolution.ID]
	if !ok {
		if prior, resolved := s.resolved[resolution.ID]; resolved {
			if (resolution.Kind == "" || resolution.Kind == prior.Kind) && resolution.Status == prior.Status && resolution.Value == prior.Value {
				if priorRequest, exists := s.resolvedRequests[resolution.ID]; exists {
					s.mu.Unlock()
					return priorRequest, nil
				}
				s.mu.Unlock()
				return DecisionRequest{ID: resolution.ID, Kind: prior.Kind}, nil
			}
			s.mu.Unlock()
			return DecisionRequest{}, fmt.Errorf("decision was already resolved: %s", resolution.ID)
		}
		s.mu.Unlock()
		return DecisionRequest{}, fmt.Errorf("decision is no longer pending: %s", resolution.ID)
	}
	if resolution.Kind != "" && resolution.Kind != request.Kind {
		s.mu.Unlock()
		return DecisionRequest{}, fmt.Errorf("decision kind mismatch: %s", resolution.ID)
	}
	if _, ok := s.resolving[resolution.ID]; ok {
		s.mu.Unlock()
		return DecisionRequest{}, fmt.Errorf("decision resolution is already in progress: %s", resolution.ID)
	}
	resolver := s.resolvers[resolution.ID]
	if s.resolving == nil {
		s.resolving = make(map[string]struct{})
	}
	s.resolving[resolution.ID] = struct{}{}
	s.mu.Unlock()
	if resolver != nil {
		if err := resolver(resolution.Value); err != nil {
			s.mu.Lock()
			delete(s.resolving, resolution.ID)
			s.mu.Unlock()
			return request, err
		}
	}
	if commit != nil {
		if err := commit(request); err != nil {
			s.mu.Lock()
			delete(s.resolving, resolution.ID)
			s.mu.Unlock()
			return request, err
		}
	}
	s.mu.Lock()
	delete(s.resolving, resolution.ID)
	delete(s.pending, resolution.ID)
	delete(s.resolvers, resolution.ID)
	if s.resolved == nil {
		s.resolved = make(map[string]DecisionResolution)
	}
	if s.resolvedRequests == nil {
		s.resolvedRequests = make(map[string]DecisionRequest)
	}
	s.resolved[resolution.ID] = resolution
	s.resolvedRequests[resolution.ID] = request
	s.mu.Unlock()
	return request, nil
}

// ClearRunWithValue removes all decisions for a Run and invokes their resolver
// callbacks with the supplied value. It is used for cancellation and timeout
// paths where no protocol resolution is available.
func (s *DecisionService) ClearRunWithValue(runID, value string) []DecisionRequest {
	if s == nil || runID == "" {
		return nil
	}
	s.mu.Lock()
	var requests []DecisionRequest
	var callbacks = make(map[string]func(string) error)
	for id, request := range s.pending {
		if request.RunID != runID {
			continue
		}
		requests = append(requests, request)
		callbacks[id] = s.resolvers[id]
		if s.resolving == nil {
			s.resolving = make(map[string]struct{})
		}
		s.resolving[id] = struct{}{}
	}
	s.mu.Unlock()
	var cleared []DecisionRequest
	for _, request := range requests {
		callbackErr := error(nil)
		if callback := callbacks[request.ID]; callback != nil {
			callbackErr = callback(value)
		}
		s.mu.Lock()
		delete(s.resolving, request.ID)
		if callbackErr == nil {
			delete(s.pending, request.ID)
			delete(s.resolvers, request.ID)
			if s.resolved == nil {
				s.resolved = make(map[string]DecisionResolution)
			}
			if s.resolvedRequests == nil {
				s.resolvedRequests = make(map[string]DecisionRequest)
			}
			s.resolved[request.ID] = DecisionResolution{ID: request.ID, Kind: request.Kind, Status: "cancelled", Value: value}
			s.resolvedRequests[request.ID] = request
			cleared = append(cleared, request)
		}
		s.mu.Unlock()
	}
	return cleared
}
func (s *DecisionService) ClearRun(runID string) []DecisionRequest {
	if s == nil || runID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var cleared []DecisionRequest
	for id, request := range s.pending {
		if request.RunID == runID {
			cleared = append(cleared, request)
			delete(s.pending, id)
			delete(s.resolvers, id)
			delete(s.resolving, id)
			if s.resolved == nil {
				s.resolved = make(map[string]DecisionResolution)
			}
			if s.resolvedRequests == nil {
				s.resolvedRequests = make(map[string]DecisionRequest)
			}
			s.resolved[id] = DecisionResolution{ID: id, Kind: request.Kind, Status: "cancelled"}
			s.resolvedRequests[id] = request
		}
	}
	return cleared
}

func (s *DecisionService) Pending() []DecisionRequest {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]DecisionRequest, 0, len(s.pending))
	for _, request := range s.pending {
		result = append(result, request)
	}
	return result
}
