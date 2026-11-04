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
	PermissionPolicy      config.ACPPermissionPolicy `json:"permissionPolicy"`
	MaxConcurrentChildren int                        `json:"maxConcurrentChildren"`
	// MaxConcurrentChildrenEnforced is the live-worker ceiling actually applied by
	// this release. It matches the configured ceiling.
	MaxConcurrentChildrenEnforced int `json:"maxConcurrentChildrenEnforced"`
	// ParallelBaseWorkspaceWrites reports that the profile opted into
	// concurrent base-workspace writers. MothX cannot confine an external
	// Agent's own tools to the declared write scope, so this is explicit
	// operator trust and stays visible in every management projection.
	ParallelBaseWorkspaceWrites bool   `json:"parallelBaseWorkspaceWrites,omitempty"`
	InvalidReason               string `json:"invalidReason,omitempty"`
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
			PermissionPolicy:      profile.EffectivePermissionPolicy(),
			MaxConcurrentChildren: profile.EffectiveMaxConcurrentChildren(),

			MaxConcurrentChildrenEnforced: profile.EffectiveEnforcedChildren(),
			ParallelBaseWorkspaceWrites:   profile.EffectiveAllowParallelBaseWorkspaceWrites(),
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
