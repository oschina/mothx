// Package bootstrap wires the internal agent and provider implementations into
// the public agent package. The public agent package must stay free of
// internal imports, so external modules blank-import this package once to
// enable agent.NewBuilder().Build() and Builder.WithProviderByName(...):
//
//	import _ "github.com/oschina/mothx/bootstrap"
//
// Importing this package has no other side effects beyond registering the
// builder hook (via internal/agent) and the provider resolution hook plus
// concrete provider factories (via provider_bridge.go and the blank-imported
// provider subpackages) at init time.
package bootstrap

import (
	// Registers the internal agent builder (agent.SetBuilderFunc) and, transitively,
	// the provider registry used by Builder.WithProviderByName.
	_ "github.com/oschina/mothx/internal/agent"
	_ "github.com/oschina/mothx/internal/provider"

	// Register the concrete provider factories in the global provider registry
	// so Builder.WithProviderByName can resolve openai/anthropic/google
	// providers (each subpackage self-registers via its init()).
	_ "github.com/oschina/mothx/internal/provider/anthropic"
	_ "github.com/oschina/mothx/internal/provider/google"
	_ "github.com/oschina/mothx/internal/provider/openai"
)
