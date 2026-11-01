package tui

import (
	"fmt"
	"time"
)

const (
	// tokenRateWindow is the trailing interval used for the live generation
	// speed. A short window keeps the footer reading responsive to bursts and
	// pauses without jumping on every single delta.
	tokenRateWindow = 2 * time.Second
	// tokenRateMinSpan hides the reading until enough of the run has elapsed
	// for a per-second value to be meaningful.
	tokenRateMinSpan = 500 * time.Millisecond
)

type tokenSample struct {
	at     time.Time
	tokens int
}

// tokenRateTracker keeps a short rolling history of generated output tokens.
// Provider usage is only authoritative at turn boundaries, so the live footer
// rate is estimated from streamed deltas instead.
type tokenRateTracker struct {
	started time.Time
	samples []tokenSample
	total   int
}

func (r *tokenRateTracker) reset(now time.Time) {
	r.started = now
	r.samples = r.samples[:0]
	r.total = 0
}

// add records tokens generated at now. Non-positive amounts are ignored so
// empty deltas do not disturb the window.
func (r *tokenRateTracker) add(tokens int, now time.Time) {
	if tokens <= 0 {
		return
	}
	if r.started.IsZero() {
		r.started = now
	}
	r.total += tokens
	r.samples = append(r.samples, tokenSample{at: now, tokens: tokens})
	r.trim(now)
}

func (r *tokenRateTracker) trim(now time.Time) {
	cutoff := now.Add(-tokenRateWindow)
	idx := 0
	for idx < len(r.samples) && !r.samples[idx].at.After(cutoff) {
		idx++
	}
	if idx > 0 {
		r.samples = append(r.samples[:0], r.samples[idx:]...)
	}
}

// tokensPerSecond returns the estimated generation rate over the trailing
// window. It returns 0 when no token arrived recently, so callers can hide the
// reading while the agent is running tools or waiting on approval.
func (r *tokenRateTracker) tokensPerSecond(now time.Time) float64 {
	r.trim(now)
	if len(r.samples) == 0 {
		return 0
	}
	count := 0
	for _, sample := range r.samples {
		count += sample.tokens
	}
	if count == 0 {
		return 0
	}
	span := now.Sub(r.started)
	if span > tokenRateWindow {
		span = tokenRateWindow
	}
	if span < tokenRateMinSpan {
		return 0
	}
	return float64(count) / span.Seconds()
}

// formatTokenRate renders a tokens-per-second reading with enough precision to
// stay useful for slow models without producing noisy long decimals.
func formatTokenRate(rate float64) string {
	if rate >= 10 {
		return fmt.Sprintf("%.0f", rate)
	}
	return fmt.Sprintf("%.1f", rate)
}
