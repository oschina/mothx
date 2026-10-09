package session

import (
	"encoding/json"
	"expvar"
	"testing"
	"time"
)

func TestLeaseHeartbeatHealthTracksSuccessAndFailure(t *testing.T) {
	dirKey := "/tmp/lease-health-success-and-failure"
	recordHeartbeatSuccess(dirKey, 1500*time.Microsecond)
	recordHeartbeatFailure(dirKey, 3*time.Millisecond)

	var found *LeaseHeartbeatHealth
	for _, stat := range LeaseHeartbeatHealthStats() {
		if stat.DatabaseIdentity == dirKey {
			copy := stat
			found = &copy
		}
	}
	if found == nil {
		t.Fatalf("no health row for %s", dirKey)
	}
	if found.Attempts != 2 || found.Failures != 1 {
		t.Fatalf("attempts/failures = %d/%d, want 2/1", found.Attempts, found.Failures)
	}
	if found.ConsecutiveFailures != 1 {
		t.Fatalf("consecutiveFailures = %d, want 1 after a failure", found.ConsecutiveFailures)
	}
	if found.MaxRenewDuration != 3*time.Millisecond {
		t.Fatalf("maxRenewDuration = %s, want 3ms", found.MaxRenewDuration)
	}
	if found.LastSuccessAt.IsZero() || found.LastAttemptAt.IsZero() {
		t.Fatalf("timestamps not recorded: %+v", found)
	}

	// A following success clears the consecutive-failure streak.
	recordHeartbeatSuccess(dirKey, time.Millisecond)
	for _, stat := range LeaseHeartbeatHealthStats() {
		if stat.DatabaseIdentity != dirKey {
			continue
		}
		if stat.ConsecutiveFailures != 0 {
			t.Fatalf("consecutiveFailures = %d, want 0 after a success", stat.ConsecutiveFailures)
		}
		if stat.Attempts != 3 || stat.Failures != 1 {
			t.Fatalf("attempts/failures = %d/%d, want 3/1", stat.Attempts, stat.Failures)
		}
	}
}

func TestRecordLeaseFenceLossIncrementsCounter(t *testing.T) {
	dirKey := "/tmp/lease-health-fence-loss"
	before := LeaseLostCount()
	recordLeaseFenceLoss(dirKey)
	if got := LeaseLostCount(); got != before+1 {
		t.Fatalf("lease lost count = %d, want %d", got, before+1)
	}
	for _, stat := range LeaseHeartbeatHealthStats() {
		if stat.DatabaseIdentity == dirKey {
			if stat.FenceLosses == 0 {
				t.Fatal("fence loss not attributed to the database row")
			}
			return
		}
	}
	t.Fatalf("no health row for %s", dirKey)
}

func TestLeaseHealthPublishedViaExpvar(t *testing.T) {
	published := expvar.Get(leaseHealthExpvarKey)
	if published == nil {
		t.Fatalf("expvar %q is not published", leaseHealthExpvarKey)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(published.String()), &snapshot); err != nil {
		t.Fatalf("decode %q: %v", published.String(), err)
	}
	if _, ok := snapshot["leaseLostCount"]; !ok {
		t.Fatalf("snapshot %q missing leaseLostCount", published.String())
	}
	if _, ok := snapshot["heartbeat"]; !ok {
		t.Fatalf("snapshot %q missing heartbeat", published.String())
	}
}
