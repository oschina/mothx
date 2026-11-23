// Package host defines the narrow shell services shared by private Native
// business panels. Each feature may embed Base and add only the accessors it
// genuinely needs, keeping the composition shell as the sole owner of state.
package host

import (
	"github.com/oschina/mothx/native/internal/acp"
)

// Base is the common capability/settings/ACP surface.
type Base interface {
	Dispatch(fn func())
	FeatureEnabled(name string) bool
	ACP() *acp.Client
	SettingsOpen() bool
	SettingsTab() string
	ReloadSequence() int64
}
