package session

import (
	"context"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"strings"
	"time"
)

type ESMGuidance struct {
	ID               string     `json:"id"`
	SessionID        string     `json:"sessionId"`
	ObjectiveVersion string     `json:"objectiveVersion,omitempty"`
	Guidance         string     `json:"guidance"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"createdAt"`
	ConsumedAt       *time.Time `json:"consumedAt,omitempty"`
}

func SaveESMGuidance(sessionDir string, g ESMGuidance) error {
	g.Guidance = strings.TrimSpace(g.Guidance)
	if g.ID == "" || g.SessionID == "" || g.Guidance == "" {
		return fmt.Errorf("ESM guidance ID, session ID, and guidance are required")
	}
	if g.Status == "" {
		g.Status = "pending"
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now().UTC()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, g.SessionID); err != nil {
		return err
	}
	var consumedAt *string
	if value := nullableTime(g.ConsumedAt); value != nil {
		formatted := value.(string)
		consumedAt = &formatted
	}
	if err := dao.NewESMGuidanceDAO(db.Bun()).Insert(context.Background(), tx, &dao.ESMGuidanceRecord{
		ID: g.ID, SessionID: g.SessionID, ObjectiveVersion: g.ObjectiveVersion,
		Guidance: g.Guidance, Status: g.Status, CreatedAt: g.CreatedAt.Format(time.RFC3339Nano), ConsumedAt: consumedAt,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func ListESMGuidance(sessionDir, sessionID, status string, limit int) ([]ESMGuidance, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewESMGuidanceDAO(db.Bun()).List(context.Background(), sessionID, status, limit)
	if err != nil {
		return nil, err
	}
	var out []ESMGuidance
	for _, record := range records {
		g := ESMGuidance{ID: record.ID, SessionID: record.SessionID, ObjectiveVersion: record.ObjectiveVersion,
			Guidance: record.Guidance, Status: record.Status, CreatedAt: parseSessionTimestamp(record.CreatedAt)}
		if record.ConsumedAt != nil {
			t := parseSessionTimestamp(*record.ConsumedAt)
			g.ConsumedAt = &t
		}
		out = append(out, g)
	}
	return out, nil
}

func ConsumeESMGuidance(sessionDir, sessionID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return err
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := dao.NewESMGuidanceDAO(db.Bun()).Consume(context.Background(), tx, sessionID, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
