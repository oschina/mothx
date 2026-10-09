package channels

import (
	"context"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// TestAwaitChannelAdmissionSendsQueueNoticesWhileWaiting pins that a channel
// message queued behind an in-flight run keeps the sender informed instead of
// waiting silently, while the admission decision stays Runtime-owned.
func TestAwaitChannelAdmissionSendsQueueNoticesWhileWaiting(t *testing.T) {
	dir := t.TempDir()
	mgr := session.New(dir, dir)
	if err := mgr.InitWithID("queue-notice-session"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	// Occupy the session's execution admission so the wait actually blocks.
	holder, err := session.AcquireExecutionAdmission(dir, "queue-notice-session")
	if err != nil {
		t.Fatalf("hold admission: %v", err)
	}

	previous := channelQueueNoticeEvery
	channelQueueNoticeEvery = 5 * time.Millisecond
	defer func() { channelQueueNoticeEvery = previous }()

	notices := make(chan string, 16)
	d := &Dispatcher{sessionDir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		guard, err := d.awaitChannelAdmission(ctx, "queue-notice-session", func(text string) {
			select {
			case notices <- text:
			default:
			}
		})
		if guard != nil {
			guard.Release()
		}
		done <- err
	}()

	select {
	case <-notices:
	case <-time.After(2 * time.Second):
		t.Fatal("no queue notice while waiting for admission")
	}

	holder.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("awaitChannelAdmission returned %v after the lease was released", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitChannelAdmission did not return after the lease was released")
	}
}
