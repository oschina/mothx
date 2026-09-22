package provider

import (
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// AdapterConfig is the provider configuration after vendor defaults are applied.
type AdapterConfig struct {
	Vendor         string
	API            string
	BaseURL        string
	ThinkingFormat string
	CacheControl   *bool
}

// VendorAdapter applies vendor-specific defaults while keeping protocol providers generic.
type VendorAdapter interface {
	Name() string
	MatchBaseURL(baseURL string) bool
	Apply(*AdapterConfig)
}

type simpleVendorAdapter struct {
	name           string
	domains        []string
	thinkingFormat string
	cacheControl   *bool
	defaultAPI     string
}

func (a simpleVendorAdapter) Name() string { return a.name }

func (a simpleVendorAdapter) MatchBaseURL(baseURL string) bool {
	lower := strings.ToLower(baseURL)
	for _, domain := range a.domains {
		if strings.Contains(lower, strings.ToLower(domain)) {
			return true
		}
	}
	return false
}

func (a simpleVendorAdapter) Apply(cfg *AdapterConfig) {
	if cfg.API == "" && a.defaultAPI != "" {
		cfg.API = a.defaultAPI
	}
	if cfg.ThinkingFormat == "" && a.thinkingFormat != "" {
		cfg.ThinkingFormat = a.thinkingFormat
	}
	if cfg.CacheControl == nil && a.cacheControl != nil {
		cfg.CacheControl = a.cacheControl
	}
}

var vendorRegistry = struct {
	sync.RWMutex
	order    []string
	adapters map[string]VendorAdapter
}{adapters: make(map[string]VendorAdapter)}

// RegisterVendorAdapter registers a vendor adapter.
func RegisterVendorAdapter(adapter VendorAdapter) {
	if adapter == nil || adapter.Name() == "" {
		return
	}
	vendorRegistry.Lock()
	defer vendorRegistry.Unlock()
	name := normalizeVendorName(adapter.Name())
	if _, ok := vendorRegistry.adapters[name]; !ok {
		vendorRegistry.order = append(vendorRegistry.order, name)
	}
	vendorRegistry.adapters[name] = adapter
}

// GetVendorAdapter returns a registered vendor adapter by name.
func GetVendorAdapter(name string) (VendorAdapter, bool) {
	vendorRegistry.RLock()
	defer vendorRegistry.RUnlock()
	adapter, ok := vendorRegistry.adapters[normalizeVendorName(name)]
	return adapter, ok
}

// ListVendorAdapters returns registered vendor adapter names in registration order.
func ListVendorAdapters() []string {
	vendorRegistry.RLock()
	defer vendorRegistry.RUnlock()
	names := make([]string, len(vendorRegistry.order))
	copy(names, vendorRegistry.order)
	return names
}

// ResolveAdapterConfig applies provider protocol detection plus vendor defaults.
func ResolveAdapterConfig(cfg *config.ProviderConfig) AdapterConfig {
	if cfg == nil {
		return AdapterConfig{API: "openai-chat"}
	}

	resolved := AdapterConfig{
		Vendor:         normalizeVendorName(cfg.Vendor),
		API:            cfg.API,
		BaseURL:        cfg.BaseURL,
		ThinkingFormat: cfg.ThinkingFormat,
		CacheControl:   cfg.CacheControl,
	}

	if resolved.Vendor != "" {
		if adapter, ok := GetVendorAdapter(resolved.Vendor); ok {
			adapter.Apply(&resolved)
		}
		if resolved.API == "" {
			resolved.API = protocolFromBaseURL(cfg.BaseURL)
		}
		return resolved
	}

	vendorRegistry.RLock()
	for _, name := range vendorRegistry.order {
		adapter := vendorRegistry.adapters[name]
		if adapter.MatchBaseURL(cfg.BaseURL) {
			resolved.Vendor = name
			adapter.Apply(&resolved)
			break
		}
	}
	vendorRegistry.RUnlock()

	if resolved.API == "" {
		resolved.API = protocolFromBaseURL(cfg.BaseURL)
	}

	return resolved
}

func protocolFromBaseURL(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "anthropic") {
		return "anthropic-messages"
	}
	return "openai-chat"
}

func normalizeVendorName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func boolPtr(v bool) *bool { return &v }
