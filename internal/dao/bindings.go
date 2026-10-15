package dao

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

type BindingRecord struct {
	SessionID   string `bun:"id"`
	CWD         string `bun:"cwd"`
	ChannelType string `bun:"channel_type"`
	ChannelID   string `bun:"channel_id"`
}

type sessionBindingInsert struct {
	bun.BaseModel `bun:"table:sessions"`
	ID            string `bun:"id"`
	CWD           string `bun:"cwd"`
	Timestamp     string `bun:"timestamp"`
	ParentSession string `bun:"parent_session"`
	Version       int    `bun:"version"`
	ChannelType   string `bun:"channel_type"`
	ChannelID     string `bun:"channel_id"`
}

type ChannelToolRecord struct {
	bun.BaseModel `bun:"table:session_channel_tools"`
	SessionID     string `bun:"session_id"`
	ToolName      string `bun:"tool_name"`
	Enabled       bool   `bun:"enabled"`
}

type ChannelToolGenerationRecord struct {
	bun.BaseModel `bun:"table:session_channel_tool_generations"`
	SessionID     string `bun:"session_id,pk"`
	Generation    int64  `bun:"generation"`
	UpdatedAt     string `bun:"updated_at"`
}

// BindingDAO owns session/channel binding and channel-tool persistence.
type BindingDAO struct {
	db *bun.DB
}

func NewBindingDAO(db *bun.DB) *BindingDAO { return &BindingDAO{db: db} }

func (d *BindingDAO) ListChannelTools(ctx context.Context, sessionID string) ([]ChannelToolRecord, error) {
	var records []ChannelToolRecord
	err := d.db.NewSelect().Model(&records).ModelTableExpr("session_channel_tools").ColumnExpr("session_id, tool_name, enabled").
		Where("session_id = ?", sessionID).OrderExpr("tool_name").Scan(ctx, &records)
	return records, err
}

func (d *BindingDAO) SetChannelTools(ctx context.Context, sessionID string, tools []ChannelToolRecord) error {
	return d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var exists string
		if err := tx.NewSelect().Table("sessions").Column("id").Where("id = ?", sessionID).Limit(1).Scan(ctx, &exists); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("session %q not found", sessionID)
			}
			return fmt.Errorf("check channel tool session: %w", err)
		}
		if _, err := tx.NewDelete().Table("session_channel_tools").Where("session_id = ?", sessionID).Exec(ctx); err != nil {
			return err
		}
		for i := range tools {
			tools[i].SessionID = sessionID
		}
		if len(tools) > 0 {
			if _, err := tx.NewInsert().Model(&tools).Exec(ctx); err != nil {
				return err
			}
		}
		result, err := tx.NewUpdate().Model((*ChannelToolGenerationRecord)(nil)).
			Set("generation = generation + 1").Set("updated_at = CURRENT_TIMESTAMP").
			Where("session_id = ?", sessionID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("update channel tool generation: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			_, err = tx.NewInsert().Model(&ChannelToolGenerationRecord{
				SessionID: sessionID, Generation: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}).Exec(ctx)
		}
		return err
	})
}

func (d *BindingDAO) ChannelToolGeneration(ctx context.Context, sessionID string) (int64, error) {
	var generation int64
	err := d.db.NewSelect().Table("session_channel_tool_generations").Column("generation").
		Where("session_id = ?", sessionID).Limit(1).Scan(ctx, &generation)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return generation, err
}

func (d *BindingDAO) List(ctx context.Context) ([]BindingRecord, error) {
	var records []BindingRecord
	err := d.db.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").
		Where("channel_type IN ('wechat', 'feishu') AND channel_id <> ''").
		OrderExpr("channel_type, channel_id").Scan(ctx, &records)
	return records, err
}

