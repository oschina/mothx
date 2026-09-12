package agent

import (
	"context"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
)

const (
	// MemberFollowUpWaitMax bounds how long a team lead waits for its still
	// running members before the run may end. Member runs have their own
	// 30-minute per-agent timeout, so this is a safety net, not the normal exit.
	MemberFollowUpWaitMax = 35 * time.Minute
	// MemberFollowUpPoll refreshes the "any member still running" check and the
	// adapter steering probe while waiting. The member wake-up itself is
	// event-driven through the mailbox activity signal.
	MemberFollowUpPoll = 2 * time.Second
)

// ComposeFollowUps builds a team lead's would-stop hook: it is called when a turn
// has no tool calls and therefore would end the run.
//
// Order of precedence:
//  1. adapter steering (for example a queued user message) is returned
//     immediately, so the user keeps control while members are still running;
//  2. pending member notifications are returned so the lead learns about
//     completions and questions;
//  3. only when members are still running does it block — event-driven on the
//     mailbox — until something arrives or the bounded wait expires.
//
// A nil mailbox yields a nil hook so non-team sessions keep the exact prior
// loop shape.
func ComposeFollowUps(mailbox *MemberMailbox, adapterSteering func() []provider.Message) func(context.Context) []provider.Message {
	if mailbox == nil {
		return nil
	}
	return func(ctx context.Context) []provider.Message {
		if messages := drainAdapterSteering(adapterSteering); len(messages) > 0 {
			return messages
		}
		if messages := mailbox.DrainSteering(); len(messages) > 0 {
			return messages
		}
		if !mailbox.RunningChildren() {
			return nil
		}
		deadline := time.Now().Add(MemberFollowUpWaitMax)
		for time.Now().Before(deadline) {
			if _, err := mailbox.WaitForActivity(ctx, MemberFollowUpPoll); err != nil {
				// Cancelled run: let the loop reach its terminal state instead of
				// waiting for members that are being torn down anyway.
				return nil
			}
			if messages := drainAdapterSteering(adapterSteering); len(messages) > 0 {
				return messages
			}
			if messages := mailbox.DrainSteering(); len(messages) > 0 {
				return messages
			}
			if !mailbox.RunningChildren() {
				return nil
			}
		}
		return nil
	}
}

func drainAdapterSteering(source func() []provider.Message) []provider.Message {
	if source == nil {
		return nil
	}
	return source()
}
