// Package expert implements the expert bundle format (manifest + persona
// markdown) and the ExpertCenter layered directory sources
// (builtin/global/project with name shadowing) used to bind persona content
// packs to sessions.
package expert

import "io/fs"

// Supported expertType values in expert.json.
const (
	// TypeAgent is a single-persona bundle (manifest agentName points at the
	// only agents/*.md persona).
	TypeAgent = "agent"
	// TypeTeam is a lead + members bundle (manifest teamInfo declares the
	// roster).
	TypeTeam = "team"
)

// Member roles within a bundle.
const (
	RoleLead   = "lead"
	RoleMember = "member"
)

// Source layer identifiers reported by Center.
const (
	SourceBuiltin = "builtin"
	SourceGlobal  = "global"
	SourceProject = "project"
)

// SchemaVersion is the only supported expert.json schema version.
const SchemaVersion = 1

// LocalizedText is a bilingual zh/en text pair used by manifest metadata.
type LocalizedText struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// MemberMeta is a manifest members[] entry: pure UI persona metadata that the
// runtime does not consume for dispatch.
type MemberMeta struct {
	ID         string        `json:"id"`
	Name       LocalizedText `json:"name"`
	Profession LocalizedText `json:"profession,omitempty"`
	Avatar     string        `json:"avatar,omitempty"`
	Role       string        `json:"role"` // "lead" | "member"
}

// TeamInfo declares the lead/member agent ids of a team bundle.
type TeamInfo struct {
	LeadAgent    string   `json:"leadAgent"`
	MemberAgents []string `json:"memberAgents"`
}

// Manifest is the expert.json content of a bundle.
type Manifest struct {
	SchemaVersion     int             `json:"schemaVersion"`
	Name              string          `json:"name"`
	ExpertType        string          `json:"expertType"`          // "agent" | "team"
	AgentName         string          `json:"agentName,omitempty"` // required for agent type; kept in sync with teamInfo.leadAgent for team
	DisplayName       LocalizedText   `json:"displayName"`
	CategoryID        string          `json:"categoryId,omitempty"`
	QuickPrompts      []LocalizedText `json:"quickPrompts,omitempty"`
	DefaultInitPrompt LocalizedText   `json:"defaultInitPrompt,omitempty"`
	TeamInfo          *TeamInfo       `json:"teamInfo,omitempty"` // team 型必填
	Members           []MemberMeta    `json:"members,omitempty"`
}

// Frontmatter holds the parsed agents/*.md frontmatter fields (persona +
// declarative capability overrides).
type Frontmatter struct {
	Name          string
	Description   string
	Role          string
	Emoji         string
	Color         string
	Vibe          string
	Mode          string // "" | plan | agent | yolo | os
	Tools         []string
	MaxIterations int
	WorkDir       string
	// Worktree marks a member persona that must run in its own isolated
	// worktree. It is mutually exclusive with WorkDir.
	Worktree bool
}

// AgentDef is one parsed persona definition (agents/<ID>.md).
type AgentDef struct {
	ID          string // = frontmatter name = agents/<ID>.md file name (without .md)
	DisplayName string // manifest members[] name.zh, else name.en, else ID
	Emoji       string
	Role        string // "lead" | "member"; manifest decision first, frontmatter fallback
	Description string
	Prompt      string // markdown body (persona system prompt)
	Meta        Frontmatter
}

// Bundle is a loaded expert package. Validation failures are reported via
// Invalid/InvalidReason instead of an error; loader errors are IO failures
// only.
type Bundle struct {
	Name          string
	Manifest      Manifest
	Defs          map[string]*AgentDef // all agents/*.md, key = ID
	Invalid       bool
	InvalidReason string
	// SkillsDir is the skills/ location when the bundle carries one. Runtime
	// resource assembly loads it through internal/skills; for OS-directory
	// loads it is a filesystem path and SkillsFS is nil, while for fs.FS loads
	// it is a slash path inside SkillsFS.
	SkillsDir string
	SkillsFS  fs.FS
}
