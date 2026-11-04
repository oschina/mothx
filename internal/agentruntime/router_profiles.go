package agentruntime

import (
	"net/url"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/config"
)

// ACPWorkerProfileDescriptor is the safe, model/management-facing view of one
// configured external ACP worker. It never contains argv, environment names,
// credentials, query/fragment values, or configuration paths.
type ACPWorkerProfileDescriptor struct {
	ID        string                   `json:"id"`
	Title     string                   `json:"title,omitempty"`
	Transport config.ACPAgentTransport `json:"transport"`
	// Endpoint is the administrator-configured remote URL with any query or
	// fragment removed. Empty for stdio profiles.
	Endpoint              string                     `json:"endpoint,omitempty"`
	Enabled               bool                       `json:"enabled"`
	RouterEligible        bool                       `json:"routerEligible"`
	Roles                 []config.ACPAgentRole      `json:"roles,omitempty"`
	PermissionPolicy      config.ACPPermissionPolicy `json:"permissionPolicy"`
	MaxConcurrentChildren int                        `json:"maxConcurrentChildren"`
	// MaxConcurrentChildrenEnforced is the live-worker ceiling actually applied by
	// this release. It currently matches the configured read-only ceiling; write
	// roles remain exclusive regardless of this value.
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
			ID: profile.ID, Title: profile.Title, Transport: profile.EffectiveTransport(),
			Endpoint: safeACPEndpoint(profile), Enabled: profile.Enabled,
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

func safeACPEndpoint(profile config.ACPAgentProfile) string {
	if !profile.IsRemote() {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(profile.URL))
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}
