package dao

import (
	"context"
	"database/sql"

	"github.com/uptrace/bun"
)

type SessionRecord struct {
	bun.BaseModel   `bun:"table:sessions"`
	ID              string  `bun:"id,pk"`
	CWD             string  `bun:"cwd"`
	Timestamp       string  `bun:"timestamp"`
	ChannelType     string  `bun:"channel_type"`
	ChannelID       string  `bun:"channel_id"`
	ParentSession   *string `bun:"parent_session,nullzero"`
	Version         int     `bun:"version"`
	ForkBoundarySeq int64   `bun:"fork_boundary_seq"`
	SeedLength      int64   `bun:"seed_length"`
	ForkKind        string  `bun:"fork_kind"`
	ExpertID        string  `bun:"expert_id"`
}

type SessionCapabilityRecord struct {
	bun.BaseModel `bun:"table:session_capabilities"`
	SessionID     string `bun:"session_id,pk"`
	Mode          string `bun:"mode"`
	DisplayMode   string `bun:"display_mode"`
	DelegateMode  int    `bun:"delegate_mode"`
	MultiAgent    int    `bun:"multi_agent"`
	Workflows     int    `bun:"workflows"`
	WebSearch     int    `bun:"web_search"`
	Browser       int    `bun:"browser"`
	A2AMaster     int    `bun:"a2a_master"`
	UpdatedAt     string `bun:"updated_at"`
}

type SessionCapabilityEventRecord struct {
	bun.BaseModel `bun:"table:session_capability_events"`
	Seq           int64  `bun:"seq"`
	ID            string `bun:"id,pk"`
	SessionID     string `bun:"session_id"`
	RunID         string `bun:"run_id"`
	EventType     string `bun:"event_type"`
	Source        string `bun:"source"`
	Actor         string `bun:"actor"`
	Capability    string `bun:"capability"`
	OldValue      string `bun:"old_value"`
	NewValue      string `bun:"new_value"`
	Timestamp     string `bun:"timestamp"`
	Data          string `bun:"data"`
}

type SessionListFilter struct {
	CWD, Search   string
	MessagesOnly  bool
	Limit, Offset int
}
type SessionDAO struct{ db *bun.DB }

type sessionListRow struct {
	ID            string  `bun:"id"`
	CWD           string  `bun:"cwd"`
	Timestamp     string  `bun:"timestamp"`
	ChannelType   string  `bun:"channel_type"`
	ChannelID     string  `bun:"channel_id"`
	ParentSession *string `bun:"parent_session"`
	Version       int     `bun:"version"`
	ForkBoundary  int64   `bun:"fork_boundary_seq"`
	SeedLength    int64   `bun:"seed_length"`
	ForkKind      string  `bun:"fork_kind"`
	ExpertID      string  `bun:"expert_id"`
}

type SessionDetailAggregates struct {
	MessageCounts map[string]int
	FirstMessages map[string]string
	LatestInfos   map[string]string
}

func NewSessionDAO(db *bun.DB) *SessionDAO { return &SessionDAO{db: db} }

func (d *SessionDAO) DetailAggregates(ctx context.Context, sessionIDs []string) (SessionDetailAggregates, error) {
	result := SessionDetailAggregates{MessageCounts: make(map[string]int), FirstMessages: make(map[string]string), LatestInfos: make(map[string]string)}
	if len(sessionIDs) == 0 {
		return result, nil
	}
	var counts []struct {
		SessionID string `bun:"session_id"`
		Count     int    `bun:"message_count"`
	}
	if err := d.db.NewSelect().Table("entries").Column("session_id").ColumnExpr("COUNT(*) AS message_count").Where("session_id IN (?) AND type = ?", bun.In(sessionIDs), "message").Group("session_id").Scan(ctx, &counts); err != nil {
		return result, err
	}
	for _, row := range counts {
		result.MessageCounts[row.SessionID] = row.Count
	}
	var first []struct {
		SessionID string `bun:"session_id"`
		Data      string `bun:"data"`
	}
	if err := d.db.NewSelect().TableExpr("entries AS e").Column("e.session_id", "e.data").Join("JOIN (SELECT session_id, MIN(seq) AS min_seq FROM entries WHERE type = 'message' AND session_id IN (?) GROUP BY session_id) AS first ON e.session_id = first.session_id AND e.seq = first.min_seq", bun.In(sessionIDs)).Scan(ctx, &first); err != nil {
		return result, err
	}
	for _, row := range first {
		result.FirstMessages[row.SessionID] = row.Data
	}
	var infos []struct {
		SessionID string `bun:"session_id"`
		Data      string `bun:"data"`
	}
	if err := d.db.NewSelect().Table("entries").Column("session_id", "data").Where("session_id IN (?) AND type = ?", bun.In(sessionIDs), "session_info").OrderExpr("seq DESC").Scan(ctx, &infos); err != nil {
		return result, err
	}
	for _, row := range infos {
		if _, ok := result.LatestInfos[row.SessionID]; !ok {
			result.LatestInfos[row.SessionID] = row.Data
		}
	}
	return result, nil
}

