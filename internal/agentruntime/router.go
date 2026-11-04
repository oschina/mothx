package agentruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// RouterProfileBinding is one configured external ACP worker a Router session
// may target, together with the non-blocking strategy recorded at activation.
type RouterProfileBinding struct {
	ProfileID        string
	ApprovalStrategy config.ACPPermissionPolicy
}

// RouterBinding is the Runtime-owned selection of one or more configured
// external ACP workers for a Router-mode session. ACPProfileID/ApprovalStrategy
// remain the primary/default selection for compatibility; Profiles is the
// ordered allowlist the model may choose from. It contains identifiers and
// policy only; executable argv and environment remain global configuration.
type RouterBinding struct {
	ACPProfileID     string
	ApprovalStrategy config.ACPPermissionPolicy
	Profiles         []RouterProfileBinding
	PolicyRevision   string
}

func routerBindingFromEntry(entry session.RouterBindingEntry) RouterBinding {
	binding := RouterBinding{
		ACPProfileID:     entry.ACPProfileID,
		ApprovalStrategy: config.ACPPermissionPolicy(entry.ApprovalStrategy),
		PolicyRevision:   entry.PolicyRevision,
	}
	if len(entry.Profiles) > 0 {
		binding.Profiles = make([]RouterProfileBinding, 0, len(entry.Profiles))
		for _, profile := range entry.Profiles {
			binding.Profiles = append(binding.Profiles, RouterProfileBinding{
				ProfileID:        profile.ProfileID,
				ApprovalStrategy: config.ACPPermissionPolicy(profile.ApprovalStrategy),
			})
		}
	}
	return binding.normalized()
}

