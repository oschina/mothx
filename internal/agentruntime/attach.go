package agentruntime

import (
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/expert"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

// AttachedResources are adapter-policy-selected resources attached to the
// common runtime. Use this only when protocol-specific registry or MCP policy
// cannot yet be represented by Builder; Runtime retains all lifecycle ownership.
type AttachedResources struct {
	ID                    string
	Source                RuntimeSource
	EntrySource           RuntimeSource
	WorkDir               string
	Manager               *session.Manager
	Registry              *tools.Registry
	SandboxMgr            *sandbox.Manager
	SkillsMgr             *skills.Manager
	MCPClients            []*mcp.Client
	Providers             ProviderCatalog
	ExtraContext          string
	RuleContent           string
	AdditionalDirectories []string
	// Settings and capability flags describe the shared resource assembly policy
	// for this compatibility bridge. When present, AttachSessionResources uses
	// them to reload context/skills with the persisted expert package included.
	Settings        *config.Settings
	Workflows       bool
	Browser         bool
	ArtifactEnabled bool
}

// AttachSessionResources creates a SessionRuntime around already-selected
// resources. It validates the session ownership boundary and is the sole
// compatibility bridge for adapters with protocol-specific Registry/MCP policy.
func AttachSessionResources(resources AttachedResources) (*SessionRuntime, error) {
	if resources.WorkDir == "" || resources.Manager == nil || resources.Registry == nil {
		return nil, fmt.Errorf("runtime work directory, session manager, and registry are required")
	}
	if resources.ID == "" && resources.Manager.GetHeader() != nil {
		resources.ID = resources.Manager.GetHeader().ID
	}
	if resources.ID == "" {
		return nil, fmt.Errorf("runtime session ID is required")
	}
	additionalDirectories, err := NormalizeAdditionalDirectories(resources.AdditionalDirectories)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveManagerSource(resources.Manager, SourceResolutionInput{Requested: resources.Source})
	if err != nil {
		return nil, err
	}
	entrySource := resources.EntrySource
	if entrySource == SourceUnknown {
		entrySource = resources.Source
	}
	attachments, err := NewAttachmentService(resources.Manager.GetSessionDir(), DefaultAttachmentPolicy())
	if err != nil {
		return nil, err
	}
	inputs, err := NewInputMaterializer(resources.Manager.GetSessionDir(), resources.WorkDir, DefaultInputPolicy())
	if err != nil {
		return nil, err
	}
	runtime := &SessionRuntime{
		ID: resources.ID, Source: resolved.Source, EntrySource: entrySource,
		Policy: PolicyForSource(resolved.Source, ""), WorkDir: resources.WorkDir,
		Manager: resources.Manager, Inputs: inputs, Attachments: attachments, Registry: resources.Registry, SandboxMgr: resources.SandboxMgr,
		SkillsMgr: resources.SkillsMgr, MCPClients: resources.MCPClients,
		Providers:    resources.Providers,
		ExtraContext: resources.ExtraContext, RuleContent: resources.RuleContent, AdditionalDirectories: additionalDirectories, LastUsed: time.Now(),
		ArtifactEnabled:     resources.ArtifactEnabled,
		imageGenerationTool: registryExposesImageGeneration(resources.Registry),
		resourceSettings:    resources.Settings,
		resourceWorkflows:   resources.Workflows,
		resourceBrowser:     resources.Browser,
	}
	runtime.Mailbox = agent.NewMemberMailbox()
	runtime.ExpertCenter = &expert.Center{ProjectDir: resources.WorkDir}
	if err := runtime.rehydrateBoundResources(); err != nil {
		return nil, err
	}
	if err := runtime.ReloadAdditionalDirectories(resources.Manager); err != nil {
		return nil, err
	}
	return runtime, nil
}
