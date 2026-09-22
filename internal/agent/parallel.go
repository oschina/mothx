package agent

import (
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// BoundedParallel applies fn to every item with at most max concurrent
// workers. Results retain the input order even when calls finish out of order.
// A non-positive max uses the product default. The helper always drains every
// item so callers can preserve one result per provider tool call.
func BoundedParallel[T any, R any](max int, items []T, fn func(T) R) []R {
	if len(items) == 0 {
		return nil
	}
	// A single call is already serial; avoid allocating a worker and channel
	// for the overwhelmingly common one-tool turn.
	if len(items) == 1 {
		return []R{fn(items[0])}
	}
	if max <= 0 {
		max = config.DefaultToolExecutionMaxConcurrency
	}
	if max > len(items) {
		max = len(items)
	}

	results := make([]R, len(items))
	type job struct {
		index int
		item  T
	}
	jobs := make(chan job, len(items))
	for i, item := range items {
		jobs <- job{index: i, item: item}
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(max)
	for i := 0; i < max; i++ {
		go func() {
			defer wg.Done()
			for item := range jobs {
				results[item.index] = fn(item.item)
			}
		}()
	}
	wg.Wait()
	return results
}
