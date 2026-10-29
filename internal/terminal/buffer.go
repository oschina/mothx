package terminal

import (
	"sync"
	"time"
)

type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRing(max int) *ringBuffer {
	if max <= 0 {
		max = 1 << 20
	}
	return &ringBuffer{max: max}
}

func (r *ringBuffer) resize(max int) {
	if max <= 0 {
		max = 1 << 20
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.max = max
	if len(r.data) > max {
		r.data = append([]byte(nil), r.data[len(r.data)-max:]...)
	}
}

func (r *ringBuffer) write(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		r.data = append([]byte(nil), r.data[len(r.data)-r.max:]...)
	}
}

func (r *ringBuffer) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.data...)
}

type outputLimiter struct {
	mu     sync.Mutex
	rate   int
	tokens float64
	last   int64 // unix nanos
}

func newOutputLimiter(rate int) *outputLimiter {
	return &outputLimiter{rate: rate, tokens: float64(rate)}
}

func (l *outputLimiter) setRate(rate int) {
	l.mu.Lock()
	l.rate = rate
	if l.tokens > float64(rate) {
		l.tokens = float64(rate)
	}
	l.mu.Unlock()
}

func (l *outputLimiter) wait(n int) bool {
	if n <= 0 {
		return false
	}
	l.mu.Lock()
	rate := l.rate
	if rate <= 0 {
		l.mu.Unlock()
		return false
	}
	waited := false
	now := nowNanos()
	if l.last == 0 {
		l.last = now
	}
	elapsed := float64(now-l.last) / 1e9
	l.last = now
	l.tokens += elapsed * float64(rate)
	if l.tokens > float64(rate) {
		l.tokens = float64(rate)
	}
	need := float64(n)
	for l.tokens < need {
		waited = true
		deficit := need - l.tokens
		l.mu.Unlock()
		sleepSeconds(deficit / float64(rate))
		l.mu.Lock()
		now = nowNanos()
		elapsed = float64(now-l.last) / 1e9
		l.last = now
		l.tokens += elapsed * float64(rate)
		if l.tokens > float64(rate) {
			l.tokens = float64(rate)
		}
	}
	l.tokens -= need
	l.mu.Unlock()
	return waited
}

// nowNanos and sleepSeconds are variables so unit tests can drive the token
// bucket deterministically without sleeping for real.
var nowNanos = func() int64 { return time.Now().UnixNano() }

var sleepSeconds = func(seconds float64) {
	if seconds <= 0 {
		return
	}
	if seconds > 1 {
		seconds = 1
	}
	time.Sleep(time.Duration(seconds * float64(time.Second)))
}
