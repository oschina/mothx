package tui

import (
	"errors"
	"path/filepath"

	database "github.com/oschina/mothx/internal/db"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// sessionCreationError keeps structural SQLite errors actionable without
// hiding the diagnostic that is useful when recovering the old database.
func sessionCreationError(tr i18n.Translator, err error, sessionDir string) string {
	var integrity *database.IntegrityError
	if !errors.As(err, &integrity) {
		return "Error creating session: " + err.Error()
	}
	path := integrity.Path
	if path == "" {
		path = filepath.Join(sessionDir, "sessions.db")
	}
	return tr.Text(i18n.MsgSessionDatabaseCorrupt, path, sessionDir, integrity.Report)
}
