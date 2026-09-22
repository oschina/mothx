package agent

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

func TestBoundedParallelPreservesOrderAndConcurrencyLimit(t *testing.T) {
	items := make([]int, 32)
	for i := range items {
		items[i] = i
	}

	var active atomic.Int32
	var peak atomic.Int32
	results := BoundedParallel(3, items, func(item int) int {
		current := active.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Duration((len(items)-item)%4+1) * time.Millisecond)
		active.Add(-1)
		return item * 2
	})

	if got := peak.Load(); got > 3 {
		t.Fatalf("peak concurrency = %d, want <= 3", got)
	}
	if len(results) != len(items) {
		t.Fatalf("result count = %d, want %d", len(results), len(items))
	}
	for i, result := range results {
		if result != i*2 {
			t.Fatalf("result[%d] = %d, want %d", i, result, i*2)
		}
	}
}

func TestBoundedParallelDefaultAndSerialLimits(t *testing.T) {
	items := make([]int, config.DefaultToolExecutionMaxConcurrency+4)
	for i := range items {
		items[i] = i
	}

	var active atomic.Int32
	var peak atomic.Int32
	BoundedParallel(0, items, func(item int) struct{} {
		current := active.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return struct{}{}
	})
	if got := peak.Load(); got > config.DefaultToolExecutionMaxConcurrency {
		t.Fatalf("default peak concurrency = %d, want <= %d", got, config.DefaultToolExecutionMaxConcurrency)
	}

	active.Store(0)
	peak.Store(0)
	BoundedParallel(1, items, func(item int) struct{} {
		current := active.Add(1)
		if current > peak.Load() {
			peak.Store(current)
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return struct{}{}
	})
	if got := peak.Load(); got != 1 {
		t.Fatalf("serial peak concurrency = %d, want 1", got)
	}
}

func TestAgentNormalizesToolExecutionSettings(t *testing.T) {
	settings := &config.Settings{ToolExecution: config.ToolExecutionSettings{Mode: "sequential", MaxConcurrency: 4}}
	a := New(Config{Settings: settings}, nil)
	if got := a.config.ToolExecutionMode; got != "sequential" {
		t.Fatalf("configured execution mode = %q, want sequential", got)
	}
	if got := a.MaxToolConcurrency(); got != 4 {
		t.Fatalf("configured max concurrency = %d, want 4", got)
	}

	configuredAgent := NewWithLoopConfig(AgentLoopConfig{Config: Config{Settings: settings}}, nil)
	if got := configuredAgent.config.ToolExecutionMode; got != "sequential" {
		t.Fatalf("loop-config execution mode = %q, want sequential", got)
	}
	if got := configuredAgent.MaxToolConcurrency(); got != 4 {
		t.Fatalf("loop-config max concurrency = %d, want 4", got)
	}

	defaultAgent := NewWithLoopConfig(AgentLoopConfig{}, nil)
	if got := defaultAgent.MaxToolConcurrency(); got != config.DefaultToolExecutionMaxConcurrency {
		t.Fatalf("zero max concurrency = %d, want %d", got, config.DefaultToolExecutionMaxConcurrency)
	}
}
