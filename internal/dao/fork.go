package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

type ForkRequestRecord struct {
	bun.BaseModel      `bun:"table:session_fork_requests"`
	RequestKeyHash     string `bun:"request_key_hash,pk"`
	RequestFingerprint string `bun:"request_fingerprint"`
	SourceSessionID    string `bun:"source_session_id"`
	ChildSessionID     string `bun:"child_session_id"`
	CreatedAt          string `bun:"created_at"`
}

type ForkSessionRecord struct {
	ID            string         `bun:"id"`
	CWD           sql.NullString `bun:"cwd"`
	Timestamp     sql.NullString `bun:"timestamp"`
	ParentSession sql.NullString `bun:"parent_session"`
	ChannelType   sql.NullString `bun:"channel_type"`
	ChannelID     sql.NullString `bun:"channel_id"`
	ForkBoundary  int64          `bun:"fork_boundary_seq"`
	SeedLength    int64          `bun:"seed_length"`
	ForkKind      sql.NullString `bun:"fork_kind"`
}

type ForkEntryRecord struct {
	SessionID string         `bun:"session_id"`
	Seq       int64          `bun:"seq"`
	ID        string         `bun:"id"`
	Type      string         `bun:"type"`
	ParentID  sql.NullString `bun:"parent_id"`
	Timestamp string         `bun:"timestamp"`
	Data      string         `bun:"data"`
}

type ForkFingerprintRecord struct {
	MaxSeq     int64
	Leaf       string
	OpenTurns  int64
	ActiveRuns int64
}
type ForkRunWindowRecord struct {
	StartedAt  string `bun:"started_at"`
	FinishedAt string `bun:"finished_at"`
	Status     string `bun:"status"`
}

type ForkDAO struct{ db *bun.DB }

func NewForkDAO(db *bun.DB) *ForkDAO { return &ForkDAO{db: db} }

func (d *ForkDAO) FindRequest(ctx context.Context, executor bun.IDB, hash, source string) (*ForkRequestRecord, error) {
	r := new(ForkRequestRecord)
	err := executor.NewSelect().Model(r).Where("request_key_hash = ? AND source_session_id = ?", hash, source).Limit(1).Scan(ctx)
	return r, err
}
func (d *ForkDAO) FindSession(ctx context.Context, executor bun.IDB, id string) (*ForkSessionRecord, error) {
	r := new(ForkSessionRecord)
	err := executor.NewSelect().Table("sessions").Column("id", "cwd", "timestamp", "parent_session", "channel_type", "channel_id", "fork_boundary_seq", "seed_length", "fork_kind").Where("id = ?", id).Limit(1).Scan(ctx, r)
	return r, err
}

func (d *ForkDAO) ChildSessionIDs(ctx context.Context, executor bun.IDB, parentID string) ([]string, error) {
	var ids []string
	err := executor.NewSelect().Table("sessions").Column("id").Where("parent_session = ?", parentID).OrderExpr("timestamp ASC").Scan(ctx, &ids)
	return ids, err
}
func (d *ForkDAO) ActiveRunCount(ctx context.Context, executor bun.IDB, sessionID string, statuses []string) (int, error) {
	var n int
	err := executor.NewSelect().Table("session_runs").ColumnExpr("COUNT(*)").Where("session_id = ? AND status IN (?)", sessionID, bun.In(statuses)).Scan(ctx, &n)
	return n, err
}
func (d *ForkDAO) OpenTurnCount(ctx context.Context, executor bun.IDB, sessionID string) (int, error) {
	var n int
	err := executor.NewSelect().Table("conversation_turns").ColumnExpr("COUNT(*)").Where("session_id = ? AND status = ?", sessionID, "open").Scan(ctx, &n)
	return n, err
}
func (d *ForkDAO) ListEntries(ctx context.Context, executor bun.IDB, sessionID string) ([]ForkEntryRecord, error) {
	var r []ForkEntryRecord
	err := executor.NewSelect().Table("entries").Column("seq", "id", "type", "parent_id", "timestamp", "data").Where("session_id = ?", sessionID).OrderExpr("seq").Scan(ctx, &r)
	return r, err
}
func (d *ForkDAO) EntryAtSeq(ctx context.Context, executor bun.IDB, sessionID string, seq int64) (*ForkEntryRecord, error) {
	r := new(ForkEntryRecord)
	err := executor.NewSelect().Table("entries").Column("seq", "id", "type", "parent_id", "timestamp", "data").Where("session_id = ? AND seq = ?", sessionID, seq).Limit(1).Scan(ctx, r)
	return r, err
}
func (d *ForkDAO) Fingerprint(ctx context.Context, executor bun.IDB, sessionID string, statuses []string) (ForkFingerprintRecord, error) {
	var r ForkFingerprintRecord
	if err := executor.NewRaw("SELECT COALESCE(MAX(seq), 0) FROM (SELECT seq FROM entries WHERE session_id = ? UNION ALL SELECT seq FROM session_event_projections WHERE session_id = ?)", sessionID, sessionID).Scan(ctx, &r.MaxSeq); err != nil {
		return r, err
	}
	if err := executor.NewRaw("SELECT COALESCE((SELECT id FROM (SELECT id, seq FROM entries WHERE session_id = ? UNION ALL SELECT id, seq FROM session_event_projections WHERE session_id = ?) ORDER BY seq DESC LIMIT 1), '')", sessionID, sessionID).Scan(ctx, &r.Leaf); err != nil {
		return r, err
	}
	if err := executor.NewSelect().Table("conversation_turns").ColumnExpr("COUNT(*)").Where("session_id = ? AND status = ?", sessionID, "open").Scan(ctx, &r.OpenTurns); err != nil {
		return r, err
	}
	if err := executor.NewSelect().Table("session_runs").ColumnExpr("COUNT(*)").Where("session_id = ? AND status IN (?)", sessionID, bun.In(statuses)).Scan(ctx, &r.ActiveRuns); err != nil {
		return r, err
	}
	return r, nil
}
func (d *ForkDAO) RunWindows(ctx context.Context, executor bun.IDB, sessionID string, statuses []string) ([]ForkRunWindowRecord, error) {
	var r []ForkRunWindowRecord
	err := executor.NewSelect().Table("session_runs").Column("started_at", "finished_at", "status").Where("session_id = ? AND status IN (?)", sessionID, bun.In(statuses)).OrderExpr("started_at, updated_at").Scan(ctx, &r)
	return r, err
}
func (d *ForkDAO) InsertSessionFrom(ctx context.Context, executor bun.IDB, child, source string, boundary, seed int64, kind string) error {
	_, err := executor.NewRaw("INSERT INTO sessions (id,cwd,timestamp,parent_session,version,channel_type,channel_id,fork_boundary_seq,seed_length,fork_kind,expert_id) SELECT ?,cwd,timestamp,?,version,'local','',?,?,?,expert_id FROM sessions WHERE id = ?", child, source, boundary, seed, kind, source).Exec(ctx)
	return err
}
func (d *ForkDAO) InsertEntry(ctx context.Context, executor bun.IDB, record *ForkEntryRecord) (int64, error) {
	var seq int64
	err := executor.NewRaw("INSERT INTO entries (session_id, id, type, parent_id, timestamp, data) VALUES (?, ?, ?, ?, ?, ?) RETURNING seq", record.SessionID, record.ID, record.Type, record.ParentIDValue(), record.Timestamp, record.Data).Scan(ctx, &seq)
	return seq, err
}
func (r *ForkEntryRecord) ParentIDValue() any {
	if !r.ParentID.Valid {
		return nil
	}
	return r.ParentID.String
}

