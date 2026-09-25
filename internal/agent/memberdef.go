package agent

import "sync"

// MemberDef is the agent-package view of one expert-team member: a persona
// prompt plus optional capability overrides. The runtime assembly layer builds
// these from the bound expert bundle and installs them via
// AgentManager.SetMemberContext; internal/agent never parses bundle files.
type MemberDef struct {
	ID            string
	DisplayName   string
	Emoji         string
	Role          string // "lead" | "member"
	Description   string
	Prompt        string // persona system prompt, injected via SystemPromptExtra
	Mode          string // ""|plan|agent|yolo|os
	Tools         []string
	MaxIterations int
	WorkDir       string
	// Worktree marks a member that must run in its own isolated worktree. It is
	// mutually exclusive with a fixed WorkDir.
	Worktree bool
}

// MemberDefRegistry is an insertion-ordered, concurrency-safe lookup table of
// member definitions keyed by id.
type MemberDefRegistry struct {
	mu   sync.RWMutex
	defs map[string]*MemberDef
	ids  []string
}

// NewMemberDefRegistry builds a registry from defs, preserving insertion
// order. Nil entries and entries with an empty id are skipped; for duplicate
// ids the first definition wins.
func NewMemberDefRegistry(defs []*MemberDef) *MemberDefRegistry {
	r := &MemberDefRegistry{defs: make(map[string]*MemberDef, len(defs))}
	for _, def := range defs {
		if def == nil || def.ID == "" {
			continue
		}
		if _, exists := r.defs[def.ID]; exists {
			continue
		}
		r.defs[def.ID] = def
		r.ids = append(r.ids, def.ID)
	}
	return r
}

// Get returns the member definition registered for id. A nil registry has no
// members.
func (r *MemberDefRegistry) Get(id string) (*MemberDef, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[id]
	return def, ok
}

// IDs returns the registered member ids in insertion order. The result is a
// copy; a nil registry returns nil.
func (r *MemberDefRegistry) IDs() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.ids))
	copy(out, r.ids)
	return out
}
