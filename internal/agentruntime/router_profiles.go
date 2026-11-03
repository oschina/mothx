package agentruntime

import (
	"sort"

	"github.com/oschina/mothx/internal/config"
)

// ACPWorkerProfileDescriptor is the safe, model/management-facing view of one
// configured external ACP worker. It never contains argv, environment names,
// credentials, or configuration paths.
type ACPWorkerProfileDescriptor struct {
	ID                    string                     `json:"id"`
	Title                 string                     `json:"title,omitempty"`
	Enabled               bool                       `json:"enabled"`
	RouterEligible        bool                       `json:"routerEligible"`
	Roles                 []config.ACPAgentRole      `json:"roles,omitempty"`
	PermissionPolicy      config.ACPPermissionPolicy `json:"permissionPolicy"`
	MaxConcurrentChildren int                        `json:"maxConcurrentChildren"`
	// MaxConcurrentChildrenEnforced is the live-worker ceiling actually applied by
	// this release. A configured MaxConcurrentChildren above it is accepted for
	// compatibility but is not yet effective, so a management projection can say
	// so instead of implying parallel workers are running.
	MaxConcurrentChildrenEnforced int    `json:"maxConcurrentChildrenEnforced"`
	InvalidReason                 string `json:"invalidReason,omitempty"`
}

// ListACPWorkerProfiles is the shared Runtime catalog used by management
// projections. It is read-only and does not launch or probe an executable:
// executables are validated by `mothx doctor`.
func ListACPWorkerProfiles(settings *config.Settings) []ACPWorkerProfileDescriptor {
	if settings == nil {
		return nil
	}
	profiles := make([]ACPWorkerProfileDescriptor, 0, len(settings.ACPAgents))
	for _, profile := range settings.ACPAgents {
		descriptor := ACPWorkerProfileDescriptor{
			ID: profile.ID, Title: profile.Title, Enabled: profile.Enabled,
			Roles:                 profile.EffectiveRoles(),
			PermissionPolicy:      profile.EffectivePermissionPolicy(),
			MaxConcurrentChildren: profile.EffectiveMaxConcurrentChildren(),

			MaxConcurrentChildrenEnforced: profile.EffectiveEnforcedChildren(),
		}
		if err := profile.Validate(); err != nil {
			descriptor.InvalidReason = err.Error()
		} else if profile.Enabled {
			descriptor.RouterEligible = profile.EffectivePermissionPolicy() != config.ACPPermissionInteractive
		}
		profiles = append(profiles, descriptor)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles
}
