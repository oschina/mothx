package agentruntime

import (
	"context"
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
// applyModeOption/SetMode so mode and binding retain their existing independent
// replay entries.
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

// ActivateRouter is the single activation entry point for TUI and CLI. It
// validates the selected profile against global configuration and source
// policy, requires an idle session, then persists the Router binding and mode
// through the normal replay entries.
//
// Ordering is deliberate: the binding is persisted first. A failure or crash
// before the mode append leaves a session that is not in Router mode, which
// resolves as "not Router" and fails closed. The inverse order would expose
// Router's read-only tool surface without an eligible worker.
func (r *SessionRuntime) ActivateRouter(ctx context.Context, sessionDir string, settings *config.Settings, binding RouterBinding) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if settings == nil {
		return fmt.Errorf("Router activation requires ACP settings")
	}
	if err := settings.ValidateACPAgentProfiles(); err != nil {
		return err
	}
	profile, ok := settings.ACPAgentProfile(binding.ACPProfileID)
	if !ok {
		return fmt.Errorf("Router ACP profile %q is unavailable", binding.ACPProfileID)
	}
	if profile.EffectivePermissionPolicy() == config.ACPPermissionInteractive {
		return fmt.Errorf("Router ACP profile %q requires interactive permission approval", profile.ID)
	}
	if binding.ApprovalStrategy == "" {
		// The profile is the non-blocking strategy owner; an activator only names
		// the worker and lets Runtime record the matching replay value.
		binding.ApprovalStrategy = profile.EffectivePermissionPolicy()
	}
	if err := binding.validate(); err != nil {
		return err
	}
	if binding.ApprovalStrategy != profile.EffectivePermissionPolicy() {
		return fmt.Errorf("Router approval strategy %q does not match profile %q policy %q",
			binding.ApprovalStrategy, profile.ID, profile.EffectivePermissionPolicy())
	}

	// A source-forced yolo session (WeChat/Feishu) can never select Router.
	_, effectiveMode, err := r.ResolvePolicy("", ModeRouter, ModeYolo)
	if err != nil {
		return err
	}
	if effectiveMode != ModeRouter {
		return fmt.Errorf("this session's source cannot select Router mode")
	}

	r.mu.RLock()
	manager := r.Manager
	id := r.ID
	r.mu.RUnlock()
	if manager == nil {
		return fmt.Errorf("Router activation requires a session manager")
	}
	if header := manager.GetHeader(); header != nil && header.ID != "" {
		id = header.ID
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("Router activation requires a persisted session")
	}
	if strings.TrimSpace(sessionDir) == "" {
		sessionDir = manager.GetSessionDir()
	}
	if strings.TrimSpace(sessionDir) == "" {
		return fmt.Errorf("Router activation requires a session directory")
	}
	// Refresh the optimistic-lock leaf first so a stale adapter handle cannot
	// report a false conflict on the binding append.
	if err := manager.Reload(); err != nil {
		return fmt.Errorf("reload session before Router activation: %w", err)
	}
	// The idle check is a canonical projection, not a held lease: another
	// process can admit a Run immediately after it. That race is bounded and
	// fails safe — the concurrently started Run keeps the policy it resolved at
	// admission, and activation only governs subsequent Runs. Holding an
	// execution admission here would instead make the session look busy to every
	// other adapter for a user-interface action.
	snapshot, err := InspectSessionExecution(sessionDir, id)
	if err != nil {
		return fmt.Errorf("inspect session execution: %w", err)
	}
	if snapshot.State != SessionExecutionIdle {
		return fmt.Errorf("Router activation requires an idle session, current state is %q", snapshot.State)
	}

	if err := r.SetRouterBinding(binding); err != nil {
		return err
	}
	if err := r.applyModeOption("", ModeRouter); err != nil {
		return err
	}
	return nil
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
	if profile.EffectivePermissionPolicy() == config.ACPPermissionInteractive {
		// Router is non-interactive by design: a worker that blocks on a UI
		// permission dialog would stall the lead with no adapter able to resolve
		// it. Interactive profiles stay available to ordinary external-child
		// flows, not Router.
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router ACP profile %q requires interactive permission approval", profile.ID)
	}
	if binding.ApprovalStrategy != profile.EffectivePermissionPolicy() {
		// The profile is the strategy owner. A policy change after activation
		// must not silently keep an old approval behavior.
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router binding approval strategy %q is stale for profile %q (now %q); reactivate Router",
			binding.ApprovalStrategy, profile.ID, profile.EffectivePermissionPolicy())
	}
	return profile, binding, nil
}
