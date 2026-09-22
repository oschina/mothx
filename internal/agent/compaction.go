package agent

import (
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
)

// CompactionSettingsFromConfig converts the user-facing settings.json
// compaction block into the agent-loop compaction settings. All runtimes
// (CLI, TUI, serve API, channels, ACP, A2A) must build agent compaction
// settings through this helper so no field is dropped. Zero-valued limits
// are filled later by NormalizeCompactionSettings inside New and
// NewWithLoopConfig.
func CompactionSettingsFromConfig(c config.CompactionSettings) ctxpkg.CompactionSettings {
	return ctxpkg.CompactionSettings{
		Enabled:          c.Enabled,
		ReserveTokens:    c.ReserveTokens,
		KeepRecentTokens: c.KeepRecentTokens,
		Tokenizer:        c.Tokenizer,
		TokenizerModel:   c.TokenizerModel,
		Template:         c.Template,
	}
}
