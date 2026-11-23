package desktop

import (
	"encoding/json"
	"strings"
)

// commandKind reads the additive ACP command kind metadata. Commands without
// metadata, or with an unknown kind, stay plain commands; only the Runtime's
// explicit "skill" marker changes the card semantics. This is shell-side
// command projection, not persisted skill management state.
func commandKind(command command) string {
	if raw, ok := command.Meta["mothx.dev"]; ok {
		var meta struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			if kind := strings.TrimSpace(meta.Kind); kind != "" {
				return kind
			}
		}
	}
	return "command"
}