func (d *BindingDAO) Find(ctx context.Context, channelType, channelID string) (*BindingRecord, error) {
	record := new(BindingRecord)
	err := d.db.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").
		Where("channel_type = ? AND channel_id = ?", channelType, channelID).Limit(1).Scan(ctx, record)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (d *BindingDAO) FindBySession(ctx context.Context, sessionID string) (*BindingRecord, error) {
	record := new(BindingRecord)
	err := d.db.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").
		Where("id = ? AND channel_type IN ('wechat', 'feishu') AND channel_id <> ''", sessionID).Limit(1).Scan(ctx, record)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (d *BindingDAO) Bind(ctx context.Context, sessionID, channelType, channelID string) error {
	return d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current BindingRecord
		if err := tx.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").Where("id = ?", sessionID).Limit(1).Scan(ctx, &current); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("session %q not found", sessionID)
			}
			return fmt.Errorf("read session binding: %w", err)
		}
		if current.ChannelType != "local" || current.ChannelID != "" {
			return fmt.Errorf("session %q is already bound to %s/%s", sessionID, current.ChannelType, current.ChannelID)
		}
		var other string
		err := tx.NewSelect().Table("sessions").Column("id").Where("channel_type = ? AND channel_id = ? AND id <> ?", channelType, channelID, sessionID).Limit(1).Scan(ctx, &other)
		if err == nil {
			return fmt.Errorf("identity is already bound to session %q", other)
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check existing binding: %w", err)
		}
		_, err = tx.NewUpdate().Table("sessions").Set("channel_type = ?", channelType).Set("channel_id = ?", channelID).Where("id = ?", sessionID).Exec(ctx)
		return err
	})
}

func (d *BindingDAO) Unbind(ctx context.Context, sessionID string) error {
	result, err := d.db.NewUpdate().Table("sessions").Set("channel_type = ?", "local").Set("channel_id = ?", "").Where("id = ?", sessionID).Exec(ctx)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 0 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return err
}

func (d *BindingDAO) Transfer(ctx context.Context, channelType, channelID, fromSessionID, toSessionID string) error {
	return d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var source BindingRecord
		if err := tx.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").Where("id = ?", fromSessionID).Limit(1).Scan(ctx, &source); err != nil {
			return fmt.Errorf("read source binding: %w", err)
		}
		if source.ChannelType != channelType || source.ChannelID != channelID {
			return fmt.Errorf("source session is not bound to %s/%s", channelType, channelID)
		}
		var target BindingRecord
		if err := tx.NewSelect().Table("sessions").Column("id", "channel_type", "channel_id").Where("id = ?", toSessionID).Limit(1).Scan(ctx, &target); err != nil {
			return fmt.Errorf("read target session: %w", err)
		}
		if target.ChannelType != "local" || target.ChannelID != "" {
			return fmt.Errorf("target session is already bound")
		}
		if _, err := tx.NewUpdate().Table("sessions").Set("channel_type = ?", "local").Set("channel_id = ?", "").Where("id = ?", fromSessionID).Exec(ctx); err != nil {
			return fmt.Errorf("clear source binding: %w", err)
		}
		_, err := tx.NewUpdate().Table("sessions").Set("channel_type = ?", channelType).Set("channel_id = ?", channelID).Where("id = ?", toSessionID).Exec(ctx)
		return err
	})
}

func (d *BindingDAO) Rotate(ctx context.Context, workDir, channelType, channelID, oldSessionID string, version int, id, timestamp string) error {
	return d.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current BindingRecord
		if err := tx.NewSelect().Table("sessions").Column("id", "cwd", "channel_type", "channel_id").Where("id = ?", oldSessionID).Limit(1).Scan(ctx, &current); err != nil {
			return err
		}
		if current.ChannelType != channelType || current.ChannelID != channelID {
			return fmt.Errorf("session is no longer bound to %s/%s", channelType, channelID)
		}
		if current.CWD != workDir {
			return fmt.Errorf("session work directory changed during channel rotation")
		}
		if _, err := tx.NewUpdate().Table("sessions").Set("channel_type = ?", "local").Set("channel_id = ?", "").Where("id = ?", oldSessionID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&sessionBindingInsert{
			ID: id, CWD: workDir, Timestamp: timestamp, ParentSession: "", Version: version,
			ChannelType: channelType, ChannelID: channelID,
		}).Exec(ctx)
		return err
	})
}