func (d *SessionDAO) InsertSession(ctx context.Context, executor bun.IDB, table string, id, cwd, timestamp, parent string, version int, channelType, channelID string, boundary, seed int64, kind, expertID string) error {
	_, err := executor.NewRaw("INSERT INTO "+table+" (id,cwd,timestamp,parent_session,version,channel_type,channel_id,fork_boundary_seq,seed_length,fork_kind,expert_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)", id, cwd, timestamp, nullableSessionString(parent), version, channelType, channelID, boundary, seed, kind, expertID).Exec(ctx)
	return err
}

// UpdateSessionExpertID persists a session's expert binding (empty string
// unbinds). It mirrors the channel-binding update pattern.
func (d *SessionDAO) UpdateSessionExpertID(ctx context.Context, executor bun.IDB, table, sessionID, expertID string) error {
	_, err := executor.NewUpdate().Table(table).Set("expert_id = ?", expertID).Where("id = ?", sessionID).Exec(ctx)
	return err
}

// UpdateSessionCWD persists the working directory owned by a session. The
// session row is the canonical header source when a session is re-opened;
// callers must rebuild any runtime resources that were derived from the old
// directory after this succeeds.
func (d *SessionDAO) UpdateSessionCWD(ctx context.Context, executor bun.IDB, table, sessionID, cwd string) error {
	_, err := executor.NewUpdate().Table(table).Set("cwd = ?", cwd).Where("id = ?", sessionID).Exec(ctx)
	return err
}

