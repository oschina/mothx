package doctor

import (
	"strings"
	"testing"
)

// The doctor probe exercises the real bridge contract without a worker: 401,
// 406, an authorized initialize, and idle listener shutdown.
func TestCheckWorkerBridgeProtocolCoversAuthorizationAndIdleClose(t *testing.T) {
	checks := checkWorkerBridgeProtocol()
	seen := map[string]bool{}
	for _, check := range checks {
		if check.Status == StatusError {
			t.Fatalf("bridge probe failed: %#v", check)
		}
		seen[check.Detail] = true
	}
	for _, want := range []string{"401 without a token", "406 for text/event-stream", "idle listener closed after token revocation"} {
		if !seen[want] {
			t.Fatalf("missing bridge check %q in %#v", want, checks)
		}
	}
	for _, check := range checks {
		if strings.Contains(strings.ToLower(check.Detail), "bearer ") {
			t.Fatalf("doctor check leaked a token: %#v", check)
		}
	}
}