var _ = sql.ErrNoRows

func (d *ForkDAO) InsertTurn(ctx context.Context, executor bun.IDB, id, session, intent, kind, status string, start, end int64, started string, ended any) error {
	_, err := executor.NewRaw("INSERT INTO conversation_turns (id, session_id, intent_id, kind, status, start_seq, end_seq, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", id, session, intent, kind, status, start, end, started, ended).Exec(ctx)
	return err
}
func (d *ForkDAO) CopyCapabilities(ctx context.Context, executor bun.IDB, source, child string) error {
	_, err := executor.NewRaw("INSERT INTO session_capabilities (session_id,mode,display_mode,delegate_mode,multi_agent,workflows,web_search,browser,a2a_master,updated_at) SELECT ?,mode,display_mode,delegate_mode,multi_agent,workflows,web_search,browser,a2a_master,updated_at FROM session_capabilities WHERE session_id = ?", child, source).Exec(ctx)
	return err
}
func (d *ForkDAO) CopyProject(ctx context.Context, executor bun.IDB, source, child string) error {
	_, err := executor.NewRaw("INSERT INTO session_metadata (session_id,project_id,pinned,updated_at) SELECT ?,project_id,0,updated_at FROM session_metadata WHERE session_id = ?", child, source).Exec(ctx)
	return err
}
func (d *ForkDAO) CurrentEntryID(ctx context.Context, executor bun.IDB, session string) (string, error) {
	var id string
	err := executor.NewSelect().Table("entries").Column("id").Where("session_id = ?", session).OrderExpr("seq DESC").Limit(1).Scan(ctx, &id)
	return id, err
}
func (d *ForkDAO) TitleExists(ctx context.Context, executor bun.IDB, parent, typ, title string) (bool, error) {
	var n int
	err := executor.NewSelect().TableExpr("entries AS e").Join("JOIN sessions AS s ON s.id = e.session_id").ColumnExpr("COUNT(*)").Where("s.parent_session = ? AND e.type = ? AND json_extract(e.data, '$.name') = ?", parent, typ, title).Scan(ctx, &n)
	return n > 0, err
}
func (d *ForkDAO) InsertForkRequest(ctx context.Context, executor bun.IDB, record *ForkRequestRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}
func (d *ForkDAO) InsertRawEntry(ctx context.Context, executor bun.IDB, session, id, typ, parent, timestamp, data string) error {
	_, err := executor.NewRaw("INSERT INTO entries (session_id, id, type, parent_id, timestamp, data) VALUES (?, ?, ?, ?, ?, ?)", session, id, typ, parent, timestamp, data).Exec(ctx)
	return err
}
func (d *ForkDAO) Result(ctx context.Context, executor bun.IDB, id string) (ForkSessionRecord, error) {
	r, err := d.FindSession(ctx, executor, id)
	if err != nil {
		return ForkSessionRecord{}, err
	}
	return *r, nil
}
