package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestTokenRateTrackerReportsWindowedRate(t *testing.T) {
	start := time.Unix(0, 0)
	var tracker tokenRateTracker
	tracker.reset(start)

	tracker.add(10, start.Add(1*time.Second))
	if got, want := tracker.tokensPerSecond(start.Add(1*time.Second)), 10.0; got != want {
		t.Fatalf("tokensPerSecond() = %v, want %v", got, want)
	}

	// A burst inside the window is averaged over the trailing window, not over
	// the whole run.
	tracker.add(20, start.Add(2*time.Second))
	if got, want := tracker.tokensPerSecond(start.Add(2*time.Second)), 15.0; got != want {
		t.Fatalf("tokensPerSecond() after burst = %v, want %v", got, want)
	}

	// Once the window has fully turned over only recent tokens count.
	tracker.add(30, start.Add(4*time.Second))
	if got, want := tracker.tokensPerSecond(start.Add(4*time.Second)), 15.0; got != want {
		t.Fatalf("tokensPerSecond() after window slide = %v, want %v", got, want)
	}
}

func TestTokenRateTrackerHidesStaleReading(t *testing.T) {
	start := time.Unix(0, 0)
	var tracker tokenRateTracker
	tracker.reset(start)
	tracker.add(50, start.Add(time.Second))

	if got := tracker.tokensPerSecond(start.Add(3 * time.Second)); got != 0 {
		t.Fatalf("stale tokensPerSecond() = %v, want 0", got)
	}
}

func TestTokenRateTrackerSuppressesEarlyReading(t *testing.T) {
	start := time.Unix(0, 0)
	var tracker tokenRateTracker
	tracker.reset(start)
	tracker.add(5, start.Add(100*time.Millisecond))

	if got := tracker.tokensPerSecond(start.Add(100 * time.Millisecond)); got != 0 {
		t.Fatalf("early tokensPerSecond() = %v, want 0", got)
	}
}

func TestFormatTokenRate(t *testing.T) {
	tests := []struct {
		rate float64
		want string
	}{
		{rate: 4.24, want: "4.2"},
		{rate: 9.99, want: "10.0"},
		{rate: 42.4, want: "42"},
		{rate: 135, want: "135"},
	}
	for _, tt := range tests {
		if got := formatTokenRate(tt.rate); got != tt.want {
			t.Errorf("formatTokenRate(%v) = %q, want %q", tt.rate, got, tt.want)
		}
	}
}

func TestFooterShowsLiveTokenRate(t *testing.T) {
	now := time.Now()
	a := &App{
		isThinking:   true,
		spinnerIndex: 0,
		width:        120,
		translator:   i18n.New(i18n.LanguageEN),
	}
	a.streamRate.reset(now.Add(-time.Second))
	a.streamRate.add(40, now.Add(-100*time.Millisecond))

	footer := stripANSI(a.renderBuiltinFooter())
	if !strings.Contains(footer, "40 tok/s") {
		t.Fatalf("footer = %q, want live token rate", footer)
	}
}

func TestFooterOmitsTokenRateWithoutRecentTokens(t *testing.T) {
	now := time.Now()
	a := &App{
		isThinking:   true,
		spinnerIndex: 0,
		width:        120,
		translator:   i18n.New(i18n.LanguageEN),
	}
	a.streamRate.reset(now.Add(-5 * time.Second))
	a.streamRate.add(40, now.Add(-4*time.Second))

	footer := stripANSI(a.renderBuiltinFooter())
	if strings.Contains(footer, "tok/s") {
		t.Fatalf("footer = %q, want stale token rate hidden", footer)
	}
}
