package session

import (
	"context"
	"strings"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/platform"
)

// SessionDetailByID loads the session-list projection for exactly one session.
// It shares the DAO row projection and buildSessionDetails with the full
// listing, so callers that need to re-read one session after a write (title or
// metadata) see the same fields as ListAllDetailed without scanning or
// replaying every session.
//
// The bool reports whether the session exists. A missing session is not an
// error.
func SessionDetailByID(sessionDir, id string) (SessionDetail, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SessionDetail{}, false, nil
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return SessionDetail{}, false, err
	}
	record, err := dao.NewSessionDAO(db.Bun()).FindListByID(context.Background(), id)
	if err != nil {
		return SessionDetail{}, false, err
	}
	if record == nil {
		return SessionDetail{}, false, nil
	}
	details, err := buildSessionDetails(sessionInfosFromRecords(sessionDir, []dao.SessionRecord{*record}), false)
	if err != nil {
		return SessionDetail{}, false, err
	}
	if len(details) == 0 {
		return SessionDetail{}, false, nil
	}
	return details[0], true, nil
}