// normalized returns a copy with a deduplicated ordered profile list and the
// primary fields synchronized with the first/default entry.
func (binding RouterBinding) normalized() RouterBinding {
	out := RouterBinding{PolicyRevision: binding.PolicyRevision}
	seen := make(map[string]bool, len(binding.Profiles)+1)
	for _, profile := range binding.Profiles {
		id := strings.TrimSpace(profile.ProfileID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		strategy := profile.ApprovalStrategy
		if strategy == "" && id == strings.TrimSpace(binding.ACPProfileID) {
			strategy = binding.ApprovalStrategy
		}
		out.Profiles = append(out.Profiles, RouterProfileBinding{ProfileID: id, ApprovalStrategy: strategy})
	}
	primary := strings.TrimSpace(binding.ACPProfileID)
	if primary == "" && len(out.Profiles) > 0 {
		primary = out.Profiles[0].ProfileID
	}
	if primary != "" && !seen[primary] {
		out.Profiles = append([]RouterProfileBinding{{
			ProfileID: primary, ApprovalStrategy: binding.ApprovalStrategy,
		}}, out.Profiles...)
	}
	out.ACPProfileID = primary
	for _, profile := range out.Profiles {
		if profile.ProfileID == primary {
			out.ApprovalStrategy = profile.ApprovalStrategy
			break
		}
	}
	return out
}

// ProfileIDs returns the ordered bound worker IDs.
func (binding RouterBinding) ProfileIDs() []string {
	normalized := binding.normalized()
	ids := make([]string, 0, len(normalized.Profiles))
	for _, profile := range normalized.Profiles {
		ids = append(ids, profile.ProfileID)
	}
	return ids
}

func (binding RouterBinding) strategyFor(profileID string) (config.ACPPermissionPolicy, bool) {
	for _, profile := range binding.normalized().Profiles {
		if profile.ProfileID == profileID {
			return profile.ApprovalStrategy, true
		}
	}
	return "", false
}

func (binding RouterBinding) validate() error {
	normalized := binding.normalized()
	if normalized.ACPProfileID == "" {
		return fmt.Errorf("Router ACP profile ID is required")
	}
	for _, profile := range normalized.Profiles {
		if profile.ProfileID == "" {
			return fmt.Errorf("Router ACP profile ID is required")
		}
		switch profile.ApprovalStrategy {
		case config.ACPPermissionAutoAllowOnce, config.ACPPermissionAutoReject:
			continue
		case config.ACPPermissionInteractive:
			return fmt.Errorf("interactive ACP approval is unavailable in Router mode")
		default:
			return fmt.Errorf("invalid Router ACP approval strategy %q", profile.ApprovalStrategy)
		}
	}
	return nil
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
	normalized := binding.normalized()
	selection := session.RouterBindingSelection{
		PrimaryProfileID: normalized.ACPProfileID,
		PolicyRevision:   normalized.PolicyRevision,
	}
	for _, profile := range normalized.Profiles {
		selection.Profiles = append(selection.Profiles, session.RouterProfileBinding{
			ProfileID: profile.ProfileID, ApprovalStrategy: string(profile.ApprovalStrategy),
		})
	}
	_, err := manager.AppendRouterBindingSelection(selection)
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
	binding = binding.normalized()
	for i, profileBinding := range binding.Profiles {
		profile, ok := settings.ACPAgentProfile(profileBinding.ProfileID)
		if !ok {
			return fmt.Errorf("Router ACP profile %q is unavailable", profileBinding.ProfileID)
		}
		if profile.EffectivePermissionPolicy() == config.ACPPermissionInteractive {
			return fmt.Errorf("Router ACP profile %q requires interactive permission approval", profile.ID)
		}
		if profileBinding.ApprovalStrategy == "" {
			// The profile is the non-blocking strategy owner; an activator only
			// names workers and lets Runtime record the matching replay values.
			profileBinding.ApprovalStrategy = profile.EffectivePermissionPolicy()
			binding.Profiles[i] = profileBinding
		}
		if profileBinding.ApprovalStrategy != profile.EffectivePermissionPolicy() {
			return fmt.Errorf("Router approval strategy %q does not match profile %q policy %q",
				profileBinding.ApprovalStrategy, profile.ID, profile.EffectivePermissionPolicy())
		}
	}
	binding = binding.normalized()
	if err := binding.validate(); err != nil {
		return err
	}
	if r.TeamExpertActive() {
		return fmt.Errorf("Router mode cannot be combined with an expert team; unbind the expert first")
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
	binding, err := r.ResolveRouterBinding(settings)
	if err != nil {
		return config.ACPAgentProfile{}, RouterBinding{}, err
	}
	return r.ResolveRouterACPProfileByID(settings, binding.ACPProfileID)
}

// ResolveRouterBinding validates every selected worker against current global
// configuration and returns the normalized multi-profile binding.
func (r *SessionRuntime) ResolveRouterBinding(settings *config.Settings) (RouterBinding, error) {
	if r == nil {
		return RouterBinding{}, fmt.Errorf("Router runtime is nil")
	}
	_, _, _, mode, _ := r.ConfigSnapshot()
	if mode != ModeRouter {
		return RouterBinding{}, fmt.Errorf("ACP worker resolution requires Router mode, got %q", mode)
	}
	binding, ok := r.RouterBinding()
	if !ok {
		return RouterBinding{}, fmt.Errorf("Router session has no ACP worker binding")
	}
	if settings == nil {
		return RouterBinding{}, fmt.Errorf("Router ACP settings are required")
	}
	if err := settings.ValidateACPAgentProfiles(); err != nil {
		return RouterBinding{}, err
	}
	for _, profileBinding := range binding.Profiles {
		profile, ok := settings.ACPAgentProfile(profileBinding.ProfileID)
		if !ok {
			return RouterBinding{}, fmt.Errorf("Router ACP profile %q is unavailable", profileBinding.ProfileID)
		}
		if profile.EffectivePermissionPolicy() == config.ACPPermissionInteractive {
			// Router is non-interactive by design: a worker that blocks on a UI
			// permission dialog would stall the lead with no adapter able to
			// resolve it. Interactive profiles stay available to ordinary
			// external-child flows, not Router.
			return RouterBinding{}, fmt.Errorf("Router ACP profile %q requires interactive permission approval", profile.ID)
		}
		if profileBinding.ApprovalStrategy != profile.EffectivePermissionPolicy() {
			// The profile is the strategy owner. A policy change after activation
			// must not silently keep an old approval behavior.
			return RouterBinding{}, fmt.Errorf("Router binding approval strategy %q is stale for profile %q (now %q); reactivate Router",
				profileBinding.ApprovalStrategy, profile.ID, profile.EffectivePermissionPolicy())
		}
	}
	return binding, nil
}

// ResolveRouterACPProfileByID resolves one worker from the session's bound
// allowlist. A Router session may bind several eligible profiles; the model may
// choose only one of those IDs.
func (r *SessionRuntime) ResolveRouterACPProfileByID(settings *config.Settings, profileID string) (config.ACPAgentProfile, RouterBinding, error) {
	binding, err := r.ResolveRouterBinding(settings)
	if err != nil {
		return config.ACPAgentProfile{}, RouterBinding{}, err
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = binding.ACPProfileID
	}
	profile, ok := settings.ACPAgentProfile(profileID)
	if !ok {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf("Router ACP profile %q is unavailable", profileID)
	}
	if _, bound := binding.strategyFor(profileID); !bound {
		return config.ACPAgentProfile{}, RouterBinding{}, fmt.Errorf(
			"ACP worker %q is not bound to this Router session; bound workers: %s",
			profileID, strings.Join(binding.ProfileIDs(), ", "))
	}
	return profile, binding, nil
}
