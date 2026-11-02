package agentruntime

import (
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// RouterBinding is the Runtime-owned selection of one configured external ACP
// worker for a Router-mode session. It contains identifiers and policy only;
// executable argv and environment remain global configuration.
type RouterBinding struct {
	ACPProfileID     string
	ApprovalStrategy config.ACPPermissionPolicy
	PolicyRevision   string
}

func routerBindingFromEntry(entry session.RouterBindingEntry) RouterBinding {
	return RouterBinding{
		ACPProfileID:     entry.ACPProfileID,
		ApprovalStrategy: config.ACPPermissionPolicy(entry.ApprovalStrategy),
		PolicyRevision:   entry.PolicyRevision,
	}
}

func (binding RouterBinding) validate() error {
	if strings.TrimSpace(binding.ACPProfileID) == "" {
		return fmt.Errorf("Router ACP profile ID is required")
	}
	switch binding.ApprovalStrategy {
	case config.ACPPermissionAutoAllowOnce, config.ACPPermissionAutoReject:
		return nil
	case config.ACPPermissionInteractive:
		return fmt.Errorf("interactive ACP approval is unavailable in Router mode")
	default:
		return fmt.Errorf("invalid Router ACP approval strategy %q", binding.ApprovalStrategy)
	}
}

// RouterBinding returns the persisted Router selection, if this session has
// one. A copied value prevents adapters from mutating Runtime policy.
func (r *SessionRuntime) RouterBinding() (RouterBinding, bool) {
	if r == nil {
		return RouterBinding{}, false
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil {
		return RouterBinding{}, false
	}
	entry, ok := manager.GetLatestRouterBinding()
	if !ok {
		return RouterBinding{}, false
	}
	binding := routerBindingFromEntry(entry)
	return binding, binding.validate() == nil
}

// SetRouterBinding persists one Router worker selection before exposing it in
// Runtime. Callers must switch the session to ModeRouter separately through
// SetConfigOption so mode and binding retain their existing independent replay
// entries.
func (r *SessionRuntime) SetRouterBinding(binding RouterBinding) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if err := binding.validate(); err != nil {
		return err
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager == nil {
		return fmt.Errorf("Router binding requires a session manager")
	}
	_, err := manager.AppendRouterBinding(binding.ACPProfileID, string(binding.ApprovalStrategy), binding.PolicyRevision)
	return err
}

// ResolveRouterACPProfile joins the persisted Router binding with global
// executable configuration. It is the only Runtime entry point that turns the
// session's opaque profile ID into an executable ACP profile.
func (r *SessionRuntime) ResolveRouterACPProfile(settings *config.Settings) (config.ACPAgentProfile, RouterBinding, error) {
	if r == nil {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router runtime is nil")
	}
	_, _, _, mode, _ := r.ConfigSnapshot()
	if mode != ModeRouter {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("ACP worker resolution requires Router mode, got %q", mode)
	}
	binding, ok := r.RouterBinding()
	if !ok {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router session has no ACP worker binding")
	}
	if settings == nil {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router ACP settings are required")
	}
	if err := settings.ValidateACPAgentProfiles(); err != nil {
		return config.ACPAgentProfile{}, RouterBinding{}, err
	}
	profile, ok := settings.ACPAgentProfile(binding.ACPProfileID)
	if !ok {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router ACP profile %q is unavailable", binding.ACPProfileID)
	}
	return profile, binding, nil
}
