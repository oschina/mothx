package openaiapi

import "sync"

// chainReleases composes release functions so a single call releases all of
// them, at most once each. It lets a run keep its existing single
// runtimeRelease call sites while also returning the concurrency slot.
func chainReleases(releases ...func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, release := range releases {
				if release != nil {
					release()
				}
			}
		})
	}
}

// acquireRunSlot reserves one entry of the server-wide run concurrency limit.
// maxConcurrentRequests is documented as the number of concurrent Agent Runs
// (default 0 = unlimited), so every Serve-owned run entry must share this one
// semaphore: a streaming chat completion, a submitted background run, and an
// externally submitted Responses-background run.
//
// The returned release is idempotent and must be called exactly once when the
// run reaches its terminal state (not when the HTTP handler returns). A nil or
// unlimited server returns a no-op release with ok=true, preserving today's
// behavior when the limit is not configured.
func (s *Server) acquireRunSlot() (release func(), ok bool) {
	if s == nil || s.runSlots == nil {
		return func() {}, true
	}
	select {
	case s.runSlots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-s.runSlots }) }, true
	default:
		return func() {}, false
	}
}
