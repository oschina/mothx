package provider

import (
	"fmt"
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// ProviderFactory creates a Provider from a ProviderConfig.
type ProviderFactory func(cfg *config.ProviderConfig) (Provider, error)

// ProviderRegistry manages provider factory registration and creation.
type ProviderRegistry struct {
	mu        sync.RWMutex
	factories map[string]ProviderFactory
}

// NewProviderRegistry creates a new provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		factories: make(map[string]ProviderFactory),
	}
}

// Register registers a provider factory by name.
func (r *ProviderRegistry) Register(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Create creates a provider by name using the given config.
func (r *ProviderRegistry) Create(name string, cfg *config.ProviderConfig) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", name)
	}
	return factory(cfg)
}

// List returns all registered provider names.
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// Has checks if a provider is registered.
func (r *ProviderRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[name]
	return ok
}

// Global registry instance
var globalRegistry = NewProviderRegistry()

// Register registers a provider factory in the global registry.
func Register(name string, factory ProviderFactory) {
	globalRegistry.Register(name, factory)
}

// CreateProvider creates a provider using the global registry.
func CreateProvider(name string, cfg *config.ProviderConfig) (Provider, error) {
	return globalRegistry.Create(name, cfg)
}

// ListProviders returns all registered provider names.
func ListProviders() []string {
	return globalRegistry.List()
}

// ResolveProvider resolves a provider from config with three-level fallback:
// 1. explicit vendor
// 2. baseUrl auto-detect
// 3. generic fallback by API protocol
func ResolveProvider(cfg *config.ProviderConfig) (Provider, error) {
	resolved := ResolveAdapterConfig(cfg)
	if resolved.Vendor != "" {
		if globalRegistry.Has(resolved.Vendor) {
			return globalRegistry.Create(resolved.Vendor, cfg)
		}
	}

	switch resolved.API {
	case "openai-chat":
		return globalRegistry.Create("openai-chat", cfg)
	case "openai-responses":
		return globalRegistry.Create("openai-responses", cfg)
	case "anthropic-messages":
		return globalRegistry.Create("anthropic-messages", cfg)
	case "google-gemini":
		return globalRegistry.Create("google-gemini", cfg)
	case "google-vertex":
		return globalRegistry.Create("google-vertex", cfg)
	default:
		return nil, fmt.Errorf("unsupported API type: %s (use 'openai-chat', 'openai-responses', 'anthropic-messages', 'google-gemini', or 'google-vertex')", resolved.API)
	}
}

// VendorFromBaseURL attempts to identify the vendor from a base URL.
// Returns empty string if no match.
func VendorFromBaseURL(baseURL string) string {
	vendorRegistry.RLock()
	defer vendorRegistry.RUnlock()
	for _, name := range vendorRegistry.order {
		adapter := vendorRegistry.adapters[name]
		if adapter.MatchBaseURL(baseURL) {
			return name
		}
	}
	return ""
}