func nullableSessionString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (d *SessionDAO) CurrentLeaf(ctx context.Context, executor bun.IDB, table, sessionID, excludedType string) (string, error) {
	var id string
	err := executor.NewRaw("SELECT id FROM "+table+" WHERE session_id = ? AND type != ? ORDER BY seq DESC LIMIT 1", sessionID, excludedType).Scan(ctx, &id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func (d *SessionDAO) InsertEntry(ctx context.Context, executor bun.IDB, table string, sessionID, id, typ string, parent any, timestamp, data string) error {
	_, err := executor.NewRaw("INSERT INTO "+table+" (session_id,id,type,parent_id,timestamp,data) VALUES (?,?,?,?,?,?)", sessionID, id, typ, parent, timestamp, data).Exec(ctx)
	return err
}

func (d *SessionDAO) ListForDir(ctx context.Context, cwd string) ([]SessionRecord, error) {
	var rows []SessionRecord
	err := d.db.NewSelect().Model(&rows).Where("cwd = ?", cwd).OrderExpr("timestamp DESC").Scan(ctx)
	return rows, err
}
func (d *SessionDAO) List(ctx context.Context, filter SessionListFilter) ([]SessionRecord, error) {
	var rows []sessionListRow
	q := d.db.NewSelect().TableExpr("sessions AS s").ColumnExpr("s.id, s.cwd, s.timestamp, s.channel_type, s.channel_id, s.parent_session, s.version, s.fork_boundary_seq, s.seed_length, s.fork_kind, s.expert_id")
	if filter.CWD != "" {
		q.Where("s.cwd = ?", filter.CWD)
	}
	if filter.MessagesOnly {
		q.Where("EXISTS (SELECT 1 FROM entries e WHERE e.session_id = s.id AND e.type = 'message')")
	}
	if filter.Search != "" {
		p := "%" + filter.Search + "%"
		q.Where("(s.id LIKE ? COLLATE NOCASE OR s.cwd LIKE ? COLLATE NOCASE OR s.channel_type LIKE ? COLLATE NOCASE OR s.channel_id LIKE ? COLLATE NOCASE OR EXISTS (SELECT 1 FROM entries e WHERE e.session_id = s.id AND e.data LIKE ? COLLATE NOCASE))", p, p, p, p, p)
	}
	q.OrderExpr("s.timestamp DESC")
	if filter.Limit > 0 {
		q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q.Offset(filter.Offset)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	result := make([]SessionRecord, len(rows))
	for i, row := range rows {
		result[i] = SessionRecord{ID: row.ID, CWD: row.CWD, Timestamp: row.Timestamp, ChannelType: row.ChannelType, ChannelID: row.ChannelID, ParentSession: row.ParentSession, Version: row.Version, ForkBoundarySeq: row.ForkBoundary, SeedLength: row.SeedLength, ForkKind: row.ForkKind, ExpertID: row.ExpertID}
	}
	return result, nil
}
func (d *SessionDAO) Count(ctx context.Context, filter SessionListFilter) (int, error) {
	q := d.db.NewSelect().TableExpr("sessions AS s").ColumnExpr("COUNT(*)")
	if filter.MessagesOnly {
		q.Where("EXISTS (SELECT 1 FROM entries e WHERE e.session_id = s.id AND e.type = 'message')")
	}
	if filter.CWD != "" {
		q.Where("s.cwd = ?", filter.CWD)
	}
	if filter.Search != "" {
		p := "%" + filter.Search + "%"
		q.Where("(s.id LIKE ? COLLATE NOCASE OR s.cwd LIKE ? COLLATE NOCASE OR s.channel_type LIKE ? COLLATE NOCASE OR s.channel_id LIKE ? COLLATE NOCASE OR EXISTS (SELECT 1 FROM entries e WHERE e.session_id = s.id AND e.data LIKE ? COLLATE NOCASE))", p, p, p, p, p)
	}
	var count int
	return count, q.Scan(ctx, &count)
}
func (d *SessionDAO) FindExact(ctx context.Context, table, id string) (string, error) {
	var value string
	err := d.db.NewSelect().Table(table).Column("id").Where("id = ?", id).Limit(1).Scan(ctx, &value)
	return value, err
}
func (d *SessionDAO) PrefixIDs(ctx context.Context, table, cwd, prefix string) ([]string, error) {
	var ids []string
	err := d.db.NewSelect().Table(table).Column("id").Where("cwd = ? AND id LIKE ?", cwd, prefix+"%").Scan(ctx, &ids)
	return ids, err
}
func (d *SessionDAO) Timestamp(ctx context.Context, table, id string) (string, error) {
	var value string
	err := d.db.NewSelect().Table(table).Column("timestamp").Where("id = ?", id).Limit(1).Scan(ctx, &value)
	return value, err
}
func (d *SessionDAO) Header(ctx context.Context, table, id string) (*SessionRecord, error) {
	row := new(SessionRecord)
	err := d.db.NewSelect().Table(table).Column("cwd", "timestamp", "parent_session", "version", "channel_type", "channel_id", "fork_boundary_seq", "seed_length", "fork_kind", "expert_id").Where("id = ?", id).Limit(1).Scan(ctx, row)
	return row, err
}
func (d *SessionDAO) Entries(ctx context.Context, table, sessionID string) ([]EntryRecord, error) {
	var rows []EntryRecord
	err := d.db.NewSelect().Table(table).Column("type", "data").Where("session_id = ?", sessionID).OrderExpr("seq ASC").Scan(ctx, &rows)
	return rows, err
}

// DeleteSession removes the session row and every child row that references
// it. tables lists the session_id-keyed child tables and must be ordered
// child-first so the delete stays safe if SQLite foreign key enforcement is
// ever enabled. Child tables without a session_id column are pruned through
// their session-owned parents before those parents are deleted.
func (d *SessionDAO) DeleteSession(ctx context.Context, executor bun.IDB, sessionID string, tables []string) error {
	if _, err := executor.NewDelete().Table("session_fork_requests").Where("source_session_id = ? OR child_session_id = ?", sessionID, sessionID).Exec(ctx); err != nil {
		return err
	}
	// delivery_operations has no session_id column; prune it through its
	// session-owned intent parent before delivery_intents is deleted.
	if _, err := executor.NewDelete().Table("delivery_operations").
		Where("intent_id IN (SELECT id FROM delivery_intents WHERE session_id = ?)", sessionID).Exec(ctx); err != nil {
		return err
	}
	// attachment_deliveries has no session_id column; prune it through its
	// session-owned attachment parent before session_attachments is deleted.
	if _, err := executor.NewDelete().Table("attachment_deliveries").
		Where("attachment_id IN (SELECT id FROM session_attachments WHERE session_id = ?)", sessionID).Exec(ctx); err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := executor.NewDelete().Table(table).Where("session_id = ?", sessionID).Exec(ctx); err != nil {
			return err
		}
	}
	_, err := executor.NewDelete().Table("sessions").Where("id = ?", sessionID).Exec(ctx)
	return err
}
func (d *SessionDAO) UpsertCapability(ctx context.Context, executor bun.IDB, row *SessionCapabilityRecord) error {
	_, err := executor.NewInsert().Model(row).On("CONFLICT(session_id) DO UPDATE SET mode=excluded.mode, display_mode=excluded.display_mode, delegate_mode=excluded.delegate_mode, multi_agent=excluded.multi_agent, workflows=excluded.workflows, web_search=excluded.web_search, browser=excluded.browser, a2a_master=excluded.a2a_master, updated_at=excluded.updated_at").Exec(ctx)
	return err
}
func (d *SessionDAO) Capability(ctx context.Context, sessionID string) (*SessionCapabilityRecord, error) {
	row := new(SessionCapabilityRecord)
	err := d.db.NewSelect().Model(row).Where("session_id = ?", sessionID).Limit(1).Scan(ctx)
	return row, err
}
func (d *SessionDAO) InsertRunEvent(ctx context.Context, executor bun.IDB, row *SessionRunEventRecord) error {
	_, err := executor.NewInsert().Model(row).ExcludeColumn("seq").Exec(ctx)
	return err
}
func (d *SessionDAO) ListRunEvents(ctx context.Context, sessionID string) ([]SessionRunEventRecord, error) {
	return d.ListRunEventsFrom(ctx, d.db, sessionID)
}
func (d *SessionDAO) ListRunEventsFrom(ctx context.Context, executor bun.IDB, sessionID string) ([]SessionRunEventRecord, error) {
	var rows []SessionRunEventRecord
	err := executor.NewSelect().Model(&rows).Where("session_id = ?", sessionID).OrderExpr("seq ASC").Scan(ctx)
	return rows, err
}
func (d *SessionDAO) MaxRunEventSeq(ctx context.Context, runID string) (int64, error) {
	var seq int64
	err := d.db.NewSelect().Table("session_run_events").ColumnExpr("COALESCE(MAX(seq), 0)").Where("run_id = ?", runID).Scan(ctx, &seq)
	return seq, err
}
func (d *SessionDAO) InsertCapabilityEvent(ctx context.Context, executor bun.IDB, row *SessionCapabilityEventRecord) error {
	_, err := executor.NewInsert().Model(row).ExcludeColumn("seq").Exec(ctx)
	return err
}
func (d *SessionDAO) ListCapabilityEvents(ctx context.Context, sessionID string) ([]SessionCapabilityEventRecord, error) {
	var rows []SessionCapabilityEventRecord
	err := d.db.NewSelect().Model(&rows).Where("session_id = ?", sessionID).OrderExpr("seq ASC").Scan(ctx)
	return rows, err
}
func (d *SessionDAO) Messages(ctx context.Context, sessionID string) ([]EntryRecord, error) {
	var rows []EntryRecord
	err := d.db.NewSelect().Table("entries").Column("seq", "type", "data").Where("session_id = ? AND type IN (?, ?, ?)", sessionID, "message", "compaction", "content_override").OrderExpr("seq ASC").Scan(ctx, &rows)
	return rows, err
}
func (d *SessionDAO) SimpleEntries(ctx context.Context, sessionID string) ([]EntryRecord, error) {
	var rows []EntryRecord
	err := d.db.NewSelect().Table("entries").Column("seq", "data").Where("session_id = ?", sessionID).OrderExpr("seq ASC").Scan(ctx, &rows)
	return rows, err
}
func (d *SessionDAO) MessagesAfter(ctx context.Context, sessionID string, after int64, limit int) ([]EntryRecord, error) {
	var rows []EntryRecord
	q := d.db.NewSelect().Table("entries").Column("seq", "data").Where("session_id = ? AND type = ? AND seq > ?", sessionID, "message", after).OrderExpr("seq ASC").Limit(limit)
	return rows, q.Scan(ctx, &rows)
}
func (d *SessionDAO) MessagesLatest(ctx context.Context, sessionID string, limit int) ([]EntryRecord, error) {
	var rows []EntryRecord
	q := d.db.NewSelect().Table("entries").Column("seq", "data").Where("session_id = ? AND type = ?", sessionID, "message").OrderExpr("seq DESC").Limit(limit)
	return rows, q.Scan(ctx, &rows)
}
func (d *SessionDAO) MessagesBefore(ctx context.Context, sessionID string, before int64, limit int) ([]EntryRecord, error) {
	var rows []EntryRecord
	q := d.db.NewSelect().Table("entries").Column("seq", "data").Where("session_id = ? AND type = ? AND seq < ?", sessionID, "message", before).OrderExpr("seq DESC").Limit(limit)
	return rows, q.Scan(ctx, &rows)
}
func (d *SessionDAO) RunEventsAfter(ctx context.Context, sessionID string, after int64, limit int) ([]SessionRunEventRecord, error) {
	var rows []SessionRunEventRecord
	q := d.db.NewSelect().Model(&rows).Where("session_id = ? AND seq > ?", sessionID, after).OrderExpr("seq ASC")
	if limit > 0 {
		q.Limit(limit)
	}
	return rows, q.Scan(ctx)
}
func (d *SessionDAO) CapabilityEventsAfter(ctx context.Context, sessionID string, after int64, limit int) ([]SessionCapabilityEventRecord, error) {
	var rows []SessionCapabilityEventRecord
	q := d.db.NewSelect().Model(&rows).Where("session_id = ? AND seq > ?", sessionID, after).OrderExpr("seq ASC")
	if limit > 0 {
		q.Limit(limit)
	}
	return rows, q.Scan(ctx)
}
func IsNoRowsSession(err error) bool { return err == sql.ErrNoRows }
