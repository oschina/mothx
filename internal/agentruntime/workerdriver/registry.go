package workerdriver

import "strings"

// Builtins returns the explicitly registered driver adapters, in stable order.
// Registration is explicit (no init() side effects) so adding a flavor is a
// reviewed code change, not an import accident.
func Builtins() []Adapter {
	return []Adapter{
		genericACP{},
	}
}

// Lookup returns the adapter for a configured driver identity. An empty or
// unknown identity falls back to generic-acp: a stale settings value must not
// make an otherwise valid worker unusable, and unknown behavior must never be
// invented.
func Lookup(driver WorkerDriver) Adapter {
	normalized := NormalizeDriver(driver)
	for _, adapter := range Builtins() {
		if adapter.Driver() == normalized {
			return adapter
		}
	}
	return genericACP{}
}

// NormalizeDriver canonicalizes a configured driver identity.
func NormalizeDriver(driver WorkerDriver) WorkerDriver {
	normalized := WorkerDriver(strings.ToLower(strings.TrimSpace(string(driver))))
	if normalized == "" {
		return WorkerDriverGenericACP
	}
	return normalized
}
